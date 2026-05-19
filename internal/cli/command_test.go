package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/bootstrap"
	"github.com/s1liconcow/skiff/internal/config"
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestCommandsSupportHelpFlags(t *testing.T) {
	paths := [][]string{
		nil,
		{"adopt"},
		{"adopt", "terraform"},
		{"authz"},
		{"authz", "explain"},
		{"bootstrap"},
		{"bootstrap", "aws"},
		{"ci"},
		{"ci", "generate"},
		{"compile"},
		{"completion"},
		{"config"},
		{"config", "show"},
		{"config", "get-contexts"},
		{"config", "current-context"},
		{"config", "use-context"},
		{"contract"},
		{"contract", "test"},
		{"cost"},
		{"cost", "explain"},
		{"cutover"},
		{"database"},
		{"database", "backup"},
		{"database", "restore"},
		{"debug"},
		{"debug", "collect"},
		{"debug", "shell"},
		{"debug", "port-forward"},
		{"debug", "command"},
		{"deploy"},
		{"doctor"},
		{"drift"},
		{"events"},
		{"explain"},
		{"failover"},
		{"gc"},
		{"gc", "plan"},
		{"gc", "apply"},
		{"import"},
		{"import", "kube"},
		{"init"},
		{"init", "stack"},
		{"init", "stack", "api-database"},
		{"init", "stack", "api-sqlite"},
		{"logs"},
		{"metrics"},
		{"object"},
		{"object", "verify"},
		{"ops"},
		{"ops", "list"},
		{"ops", "inspect"},
		{"ops", "events"},
		{"ops", "resume"},
		{"ops", "watch"},
		{"ops", "approve"},
		{"ops", "reject"},
		{"ops", "cancel"},
		{"ops", "compensate"},
		{"plan"},
		{"plugin"},
		{"plugin", "list"},
		{"plugin", "validate"},
		{"plugin", "explain"},
		{"plugin", "dev"},
		{"policy"},
		{"policy", "explain"},
		{"promote"},
		{"release"},
		{"release", "promote"},
		{"release", "verify"},
		{"release", "candidate"},
		{"release", "candidate", "create"},
		{"release", "candidate", "show"},
		{"rollback"},
		{"rollout"},
		{"rollout", "watch"},
		{"rotate"},
		{"rotate", "cert"},
		{"rotate", "key"},
		{"rotate", "secret"},
		{"saga"},
		{"saga", "inspect"},
		{"saga", "approve"},
		{"saga", "reject"},
		{"saga", "start"},
		{"saga", "resume"},
		{"saga", "watch"},
		{"saga", "cancel"},
		{"saga", "compensate"},
		{"solve"},
		{"state"},
		{"state", "path"},
		{"stateful"},
		{"stateful", "plan"},
		{"stateful", "apply"},
		{"stateful", "inspect"},
		{"stateful", "status"},
		{"stateful", "doctor"},
		{"stateful", "solve"},
		{"stateful", "logs"},
		{"stateful", "metrics"},
		{"stateful", "update-release"},
		{"stateful", "replace-member"},
		{"stateful", "snapshot"},
		{"stateful", "backup"},
		{"stateful", "backup", "plan"},
		{"stateful", "restore"},
		{"stateful", "restore", "plan"},
		{"stateful", "restore", "apply"},
		{"stateful", "resume"},
		{"stateful", "watch"},
		{"stateful", "cancel"},
		{"stateful", "compensate"},
		{"status"},
		{"terraform"},
		{"terraform", "generate"},
		{"tui"},
		{"validate"},
		{"version"},
		{"help", "workflows"},
		{"help", "adoption"},
		{"help", "dev"},
		{"help", "all"},
		{"help", "flags"},
	}

	for _, helpFlag := range []string{"-h", "--help"} {
		for _, path := range paths {
			name := strings.Join(append(append([]string{}, path...), helpFlag), " ")
			if name == "" {
				name = helpFlag
			}
			t.Run(name, func(t *testing.T) {
				args := append(append([]string{}, path...), helpFlag)
				var stdout, stderr bytes.Buffer
				code := Run("skiff", args, &stdout, &stderr)
				if code != ExitSuccess {
					t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
				}
				if stderr.Len() != 0 {
					t.Fatalf("stderr = %q, want empty", stderr.String())
				}
				if !strings.Contains(stdout.String(), "Usage") {
					t.Fatalf("stdout does not look like help output:\n%s", stdout.String())
				}
			})
		}
	}
}

