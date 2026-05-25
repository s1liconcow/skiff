package release

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/compat"
	"github.com/s1liconcow/skiff/internal/security"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Finding struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
}

type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
}

type VerifyOptions struct {
	Service               string
	Env                   string
	ReleaseID             string
	RuntimeManifest       *schema.RuntimeManifest
	Verifier              signing.Verifier
	Now                   time.Time
	RequireArtifactDigest bool
	AllowUnsignedRelease  bool
	RunnerVersion         string
}

type VerificationResult struct {
	OK                    bool              `json:"ok"`
	Service               string            `json:"service"`
	Env                   string            `json:"env"`
	ReleaseID             string            `json:"release_id"`
	Digest                string            `json:"digest,omitempty"`
	RuntimeManifestDigest string            `json:"runtime_manifest_digest,omitempty"`
	VerifiedSignature     *schema.Signature `json:"verified_signature,omitempty"`
	Checks                []Check           `json:"checks"`
	Findings              []Finding         `json:"findings,omitempty"`
}

func RuntimeManifestDigest(manifest schema.RuntimeManifest) (string, error) {
	return canonicalDocumentDigest(manifest)
}

func ManifestDigest(manifest schema.ReleaseManifest) (string, error) {
	unsigned := UnsignedManifest(manifest)
	return canonicalDocumentDigest(unsigned)
}

func UnsignedManifest(manifest schema.ReleaseManifest) schema.ReleaseManifest {
	manifest.Digest = ""
	manifest.Signatures = nil
	return manifest
}

func canonicalDocumentDigest(document any) (string, error) {
	body, err := canonical.Marshal(document)
	if err != nil {
		return "", err
	}
	return security.CanonicalJSONDigest(body)
}

func SignManifest(ctx context.Context, manifest schema.ReleaseManifest, signer signing.Signer, actor schema.Actor, signedAt time.Time) (schema.ReleaseManifest, error) {
	digest, err := ManifestDigest(manifest)
	if err != nil {
		return schema.ReleaseManifest{}, err
	}
	signature, err := signing.SignDigest(ctx, signer, digest, actor, signedAt)
	if err != nil {
		return schema.ReleaseManifest{}, err
	}
	manifest.Digest = digest
	manifest.Signatures = append([]schema.Signature(nil), signature)
	return manifest, nil
}

func VerifyManifest(ctx context.Context, manifest schema.ReleaseManifest, opts VerifyOptions) VerificationResult {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	result := VerificationResult{
		Service:               manifest.Service,
		Env:                   manifest.Env,
		ReleaseID:             manifest.ReleaseID,
		Digest:                manifest.Digest,
		RuntimeManifestDigest: manifest.RuntimeManifestDigest,
	}

	addCheck := func(name string, ok bool, successSummary, failureCode, failureSummary string) {
		summary := successSummary
		if !ok {
			summary = failureSummary
			result.Findings = append(result.Findings, Finding{Code: failureCode, Summary: failureSummary})
		}
		result.Checks = append(result.Checks, Check{Name: name, OK: ok, Summary: summary})
	}

	addCheck(
		"schema",
		manifest.SchemaVersion == schema.Version,
		"schema version is supported",
		"UNSUPPORTED_SCHEMA",
		fmt.Sprintf("release schema version %q is not supported", manifest.SchemaVersion),
	)
	addCheck(
		"service",
		opts.Service == "" || manifest.Service == opts.Service,
		"service matches expected target",
		"SERVICE_MISMATCH",
		fmt.Sprintf("release service %q does not match expected service %q", manifest.Service, opts.Service),
	)
	addCheck(
		"env",
		opts.Env == "" || manifest.Env == opts.Env,
		"environment matches expected target",
		"ENV_MISMATCH",
		fmt.Sprintf("release env %q does not match expected env %q", manifest.Env, opts.Env),
	)
	addCheck(
		"release_id",
		opts.ReleaseID == "" || manifest.ReleaseID == opts.ReleaseID,
		"release ID matches expected target",
		"RELEASE_ID_MISMATCH",
		fmt.Sprintf("release ID %q does not match expected release %q", manifest.ReleaseID, opts.ReleaseID),
	)

	expectedDigest, err := ManifestDigest(manifest)
	if err != nil {
		result.Checks = append(result.Checks, Check{Name: "digest", OK: false, Summary: err.Error()})
		result.Findings = append(result.Findings, Finding{Code: "DIGEST_CALCULATION_FAILED", Summary: err.Error()})
	} else {
		result.Digest = expectedDigest
		addCheck(
			"digest",
			manifest.Digest != "" && manifest.Digest == expectedDigest,
			"release digest matches canonical unsigned manifest",
			"INVALID_DIGEST",
			"release digest is missing or does not match canonical unsigned manifest",
		)
	}

	if opts.RequireArtifactDigest {
		addCheck(
			"artifact_digest",
			security.IsSHA256Digest(manifest.Artifact.Digest),
			"artifact is digest-pinned",
			"ARTIFACT_DIGEST_REQUIRED",
			"this environment class requires a sha256 artifact digest",
		)
	}

	if opts.RuntimeManifest != nil {
		verifyRuntimeManifest(manifest, *opts.RuntimeManifest, &result, addCheck)
	}

	verifyExpiry(manifest, now, &result, addCheck)
	verifyRunnerVersion(manifest, opts.RunnerVersion, &result, addCheck)
	verifySignature(ctx, manifest, expectedDigest, opts.Verifier, opts.AllowUnsignedRelease, &result, addCheck)

	result.OK = len(result.Findings) == 0
	return result
}

