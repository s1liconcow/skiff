package deploy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const RollbackPreviousStable = templates.PreviousStable

type RollbackRequest struct {
	Actor                schema.Actor  `json:"actor"`
	TraceID              string        `json:"trace_id,omitempty"`
	Service              string        `json:"service"`
	Env                  string        `json:"env"`
	TargetRelease        string        `json:"target_release,omitempty"`
	OperationID          string        `json:"operation_id,omitempty"`
	SagaID               string        `json:"saga_id,omitempty"`
	ApprovalID           string        `json:"approval_id,omitempty"`
	LeaseDuration        time.Duration `json:"lease_duration,omitempty"`
	NoWatch              bool          `json:"no_watch,omitempty"`
	MinHealthyPercentage int           `json:"min_healthy_percentage,omitempty"`
	InstanceWarmup       int           `json:"instance_warmup,omitempty"`
}

type RollbackResult struct {
	OK              bool                    `json:"ok"`
	Service         string                  `json:"service"`
	Env             string                  `json:"env"`
	OperationID     string                  `json:"operation_id"`
	SagaID          string                  `json:"saga_id,omitempty"`
	TraceID         string                  `json:"trace_id,omitempty"`
	Target          string                  `json:"target"`
	FromRelease     string                  `json:"from_release,omitempty"`
	ToRelease       string                  `json:"to_release"`
	ReleaseHistory  []string                `json:"release_history,omitempty"`
	ServiceControl  *schema.ServiceControl  `json:"service_control,omitempty"`
	Rollout         *provider.Rollout       `json:"rollout,omitempty"`
	RolloutStatus   *provider.RolloutStatus `json:"rollout_status,omitempty"`
	Events          []events.Event          `json:"events,omitempty"`
	NextCommands    []string                `json:"next_commands,omitempty"`
	RecommendedNote string                  `json:"recommended_note,omitempty"`
}

