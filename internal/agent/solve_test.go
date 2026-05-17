package agent

import (
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/doctor"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestSolveFailedRolloutPlansDiagnosticsRollbackAndVerify(t *testing.T) {
	result := doctor.Result{
		TraceID: "tr_solve",
		Service: "payments-api",
		Health:  "degraded",
		Findings: []doctor.Finding{{
			ID:         "payments-api_rollout_failed_or_degraded",
			Code:       "ROLLOUT_FAILED_OR_DEGRADED",
			Severity:   doctor.SeverityHigh,
			Service:    "payments-api",
			Summary:    "rollout failed",
			Confidence: 0.84,
		}},
		RecommendedActions: []doctor.RecommendedAction{
			{
				ID:            "payments-api_rollback_to_stable",
				Kind:          "command",
				Service:       "payments-api",
				Command:       "skiff rollback payments-api --to rel_stable --yes --format json",
				Mutating:      true,
				Risk:          schema.RiskMedium,
				Reversibility: schema.Reversible,
				Safety:        "prefer this before destructive repair when a stable release exists",
			},
			{
				ID:       "payments-api_watch_rollout",
				Kind:     "command",
				Service:  "payments-api",
				Command:  "skiff rollout watch payments-api --operation op_123 --provider-id asg-123 --format json",
				Mutating: true,
				Risk:     schema.RiskLow,
			},
			{
				ID:       "payments-api_inspect_logs",
				Kind:     "command",
				Service:  "payments-api",
				Command:  "skiff logs payments-api --since 20m --format json",
				Mutating: false,
			},
		},
	}

	graph := Solve(result, SolveOptions{Goal: GoalRestoreHealth, Binary: "skiff"})

	if graph.Status != StatusPlanReady || graph.Confidence != 0.84 {
		t.Fatalf("unexpected graph status/confidence: %+v", graph)
	}
	wantIDs := []string{"payments-api_inspect_logs", "payments-api_watch_rollout", "payments-api_rollback_to_stable", "verify_health"}
	if len(graph.Steps) != len(wantIDs) {
		t.Fatalf("steps = %+v, want ids %v", graph.Steps, wantIDs)
	}
	for i, want := range wantIDs {
		if graph.Steps[i].ID != want {
			t.Fatalf("step %d id = %q, want %q", i, graph.Steps[i].ID, want)
		}
	}
	rollback := graph.Steps[2]
	if !rollback.Mutating || rollback.Risk != schema.RiskMedium || rollback.Reversibility != schema.Reversible || !rollback.Reversible || rollback.RequiresApproval {
		t.Fatalf("rollback metadata = %+v", rollback)
	}
	if !strings.Contains(rollback.Command, "--yes") {
		t.Fatalf("safe rollback command lost --yes: %q", rollback.Command)
	}
	if len(rollback.Requires) != 2 || rollback.Requires[0] != "payments-api_inspect_logs" || rollback.Requires[1] != "payments-api_watch_rollout" {
		t.Fatalf("rollback dependencies = %v", rollback.Requires)
	}
	if rollback.APIOperation == nil || rollback.APIOperation.Operation != "rollback.start" || rollback.APIOperation.Params["target_release"] != "rel_stable" {
		t.Fatalf("rollback api operation = %+v", rollback.APIOperation)
	}
	verify := graph.Steps[3]
	if verify.Mutating || len(verify.Requires) != 2 || verify.Requires[0] != "payments-api_watch_rollout" || verify.Requires[1] != "payments-api_rollback_to_stable" {
		t.Fatalf("verify step = %+v", verify)
	}
}

