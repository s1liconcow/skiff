package status

import (
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	stateindex "github.com/s1liconcow/skiff/internal/index"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestFromSnapshotComposesServiceHealthAndDependencies(t *testing.T) {
	snapshot := stateindex.Snapshot{
		Ready:       true,
		Generation:  3,
		RefreshedAt: time.Date(2026, 5, 16, 23, 20, 0, 0, time.UTC),
		Services: []stateindex.ServiceSummary{{
			Service:        "payments-api",
			Env:            "prod",
			DesiredRelease: "rel_02",
			StableRelease:  "rel_01",
			OperationID:    "op_01",
			OperationKind:  "deploy",
			OperationState: "rolling_out",
		}},
		Operations: []stateindex.OperationSummary{{
			OperationID: "op_01",
			Service:     "payments-api",
			Status:      schema.OperationRunning,
			ProviderOperations: []schema.ProviderOperationRef{{
				Provider:    "aws",
				Kind:        "asg-instance-refresh",
				ID:          "ir-123",
				Description: "ASG instance refresh",
			}},
		}},
		Resources: []stateindex.ResourceSummary{
			{Provider: "aws", Kind: "autoscaling-group", ID: "asg-123", Service: "payments-api", Env: "prod", ObservedAt: "2026-05-16T23:19:00Z"},
			{Provider: "aws", Kind: "target-group", ID: "tg-123", Service: "payments-api", Env: "prod"},
			{Provider: "aws", Kind: "rds-db-instance", ID: "db-123", Service: "payments-api", Env: "prod"},
			{Provider: "aws", Kind: "log-group", ID: "/skiff/prod/payments-api", Service: "payments-api", Env: "prod"},
			{Provider: "aws", Kind: "metric-config", ID: "Skiff/prod/payments-api", Service: "payments-api", Env: "prod"},
		},
		RecentEvents: []schema.Event{{
			ID:      "01JSTART",
			Time:    "2026-05-16T23:18:00Z",
			Subject: schema.Target{Kind: "service", Name: "payments-api"},
			Type:    "deploy.started",
			Summary: "deploy started",
		}},
	}
	result := FromSnapshot(snapshot, Options{
		Mode:      config.ModeDirect,
		Env:       "prod",
		Provider:  "aws",
		Region:    "us-west-2",
		Source:    "direct",
		Freshness: FreshnessFromIndex(stateindex.FreshnessFromSnapshot(snapshot, snapshot.RefreshedAt, "direct_object_store")),
	})
	if len(result.Services) != 1 {
		t.Fatalf("services = %d", len(result.Services))
	}
	service := result.Services[0]
	if service.Health != "updating" || service.Rollout == nil || service.Rollout.ProviderID != "ir-123" {
		t.Fatalf("unexpected rollout health: %+v", service)
	}
	if service.Capacity.Status != "configured" || service.Database.Status != "configured" || service.Database.ProviderID != "db-123" || service.Logs.Status != "configured" || service.Metrics.Status != "configured" {
		t.Fatalf("dependencies not configured: %+v", service)
	}
	if len(service.RecentEvents) != 1 || service.RecentEvents[0].ID != "01JSTART" {
		t.Fatalf("recent events not attached: %+v", service.RecentEvents)
	}
}
