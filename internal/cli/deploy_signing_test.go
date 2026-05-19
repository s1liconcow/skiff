package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/config"
	awsprovider "github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/security/signing"
)

func TestDeployFakeProviderGeneratesEphemeralSigner(t *testing.T) {
	clearSkiffEnv(t)
	root := t.TempDir()
	specPath := filepath.Join("..", "..", "examples", "service", "http-hello", "skiff.yaml")

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"deploy", specPath,
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "fake",
		"--region", "local",
		"--key-id", "demo-ephemeral",
		"--format", "json",
		"--trace-id", "tr_ephemeral_signer",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got deployOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("deploy output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || !got.Result.OK || got.Result.ReleaseManifest == nil {
		t.Fatalf("unexpected deploy output: %+v", got)
	}
	if !strings.HasPrefix(got.Result.ReleaseID, "rel_") || !strings.HasPrefix(got.Result.OperationID, "op_") {
		t.Fatalf("expected generated release/operation IDs, got release=%q operation=%q", got.Result.ReleaseID, got.Result.OperationID)
	}
	signatures := got.Result.ReleaseManifest.Signatures
	if len(signatures) != 1 || signatures[0].KeyID != "demo-ephemeral" || signatures[0].Algorithm != "ed25519" {
		t.Fatalf("unexpected signatures: %+v", signatures)
	}
}

func TestDeployNonFakeProviderRequiresSigningSeed(t *testing.T) {
	clearSkiffEnv(t)
	root := t.TempDir()
	specPath := filepath.Join("..", "..", "examples", "service", "http-hello", "skiff.yaml")

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"deploy", specPath,
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_missing_seed",
	}, &stdout, &stderr)
	if code != ExitUserError {
		t.Fatalf("exit code = %d, want %d; stderr = %s, stdout = %s", code, ExitUserError, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty JSON-mode stderr", stderr.String())
	}
	var got specErrorOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Summary == "" {
		t.Fatalf("unexpected error output: %+v", got)
	}
}

func TestSignerForDeployUsesConfiguredKeyRef(t *testing.T) {
	fake := withFakeReleaseSignerStore(t)
	record, _, err := fake.Ensure(context.Background(), "quickstart")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signerForDeploy(config.Config{Provider: "aws", Env: "quickstart", ReleaseSigningKeyRef: record.KeyRef}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if signer.KeyID() != record.KeyID || signer.Algorithm() != "ed25519" {
		t.Fatalf("unexpected signer: key=%q algorithm=%q", signer.KeyID(), signer.Algorithm())
	}
}

func TestSignerForDeployUsesAWSKMSKeyRef(t *testing.T) {
	fake := withFakeAWSReleaseSignerStore(t, "us-west-2")
	record, _, err := fake.Ensure(context.Background(), "quickstart")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signerForDeploy(config.Config{Provider: "aws", Env: "quickstart", Region: "us-west-2", ReleaseSigningKeyRef: record.KeyRef}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if signer.KeyID() != record.KeyID || signer.Algorithm() != signing.AlgorithmECDSAP256SHA256 {
		t.Fatalf("unexpected signer: key=%q algorithm=%q", signer.KeyID(), signer.Algorithm())
	}
}

func withFakeReleaseSignerStore(t *testing.T) *fakeReleaseSignerStore {
	t.Helper()
	fake := newFakeReleaseSignerStore(signing.KeychainScheme, "")
	previous := releaseSignerStore
	releaseSignerStore = fake
	t.Cleanup(func() { releaseSignerStore = previous })
	return fake
}

func withFakeAWSReleaseSignerStore(t *testing.T, region string) *fakeReleaseSignerStore {
	t.Helper()
	fake := newFakeReleaseSignerStore(awsprovider.KMSReleaseSigningScheme, region)
	previous := newAWSReleaseSignerStore
	newAWSReleaseSignerStore = func(ctx context.Context, requestedRegion string) (signing.ReleaseSignerStore, error) {
		if requestedRegion != "" {
			fake.region = requestedRegion
		}
		return fake, nil
	}
	t.Cleanup(func() { newAWSReleaseSignerStore = previous })
	return fake
}

func newFakeReleaseSignerStore(backend, region string) *fakeReleaseSignerStore {
	return &fakeReleaseSignerStore{
		backend: backend,
		region:  region,
		records: map[string]*fakeReleaseSignerRecord{},
	}
}

type fakeReleaseSignerStore struct {
	backend string
	region  string
	records map[string]*fakeReleaseSignerRecord
}

type fakeReleaseSignerRecord struct {
	record *signing.ReleaseSignerRecord
	signer signing.Signer
}

func (f *fakeReleaseSignerStore) Ensure(ctx context.Context, env string) (*signing.ReleaseSignerRecord, signing.Signer, error) {
	keyRef := f.keyRef(env)
	if existing := f.records[keyRef]; existing != nil {
		return existing.record, existing.signer, nil
	}
	seed := []byte(strings.Repeat("Q", ed25519.SeedSize))
	keyID := "skiff-" + env + "-release-test"
	signer, err := signing.NewLocalSignerFromSeed(keyID, seed)
	if err != nil {
		return nil, nil, err
	}
	record := &signing.ReleaseSignerRecord{
		KeyID:     keyID,
		KeyRef:    keyRef,
		Backend:   f.backend,
		Algorithm: signer.Algorithm(),
		Encoding:  signing.PublicKeyEncodingRaw,
		PublicKey: base64.StdEncoding.EncodeToString(signer.PublicKey()),
	}
	if f.backend == awsprovider.KMSReleaseSigningScheme {
		record.KeyID = "skiff-" + env + "-release-123456789abc"
		record.Algorithm = signing.AlgorithmECDSAP256SHA256
		record.Encoding = signing.PublicKeyEncodingPKIXDER
		record.PublicKey = base64.StdEncoding.EncodeToString([]byte("fake-pkix-der"))
		f.records[keyRef] = &fakeReleaseSignerRecord{record: record, signer: fakeSigner{keyID: record.KeyID, algorithm: record.Algorithm}}
		return record, f.records[keyRef].signer, nil
	}
	f.records[keyRef] = &fakeReleaseSignerRecord{record: record, signer: signer}
	return record, signer, nil
}

func (f *fakeReleaseSignerStore) Resolve(ctx context.Context, keyRef string) (*signing.ReleaseSignerRecord, signing.Signer, error) {
	if existing := f.records[keyRef]; existing != nil {
		return existing.record, existing.signer, nil
	}
	return nil, nil, signing.ErrNoValidSignature
}

func (f *fakeReleaseSignerStore) Signer(ctx context.Context, keyRef string) (signing.Signer, error) {
	_, signer, err := f.Resolve(ctx, keyRef)
	return signer, err
}

func (f *fakeReleaseSignerStore) keyRef(env string) string {
	if f.backend == awsprovider.KMSReleaseSigningScheme {
		return awsprovider.DefaultKMSReleaseSigningRef(env, firstNonEmptyString(f.region, "us-west-2"))
	}
	return signing.KeychainRef("dev.skiff.test-release-signing", env+"/release")
}

type fakeSigner struct {
	keyID     string
	algorithm string
}

func (s fakeSigner) KeyID() string {
	return s.keyID
}

func (s fakeSigner) Algorithm() string {
	return s.algorithm
}

func (s fakeSigner) SignDigest(ctx context.Context, digest string) ([]byte, error) {
	return []byte("fake-signature:" + digest), nil
}
