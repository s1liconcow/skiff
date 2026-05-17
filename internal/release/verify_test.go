package release_test

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestVerifyManifestAcceptsSignedRelease(t *testing.T) {
	manifest, runtimeManifest, verifier := signedReleaseFixture(t, "2026-06-16T17:00:00Z")

	result := release.VerifyManifest(context.Background(), manifest, release.VerifyOptions{
		Service:         "payments-api",
		Env:             "prod",
		RuntimeManifest: &runtimeManifest,
		Verifier:        verifier,
		Now:             time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	})
	if !result.OK {
		t.Fatalf("VerifyManifest failed: %+v", result.Findings)
	}
	if result.VerifiedSignature == nil || result.VerifiedSignature.KeyID != "local-test" {
		t.Fatalf("verified signature = %+v", result.VerifiedSignature)
	}
}

func TestVerifyManifestRejectsWrongTargetExpiredTamperedAndMissingSignature(t *testing.T) {
	manifest, _, verifier := signedReleaseFixture(t, "2026-06-16T17:00:00Z")

	wrongEnv := release.VerifyManifest(context.Background(), manifest, release.VerifyOptions{
		Service:  "payments-api",
		Env:      "staging",
		Verifier: verifier,
		Now:      time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	})
	if !hasFinding(wrongEnv.Findings, "ENV_MISMATCH") {
		t.Fatalf("wrong env findings = %+v", wrongEnv.Findings)
	}

	wrongService := release.VerifyManifest(context.Background(), manifest, release.VerifyOptions{
		Service:  "orders-api",
		Env:      "prod",
		Verifier: verifier,
		Now:      time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	})
	if !hasFinding(wrongService.Findings, "SERVICE_MISMATCH") {
		t.Fatalf("wrong service findings = %+v", wrongService.Findings)
	}

	wrongRelease := release.VerifyManifest(context.Background(), manifest, release.VerifyOptions{
		Service:   "payments-api",
		Env:       "prod",
		ReleaseID: "rel_other",
		Verifier:  verifier,
		Now:       time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	})
	if !hasFinding(wrongRelease.Findings, "RELEASE_ID_MISMATCH") {
		t.Fatalf("wrong release findings = %+v", wrongRelease.Findings)
	}

	expired := release.VerifyManifest(context.Background(), manifest, release.VerifyOptions{
		Service:  "payments-api",
		Env:      "prod",
		Verifier: verifier,
		Now:      time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC),
	})
	if !hasFinding(expired.Findings, "RELEASE_EXPIRED") {
		t.Fatalf("expired findings = %+v", expired.Findings)
	}

	tampered := manifest
	tampered.ReleaseID = "rel_tampered"
	tamperResult := release.VerifyManifest(context.Background(), tampered, release.VerifyOptions{
		Service:  "payments-api",
		Env:      "prod",
		Verifier: verifier,
		Now:      time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	})
	if !hasFinding(tamperResult.Findings, "INVALID_DIGEST") || !hasFinding(tamperResult.Findings, "INVALID_SIGNATURE") {
		t.Fatalf("tamper findings = %+v", tamperResult.Findings)
	}

	missingSignature := manifest
	missingSignature.Signatures = nil
	missingResult := release.VerifyManifest(context.Background(), missingSignature, release.VerifyOptions{
		Service:  "payments-api",
		Env:      "prod",
		Verifier: verifier,
		Now:      time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	})
	if !hasFinding(missingResult.Findings, "MISSING_SIGNATURE") {
		t.Fatalf("missing signature findings = %+v", missingResult.Findings)
	}

	weakArtifactDigest := unsignedReleaseFixture(t, "2026-06-16T17:00:00Z")
	weakArtifactDigest.Artifact.Digest = "sha256:abc"
	weakArtifactDigest = signManifest(t, weakArtifactDigest)
	weakArtifactResult := release.VerifyManifest(context.Background(), weakArtifactDigest, release.VerifyOptions{
		Service:  "payments-api",
		Env:      "prod",
		Verifier: verifier,
		Now:      time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	})
	if !hasFinding(weakArtifactResult.Findings, "ARTIFACT_DIGEST_REQUIRED") {
		t.Fatalf("weak artifact digest findings = %+v", weakArtifactResult.Findings)
	}
}

