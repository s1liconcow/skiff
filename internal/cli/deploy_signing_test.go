package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployFakeProviderGeneratesEphemeralSigner(t *testing.T) {
	clearSkiffEnv(t)
	root := t.TempDir()
	specPath := filepath.Join("..", "..", "examples", "service", "http-hello", "skiff.yaml")

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"deploy", specPath,
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "fake",
		"--region", "local",
		"--key-id", "demo-ephemeral",
		"--format", "json",
		"--trace-id", "tr_ephemeral_signer",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got deployOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("deploy output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || !got.Result.OK || got.Result.ReleaseManifest == nil {
		t.Fatalf("unexpected deploy output: %+v", got)
	}
	if !strings.HasPrefix(got.Result.ReleaseID, "rel_") || !strings.HasPrefix(got.Result.OperationID, "op_") {
		t.Fatalf("expected generated release/operation IDs, got release=%q operation=%q", got.Result.ReleaseID, got.Result.OperationID)
	}
	signatures := got.Result.ReleaseManifest.Signatures
	if len(signatures) != 1 || signatures[0].KeyID != "demo-ephemeral" || signatures[0].Algorithm != "ed25519" {
		t.Fatalf("unexpected signatures: %+v", signatures)
	}
}

func TestDeployNonFakeProviderRequiresSigningSeed(t *testing.T) {
	clearSkiffEnv(t)
	root := t.TempDir()
	specPath := filepath.Join("..", "..", "examples", "service", "http-hello", "skiff.yaml")

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"deploy", specPath,
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_missing_seed",
	}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d; stderr = %s, stdout = %s", code, ExitUserError, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty JSON-mode stderr", stderr.String())
	}
	var got specErrorOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Summary == "" {
		t.Fatalf("unexpected error output: %+v", got)
	}
}
