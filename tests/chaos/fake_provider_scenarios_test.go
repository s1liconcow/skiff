package chaos_test

import (
	"context"
	"errors"
	"testing"
	"time"

	servicedoctor "github.com/s1liconcow/skiff/internal/doctor"
	"github.com/s1liconcow/skiff/internal/provider"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
)

func TestFakeProviderChaosScenarios(t *testing.T) {
	t.Run("asg rollout failure", func(t *testing.T) {
		cloud := fakeprovider.New(fakeprovider.WithRolloutStatus("failed"))
		status, err := cloud.WatchRollout(context.Background(), provider.WatchRolloutRequest{
			Service:    "payments-api",
			Env:        "prod",
			RolloutID:  "op-chaos-asg",
			ProviderID: "ir-chaos-asg",
		})
		if err != nil {
			t.Fatalf("watch rollout: %v", err)
		}
		if status.Status != "failed" || status.ProviderID != "ir-chaos-asg" {
			t.Fatalf("unexpected rollout status: %+v", status)
		}
	})

	t.Run("log and metric backend outage", func(t *testing.T) {
		cloud := fakeprovider.New(
			fakeprovider.WithLogsError(&provider.Error{Code: provider.CodeProvider, Provider: fakeprovider.Name, Op: "logs", Summary: "log backend unavailable"}),
			fakeprovider.WithMetricsError(&provider.Error{Code: provider.CodeProvider, Provider: fakeprovider.Name, Op: "metrics", Summary: "metric backend unavailable"}),
		)
		if _, err := cloud.Logs(context.Background(), provider.LogsRequest{Service: "payments-api", Env: "prod"}); !isProviderOp(err, "logs") {
			t.Fatalf("logs err = %v, want provider logs error", err)
		}
		if _, err := cloud.Metrics(context.Background(), provider.MetricsRequest{Service: "payments-api", Env: "prod"}); !isProviderOp(err, "metrics") {
			t.Fatalf("metrics err = %v, want provider metrics error", err)
		}
	})

	t.Run("instance death", func(t *testing.T) {
		result := diagnoseChaos(t, "runner.failed", "runner failed after instance i-dead stopped unexpectedly")
		if !hasFinding(result.Findings, "RUNNER_NOT_SERVING") {
			t.Fatalf("missing runner finding: %+v", result.Findings)
		}
	})

	t.Run("target health failure", func(t *testing.T) {
		result := diagnoseChaos(t, "target_health.failed", "target group health check failed: 1 target unhealthy")
		if !hasFinding(result.Findings, "TARGET_HEALTH_UNHEALTHY") {
			t.Fatalf("missing target health finding: %+v", result.Findings)
		}
	})

	t.Run("regional outage", func(t *testing.T) {
		result := diagnoseChaos(t, "regional.outage", "autoscaling failed with insufficient regional capacity in us-east-1")
		if !hasFinding(result.Findings, "CAPACITY_MISMATCH") {
			t.Fatalf("missing capacity mismatch finding: %+v", result.Findings)
		}
	})
}

func diagnoseChaos(t *testing.T, eventType, summary string) servicedoctor.Result {
	t.Helper()
	status := servicestatus.Result{
		Env:       "prod",
		Provider:  fakeprovider.Name,
		Region:    "local",
		Source:    "fake-chaos",
		Freshness: servicestatus.Freshness{Source: "fake-chaos", Ready: true, Generation: 1, RefreshedAt: chaosNow()},
		Services: []servicestatus.Service{{
			Service:        "payments-api",
			Env:            "prod",
			DesiredRelease: "rel-chaos",
			StableRelease:  "rel-stable",
			Health:         "degraded",
			Capacity:       servicestatus.DependencyStatus{Status: "configured", Source: "capacity", ProviderID: "fake-asg-payments-api"},
			TargetHealth:   servicestatus.DependencyStatus{Status: "configured", Source: "target_health", ProviderID: "fake-tg-payments-api"},
			Logs:           servicestatus.DependencyStatus{Status: "configured", Source: "logs", ProviderID: "fake-log-payments-api"},
			Metrics:        servicestatus.DependencyStatus{Status: "configured", Source: "metrics", ProviderID: "fake-metric-payments-api"},
			RecentEvents: []schema.Event{{
				SchemaVersion: schema.Version,
				ID:            "evt-chaos",
				Time:          "2026-05-17T18:00:00Z",
				TraceID:       "tr_chaos",
				Subject:       schema.Target{Kind: "service", Name: "payments-api"},
				Type:          eventType,
				Severity:      "high",
				Summary:       summary,
			}},
		}},
	}
	result, err := servicedoctor.Diagnose(context.Background(), status, servicedoctor.Options{Service: "payments-api", TraceID: "tr_chaos", Binary: "skiff"})
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	return result
}

func hasFinding(findings []servicedoctor.Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func isProviderOp(err error, op string) bool {
	var providerErr *provider.Error
	return errors.As(err, &providerErr) && providerErr.Provider == fakeprovider.Name && providerErr.Op == op
}

func chaosNow() time.Time {
	return time.Date(2026, 5, 17, 18, 0, 0, 0, time.UTC)
}
