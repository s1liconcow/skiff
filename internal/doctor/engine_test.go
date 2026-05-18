package doctor

import (
	"context"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
)

func TestDiagnoseBadReleaseKeepsFactsHypothesesAndActionsSeparate(t *testing.T) {
	result, err := Diagnose(context.Background(), servicestatus.Result{
		Mode:      config.ModeDirect,
		Env:       "prod",
		Provider:  "aws",
		Region:    "us-west-2",
		Source:    "direct",
		Freshness: servicestatus.Freshness{Source: "direct_object_store", Ready: true, RefreshedAt: fixedTime()},
		Services: []servicestatus.Service{{
			Service:        "payments-api",
			Env:            "prod",
			DesiredRelease: "rel_bad",
			StableRelease:  "rel_good",
			OperationID:    "op_01",
			OperationKind:  "deploy",
			OperationState: "failed",
			Health:         "degraded",
			Operation: &servicestatus.Operation{
				ID:    "op_01",
				Kind:  "deploy",
				State: "failed",
				ProviderOperations: []schema.ProviderOperationRef{{
					Provider: "aws",
					Kind:     "asg-instance-refresh",
					ID:       "ir-123",
				}},
			},
			Rollout:      &servicestatus.Rollout{Status: "failed", ProviderID: "ir-123"},
			Capacity:     servicestatus.DependencyStatus{Status: "configured", Source: "capacity", ProviderID: "asg-123"},
			TargetHealth: servicestatus.DependencyStatus{Status: "configured", Source: "target_health", ProviderID: "tg-123"},
			Logs:         servicestatus.DependencyStatus{Status: "configured", Source: "logs", ProviderID: "/skiff/prod/payments-api"},
			Metrics:      servicestatus.DependencyStatus{Status: "configured", Source: "metrics", ProviderID: "Skiff/prod/payments-api"},
			RecentEvents: []schema.Event{{
				ID:      "01JTGT",
				Time:    "2026-05-16T23:00:00Z",
				Type:    "rollout.target_health",
				Subject: schema.Target{Kind: "service", Name: "payments-api"},
				Summary: "target group reports 1 unhealthy target",
				Facts: []schema.Fact{
					{Type: "target_health", Message: "1 target unhealthy"},
					{Type: "runner_state", Message: "i-abc123 is WaitingForHealth"},
				},
			}},
		}},
	}, Options{Service: "payments-api", TraceID: "tr_doctor"})
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if result.Health != "degraded" || result.TraceID != "tr_doctor" || result.Service != "payments-api" {
		t.Fatalf("unexpected result metadata: %+v", result)
	}
	assertFinding(t, result, "ROLLOUT_FAILED_OR_DEGRADED", SeverityHigh)
	assertFinding(t, result, "RUNNER_NOT_SERVING", SeverityHigh)
	assertFinding(t, result, "TARGET_HEALTH_UNHEALTHY", SeverityHigh)
	if len(result.Facts) == 0 || len(result.Hypotheses) == 0 {
		t.Fatalf("facts or hypotheses missing: %+v", result)
	}
	if !hasAction(result, "payments-api_inspect_logs", false) {
		t.Fatalf("missing read-only log action: %+v", result.RecommendedActions)
	}
	if !hasAction(result, "payments-api_rollback_to_stable", true) {
		t.Fatalf("missing mutating rollback action: %+v", result.RecommendedActions)
	}
	for _, action := range result.RecommendedActions {
		if action.Mutating && action.Risk == "" {
			t.Fatalf("mutating action lacks risk: %+v", action)
		}
	}
}

