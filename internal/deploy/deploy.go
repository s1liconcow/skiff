package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/buildinfo"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Deployer struct {
	Store      objstore.ObjectStore
	Provider   provider.Provider
	Signer     signing.Signer
	Clock      func() time.Time
	Authorizer authz.Authorizer
}

type Request struct {
	Actor         schema.Actor  `json:"actor"`
	TraceID       string        `json:"trace_id,omitempty"`
	ReleaseID     string        `json:"release_id,omitempty"`
	OperationID   string        `json:"operation_id,omitempty"`
	ApprovalID    string        `json:"approval_id,omitempty"`
	DryRun        bool          `json:"dry_run,omitempty"`
	PlanOnly      bool          `json:"plan_only,omitempty"`
	Shadow        bool          `json:"shadow,omitempty"`
	LeaseDuration time.Duration `json:"lease_duration,omitempty"`
}

type Result struct {
	OK              bool                    `json:"ok"`
	DryRun          bool                    `json:"dry_run,omitempty"`
	PlanOnly        bool                    `json:"plan_only,omitempty"`
	Shadow          bool                    `json:"shadow,omitempty"`
	OperationID     string                  `json:"operation_id,omitempty"`
	ReleaseID       string                  `json:"release_id,omitempty"`
	TraceID         string                  `json:"trace_id,omitempty"`
	Plan            *provider.Plan          `json:"plan,omitempty"`
	RuntimeManifest *schema.RuntimeManifest `json:"runtime_manifest,omitempty"`
	ReleaseManifest *schema.ReleaseManifest `json:"release_manifest,omitempty"`
	ServiceControl  *schema.ServiceControl  `json:"service_control,omitempty"`
	Events          []events.Event          `json:"events,omitempty"`
	NextCommands    []string                `json:"next_commands,omitempty"`
}

type PublishReleaseResult struct {
	OK              bool                    `json:"ok"`
	OperationID     string                  `json:"operation_id,omitempty"`
	ReleaseID       string                  `json:"release_id,omitempty"`
	TraceID         string                  `json:"trace_id,omitempty"`
	RuntimeManifest *schema.RuntimeManifest `json:"runtime_manifest,omitempty"`
	ReleaseManifest *schema.ReleaseManifest `json:"release_manifest,omitempty"`
	Events          []events.Event          `json:"events,omitempty"`
}

