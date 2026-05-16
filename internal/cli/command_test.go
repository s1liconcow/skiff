package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigShowJSONDirectMode(t *testing.T) {
	clearSkiffEnv(t)

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"config", "show",
		"--format", "json",
		"--trace-id", "tr_config",
		"--mode", "direct",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--state", "s3://skiff-state-prod",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Config  struct {
			Mode        string `json:"mode"`
			Env         string `json:"env"`
			Provider    string `json:"provider"`
			Region      string `json:"region"`
			StateBucket string `json:"state_bucket"`
		} `json:"config"`
		Sources map[string]string `json:"sources"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("config show output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_config" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Config.Mode != "direct" || got.Config.StateBucket != "s3://skiff-state-prod" {
		t.Fatalf("unexpected config: %+v", got.Config)
	}
	if got.Sources["state_bucket"] != "flag" {
		t.Fatalf("state_bucket source = %q, want flag", got.Sources["state_bucket"])
	}
}

func TestConfigShowJSONValidationError(t *testing.T) {
	clearSkiffEnv(t)

	var stdout, stderr bytes.Buffer
	code := Run("skiff-runner", []string{
		"config", "show",
		"--format", "json",
		"--trace-id", "tr_bad",
		"--mode", "runner",
		"--env", "prod",
	}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d", code, ExitUserError)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty JSON-mode stderr", stderr.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		TraceID string `json:"trace_id"`
		Fields  []struct {
			Field string `json:"field"`
			Code  string `json:"code"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != "CONFIG_INVALID" || got.TraceID != "tr_bad" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
	found := false
	for _, field := range got.Fields {
		if field.Field == "state_bucket" && field.Code == "REQUIRED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing state_bucket required error: %+v", got.Fields)
	}
}

func TestStatePathJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"state", "path", "operation",
		"--format", "json",
		"--trace-id", "tr_path",
		"--service", "payments-api",
		"--operation", "op_01JABC",
		"--doc", "event",
		"--event", "01JABCDEF",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var got struct {
		OK      bool              `json:"ok"`
		TraceID string            `json:"trace_id"`
		Kind    string            `json:"kind"`
		Path    string            `json:"path"`
		Inputs  map[string]string `json:"inputs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("state path output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_path" || got.Kind != "operation" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	wantPath := "services/payments-api/operations/op_01JABC/events/01JABCDEF.json"
	if got.Path != wantPath {
		t.Fatalf("path = %q, want %q", got.Path, wantPath)
	}
	if got.Inputs["doc"] != "event" {
		t.Fatalf("doc input = %q, want event", got.Inputs["doc"])
	}
}

func TestStatePathJSONValidationError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"state", "path", "service",
		"--format", "json",
		"--trace-id", "tr_bad_path",
		"--service", "payments_api",
	}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d", code, ExitUserError)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty JSON-mode stderr", stderr.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		TraceID string `json:"trace_id"`
		Fields  []struct {
			Field string `json:"field"`
			Code  string `json:"code"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != "STATE_PATH_INVALID" || got.TraceID != "tr_bad_path" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
	if len(got.Fields) != 1 || got.Fields[0].Field != "service" || got.Fields[0].Code != "INVALID_NAME" {
		t.Fatalf("unexpected fields: %+v", got.Fields)
	}
}

