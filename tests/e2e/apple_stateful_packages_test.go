package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type statefulPackageValidationRow struct {
	Package   string
	Mode      string
	OpsemMode string
	Fixture   string
	Profile   string
	Status    string
}

func statefulPackageValidationMatrix() []statefulPackageValidationRow {
	return []statefulPackageValidationRow{
		{Package: "postgres-ha", Mode: "self-managed", OpsemMode: "primary-replica", Fixture: "postgres-ha", Profile: "primary-switchover-update", Status: coverageCovered},
		{Package: "mysql-ha", Mode: "self-managed", OpsemMode: "primary-replica", Fixture: "mysql-ha", Profile: "primary-switchover-update", Status: coverageNotImplemented},
		{Package: "kafka", Mode: "self-managed", OpsemMode: "partition-isr", Fixture: "kafka", Profile: "partition-quorum-rolling-update", Status: coverageNotImplemented},
		{Package: "nats-jetstream", Mode: "self-managed", OpsemMode: "raft-groups", Fixture: "nats-jetstream", Profile: "raft-group-rolling-update", Status: coverageNotImplemented},
		{Package: "redis-ha", Mode: "self-managed", OpsemMode: "primary-replica", Fixture: "redis-ha", Profile: "primary-switchover-update", Status: coverageNotImplemented},
		{Package: "redis-cluster", Mode: "self-managed", OpsemMode: "slot-cluster", Fixture: "redis-cluster", Profile: "slot-aware-failover-update", Status: coverageNotImplemented},
		{Package: "opensearch-ha", Mode: "self-managed", OpsemMode: "shard-cluster", Fixture: "opensearch-ha", Profile: "shard-allocation-rolling-update", Status: coverageNotImplemented},
		{Package: "elasticsearch-ha", Mode: "self-managed", OpsemMode: "shard-cluster", Fixture: "elasticsearch-ha", Profile: "shard-allocation-rolling-update", Status: coverageNotImplemented},
	}
}

func TestStatefulPackageValidationMatrixData(t *testing.T) {
	rows := statefulPackageValidationMatrix()
	if len(rows) == 0 {
		t.Fatal("stateful package validation matrix is empty")
	}
	docs := readStatefulPackageValidationDocs(t)
	seen := map[string]bool{}
	covered := 0
	for _, row := range rows {
		if row.Package == "" || row.Mode == "" || row.OpsemMode == "" || row.Profile == "" || row.Status == "" {
			t.Fatalf("incomplete package validation row: %+v", row)
		}
		if seen[row.Package] {
			t.Fatalf("duplicate package validation row for %s", row.Package)
		}
		seen[row.Package] = true
		if !strings.Contains(docs, row.Package) {
			t.Fatalf("docs/dev/e2e-matrix.md does not mention package %s", row.Package)
		}
		switch row.Status {
		case coverageCovered:
			covered++
			assertPackageFixtureExists(t, row.Fixture)
		case coverageNotImplemented:
			if packageFixtureExists(row.Fixture) {
				t.Fatalf("package fixture %s exists but matrix still marks it not_implemented", row.Fixture)
			}
		default:
			t.Fatalf("unsupported package validation status %q for %s", row.Status, row.Package)
		}
	}
	if covered == 0 {
		t.Fatal("stateful package validation matrix has no implemented packages")
	}
}

func readStatefulPackageValidationDocs(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "docs", "dev", "e2e-matrix.md"))
	if err != nil {
		t.Fatalf("read e2e matrix docs: %v", err)
	}
	return string(body)
}

func assertPackageFixtureExists(t *testing.T, fixture string) {
	t.Helper()
	if !packageFixtureExists(fixture) {
		t.Fatalf("package fixture %s is missing", fixture)
	}
}

func packageFixtureExists(fixture string) bool {
	info, err := os.Stat(filepath.Join("..", "fixtures", "packages", fixture, "skiff-package.json"))
	return err == nil && !info.IsDir()
}
