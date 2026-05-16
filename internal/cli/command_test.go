package cli

import (
	"bytes"
	"encoding/json"
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
