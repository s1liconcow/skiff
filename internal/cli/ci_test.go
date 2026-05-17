package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIGenerateJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"ci", "generate", "github-actions", "--service", "payments-api", "--format", "json", "--trace-id", "tr_ci"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var got ciGenerateOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_ci" || got.Target != "github-actions" {
		t.Fatalf("unexpected output: %+v", got)
	}
	for _, want := range []string{"skiff validate", "skiff contract test", "skiff release candidate create", "skiff deploy", "skiff promote"} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("generated content missing %q:\n%s", want, got.Content)
		}
	}
}

func TestCIGenerateWritesOutFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, ".buildkite", "pipeline.yml")
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"ci", "generate", "buildkite", "--out", out, "--format", "human"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if !strings.Contains(string(body), "skiff release candidate create") || !strings.Contains(stdout.String(), "generated buildkite CI template") {
		t.Fatalf("unexpected output stdout=%s\nfile=%s", stdout.String(), string(body))
	}
}
