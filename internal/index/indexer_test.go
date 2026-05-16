package index

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRebuildFromMemoryStoreIndexesDurableState(t *testing.T) {
	store := memory.New()
	createJSON(t, store, "services/payments-api/control.json", schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        "payments-api",
		Env:            "prod",
		DesiredRelease: "rel_02",
		StableRelease:  "rel_01",
		Version:        1,
		UpdatedAt:      "2026-05-16T21:00:00Z",
		UpdatedBy:      schema.Actor{ID: "agent-one", Type: "agent"},
	})
	createJSON(t, store, "sagas/saga_01JABC/control.json", schema.SagaControl{
		SchemaVersion: schema.Version,
		SagaID:        "saga_01JABC",
		Status:        schema.SagaRunning,
		CurrentSteps:  []string{"shift-traffic"},
		UpdatedAt:     "2026-05-16T21:01:00Z",
		TraceID:       "tr_saga",
	})
	createJSON(t, store, "services/payments-api/operations/op_01JABC/control.json", schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   "op_01JABC",
		Service:       "payments-api",
		Env:           "prod",
		Status:        schema.OperationRunning,
		UpdatedAt:     "2026-05-16T21:02:00Z",
	})
	createJSON(t, store, "resources/by-provider/aws/asg/skiff-payments-api.json", schema.ResourceRecord{
		SchemaVersion: schema.Version,
		Logical:       schema.ResourceLogicalRef{Kind: "asg", Name: "payments-api"},
		Provider:      schema.ResourceProviderRef{Provider: "aws", Kind: "asg", ID: "asg-123"},
		Service:       "payments-api",
		Env:           "prod",
		ObservedAt:    "2026-05-16T21:03:00Z",
	})
	createJSON(t, store, "services/payments-api/events/01JROOT.json", schema.Event{
		SchemaVersion: schema.Version,
		ID:            "01JROOT",
		Time:          "2026-05-16T21:04:00Z",
		Subject:       schema.Target{Kind: "service", Name: "payments-api"},
		Type:          "service.updated",
		Summary:       "service updated",
	})

	idx, err := New(store, Options{Clock: fixedClock})
	if err != nil {
		t.Fatalf("new index: %v", err)
	}
	snapshot, err := idx.Rebuild(context.Background())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if !snapshot.Ready || snapshot.Generation != 1 || snapshot.LastFullScanAt.IsZero() {
		t.Fatalf("unexpected snapshot metadata: %+v", snapshot)
	}
	if len(snapshot.Services) != 1 || snapshot.Services[0].Service != "payments-api" || snapshot.Services[0].DesiredRelease != "rel_02" {
		t.Fatalf("unexpected services: %+v", snapshot.Services)
	}
	if len(snapshot.Sagas) != 1 || snapshot.Sagas[0].SagaID != "saga_01JABC" || snapshot.Sagas[0].Status != schema.SagaRunning {
		t.Fatalf("unexpected sagas: %+v", snapshot.Sagas)
	}
	if len(snapshot.Operations) != 1 || snapshot.Operations[0].OperationID != "op_01JABC" {
		t.Fatalf("unexpected operations: %+v", snapshot.Operations)
	}
	if len(snapshot.Resources) != 1 || snapshot.Resources[0].ID != "asg-123" {
		t.Fatalf("unexpected resources: %+v", snapshot.Resources)
	}
	if len(snapshot.RecentEvents) != 1 || snapshot.RecentEvents[0].ID != "01JROOT" {
		t.Fatalf("unexpected recent events: %+v", snapshot.RecentEvents)
	}
}

