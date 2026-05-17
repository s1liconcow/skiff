package cli

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAuthzExplainJSONDeniedWithApprovalRequirement(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"authz", "explain",
		"--action", "restore",
		"--service", "payments-api",
		"--env", "prod",
		"--risk", "high",
		"--actor-id", "agent-one",
		"--actor-type", "agent",
		"--format", "json",
		"--trace-id", "tr_authz",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var got struct {
		OK       bool   `json:"ok"`
		TraceID  string `json:"trace_id"`
		Decision struct {
			Allowed          bool     `json:"allowed"`
			RequiresApproval bool     `json:"requires_approval"`
			ApprovalRole     string   `json:"approval_role"`
			Denials          []string `json:"denials"`
		} `json:"decision"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("authz explain output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_authz" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Decision.Allowed || !got.Decision.RequiresApproval || got.Decision.ApprovalRole != "database-admin" || len(got.Decision.Denials) == 0 {
		t.Fatalf("unexpected decision: %+v", got.Decision)
	}
}
