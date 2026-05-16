package smoke_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVersionJSONForEntrypoints(t *testing.T) {
	root := repoRoot(t)
	for _, tc := range []struct {
		name   string
		pkg    string
		binary string
	}{
		{name: "skiff", pkg: "./cmd/skiff", binary: "skiff"},
		{name: "skiffd", pkg: "./cmd/skiffd", binary: "skiffd"},
		{name: "skiff-runner", pkg: "./cmd/skiff-runner", binary: "skiff-runner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), tc.binary)
			if runtime.GOOS == "windows" {
				exe += ".exe"
			}
			build := exec.Command("go", "build", "-o", exe, tc.pkg)
			build.Dir = root
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, out)
			}

			cmd := exec.Command(exe, "version", "--format", "json", "--trace-id", "tr_smoke")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("version command failed: %v\n%s", err, out)
			}

			var got struct {
				OK        bool   `json:"ok"`
				Binary    string `json:"binary"`
				Version   string `json:"version"`
				Commit    string `json:"commit"`
				BuildDate string `json:"build_date"`
				TraceID   string `json:"trace_id"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("version output is not valid JSON: %v\n%s", err, out)
			}
			if !got.OK {
				t.Fatalf("ok = false, want true")
			}
			if got.Binary != tc.binary {
				t.Fatalf("binary = %q, want %q", got.Binary, tc.binary)
			}
			if got.Version == "" || got.Commit == "" || got.BuildDate == "" {
				t.Fatalf("version fields must be populated: %+v", got)
			}
			if got.TraceID != "tr_smoke" {
				t.Fatalf("trace_id = %q, want tr_smoke", got.TraceID)
			}
		})
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("could not find repository root")
		}
		dir = next
	}
}
