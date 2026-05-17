package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginValidateJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"plugin", "validate",
		filepath.Join("..", "..", "tests", "fixtures", "plugins", "security-group-rule"),
		"--format", "json",
		"--trace-id", "tr_plugin_validate",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var got struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Plugin  struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"plugin"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("plugin validate output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_plugin_validate" || got.Plugin.Name != "security-group-rule" {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func TestExplainRunsPluginMutationAndExplainsPatch(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "plugin.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat >/dev/null
printf '%s' '{"patches":[{"op":"add","path":"/resources/security_groups/security-group:payments-api/rules/-","kind":"SecurityGroupRule","value":{"direction":"egress","protocol":"tcp","from_port":8443,"destination":"10.0.0.0/8"},"summary":"allow mTLS egress"}]}'
`), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	manifestPath := filepath.Join(dir, "skiff-plugin.json")
	manifest := fmt.Sprintf(`{
  "apiVersion": "skiff.dev/plugin/v1alpha1",
  "kind": "Plugin",
  "name": "mtls-egress",
  "version": "0.1.0",
  "runtime": {"kind": "command", "command": [%q]},
  "hooks": ["mutate_ir"],
  "permissions": {"allowed_patch_kinds": ["SecurityGroupRule"]},
  "capabilities": [{"kind": "ir_patch", "name": "mtls-egress", "patch_kinds": ["SecurityGroupRule"]}]
}`, script)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"explain",
		filepath.Join("..", "..", "tests", "fixtures", "services", "minimal.yaml"),
		"--plugin", manifestPath,
		"--format", "json",
		"--trace-id", "tr_plugin_explain",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got struct {
		OK            bool `json:"ok"`
		PluginPatches []struct {
			Plugin  string `json:"plugin"`
			Kind    string `json:"kind"`
			Summary string `json:"summary"`
		} `json:"plugin_patches"`
		AWS struct {
			SecurityGroups []struct {
				LogicalID string `json:"logical_id"`
				Egress    []struct {
					ToPort int    `json:"to_port"`
					CIDR   string `json:"cidr"`
				} `json:"egress"`
			} `json:"security_groups"`
		} `json:"aws"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("explain output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || len(got.PluginPatches) != 1 || got.PluginPatches[0].Plugin != "mtls-egress" {
		t.Fatalf("plugin patches = %+v", got.PluginPatches)
	}
	found := false
	for _, group := range got.AWS.SecurityGroups {
		for _, rule := range group.Egress {
			if rule.ToPort == 8443 && rule.CIDR == "10.0.0.0/8" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("plugin egress rule not lowered into AWS security group: %s", stdout.String())
	}
}

func TestPluginValidateInvalidManifestJSON(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "skiff-plugin.json")
	if err := os.WriteFile(manifestPath, []byte(`{"apiVersion":"skiff.dev/plugin/v1alpha1","kind":"Plugin","name":"Bad_Name"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"plugin", "validate", manifestPath, "--format", "json", "--trace-id", "tr_bad_plugin"}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d", code, ExitUserError)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "PLUGIN_NAME_INVALID") {
		t.Fatalf("stdout missing manifest diagnostic: %s", stdout.String())
	}
}
