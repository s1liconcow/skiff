package runner_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/runner"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestBootstrapFetchesVerifiesAndPersistsRelease(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	signer := testSigner(t, "local-test", "A")
	verifier := verifierFor(t, signer)

	cfg := parseRunnerConfig(t)
	putServiceControl(t, ctx, store, cfg.Service, cfg.Env, "rel_01JNEW", "rel_01JOLD")
	putSignedRelease(t, ctx, store, signer, releaseFixture{
		service:   cfg.Service,
		env:       cfg.Env,
		releaseID: "rel_01JNEW",
		createdAt: "2026-05-17T00:00:00Z",
		expiresAt: "2026-06-17T00:00:00Z",
	})

	statePath := filepath.Join(t.TempDir(), "state.json")
	events := &collectingSink{}
	result, err := runner.Bootstrap(ctx, runner.BootstrapRequest{
		Config:   cfg,
		Store:    store,
		Verifier: verifier,
		MetadataProvider: runner.StaticMetadataProvider{Value: runner.Identity{
			Provider:   "aws",
			Region:     "us-west-2",
			InstanceID: "i-abc123",
			AccountID:  "123456789012",
			Zone:       "us-west-2a",
		}},
		StateStore: runner.FileStateStore{Path: statePath},
		EventSink:  events,
		TraceID:    "tr_boot",
		Now:        fixedNow,
	})
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if !result.Verification.OK || result.ReleaseID != "rel_01JNEW" {
		t.Fatalf("unexpected bootstrap result: %+v", result)
	}

	loaded, err := (runner.FileStateStore{Path: statePath}).LoadState(ctx)
	if err != nil {
		t.Fatalf("LoadState returned error: %v", err)
	}
	if loaded.LastAcceptedRelease != "rel_01JNEW" || loaded.CurrentState != runner.StatePreparingArtifact {
		t.Fatalf("unexpected local state: %+v", loaded)
	}
	if loaded.Identity == nil || loaded.Identity.InstanceID != "i-abc123" {
		t.Fatalf("identity was not persisted: %+v", loaded.Identity)
	}
	wantStates := []runner.State{
		runner.StateBooting,
		runner.StateFetchingManifest,
		runner.StateVerifyingRelease,
		runner.StatePreparingArtifact,
	}
	if got := eventStates(events.events); !reflect.DeepEqual(got, wantStates) {
		t.Fatalf("event states = %v, want %v", got, wantStates)
	}
}

func TestBootstrapBuildsVerifierFromEnvironmentReleaseTrust(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	signer := testSigner(t, "local-test", "A")
	cfg := parseRunnerConfig(t)

	putEnvironmentRoot(t, ctx, store, cfg, signer)
	putServiceControl(t, ctx, store, cfg.Service, cfg.Env, "rel_01JNEW", "rel_01JOLD")
	putSignedRelease(t, ctx, store, signer, releaseFixture{
		service:   cfg.Service,
		env:       cfg.Env,
		releaseID: "rel_01JNEW",
		createdAt: "2026-05-17T00:00:00Z",
		expiresAt: "2026-06-17T00:00:00Z",
	})

	result, err := runner.Bootstrap(ctx, runner.BootstrapRequest{
		Config: cfg,
		Store:  store,
		MetadataProvider: runner.StaticMetadataProvider{Value: runner.Identity{
			Provider:   "aws",
			Region:     "us-west-2",
			InstanceID: "i-abc123",
			AccountID:  "123456789012",
			Zone:       "us-west-2a",
		}},
		StateStore: runner.FileStateStore{Path: filepath.Join(t.TempDir(), "state.json")},
		TraceID:    "tr_boot",
		Now:        fixedNow,
	})
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if !result.Verification.OK || result.Verification.VerifiedSignature == nil || result.Verification.VerifiedSignature.KeyID != "local-test" {
		t.Fatalf("unexpected verification: %+v", result.Verification)
	}
}

