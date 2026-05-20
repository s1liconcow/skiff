package ops

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type ProfileOperationRequest struct {
	Env         string               `json:"env,omitempty"`
	OperationID string               `json:"operation_id"`
	Render      ProfileRenderRequest `json:"render"`
}

type ProfileOperationResult struct {
	OperationID string            `json:"operation_id"`
	SagaID      string            `json:"saga_id"`
	TraceID     string            `json:"trace_id,omitempty"`
	Paths       map[string]string `json:"paths,omitempty"`
}

func CreateProfileOperation(ctx context.Context, store objstore.ObjectStore, req ProfileOperationRequest) (*ProfileOperationResult, *ProfileRenderResult, error) {
	if store == nil {
		return nil, nil, errors.New("object store is required")
	}
	rendered, err := RenderProfile(req.Render)
	if err != nil {
		return nil, nil, err
	}
	req.Render.SagaID = rendered.Intent.SagaID
	req.Render.TraceID = rendered.Intent.TraceID
	now := req.Render.CreatedAt.UTC()
	if now.IsZero() {
		if parsed, err := time.Parse(time.RFC3339Nano, rendered.Intent.CreatedAt); err == nil {
			now = parsed.UTC()
		} else {
			now = time.Now().UTC()
		}
	}
	service := req.Render.Target.Name
	operationIntent := schema.NewOperationIntent(req.OperationID, service, req.Env, rendered.Explanation.Name, req.Render.Target, req.Render.Actor, rendered.Intent.TraceID, canonical.Time(now))
	operationIntent.Risk = rendered.Intent.Risk
	operationIntent.Reversibility = rendered.Intent.Reversibility
	operationIntent.PackageLockDigest = rendered.Intent.PackageLockDigest
	operationIntent.Summary = rendered.Intent.Summary
	operationIntent.Params = cloneRawMessage(rendered.Params)
	intentKey, err := paths.OperationIntent(service, req.OperationID)
	if err != nil {
		return nil, rendered, err
	}
	intentBody, err := canonical.Marshal(operationIntent)
	if err != nil {
		return nil, rendered, err
	}
	if _, err := store.Create(ctx, intentKey, intentBody, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		return nil, rendered, err
	}
	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   req.OperationID,
		Service:       service,
		Env:           req.Env,
		Status:        schema.OperationPending,
		UpdatedAt:     canonical.Time(now),
		TraceID:       rendered.Intent.TraceID,
	}
	controlKey, err := paths.OperationControl(service, req.OperationID)
	if err != nil {
		return nil, rendered, err
	}
	controlBody, err := canonical.Marshal(control)
	if err != nil {
		return nil, rendered, err
	}
	if _, err := store.Create(ctx, controlKey, controlBody, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		return nil, rendered, err
	}
	docs, _, err := CreateProfileSaga(ctx, store, req.Render)
	if err != nil {
		return nil, rendered, err
	}
	written := map[string]string{
		"operation_intent":  intentKey,
		"operation_control": controlKey,
		"saga_intent":       docs.Intent.Key,
		"saga_graph":        docs.Graph.Key,
		"saga_control":      docs.Control.Key,
	}
	log, err := events.NewLog(events.Options{Store: store, Clock: func() time.Time { return now }})
	if err != nil {
		return nil, rendered, err
	}
	audit := events.NewAuditRecord(req.Render.Actor, req.Render.Target, "operation_profile.run", "created operation profile saga "+req.Render.SagaID, rendered.Intent.TraceID, now, req.OperationID+req.Render.SagaID+"audit")
	audit.Risk = rendered.Intent.Risk
	audit.Data = profileOperationAuditData(req.OperationID, req.Render.SagaID, rendered.Explanation.Name, req.Render.Package)
	auditMeta, err := log.AppendAudit(ctx, audit)
	if err != nil {
		return nil, rendered, err
	}
	written["audit"] = auditMeta.Key
	event := events.NewOperationEvent(service, req.OperationID, "operation.profile.created", "created operation profile saga "+req.Render.SagaID, now, req.OperationID+req.Render.SagaID+"event")
	event.TraceID = rendered.Intent.TraceID
	event.Actor = &req.Render.Actor
	event.Facts = []schema.Fact{
		{Type: "saga_id", Message: req.Render.SagaID},
		{Type: "operation", Message: rendered.Explanation.Name},
	}
	eventMeta, err := log.Append(ctx, event)
	if err != nil {
		return nil, rendered, err
	}
	written["operation_event"] = eventMeta.Key
	return &ProfileOperationResult{OperationID: req.OperationID, SagaID: req.Render.SagaID, TraceID: rendered.Intent.TraceID, Paths: written}, rendered, nil
}

func profileOperationAuditData(operationID, sagaID, operation string, provenance schema.PackageProvenance) json.RawMessage {
	body, err := json.Marshal(map[string]any{
		"operation_id": operationID,
		"saga_id":      sagaID,
		"operation":    operation,
		"package":      provenance,
	})
	if err != nil {
		return nil
	}
	return body
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}
