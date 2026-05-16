package saga

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestCreateSagaWritesImmutableIntentGraphAndControl(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := NewStore(store, WithClock(fixedClock))

	docs, err := sagas.Create(ctx, sampleCreateRequest())
	if err != nil {
		t.Fatalf("create saga: %v", err)
	}
	if docs.Intent.Key != "sagas/saga_01JABC/intent.json" || docs.Graph.Key != "sagas/saga_01JABC/graph.json" || docs.Control.Key != "sagas/saga_01JABC/control.json" {
		t.Fatalf("unexpected keys: %+v", docs)
	}
	if docs.Intent.Intent.Risk != schema.RiskHigh || docs.Intent.Intent.Reversibility != schema.Compensatable {
		t.Fatalf("risk/reversibility not preserved: %+v", docs.Intent.Intent)
	}
	if docs.Graph.Graph.Nodes[1].Requires[0] != "preflight" || docs.Graph.Graph.Nodes[1].Risk != schema.RiskHigh {
		t.Fatalf("graph node fields not preserved: %+v", docs.Graph.Graph.Nodes)
	}

	_, err = sagas.Create(ctx, sampleCreateRequest())
	if !errors.Is(err, objstore.ErrAlreadyExists) {
		t.Fatalf("duplicate create error = %v, want ErrAlreadyExists", err)
	}
}

func TestSagaControlCASRejectsStaleETag(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := NewStore(store, WithClock(fixedClock))
	if _, err := sagas.Create(ctx, sampleCreateRequest()); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	current, err := sagas.GetControl(ctx, "saga_01JABC")
	if err != nil {
		t.Fatalf("get control: %v", err)
	}
	next := current.Control
	next.Status = schema.SagaRunning
	next.CurrentSteps = []string{"preflight"}
	updated, err := sagas.UpdateControlCAS(ctx, current, next)
	if err != nil {
		t.Fatalf("update control: %v", err)
	}
	if updated.Control.Status != schema.SagaRunning || updated.Control.CurrentSteps[0] != "preflight" {
		t.Fatalf("unexpected updated control: %+v", updated.Control)
	}
	_, err = sagas.UpdateControlCAS(ctx, current, next)
	if !errors.Is(err, state.ErrPreconditionFailed) {
		t.Fatalf("stale update error = %v, want ErrPreconditionFailed", err)
	}
}

func TestSagaEventsAndStepResultsAreAppendOnly(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := NewStore(store, WithClock(fixedClock))
	if _, err := sagas.Create(ctx, sampleCreateRequest()); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	event := schema.Event{
		ID:      "01JEVT",
		Type:    "saga.started",
		Summary: "saga started",
	}
	doc, err := sagas.AppendEvent(ctx, "saga_01JABC", event)
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
	if doc.Event.Subject.Kind != "saga" || doc.Event.Subject.Name != "saga_01JABC" || doc.Key != "sagas/saga_01JABC/events/01JEVT.json" {
		t.Fatalf("event not normalized: %+v", doc)
	}
	if _, err := sagas.AppendEvent(ctx, "saga_01JABC", event); !errors.Is(err, objstore.ErrAlreadyExists) {
		t.Fatalf("duplicate event error = %v, want ErrAlreadyExists", err)
	}

	result := schema.StepResult{
		SagaID:      "saga_01JABC",
		StepID:      "preflight",
		Kind:        "check.preflight",
		Status:      "succeeded",
		Result:      json.RawMessage(`{"object_state":"ok"}`),
		CompletedAt: "2026-05-16T23:50:00Z",
	}
	resultDoc, err := sagas.CreateStepResult(ctx, result)
	if err != nil {
		t.Fatalf("create step result: %v", err)
	}
	if resultDoc.Key != "sagas/saga_01JABC/artifacts/results/preflight.json" {
		t.Fatalf("step result key = %q", resultDoc.Key)
	}
	if _, err := sagas.CreateStepResult(ctx, result); !errors.Is(err, objstore.ErrAlreadyExists) {
		t.Fatalf("duplicate result error = %v, want ErrAlreadyExists", err)
	}
}