func TestBootstrapReportsWrongReleaseTarget(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	signer := testSigner(t, "local-test", "A")
	verifier := verifierFor(t, signer)
	cfg := parseRunnerConfig(t)

	putServiceControl(t, ctx, store, cfg.Service, cfg.Env, "rel_01JNEW", "rel_01JOLD")
	putSignedRelease(t, ctx, store, signer, releaseFixture{
		service:       "orders-api",
		env:           cfg.Env,
		releaseID:     "rel_01JNEW",
		objectService: cfg.Service,
		createdAt:     "2026-05-17T00:00:00Z",
		expiresAt:     "2026-06-17T00:00:00Z",
	})

	err := bootstrapErr(ctx, t, cfg, store, verifier, nil)
	var bootErr *runner.BootstrapError
	if !errors.As(err, &bootErr) || bootErr.Code != release.CodeReleaseVerifyFailed {
		t.Fatalf("Bootstrap error = %v, want release verify failure", err)
	}
	if bootErr.Verification == nil || !hasFinding(bootErr.Verification.Findings, "SERVICE_MISMATCH") {
		t.Fatalf("verification findings = %+v", bootErr.Verification)
	}
}

func TestBootstrapReportsMissingControlAndRelease(t *testing.T) {
	ctx := context.Background()
	signer := testSigner(t, "local-test", "A")
	verifier := verifierFor(t, signer)
	cfg := parseRunnerConfig(t)

	err := bootstrapErr(ctx, t, cfg, memory.New(), verifier, nil)
	assertBootstrapCode(t, err, runner.CodeControlNotFound)

	store := memory.New()
	putServiceControl(t, ctx, store, cfg.Service, cfg.Env, "rel_missing", "")
	err = bootstrapErr(ctx, t, cfg, store, verifier, nil)
	assertBootstrapCode(t, err, release.CodeReleaseNotFound)
}

func TestBootstrapReportsInvalidSignatureAndExpiredRelease(t *testing.T) {
	ctx := context.Background()
	cfg := parseRunnerConfig(t)
	trustedSigner := testSigner(t, "local-test", "A")
	otherSigner := testSigner(t, "other-test", "B")
	verifier := verifierFor(t, trustedSigner)

	invalidSignatureStore := memory.New()
	putServiceControl(t, ctx, invalidSignatureStore, cfg.Service, cfg.Env, "rel_bad_sig", "")
	putSignedRelease(t, ctx, invalidSignatureStore, otherSigner, releaseFixture{
		service:   cfg.Service,
		env:       cfg.Env,
		releaseID: "rel_bad_sig",
		createdAt: "2026-05-17T00:00:00Z",
		expiresAt: "2026-06-17T00:00:00Z",
	})
	err := bootstrapErr(ctx, t, cfg, invalidSignatureStore, verifier, nil)
	assertVerificationFinding(t, err, "INVALID_SIGNATURE")

	expiredStore := memory.New()
	putServiceControl(t, ctx, expiredStore, cfg.Service, cfg.Env, "rel_expired", "")
	putSignedRelease(t, ctx, expiredStore, trustedSigner, releaseFixture{
		service:   cfg.Service,
		env:       cfg.Env,
		releaseID: "rel_expired",
		createdAt: "2026-05-01T00:00:00Z",
		expiresAt: "2026-05-02T00:00:00Z",
	})
	err = bootstrapErr(ctx, t, cfg, expiredStore, verifier, nil)
	assertVerificationFinding(t, err, "RELEASE_EXPIRED")
}

