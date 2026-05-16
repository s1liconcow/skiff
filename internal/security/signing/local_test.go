package signing_test

import (
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestLocalSignerVerifierRoundTrip(t *testing.T) {
	signer, err := signing.NewLocalSignerFromSeed("local-test", []byte(strings.Repeat("A", ed25519.SeedSize)))
	if err != nil {
		t.Fatalf("NewLocalSignerFromSeed returned error: %v", err)
	}
	verifier, err := signing.NewLocalVerifier(map[string]ed25519.PublicKey{"local-test": signer.PublicKey()})
	if err != nil {
		t.Fatalf("NewLocalVerifier returned error: %v", err)
	}

	digest := "sha256:0123456789abcdef"
	signature, err := signing.SignDigest(
		context.Background(),
		signer,
		digest,
		schema.Actor{ID: "alpha-one", Type: "agent"},
		time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("SignDigest returned error: %v", err)
	}
	if signature.Signer == nil || signature.Signer.ID != "alpha-one" {
		t.Fatalf("signature signer = %+v, want alpha-one", signature.Signer)
	}
	if err := verifier.VerifyDigest(context.Background(), digest, signature); err != nil {
		t.Fatalf("VerifyDigest returned error: %v", err)
	}
	if err := verifier.VerifyDigest(context.Background(), digest+"tampered", signature); err == nil {
		t.Fatalf("VerifyDigest accepted a tampered digest")
	}
}

func TestGenerateLocalSignerUsesValidEd25519Keys(t *testing.T) {
	signer, err := signing.GenerateLocalSigner("generated", nil)
	if err != nil {
		t.Fatalf("GenerateLocalSigner returned error: %v", err)
	}
	if len(signer.PublicKey()) != ed25519.PublicKeySize {
		t.Fatalf("public key length = %d, want %d", len(signer.PublicKey()), ed25519.PublicKeySize)
	}
}
