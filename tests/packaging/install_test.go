package packaging_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/spec"
)

func TestInstallScriptInstallsLocalReleaseArtifact(t *testing.T) {
	root := repoRoot(t)
	dist := t.TempDir()
	stage := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatalf("mkdir stage: %v", err)
	}
	for _, bin := range []string{"skiff", "skiffd", "skiff-runner", "skiff-worker"} {
		body := "#!/usr/bin/env sh\n"
		if bin == "skiff" {
			body += "echo skiff version v0.0.0\n"
		}
		if err := os.WriteFile(filepath.Join(stage, bin), []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", bin, err)
		}
	}
	asset := filepath.Join(dist, "skiff_v0.0.0_linux_amd64.tar.gz")
	cmd := exec.Command("tar", "-C", stage, "-czf", asset, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar: %v\n%s", err, out)
	}
	body, err := os.ReadFile(asset)
	if err != nil {
		t.Fatalf("read asset: %v", err)
	}
	sum := sha256.Sum256(body)
	if err := os.WriteFile(filepath.Join(dist, "checksums.txt"), []byte(fmt.Sprintf("%x  %s\n", sum, filepath.Base(asset))), 0o644); err != nil {
		t.Fatalf("write checksums: %v", err)
	}

	installDir := filepath.Join(t.TempDir(), "bin")
	cmd = exec.Command("bash", filepath.Join(root, "scripts", "install.sh"))
	cmd.Env = append(os.Environ(),
		"SKIFF_INSTALL_VERSION=v0.0.0",
		"SKIFF_INSTALL_BASE_URL=file://"+dist,
		"SKIFF_INSTALL_DIR="+installDir,
		"SKIFF_INSTALL_OS=linux",
		"SKIFF_INSTALL_ARCH=amd64",
		"SKIFF_INSTALL_CONFIGURE_AGENTS=0",
		"SKIFF_INSTALL_COMPLETIONS=0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(installDir, "skiff-runner")); err != nil {
		t.Fatalf("skiff-runner not installed: %v", err)
	}
}

func TestRunnerImageInputsAndSkiffdRecipeAreValid(t *testing.T) {
	root := repoRoot(t)
	service, err := os.ReadFile(filepath.Join(root, "build", "runner-image", "systemd", "skiff-runner.service"))
	if err != nil {
		t.Fatalf("read runner service: %v", err)
	}
	text := string(service)
	for _, required := range []string{"skiff-runner bootstrap", "skiff-runner run", "/etc/skiff/runner.json"} {
		if !strings.Contains(text, required) {
			t.Fatalf("runner service missing %q:\n%s", required, text)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "build", "runner-image", "packer.pkr.hcl")); err != nil {
		t.Fatalf("packer template missing: %v", err)
	}
	packerBody, err := os.ReadFile(filepath.Join(root, "build", "runner-image", "packer.pkr.hcl"))
	if err != nil {
		t.Fatalf("read packer template: %v", err)
	}
	packerText := string(packerBody)
	for _, required := range []string{
		`source "amazon-ebs" "runner_amd64"`,
		`source "amazon-ebs" "runner_arm64"`,
		`al2023-ami-2023.*-kernel-6.1-x86_64`,
		`al2023-ami-2023.*-kernel-6.1-arm64`,
		`skiff.dev/version`,
		`skiff.dev/provenance-commit`,
		`/skiff/runner/ami/al2023`,
		`runner-image-amd64-manifest.json`,
		`runner-image-arm64-manifest.json`,
	} {
		if !strings.Contains(packerText, required) {
			t.Fatalf("packer template missing %q:\n%s", required, packerText)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".github", "workflows", "runner-image.yml")); err != nil {
		t.Fatalf("runner image workflow missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "publish-runner-ami-ssm.sh")); err != nil {
		t.Fatalf("runner AMI SSM publish script missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "deprecate-runner-amis.sh")); err != nil {
		t.Fatalf("runner AMI deprecation script missing: %v", err)
	}
	doc, err := spec.LoadFile(filepath.Join(root, "examples", "skiffd", "skiff.yaml"), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("load skiffd recipe: %v", err)
	}
	if doc.Metadata.Name != "skiffd" || doc.Runtime.Port != 8585 {
		t.Fatalf("unexpected skiffd recipe: %+v", doc)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root not found")
		}
		dir = parent
	}
}
