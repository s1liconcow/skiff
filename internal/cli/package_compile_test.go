package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageStackValidateCompilePlanExplainDirectMode(t *testing.T) {
	pkgDir := writePkgCLIFixture(t, "postgres-ha")
	root := t.TempDir()
	lockfile := filepath.Join(root, "skiff.lock.json")
	cache := filepath.Join(root, "cache")
	_ = runPkgJSON(t, []string{"pkg", "add", "file://" + pkgDir, "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_compile_add"})

	specPath := filepath.Join(root, "skiff.yaml")
	stack := `
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: payments
  env: dev
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/payments-api:latest
      runtime:
        port: 8080
        health:
          path: /healthz
  dependencies:
    - name: db
      uses: file://` + pkgDir + `
      version: "1.2.0"
      config:
        mode: managed
        engine: postgres
        version: "16"
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
`
	if err := os.WriteFile(specPath, []byte(stack), 0o644); err != nil {
		t.Fatalf("write stack spec: %v", err)
	}

	validate := runCLIJSON(t, []string{"validate", specPath, "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_validate"})
	var validateOut specValidateOutput
	decodePkgJSON(t, validate, &validateOut)
	if !validateOut.OK || validateOut.TraceID != "tr_pkg_validate" {
		t.Fatalf("unexpected validate output: %+v", validateOut)
	}

	compile := runCLIJSON(t, []string{"compile", specPath, "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_compile"})
	var compileOut compileOutput
	decodePkgJSON(t, compile, &compileOut)
	if !compileOut.OK || compileOut.Graph == nil || compileOut.Graph.PackageLockDigest == "" {
		t.Fatalf("unexpected compile output: %+v", compileOut)
	}
	if len(compileOut.Graph.Resources.ManagedDatabases) != 1 || len(compileOut.Graph.Resources.PackageOperations) != 1 {
		t.Fatalf("package resources missing from compile output: %+v", compileOut.Graph.Resources)
	}
	if compileOut.Graph.Resources.PackageOperations[0].Mode != "managed" {
		t.Fatalf("package operation mode = %q, want managed", compileOut.Graph.Resources.PackageOperations[0].Mode)
	}

	plan := runCLIJSON(t, []string{"--direct", "plan", specPath, "--provider", "aws", "--region", "us-west-2", "--state", "s3://skiff-state-dev", "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_plan"})
	var planOut planOutput
	decodePkgJSON(t, plan, &planOut)
	if !planOut.OK || len(planOut.Plan.Resources) == 0 {
		t.Fatalf("unexpected plan output: %+v", planOut)
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"explain", specPath, "--provider", "aws", "--region", "us-west-2", "--state", "s3://skiff-state-dev", "--lockfile", lockfile, "--cache", cache, "--format", "human", "--trace-id", "tr_pkg_explain"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("explain exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "package: file://"+pkgDir+"@1.2.0") || !strings.Contains(out, "Skiff package operation profile") || !strings.Contains(out, "managed mode") {
		t.Fatalf("explain output missing package provenance/resources:\n%s", out)
	}
}

func runCLIJSON(t *testing.T, args []string) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run("skiff", args, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("%v exit code = %d, stderr = %s, stdout = %s", args, code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("%v stderr = %q, want empty", args, stderr.String())
	}
	if !json.Valid(stdout.Bytes()) {
		t.Fatalf("%v output is not JSON: %s", args, stdout.String())
	}
	return stdout.Bytes()
}
