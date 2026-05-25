package packages_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/compiler"
	internalpackages "github.com/s1liconcow/skiff/internal/packages"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestPackageManifestDecodeStrictAndValidate(t *testing.T) {
	body := []byte(`{
		"apiVersion": "skiff.dev/package/v1alpha1",
		"kind": "Package",
		"name": "postgres-ha",
		"version": "1.2.0",
		"exports": {
			"dependencies": ["postgres-ha"],
			"operation_profiles": ["primary-switchover-update"],
			"package_steps": ["postgres.verify_replica_lag"],
			"doctor_checks": ["postgres.verify_replica_lag"]
		},
		"plugin": {"manifest": "plugin.json"}
	}`)
	manifest, err := internalpackages.DecodeManifest(body)
	if err != nil {
		t.Fatalf("DecodeManifest returned error: %v", err)
	}
	if diagnostics := internalpackages.ValidateManifest(*manifest); len(diagnostics) > 0 {
		t.Fatalf("ValidateManifest diagnostics = %+v, want none", diagnostics)
	}

	if _, err := internalpackages.DecodeManifest([]byte(`{"apiVersion":"skiff.dev/package/v1alpha1","kind":"Package","name":"postgres-ha","version":"1.2.0","unknown":true}`)); err == nil {
		t.Fatal("DecodeManifest accepted unknown field")
	}
}

func TestLockfileCanonicalJSONAndValidation(t *testing.T) {
	lock := validLock()
	body, err := internalpackages.CanonicalLockJSON(lock)
	if err != nil {
		t.Fatalf("CanonicalLockJSON returned error: %v", err)
	}
	want := `{"schema":"skiff.lock/v1alpha1","packages":[{"name":"db","ref":"skiff.dev/postgres-ha","version":"1.2.0","digest":"` + digestA + `","signature_ref":"oci://registry.example.com/skiff/postgres-ha.sig@` + digestB + `","source":"oci://registry.example.com/skiff/postgres-ha:1.2.0","manifest_digest":"` + digestB + `","resolved_at":"2026-05-20T02:43:35Z"}]}`
	if string(body) != want {
		t.Fatalf("canonical lock JSON mismatch:\n got: %s\nwant: %s", string(body), want)
	}
	if diagnostics := internalpackages.ValidateLock(lock, internalpackages.ValidationOptions{}); len(diagnostics) > 0 {
		t.Fatalf("ValidateLock diagnostics = %+v, want none", diagnostics)
	}
}

func TestProductionStackRequiresDigestLockedSignedPackage(t *testing.T) {
	doc := productionPackageStack(t)
	productionPolicy := internalpackages.ValidationOptions{RequirePackageLock: true, RequireSignedLockEntries: true}
	diagnostics := internalpackages.ValidateStackLock(doc, nil, productionPolicy)
	assertPackageDiagnostic(t, diagnostics, "$.stack.dependencies", "PACKAGE_LOCK_REQUIRED")

	lock := validLock()
	lock.Packages[0].SignatureRef = ""
	diagnostics = internalpackages.ValidateStackLock(doc, &lock, productionPolicy)
	assertPackageDiagnostic(t, diagnostics, "$.packages[0].signature_ref", "PACKAGE_SIGNATURE_REQUIRED")

	lock = validLock()
	lock.Packages[0].Digest = "sha256:mutable"
	diagnostics = internalpackages.ValidateStackLock(doc, &lock, productionPolicy)
	assertPackageDiagnostic(t, diagnostics, "$.packages[0].digest", "INVALID_DIGEST")

	lock = validLock()
	if diagnostics = internalpackages.ValidateStackLock(doc, &lock, productionPolicy); len(diagnostics) > 0 {
		t.Fatalf("ValidateStackLock diagnostics = %+v, want none", diagnostics)
	}
}

func TestProdEnvNameDoesNotImplyProductionPackagePolicy(t *testing.T) {
	doc := productionPackageStack(t)
	if diagnostics := internalpackages.ValidateStackLock(doc, nil, internalpackages.ValidationOptions{}); len(diagnostics) > 0 {
		t.Fatalf("ValidateStackLock diagnostics = %+v, want none without explicit production policy", diagnostics)
	}
}