func TestValidateJSONServiceExample(t *testing.T) {
	examplePath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"validate",
		examplePath,
		"--format", "json",
		"--trace-id", "tr_validate",
		"--show-defaulted",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Result  struct {
			OK   bool   `json:"ok"`
			Kind string `json:"kind"`
			Name string `json:"name"`
			Env  string `json:"env"`
		} `json:"result"`
		Spec struct {
			Machine struct {
				Arch string `json:"arch"`
			} `json:"machine"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("validate output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || !got.Result.OK || got.TraceID != "tr_validate" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Result.Kind != "Service" || got.Result.Name != "payments-api" || got.Result.Env != "prod" {
		t.Fatalf("unexpected result: %+v", got.Result)
	}
	if got.Spec.Machine.Arch != "x86_64" {
		t.Fatalf("defaulted arch = %q, want x86_64", got.Spec.Machine.Arch)
	}
}

func TestValidateYAMLShowsDefaults(t *testing.T) {
	examplePath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"validate",
		examplePath,
		"--format", "yaml",
		"--show-defaulted",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{"arch: x86_64", "shutdownGrace: 30s", "strategy: rolling"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("yaml output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestValidateJSONDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skiff.yaml")
	body := []byte(`apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: payments-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/payments-api:latest
runtime:
  port: 8080
  health:
    path: /healthz
network:
  ingress:
    type: public-http
    host: payments.example.com
`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write temp spec: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"validate", path, "--format", "json", "--trace-id", "tr_bad_spec"}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d", code, ExitUserError)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty JSON-mode stderr", stderr.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		TraceID string `json:"trace_id"`
		Fields  []struct {
			Path string `json:"path"`
			Code string `json:"code"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("validate error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != "SPEC_INVALID" || got.TraceID != "tr_bad_spec" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
	if !hasPathCode(got.Fields, "$.artifact.ref", "MUTABLE_ARTIFACT_REF") {
		t.Fatalf("missing mutable artifact diagnostic: %+v", got.Fields)
	}
	if !hasPathCode(got.Fields, "$.network.ingress.tls", "TLS_REQUIRED") {
		t.Fatalf("missing TLS diagnostic: %+v", got.Fields)
	}
}

func TestReleaseVerifyJSONGoldenRelease(t *testing.T) {
	goldenDir := filepath.Join("..", "..", "tests", "golden", "release")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"release", "verify",
		filepath.Join(goldenDir, "release.json"),
		"--runtime-manifest", filepath.Join(goldenDir, "runtime-manifest.json"),
		"--public-key", "local-test=25lf4lFp0UHKubu6krqgH58uHs599MsqwFGQ83/MH50=",
		"--service", "payments-api",
		"--env", "prod",
		"--now", "2026-05-16T18:00:00Z",
		"--format", "json",
		"--trace-id", "tr_release",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var got struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Result  struct {
			OK                bool   `json:"ok"`
			ReleaseID         string `json:"release_id"`
			Digest            string `json:"digest"`
			VerifiedSignature *struct {
				KeyID string `json:"key_id"`
			} `json:"verified_signature"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("release verify output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || !got.Result.OK || got.TraceID != "tr_release" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Result.ReleaseID != "rel_01JABC" || got.Result.Digest == "" {
		t.Fatalf("unexpected result: %+v", got.Result)
	}
	if got.Result.VerifiedSignature == nil || got.Result.VerifiedSignature.KeyID != "local-test" {
		t.Fatalf("verified signature = %+v", got.Result.VerifiedSignature)
	}
}

func TestReleaseVerifyJSONReportsWrongEnv(t *testing.T) {
	goldenDir := filepath.Join("..", "..", "tests", "golden", "release")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"release", "verify",
		filepath.Join(goldenDir, "release.json"),
		"--public-key", "local-test=25lf4lFp0UHKubu6krqgH58uHs599MsqwFGQ83/MH50=",
		"--service", "payments-api",
		"--env", "staging",
		"--now", "2026-05-16T18:00:00Z",
		"--format", "json",
	}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d", code, ExitUserError)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Result struct {
			OK       bool          `json:"ok"`
			Findings []codeFinding `json:"findings"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("release verify output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Result.OK {
		t.Fatalf("unexpected successful result: %+v", got)
	}
	if !hasCode(got.Result.Findings, "ENV_MISMATCH") {
		t.Fatalf("findings = %+v, want ENV_MISMATCH", got.Result.Findings)
	}
}

func TestObjectVerifyJSONGoldenRelease(t *testing.T) {
	goldenDir := filepath.Join("..", "..", "tests", "golden", "release")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"object", "verify",
		filepath.Join(goldenDir, "release.json"),
		"--public-key", "local-test=25lf4lFp0UHKubu6krqgH58uHs599MsqwFGQ83/MH50=",
		"--format", "json",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Result struct {
			OK     bool   `json:"ok"`
			Digest string `json:"digest"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("object verify output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || !got.Result.OK || got.Result.Digest == "" {
		t.Fatalf("unexpected object verify result: %+v", got)
	}
}

type codeFinding struct {
	Code string `json:"code"`
}

func hasCode(items []codeFinding, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}

func hasPathCode(items []struct {
	Path string `json:"path"`
	Code string `json:"code"`
}, path, code string) bool {
	for _, item := range items {
		if item.Path == path && item.Code == code {
			return true
		}
	}
	return false
}

func clearSkiffEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SKIFF_CONFIG",
		"SKIFF_ENV",
		"SKIFF_PROVIDER",
		"SKIFF_REGION",
		"SKIFF_STATE_BUCKET",
		"SKIFF_KMS_KEY",
		"SKIFF_AUTH_MODE",
		"SKIFF_LOG_LEVEL",
		"SKIFF_MODE",
		"SKIFF_API_URL",
		"SKIFF_SERVICE",
		"SKIFF_CONTROL_KEY",
	} {
		t.Setenv(key, "")
	}
}
