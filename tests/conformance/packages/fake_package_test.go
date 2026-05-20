package packageconformance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/packages"
)

func TestFakePackageConformance(t *testing.T) {
	Run(t, Suite{
		Ref:       "file://" + filepath.Join("..", "..", "fixtures", "packages", "fake-postgres"),
		CacheRoot: t.TempDir(),
	})
}

func TestFirstPartyPackageConformance(t *testing.T) {
	for _, fixture := range []string{
		"postgres-ha",
		"mysql-ha",
		"kafka",
		"nats-jetstream",
		"redis-ha",
		"redis-cluster",
		"opensearch-ha",
		"elasticsearch-ha",
	} {
		fixture := fixture
		t.Run(fixture, func(t *testing.T) {
			result := Run(t, Suite{
				Ref:       "file://" + filepath.Join("..", "..", "fixtures", "packages", fixture),
				CacheRoot: t.TempDir(),
			})
			if result.Package.Name != fixture {
				t.Fatalf("unexpected package summary: %+v", result.Package)
			}
		})
	}
}

func TestActualPostgresHAPackageConformance(t *testing.T) {
	result := Run(t, Suite{
		Ref:       "file://" + filepath.Join("..", "..", "..", "packages", "postgres-ha"),
		CacheRoot: t.TempDir(),
	})
	if result.Package.Name != "postgres-ha" {
		t.Fatalf("unexpected package summary: %+v", result.Package)
	}
}

func TestPackageConformanceFailsForBrokenPluginExport(t *testing.T) {
	dir := copyFixture(t, filepath.Join("..", "..", "fixtures", "packages", "fake-postgres"))
	pluginPath := filepath.Join(dir, "plugin.json")
	body, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	body = []byte(strings.ReplaceAll(string(body), `"package_step_kinds": ["postgres.verify_replica_lag"]`, `"package_step_kinds": []`))
	if err := os.WriteFile(pluginPath, body, 0o644); err != nil {
		t.Fatalf("write broken plugin: %v", err)
	}
	resolved, err := packages.Resolve(context.Background(), "file://"+dir, packages.ResolveOptions{
		Cache: packages.Cache{Root: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	result := packages.RunConformance(context.Background(), *resolved, packages.ConformanceOptions{OperationProfileHook: operationProfileHook})
	if result.OK {
		t.Fatalf("conformance unexpectedly passed")
	}
	found := false
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "PACKAGE_PLUGIN_INVALID" || diagnostic.Code == "PACKAGE_STEP_NOT_DECLARED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v, want package step/plugin diagnostic", result.Diagnostics)
	}
}

func copyFixture(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(src, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, entry.Name()), body, 0o644); err != nil {
			t.Fatalf("write %s: %v", entry.Name(), err)
		}
	}
	return dst
}
