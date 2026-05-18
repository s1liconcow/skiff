package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
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
		"orders-stream",
		"vol-0",
		"rel_02",
		"saga_canary",
		"deploy.started",
		"read-only",
		"COMMAND PALETTE",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestRenderDashboardFitsWindowWidth(t *testing.T) {
	for _, tc := range []struct {
		name    string
		width   int
		noColor bool
	}{
		{name: "narrow", width: 80, noColor: true},
		{name: "wide", width: 138, noColor: true},
		{name: "color", width: 138, noColor: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := New(Options{
				Client:  fakeDashboardClient{},
				Sagas:   fakeSagaClient{},
				TraceID: "tr_tui",
				NoColor: tc.noColor,
				Width:   tc.width,
				Height:  34,
				Now:     tuiTestNow,
			})
			loaded, err := model.Load(context.Background())
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			for i, line := range strings.Split(strings.TrimRight(loaded.View(), "\n"), "\n") {
				if got := lipgloss.Width(line); got > tc.width {
					t.Fatalf("line %d width=%d, want <= %d:\n%s\n\nfull view:\n%s", i+1, got, tc.width, line, loaded.View())
				}
			}
		})
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
			Resources: []servicestatus.ResourceSummary{
				{Kind: "autoscaling-group", ProviderID: "skiff-e2e-44720-1779067497068491000-asg"},
				{Kind: "log-group", ProviderID: "skiff-e2e-44720-1779067497068491000-logs"},
				{Kind: "metric-config", ProviderID: "skiff-e2e-44720-1779067497068491000-metrics"},
				{Kind: "target-group", ProviderID: "fake-target-group-caddy-web"},
			},
		}},
		StatefulGroups: []client.StatefulGroup{{
			Group:    "orders-stream",
			Env:      "prod",
			Replicas: 1,
			Health:   "nominal",
			Members: []client.StatefulMember{{
				Member:     0,
				Generation: 1,
				InstanceID: "i-0",
				VolumeID:   "vol-0",
				DNSName:    "orders-stream-0.internal",
				Phase:      "Ready",
				Health:     "nominal",
			}},
			Backups: []client.StatefulBackup{{
				BackupID:   "backup_01",
				Member:     0,
				SnapshotID: "snap-0",
				ProviderID: "snap-0",
				Status:     "available",
			}},
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
