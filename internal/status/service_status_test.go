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

func TestFromSnapshotComposesStatefulGroupHealth(t *testing.T) {
	now := time.Date(2026, 5, 17, 1, 0, 0, 0, time.UTC)
	snapshot := stateindex.Snapshot{
		Ready:       true,
		Generation:  4,
		RefreshedAt: now.Add(-time.Minute),
		StatefulGroups: []stateindex.StatefulGroupSummary{{
			Group:    "orders-stream",
			Env:      "prod",
			Replicas: 2,
			Members: []stateindex.StatefulMemberSummary{
				{
					Member:             0,
					Generation:         1,
					ExpectedGeneration: 2,
					InstanceID:         "i-old",
					ExpectedInstanceID: "i-new",
					VolumeID:           "vol-wrong",
					ExpectedVolumeID:   "vol-0",
					DNSName:            "",
					ExpectedDNSName:    "orders-stream-0.internal",
					Phase:              "failed",
					UpdatedAt:          "2026-05-17T00:58:00Z",
				},
				{
					Member:     1,
					Generation: 1,
					InstanceID: "i-1",
					VolumeID:   "vol-1",
					DNSName:    "orders-stream-1.internal",
					Phase:      "ready",
					UpdatedAt:  "2026-05-17T00:59:00Z",
				},
			},
			Backups: []stateindex.StatefulBackupSummary{
				{
					BackupID:      "backup_old",
					Member:        0,
					VolumeID:      "vol-0",
					SnapshotID:    "snap-old",
					ProviderID:    "snap-old",
					Status:        "available",
					RecipeStatus:  "unhealthy",
					RecipeSummary: "backup hook reported lagging follower",
					CreatedAt:     "2026-05-16T00:00:00Z",
					ExpiresAt:     "2026-05-16T12:00:00Z",
				},
				{
					BackupID:   "backup_fresh",
					Member:     1,
					VolumeID:   "vol-1",
					SnapshotID: "snap-fresh",
					ProviderID: "snap-fresh",
					Status:     "available",
					CreatedAt:  "2026-05-17T00:30:00Z",
					ExpiresAt:  "2026-05-18T00:30:00Z",
				},
			},
		}},
	}
	result := FromSnapshot(snapshot, Options{
		Mode:        config.ModeDirect,
		Env:         "prod",
		Provider:    "aws",
		Region:      "us-west-2",
		Source:      "direct",
		Service:     "orders-stream",
		Now:         now,
		StateBucket: "file:///state",
	})
	if len(result.StatefulGroups) != 1 {
		t.Fatalf("stateful groups = %+v", result.StatefulGroups)
	}
	group := result.StatefulGroups[0]
	if group.Health != "degraded" || group.Group != "orders-stream" || group.Replicas != 2 {
		t.Fatalf("unexpected stateful group: %+v", group)
	}
	if len(group.Members) != 2 || group.Members[0].Health != "degraded" || group.Members[1].Health != "nominal" {
		t.Fatalf("unexpected member health: %+v", group.Members)
	}
	if !hasStatusFinding(group.Findings, "STATEFUL_MEMBER_NOT_READY") || !hasStatusFinding(group.Findings, "STATEFUL_RUNNER_STALE") || !hasStatusFinding(group.Findings, "STATEFUL_MEMBER_VOLUME_MISMATCH") || !hasStatusFinding(group.Findings, "STATEFUL_MEMBER_DNS_MISSING") || !hasStatusFinding(group.Findings, "STATEFUL_PROVIDER_DRIFT") || !hasStatusFinding(group.Findings, "STATEFUL_RECIPE_UNHEALTHY") || !hasStatusFinding(group.Findings, "STATEFUL_BACKUP_STALE") || !hasStatusFinding(group.Findings, "STATEFUL_QUORUM_RISK") {
		t.Fatalf("missing stateful findings: %+v", group.Findings)
	}
	if len(group.Backups) != 2 || !group.Backups[0].Stale || group.Backups[1].Stale {
		t.Fatalf("backup staleness = %+v", group.Backups)
	}
}

func hasStatusFinding(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
