package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

func TestPkgCommandFamilyJSONFlow(t *testing.T) {
	dir := writePkgCLIFixture(t, "postgres-ha")
	root := t.TempDir()
	lockfile := filepath.Join(root, "skiff.lock.json")
	cache := filepath.Join(root, "cache")

	add := runPkgJSON(t, []string{"pkg", "add", "file://" + dir, "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_add"})
	var addOut pkgCommandOutput
	decodePkgJSON(t, add, &addOut)
	if !addOut.OK || addOut.TraceID != "tr_pkg_add" || addOut.Package.Name != "postgres-ha" || addOut.Entry == nil || addOut.Entry.Digest == "" {
		t.Fatalf("unexpected pkg add output: %+v", addOut)
	}
	if _, err := os.Stat(lockfile); err != nil {
		t.Fatalf("lockfile not written: %v", err)
	}
	if addOut.Cache == nil || addOut.Cache.Path == "" {
		t.Fatalf("cache output missing: %+v", addOut)
	}

	list := runPkgJSON(t, []string{"pkg", "list", "--lockfile", lockfile, "--format", "json", "--trace-id", "tr_pkg_list"})
	var listOut pkgListOutput
	decodePkgJSON(t, list, &listOut)
	if !listOut.OK || len(listOut.Packages) != 1 || listOut.Packages[0].Name != "postgres-ha" {
		t.Fatalf("unexpected pkg list output: %+v", listOut)
	}

	explain := runPkgJSON(t, []string{"pkg", "explain", "postgres-ha", "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_explain"})
	var explainOut pkgCommandOutput
	decodePkgJSON(t, explain, &explainOut)
	if !explainOut.OK || explainOut.Explanation == nil || explainOut.Explanation.Plugin == nil {
		t.Fatalf("unexpected pkg explain output: %+v", explainOut)
	}
	if got := explainOut.Explanation.Plugin.Runtime.Kind; got != "command" {
		t.Fatalf("plugin runtime kind = %q, want command", got)
	}
	if len(explainOut.Explanation.Exports.OperationProfiles) != 1 || explainOut.Explanation.Exports.OperationProfiles[0] != "primary-switchover-update" {
		t.Fatalf("operation profile exports missing: %+v", explainOut.Explanation.Exports)
	}
	if len(explainOut.Explanation.Exports.PackageSteps) != 1 || explainOut.Explanation.Exports.PackageSteps[0] != "postgres.verify_replica_lag" {
		t.Fatalf("package step exports missing: %+v", explainOut.Explanation.Exports)
	}
	if len(explainOut.Explanation.Plugin.Capabilities) < 2 || !hasPackageCapability(explainOut.Explanation.Plugin.Capabilities, "postgres-safety") {
		t.Fatalf("plugin capabilities missing: %+v", explainOut.Explanation.Plugin.Capabilities)
	}

	verify := runPkgJSON(t, []string{"pkg", "verify", "postgres-ha", "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_verify"})
	var verifyOut pkgVerifyOutput
	decodePkgJSON(t, verify, &verifyOut)
	if !verifyOut.OK || verifyOut.Package.Name != "postgres-ha" || len(verifyOut.Diagnostics) != 0 {
		t.Fatalf("unexpected pkg verify output: %+v", verifyOut)
	}

	conformance := runPkgJSON(t, []string{"pkg", "verify", "postgres-ha", "--conformance", "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_conformance"})
	var conformanceOut pkgVerifyOutput
	decodePkgJSON(t, conformance, &conformanceOut)
	if !conformanceOut.OK || conformanceOut.Conformance == nil || !conformanceOut.Conformance.OK {
		t.Fatalf("unexpected pkg conformance output: %+v", conformanceOut)
	}

	bundle := runPkgJSON(t, []string{"pkg", "bundle", dir, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_bundle"})
	var bundleOut pkgCommandOutput
	decodePkgJSON(t, bundle, &bundleOut)
	if !bundleOut.OK || bundleOut.Cache == nil || bundleOut.Cache.Path == "" || bundleOut.Package.Name != "postgres-ha" {
		t.Fatalf("unexpected pkg bundle output: %+v", bundleOut)
	}

	update := runPkgJSON(t, []string{"pkg", "update", "postgres-ha", "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_update"})
	var updateOut pkgCommandOutput
	decodePkgJSON(t, update, &updateOut)
	if !updateOut.OK || updateOut.Entry == nil || updateOut.Entry.Name != "postgres-ha" {
		t.Fatalf("unexpected pkg update output: %+v", updateOut)
	}
}

func TestPkgAddActualFirstPartyPostgresHAFromRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	lockfile := filepath.Join(t.TempDir(), "skiff.lock.json")
	cache := filepath.Join(t.TempDir(), "cache")

	add := runPkgJSON(t, []string{
		"pkg", "add", "skiff.dev/postgres-ha",
		"--registry-dir", filepath.Join(root, "packages"),
		"--lockfile", lockfile,
		"--cache", cache,
		"--format", "json",
		"--trace-id", "tr_pkg_actual_postgres_add",
	})
	var addOut pkgCommandOutput
	decodePkgJSON(t, add, &addOut)
	if !addOut.OK || addOut.Package.Name != "postgres-ha" || addOut.Entry == nil || addOut.Entry.Ref != "skiff.dev/postgres-ha" || addOut.Entry.Digest == "" {
		t.Fatalf("unexpected actual postgres package add output: %+v", addOut)
	}

	explain := runPkgJSON(t, []string{
		"pkg", "explain", "postgres-ha",
		"--lockfile", lockfile,
		"--cache", cache,
		"--format", "json",
		"--trace-id", "tr_pkg_actual_postgres_explain",
	})
	var explainOut pkgCommandOutput
	decodePkgJSON(t, explain, &explainOut)
	if !explainOut.OK || explainOut.Explanation == nil || explainOut.Explanation.Plugin == nil {
		t.Fatalf("unexpected actual postgres package explain output: %+v", explainOut)
	}
	if got := explainOut.Explanation.Plugin.Runtime.Command; len(got) != 1 || got[0] != "postgres-ha-plugin" {
		t.Fatalf("plugin command = %+v, want postgres-ha-plugin", got)
	}
	if !containsString(explainOut.Explanation.Exports.PackageSteps, "postgres.switchover") ||
		!containsString(explainOut.Explanation.Exports.PackageSteps, "package.primary_switchover.verify_candidate_caught_up") {
		t.Fatalf("actual postgres package missing switchover steps: %+v", explainOut.Explanation.Exports.PackageSteps)
	}

	verify := runPkgJSON(t, []string{
		"pkg", "verify", "postgres-ha",
		"--conformance",
		"--lockfile", lockfile,
		"--cache", cache,
		"--format", "json",
		"--trace-id", "tr_pkg_actual_postgres_verify",
	})
	var verifyOut pkgVerifyOutput
	decodePkgJSON(t, verify, &verifyOut)
	if !verifyOut.OK || verifyOut.Conformance == nil || !verifyOut.Conformance.OK {
		t.Fatalf("unexpected actual postgres package verify output: %+v", verifyOut)
	}

	specPath := filepath.Join(t.TempDir(), "skiff.yaml")
	if err := os.WriteFile(specPath, []byte(`
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
      uses: skiff.dev/postgres-ha
      version: "1.0.0"
      config:
        mode: managed
        engine: postgres
        version: "16"
        maxReplicaLagBytes: 1048576
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
`), 0o644); err != nil {
		t.Fatalf("write actual package stack spec: %v", err)
	}
	compile := runCLIJSON(t, []string{"compile", specPath, "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_actual_postgres_compile"})
	var compileOut compileOutput
	decodePkgJSON(t, compile, &compileOut)
	if !compileOut.OK || compileOut.Graph == nil || len(compileOut.Graph.Resources.ManagedDatabases) != 1 || len(compileOut.Graph.Resources.PackageOperations) != 1 {
		t.Fatalf("actual postgres package did not compile to database and package operations: %+v", compileOut)
	}
	if compileOut.Graph.Resources.PackageOperations[0].Mode != "managed" || compileOut.Graph.Resources.RuntimeManifests[0].Env["DATABASE_URL"] == "" {
		t.Fatalf("actual postgres package compile output missing managed mode/binding: %+v", compileOut.Graph.Resources)
	}
}

func TestPkgAddDuplicateJSONErrorIsAgentSafe(t *testing.T) {
	dir := writePkgCLIFixture(t, "redis-ha")
	root := t.TempDir()
	lockfile := filepath.Join(root, "skiff.lock.json")
	cache := filepath.Join(root, "cache")
	_ = runPkgJSON(t, []string{"pkg", "add", "file://" + dir, "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_add_one"})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"pkg", "add", "file://" + dir, "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_pkg_add_dup"}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if strings.Contains(stdout.String(), "\x1b[") {
		t.Fatalf("JSON error contains ANSI escapes: %q", stdout.String())
	}
	var got commandErrorOutput
	decodePkgJSON(t, stdout.Bytes(), &got)
	if got.OK || got.Code != "DUPLICATE_PACKAGE_LOCK_ENTRY" || got.TraceID != "tr_pkg_add_dup" {
		t.Fatalf("unexpected duplicate output: %+v", got)
	}
}

