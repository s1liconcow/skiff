package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestFailoverDryRunJSONIncludesIrreversiblePlan(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"failover", "orders",
		"--database", "orders-db",
		"--from-region", "us-west-2",
		"--to-region", "us-east-1",
		"--dry-run",
		"--direct",
		"--state", "memory://failover-dry-run",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_failover_plan",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got failoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("failover output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_failover_plan" || got.Result.Status != schema.SagaPending || got.Result.NextAction != "create_saga" {
		t.Fatalf("unexpected failover output: %+v", got)
	}
	var sawBoundary bool
	for _, node := range got.Result.Plan.Graph.Nodes {
		if node.ID == "writes-after-promotion-boundary" && node.Reversibility == schema.Irreversible {
			sawBoundary = true
		}
	}
	if !sawBoundary {
		t.Fatalf("dry-run plan did not include irreversible boundary: %+v", got.Result.Plan.Graph.Nodes)
	}
}
