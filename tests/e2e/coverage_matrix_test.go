package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverageMatrixIsCompleteAndDocumented(t *testing.T) {
	matrix := e2eCoverageMatrix()
	if len(matrix) == 0 {
		t.Fatal("coverage matrix is empty")
	}
	seen := map[string]bool{}
	validStatuses := map[string]bool{
		coverageCovered:        true,
		coverageOptional:       true,
		coverageGated:          true,
		coverageNotImplemented: true,
		coverageNotApplicable:  true,
	}
	docs, err := os.ReadFile(filepath.Join("..", "..", "docs", "dev", "e2e-matrix.md"))
	if err != nil {
		t.Fatalf("read e2e docs: %v", err)
	}
	docText := string(docs)
	for _, row := range matrix {
		if strings.TrimSpace(row.Capability) == "" {
			t.Fatalf("coverage row with empty capability: %+v", row)
		}
		if seen[row.Capability] {
			t.Fatalf("duplicate capability row %q", row.Capability)
		}
		seen[row.Capability] = true
		for name, status := range map[string]string{"local": row.Local, "apple": row.AppleSilicon, "aws": row.AWS} {
			if !validStatuses[status] {
				t.Fatalf("%s has invalid %s status %q", row.Capability, name, status)
			}
		}
		if strings.TrimSpace(row.Evidence) == "" || strings.TrimSpace(row.Command) == "" {
			t.Fatalf("coverage row missing evidence or command: %+v", row)
		}
		if !strings.Contains(docText, row.Capability) {
			t.Fatalf("docs/dev/e2e-matrix.md does not mention capability %q", row.Capability)
		}
	}
}
