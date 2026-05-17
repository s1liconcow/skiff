package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRenderDashboardSnapshot(t *testing.T) {
	model := New(Options{
		Client:   fakeDashboardClient{},
		Sagas:    fakeSagaClient{},
		TraceID:  "tr_tui",
		ReadOnly: true,
		NoColor:  true,
		Width:    100,
		Now:      tuiTestNow,
	})
	loaded, err := model.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	view := loaded.View()
	for _, want := range []string{
		"Skiff Operations",
		"payments-api",
		"rel_02",
		"saga_canary",
		"deploy.started",
		"read-only",
		"Command Palette",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestReadOnlyBlocksMutatingActions(t *testing.T) {
	model := New(Options{Client: fakeDashboardClient{}, Sagas: fakeSagaClient{}, ReadOnly: true, NoColor: true})
	loaded, err := model.Load(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	action, ok := loaded.ActionForKey("b")
	if !ok {
		t.Fatalf("rollback action missing")
	}
	if !action.Mutating || action.Allowed {
		t.Fatalf("rollback should be mutating and blocked in read-only mode: %+v", action)
	}
	readAction, ok := loaded.ActionForKey("d")
	if !ok || readAction.Mutating || !readAction.Allowed {
		t.Fatalf("doctor should be allowed read-only action: %+v ok=%v", readAction, ok)
	}
}

type fakeDashboardClient struct{}

func (fakeDashboardClient) Version(ctx context.Context, opts client.VersionOptions) (*client.Version, error) {
	return &client.Version{Binary: "skiff"}, nil
}

func (fakeDashboardClient) Status(ctx context.Context, opts client.StatusOptions) (*client.Status, error) {
	return &client.Status{
		Mode:     config.ModeDirect,
		Env:      "prod",
		Provider: "aws",
		Region:   "us-west-2",
		Source:   "direct",
		Freshness: client.Freshness{
			Source:           "direct_object_store",
			Ready:            true,
			RefreshedAt:      tuiTestNow(),
			FreshnessSeconds: 2,
		},
		Services: []client.ServiceStatus{{
			Service:        "payments-api",
			Env:            "prod",
			DesiredRelease: "rel_02",
			StableRelease:  "rel_01",
			Health:         "updating",
			OperationID:    "op_rollout",
			OperationKind:  "deploy",
			OperationState: "running",
			Capacity:       client.DependencyStatus{Status: "configured", ProviderID: "asg-123"},
			TargetHealth:   client.DependencyStatus{Status: "configured", ProviderID: "tg-123"},
			Logs:           client.DependencyStatus{Status: "configured", ProviderID: "log-123"},
			Metrics:        client.DependencyStatus{Status: "configured", ProviderID: "metric-123"},
		}},
	}, nil
}

func (fakeDashboardClient) Doctor(ctx context.Context, opts client.DoctorOptions) (*client.Doctor, error) {
	return &client.Doctor{}, nil
}

func (fakeDashboardClient) Events(ctx context.Context, opts client.EventOptions) (*client.EventList, error) {
	return &client.EventList{
		Events: []schema.Event{{
			ID:      "evt_01",
			Time:    "2026-05-17T02:20:00Z",
			Subject: schema.Target{Kind: "service", Name: "payments-api"},
			Type:    "deploy.started",
			Summary: "deploy started",
		}},
		Source: "direct",
	}, nil
}

type fakeSagaClient struct{}

func (fakeSagaClient) Sagas(ctx context.Context, opts client.SagaOptions) (*client.SagaList, error) {
	return &client.SagaList{Sagas: []client.SagaSummary{{
		SagaID:       "saga_canary",
		Status:       schema.SagaRunning,
		CurrentSteps: []string{"approval-before-cutover"},
		UpdatedAt:    "2026-05-17T02:19:00Z",
		TraceID:      "tr_tui",
	}}}, nil
}

func tuiTestNow() time.Time {
	return time.Date(2026, 5, 17, 2, 20, 0, 0, time.UTC)
}