func (d Deployer) Deploy(ctx context.Context, graph *ir.Graph, req Request) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, fmt.Errorf("graph is required")
	}
	if d.Provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	if d.Store == nil && !req.DryRun && !req.PlanOnly {
		return nil, fmt.Errorf("object store is required")
	}
	now := d.now()
	req = normalizeRequest(req, graph, now)
	plan, err := d.Provider.Plan(ctx, graph)
	if err != nil {
		return nil, err
	}
	result := &Result{
		OK:          true,
		DryRun:      req.DryRun,
		PlanOnly:    req.PlanOnly,
		Shadow:      req.Shadow,
		OperationID: req.OperationID,
		ReleaseID:   req.ReleaseID,
		TraceID:     req.TraceID,
		Plan:        plan,
		NextCommands: []string{
			fmt.Sprintf("skiff status %s --format json --trace-id %s", graph.Service, req.TraceID),
			fmt.Sprintf("skiff ops events %s --service %s --format json --trace-id %s", req.OperationID, graph.Service, req.TraceID),
		},
	}
	if req.DryRun || req.PlanOnly {
		return result, nil
	}
	if _, err := d.authorize(ctx, authz.Request{
		Actor:      req.Actor,
		Action:     authz.ActionDeploy,
		Target:     schema.Target{Kind: "service", Name: graph.Service},
		Env:        graph.Env,
		Service:    graph.Service,
		Risk:       schema.RiskMedium,
		ApprovalID: req.ApprovalID,
		TraceID:    req.TraceID,
	}); err != nil {
		result.OK = false
		return result, err
	}
	if d.Signer == nil {
		return nil, fmt.Errorf("signer is required for deploy")
	}

	log, err := events.NewLog(events.Options{Store: d.Store, Clock: d.now})
	if err != nil {
		return nil, err
	}
	stateClient := state.NewClient(d.Store, state.WithClock(clockFunc(d.now)))
	if err := d.createOperationIntent(ctx, graph, req, now); err != nil {
		return nil, err
	}
	if err := d.ensureServiceControl(ctx, stateClient, graph, req, now); err != nil {
		return nil, err
	}
	handle, _, err := stateClient.AcquireLease(ctx, graph.Service, state.LeaseOptions{
		Owner:    req.Actor.ID,
		Duration: req.LeaseDuration,
		Actor:    req.Actor,
		TraceID:  req.TraceID,
	})
	if err != nil {
		return nil, err
	}
	leaseHeld := true
	defer func() {
		if leaseHeld {
			_, _ = stateClient.ReleaseLease(context.Background(), *handle)
		}
	}()

	if err := d.createOperationControl(ctx, graph, req, schema.OperationRunning, now, nil); err != nil {
		return nil, err
	}
	appendEvent := func(eventType, summary string, facts ...schema.Fact) {
		event := events.NewOperationEvent(graph.Service, req.OperationID, eventType, summary, d.now(), req.TraceID+eventType)
		event.TraceID = req.TraceID
		event.Actor = &req.Actor
		event.Facts = facts
		if _, err := log.Append(ctx, event); err == nil {
			result.Events = append(result.Events, event)
		}
	}
	fail := func(err error, step string) (*Result, error) {
		result.OK = false
		appendEvent("deploy.failed", err.Error(), schema.Fact{Type: "step", Message: step})
		_ = d.updateOperationControl(ctx, graph, req, schema.OperationFailed, []schema.StepResultRef{{
			StepID:      step,
			Kind:        "deploy",
			Status:      "failed",
			CompletedAt: canonical.Time(d.now()),
		}})
		return result, err
	}

	appendEvent("deploy.started", "deploy operation started")
	runtimeManifest, releaseManifest, err := d.publishRelease(ctx, graph, req, now)
	if err != nil {
		return fail(err, "publish_release")
	}
	result.RuntimeManifest = runtimeManifest
	result.ReleaseManifest = releaseManifest
	appendEvent("deploy.release_published", "signed release and runtime manifest objects published")

	applyResult, err := d.Provider.Apply(ctx, plan)
	if err != nil {
		return fail(err, "apply_infrastructure")
	}
	appendEvent("deploy.infrastructure_applied", "provider infrastructure plan applied", schema.Fact{Type: "resources", Message: fmt.Sprintf("%d resource(s)", len(applyResult.ResourceIDs))})

	handle, controlDoc, err := stateClient.UpdateServiceControlWithLeaseCAS(ctx, *handle, func(control *schema.ServiceControl) error {
		control.DesiredRelease = req.ReleaseID
		control.Operation = &schema.ActiveOperation{ID: req.OperationID, Kind: "deploy", State: string(schema.OperationSucceeded)}
		control.UpdatedBy = req.Actor
		control.TraceID = req.TraceID
		return nil
	})
	if err != nil {
		return fail(err, "update_desired_release")
	}
	result.ServiceControl = &controlDoc.Control
	appendEvent("deploy.desired_release_updated", "service desired release updated", schema.Fact{Type: "release", Message: req.ReleaseID})

	if err := d.updateOperationControl(ctx, graph, req, schema.OperationSucceeded, []schema.StepResultRef{{
		StepID:      "deploy",
		Kind:        "deploy",
		Status:      "succeeded",
		CompletedAt: canonical.Time(d.now()),
	}}); err != nil {
		return fail(err, "complete_operation")
	}
	appendEvent("deploy.succeeded", "deploy operation completed")
	audit := events.NewAuditRecord(req.Actor, schema.Target{Kind: "service", Name: graph.Service}, "deploy", "updated desired release to "+req.ReleaseID, req.TraceID, d.now(), req.OperationID)
	audit.Risk = schema.RiskMedium
	audit.ApprovalID = req.ApprovalID
	audit.AfterSummary = "desired release " + req.ReleaseID
	audit.Data = rawJSON(map[string]string{"operation_id": req.OperationID, "release_id": req.ReleaseID})
	_, _ = log.AppendAudit(ctx, audit)

	if _, err := stateClient.ReleaseLease(ctx, *handle); err != nil {
		return fail(err, "release_lease")
	}
	leaseHeld = false
	return result, nil
}