func verifyRunnerVersion(manifest schema.ReleaseManifest, runnerVersion string, result *VerificationResult, addCheck func(string, bool, string, string, string)) {
	if manifest.MinRunnerVersion == "" {
		result.Checks = append(result.Checks, Check{Name: "runner_version", OK: true, Summary: "release does not declare a minimum runner version"})
		return
	}
	if runnerVersion == "" {
		result.Checks = append(result.Checks, Check{Name: "runner_version", OK: true, Summary: "runner version was not provided; release minimum was not evaluated"})
		return
	}
	findings := compat.CheckRunnerRelease(runnerVersion, manifest.MinRunnerVersion)
	if len(findings) == 0 {
		addCheck("runner_version", true, "runner version satisfies release minimum", "", "")
		return
	}
	code := findings[0].Code
	summary := findings[0].Summary
	addCheck("runner_version", false, "", code, summary)
}

func verifyRuntimeManifest(manifest schema.ReleaseManifest, runtimeManifest schema.RuntimeManifest, result *VerificationResult, addCheck func(string, bool, string, string, string)) {
	addCheck(
		"runtime_schema",
		runtimeManifest.SchemaVersion == schema.Version,
		"runtime manifest schema version is supported",
		"UNSUPPORTED_RUNTIME_SCHEMA",
		fmt.Sprintf("runtime manifest schema version %q is not supported", runtimeManifest.SchemaVersion),
	)
	addCheck(
		"runtime_target",
		runtimeManifest.Service == manifest.Service && runtimeManifest.Env == manifest.Env && runtimeManifest.ReleaseID == manifest.ReleaseID,
		"runtime manifest target matches release",
		"RUNTIME_TARGET_MISMATCH",
		"runtime manifest service, env, or release ID does not match release manifest",
	)
	digest, err := RuntimeManifestDigest(runtimeManifest)
	if err != nil {
		result.Checks = append(result.Checks, Check{Name: "runtime_digest", OK: false, Summary: err.Error()})
		result.Findings = append(result.Findings, Finding{Code: "RUNTIME_DIGEST_CALCULATION_FAILED", Summary: err.Error()})
		return
	}
	result.RuntimeManifestDigest = digest
	addCheck(
		"runtime_digest",
		manifest.RuntimeManifestDigest != "" && manifest.RuntimeManifestDigest == digest,
		"runtime manifest digest matches release",
		"RUNTIME_MANIFEST_DIGEST_MISMATCH",
		"runtime manifest digest is missing or does not match release manifest",
	)
}

func verifyExpiry(manifest schema.ReleaseManifest, now time.Time, result *VerificationResult, addCheck func(string, bool, string, string, string)) {
	if manifest.ExpiresAt == "" {
		result.Checks = append(result.Checks, Check{Name: "expiry", OK: true, Summary: "release has no expiry"})
		return
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, manifest.ExpiresAt)
	if err != nil {
		addCheck("expiry", false, "", "INVALID_EXPIRY", fmt.Sprintf("release expiry %q is not RFC3339", manifest.ExpiresAt))
		return
	}
	addCheck(
		"expiry",
		!now.After(expiresAt),
		"release is not expired",
		"RELEASE_EXPIRED",
		fmt.Sprintf("release expired at %s", expiresAt.UTC().Format(time.RFC3339Nano)),
	)
}

func verifySignature(ctx context.Context, manifest schema.ReleaseManifest, digest string, verifier signing.Verifier, allowUnsigned bool, result *VerificationResult, addCheck func(string, bool, string, string, string)) {
	if digest == "" {
		addCheck("signature", false, "", "SIGNATURE_SKIPPED", "signature verification requires a valid digest")
		return
	}
	if allowUnsigned && len(manifest.Signatures) == 0 {
		addCheck("signature", true, "unsigned release is allowed by environment policy", "", "")
		return
	}
	signature, err := signing.VerifyAny(ctx, verifier, digest, manifest.Signatures)
	if err == nil {
		result.VerifiedSignature = &signature
		addCheck("signature", true, "at least one release signature is valid", "", "")
		return
	}
	code := "INVALID_SIGNATURE"
	if errors.Is(err, signing.ErrNoValidSignature) && len(manifest.Signatures) == 0 {
		code = "MISSING_SIGNATURE"
	}
	addCheck("signature", false, "", code, err.Error())
}
