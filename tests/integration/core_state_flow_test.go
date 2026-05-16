package integration_test

import (
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/tests/harness"
)

func TestCoreObjectStateFlow(t *testing.T) {
	h := harness.New(t)
	doc := h.LoadSpec("../fixtures/services/minimal.yaml")
	h.ValidateSpec(doc)

	graph := h.Compile(doc)
	if graph.Service != doc.Metadata.Name || graph.Env != doc.Metadata.Env {
		t.Fatalf("compiled graph target = %s/%s, want %s/%s", graph.Service, graph.Env, doc.Metadata.Name, doc.Metadata.Env)
	}

	h.CreateEnvironmentRoot(graph.Env)
	control := h.CreateServiceControl(graph.Service, graph.Env)
	if control.Control.Version != 1 {
		t.Fatalf("initial control version = %d, want 1", control.Control.Version)
	}

	operationID := "op_01JFLOW"
	releaseID := "rel_01JFLOW"
	h.CreateOperationIntent(graph.Service, graph.Env, operationID)
	h.CreateOperationControl(graph.Service, graph.Env, operationID, schema.OperationRunning)
	handle := h.AcquireServiceLease(graph.Service)
	h.PublishSignedRelease(harness.ReleaseFixture{
		Service:   graph.Service,
		Env:       graph.Env,
		ReleaseID: releaseID,
	})
	fetched := h.FetchRelease(graph.Service, graph.Env, releaseID)
	if !fetched.Verification.OK {
		t.Fatalf("release verification failed: %+v", fetched.Verification)
	}

	handle = h.SetDesiredRelease(handle, releaseID, operationID)
	event := h.AppendOperationEvent(graph.Service, operationID, "deploy.desired_release_written", "desired release updated")
	final := h.ReleaseServiceLease(handle)

	if final.Control.DesiredRelease != releaseID {
		t.Fatalf("desired release = %q, want %q", final.Control.DesiredRelease, releaseID)
	}
	if final.Control.Lease != nil {
		t.Fatalf("final control still has lease: %+v", final.Control.Lease)
	}
	if final.Control.Operation == nil || final.Control.Operation.ID != operationID || final.Control.Operation.State != string(schema.OperationRunning) {
		t.Fatalf("unexpected active operation: %+v", final.Control.Operation)
	}
	if final.Control.Version != 4 {
		t.Fatalf("final control version = %d, want 4", final.Control.Version)
	}

	controlKey := harness.ServiceControlPath(t, graph.Service)
	h.AssertObjectExists(controlKey)
	h.AssertObjectMatchesGolden(controlKey, "../golden/state/final-service-control.json")
	h.AssertObjectExists(harness.EnvironmentRootPath(t, graph.Env))
	h.AssertObjectExists(harness.OperationIntentPath(t, graph.Service, operationID))
	h.AssertObjectExists(harness.OperationControlPath(t, graph.Service, operationID))
	h.AssertObjectExists(harness.RuntimeManifestPath(t, graph.Service, releaseID))
	h.AssertObjectExists(harness.ReleaseManifestPath(t, graph.Service, releaseID))
	h.AssertObjectExists(harness.OperationEventPath(t, graph.Service, operationID, event.ID))

	wantKeys := []string{
		harness.EnvironmentRootPath(t, graph.Env),
		harness.OperationControlPath(t, graph.Service, operationID),
		harness.OperationEventPath(t, graph.Service, operationID, event.ID),
		harness.OperationIntentPath(t, graph.Service, operationID),
		harness.ReleaseManifestPath(t, graph.Service, releaseID),
		harness.RuntimeManifestPath(t, graph.Service, releaseID),
		harness.ServiceControlPath(t, graph.Service),
	}
	sort.Strings(wantKeys)
	gotKeys := h.ObjectKeys("")
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("object keys = %#v, want %#v", gotKeys, wantKeys)
	}
	for i := range wantKeys {
		if gotKeys[i] != wantKeys[i] {
			t.Fatalf("object keys = %#v, want %#v", gotKeys, wantKeys)
		}
	}
}

func TestConcurrentServiceLeaseAcquisitionAdmitsOneHolder(t *testing.T) {
	h := harness.New(t)
	doc := h.LoadSpec("../fixtures/services/minimal.yaml")
	h.ValidateSpec(doc)
	h.CreateServiceControl(doc.Metadata.Name, doc.Metadata.Env)

	const contenders = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	leaseHeld := 0
	var unexpected []error

	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			client := state.NewClient(h.Store, state.WithClock(h.Clock))
			_, _, err := client.AcquireLease(h.Ctx, doc.Metadata.Name, state.LeaseOptions{
				Owner:    "concurrent-test",
				Duration: time.Minute,
				Actor:    harness.DefaultActor,
				TraceID:  harness.DefaultTraceID,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, state.ErrLeaseHeld):
				leaseHeld++
			default:
				unexpected = append(unexpected, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(unexpected) > 0 {
		t.Fatalf("unexpected lease errors: %v", unexpected)
	}
	if successes != 1 {
		t.Fatalf("lease successes = %d, want 1", successes)
	}
	if leaseHeld != contenders-1 {
		t.Fatalf("lease held errors = %d, want %d", leaseHeld, contenders-1)
	}
}
