package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const defaultOperationLeaseDuration = 30 * time.Second

type Resumer struct {
	Store    objstore.ObjectStore
	Provider provider.Provider
	Clock    func() time.Time
}

func (r Resumer) Resume(ctx context.Context, req ResumeRequest) (*ResumeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.Store == nil {
		return nil, errors.New("object store is required")
	}
	if r.Provider == nil {
		return nil, errors.New("provider is required")
	}
	req = normalizeResumeRequest(req, r.now())
	opStore := NewStore(r.Store, WithClock(r.now))
	inspect, err := opStore.Inspect(ctx, req.Service, req.OperationID)
	if err != nil {
		return nil, err
	}
	result := &ResumeResult{
		OK:                 true,
		OperationID:        inspect.OperationID,
		Service:            inspect.Service,
		Env:                inspect.Env,
		Kind:               inspect.Kind,
		TraceID:            firstNonEmpty(req.TraceID, inspect.TraceID),
		PreviousStatus:     inspect.Status,
		Status:             inspect.Status,
		ProviderOperations: append([]schema.ProviderOperationRef(nil), inspect.ProviderOperations...),
		NextCommands:       nextCommands(inspect.Service, inspect.OperationID, firstNonEmpty(req.TraceID, inspect.TraceID), sagaIDFromIntent(inspect.Intent)),
	}
	if terminalStatus(inspect.Status) {
		return result, nil
	}
	if len(inspect.ProviderOperations) == 0 {
		return result, fmt.Errorf("operation %s has no stored provider operation ID to resume", req.OperationID)
	}
	acquired, err := opStore.AcquireLease(ctx, inspect.Service, inspect.OperationID, LeaseOptions{Owner: req.Owner, Duration: req.LeaseDuration})
	if err != nil {
		return result, err
	}
	result.TookOver = acquired.TookOver
	release := true
	defer func() {
		if release && acquired.Handle != nil {
			_, _ = opStore.ReleaseLease(context.Background(), *acquired.Handle)
		}
	}()

	log, err := events.NewLog(events.Options{Store: r.Store, Clock: r.now})
	if err != nil {
		return result, err
	}
	if req.Takeover && acquired.TookOver {
		_ = appendTakeover(ctx, log, inspect.Service, inspect.OperationID, req.TraceID, req.Actor, acquired.PreviousLease, req.Owner, r.now())
	} else {
		_ = appendResumeEvent(ctx, log, inspect.Service, inspect.OperationID, req.TraceID, req.Actor, "operation.resume.started", "operation resume started", r.now())
	}

	rolloutRef := firstASGRollout(inspect.ProviderOperations)
	if rolloutRef.ID == "" {
		return result, fmt.Errorf("operation %s has no stored ASG instance refresh provider ID", req.OperationID)
	}
	status, err := (deploy.Deployer{Store: r.Store, Provider: r.Provider, Clock: r.now}).WatchRollout(ctx, deploy.WatchRolloutRequest{
		Service:     inspect.Service,
		Env:         inspect.Env,
		OperationID: inspect.OperationID,
		RolloutID:   inspect.OperationID,
		ProviderID:  rolloutRef.ID,
		TraceID:     result.TraceID,
		Actor:       req.Actor,
	})
	if err != nil {
		_ = appendResumeEvent(ctx, log, inspect.Service, inspect.OperationID, result.TraceID, req.Actor, "operation.resume.failed", err.Error(), r.now())
		return result, err
	}
	result.Resumed = true
	result.RolloutStatus = status
	if current, err := opStore.GetControl(ctx, inspect.Service, inspect.OperationID); err == nil {
		result.Status = current.Control.Status
		result.ProviderOperations = append([]schema.ProviderOperationRef(nil), current.Control.ProviderOperations...)
	}
	if result.Status == schema.OperationSucceeded && inspect.Kind == "rollback" {
		_ = completeRollbackSagaAfterResume(ctx, r.Store, inspect, status, r.now)
	}
	_ = appendResumeEvent(ctx, log, inspect.Service, inspect.OperationID, result.TraceID, req.Actor, "operation.resume.completed", "operation resume completed", r.now())
	return result, nil
}

func normalizeResumeRequest(req ResumeRequest, now time.Time) ResumeRequest {
	req.Service = strings.TrimSpace(req.Service)
	req.OperationID = strings.TrimSpace(req.OperationID)
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.Owner == "" {
		req.Owner = req.Actor.ID
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(now, req.Service+req.OperationID+"resume")
	}
	if req.LeaseDuration <= 0 {
		req.LeaseDuration = defaultOperationLeaseDuration
	}
	return req
}

func (r Resumer) now() time.Time {
	if r.Clock != nil {
		return r.Clock().UTC()
	}
	return time.Now().UTC()
}

func firstASGRollout(refs []schema.ProviderOperationRef) schema.ProviderOperationRef {
	for _, ref := range refs {
		if ref.Provider == aws.Name && ref.Kind == aws.RolloutKindASGInstanceRefresh && ref.ID != "" {
			return ref
		}
	}
	for _, ref := range refs {
		if ref.ID != "" {
			return ref
		}
	}
	return schema.ProviderOperationRef{}
}

func appendResumeEvent(ctx context.Context, log *events.Log, service, operationID, traceID string, actor schema.Actor, eventType, summary string, now time.Time, facts ...schema.Fact) error {
	event := events.NewOperationEvent(service, operationID, eventType, summary, now, traceID+eventType)
	event.TraceID = traceID
	event.Actor = &actor
	event.Facts = facts
	_, err := log.Append(ctx, event)
	return err
}