func (d Deployer) Rollback(ctx context.Context, req RollbackRequest) (*RollbackResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Store == nil {
		return nil, fmt.Errorf("object store is required")
	}
	if d.Provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	now := d.now()
	req = normalizeRollbackRequest(req, now)
	result := &RollbackResult{
		OK:          true,
		Service:     req.Service,
		Env:         req.Env,
		OperationID: req.OperationID,
		SagaID:      req.SagaID,
		TraceID:     req.TraceID,
		Target:      req.TargetRelease,
		NextCommands: []string{
			fmt.Sprintf("skiff status %s --format json --trace-id %s", req.Service, req.TraceID),
			fmt.Sprintf("skiff ops watch %s --operation %s --format json --trace-id %s", req.Service, req.OperationID, req.TraceID),
			fmt.Sprintf("skiff ops events %s --service %s --format json --trace-id %s", req.OperationID, req.Service, req.TraceID),
			fmt.Sprintf("skiff ops inspect %s --format json --trace-id %s", req.SagaID, req.TraceID),
		},
	}

	log, err := events.NewLog(events.Options{Store: d.Store, Clock: d.now})
	if err != nil {
		return nil, err
	}
	stateClient := state.NewClient(d.Store, state.WithClock(clockFunc(d.now)))
	serviceDoc, err := stateClient.GetServiceControl(ctx, req.Service)
	if err != nil {
		return result, err
	}
	result.ServiceControl = &serviceDoc.Control
	result.FromRelease = serviceDoc.Control.DesiredRelease
	toRelease, err := resolveRollbackTarget(serviceDoc.Control, req.TargetRelease)
	if err != nil {
		return result, err
	}
	result.ToRelease = toRelease
	result.ReleaseHistory, err = d.releaseHistory(ctx, req.Service)
	if err != nil {
		return result, err
	}
	if _, err := d.readReleaseManifest(ctx, req.Service, req.Env, toRelease); err != nil {
		return result, err
	}
	if _, err := d.authorize(ctx, authz.Request{
		Actor:      req.Actor,
		Action:     authz.ActionRollback,
		Target:     schema.Target{Kind: "service", Name: req.Service},
		Env:        req.Env,
		Service:    req.Service,
		Risk:       schema.RiskMedium,
		ApprovalID: req.ApprovalID,
		TraceID:    req.TraceID,
	}); err != nil {
		result.OK = false
		return result, err
	}

	sagaStore := sagastate.NewStore(d.Store, sagastate.WithClock(d.now))
	sagaDocs, err := d.createRollbackSaga(ctx, sagaStore, req, result.FromRelease, toRelease, now)
	if err != nil {
		return result, err
	}
	sagaControl := sagaDocs.Control
	if err := d.createRollbackOperationIntent(ctx, req, result.FromRelease, toRelease, now); err != nil {
		return result, err
	}

	var handle *state.LeaseHandle
	leaseHeld := false
	operationControlCreated := false
	desiredUpdated := false
	defer func() {
		if leaseHeld && handle != nil {
			_, _ = stateClient.ReleaseLease(context.Background(), *handle)
		}
	}()
	appendEvent := func(eventType, summary string, facts ...schema.Fact) {
		event := events.NewOperationEvent(req.Service, req.OperationID, eventType, summary, d.now(), req.TraceID+eventType)
		event.TraceID = req.TraceID
		event.Actor = &req.Actor
		event.Facts = facts
		if _, err := log.Append(ctx, event); err == nil {
			result.Events = append(result.Events, event)
		}
	}
	updateSaga := func(status schema.SagaStatus, current []string, refs []schema.StepResultRef) {
		if sagaControl == nil {
			return
		}
		next := sagaControl.Control
		next.Status = status
		next.CurrentSteps = append([]string(nil), current...)
		next.StepResults = refs
		next.UpdatedAt = canonical.Time(d.now())
		updated, err := sagaStore.UpdateControlCAS(ctx, sagaControl, next)
		if err == nil {
			sagaControl = updated
		}
	}
	baseRefs := []schema.StepResultRef{}
	fail := func(err error, step string) (*RollbackResult, error) {
		result.OK = false
		result.RecommendedNote = "inspect operation events and provider rollout status before retrying rollback"
		appendEvent("rollback.failed", err.Error(), schema.Fact{Type: "step", Message: step})
		if leaseHeld && handle != nil && desiredUpdated {
			if nextHandle, controlDoc, updateErr := stateClient.UpdateServiceControlWithLeaseCAS(ctx, *handle, func(control *schema.ServiceControl) error {
				control.Operation = &schema.ActiveOperation{ID: req.OperationID, Kind: "rollback", State: string(schema.OperationFailed), Step: step}
				control.UpdatedBy = req.Actor
				control.TraceID = req.TraceID
				return nil
			}); updateErr == nil {
				handle = nextHandle
				result.ServiceControl = &controlDoc.Control
			}
		}
		failureRef := rollbackStepResult(step, "rollback."+step, "failed", d.now(), map[string]string{"error": err.Error()})
		if operationControlCreated {
			_ = d.updateRollbackOperationControl(ctx, req, schema.OperationFailed, appendStepRefs(baseRefs, failureRef))
		}
		updateSaga(schema.SagaFailed, nil, appendStepRefs(baseRefs, failureRef))
		return result, err
	}

	baseRefs = []schema.StepResultRef{
		rollbackStepResult("resolve-target", "deployment.rollback.resolve_target", "succeeded", now, map[string]string{"target_release": toRelease}),
		rollbackStepResult("create-operation", "operation.intent.create", "succeeded", now, map[string]string{"operation_id": req.OperationID}),
	}
	updateSaga(schema.SagaRunning, []string{"acquire-service-lease"}, baseRefs)
	handle, _, err = stateClient.AcquireLease(ctx, req.Service, state.LeaseOptions{
		Owner:    req.Actor.ID,
		Duration: req.LeaseDuration,
		Actor:    req.Actor,
		TraceID:  req.TraceID,
	})
	if err != nil {
		return fail(err, "acquire-service-lease")
	}
	leaseHeld = true
	baseRefs = append(baseRefs, rollbackStepResult("acquire-service-lease", "service.lease.acquire", "succeeded", d.now(), map[string]string{"service": req.Service}))
	if err := d.createRollbackOperationControl(ctx, req, schema.OperationRunning, now, nil); err != nil {
		return fail(err, "create-operation-control")
	}
	operationControlCreated = true
	appendEvent("rollback.started", "rollback operation started", schema.Fact{Type: "from_release", Message: result.FromRelease}, schema.Fact{Type: "to_release", Message: toRelease})

	handle, controlDoc, err := stateClient.UpdateServiceControlWithLeaseCAS(ctx, *handle, func(control *schema.ServiceControl) error {
		control.DesiredRelease = toRelease
		control.Operation = &schema.ActiveOperation{ID: req.OperationID, Kind: "rollback", State: "rolling_out", Step: "start-asg-instance-refresh"}
		control.UpdatedBy = req.Actor
		control.TraceID = req.TraceID
		return nil
	})
	if err != nil {
		return fail(err, "update-desired-release")
	}
	result.ServiceControl = &controlDoc.Control
	desiredUpdated = true
	appendEvent("rollback.desired_release_updated", "service desired release updated for rollback", schema.Fact{Type: "release", Message: toRelease})

	rollout, err := d.StartRollout(ctx, StartRolloutRequest{
		Service:              req.Service,
		Env:                  req.Env,
		OperationID:          req.OperationID,
		ReleaseID:            toRelease,
		TraceID:              req.TraceID,
		Actor:                req.Actor,
		MinHealthyPercentage: req.MinHealthyPercentage,
		InstanceWarmup:       req.InstanceWarmup,
	})
	if err != nil {
		return fail(err, "start-asg-instance-refresh")
	}
	result.Rollout = rollout
	if req.NoWatch {
		refs := appendStepRefs(baseRefs,
			rollbackStepResult("update-desired-release", "service.desired_release.update", "succeeded", d.now(), map[string]string{"release": toRelease}),
			rollbackStepResult("start-asg-instance-refresh", "provider.aws.asg_instance_refresh.start", "succeeded", d.now(), map[string]string{"provider_id": rollout.ProviderID}),
		)
		_ = d.updateRollbackOperationControl(ctx, req, schema.OperationRunning, refs)
		updateSaga(schema.SagaRunning, []string{"watch-rollout-health"}, refs)
		if _, err := stateClient.ReleaseLease(ctx, *handle); err != nil {
			return fail(err, "release-lease")
		}
		leaseHeld = false
		return result, nil
	}

	status, err := d.WatchRollout(ctx, WatchRolloutRequest{
		Service:     req.Service,
		Env:         req.Env,
		OperationID: req.OperationID,
		RolloutID:   rollout.ID,
		ProviderID:  rollout.ProviderID,
		TraceID:     req.TraceID,
		Actor:       req.Actor,
	})
	if err != nil {
		return fail(err, "watch-rollout-health")
	}
	result.RolloutStatus = status
	switch operationStatusForRollout(status.Status) {
	case schema.OperationSucceeded:
		handle, controlDoc, err = stateClient.UpdateServiceControlWithLeaseCAS(ctx, *handle, func(control *schema.ServiceControl) error {
			control.DesiredRelease = toRelease
			control.StableRelease = toRelease
			control.Operation = &schema.ActiveOperation{ID: req.OperationID, Kind: "rollback", State: string(schema.OperationSucceeded)}
			control.UpdatedBy = req.Actor
			control.TraceID = req.TraceID
			return nil
		})
		if err != nil {
			return fail(err, "mark-complete")
		}
		result.ServiceControl = &controlDoc.Control
		refs := appendStepRefs(baseRefs,
			rollbackStepResult("update-desired-release", "service.desired_release.update", "succeeded", d.now(), map[string]string{"release": toRelease}),
			rollbackStepResult("start-asg-instance-refresh", "provider.aws.asg_instance_refresh.start", "succeeded", d.now(), map[string]string{"provider_id": rollout.ProviderID}),
			rollbackStepResult("watch-rollout-health", "provider.aws.asg_instance_refresh.watch", "succeeded", d.now(), map[string]string{"status": status.Status}),
			rollbackStepResult("mark-complete", "operation.rollback.complete", "succeeded", d.now(), map[string]string{"stable_release": toRelease}),
		)
		if err := d.updateRollbackOperationControl(ctx, req, schema.OperationSucceeded, refs); err != nil {
			return fail(err, "complete-operation")
		}
		updateSaga(schema.SagaSucceeded, nil, refs)
		appendEvent("rollback.succeeded", "rollback operation completed", schema.Fact{Type: "release", Message: toRelease})
		audit := events.NewAuditRecord(req.Actor, schema.Target{Kind: "service", Name: req.Service}, "rollback", "rolled back "+req.Service+" to "+toRelease, req.TraceID, d.now(), req.OperationID)
		audit.Risk = schema.RiskMedium
		audit.ApprovalID = req.ApprovalID
		audit.BeforeSummary = "desired release " + result.FromRelease
		audit.AfterSummary = "stable release " + toRelease
		audit.Data = rawJSON(map[string]string{"operation_id": req.OperationID, "saga_id": req.SagaID, "from_release": result.FromRelease, "to_release": toRelease})
		_, _ = log.AppendAudit(ctx, audit)
	case schema.OperationFailed, schema.OperationCanceled:
		return fail(fmt.Errorf("rollback rollout %s", status.Status), "watch-rollout-health")
	default:
		refs := appendStepRefs(baseRefs,
			rollbackStepResult("update-desired-release", "service.desired_release.update", "succeeded", d.now(), map[string]string{"release": toRelease}),
			rollbackStepResult("start-asg-instance-refresh", "provider.aws.asg_instance_refresh.start", "succeeded", d.now(), map[string]string{"provider_id": rollout.ProviderID}),
			rollbackStepResult("watch-rollout-health", "provider.aws.asg_instance_refresh.watch", status.Status, d.now(), map[string]string{"status": status.Status}),
		)
		_ = d.updateRollbackOperationControl(ctx, req, schema.OperationRunning, refs)
		updateSaga(schema.SagaRunning, []string{"watch-rollout-health"}, refs)
	}

	if _, err := stateClient.ReleaseLease(ctx, *handle); err != nil {
		return fail(err, "release-lease")
	}
	leaseHeld = false
	return result, nil
}