func TestRootHelpUsesCuratedCommandList(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"help"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{"  deploy", "  ops", "  cost", "skiff help workflows", "skiff help all"} {
		if !strings.Contains(out, want) {
			t.Fatalf("root help missing %q:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"  authz", "  object", "  state ", "  terraform", "  rollout"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("root help should not show niche command %q:\n%s", hidden, out)
		}
	}
}

func TestVersionJSONPretty(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		wantColor bool
	}{
		{
			name:      "command flag",
			args:      []string{"version", "--format", "json-pretty", "--trace-id", "tr_pretty"},
			wantColor: true,
		},
		{
			name:      "global flag",
			args:      []string{"--format", "json-pretty", "version", "--trace-id", "tr_pretty_global"},
			wantColor: true,
		},
		{
			name:      "no color",
			args:      []string{"version", "--format", "json-pretty", "--no-color", "--trace-id", "tr_pretty_plain"},
			wantColor: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run("skiff", tc.args, &stdout, &stderr)
			if code != ExitSuccess {
				t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			if strings.Contains(stdout.String(), "\x1b[") != tc.wantColor {
				t.Fatalf("color presence = %t, want %t\n%s", strings.Contains(stdout.String(), "\x1b["), tc.wantColor, stdout.String())
			}
			stripped := ansiEscapeRE.ReplaceAll(stdout.Bytes(), nil)
			if !bytes.Contains(stripped, []byte("\n  \"ok\"")) {
				t.Fatalf("output was not pretty printed:\n%s", stripped)
			}
			var got versionOutput
			if err := json.Unmarshal(stripped, &got); err != nil {
				t.Fatalf("stripped output is not valid JSON: %v\n%s", err, stripped)
			}
			if !got.OK || got.Binary != "skiff" {
				t.Fatalf("unexpected version output: %+v", got)
			}
		})
	}
}

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

