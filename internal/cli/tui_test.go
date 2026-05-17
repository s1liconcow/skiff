package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestTUIOnceRendersDashboard(t *testing.T) {
	clearSkiffEnv(t)
	restore := newTUIClient
	newTUIClient = func(cfg config.Config, opts client.Options) (client.Interface, error) {
		return fakeTUIClient{}, nil
	}
	t.Cleanup(func() { newTUIClient = restore })

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"tui", "--once", "--no-color", "--api-url", "http://127.0.0.1:8585", "--format", "human", "--trace-id", "tr_tui"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "Skiff Operations") || !strings.Contains(stdout.String(), "payments-api") {
		t.Fatalf("unexpected TUI output:\n%s", stdout.String())
	}
}

func TestTUIJSONOutputsDashboard(t *testing.T) {
	clearSkiffEnv(t)
	restore := newTUIClient
	newTUIClient = func(cfg config.Config, opts client.Options) (client.Interface, error) {
		return fakeTUIClient{}, nil
	}
	t.Cleanup(func() { newTUIClient = restore })

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"tui", "--api-url", "http://127.0.0.1:8585", "--format", "json", "--trace-id", "tr_tui"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var out tuiOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !out.OK || out.TraceID != "tr_tui" || len(out.Dashboard.Status.Services) != 1 || len(out.Dashboard.Sagas) != 1 {
		t.Fatalf("unexpected output: %+v", out)
	}
}

type fakeTUIClient struct{}

func (fakeTUIClient) Version(ctx context.Context, opts client.VersionOptions) (*client.Version, error) {
	return &client.Version{Binary: "skiff"}, nil
}

func (fakeTUIClient) Status(ctx context.Context, opts client.StatusOptions) (*client.Status, error) {
	return &client.Status{
		Mode:   config.ModeAPI,
		Env:    "prod",
		Source: "api",
		Freshness: client.Freshness{
			Source:           "memory",
			Ready:            true,
			RefreshedAt:      time.Date(2026, 5, 17, 2, 25, 0, 0, time.UTC),
			FreshnessSeconds: 1,
		},
		Services: []client.ServiceStatus{{
			Service:        "payments-api",
			DesiredRelease: "rel_02",
			StableRelease:  "rel_01",
			Health:         "updating",
			Capacity:       client.DependencyStatus{Status: "configured", ProviderID: "asg-123"},
			TargetHealth:   client.DependencyStatus{Status: "configured", ProviderID: "tg-123"},
			Logs:           client.DependencyStatus{Status: "configured", ProviderID: "logs"},
			Metrics:        client.DependencyStatus{Status: "configured", ProviderID: "metrics"},
		}},
	}, nil
}

func (fakeTUIClient) Doctor(ctx context.Context, opts client.DoctorOptions) (*client.Doctor, error) {
	return &client.Doctor{}, nil
}

func (fakeTUIClient) Events(ctx context.Context, opts client.EventOptions) (*client.EventList, error) {
	return &client.EventList{
		Events: []schema.Event{{
			ID:      "evt_01",
			Time:    "2026-05-17T02:24:00Z",
			Subject: schema.Target{Kind: "service", Name: "payments-api"},
			Type:    "deploy.started",
			Summary: "deploy started",
		}},
		Source: "api",
	}, nil
}

func (fakeTUIClient) Sagas(ctx context.Context, opts client.SagaOptions) (*client.SagaList, error) {
	return &client.SagaList{Sagas: []client.SagaSummary{{
		SagaID:       "saga_canary",
		Status:       schema.SagaRunning,
		CurrentSteps: []string{"approval"},
	}}}, nil
}
