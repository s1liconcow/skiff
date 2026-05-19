package signing_test

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/security/signing"
)

func TestKeychainRefRoundTrip(t *testing.T) {
	ref := signing.KeychainRef("dev.skiff.release-signing", "quickstart/release")
	service, account, err := signing.ParseKeychainRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	if service != "dev.skiff.release-signing" || account != "quickstart/release" {
		t.Fatalf("parsed ref = %q/%q", service, account)
	}
}

func TestReleaseSigningKeyID(t *testing.T) {
	signer, err := signing.NewLocalSignerFromSeed("seed", []byte(strings.Repeat("K", ed25519.SeedSize)))
	if err != nil {
		t.Fatal(err)
	}
	keyID := signing.ReleaseSigningKeyID("QuickStart", signer.PublicKey())
	if !strings.HasPrefix(keyID, "skiff-quickstart-release-") {
		t.Fatalf("key id = %q", keyID)
	}
}
