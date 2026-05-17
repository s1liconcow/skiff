package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/s1liconcow/skiff/internal/agent"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	servicedoctor "github.com/s1liconcow/skiff/internal/doctor"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestSolveJSONUsesDoctorClientAndEmitsActionGraph(t *testing.T) {
	clearSkiffEnv(t)
	fake := &fakeDoctorClient{doctor: &client.Doctor{
		TraceID: "tr_solve",
		Service: "payments-api",
		Env:     "prod",
		Health:  "degraded",
		Findings: []servicedoctor.Finding{{
			ID:         "payments-api_rollout_failed_or_degraded",
			Code:       "ROLLOUT_FAILED_OR_DEGRADED",
			Severity:   servicedoctor.SeverityHigh,
			Service:    "payments-api",
			Summary:    "rollout failed",
			Confidence: 0.84,
		}},
		RecommendedActions: []servicedoctor.RecommendedAction{
			{ID: "payments-api_inspect_logs", Kind: "command", Service: "payments-api", Command: "skiff logs payments-api --since 20m --format json", Mutating: false},
			{ID: "payments-api_rollback_to_stable", Kind: "command", Service: "payments-api", Command: "skiff rollback payments-api --to rel_01 --yes --format json", Mutating: true, Risk: schema.RiskMedium, Reversibility: schema.Reversible},
		},
	}}
	oldNewSolveClient := newSolveClient
	newSolveClient = func(cfg config.Config, opts client.Options) (client.Interface, error) {
		if cfg.Env != "prod" || cfg.Provider != "aws" || cfg.Region != "us-west-2" {
			return nil, errors.New("unexpected config")
		}
		return fake, nil
	}
	t.Cleanup(func() {
		newSolveClient = oldNewSolveClient
	})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"solve", "payments-api",
		"--goal", "restore-health",
		"--direct",
		"--state", "file://" + t.TempDir(),
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--fresh",
		"--format", "json",
		"--trace-id", "tr_solve",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got solveOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("solve output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_solve" || got.Goal != agent.GoalRestoreHealth || got.Status != agent.StatusPlanReady {
		t.Fatalf("unexpected solve output: %+v", got)
	}
	if len(got.Steps) != 3 || got.Steps[1].ID != "payments-api_rollback_to_stable" || got.Steps[2].ID != "verify_health" {
		t.Fatalf("unexpected action graph steps: %+v", got.Steps)
	}
	if got.Steps[1].APIOperation == nil || got.Steps[1].APIOperation.Operation != "rollback.start" {
		t.Fatalf("rollback step missing API operation: %+v", got.Steps[1])
	}
	if fake.opts.Service != "payments-api" || !fake.opts.Fresh || fake.opts.TraceID != "tr_solve" {
		t.Fatalf("doctor opts not propagated: %+v", fake.opts)
	}
}
