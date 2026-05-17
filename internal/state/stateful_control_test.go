package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestStatefulMemberLeaseAndGenerationFencing(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	clock := newManualClock(time.Date(2026, 5, 17, 7, 0, 0, 0, time.UTC))
	client := state.NewClient(store, state.WithClock(clock), state.WithTokenGenerator(func() string { return "member-token" }))
	_, err := client.CreateStatefulMemberControl(ctx, schema.StatefulMemberControl{
		Group:      "postgres",
		Env:        "prod",
		Member:     0,
		InstanceID: "i-old",
		VolumeID:   "vol-123",
		Generation: 1,
		UpdatedBy:  schema.Actor{ID: "seed", Type: "test"},
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}

	handle, leased, err := client.AcquireStatefulMemberLease(ctx, "postgres", 0, state.StatefulMemberLeaseOptions{
		Owner:    "replacer",
		Duration: time.Minute,
		Actor:    schema.Actor{ID: "operator", Type: "user"},
		TraceID:  "tr_stateful",
	})
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if handle.Generation != 2 || leased.Control.Lease == nil {
		t.Fatalf("unexpected lease handle/doc: %+v %+v", handle, leased.Control.Lease)
	}
	if _, _, err := client.AcquireStatefulMemberLease(ctx, "postgres", 0, state.StatefulMemberLeaseOptions{Owner: "other", Duration: time.Minute}); !errors.Is(err, state.ErrLeaseHeld) {
		t.Fatalf("second lease error = %v, want ErrLeaseHeld", err)
	}

	handle, updated, err := client.UpdateStatefulMemberWithLeaseCAS(ctx, *handle, func(control *schema.StatefulMemberControl) error {
		control.Phase = state.StatefulMemberReplacing
		control.Generation = 2
		return nil
	})
	if err != nil {
		t.Fatalf("update with lease: %v", err)
	}
	if updated.Control.Generation != 2 || updated.Control.Phase != state.StatefulMemberReplacing {
		t.Fatalf("generation or phase not updated: %+v", updated.Control)
	}
	if _, err := client.ReleaseStatefulMemberLease(ctx, *handle); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	got, err := client.GetStatefulMemberControl(ctx, "postgres", 0)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if got.Control.Lease != nil {
		t.Fatalf("lease was not released: %+v", got.Control.Lease)
	}
}

func TestStatefulGroupControlCreateAndUpdateCAS(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	clock := newManualClock(time.Date(2026, 5, 17, 7, 0, 0, 0, time.UTC))
	client := state.NewClient(store, state.WithClock(clock))
	created, err := client.CreateStatefulGroupControl(ctx, schema.StatefulGroupControl{
		Group:     "postgres",
		Env:       "prod",
		Replicas:  1,
		UpdatedBy: schema.Actor{ID: "seed", Type: "test"},
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if created.Key != "stateful/postgres/control.json" || created.Control.Version != 1 {
		t.Fatalf("unexpected created group doc: %+v", created)
	}
	next := created.Control
	next.Members = []schema.StatefulMemberSummary{{Member: 0, Generation: 1, InstanceID: "i-123", VolumeID: "vol-123", Phase: state.StatefulMemberReady}}
	updated, err := client.UpdateStatefulGroupControlCAS(ctx, created, next)
	if err != nil {
		t.Fatalf("update group: %v", err)
	}
	if updated.Control.Version != 2 || len(updated.Control.Members) != 1 {
		t.Fatalf("group update did not CAS version/member summary: %+v", updated.Control)
	}
	if _, err := client.UpdateStatefulGroupControlCAS(ctx, created, next); !errors.Is(err, state.ErrPreconditionFailed) {
		t.Fatalf("stale group update error = %v, want ErrPreconditionFailed", err)
	}
}