func TestSolveMissingCapacityProducesReadOnlyPlan(t *testing.T) {
	result := doctor.Result{
		Service: "payments-api",
		Health:  "degraded",
		Findings: []doctor.Finding{{
			ID:         "payments-api_capacity_resource_unknown",
			Code:       "CAPACITY_RESOURCE_UNKNOWN",
			Severity:   doctor.SeverityHigh,
			Service:    "payments-api",
			Summary:    "capacity unknown",
			Confidence: 0.82,
		}},
		RecommendedActions: []doctor.RecommendedAction{
			{
				ID:       "payments-api_inspect_events",
				Kind:     "command",
				Service:  "payments-api",
				Command:  "skiff events --scope service --service payments-api --limit 20 --fresh --format json",
				Mutating: false,
			},
			{
				ID:       "payments-api_inspect_status",
				Kind:     "command",
				Service:  "payments-api",
				Command:  "skiff status payments-api --fresh --format json",
				Mutating: false,
			},
		},
	}

	graph := Solve(result, SolveOptions{Goal: GoalRestoreHealth, Binary: "skiff"})

	if graph.Status != StatusPlanReady || len(graph.Steps) != 2 {
		t.Fatalf("unexpected read-only graph: %+v", graph)
	}
	for _, step := range graph.Steps {
		if step.Mutating || step.RequiresApproval || step.Risk != schema.RiskLow || step.Reversibility != schema.Reversible {
			t.Fatalf("read-only step has unsafe metadata: %+v", step)
		}
		if len(step.Requires) != 0 {
			t.Fatalf("read-only step should have no dependencies: %+v", step)
		}
	}
}

func TestSolveLogsUnavailableProducesReadOnlyLogInspection(t *testing.T) {
	result := doctor.Result{
		Service: "payments-api",
		Health:  "warning",
		Findings: []doctor.Finding{{
			ID:         "payments-api_log_delivery_unavailable",
			Code:       "LOG_DELIVERY_UNAVAILABLE",
			Severity:   doctor.SeverityMedium,
			Service:    "payments-api",
			Summary:    "logs unavailable",
			Confidence: 0.76,
		}},
		RecommendedActions: []doctor.RecommendedAction{{
			ID:       "payments-api_inspect_logs",
			Kind:     "command",
			Service:  "payments-api",
			Command:  "skiff logs payments-api --since 20m --format json",
			Mutating: false,
		}},
	}

	graph := Solve(result, SolveOptions{Goal: GoalRestoreHealth, Binary: "skiff"})

	if graph.Status != StatusPlanReady || len(graph.Steps) != 1 {
		t.Fatalf("unexpected logs graph: %+v", graph)
	}
	step := graph.Steps[0]
	if step.APIOperation == nil || step.APIOperation.Operation != "logs.query" || step.APIOperation.Params["since"] != "20m" {
		t.Fatalf("logs api operation = %+v", step.APIOperation)
	}
}

func TestSolveHighRiskIrreversibleActionRequiresApprovalAndStripsYes(t *testing.T) {
	result := doctor.Result{
		Service: "payments-api",
		Health:  "critical",
		Findings: []doctor.Finding{{
			ID:         "payments-api_manual_state_repair_required",
			Code:       "MANUAL_STATE_REPAIR_REQUIRED",
			Severity:   doctor.SeverityCritical,
			Service:    "payments-api",
			Summary:    "manual repair required",
			Confidence: 0.9,
		}},
		RecommendedActions: []doctor.RecommendedAction{{
			ID:            "payments-api_replace_state",
			Kind:          "command",
			Service:       "payments-api",
			Command:       "skiff state replace payments-api --yes --format json",
			Mutating:      true,
			Risk:          schema.RiskCritical,
			Reversibility: schema.Irreversible,
		}},
	}

	graph := Solve(result, SolveOptions{Goal: GoalRestoreHealth, Binary: "skiff"})

	if graph.Status != StatusApprovalRequired || len(graph.Steps) != 2 {
		t.Fatalf("unexpected approval graph: %+v", graph)
	}
	step := graph.Steps[0]
	if !step.RequiresApproval || step.Risk != schema.RiskCritical || step.Reversibility != schema.Irreversible || step.Reversible {
		t.Fatalf("high-risk step metadata = %+v", step)
	}
	if strings.Contains(step.Command, "--yes") {
		t.Fatalf("approval-required command kept --yes: %q", step.Command)
	}
	if graph.Steps[1].ID != "verify_health" || graph.Steps[1].Requires[0] != step.ID {
		t.Fatalf("approval verify step = %+v", graph.Steps[1])
	}
}

func TestSolveNominalNoAction(t *testing.T) {
	graph := Solve(doctor.Result{Service: "payments-api", Health: "nominal"}, SolveOptions{Goal: GoalRestoreHealth, Binary: "skiff"})

	if graph.Status != StatusNoAction || graph.Confidence != 1 || len(graph.Steps) != 0 {
		t.Fatalf("unexpected no-action graph: %+v", graph)
	}
}