func TestDiagnoseCommonFailureScenarios(t *testing.T) {
	result, err := Diagnose(context.Background(), servicestatus.Result{
		Env:       "prod",
		Provider:  "aws",
		Region:    "us-west-2",
		Source:    "api",
		Freshness: servicestatus.Freshness{Source: "memory", Ready: true},
		Services: []servicestatus.Service{{
			Service:        "payments-api",
			Env:            "prod",
			DesiredRelease: "rel_02",
			StableRelease:  "rel_01",
			Health:         "degraded",
			Capacity:       servicestatus.DependencyStatus{Status: "unknown", Source: "capacity", Summary: "resource has not been observed in object state"},
			TargetHealth:   servicestatus.DependencyStatus{Status: "unknown", Source: "target_health", Summary: "target group missing"},
			Logs:           servicestatus.DependencyStatus{Status: "unknown", Source: "logs", Summary: "CloudWatch log group missing"},
			Metrics:        servicestatus.DependencyStatus{Status: "unknown", Source: "metrics", Summary: "metric config missing"},
			RecentEvents: []schema.Event{
				{
					ID:      "01JIAM",
					Time:    "2026-05-16T23:01:00Z",
					Type:    "runner.manifest_fetch_failed",
					Subject: schema.Target{Kind: "service", Name: "payments-api"},
					Summary: "runner AccessDenied reading secret and object manifest from S3",
				},
				{
					ID:      "01JMETRIC",
					Time:    "2026-05-16T23:02:00Z",
					Type:    "metric.gate_failed",
					Subject: schema.Target{Kind: "service", Name: "payments-api"},
					Summary: "metric gate failed: alb_5xx above threshold",
				},
				{
					ID:      "01JLOG",
					Time:    "2026-05-16T23:03:00Z",
					Type:    "logs.bad",
					Subject: schema.Target{Kind: "service", Name: "payments-api"},
					Summary: "application log panic on startup",
				},
				{
					ID:      "01JSECRET",
					Time:    "2026-05-16T23:04:00Z",
					Type:    "secret.rotation_failed",
					Subject: schema.Target{Kind: "service", Name: "payments-api"},
					Summary: "secret rotation failed: canary failed and restored previous credential version",
				},
				{
					ID:      "01JSTALE",
					Time:    "2026-05-16T23:05:00Z",
					Type:    "secret.consumer_stale",
					Subject: schema.Target{Kind: "service", Name: "payments-api"},
					Summary: "orders-api still using old version of credential after roll",
				},
				{
					ID:      "01JCERT",
					Time:    "2026-05-16T23:06:00Z",
					Type:    "certificate.expiry",
					Subject: schema.Target{Kind: "service", Name: "payments-api"},
					Summary: "mTLS certificate expires in 48h and is near expiry",
				},
				{
					ID:      "01JKEY",
					Time:    "2026-05-16T23:07:00Z",
					Type:    "key.policy_mismatch",
					Subject: schema.Target{Kind: "service", Name: "payments-api"},
					Summary: "KMS key policy mismatch: missing grant for runner role",
				},
			},
		}},
	}, Options{Service: "payments-api"})
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	for _, code := range []string{
		"CAPACITY_RESOURCE_UNKNOWN",
		"TARGET_HEALTH_UNKNOWN",
		"LOG_DELIVERY_UNAVAILABLE",
		"METRIC_DELIVERY_UNAVAILABLE",
		"IAM_OR_SECRET_ACCESS_DENIED",
		"METRIC_GATE_FAILED",
		"RECENT_BAD_LOGS",
		"DESIRED_RELEASE_NOT_STABLE",
		"SECRET_ROTATION_FAILED",
		"SECRET_CONSUMER_STALE",
		"CERTIFICATE_EXPIRING",
		"KEY_POLICY_MISMATCH",
	} {
		assertFinding(t, result, code, "")
	}
	if result.Findings[0].Severity != SeverityHigh {
		t.Fatalf("findings are not severity-ranked: %+v", result.Findings)
	}
}

func TestDiagnoseMissingService(t *testing.T) {
	result, err := Diagnose(context.Background(), servicestatus.Result{
		Env:       "prod",
		Source:    "direct",
		Freshness: servicestatus.Freshness{Ready: true},
	}, Options{Service: "missing-api"})
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	assertFinding(t, result, "SERVICE_NOT_FOUND", SeverityHigh)
	if !hasAction(result, "missing-api_inspect_status", false) {
		t.Fatalf("missing inspect status action: %+v", result.RecommendedActions)
	}
}

