package stateful_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestStatefulApplyWritesControlsOperationEventsAndAudit(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	graph := compileStatefulGraph(t)
	result, err := statefulApplier(store).Apply(ctx, graph, deploy.StatefulRequest{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_stateful_apply",
		OperationID: "op_stateful_apply",
	})
	if err != nil {
		t.Fatalf("stateful apply: %v", err)
	}
	if !result.OK || result.Group != "orders-stream" || result.Risk != schema.RiskMedium || result.Reversibility != schema.Compensatable {
		t.Fatalf("unexpected stateful apply result: %+v", result)
	}
	if result.GroupControl == nil || result.GroupControl.Operation == nil || result.GroupControl.Operation.State != string(schema.OperationSucceeded) {
		t.Fatalf("missing successful group operation: %+v", result.GroupControl)
	}
	if len(result.MemberControls) != 3 || result.MemberControls[0].DNSName == "" || result.MemberControls[0].Phase != state.StatefulMemberReady {
		t.Fatalf("unexpected member controls: %+v", result.MemberControls)
	}
	intent := readObject[schema.OperationIntent](t, store, mustOperationIntentKey(t, "orders-stream", "op_stateful_apply"))
	if intent.Target.Kind != "stateful-group" || intent.Risk != schema.RiskMedium || intent.Reversibility != schema.Compensatable {
		t.Fatalf("operation intent missing stateful safety fields: %+v", intent)
	}
	control := readObject[schema.OperationControl](t, store, mustOperationControlKey(t, "orders-stream", "op_stateful_apply"))
	if control.Status != schema.OperationSucceeded || len(control.ProviderOperations) == 0 || len(control.StepResults) != 1 {
		t.Fatalf("operation control missing provider IDs/step result: %+v", control)
	}
	group := readObject[schema.StatefulGroupControl](t, store, "stateful/orders-stream/control.json")
	if group.Replicas != 3 || len(group.Members) != 3 || group.Members[0].DNSName == "" {
		t.Fatalf("group control not persisted: %+v", group)
	}
	member := readObject[schema.StatefulMemberControl](t, store, "stateful/orders-stream/members/0/control.json")
	if member.Zone != "us-west-2a" || member.DNSName == "" || member.Generation != 1 {
		t.Fatalf("member control not persisted: %+v", member)
	}
	log, err := events.NewLog(events.Options{Store: store, Clock: fixedStatefulNow})
	if err != nil {
		t.Fatal(err)
	}
	operationEvents, err := log.List(ctx, events.Scope{Kind: events.ScopeOperation, Service: "orders-stream", Operation: "op_stateful_apply"}, events.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(operationEvents) < 3 {
		t.Fatalf("operation events = %d, want at least 3", len(operationEvents))
	}
	audits, err := store.List(ctx, "audit/", objstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 2 {
		t.Fatalf("audit records = %d, want 2", len(audits))
	}
	inspect, err := statefulApplier(store).Inspect(ctx, "orders-stream", "", "tr_inspect")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if inspect.OperationID != "op_stateful_apply" || inspect.Status != schema.OperationSucceeded || len(inspect.MemberControls) != 3 {
		t.Fatalf("inspect did not recover direct state: %+v", inspect)
	}
}

func TestStatefulApplyIsIdempotentWithNewOperation(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	graph := compileStatefulGraph(t)
	first, err := statefulApplier(store).Apply(ctx, graph, deploy.StatefulRequest{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_stateful_first",
		OperationID: "op_stateful_first",
	})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	second, err := statefulApplier(store).Apply(ctx, graph, deploy.StatefulRequest{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_stateful_second",
		OperationID: "op_stateful_second",
	})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if second.GroupControl.Version <= first.GroupControl.Version || len(second.MemberControls) != len(first.MemberControls) {
		t.Fatalf("re-apply did not CAS-update controls: first=%+v second=%+v", first.GroupControl, second.GroupControl)
	}
	audits, err := store.List(ctx, "audit/", objstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 4 {
		t.Fatalf("audit records = %d, want 4", len(audits))
	}
}

func TestStatefulApplyMarksOperationFailedForMalformedControl(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	if _, err := store.Create(ctx, "stateful/orders-stream/control.json", []byte(`{"schema_version":"skiff.state/v1","group":"orders-stream"}`), objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
	_, err := statefulApplier(store).Apply(ctx, compileStatefulGraph(t), deploy.StatefulRequest{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_stateful_bad_control",
		OperationID: "op_stateful_bad_control",
	})
	if err == nil {
		t.Fatalf("stateful apply succeeded with malformed control")
	}
	control := readObject[schema.OperationControl](t, store, mustOperationControlKey(t, "orders-stream", "op_stateful_bad_control"))
	if control.Status != schema.OperationFailed || len(control.StepResults) != 1 || control.StepResults[0].Failure == nil {
		t.Fatalf("operation control was not marked failed: %+v", control)
	}
	audits, err := store.List(ctx, "audit/", objstore.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("audit records = %d, want start audit only", len(audits))
	}
}

func TestStatefulApplySurfacesCASConflict(t *testing.T) {
	ctx := context.Background()
	base := memory.New()
	store := &casConflictStore{ObjectStore: base, key: "stateful/orders-stream/control.json", remaining: 1}
	_, err := statefulApplier(store).Apply(ctx, compileStatefulGraph(t), deploy.StatefulRequest{
		Actor:       schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:     "tr_stateful_cas",
		OperationID: "op_stateful_cas",
	})
	if !errors.Is(err, state.ErrPreconditionFailed) {
		t.Fatalf("stateful apply err = %v, want state.ErrPreconditionFailed", err)
	}
	control := readObject[schema.OperationControl](t, base, mustOperationControlKey(t, "orders-stream", "op_stateful_cas"))
	if control.Status != schema.OperationFailed {
		t.Fatalf("operation control status = %s, want failed", control.Status)
	}
}

type casConflictStore struct {
	objstore.ObjectStore
	key       string
	remaining int
}

func (s *casConflictStore) CompareAndSwap(ctx context.Context, key string, previousETag string, body []byte, opts objstore.PutOptions) (*objstore.ObjectMeta, error) {
	if key == s.key && s.remaining > 0 {
		s.remaining--
		return nil, objstore.WrapError("compare-and-swap", key, objstore.ErrPreconditionFailed)
	}
	return s.ObjectStore.CompareAndSwap(ctx, key, previousETag, body, opts)
}

func statefulApplier(store objstore.ObjectStore) deploy.StatefulApplier {
	return deploy.StatefulApplier{
		Store:    store,
		Provider: fakeprovider.New(fakeprovider.WithStateStore(store), fakeprovider.WithClock(fixedStatefulNow)),
		Clock:    fixedStatefulNow,
	}
}

func compileStatefulGraph(t *testing.T) *ir.Graph {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "stateful", "jetstream", "skiff.yaml")
	doc, err := spec.LoadFile(path, spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("load StatefulGroup spec: %v", err)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile StatefulGroup spec: %v", err)
	}
	return graph
}

func readObject[T any](t *testing.T, store objstore.ObjectStore, key string) T {
	t.Helper()
	obj, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	var value T
	if err := canonical.UnmarshalStrict(obj.Body, &value); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	return value
}

func mustOperationIntentKey(t *testing.T, service, operation string) string {
	t.Helper()
	key, err := paths.OperationIntent(service, operation)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustOperationControlKey(t *testing.T, service, operation string) string {
	t.Helper()
	key, err := paths.OperationControl(service, operation)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func fixedStatefulNow() time.Time {
	return time.Date(2026, 5, 18, 2, 0, 0, 0, time.UTC)
}
