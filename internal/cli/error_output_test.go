package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRootInvalidJSONMatchesGoldenFailureEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{"--format", "json", "--trace-id", "tr_golden", "--bogus"}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, stderr = %s stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	golden, err := os.ReadFile(filepath.Join("..", "..", "tests", "golden", "cli", "root_invalid.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	want := append(bytes.TrimSpace(golden), '\n')
	if !bytes.Equal(stdout.Bytes(), want) {
		t.Fatalf("golden mismatch\nwant: %s\ngot:  %s", want, stdout.Bytes())
	}
}