func appendTakeover(ctx context.Context, log *events.Log, service, operationID, traceID string, actor schema.Actor, previous *schema.Lease, owner string, now time.Time) error {
	facts := []schema.Fact{{Type: "new_owner", Message: owner}}
	if previous != nil {
		facts = append(facts,
			schema.Fact{Type: "previous_owner", Message: previous.Owner},
			schema.Fact{Type: "previous_expires_at", Message: previous.ExpiresAt},
		)
	}
	event := events.NewOperationEvent(service, operationID, "operation.takeover", "worker took over expired operation lease", now, traceID+"operation.takeover")
	event.TraceID = traceID
	event.Actor = &actor
	event.Facts = facts
	if _, err := log.Append(ctx, event); err != nil {
		return err
	}
	audit := events.NewAuditRecord(actor, schema.Target{Kind: "operation", Name: operationID}, "operation_takeover", "worker took over expired operation "+operationID, traceID, now, operationID+"takeover")
	audit.Risk = schema.RiskLow
	audit.Data = rawJSON(map[string]string{"service": service, "operation_id": operationID, "owner": owner})
	_, err := log.AppendAudit(ctx, audit)
	return err
}

func nextCommands(service, operationID, traceID, sagaID string) []string {
	commands := []string{
		fmt.Sprintf("skiff ops inspect %s --service %s --format json --trace-id %s", operationID, service, traceID),
		fmt.Sprintf("skiff ops watch %s --operation %s --format json --trace-id %s", service, operationID, traceID),
		fmt.Sprintf("skiff status %s --format json --trace-id %s", service, traceID),
	}
	if sagaID != "" {
		commands = append(commands, fmt.Sprintf("skiff ops inspect %s --format json --trace-id %s", sagaID, traceID))
	}
	return commands
}

func sagaIDFromIntent(intent *schema.OperationIntent) string {
	if intent == nil || len(intent.Params) == 0 {
		return ""
	}
	var params struct {
		SagaID string `json:"saga_id"`
	}
	if err := json.Unmarshal(intent.Params, &params); err != nil {
		return ""
	}
	return params.SagaID
}

func rawJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return body
}

func completeRollbackSagaAfterResume(ctx context.Context, store objstore.ObjectStore, inspect *InspectResult, status *provider.RolloutStatus, clock func() time.Time) error {
	sagaID := sagaIDFromIntent(inspect.Intent)
	if sagaID == "" || status == nil || status.Status != "succeeded" {
		return nil
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	sagas := sagastate.NewStore(store, sagastate.WithClock(clock))
	current, err := sagas.GetControl(ctx, sagaID)
	if err != nil {
		return err
	}
	if current.Control.Status == schema.SagaSucceeded || current.Control.Status == schema.SagaFailed || current.Control.Status == schema.SagaCanceled {
		return nil
	}
	handle, control, err := sagas.AcquireLease(ctx, sagaID, sagastate.LeaseOptions{Owner: "operation-resume", Duration: defaultOperationLeaseDuration})
	if err != nil {
		if errors.Is(err, state.ErrLeaseHeld) {
			return nil
		}
		return err
	}
	defer func() {
		_, _ = sagas.ReleaseLease(context.Background(), *handle)
	}()
	now := clock().UTC()
	refs := upsertSagaStepRef(control.Control.StepResults, schema.StepResultRef{
		StepID:      "watch-rollout-health",
		Kind:        "provider.aws.asg_instance_refresh.watch",
		Status:      "succeeded",
		Result:      rawJSON(map[string]string{"status": status.Status, "provider_id": status.ProviderID}),
		CompletedAt: canonical.Time(now),
	})
	refs = upsertSagaStepRef(refs, schema.StepResultRef{
		StepID:      "mark-complete",
		Kind:        "operation.rollback.complete",
		Status:      "succeeded",
		Result:      rawJSON(map[string]string{"operation_id": inspect.OperationID}),
		CompletedAt: canonical.Time(now),
	})
	nextHandle, _, err := sagas.UpdateControlWithLeaseCAS(ctx, *handle, func(control *schema.SagaControl) error {
		control.Status = schema.SagaSucceeded
		control.CurrentSteps = nil
		control.StepResults = refs
		return nil
	})
	if err != nil {
		return err
	}
	handle = nextHandle
	event := schema.Event{
		SchemaVersion: schema.Version,
		ID:            events.NewID(now, inspect.OperationID+"rollback-saga-resumed"),
		Time:          canonical.Time(now),
		TraceID:       inspect.TraceID,
		Subject:       schema.Target{Kind: "saga", Name: sagaID},
		Type:          "saga.resume.succeeded",
		Severity:      "info",
		Summary:       "rollback saga completed after operation resume",
		Facts: []schema.Fact{
			{Type: "operation_id", Message: inspect.OperationID},
			{Type: "rollout_status", Message: status.Status},
		},
	}
	_, err = sagas.AppendEvent(ctx, sagaID, event)
	return err
}

func upsertSagaStepRef(refs []schema.StepResultRef, next schema.StepResultRef) []schema.StepResultRef {
	out := refs[:0]
	for _, ref := range refs {
		if ref.StepID != next.StepID {
			out = append(out, ref)
		}
	}
	out = append(out, next)
	return out
}
