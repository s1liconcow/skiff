package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestContractTestJSON(t *testing.T) {
	specPath := filepath.Join("..", "..", "tests", "fixtures", "services", "minimal.yaml")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"contract", "test", specPath, "--format", "json", "--trace-id", "tr_contract"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got contractTestOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !got.OK || !got.Result.OK || got.TraceID != "tr_contract" || got.Result.ArtifactDigest != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected contract output: %+v", got)
	}
}

func TestContractTestRejectsMutableArtifact(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "skiff.yaml")
	body := []byte(`apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: payments-api
  env: staging
artifact:
  type: oci
  ref: registry.example.com/payments-api:main
runtime:
  port: 8080
  health:
    path: /healthz
machine:
  size: small
scale:
  min: 1
  max: 1
`)
	if err := os.WriteFile(specPath, body, 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"contract", "test", specPath, "--format", "json", "--trace-id", "tr_contract_bad"}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got contractTestOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != "CONTRACT_FAILED" {
		t.Fatalf("unexpected failed contract output: %+v", got)
	}
}