func TestBootstrapRejectsStaleReleaseUnlessStable(t *testing.T) {
	ctx := context.Background()
	signer := testSigner(t, "local-test", "A")
	verifier := verifierFor(t, signer)
	cfg := parseRunnerConfig(t)

	store := memory.New()
	statePath := filepath.Join(t.TempDir(), "state.json")

	putServiceControl(t, ctx, store, cfg.Service, cfg.Env, "rel_01JOLD", "")
	putSignedRelease(t, ctx, store, signer, releaseFixture{
		service:   cfg.Service,
		env:       cfg.Env,
		releaseID: "rel_01JOLD",
		createdAt: "2026-05-16T00:00:00Z",
		expiresAt: "2026-06-16T00:00:00Z",
	})
	if err := (runner.FileStateStore{Path: statePath}).SaveState(ctx, runner.LocalState{
		Service:                    cfg.Service,
		Env:                        cfg.Env,
		CurrentState:               runner.StatePreparingArtifact,
		LastAcceptedRelease:        "rel_01JNEW",
		LastAcceptedReleaseCreated: "2026-05-17T00:00:00Z",
		ControlKey:                 cfg.ControlKey,
		UpdatedAt:                  "2026-05-17T00:00:00Z",
	}); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
	}

	err := bootstrapErr(ctx, t, cfg, store, verifier, runner.FileStateStore{Path: statePath})
	assertBootstrapCode(t, err, runner.CodeStaleReleaseRejected)

	stableStore := memory.New()
	stableStatePath := filepath.Join(t.TempDir(), "state.json")
	putServiceControl(t, ctx, stableStore, cfg.Service, cfg.Env, "rel_01JOLD", "rel_01JOLD")
	putSignedRelease(t, ctx, stableStore, signer, releaseFixture{
		service:   cfg.Service,
		env:       cfg.Env,
		releaseID: "rel_01JOLD",
		createdAt: "2026-05-16T00:00:00Z",
		expiresAt: "2026-06-16T00:00:00Z",
	})
	if err := (runner.FileStateStore{Path: stableStatePath}).SaveState(ctx, runner.LocalState{
		Service:                    cfg.Service,
		Env:                        cfg.Env,
		CurrentState:               runner.StatePreparingArtifact,
		LastAcceptedRelease:        "rel_01JNEW",
		LastAcceptedReleaseCreated: "2026-05-17T00:00:00Z",
		ControlKey:                 cfg.ControlKey,
		UpdatedAt:                  "2026-05-17T00:00:00Z",
	}); err != nil {
		t.Fatalf("SaveState stable case returned error: %v", err)
	}
	if _, err := runner.Bootstrap(ctx, runner.BootstrapRequest{
		Config:   cfg,
		Store:    stableStore,
		Verifier: verifier,
		MetadataProvider: runner.StaticMetadataProvider{Value: runner.Identity{
			Provider:   "aws",
			Region:     "us-west-2",
			InstanceID: "i-abc123",
		}},
		StateStore: runner.FileStateStore{Path: stableStatePath},
		Now:        fixedNow,
	}); err != nil {
		t.Fatalf("Bootstrap stable rollback returned error: %v", err)
	}
}

func TestDiscoverIdentityRetriesMetadataProvider(t *testing.T) {
	provider := &flakyMetadataProvider{
		failures: 1,
		value:    runner.Identity{Provider: "aws", Region: "us-west-2", InstanceID: "i-abc123"},
	}
	identity, err := runner.DiscoverIdentity(context.Background(), provider, runner.IdentityOptions{
		Attempts: 2,
		Backoff:  time.Nanosecond,
		Sleep:    func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("DiscoverIdentity returned error: %v", err)
	}
	if identity.InstanceID != "i-abc123" || provider.calls != 2 {
		t.Fatalf("identity = %+v, calls = %d", identity, provider.calls)
	}
}

func parseRunnerConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := runner.ParseUserData([]byte(`{
		"skiff": {
			"env": "prod",
			"service": "payments-api",
			"provider": "aws",
			"region": "us-west-2",
			"state_bucket": "memory://runner-test",
			"control_key": "services/payments-api/control.json"
		}
	}`))
	if err != nil {
		t.Fatalf("ParseUserData returned error: %v", err)
	}
	return cfg
}

