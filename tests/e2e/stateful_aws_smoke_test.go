package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWSStatefulE2ESmokeGatesAndReport(t *testing.T) {
	if os.Getenv("SKIFF_AWS_STATEFUL_E2E") != "1" {
		t.Skip("set SKIFF_AWS_STATEFUL_E2E=1 to run the AWS StatefulGroup e2e smoke gates")
	}
	resetSkiffEnv(t)

	prefix := strings.TrimSpace(os.Getenv("SKIFF_AWS_STATEFUL_E2E_PREFIX"))
	if prefix == "" {
		t.Skip("SKIFF_AWS_STATEFUL_E2E_PREFIX is required for unique report naming")
	}
	traceID := "tr_aws_stateful_e2e_" + awsE2EID(prefix)
	report := newE2EReport(t, "aws-stateful", "orders-stream", "prod", traceID)
	report.CleanupStatus = "not required; StatefulGroup AWS smoke is read-only plan/explain coverage"
	report.fact("aws_stateful_gate", "AWS StatefulGroup e2e explicitly enabled with isolated prefix "+prefix)
	defer writeE2EReport(t, report)

	specPath := filepath.Join("..", "..", "examples", "stateful", "jetstream", "skiff.yaml")
	var ok basicOK
	decodeCLIJSON(t, runSkiffCLI(t, report, "validate", specPath, "--format", "json", "--trace-id", traceID), &ok)
	if !ok.OK {
		t.Fatal("StatefulGroup validate returned ok=false")
	}

	var explained localExplainOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "explain", specPath, "--provider", "aws", "--format", "json", "--trace-id", traceID), &explained)
	if !explained.OK || explained.Result.Service != "orders-stream" || len(explained.Result.Resources) == 0 {
		t.Fatalf("unexpected AWS StatefulGroup explain output: %+v", explained)
	}
	report.fact("aws_stateful_explain", "StatefulGroup AWS read-only explain exposed member VM, volume, DNS, recipe, snapshot, and update primitives")

	var planned localPlanOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "stateful", "plan", specPath, "--provider", "aws", "--format", "json", "--trace-id", traceID), &planned)
	if !planned.OK || planned.Plan.Service != "orders-stream" || len(planned.Plan.Resources) == 0 {
		t.Fatalf("unexpected AWS StatefulGroup plan output: %+v", planned)
	}
	for _, resource := range planned.Plan.Resources {
		report.addProviderID(resource.ProviderID)
	}
	report.fact("aws_stateful_plan", "StatefulGroup AWS read-only plan completed without writing object state or creating cloud resources")
}
