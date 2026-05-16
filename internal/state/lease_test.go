package state_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestConcurrentAcquireLeaseAdmitsExactlyOneOwner(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	clock := newManualClock(time.Date(2026, 5, 16, 17, 0, 0, 0, time.UTC))
	var tokenCounter atomic.Int64
	client := state.NewClient(
		store,
		state.WithClock(clock),
		state.WithTokenGenerator(func() string {
			return fmt.Sprintf("lease_%03d", tokenCounter.Add(1))
		}),
		state.WithRetryOptions(state.RetryOptions{MaxAttempts: 4, BaseDelay: time.Microsecond, MaxDelay: time.Millisecond}),
	)
	createControl(t, ctx, client)

	const contenders = 32
	var acquired atomic.Int64
	var held atomic.Int64
	var unexpected atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			handle, _, err := client.AcquireLease(ctx, "payments-api", state.LeaseOptions{
				Owner:    fmt.Sprintf("worker-%02d", i),
				Duration: time.Minute,
			})
			switch {
			case err == nil:
				if handle.Owner == "" || handle.Token == "" || handle.ETag == "" {
					t.Errorf("incomplete handle: %+v", handle)
				}
				acquired.Add(1)
			case errors.Is(err, state.ErrLeaseHeld):
				held.Add(1)
			default:
				t.Errorf("AcquireLease returned unexpected error: %v", err)
				unexpected.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if acquired.Load() != 1 {
		t.Fatalf("acquired = %d, want 1", acquired.Load())
	}
	if held.Load() != contenders-1 {
		t.Fatalf("held = %d, want %d", held.Load(), contenders-1)
	}
	if unexpected.Load() != 0 {
		t.Fatalf("unexpected errors = %d", unexpected.Load())
	}
}

func TestExpiredLeaseCanBeTakenOverAndOldOwnerLosesLease(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	clock := newManualClock(time.Date(2026, 5, 16, 17, 0, 0, 0, time.UTC))
	var tokenCounter atomic.Int64
	client := state.NewClient(store, state.WithClock(clock), state.WithTokenGenerator(func() string {
		return fmt.Sprintf("lease_%03d", tokenCounter.Add(1))
	}))
	createControl(t, ctx, client)

	first, firstDoc, err := client.AcquireLease(ctx, "payments-api", state.LeaseOptions{Owner: "skiffd/instance-a", Duration: time.Minute})
	if err != nil {
		t.Fatalf("first AcquireLease returned error: %v", err)
	}
	if first.Generation != 2 || firstDoc.Control.Version != 2 {
		t.Fatalf("first generation/version = %d/%d, want 2/2", first.Generation, firstDoc.Control.Version)
	}

	clock.Add(2 * time.Minute)
	if _, _, err := client.HeartbeatLease(ctx, *first, time.Minute); !errors.Is(err, state.ErrLeaseLost) {
		t.Fatalf("expired owner heartbeat error = %v, want ErrLeaseLost", err)
	}
	second, secondDoc, err := client.AcquireLease(ctx, "payments-api", state.LeaseOptions{Owner: "skiffd/instance-b", Duration: time.Minute})
	if err != nil {
		t.Fatalf("takeover AcquireLease returned error: %v", err)
	}
	if second.Owner != "skiffd/instance-b" || second.Generation != 3 || secondDoc.Control.Version != 3 {
		t.Fatalf("takeover handle/doc = %+v / %+v", second, secondDoc.Control)
	}

	if _, _, err := client.HeartbeatLease(ctx, *first, time.Minute); !errors.Is(err, state.ErrLeaseLost) {
		t.Fatalf("old owner heartbeat error = %v, want ErrLeaseLost", err)
	}
}

func TestHeartbeatRequiresLatestETagAndDetectsLeaseLoss(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	clock := newManualClock(time.Date(2026, 5, 16, 17, 0, 0, 0, time.UTC))
	client := state.NewClient(store, state.WithClock(clock), state.WithTokenGenerator(func() string {
		return "lease_alpha"
	}))
	createControl(t, ctx, client)

	handle, _, err := client.AcquireLease(ctx, "payments-api", state.LeaseOptions{Owner: "skiffd/instance-a", Duration: time.Minute})
	if err != nil {
		t.Fatalf("AcquireLease returned error: %v", err)
	}

	clock.Add(10 * time.Second)
	nextHandle, mutated, err := client.UpdateServiceControlWithLeaseCAS(ctx, *handle, func(control *schema.ServiceControl) error {
		control.DesiredRelease = "rel_01JNEW"
		control.Lease = nil
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateServiceControlWithLeaseCAS returned error: %v", err)
	}
	if mutated.Control.DesiredRelease != "rel_01JNEW" || mutated.Control.Lease.Token != handle.Token {
		t.Fatalf("mutated control = %+v", mutated.Control)
	}
	if mutated.Control.Version != 3 {
		t.Fatalf("mutated version = %d, want 3", mutated.Control.Version)
	}

	clock.Add(10 * time.Second)
	if _, _, err := client.HeartbeatLease(ctx, *handle, time.Minute); !errors.Is(err, state.ErrPreconditionFailed) {
		t.Fatalf("stale handle heartbeat error = %v, want ErrPreconditionFailed", err)
	}

	refreshed, heartbeatDoc, err := client.HeartbeatLease(ctx, *nextHandle, time.Minute)
	if err != nil {
		t.Fatalf("fresh handle heartbeat returned error: %v", err)
	}
	if refreshed.ETag == nextHandle.ETag || heartbeatDoc.Control.Version != 4 {
		t.Fatalf("heartbeat did not advance ETag/version: %+v / %d", refreshed, heartbeatDoc.Control.Version)
	}

	if _, err := client.ReleaseLease(ctx, *refreshed); err != nil {
		t.Fatalf("ReleaseLease returned error: %v", err)
	}
	if _, _, err := client.HeartbeatLease(ctx, *refreshed, time.Minute); !errors.Is(err, state.ErrLeaseLost) {
		t.Fatalf("heartbeat after release error = %v, want ErrLeaseLost", err)
	}
}

func createControl(t *testing.T, ctx context.Context, client *state.Client) {
	t.Helper()
	control := schema.NewServiceControl("payments-api", "prod", "", schema.Actor{ID: "alpha-one", Type: "agent"})
	if _, err := client.CreateServiceControl(ctx, control); err != nil {
		t.Fatalf("CreateServiceControl returned error: %v", err)
	}
}
