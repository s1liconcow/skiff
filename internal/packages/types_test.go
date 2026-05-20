package packages_test

import (
	"context"
	"errors"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	internalpackages "github.com/s1liconcow/skiff/internal/packages"
	"github.com/s1liconcow/skiff/internal/spec"
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
	diagnostics := internalpackages.ValidateStackLock(doc, nil, internalpackages.ValidationOptions{})
	assertPackageDiagnostic(t, diagnostics, "$.stack.dependencies", "PACKAGE_LOCK_REQUIRED")

	lock := validLock()
	lock.Packages[0].SignatureRef = ""
	diagnostics = internalpackages.ValidateStackLock(doc, &lock, internalpackages.ValidationOptions{})
	assertPackageDiagnostic(t, diagnostics, "$.packages[0].signature_ref", "PACKAGE_SIGNATURE_REQUIRED")

	lock = validLock()
	lock.Packages[0].Digest = "sha256:mutable"
	diagnostics = internalpackages.ValidateStackLock(doc, &lock, internalpackages.ValidationOptions{})
	assertPackageDiagnostic(t, diagnostics, "$.packages[0].digest", "INVALID_DIGEST")

	lock = validLock()
	if diagnostics = internalpackages.ValidateStackLock(doc, &lock, internalpackages.ValidationOptions{}); len(diagnostics) > 0 {
		t.Fatalf("ValidateStackLock diagnostics = %+v, want none", diagnostics)
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
	_, err := compiler.Compile(context.Background(), doc, compiler.Options{})
	if err == nil {
		t.Fatal("Compile returned nil error, want missing package lock")
	}
	var validation spec.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Compile error = %v, want spec.ValidationError", err)
	}
	assertPackageDiagnostic(t, validation.Diagnostics, "$.stack.dependencies", "PACKAGE_LOCK_REQUIRED")
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