func TestVerifyManifestRejectsRuntimeDigestMismatchAndUnsupportedSchema(t *testing.T) {
	_, runtimeManifest, verifier := signedReleaseFixture(t, "2026-06-16T17:00:00Z")
	badRuntimeDigest := unsignedReleaseFixture(t, "2026-06-16T17:00:00Z")
	badRuntimeDigest.RuntimeManifestDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	badRuntimeDigest = signManifest(t, badRuntimeDigest)

	runtimeResult := release.VerifyManifest(context.Background(), badRuntimeDigest, release.VerifyOptions{
		Service:         "payments-api",
		Env:             "prod",
		RuntimeManifest: &runtimeManifest,
		Verifier:        verifier,
		Now:             time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	})
	if !hasFinding(runtimeResult.Findings, "RUNTIME_MANIFEST_DIGEST_MISMATCH") {
		t.Fatalf("runtime digest findings = %+v", runtimeResult.Findings)
	}
	if hasFinding(runtimeResult.Findings, "INVALID_SIGNATURE") {
		t.Fatalf("runtime digest mismatch should keep signature valid: %+v", runtimeResult.Findings)
	}

	unsupported := unsignedReleaseFixture(t, "2026-06-16T17:00:00Z")
	unsupported.SchemaVersion = "skiff.state/v0"
	unsupported = signManifest(t, unsupported)
	schemaResult := release.VerifyManifest(context.Background(), unsupported, release.VerifyOptions{
		Service:  "payments-api",
		Env:      "prod",
		Verifier: verifier,
		Now:      time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	})
	if !hasFinding(schemaResult.Findings, "UNSUPPORTED_SCHEMA") {
		t.Fatalf("unsupported schema findings = %+v", schemaResult.Findings)
	}
}

func TestVerifyManifestRejectsUnsupportedRunnerVersion(t *testing.T) {
	manifest, runtimeManifest, verifier := signedReleaseFixture(t, "2026-06-16T17:00:00Z")
	manifest.MinRunnerVersion = "1.2.0"
	manifest = signManifest(t, release.UnsignedManifest(manifest))

	result := release.VerifyManifest(context.Background(), manifest, release.VerifyOptions{
		Service:         "payments-api",
		Env:             "prod",
		RuntimeManifest: &runtimeManifest,
		Verifier:        verifier,
		Now:             time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
		RunnerVersion:   "1.1.9",
	})
	if !hasFinding(result.Findings, "RUNNER_VERSION_TOO_OLD") {
		t.Fatalf("runner version findings = %+v", result.Findings)
	}
}

func signedReleaseFixture(t *testing.T, expiresAt string) (schema.ReleaseManifest, schema.RuntimeManifest, *signing.LocalVerifier) {
	t.Helper()
	runtimeManifest := runtimeFixture()
	runtimeDigest, err := release.RuntimeManifestDigest(runtimeManifest)
	if err != nil {
		t.Fatalf("RuntimeManifestDigest returned error: %v", err)
	}
	manifest := unsignedReleaseFixture(t, expiresAt)
	manifest.RuntimeManifestDigest = runtimeDigest
	manifest = signManifest(t, manifest)
	verifier, err := signing.NewLocalVerifier(map[string]ed25519.PublicKey{"local-test": testSigner(t).PublicKey()})
	if err != nil {
		t.Fatalf("NewLocalVerifier returned error: %v", err)
	}
	return manifest, runtimeManifest, verifier
}

func runtimeFixture() schema.RuntimeManifest {
	return schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       "payments-api",
		Env:           "prod",
		ReleaseID:     "rel_01JABC",
		Command:       []string{"./payments-api", "serve"},
		EnvVars:       map[string]string{"PORT": "8080"},
		HealthCheck:   &schema.HealthCheck{Type: "http", Path: "/healthz", Port: 8080, Interval: "10s", Timeout: "2s"},
		CreatedAt:     "2026-05-16T17:00:00Z",
	}
}

func unsignedReleaseFixture(t *testing.T, expiresAt string) schema.ReleaseManifest {
	t.Helper()
	runtimeDigest, err := release.RuntimeManifestDigest(runtimeFixture())
	if err != nil {
		t.Fatalf("RuntimeManifestDigest returned error: %v", err)
	}
	return schema.ReleaseManifest{
		SchemaVersion: schema.Version,
		Service:       "payments-api",
		Env:           "prod",
		ReleaseID:     "rel_01JABC",
		Artifact: schema.ArtifactRef{
			Type:   "oci",
			URI:    "oci://ghcr.io/acme/payments-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		RuntimeManifestKey:    "services/payments-api/releases/rel_01JABC/runtime-manifest.json",
		RuntimeManifestDigest: runtimeDigest,
		CreatedAt:             "2026-05-16T17:00:00Z",
		ExpiresAt:             expiresAt,
	}
}

func signManifest(t *testing.T, manifest schema.ReleaseManifest) schema.ReleaseManifest {
	t.Helper()
	signed, err := release.SignManifest(
		context.Background(),
		manifest,
		testSigner(t),
		schema.Actor{ID: "alpha-one", Type: "agent"},
		time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("SignManifest returned error: %v", err)
	}
	return signed
}

func testSigner(t *testing.T) *signing.LocalSigner {
	t.Helper()
	signer, err := signing.NewLocalSignerFromSeed("local-test", []byte(strings.Repeat("A", ed25519.SeedSize)))
	if err != nil {
		t.Fatalf("NewLocalSignerFromSeed returned error: %v", err)
	}
	return signer
}

func hasFinding(findings []release.Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