func TestDevUnsignedLocalPackageRequiresExplicitAllowance(t *testing.T) {
	doc := productionPackageStack(t)
	doc.Metadata.Env = "dev"
	doc.Stack.Dependencies[0].Uses = "file://../postgres-ha"
	lock := validLock()
	lock.Packages[0].Ref = "file://../postgres-ha"
	lock.Packages[0].Source = "file://../postgres-ha"
	lock.Packages[0].SignatureRef = ""

	diagnostics := internalpackages.ValidateStackLock(doc, &lock, internalpackages.ValidationOptions{})
	assertPackageDiagnostic(t, diagnostics, "$.packages[0].signature_ref", "PACKAGE_SIGNATURE_REQUIRED")

	if diagnostics = internalpackages.ValidateStackLock(doc, &lock, internalpackages.ValidationOptions{AllowUnsignedLocal: true}); len(diagnostics) > 0 {
		t.Fatalf("ValidateStackLock diagnostics = %+v, want none", diagnostics)
	}
}

func TestProductionCompilerRejectsMissingPackageLock(t *testing.T) {
	doc := productionPackageStack(t)
	_, err := compiler.Compile(context.Background(), doc, compiler.Options{ReleasePolicy: schema.EnvironmentReleasePolicy{RequireSignedReleases: true}})
	if err == nil {
		t.Fatal("Compile returned nil error, want missing package lock")
	}
	var validation spec.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Compile error = %v, want spec.ValidationError", err)
	}
	assertPackageDiagnostic(t, validation.Diagnostics, "$.stack.dependencies", "PACKAGE_LOCK_REQUIRED")
}