func TestDiagnoseStatefulGroupFindingsAndActions(t *testing.T) {
	result, err := Diagnose(context.Background(), servicestatus.Result{
		Env:       "prod",
		Provider:  "aws",
		Region:    "us-west-2",
		Source:    "direct",
		Freshness: servicestatus.Freshness{Source: "direct_object_store", Ready: true},
		StatefulGroups: []servicestatus.StatefulGroup{{
			Group:          "orders-stream",
			Env:            "prod",
			Replicas:       1,
			Health:         "degraded",
			OperationID:    "saga_replace",
			OperationKind:  "stateful.replace_member",
			OperationState: "running",
			Operation: &servicestatus.Operation{
				ID:    "saga_replace",
				Kind:  "stateful.replace_member",
				State: "running",
			},
			Members: []servicestatus.StatefulMember{{
				Member:     0,
				Generation: 2,
				Phase:      "failed",
				Health:     "degraded",
				InstanceID: "",
				VolumeID:   "vol-0",
				DNSName:    "",
				UpdatedAt:  "2026-05-17T00:58:00Z",
				Findings: []servicestatus.Finding{
					{Code: "STATEFUL_MEMBER_NOT_READY", Summary: "orders-stream member 0 phase is failed"},
					{Code: "STATEFUL_MEMBER_INSTANCE_MISSING", Summary: "orders-stream member 0 instance provider ID is missing"},
				},
			}},
			Backups: []servicestatus.StatefulBackup{{
				BackupID:   "backup_old",
				Member:     0,
				VolumeID:   "vol-0",
				SnapshotID: "snap-old",
				ProviderID: "snap-old",
				Status:     "available",
				CreatedAt:  "2026-05-16T00:00:00Z",
				ExpiresAt:  "2026-05-16T12:00:00Z",
				Stale:      true,
			}},
			Findings: []servicestatus.Finding{
				{Code: "STATEFUL_MEMBER_NOT_READY", Summary: "orders-stream member 0 phase is failed"},
				{Code: "STATEFUL_MEMBER_INSTANCE_MISSING", Summary: "orders-stream member 0 instance provider ID is missing"},
				{Code: "STATEFUL_BACKUP_STALE", Summary: "orders-stream member 0 no available fresh backup was observed"},
			},
		}},
	}, Options{Service: "orders-stream", TraceID: "tr_stateful_doctor", Binary: "skiff"})
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if result.Health != "degraded" || result.Service != "orders-stream" || result.TraceID != "tr_stateful_doctor" {
		t.Fatalf("unexpected result metadata: %+v", result)
	}
	assertFinding(t, result, "STATEFUL_MEMBER_NOT_READY", SeverityHigh)
	assertFinding(t, result, "STATEFUL_MEMBER_INSTANCE_MISSING", SeverityHigh)
	assertFinding(t, result, "STATEFUL_BACKUP_STALE", SeverityHigh)
	if len(result.Facts) == 0 || len(result.Hypotheses) == 0 {
		t.Fatalf("facts or hypotheses missing: %+v", result)
	}
	for _, id := range []string{"orders-stream_stateful_status", "orders-stream_stateful_logs", "orders-stream_stateful_metrics"} {
		if !hasAction(result, id, false) {
			t.Fatalf("missing read-only stateful action %s: %+v", id, result.RecommendedActions)
		}
	}
	if !hasAction(result, "orders-stream_stateful_snapshot_member", true) || !hasAction(result, "orders-stream_stateful_replace_member", true) || !hasAction(result, "orders-stream_stateful_resume", true) {
		t.Fatalf("missing mutating stateful actions: %+v", result.RecommendedActions)
	}
	for _, action := range result.RecommendedActions {
		if !action.Mutating {
			continue
		}
		if action.Risk == "" || action.Reversibility == "" || action.Safety == "" {
			t.Fatalf("mutating action lacks safety metadata: %+v", action)
		}
		if action.ID == "orders-stream_stateful_replace_member" && (!action.RequiresApproval || action.Risk != schema.RiskHigh) {
			t.Fatalf("replacement action should require approval: %+v", action)
		}
	}
}

func assertFinding(t *testing.T, result Result, code string, severity Severity) {
	t.Helper()
	for _, finding := range result.Findings {
		if finding.Code != code {
			continue
		}
		if severity != "" && finding.Severity != severity {
			t.Fatalf("%s severity = %s, want %s", code, finding.Severity, severity)
		}
		return
	}
	t.Fatalf("missing finding %s in %+v", code, result.Findings)
}

func hasAction(result Result, id string, mutating bool) bool {
	for _, action := range result.RecommendedActions {
		if action.ID == id && action.Mutating == mutating {
			return true
		}
	}
	return false
}

func fixedTime() time.Time {
	return time.Date(2026, 5, 16, 23, 10, 0, 0, time.UTC)
}