func TestMalformedObjectsBecomeFindings(t *testing.T) {
	store := memory.New()
	if _, err := store.Create(context.Background(), "services/payments-api/control.json", []byte(`{`), objstore.PutOptions{ContentType: "application/json"}); err != nil {
		t.Fatalf("create malformed control: %v", err)
	}
	if _, err := store.Create(context.Background(), "sagas/saga_01JABC/events/01JBAD.json", []byte(`{`), objstore.PutOptions{ContentType: "application/json"}); err != nil {
		t.Fatalf("create malformed event: %v", err)
	}
	snapshot, err := BuildSnapshot(context.Background(), store, BuildOptions{Now: fixedClock(), Generation: 1})
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if len(snapshot.Findings) != 2 {
		t.Fatalf("findings = %+v, want 2", snapshot.Findings)
	}
	want := map[string]bool{"MALFORMED_SERVICE_CONTROL": false, "MALFORMED_EVENT": false}
	for _, finding := range snapshot.Findings {
		if _, ok := want[finding.Code]; ok {
			want[finding.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Fatalf("missing finding %s in %+v", code, snapshot.Findings)
		}
	}
}

func TestRefreshKeyAppliesHotControlHint(t *testing.T) {
	store := memory.New()
	doc := schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        "payments-api",
		Env:            "prod",
		DesiredRelease: "rel_01",
		Version:        1,
		UpdatedAt:      "2026-05-16T21:00:00Z",
		UpdatedBy:      schema.Actor{ID: "agent-one", Type: "agent"},
	}
	createJSON(t, store, "services/payments-api/control.json", doc)

	idx, err := New(store, Options{Clock: fixedClock})
	if err != nil {
		t.Fatalf("new index: %v", err)
	}
	if _, err := idx.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	current, err := store.Head(context.Background(), "services/payments-api/control.json")
	if err != nil {
		t.Fatalf("head control: %v", err)
	}
	doc.DesiredRelease = "rel_02"
	body, err := canonical.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal updated control: %v", err)
	}
	if _, err := store.CompareAndSwap(context.Background(), "services/payments-api/control.json", current.ETag, body, objstore.PutOptions{ContentType: "application/json"}); err != nil {
		t.Fatalf("cas control: %v", err)
	}

	snapshot, err := idx.ApplyHint(context.Background(), HintForKey(KeyForService("payments-api")))
	if err != nil {
		t.Fatalf("apply hint: %v", err)
	}
	if snapshot.Generation != 2 {
		t.Fatalf("generation = %d, want 2", snapshot.Generation)
	}
	if len(snapshot.Services) != 1 || snapshot.Services[0].DesiredRelease != "rel_02" {
		t.Fatalf("hint did not refresh service: %+v", snapshot.Services)
	}
}

func TestAtomicSnapshotReadsDuringRebuilds(t *testing.T) {
	store := memory.New()
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("svc-%02d", i)
		createJSON(t, store, "services/"+name+"/control.json", schema.ServiceControl{
			SchemaVersion: schema.Version,
			Service:       name,
			Env:           "prod",
			Version:       1,
			UpdatedAt:     "2026-05-16T21:00:00Z",
			UpdatedBy:     schema.Actor{ID: "agent-one", Type: "agent"},
		})
	}
	idx, err := New(store, Options{Clock: fixedClock})
	if err != nil {
		t.Fatalf("new index: %v", err)
	}
	if _, err := idx.Rebuild(context.Background()); err != nil {
		t.Fatalf("initial rebuild: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 20)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				snapshot, err := idx.Snapshot(context.Background())
				if err != nil {
					errCh <- err
					return
				}
				if snapshot.Ready && len(snapshot.Services) != 20 {
					errCh <- fmt.Errorf("saw partial service set: %d", len(snapshot.Services))
					return
				}
			}
		}()
	}
	for i := 0; i < 5; i++ {
		if _, err := idx.Rebuild(context.Background()); err != nil {
			t.Fatalf("rebuild %d: %v", i, err)
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func createJSON(t *testing.T, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: "application/json"}); err != nil {
		t.Fatalf("create %s: %v", key, err)
	}
}

func fixedClock() time.Time {
	return time.Date(2026, 5, 16, 21, 30, 0, 0, time.UTC)
}