func TestInspectCombinesIntentGraphAndControl(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	sagas := NewStore(store, WithClock(fixedClock))
	if _, err := sagas.Create(ctx, sampleCreateRequest()); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	current, err := sagas.GetControl(ctx, "saga_01JABC")
	if err != nil {
		t.Fatalf("get control: %v", err)
	}
	next := current.Control
	next.Status = schema.SagaRunning
	next.CurrentSteps = []string{"shift-traffic"}
	next.TraceID = "tr_control"
	if _, err := sagas.UpdateControlCAS(ctx, current, next); err != nil {
		t.Fatalf("update control: %v", err)
	}

	result, err := sagas.Inspect(ctx, "saga_01JABC")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if result.SagaID != "saga_01JABC" || result.Kind != "canary" || result.Status != schema.SagaRunning {
		t.Fatalf("unexpected inspect result: %+v", result)
	}
	if result.Risk != schema.RiskHigh || result.Reversibility != schema.Compensatable || result.CurrentSteps[0] != "shift-traffic" {
		t.Fatalf("missing summary fields: %+v", result)
	}
	if len(result.Nodes) != 2 || result.Nodes[1].Requires[0] != "preflight" {
		t.Fatalf("unexpected node summaries: %+v", result.Nodes)
	}
	if result.Paths["events"] != "sagas/saga_01JABC/events/" {
		t.Fatalf("missing event path: %+v", result.Paths)
	}
}

func TestSagaSchemaCanonicalRoundTrip(t *testing.T) {
	req := sampleCreateRequest()
	body, err := canonical.Marshal(req.Graph)
	if err != nil {
		t.Fatalf("marshal graph: %v", err)
	}
	const want = `{"schema_version":"skiff.state/v1","saga_id":"saga_01JABC","nodes":[{"id":"preflight","kind":"check.preflight","risk":"low","reversibility":"reversible"},{"id":"shift-traffic","kind":"aws.shift_traffic","requires":["preflight"],"retry":{"max_attempts":3,"backoff":"exponential"},"compensate":{"kind":"aws.shift_traffic","params":{"weight":0}},"risk":"high","reversibility":"compensatable"}],"edges":[{"from":"preflight","to":"shift-traffic"}],"created_at":"2026-05-16T23:50:00Z"}`
	if strings.TrimSpace(string(body)) != want {
		t.Fatalf("canonical graph = %s\nwant %s", body, want)
	}
	var roundTrip schema.SagaGraph
	if err := canonical.UnmarshalStrict(body, &roundTrip); err != nil {
		t.Fatalf("round trip graph: %v", err)
	}
	if roundTrip.Nodes[1].Compensate.Kind != "aws.shift_traffic" {
		t.Fatalf("unexpected round trip graph: %+v", roundTrip)
	}
}

func sampleCreateRequest() CreateRequest {
	return CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        "saga_01JABC",
			Kind:          "canary",
			Target:        schema.Target{Kind: "service", Name: "payments-api"},
			Actor:         schema.Actor{ID: "agent-one", Type: "agent"},
			TraceID:       "tr_saga",
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
			Summary:       "canary payments-api release",
			CreatedAt:     "2026-05-16T23:50:00Z",
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        "saga_01JABC",
			Nodes: []schema.SagaNode{
				{ID: "preflight", Kind: "check.preflight", Risk: schema.RiskLow, Reversibility: schema.Reversible},
				{
					ID:            "shift-traffic",
					Kind:          "aws.shift_traffic",
					Requires:      []string{"preflight"},
					Retry:         &schema.RetryPolicy{MaxAttempts: 3, Backoff: "exponential"},
					Compensate:    &schema.CompensationSpec{Kind: "aws.shift_traffic", Params: json.RawMessage(`{"weight":0}`)},
					Risk:          schema.RiskHigh,
					Reversibility: schema.Compensatable,
				},
			},
			Edges:     []schema.SagaEdge{{From: "preflight", To: "shift-traffic"}},
			CreatedAt: "2026-05-16T23:50:00Z",
		},
		Control: schema.SagaControl{
			SchemaVersion: schema.Version,
			SagaID:        "saga_01JABC",
			Status:        schema.SagaPending,
			UpdatedAt:     "2026-05-16T23:50:00Z",
			TraceID:       "tr_saga",
		},
	}
}

func fixedClock() time.Time {
	return time.Date(2026, 5, 16, 23, 50, 0, 0, time.UTC)
}