func runPkgJSON(t *testing.T, args []string) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run("skiff", args, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("%v exit code = %d, stderr = %s, stdout = %s", args, code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("%v stderr = %q, want empty", args, stderr.String())
	}
	if strings.Contains(stdout.String(), "\x1b[") {
		t.Fatalf("%v JSON output contains ANSI escapes: %q", args, stdout.String())
	}
	return stdout.Bytes()
}

func decodePkgJSON(t *testing.T, body []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(body, dst); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, string(body))
	}
}

func writePkgCLIFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{
		"apiVersion": "skiff.dev/package/v1alpha1",
		"kind": "Package",
		"name": "` + name + `",
		"version": "1.2.0",
		"exports": {
			"dependencies": ["` + name + `"],
			"operation_profiles": ["primary-switchover-update"],
			"package_steps": ["postgres.verify_replica_lag"],
			"doctor_checks": ["postgres.verify_replica_lag"]
		},
		"plugin": {"manifest": "plugin.json"}
	}`
	if err := os.WriteFile(filepath.Join(dir, "skiff-package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write package manifest: %v", err)
	}
	plugin := `{
		"apiVersion": "skiff.dev/plugin/v1alpha1",
		"kind": "Plugin",
		"name": "` + name + `",
		"version": "1.2.0",
		"runtime": {"kind": "command", "command": ["` + name + "-plugin" + `"]},
		"hooks": ["doctor_checks", "package_step"],
		"permissions": {
			"doctor_checks": true,
			"package_step_kinds": ["postgres.verify_replica_lag"]
		},
		"capabilities": [
			{"kind": "doctor_check", "name": "replica-lag-doctor", "doctor_checks": ["postgres.verify_replica_lag"]},
			{
				"kind": "package_step",
				"name": "postgres-safety",
				"package_steps": [{
					"kind": "postgres.verify_replica_lag",
					"params": {"target": {"type": "string", "required": true}},
					"risk": "low",
					"reversibility": "reversible"
				}]
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(plugin), 0o644); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skiff-package.sig"), []byte("test-signature"), 0o644); err != nil {
		t.Fatalf("write signature: %v", err)
	}
	return dir
}

func hasPackageCapability(capabilities []pluginapi.Capability, name string) bool {
	for _, capability := range capabilities {
		if capability.Kind == pluginapi.CapabilityPackageStep && capability.Name == name {
			return true
		}
	}
	return false
}
