package docs_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var markdownLinkPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

func TestDocsRelativeLinksResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	docsRoot := filepath.Join(root, "docs")
	err := filepath.WalkDir(docsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if filepath.Base(path) == "bead_plans" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Base(path) == "DESIGN.md" {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range markdownLinkPattern.FindAllStringSubmatch(string(body), -1) {
			target := strings.TrimSpace(match[1])
			if skipLinkTarget(target) {
				continue
			}
			cleanTarget := strings.TrimPrefix(strings.Split(target, "#")[0], "./")
			fullPath := filepath.Clean(filepath.Join(filepath.Dir(path), cleanTarget))
			if _, err := os.Stat(fullPath); err != nil {
				t.Fatalf("%s links to missing target %q", path, target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}
}

func TestGoldenDemoScriptsRunWithFakeProvider(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, script := range []string{"demos/quickstart-fake.sh", "demos/cicd-templates.sh"} {
		script := script
		t.Run(filepath.Base(script), func(t *testing.T) {
			outDir := filepath.Join(t.TempDir(), strings.TrimSuffix(filepath.Base(script), ".sh"))
			cmd := exec.Command("bash", script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(),
				"DEMO_OUT="+outDir,
				"SKIFF_BIN=go run ./cmd/skiff",
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", script, err, string(output))
			}
			entries, err := os.ReadDir(outDir)
			if err != nil {
				t.Fatalf("read demo output: %v", err)
			}
			if len(entries) == 0 {
				t.Fatalf("%s produced no artifacts", script)
			}
		})
	}
}

func skipLinkTarget(target string) bool {
	if target == "" || strings.HasPrefix(target, "#") {
		return true
	}
	for _, prefix := range []string{"http://", "https://", "mailto:"} {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	return strings.HasPrefix(target, "s3://") || strings.HasPrefix(target, "secret://")
}
