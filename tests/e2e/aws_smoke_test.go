package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWSE2ESmokeGatesAndReport(t *testing.T) {
	if os.Getenv("SKIFF_AWS_E2E") != "1" {
		t.Skip("set SKIFF_AWS_E2E=1 to run the AWS e2e smoke gates")
	}
	resetSkiffEnv(t)

	stateURI := strings.TrimSpace(os.Getenv("SKIFF_AWS_E2E_STATE"))
	region := strings.TrimSpace(os.Getenv("SKIFF_AWS_E2E_REGION"))
	prefix := strings.TrimSpace(os.Getenv("SKIFF_AWS_E2E_PREFIX"))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION"))
	}
	if stateURI == "" || region == "" || prefix == "" {
		t.Skip("SKIFF_AWS_E2E_STATE, SKIFF_AWS_E2E_REGION or AWS_REGION, and SKIFF_AWS_E2E_PREFIX are required")
	}

	const (
		service = "http-hello"
		env     = "prod"
		traceID = "tr_aws_e2e"
	)
	report := newE2EReport(t, "aws", service, env, traceID)
	report.CleanupStatus = "not started"
	report.fact("aws_gate", "AWS e2e explicitly enabled with isolated prefix "+prefix)
	defer writeE2EReport(t, report)

	specPath := filepath.Join("..", "..", "examples", "service", "http-hello", "skiff.yaml")
	var plan localPlanOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "plan", specPath, "--provider", "aws", "--region", region, "--state", stateURI, "--format", "json", "--trace-id", traceID), &plan)
	if !plan.OK || len(plan.Plan.Resources) == 0 {
		t.Fatalf("unexpected AWS plan gate output: %+v", plan)
	}
	for _, resource := range plan.Plan.Resources {
		report.addProviderID(resource.ProviderID)
	}

	var explained localExplainOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "explain", specPath, "--provider", "aws", "--region", region, "--state", stateURI, "--release-id", prefix+"-release", "--format", "json", "--trace-id", traceID), &explained)
	if !explained.OK || len(explained.Result.Resources) == 0 {
		t.Fatalf("unexpected AWS explain gate output: %+v", explained)
	}

	if os.Getenv("SKIFF_AWS_E2E_LIVE_APPLY") != "1" {
		report.fact("aws_live_apply", "skipped because SKIFF_AWS_E2E_LIVE_APPLY is not 1")
		t.Skip("AWS live apply is gated by SKIFF_AWS_E2E_LIVE_APPLY=1; plan/explain smoke gates passed")
	}
	missing := missingAWSLiveShapeEnv()
	if len(missing) > 0 {
		report.fact("aws_live_apply_preflight", "missing AWS live-shape inputs: "+strings.Join(missing, ", "))
		t.Skip("AWS live apply requires " + strings.Join(missing, ", "))
	}
	report.fact("aws_live_apply_preflight", "AWS live-shape inputs are present")
	report.fact("aws_live_apply", "requested but real AWS apply adapters are not linked into this provider build")
	t.Skip("real AWS apply/discovery adapters are not available in this build; tracked as an explicit matrix gap")
}

func missingAWSLiveShapeEnv() []string {
	required := []string{
		"SKIFF_AWS_VPC_ID",
		"SKIFF_AWS_SUBNET_IDS",
		"SKIFF_AWS_AMI_ID",
	}
	var missing []string
	for _, name := range required {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}
