package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestCutoverDryRunJSON(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"cutover", "payments-api",
		"--env", "prod",
		"--from", "kube",
		"--to", "skiff",
		"--percent", "25",
		"--dry-run",
		"--format", "json",
		"--trace-id", "tr_cutover",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got cutoverOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_cutover" || got.Result.Service != "payments-api" || got.Result.Percent != 25 {
		t.Fatalf("unexpected output: %+v", got)
	}
	if got.Result.Status != schema.SagaPending || got.Result.Risk != schema.RiskMedium || got.Result.Reversibility != schema.Compensatable {
		t.Fatalf("unexpected cutover classification: %+v", got.Result)
	}
	if len(got.Result.Paths) != 0 || len(got.Result.NextCommands) == 0 {
		t.Fatalf("dry-run should include next commands but not paths: %+v", got.Result)
	}
}