func (d Deployer) PublishRelease(ctx context.Context, graph *ir.Graph, req Request) (*PublishReleaseResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, fmt.Errorf("graph is required")
	}
	if d.Store == nil {
		return nil, fmt.Errorf("object store is required")
	}
	if d.Signer == nil {
		return nil, fmt.Errorf("signer is required for release publish")
	}
	now := d.now()
	req = normalizeRequest(req, graph, now)
	if _, err := d.authorize(ctx, authz.Request{
		Actor:      req.Actor,
		Action:     authz.ActionDeploy,
		Target:     schema.Target{Kind: "service", Name: graph.Service},
		Env:        graph.Env,
		Service:    graph.Service,
		Risk:       schema.RiskLow,
		ApprovalID: req.ApprovalID,
		TraceID:    req.TraceID,
	}); err != nil {
		return nil, err
	}
	runtimeManifest, releaseManifest, err := d.publishRelease(ctx, graph, req, now)
	if err != nil {
		return nil, err
	}
	result := &PublishReleaseResult{
		OK:              true,
		OperationID:     req.OperationID,
		ReleaseID:       req.ReleaseID,
		TraceID:         req.TraceID,
		RuntimeManifest: runtimeManifest,
		ReleaseManifest: releaseManifest,
	}
	log, err := events.NewLog(events.Options{Store: d.Store, Clock: d.now})
	if err != nil {
		return nil, err
	}
	event := events.NewOperationEvent(graph.Service, req.OperationID, "deploy.release_published", "signed release and runtime manifest objects published", d.now(), req.TraceID+"canary-release-published")
	event.TraceID = req.TraceID
	event.Actor = &req.Actor
	event.Facts = []schema.Fact{{Type: "release", Message: req.ReleaseID}}
	if _, err := log.Append(ctx, event); err == nil {
		result.Events = append(result.Events, event)
	}
	audit := events.NewAuditRecord(req.Actor, schema.Target{Kind: "service", Name: graph.Service}, "publish_release", "published signed release "+req.ReleaseID, req.TraceID, d.now(), req.OperationID+"publish-release")
	audit.Risk = schema.RiskLow
	audit.ApprovalID = req.ApprovalID
	audit.AfterSummary = "release " + req.ReleaseID + " published"
	audit.Data = rawJSON(map[string]string{"operation_id": req.OperationID, "release_id": req.ReleaseID})
	_, _ = log.AppendAudit(ctx, audit)
	return result, nil
}