func bootstrapErr(ctx context.Context, t *testing.T, cfg config.Config, store objstore.ObjectStore, verifier signing.Verifier, stateStore runner.StateStore) error {
	t.Helper()
	if stateStore == nil {
		stateStore = runner.FileStateStore{Path: filepath.Join(t.TempDir(), "state.json")}
	}
	_, err := runner.Bootstrap(ctx, runner.BootstrapRequest{
		Config:   cfg,
		Store:    store,
		Verifier: verifier,
		MetadataProvider: runner.StaticMetadataProvider{Value: runner.Identity{
			Provider:   "aws",
			Region:     "us-west-2",
			InstanceID: "i-abc123",
		}},
		StateStore: stateStore,
		Now:        fixedNow,
	})
	if err == nil {
		t.Fatal("Bootstrap succeeded, want error")
	}
	return err
}

func assertBootstrapCode(t *testing.T, err error, code string) {
	t.Helper()
	var bootErr *runner.BootstrapError
	if !errors.As(err, &bootErr) {
		t.Fatalf("error = %T, want BootstrapError", err)
	}
	if bootErr.Code != code {
		t.Fatalf("BootstrapError code = %q, want %q; err = %v", bootErr.Code, code, err)
	}
}

func assertVerificationFinding(t *testing.T, err error, code string) {
	t.Helper()
	var bootErr *runner.BootstrapError
	if !errors.As(err, &bootErr) {
		t.Fatalf("error = %T, want BootstrapError", err)
	}
	if bootErr.Code != release.CodeReleaseVerifyFailed {
		t.Fatalf("BootstrapError code = %q, want %q", bootErr.Code, release.CodeReleaseVerifyFailed)
	}
	if bootErr.Verification == nil || !hasFinding(bootErr.Verification.Findings, code) {
		t.Fatalf("verification findings = %+v, want %s", bootErr.Verification, code)
	}
}

func putServiceControl(t *testing.T, ctx context.Context, store objstore.ObjectStore, service, env, desiredRelease, stableRelease string) {
	t.Helper()
	control := schema.NewServiceControl(service, env, "2026-05-16T17:00:00Z", schema.Actor{ID: "alpha-one", Type: "agent"})
	control.DesiredRelease = desiredRelease
	control.StableRelease = stableRelease
	key, err := paths.ServiceControl(service)
	if err != nil {
		t.Fatalf("ServiceControl path returned error: %v", err)
	}
	putJSON(t, ctx, store, key, control)
}

func putEnvironmentRoot(t *testing.T, ctx context.Context, store objstore.ObjectStore, cfg config.Config, signer *signing.LocalSigner) {
	t.Helper()
	key, err := paths.EnvironmentRoot(cfg.Env)
	if err != nil {
		t.Fatalf("EnvironmentRoot path returned error: %v", err)
	}
	root := schema.EnvironmentRoot{
		SchemaVersion: schema.EnvironmentRootSchemaVersion,
		Env:           cfg.Env,
		Provider:      cfg.Provider,
		Region:        cfg.Region,
		StateBucket:   cfg.StateBucket,
		Roles:         map[string]string{},
		ReleaseTrust: &schema.ReleaseTrust{
			ActiveKeyIDs: []string{signer.KeyID()},
			Keys: []schema.ReleaseTrustKey{{
				KeyID:     signer.KeyID(),
				Backend:   signing.KeychainScheme,
				PublicKey: base64.StdEncoding.EncodeToString(signer.PublicKey()),
				Status:    "active",
				CreatedAt: "2026-05-16T17:00:00Z",
			}},
		},
		CreatedAt: "2026-05-16T17:00:00Z",
		UpdatedAt: "2026-05-16T17:00:00Z",
	}
	putJSON(t, ctx, store, key, root)
}

type releaseFixture struct {
	service       string
	env           string
	releaseID     string
	objectService string
	createdAt     string
	expiresAt     string
}

