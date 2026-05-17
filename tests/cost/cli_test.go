package cost_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/cli"
	"github.com/s1liconcow/skiff/internal/cost"
)

func TestCostExplainJSON(t *testing.T) {
	specPath := writeServiceSpec(t, "medium", 12, 24)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "explain", "payments-api",
		"--file", specPath,
		"--cpu-p95", "18",
		"--memory-p95", "41",
		"--request-count", "10300000",
		"--warm-capacity", "8",
		"--log-mb-per-hour", "1400",
		"--format", "json",
		"--trace-id", "tr_cost",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var got struct {
		OK      bool        `json:"ok"`
		TraceID string      `json:"trace_id"`
		Result  cost.Result `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode cost explain: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_cost" || got.Result.Service != "payments-api" || got.Result.Shape.Name != "medium" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	for _, id := range []string{"cost.shape.downsize", "cost.replicas.reduce_min", "cost.logs.noisy"} {
		if !hasRecommendation(got.Result.Recommendations, id) {
			t.Fatalf("missing recommendation %s in %+v", id, got.Result.Recommendations)
		}
	}
}

func TestCostExplainHuman(t *testing.T) {
	specPath := writeServiceSpec(t, "medium", 12, 24)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "explain", "payments-api",
		"--file", specPath,
		"--cpu-p95", "18",
		"--memory-p95", "41",
		"--warm-capacity", "8",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"cost advisor for payments-api/prod",
		"shape: medium",
		"cpu_p95: 18.0 %",
		"cost.shape.downsize",
		"cost.replicas.reduce_min",
		"recommendations are relative shape and capacity guidance, not billing truth",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output missing %q:\n%s", want, out)
		}
	}
}

func TestCostExplainRejectsInvalidMetricJSON(t *testing.T) {
	specPath := writeServiceSpec(t, "small", 1, 2)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"cost", "explain", "payments-api",
		"--file", specPath,
		"--cpu-p95", "120",
		"--format", "json",
		"--trace-id", "tr_bad_cost",
	}, &stdout, &stderr)
	if code != cli.ExitUserError {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Summary string `json:"summary"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode error JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.TraceID != "tr_bad_cost" || !strings.Contains(got.Summary, "--cpu-p95") {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
}

func TestPlanJSONIncludesAdvisorWarnings(t *testing.T) {
	specPath := writeServiceSpec(t, "large", 8, 8)
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{
		"plan", specPath,
		"--provider", "aws",
		"--region", "us-west-2",
		"--state", "s3://skiff-state-prod",
		"--format", "json",
		"--trace-id", "tr_plan_cost",
	}, &stdout, &stderr)
	if code != cli.ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got struct {
		OK              bool                  `json:"ok"`
		TraceID         string                `json:"trace_id"`
		AdvisorWarnings []cost.Recommendation `json:"advisor_warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_plan_cost" {
		t.Fatalf("unexpected plan envelope: %+v", got)
	}
	for _, id := range []string{"cost.plan.high_min_replicas", "cost.plan.large_shape", "cost.plan.fixed_warm_capacity"} {
		if !hasRecommendation(got.AdvisorWarnings, id) {
			t.Fatalf("missing advisor warning %s in %+v", id, got.AdvisorWarnings)
		}
	}
}

func writeServiceSpec(t *testing.T, machine string, min, max int) string {
	t.Helper()
	body := strings.ReplaceAll(serviceSpecTemplate, "{{MACHINE}}", machine)
	body = strings.ReplaceAll(body, "{{MIN}}", strconv.Itoa(min))
	body = strings.ReplaceAll(body, "{{MAX}}", strconv.Itoa(max))
	path := filepath.Join(t.TempDir(), "skiff.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

func hasRecommendation(recs []cost.Recommendation, id string) bool {
	for _, rec := range recs {
		if rec.ID == id {
			return true
		}
	}
	return false
}

const serviceSpecTemplate = `apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: payments-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/payments-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
runtime:
  port: 8080
  command:
    - ./app
    - serve
  health:
    path: /healthz
machine:
  size: {{MACHINE}}
scale:
  min: {{MIN}}
  max: {{MAX}}
network:
  ingress:
    type: private
`