func (d Deployer) createOperationIntent(ctx context.Context, graph *ir.Graph, req Request, now time.Time) error {
	intent := schema.NewOperationIntent(req.OperationID, graph.Service, graph.Env, "deploy", schema.Target{Kind: "service", Name: graph.Service}, req.Actor, req.TraceID, canonical.Time(now))
	intent.Risk = schema.RiskMedium
	intent.Reversibility = schema.Compensatable
	intent.Summary = "deploy " + graph.Service + " release " + req.ReleaseID
	intent.Params = rawJSON(map[string]string{"release_id": req.ReleaseID})
	body, err := canonical.Marshal(intent)
	if err != nil {
		return err
	}
	key, err := paths.OperationIntent(graph.Service, req.OperationID)
	if err != nil {
		return err
	}
	_, err = d.Store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func (d Deployer) createOperationControl(ctx context.Context, graph *ir.Graph, req Request, status schema.OperationStatus, now time.Time, results []schema.StepResultRef) error {
	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   req.OperationID,
		Service:       graph.Service,
		Env:           graph.Env,
		Status:        status,
		StepResults:   results,
		UpdatedAt:     canonical.Time(now),
		TraceID:       req.TraceID,
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		return err
	}
	key, err := paths.OperationControl(graph.Service, req.OperationID)
	if err != nil {
		return err
	}
	_, err = d.Store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func (d Deployer) updateOperationControl(ctx context.Context, graph *ir.Graph, req Request, status schema.OperationStatus, results []schema.StepResultRef) error {
	key, err := paths.OperationControl(graph.Service, req.OperationID)
	if err != nil {
		return err
	}
	obj, err := d.Store.Get(ctx, key)
	if err != nil {
		return err
	}
	var control schema.OperationControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		return err
	}
	control.Status = status
	control.StepResults = results
	control.UpdatedAt = canonical.Time(d.now())
	body, err := canonical.Marshal(control)
	if err != nil {
		return err
	}
	_, err = d.Store.CompareAndSwap(ctx, key, obj.ETag, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func (d Deployer) ensureServiceControl(ctx context.Context, client *state.Client, graph *ir.Graph, req Request, now time.Time) error {
	_, err := client.GetServiceControl(ctx, graph.Service)
	if err == nil {
		return nil
	}
	if !errors.Is(err, objstore.ErrNotFound) {
		return err
	}
	control := schema.NewServiceControl(graph.Service, graph.Env, canonical.Time(now), req.Actor)
	control.TraceID = req.TraceID
	_, err = client.CreateServiceControl(ctx, control)
	if errors.Is(err, objstore.ErrAlreadyExists) {
		return nil
	}
	return err
}

func (d Deployer) publishRelease(ctx context.Context, graph *ir.Graph, req Request, now time.Time) (*schema.RuntimeManifest, *schema.ReleaseManifest, error) {
	compiled, err := releaseRuntimeSource(graph)
	if err != nil {
		return nil, nil, err
	}
	runtimeManifest := schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       graph.Service,
		Env:           graph.Env,
		ReleaseID:     req.ReleaseID,
		Command:       append([]string(nil), compiled.Command...),
		EnvVars:       cloneStringMap(compiled.Env),
		HealthCheck:   healthCheck(compiled.HealthCheck),
		Metrics:       metricsConfig(compiled.Metrics),
		CreatedAt:     canonical.Time(now),
	}
	runtimeDigest, err := release.RuntimeManifestDigest(runtimeManifest)
	if err != nil {
		return nil, nil, err
	}
	runtimeKey, err := paths.RuntimeManifest(graph.Service, req.ReleaseID)
	if err != nil {
		return nil, nil, err
	}
	releaseManifest := schema.ReleaseManifest{
		SchemaVersion:         schema.Version,
		Service:               graph.Service,
		Env:                   graph.Env,
		ReleaseID:             req.ReleaseID,
		Artifact:              artifactRef(compiled.Artifact),
		RuntimeManifestKey:    runtimeKey,
		RuntimeManifestDigest: runtimeDigest,
		MinRunnerVersion:      buildinfo.Version,
		CreatedAt:             canonical.Time(now),
	}
	signedManifest, err := release.SignManifest(ctx, releaseManifest, d.Signer, req.Actor, now)
	if err != nil {
		return nil, nil, err
	}
	runtimeBody, err := canonical.Marshal(runtimeManifest)
	if err != nil {
		return nil, nil, err
	}
	if _, err := d.Store.Create(ctx, runtimeKey, runtimeBody, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		return nil, nil, err
	}
	releaseBody, err := canonical.Marshal(signedManifest)
	if err != nil {
		return nil, nil, err
	}
	releaseKey, err := paths.ReleaseManifest(graph.Service, req.ReleaseID)
	if err != nil {
		return nil, nil, err
	}
	if _, err := d.Store.Create(ctx, releaseKey, releaseBody, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		return nil, nil, err
	}
	return &runtimeManifest, &signedManifest, nil
}

type runtimeSource struct {
	Artifact    ir.Artifact
	Command     []string
	Env         map[string]string
	HealthCheck ir.HealthCheck
	Metrics     ir.AppMetrics
}

func releaseRuntimeSource(graph *ir.Graph) (runtimeSource, error) {
	if graph == nil {
		return runtimeSource{}, fmt.Errorf("graph is required")
	}
	if len(graph.Resources.RuntimeManifests) > 0 {
		compiled := graph.Resources.RuntimeManifests[0]
		return runtimeSource{
			Artifact:    compiled.Artifact,
			Command:     append([]string(nil), compiled.Command...),
			Env:         cloneStringMap(compiled.Env),
			HealthCheck: compiled.HealthCheck,
			Metrics:     compiled.Metrics,
		}, nil
	}
	if len(graph.Resources.StatefulRecipes) > 0 {
		recipe := graph.Resources.StatefulRecipes[0]
		return runtimeSource{
			Artifact:    recipe.Artifact,
			Command:     append([]string(nil), recipe.Command...),
			Env:         cloneStringMap(recipe.Env),
			HealthCheck: recipe.HealthCheck,
			Metrics:     recipe.Metrics,
		}, nil
	}
	return runtimeSource{}, fmt.Errorf("compiled graph has no runtime manifest or stateful recipe runtime")
}

func (d Deployer) now() time.Time {
	if d.Clock != nil {
		return d.Clock().UTC()
	}
	return time.Now().UTC()
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time {
	return f()
}

func normalizeRequest(req Request, graph *ir.Graph, now time.Time) Request {
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(now, graph.Service+"deploy")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(now, req.TraceID)
	}
	if req.ReleaseID == "" {
		req.ReleaseID = "rel_" + events.NewID(now, req.TraceID)
	}
	if req.LeaseDuration <= 0 {
		req.LeaseDuration = 5 * time.Minute
	}
	return req
}

func artifactRef(artifact ir.Artifact) schema.ArtifactRef {
	return schema.ArtifactRef{
		Type:   artifact.Type,
		URI:    artifact.Ref,
		Digest: artifactDigest(artifact),
	}
}

func artifactDigest(artifact ir.Artifact) string {
	if artifact.Digest != "" {
		return artifact.Digest
	}
	for _, marker := range []string{"@sha256:", "sha256:"} {
		if idx := indexDigest(artifact.Ref, marker); idx >= 0 {
			return "sha256:" + artifact.Ref[idx+len(marker):]
		}
	}
	return ""
}

func indexDigest(value, marker string) int {
	for i := 0; i+len(marker) <= len(value); i++ {
		if value[i:i+len(marker)] == marker {
			return i
		}
	}
	return -1
}

func healthCheck(health ir.HealthCheck) *schema.HealthCheck {
	if health.Type == "" && health.Path == "" && len(health.Command) == 0 {
		return nil
	}
	return &schema.HealthCheck{
		Type:     health.Type,
		Path:     health.Path,
		Port:     health.Port,
		Command:  append([]string(nil), health.Command...),
		Interval: health.Interval,
		Timeout:  health.Timeout,
	}
}

func metricsConfig(metrics ir.AppMetrics) *schema.MetricsConfig {
	if !metrics.Enabled {
		return nil
	}
	return &schema.MetricsConfig{
		Enabled: true,
		Path:    metrics.Path,
		Port:    metrics.Port,
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func rawJSON(value any) json.RawMessage {
	body, _ := json.Marshal(value)
	return body
}