func putSignedRelease(t *testing.T, ctx context.Context, store objstore.ObjectStore, signer signing.Signer, fixture releaseFixture) {
	t.Helper()
	objectService := fixture.objectService
	if objectService == "" {
		objectService = fixture.service
	}
	runtimeKey, err := paths.RuntimeManifest(objectService, fixture.releaseID)
	if err != nil {
		t.Fatalf("RuntimeManifest path returned error: %v", err)
	}
	runtimeManifest := schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       fixture.service,
		Env:           fixture.env,
		ReleaseID:     fixture.releaseID,
		Command:       []string{"./app", "serve"},
		EnvVars:       map[string]string{"PORT": "8080"},
		HealthCheck:   &schema.HealthCheck{Type: "http", Path: "/healthz", Port: 8080, Interval: "10s", Timeout: "2s"},
		CreatedAt:     fixture.createdAt,
	}
	runtimeDigest, err := release.RuntimeManifestDigest(runtimeManifest)
	if err != nil {
		t.Fatalf("RuntimeManifestDigest returned error: %v", err)
	}
	manifest := schema.ReleaseManifest{
		SchemaVersion: schema.Version,
		Service:       fixture.service,
		Env:           fixture.env,
		ReleaseID:     fixture.releaseID,
		Artifact: schema.ArtifactRef{
			Type:   "oci",
			URI:    "oci://ghcr.io/acme/" + fixture.service + "@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		RuntimeManifestKey:    runtimeKey,
		RuntimeManifestDigest: runtimeDigest,
		CreatedAt:             fixture.createdAt,
		ExpiresAt:             fixture.expiresAt,
	}
	signed, err := release.SignManifest(ctx, manifest, signer, schema.Actor{ID: "alpha-one", Type: "agent"}, fixedTime())
	if err != nil {
		t.Fatalf("SignManifest returned error: %v", err)
	}
	releaseKey, err := paths.ReleaseManifest(objectService, fixture.releaseID)
	if err != nil {
		t.Fatalf("ReleaseManifest path returned error: %v", err)
	}
	putJSON(t, ctx, store, runtimeKey, runtimeManifest)
	putJSON(t, ctx, store, releaseKey, signed)
}

func putJSON(t *testing.T, ctx context.Context, store objstore.ObjectStore, key string, document any) {
	t.Helper()
	body, err := canonical.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if _, err := store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatalf("Create %s returned error: %v", key, err)
	}
}

func testSigner(t *testing.T, keyID string, fill string) *signing.LocalSigner {
	t.Helper()
	signer, err := signing.NewLocalSignerFromSeed(keyID, []byte(strings.Repeat(fill, ed25519.SeedSize)))
	if err != nil {
		t.Fatalf("NewLocalSignerFromSeed returned error: %v", err)
	}
	return signer
}

func verifierFor(t *testing.T, signer *signing.LocalSigner) *signing.LocalVerifier {
	t.Helper()
	verifier, err := signing.NewLocalVerifier(map[string]ed25519.PublicKey{signer.KeyID(): signer.PublicKey()})
	if err != nil {
		t.Fatalf("NewLocalVerifier returned error: %v", err)
	}
	return verifier
}

func fixedNow() time.Time {
	return fixedTime()
}

func fixedTime() time.Time {
	return time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
}

func eventStates(events []runner.StateEvent) []runner.State {
	out := make([]runner.State, 0, len(events))
	for _, event := range events {
		out = append(out, event.State)
	}
	return out
}

func hasFinding(findings []release.Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

type collectingSink struct {
	events []runner.StateEvent
}

func (s *collectingSink) EmitRunnerEvent(ctx context.Context, event runner.StateEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.events = append(s.events, event)
	return nil
}

type flakyMetadataProvider struct {
	failures int
	calls    int
	value    runner.Identity
}

func (p *flakyMetadataProvider) Identity(ctx context.Context) (runner.Identity, error) {
	p.calls++
	if p.calls <= p.failures {
		return runner.Identity{}, errors.New("metadata unavailable")
	}
	return p.value, nil
}
