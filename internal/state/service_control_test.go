package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestServiceControlCreateGetAndUpdateCAS(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	clock := newManualClock(time.Date(2026, 5, 16, 17, 0, 0, 0, time.UTC))
	client := state.NewClient(store, state.WithClock(clock))

	control := schema.NewServiceControl("payments-api", "prod", "", schema.Actor{ID: "alpha-one", Type: "agent"})
	created, err := client.CreateServiceControl(ctx, control)
	if err != nil {
		t.Fatalf("CreateServiceControl returned error: %v", err)
	}
	if created.Key != "services/payments-api/control.json" || created.ETag == "" {
		t.Fatalf("created document missing key or etag: %+v", created)
	}
	if created.Control.Version != 1 {
		t.Fatalf("created version = %d, want 1", created.Control.Version)
	}
	if created.Control.UpdatedAt != "2026-05-16T17:00:00Z" {
		t.Fatalf("created UpdatedAt = %q", created.Control.UpdatedAt)
	}
	if created.Meta.ContentType != "application/json" || created.Meta.Metadata["service"] != "payments-api" {
		t.Fatalf("metadata = %+v content_type=%q", created.Meta.Metadata, created.Meta.ContentType)
	}

	got, err := client.GetServiceControl(ctx, "payments-api")
	if err != nil {
		t.Fatalf("GetServiceControl returned error: %v", err)
	}
	if got.ETag != created.ETag || got.Control.Service != "payments-api" {
		t.Fatalf("got = %+v, created = %+v", got, created)
	}

	clock.Set(time.Date(2026, 5, 16, 17, 5, 0, 0, time.UTC))
	next := got.Control
	next.DesiredRelease = "rel_01JNEW"
	next.Operation = &schema.ActiveOperation{ID: "op_01JDEPLOY", Kind: "deploy", State: "rolling_out", Step: "canary"}
	updated, err := client.UpdateServiceControlCAS(ctx, got, next)
	if err != nil {
		t.Fatalf("UpdateServiceControlCAS returned error: %v", err)
	}
	if updated.Control.Version != 2 || updated.Control.UpdatedAt != "2026-05-16T17:05:00Z" {
		t.Fatalf("updated version/time = %d/%q", updated.Control.Version, updated.Control.UpdatedAt)
	}
	if updated.Control.DesiredRelease != "rel_01JNEW" || updated.Control.Operation.ID != "op_01JDEPLOY" {
		t.Fatalf("updated control = %+v", updated.Control)
	}

	staleNext := got.Control
	staleNext.DesiredRelease = "rel_01JSTALE"
	_, err = client.UpdateServiceControlCAS(ctx, got, staleNext)
	if !errors.Is(err, state.ErrPreconditionFailed) {
		t.Fatalf("stale update error = %v, want ErrPreconditionFailed", err)
	}
	var stateErr *state.Error
	if !errors.As(err, &stateErr) || stateErr.Code != state.CodePreconditionFailed {
		t.Fatalf("stale update structured error = %#v", err)
	}
}

func TestServiceControlCreateUsesCreateOnlySemantics(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	client := state.NewClient(store, state.WithClock(newManualClock(time.Date(2026, 5, 16, 17, 0, 0, 0, time.UTC))))
	control := schema.NewServiceControl("payments-api", "prod", "", schema.Actor{ID: "alpha-one", Type: "agent"})

	if _, err := client.CreateServiceControl(ctx, control); err != nil {
		t.Fatalf("initial CreateServiceControl returned error: %v", err)
	}
	if _, err := client.CreateServiceControl(ctx, control); !errors.Is(err, objstore.ErrAlreadyExists) {
		t.Fatalf("duplicate CreateServiceControl error = %v, want ErrAlreadyExists", err)
	}
}
