package e2e_test

import (
	"crypto/ed25519"
	"encoding/base64"
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

	service := awsE2EServiceName(prefix)
	const env = "prod"
	traceID := "tr_aws_e2e_" + awsE2EID(prefix)
	report := newE2EReport(t, "aws", service, env, traceID)
	report.CleanupStatus = "not started"
	report.fact("aws_gate", "AWS e2e explicitly enabled with isolated prefix "+prefix)
	defer writeE2EReport(t, report)

	specPath := awsE2ESpec(t, filepath.Join("..", "..", "examples", "service", "http-hello", "skiff.yaml"), service)
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
	report.CleanupStatus = "live AWS resources require explicit cleanup by tags skiff.dev/service=" + service + " skiff.dev/env=" + env
	report.RecommendedNextCommands = append(report.RecommendedNextCommands,
		"aws resourcegroupstaggingapi get-resources --tag-filters Key=skiff.dev/service,Values="+service+" Key=skiff.dev/env,Values="+env,
		"skiff status "+service+" --direct --state "+stateURI+" --env "+env+" --provider aws --region "+region+" --aws-live-apply --format json --trace-id "+traceID,
	)

	signingSeed := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("A", ed25519.SeedSize)))
	var deployed localDeployOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"deploy", specPath,
		"--direct", "--state", stateURI, "--env", env, "--provider", "aws", "--region", region,
		"--aws-live-apply",
		"--aws-vpc-id", strings.TrimSpace(os.Getenv("SKIFF_AWS_VPC_ID")),
		"--aws-subnet-ids", strings.TrimSpace(os.Getenv("SKIFF_AWS_SUBNET_IDS")),
		"--aws-ami-id", strings.TrimSpace(os.Getenv("SKIFF_AWS_AMI_ID")),
		"--release-id", "rel-"+awsE2EID(prefix),
		"--operation-id", "op-"+awsE2EID(prefix),
		"--key-id", "aws-e2e",
		"--signing-seed-base64", signingSeed,
		"--format", "json",
		"--trace-id", traceID,
	), &deployed)
	if !deployed.OK || !deployed.Result.OK || deployed.Result.OperationID == "" {
		t.Fatalf("unexpected AWS live deploy output: %+v", deployed)
	}
	report.addOperationID(deployed.Result.OperationID)
	for _, resource := range deployed.Result.Plan.Resources {
		report.addProviderID(resource.ProviderID)
	}

	var status awsLiveStatusOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"status", service,
		"--direct", "--state", stateURI, "--env", env, "--provider", "aws", "--region", region,
		"--aws-live-apply",
		"--format", "json",
		"--trace-id", traceID,
	), &status)
	if !status.OK || len(status.Status.Services) == 0 {
		t.Fatalf("unexpected AWS live status output: %+v", status)
	}
	for _, resource := range status.Status.Services[0].Resources {
		report.addProviderID(resource.ProviderID)
	}
	report.fact("aws_live_apply", "live AWS apply completed and direct status observed resource records")
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

type awsLiveStatusOutput struct {
	OK     bool `json:"ok"`
	Status struct {
		Services []struct {
			Service   string `json:"service"`
			Resources []struct {
				ProviderID string `json:"provider_id"`
			} `json:"resources"`
		} `json:"services"`
	} `json:"status"`
}

func awsE2ESpec(t *testing.T, basePath, service string) string {
	t.Helper()
	body, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("read AWS e2e spec: %v", err)
	}
	rendered := strings.Replace(string(body), "  name: http-hello\n", "  name: "+service+"\n", 1)
	if rendered == string(body) {
		t.Fatal("AWS e2e spec did not contain expected service name")
	}
	path := filepath.Join(t.TempDir(), "skiff.yaml")
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
		t.Fatalf("write AWS e2e spec: %v", err)
	}
	return path
}

func awsE2EServiceName(prefix string) string {
	id := awsE2EID(prefix)
	if id == "" {
		id = "live"
	}
	return id + "-http-hello"
}

func awsE2EID(value string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "aws-e2e"
	}
	if len(out) > 24 {
		out = strings.Trim(out[:24], "-")
	}
	if out == "" {
		return "aws-e2e"
	}
	return out
}