func normalizeRollbackRequest(req RollbackRequest, now time.Time) RollbackRequest {
	req.Service = strings.TrimSpace(req.Service)
	req.Env = strings.TrimSpace(req.Env)
	req.TargetRelease = strings.TrimSpace(req.TargetRelease)
	if req.TargetRelease == "" {
		req.TargetRelease = RollbackPreviousStable
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(now, req.Service+"rollback")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(now, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(now, req.OperationID)
	}
	if req.LeaseDuration <= 0 {
		req.LeaseDuration = 5 * time.Minute
	}
	return req
}

func resolveRollbackTarget(control schema.ServiceControl, target string) (string, error) {
	if strings.TrimSpace(control.DesiredRelease) == "" {
		return "", fmt.Errorf("service control does not contain desired_release")
	}
	if target == "" || target == RollbackPreviousStable {
		if strings.TrimSpace(control.StableRelease) == "" {
			return "", fmt.Errorf("service control does not contain stable_release for previous-stable rollback")
		}
		return control.StableRelease, nil
	}
	return target, nil
}

func (d Deployer) createRollbackSaga(ctx context.Context, store *sagastate.Store, req RollbackRequest, fromRelease, toRelease string, now time.Time) (*sagastate.Documents, error) {
	createReq, err := templates.DeploymentRollback(templates.RollbackRequest{
		SagaID:        req.SagaID,
		OperationID:   req.OperationID,
		Service:       req.Service,
		Env:           req.Env,
		FromRelease:   fromRelease,
		Target:        req.TargetRelease,
		TargetRelease: toRelease,
		TraceID:       req.TraceID,
		Actor:         req.Actor,
		CreatedAt:     now,
	})
	if err != nil {
		return nil, err
	}
	return store.Create(ctx, createReq)
}

func (d Deployer) createRollbackOperationIntent(ctx context.Context, req RollbackRequest, fromRelease, toRelease string, now time.Time) error {
	intent := schema.NewOperationIntent(req.OperationID, req.Service, req.Env, "rollback", schema.Target{Kind: "service", Name: req.Service}, req.Actor, req.TraceID, canonical.Time(now))
	intent.Risk = schema.RiskMedium
	intent.Reversibility = schema.Reversible
	intent.Summary = "rollback " + req.Service + " from " + fromRelease + " to " + toRelease
	intent.Params = rawJSON(map[string]string{"from_release": fromRelease, "to_release": toRelease, "saga_id": req.SagaID})
	body, err := canonical.Marshal(intent)
	if err != nil {
		return err
	}
	key, err := paths.OperationIntent(req.Service, req.OperationID)
	if err != nil {
		return err
	}
	_, err = d.Store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func (d Deployer) createRollbackOperationControl(ctx context.Context, req RollbackRequest, status schema.OperationStatus, now time.Time, results []schema.StepResultRef) error {
	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   req.OperationID,
		Service:       req.Service,
		Env:           req.Env,
		Status:        status,
		StepResults:   results,
		UpdatedAt:     canonical.Time(now),
		TraceID:       req.TraceID,
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		return err
	}
	key, err := paths.OperationControl(req.Service, req.OperationID)
	if err != nil {
		return err
	}
	_, err = d.Store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func (d Deployer) updateRollbackOperationControl(ctx context.Context, req RollbackRequest, status schema.OperationStatus, results []schema.StepResultRef) error {
	control, etag, err := d.getOperationControl(ctx, req.Service, req.OperationID)
	if err != nil {
		return err
	}
	control.Status = status
	control.StepResults = results
	control.UpdatedAt = canonical.Time(d.now())
	return d.putOperationControlCAS(ctx, req.Service, req.OperationID, etag, control)
}

func rollbackStepResult(stepID, kind, status string, completedAt time.Time, result any) schema.StepResultRef {
	return schema.StepResultRef{
		StepID:      stepID,
		Kind:        kind,
		Status:      status,
		Result:      rawJSON(result),
		CompletedAt: canonical.Time(completedAt),
	}
}

func appendStepRefs(base []schema.StepResultRef, refs ...schema.StepResultRef) []schema.StepResultRef {
	out := append([]schema.StepResultRef(nil), base...)
	out = append(out, refs...)
	return out
}

func (d Deployer) readReleaseManifest(ctx context.Context, service, env, releaseID string) (*schema.ReleaseManifest, error) {
	key, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		return nil, err
	}
	obj, err := d.Store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) {
			return nil, fmt.Errorf("release %s is not present in immutable release history", releaseID)
		}
		return nil, err
	}
	var manifest schema.ReleaseManifest
	if err := canonical.UnmarshalStrict(obj.Body, &manifest); err != nil {
		return nil, fmt.Errorf("decode release manifest %s: %w", key, err)
	}
	if manifest.Service != service {
		return nil, fmt.Errorf("release %s belongs to service %s, want %s", releaseID, manifest.Service, service)
	}
	if manifest.Env != env {
		return nil, fmt.Errorf("release %s belongs to env %s, want %s", releaseID, manifest.Env, env)
	}
	if manifest.ReleaseID != releaseID {
		return nil, fmt.Errorf("release manifest %s names release %s", key, manifest.ReleaseID)
	}
	return &manifest, nil
}

func (d Deployer) releaseHistory(ctx context.Context, service string) ([]string, error) {
	prefix, err := paths.ServiceReleasesPrefix(service)
	if err != nil {
		return nil, err
	}
	metas, err := d.Store.List(ctx, prefix, objstore.ListOptions{})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, meta := range metas {
		if !strings.HasSuffix(meta.Key, "/release.json") {
			continue
		}
		relPath := strings.TrimSuffix(strings.TrimPrefix(meta.Key, prefix), "/release.json")
		if relPath == "" || strings.Contains(relPath, "/") {
			continue
		}
		seen[relPath] = struct{}{}
	}
	releases := make([]string, 0, len(seen))
	for releaseID := range seen {
		releases = append(releases, releaseID)
	}
	sort.Strings(releases)
	return releases, nil
}