func TestConfigContextsCommands(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, ".skiffconfig")
	if err := os.WriteFile(path, []byte(`
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: local
contexts:
  - name: local
    context:
      mode: direct
      env: prod
      provider: fake
      region: local
      state: file:///tmp/skiff-state
  - name: prod
    context:
      mode: direct
      env: prod
      provider: aws
      region: us-west-2
      state: s3://skiff-state-prod
`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"config", "use-context", "prod", "--config", path, "--format", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("use-context exit = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run("skiff", []string{"config", "current-context", "--config", path, "--format", "human"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("current-context exit = %d, stderr = %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "prod" {
		t.Fatalf("current-context output = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run("skiff", []string{"config", "show", "--config", path, "--format", "json"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("show exit = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		Context string `json:"context"`
		Config  struct {
			Provider    string `json:"provider"`
			StateBucket string `json:"state_bucket"`
		} `json:"config"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("config show output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.Context != "prod" || got.Config.Provider != "aws" || got.Config.StateBucket != "s3://skiff-state-prod" {
		t.Fatalf("unexpected config show: %+v", got)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run("skiff", []string{"config", "current-context", "--config", path + "#local", "--format", "human"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("fragment current-context exit = %d, stderr = %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "local" {
		t.Fatalf("fragment current-context output = %q", stdout.String())
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
	if got.OK || got.Code != "VALIDATION_FAILED" || got.TraceID != "tr_bad" {
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

func TestStatePathSagaResultJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"state", "path", "saga",
		"--format", "json",
		"--trace-id", "tr_saga_path",
		"--saga", "saga_01JABC",
		"--doc", "result",
		"--step", "shift-traffic",
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
		Path    string            `json:"path"`
		Inputs  map[string]string `json:"inputs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("state path output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_saga_path" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	wantPath := "sagas/saga_01JABC/artifacts/results/shift-traffic.json"
	if got.Path != wantPath || got.Inputs["step"] != "shift-traffic" {
		t.Fatalf("unexpected path output: %+v", got)
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
	if got.OK || got.Code != "VALIDATION_FAILED" || got.TraceID != "tr_bad_path" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
	if len(got.Fields) != 1 || got.Fields[0].Field != "service" || got.Fields[0].Code != "INVALID_NAME" {
		t.Fatalf("unexpected fields: %+v", got.Fields)
	}
}

func TestBootstrapAWSDryRunJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "prod",
		"--region", "us-west-2",
		"--bucket", "skiff-state-prod",
		"--dry-run",
		"--format", "json",
		"--trace-id", "tr_bootstrap",
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
		DryRun  bool   `json:"dry_run"`
		Plan    struct {
			Provider       string `json:"provider"`
			StateBucketURI string `json:"state_bucket_uri"`
			KMSAlias       string `json:"kms_alias"`
			SigningKeyRef  string `json:"release_signing_key_ref"`
			RootObjectKey  string `json:"root_object_key"`
			Resources      []struct {
				Kind   string `json:"kind"`
				Action string `json:"action"`
			} `json:"resources"`
			BucketPolicy struct {
				Statement []struct {
					Sid string `json:"Sid"`
				} `json:"Statement"`
			} `json:"bucket_policy"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("bootstrap output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_bootstrap" || !got.DryRun {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Plan.Provider != "aws" || got.Plan.StateBucketURI != "s3://skiff-state-prod" || got.Plan.KMSAlias == "" {
		t.Fatalf("unexpected plan: %+v", got.Plan)
	}
	if got.Plan.SigningKeyRef != "aws-kms://alias/skiff-prod-release-signing?region=us-west-2" {
		t.Fatalf("release signing key ref = %q", got.Plan.SigningKeyRef)
	}
	if got.Plan.RootObjectKey != "envs/prod/root.json" {
		t.Fatalf("root object key = %q", got.Plan.RootObjectKey)
	}
	if len(got.Plan.Resources) != 8 {
		t.Fatalf("resource count = %d, want 8", len(got.Plan.Resources))
	}
	if len(got.Plan.BucketPolicy.Statement) != 5 {
		t.Fatalf("bucket policy statements = %d, want 5", len(got.Plan.BucketPolicy.Statement))
	}
}

func TestBootstrapAWSManagedNetworkDryRunJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "dev",
		"--region", "us-west-2",
		"--network", "managed",
		"--ingress", "private",
		"--dry-run",
		"--format", "json",
		"--trace-id", "tr_bootstrap_network",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var got struct {
		OK   bool `json:"ok"`
		Plan struct {
			StateBucketURI string `json:"state_bucket_uri"`
			RootConfig     struct {
				Network *struct {
					Mode string `json:"mode"`
				} `json:"network"`
				Ingress *struct {
					Type string `json:"type"`
				} `json:"ingress"`
				Runner *struct {
					AMISSMParameter string `json:"ami_ssm_parameter"`
					InstallVersion  string `json:"install_version"`
				} `json:"runner"`
			} `json:"root_config"`
			Resources []struct {
				Kind string `json:"kind"`
			} `json:"resources"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("bootstrap output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || !strings.HasPrefix(got.Plan.StateBucketURI, "s3://skiff-") || !strings.HasSuffix(got.Plan.StateBucketURI, "-dev-us-west-2-state") {
		t.Fatalf("unexpected plan: %+v", got.Plan)
	}
	if got.Plan.RootConfig.Network == nil || got.Plan.RootConfig.Network.Mode != "managed" {
		t.Fatalf("managed network missing from root config: %+v", got.Plan.RootConfig)
	}
	if got.Plan.RootConfig.Ingress == nil || got.Plan.RootConfig.Ingress.Type != "private" {
		t.Fatalf("private ingress missing from root config: %+v", got.Plan.RootConfig)
	}
	if got.Plan.RootConfig.Runner == nil || got.Plan.RootConfig.Runner.AMISSMParameter == "" || got.Plan.RootConfig.Runner.InstallVersion == "" {
		t.Fatalf("runner defaults missing from root config: %+v", got.Plan.RootConfig.Runner)
	}
	hasVPC := false
	hasNAT := false
	hasSigningKMS := false
	for _, resource := range got.Plan.Resources {
		hasVPC = hasVPC || resource.Kind == "vpc"
		hasNAT = hasNAT || resource.Kind == "nat-gateway"
		hasSigningKMS = hasSigningKMS || resource.Kind == "kms-key"
	}
	if !hasVPC || !hasNAT || !hasSigningKMS {
		t.Fatalf("managed network resources missing: %+v", got.Plan.Resources)
	}
}

func TestBootstrapAWSUsesEnvironmentDefaults(t *testing.T) {
	clearSkiffEnv(t)
	t.Setenv("SKIFF_ENV", "quickstart")
	t.Setenv("AWS_REGION", "us-west-2")

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--network", "managed",
		"--ingress", "private",
		"--dry-run",
		"--format", "json",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got struct {
		OK   bool `json:"ok"`
		Plan struct {
			Env            string `json:"env"`
			Region         string `json:"region"`
			StateBucketURI string `json:"state_bucket_uri"`
			RootObjectKey  string `json:"root_object_key"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("bootstrap output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.Plan.Env != "quickstart" || got.Plan.Region != "us-west-2" {
		t.Fatalf("unexpected env-derived plan: %+v", got.Plan)
	}
	if got.Plan.RootObjectKey != "envs/quickstart/root.json" {
		t.Fatalf("root object key = %q", got.Plan.RootObjectKey)
	}
	if !strings.HasPrefix(got.Plan.StateBucketURI, "s3://skiff-") || !strings.HasSuffix(got.Plan.StateBucketURI, "-quickstart-us-west-2-state") {
		t.Fatalf("unexpected generated state bucket: %q", got.Plan.StateBucketURI)
	}
}

func TestBootstrapAWSPublicDomainHumanSummaryIsBackendNeutral(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "quickstart",
		"--region", "us-west-2",
		"--network", "managed",
		"--ingress", "public",
		"--domain-name", "example.com",
		"--dry-run",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "certificate: Skiff-managed ACM DNS-validated certificate for base and wildcard service hosts") {
		t.Fatalf("human output missing backend-neutral certificate summary:\n%s", got)
	}
	if !strings.Contains(got, "- kms-key alias/skiff-quickstart-release-signing: asymmetric AWS KMS key for release signing") {
		t.Fatalf("human output missing release signing KMS key:\n%s", got)
	}
	if strings.Contains(got, "managed by "+"Terraform") {
		t.Fatalf("human output should not describe Skiff-owned ingress as managed by the Terraform backend:\n%s", got)
	}
}

func TestPolicyExplainJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"policy", "explain",
		"--role", "runner",
		"--bucket", "skiff-state-prod",
		"--kms-alias", "alias/skiff-prod-state",
		"--format", "json",
		"--trace-id", "tr_policy",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var got struct {
		OK           bool   `json:"ok"`
		TraceID      string `json:"trace_id"`
		Role         string `json:"role"`
		Findings     []any  `json:"findings"`
		Explanations []struct {
			Sid     string   `json:"sid"`
			Actions []string `json:"actions"`
			Reason  string   `json:"reason"`
			Safety  string   `json:"safety"`
		} `json:"explanations"`
		Policy struct {
			Statement []struct {
				Sid    string `json:"Sid"`
				Action any    `json:"Action"`
			} `json:"Statement"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("policy explain output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_policy" || got.Role != "runner" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if len(got.Findings) != 0 {
		t.Fatalf("policy explain findings = %+v", got.Findings)
	}
	if len(got.Explanations) != len(got.Policy.Statement) || len(got.Explanations) == 0 {
		t.Fatalf("explanations = %+v, statements = %+v", got.Explanations, got.Policy.Statement)
	}
	for _, explanation := range got.Explanations {
		if explanation.Reason == "" || explanation.Safety == "" || len(explanation.Actions) == 0 {
			t.Fatalf("incomplete explanation: %+v", explanation)
		}
	}
	for _, statement := range got.Policy.Statement {
		if hasJSONAction(statement.Action, "s3:PutObject") {
			t.Fatalf("runner policy grants PutObject in %s: %+v", statement.Sid, statement)
		}
	}
}

func TestBootstrapAWSEmitTerraform(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "prod",
		"--region", "us-west-2",
		"--state-bucket", "s3://skiff-state-prod",
		"--emit", "terraform",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{
		`resource "aws_s3_bucket" "skiff_state"`,
		`resource "aws_kms_key" "skiff_state"`,
		`resource "aws_kms_key" "skiff_release_signing"`,
		`data "aws_kms_public_key" "skiff_release_signing"`,
		`release_trust = {`,
		`DenyUnconditionalStateWrites`,
		`UseReleaseSigningKMSKey`,
		`skiff-prod-runner`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("terraform output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestBootstrapAWSManagedNetworkEmitTerraform(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "dev",
		"--region", "us-west-2",
		"--network", "managed",
		"--ingress", "private",
		"--emit", "terraform",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{
		`resource "aws_vpc" "skiff"`,
		`resource "aws_nat_gateway" "skiff"`,
		`resource "aws_s3_object" "skiff_env_root"`,
		`private_subnet_ids = aws_subnet.skiff_private[*].id`,
		`ami_ssm_parameter = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"`,
		`install_version = "v0.1.0"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("terraform output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestBootstrapAWSPublicIngressEmitTerraform(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "quickstart",
		"--region", "us-west-2",
		"--network", "managed",
		"--ingress", "public",
		"--emit", "terraform",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{
		`resource "aws_security_group" "skiff_public_lb"`,
		`resource "aws_lb" "skiff_public"`,
		`internal           = false`,
		`subnets            = aws_subnet.skiff_public[*].id`,
		`resource "aws_lb_listener" "skiff_public_http"`,
		`security_group_id = aws_security_group.skiff_public_lb.id`,
		`http_listener_arn = aws_lb_listener.skiff_public_http.arn`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("terraform output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestBootstrapAWSPublicIngressWithDomainEmitTerraform(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "quickstart",
		"--region", "us-west-2",
		"--network", "managed",
		"--ingress", "public",
		"--company-name", "Acme Corp",
		"--domain-name", "example.com",
		"--emit", "terraform",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{
		`data "aws_route53_zone" "skiff_public"`,
		`name         = "example.com."`,
		`resource "aws_acm_certificate" "skiff_public"`,
		`domain_name               = "quickstart.example.com"`,
		`subject_alternative_names = ["*.quickstart.example.com"]`,
		`resource "aws_acm_certificate_validation" "skiff_public"`,
		`resource "aws_lb_listener" "skiff_public_https"`,
		`certificate_arn   = aws_acm_certificate_validation.skiff_public.certificate_arn`,
		`resource "aws_route53_record" "skiff_public_alias"`,
		`name    = "quickstart.example.com"`,
		`resource "aws_route53_record" "skiff_public_wildcard_alias"`,
		`name    = "*.quickstart.example.com"`,
		`bucket = "skiff-acme-corp-quickstart-us-west-2-state"`,
		`base_domain           = "quickstart.example.com"`,
		`default_host_template = "{service}.quickstart.example.com"`,
		`dns_name          = "quickstart.example.com"`,
		`provider_dns_name = aws_lb.skiff_public.dns_name`,
		`https_listener_arn = aws_lb_listener.skiff_public_https.arn`,
		`"skiff.dev/company" = "acme-corp"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("terraform output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestBootstrapAWSPublicIngressWithExistingCertificateEmitTerraform(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "quickstart",
		"--region", "us-west-2",
		"--network", "managed",
		"--ingress", "public",
		"--certificate-arn", "arn:aws:acm:us-west-2:123456789012:certificate/abc123",
		"--emit", "terraform",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	got := stdout.String()
	for _, want := range []string{
		`resource "aws_lb_listener" "skiff_public_https"`,
		`certificate_arn   = "arn:aws:acm:us-west-2:123456789012:certificate/abc123"`,
		`https_listener_arn = aws_lb_listener.skiff_public_https.arn`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("terraform output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `resource "aws_acm_certificate" "skiff_public"`) {
		t.Fatalf("terraform output should not create an ACM certificate when --certificate-arn is supplied:\n%s", got)
	}
}

func TestBootstrapAWSDirectApplyUsesBootstrapClientAndWritesContext(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".skiffconfig")
	outDir := filepath.Join(dir, "bootstrap")
	withFakeAWSReleaseSignerStore(t, "us-west-2")
	fake := newCLIFakeAWSBootstrapClient()
	oldFactory := newAWSBootstrapClient
	t.Cleanup(func() { newAWSBootstrapClient = oldFactory })
	var factoryRegion string
	newAWSBootstrapClient = func(ctx context.Context, region string) (bootstrap.AWSBootstrapClient, error) {
		factoryRegion = region
		return fake, nil
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "quickstart",
		"--region", "us-west-2",
		"--network", "managed",
		"--ingress", "public",
		"--domain-name", "example.com",
		"--yes",
		"--out", outDir,
		"--format", "json",
		"--config", configPath,
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if factoryRegion != "us-west-2" {
		t.Fatalf("bootstrap client region = %q", factoryRegion)
	}
	var got bootstrapAWSOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("bootstrap output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.DryRun || got.Apply == nil {
		t.Fatalf("unexpected bootstrap output: %+v", got)
	}
	if got.Plan.RootConfig.ReleaseTrust == nil || len(got.Plan.RootConfig.ReleaseTrust.Keys) != 1 {
		t.Fatalf("bootstrap plan missing release trust: %+v", got.Plan.RootConfig.ReleaseTrust)
	}
	if got.Plan.RootConfig.ReleaseTrust.Keys[0].Backend != "aws-kms" {
		t.Fatalf("bootstrap plan should default to AWS KMS signing trust: %+v", got.Plan.RootConfig.ReleaseTrust.Keys[0])
	}
	lb := got.Apply.RootConfig.Ingress.LoadBalancer
	if lb == nil || lb.ProviderDNSName != "skiff-cli-public.elb.amazonaws.com" || lb.CertificateARN != "arn:aws:acm:us-west-2:123456789012:certificate/quickstart.example.com" {
		t.Fatalf("direct apply did not return materialized public ingress root: %+v", got.Apply.RootConfig.Ingress)
	}
	if strings.Contains(string(stdout.Bytes()), "${") {
		t.Fatalf("direct apply output should not contain Terraform expressions:\n%s", stdout.String())
	}
	if !fake.seen["dns-record/quickstart.example.com"] || !fake.seen["dns-record/*.quickstart.example.com"] {
		t.Fatalf("direct apply did not upsert base and wildcard DNS aliases: %+v", fake.seen)
	}
	if _, err := os.Stat(filepath.Join(outDir, "teardown-aws-cli.sh")); err != nil {
		t.Fatalf("direct apply should write AWS CLI teardown script when --out is set: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "main.tf")); !os.IsNotExist(err) {
		t.Fatalf("direct apply should not write Terraform when --emit is omitted: %v", err)
	}
	file, err := config.LoadSkiffConfigFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	name, values, err := file.SelectContext("quickstart")
	if err != nil {
		t.Fatal(err)
	}
	if name != "quickstart" || values[config.FieldAWSLiveApply] != "true" || values[config.FieldStateBucket] != got.Plan.StateBucketURI {
		t.Fatalf("unexpected written context %q: %+v", name, values)
	}
	if values[config.FieldReleaseSigningKeyRef] == "" || values[config.FieldReleaseSigningKeyID] == "" {
		t.Fatalf("written context missing signing key ref: %+v", values)
	}
}

func TestBootstrapAWSEmitTerraformOutWritesContextAndTeardown(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "bootstrap")
	configPath := filepath.Join(dir, ".skiffconfig")

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "quickstart",
		"--region", "us-west-2",
		"--network", "managed",
		"--ingress", "private",
		"--emit", "terraform",
		"--out", outDir,
		"--config", configPath,
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if strings.Contains(stdout.String(), `resource "aws_s3_bucket"`) {
		t.Fatalf("human output should summarize paths, not dump Terraform:\n%s", stdout.String())
	}
	for _, path := range []string{filepath.Join(outDir, "main.tf"), filepath.Join(outDir, "teardown-aws-cli.sh"), configPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to be written: %v", path, err)
		}
	}
	if body, err := os.ReadFile(filepath.Join(outDir, "teardown-aws-cli.sh")); err != nil || !strings.Contains(string(body), "Type %s to continue") {
		t.Fatalf("teardown script missing confirmation: err=%v body=%s", err, string(body))
	}
	if body, err := os.ReadFile(filepath.Join(outDir, "main.tf")); err != nil || !strings.Contains(string(body), `resource "aws_kms_key" "skiff_release_signing"`) || !strings.Contains(string(body), `release_trust = {`) {
		t.Fatalf("terraform output missing release signing KMS resources: err=%v body=%s", err, string(body))
	}
	file, err := config.LoadSkiffConfigFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	name, values, err := file.SelectContext("quickstart")
	if err != nil {
		t.Fatal(err)
	}
	if name != "quickstart" || values[config.FieldEnv] != "quickstart" || values[config.FieldProvider] != "aws" || values[config.FieldAWSLiveApply] != "true" {
		t.Fatalf("unexpected written context %q: %+v", name, values)
	}
	if !strings.HasPrefix(values[config.FieldStateBucket], "s3://skiff-") || !strings.HasSuffix(values[config.FieldStateBucket], "-quickstart-us-west-2-state") {
		t.Fatalf("unexpected generated state bucket: %q", values[config.FieldStateBucket])
	}
	if !strings.HasPrefix(values[config.FieldReleaseSigningKeyRef], "aws-kms://alias/skiff-quickstart-release-signing") {
		t.Fatalf("written context missing AWS KMS signing key ref: %+v", values)
	}
	if values[config.FieldReleaseSigningKeyID] != "" {
		t.Fatalf("terraform-managed signing key ID should be resolved by Terraform, got context: %+v", values)
	}
}

func TestBootstrapAWSCanUseKeychainSigningBackend(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "bootstrap")
	configPath := filepath.Join(dir, ".skiffconfig")
	withFakeReleaseSignerStore(t)

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"bootstrap", "aws",
		"--env", "quickstart",
		"--region", "us-west-2",
		"--signing-backend", "keychain",
		"--emit", "terraform",
		"--out", outDir,
		"--config", configPath,
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	body, err := os.ReadFile(filepath.Join(outDir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `resource "aws_kms_key" "skiff_release_signing"`) {
		t.Fatalf("keychain signing backend should not emit release signing KMS resources:\n%s", string(body))
	}
	file, err := config.LoadSkiffConfigFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	_, values, err := file.SelectContext("quickstart")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(values[config.FieldReleaseSigningKeyRef], "keychain://") || values[config.FieldReleaseSigningKeyID] == "" {
		t.Fatalf("written context should use keychain signing metadata: %+v", values)
	}
}

func TestEventsListServiceJSONFromStateDir(t *testing.T) {
	dir := t.TempDir()
	eventDir := filepath.Join(dir, "services", "payments-api", "events")
	if err := os.MkdirAll(eventDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eventDir, "01JABC-service-created.json"), []byte(`{
		"schema_version":"skiff.event/v1",
		"id":"01JABC-service-created",
		"time":"2026-05-16T21:30:00Z",
		"trace_id":"tr_events",
		"scope":{"kind":"service","service":"payments-api"},
		"type":"service.created",
		"summary":"service control created"
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"events",
		"--state-dir", dir,
		"--scope", "service",
		"--service", "payments-api",
		"--format", "json",
		"--trace-id", "tr_list",
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
		Events  []struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Summary string `json:"summary"`
		} `json:"events"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("events output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_list" || len(got.Events) != 1 {
		t.Fatalf("unexpected events output: %+v", got)
	}
	if got.Events[0].Type != "service.created" || got.Events[0].Summary != "service control created" {
		t.Fatalf("unexpected event: %+v", got.Events[0])
	}
}

func TestEventsListSagaJSONFromStateDir(t *testing.T) {
	dir := t.TempDir()
	eventDir := filepath.Join(dir, "sagas", "saga_01JABC", "events")
	if err := os.MkdirAll(eventDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eventDir, "01JABC-approval-required.json"), []byte(`{
		"schema_version":"skiff.event/v1",
		"id":"01JABC-approval-required",
		"time":"2026-05-16T21:30:00Z",
		"scope":{"kind":"saga","saga":"saga_01JABC"},
		"type":"approval.required",
		"summary":"manual approval required"
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"events",
		"--state-dir", dir,
		"--scope", "saga",
		"--saga", "saga_01JABC",
		"--format", "json",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("events output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || len(got.Events) != 1 || got.Events[0].Type != "approval.required" {
		t.Fatalf("unexpected saga events output: %+v", got)
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

func TestCompileJSONServiceExample(t *testing.T) {
	examplePath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"compile",
		examplePath,
		"--format", "json",
		"--trace-id", "tr_compile",
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
		Graph   struct {
			SchemaVersion string `json:"schema_version"`
			Service       string `json:"service"`
			Env           string `json:"env"`
			Resources     struct {
				AutoscalingGroups []struct {
					Meta struct {
						Tags map[string]string `json:"tags"`
					} `json:"meta"`
					Min int `json:"min"`
					Max int `json:"max"`
				} `json:"autoscaling_groups"`
				RuntimeManifests []struct {
					HealthCheck struct {
						Path string `json:"path"`
					} `json:"health_check"`
				} `json:"runtime_manifests"`
			} `json:"resources"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("compile output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_compile" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Graph.SchemaVersion != "skiff.ir/v1alpha1" || got.Graph.Service != "payments-api" || got.Graph.Env != "prod" {
		t.Fatalf("unexpected graph identity: %+v", got.Graph)
	}
	if len(got.Graph.Resources.AutoscalingGroups) != 1 || got.Graph.Resources.AutoscalingGroups[0].Min != 3 || got.Graph.Resources.AutoscalingGroups[0].Max != 20 {
		t.Fatalf("unexpected ASG lowering: %+v", got.Graph.Resources.AutoscalingGroups)
	}
	if got.Graph.Resources.AutoscalingGroups[0].Meta.Tags["skiff.dev/service"] != "payments-api" {
		t.Fatalf("missing service tag: %+v", got.Graph.Resources.AutoscalingGroups[0].Meta.Tags)
	}
	if len(got.Graph.Resources.RuntimeManifests) != 1 || got.Graph.Resources.RuntimeManifests[0].HealthCheck.Path != "/healthz" {
		t.Fatalf("unexpected runtime manifest lowering: %+v", got.Graph.Resources.RuntimeManifests)
	}
}

func TestCompileOutWritesCanonicalIR(t *testing.T) {
	examplePath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	outPath := filepath.Join(t.TempDir(), "ir.json")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"compile",
		examplePath,
		"--format", "json",
		"--trace-id", "tr_compile_out",
		"--out", outPath,
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var envelope struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Out     string `json:"out"`
		Graph   any    `json:"graph"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("compile output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !envelope.OK || envelope.TraceID != "tr_compile_out" || envelope.Out != outPath || envelope.Graph != nil {
		t.Fatalf("unexpected compile envelope: %+v", envelope)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read IR output: %v", err)
	}
	var graph struct {
		SchemaVersion string `json:"schema_version"`
		Service       string `json:"service"`
	}
	if err := json.Unmarshal(body, &graph); err != nil {
		t.Fatalf("IR output is not valid JSON: %v\n%s", err, string(body))
	}
	if graph.SchemaVersion != "skiff.ir/v1alpha1" || graph.Service != "payments-api" {
		t.Fatalf("unexpected IR output: %+v", graph)
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
    host: localhost
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
	if got.OK || got.Code != "VALIDATION_FAILED" || got.TraceID != "tr_bad_spec" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
	if !hasPathCode(got.Fields, "$.artifact.ref", "MUTABLE_ARTIFACT_REF") {
		t.Fatalf("missing mutable artifact diagnostic: %+v", got.Fields)
	}
	if !hasPathCode(got.Fields, "$.network.ingress.host", "INVALID_HOST") {
		t.Fatalf("missing invalid host diagnostic: %+v", got.Fields)
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

func hasJSONAction(value any, want string) bool {
	switch typed := value.(type) {
	case string:
		return typed == want
	case []any:
		for _, item := range typed {
			if item == want {
				return true
			}
		}
	}
	return false
}

func clearSkiffEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SKIFF_CONFIG",
		"SKIFF_CONTEXT",
		"SKIFF_ENV",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"SKIFF_PROVIDER",
		"SKIFF_REGION",
		"SKIFF_STATE_BUCKET",
		"SKIFF_RELEASE_SIGNING_KEY_ID",
		"SKIFF_RELEASE_SIGNING_KEY_REF",
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

type cliFakeAWSBootstrapClient struct {
	seen map[string]bool
}

func newCLIFakeAWSBootstrapClient() *cliFakeAWSBootstrapClient {
	return &cliFakeAWSBootstrapClient{seen: map[string]bool{}}
}

func (f *cliFakeAWSBootstrapClient) EnsureKMSKey(ctx context.Context, spec bootstrap.KMSKeySpec) (bootstrap.ApplyAction, error) {
	return f.ensure("kms-key", spec.Alias), nil
}

func (f *cliFakeAWSBootstrapClient) EnsureStateBucket(ctx context.Context, spec bootstrap.StateBucketSpec) (bootstrap.ApplyAction, error) {
	return f.ensure("s3-bucket", spec.Name), nil
}

func (f *cliFakeAWSBootstrapClient) EnsureIAMRole(ctx context.Context, spec bootstrap.IAMRoleSpec) (bootstrap.ApplyAction, error) {
	action := f.ensure("iam-role", spec.Name)
	action.ProviderID = "arn:aws:iam::123456789012:role/" + spec.Name
	return action, nil
}

func (f *cliFakeAWSBootstrapClient) PutBucketPolicy(ctx context.Context, spec bootstrap.BucketPolicySpec) (bootstrap.ApplyAction, error) {
	return f.ensure("s3-bucket-policy", spec.Bucket), nil
}

func (f *cliFakeAWSBootstrapClient) EnsureManagedNetwork(ctx context.Context, spec bootstrap.ManagedNetworkSpec) (*bootstrap.ManagedNetworkResult, error) {
	return &bootstrap.ManagedNetworkResult{
		Actions: []bootstrap.ApplyAction{
			f.ensure("vpc", spec.NamePrefix),
			f.ensure("subnet", spec.NamePrefix+"-public"),
			f.ensure("subnet", spec.NamePrefix+"-private"),
			f.ensure("internet-gateway", spec.NamePrefix),
			f.ensure("nat-gateway", spec.NamePrefix),
			f.ensure("route-table", spec.NamePrefix),
		},
		VPCID:            "vpc-cli",
		PublicSubnetIDs:  []string{"subnet-public-a", "subnet-public-b"},
		PrivateSubnetIDs: []string{"subnet-private-a", "subnet-private-b"},
	}, nil
}

func (f *cliFakeAWSBootstrapClient) ResolveHostedZone(ctx context.Context, spec bootstrap.HostedZoneSpec) (*bootstrap.HostedZoneResult, error) {
	return &bootstrap.HostedZoneResult{
		Action:       f.ensure("hosted-zone", "ZCLI"),
		HostedZoneID: "ZCLI",
		Name:         spec.DomainName,
	}, nil
}

func (f *cliFakeAWSBootstrapClient) EnsureCertificate(ctx context.Context, spec bootstrap.CertificateSpec) (*bootstrap.CertificateResult, error) {
	return &bootstrap.CertificateResult{
		Action:         f.ensure("certificate", spec.DomainName),
		CertificateARN: "arn:aws:acm:us-west-2:123456789012:certificate/" + spec.DomainName,
	}, nil
}

func (f *cliFakeAWSBootstrapClient) EnsureLoadBalancerSecurityGroup(ctx context.Context, spec bootstrap.LoadBalancerSecurityGroupSpec) (*bootstrap.SecurityGroupResult, error) {
	return &bootstrap.SecurityGroupResult{Action: f.ensure("security-group", spec.Name), GroupID: "sg-cli"}, nil
}

func (f *cliFakeAWSBootstrapClient) EnsureLoadBalancer(ctx context.Context, spec bootstrap.LoadBalancerSpec) (*bootstrap.LoadBalancerResult, error) {
	return &bootstrap.LoadBalancerResult{
		Action:       f.ensure("load-balancer", spec.Name),
		ARN:          "arn:aws:elasticloadbalancing:us-west-2:123456789012:loadbalancer/app/skiff-cli/abc",
		DNSName:      "skiff-cli-public.elb.amazonaws.com",
		HostedZoneID: "ZALBCLI",
	}, nil
}

func (f *cliFakeAWSBootstrapClient) EnsureListener(ctx context.Context, spec bootstrap.ListenerSpec) (*bootstrap.ListenerResult, error) {
	return &bootstrap.ListenerResult{
		Action: f.ensure("listener", spec.Name),
		ARN:    "arn:aws:elasticloadbalancing:us-west-2:123456789012:listener/app/skiff-cli/abc/" + spec.Name,
	}, nil
}

func (f *cliFakeAWSBootstrapClient) EnsureDNSAlias(ctx context.Context, spec bootstrap.DNSAliasSpec) (bootstrap.ApplyAction, error) {
	return f.ensure("dns-record", spec.Name), nil
}

func (f *cliFakeAWSBootstrapClient) PutEnvironmentRoot(ctx context.Context, spec bootstrap.EnvironmentRootSpec) (bootstrap.ApplyAction, error) {
	return f.ensure("environment-root", spec.Key), nil
}

func (f *cliFakeAWSBootstrapClient) ensure(kind, name string) bootstrap.ApplyAction {
	key := kind + "/" + name
	if f.seen[key] {
		return bootstrap.ApplyAction{Kind: kind, Name: name, Action: "unchanged", ProviderID: key}
	}
	f.seen[key] = true
	return bootstrap.ApplyAction{Kind: kind, Name: name, Action: "created", ProviderID: key}
}