func TestResolveLocalPackageCachesAndDetectsDigestMismatch(t *testing.T) {
	dir := writeTestPackage(t, "postgres-ha", true)
	cacheRoot := t.TempDir()
	clock := func() time.Time { return time.Date(2026, 5, 20, 3, 30, 0, 0, time.UTC) }
	resolved, err := internalpackages.Resolve(context.Background(), "file://"+dir, internalpackages.ResolveOptions{
		Cache: internalpackages.Cache{Root: cacheRoot},
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.Entry.Name != "postgres-ha" || resolved.Entry.Digest == "" || resolved.Entry.ManifestDigest == "" || resolved.Entry.SignatureRef == "" {
		t.Fatalf("resolved entry missing fields: %+v", resolved.Entry)
	}
	if _, err := os.Stat(filepath.Join(resolved.Cache.Path, "skiff-package.json")); err != nil {
		t.Fatalf("package not stored in content-addressed cache: %v", err)
	}

	reused, err := internalpackages.Resolve(context.Background(), "file://"+dir, internalpackages.ResolveOptions{
		Cache: internalpackages.Cache{Root: cacheRoot},
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("Resolve second pass returned error: %v", err)
	}
	if !reused.Cache.Reused {
		t.Fatalf("second resolve did not reuse cache: %+v", reused.Cache)
	}

	_, err = internalpackages.Resolve(context.Background(), "file://"+dir, internalpackages.ResolveOptions{
		Cache:          internalpackages.Cache{Root: cacheRoot},
		ExpectedDigest: digestA,
		Clock:          clock,
	})
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("Resolve digest mismatch err = %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "skiff-package.sig"), []byte("rotated-signature"), 0o644); err != nil {
		t.Fatalf("rewrite signature: %v", err)
	}
	rotated, err := internalpackages.Resolve(context.Background(), "file://"+dir, internalpackages.ResolveOptions{
		Cache:          internalpackages.Cache{Root: cacheRoot},
		ExpectedDigest: resolved.Entry.Digest,
		Clock:          clock,
	})
	if err != nil {
		t.Fatalf("Resolve after signature rotation returned error: %v", err)
	}
	if rotated.Entry.Digest != resolved.Entry.Digest {
		t.Fatalf("package digest changed after signature rotation: got %s want %s", rotated.Entry.Digest, resolved.Entry.Digest)
	}
}

func TestResolveLocalPackageRequiresSignatureUnlessAllowed(t *testing.T) {
	dir := writeTestPackage(t, "redis-ha", false)
	_, err := internalpackages.Resolve(context.Background(), "file://"+dir, internalpackages.ResolveOptions{
		Cache: internalpackages.Cache{Root: t.TempDir()},
	})
	if err == nil || !strings.Contains(err.Error(), "signature is required") {
		t.Fatalf("Resolve err = %v, want signature required", err)
	}
	resolved, err := internalpackages.Resolve(context.Background(), "file://"+dir, internalpackages.ResolveOptions{
		Cache:              internalpackages.Cache{Root: t.TempDir()},
		AllowUnsignedLocal: true,
	})
	if err != nil {
		t.Fatalf("Resolve with AllowUnsignedLocal returned error: %v", err)
	}
	if resolved.Entry.SignatureRef != "" {
		t.Fatalf("unsigned local package wrote signature ref: %+v", resolved.Entry)
	}
}

func TestResolveLocalPackageRejectsMissingExplicitSignatureRef(t *testing.T) {
	dir := writeTestPackage(t, "mysql-ha", true)
	missing := filepath.Join(t.TempDir(), "missing.sig")
	_, err := internalpackages.Resolve(context.Background(), "file://"+dir, internalpackages.ResolveOptions{
		Cache:        internalpackages.Cache{Root: t.TempDir()},
		SignatureRef: "file://" + missing,
	})
	if err == nil || !strings.Contains(err.Error(), "signature file was not found") {
		t.Fatalf("Resolve err = %v, want missing explicit signature", err)
	}
}

func TestResolveOCIPackageVerifiesCachedDigest(t *testing.T) {
	dir := writeTestPackage(t, "kafka-ha", true)
	cacheRoot := t.TempDir()
	resolved, err := internalpackages.Resolve(context.Background(), "file://"+dir, internalpackages.ResolveOptions{
		Cache: internalpackages.Cache{Root: cacheRoot},
	})
	if err != nil {
		t.Fatalf("Resolve local package returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resolved.Cache.Path, "README.md"), []byte("# tampered\n"), 0o644); err != nil {
		t.Fatalf("tamper cache: %v", err)
	}
	_, err = internalpackages.Resolve(context.Background(), "oci://registry.example.com/skiff/kafka-ha@"+resolved.Entry.Digest, internalpackages.ResolveOptions{
		Cache: internalpackages.Cache{Root: cacheRoot},
	})
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("Resolve OCI cache err = %v, want digest mismatch", err)
	}
}

func TestLockEntryAddRejectsDuplicatePackageNames(t *testing.T) {
	lock := internalpackages.LockFile{Schema: "skiff.lock/v1alpha1"}
	first := internalpackages.LockEntry{Name: "db", Ref: "file://one", Version: "1.0.0", Digest: digestA, Source: "file://one", ManifestDigest: digestB, ResolvedAt: "2026-05-20T03:30:00Z"}
	next := internalpackages.LockEntry{Name: "db", Ref: "file://two", Version: "1.0.0", Digest: digestB, Source: "file://two", ManifestDigest: digestA, ResolvedAt: "2026-05-20T03:30:00Z"}
	lock, err := internalpackages.AddLockEntry(lock, first)
	if err != nil {
		t.Fatalf("AddLockEntry first: %v", err)
	}
	if _, err := internalpackages.AddLockEntry(lock, next); err == nil || !strings.Contains(err.Error(), "already locked") {
		t.Fatalf("AddLockEntry duplicate err = %v", err)
	}
}

func validLock() internalpackages.LockFile {
	return internalpackages.LockFile{
		Schema: "skiff.lock/v1alpha1",
		Packages: []internalpackages.LockEntry{{
			Name:           "db",
			Ref:            "skiff.dev/postgres-ha",
			Version:        "1.2.0",
			Digest:         digestA,
			SignatureRef:   "oci://registry.example.com/skiff/postgres-ha.sig@" + digestB,
			Source:         "oci://registry.example.com/skiff/postgres-ha:1.2.0",
			ManifestDigest: digestB,
			ResolvedAt:     "2026-05-20T02:43:35Z",
		}},
	}
}

func productionPackageStack(t *testing.T) spec.Document {
	t.Helper()
	doc, err := spec.Decode([]byte(`
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: orders
  env: prod
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/orders-api@sha256:abc123
      runtime:
        port: 8080
        health:
          path: /healthz
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: 1.x
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
`), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	return *doc
}

func assertPackageDiagnostic(t *testing.T, diagnostics []spec.Diagnostic, path, code string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Path == path && diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("diagnostics = %+v, want %s %s", diagnostics, path, code)
}

func writeTestPackage(t *testing.T, name string, signed bool) string {
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
			"package_steps": ["` + name + `.doctor"],
			"doctor_checks": ["` + name + `.doctor"]
		},
		"plugin": {"manifest": "plugin.json"}
	}`
	if err := os.WriteFile(filepath.Join(dir, "skiff-package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	plugin := `{
		"apiVersion": "skiff.dev/plugin/v1alpha1",
		"kind": "Plugin",
		"name": "` + name + `",
		"version": "1.2.0",
		"runtime": {"kind": "command", "command": ["` + name + `-plugin"]},
		"hooks": ["saga_step"],
		"permissions": {"saga_step_kinds": ["` + name + `.doctor"]},
		"capabilities": [{"kind": "saga_step", "name": "` + name + `.doctor", "saga_step_kinds": ["` + name + `.doctor"]}]
	}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(plugin), 0o644); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if signed {
		if err := os.WriteFile(filepath.Join(dir, "skiff-package.sig"), []byte("test-signature"), 0o644); err != nil {
			t.Fatalf("write signature: %v", err)
		}
	}
	return dir
}
