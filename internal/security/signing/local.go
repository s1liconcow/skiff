package signing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

type LocalSigner struct {
	keyID      string
	privateKey ed25519.PrivateKey
}

func GenerateLocalSigner(keyID string, random io.Reader) (*LocalSigner, error) {
	if random == nil {
		random = rand.Reader
	}
	_, privateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return nil, err
	}
	return NewLocalSigner(keyID, privateKey)
}

func NewLocalSignerFromSeed(keyID string, seed []byte) (*LocalSigner, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("ed25519 seed must be %d bytes", ed25519.SeedSize)
	}
	return NewLocalSigner(keyID, ed25519.NewKeyFromSeed(seed))
}

func NewLocalSigner(keyID string, privateKey ed25519.PrivateKey) (*LocalSigner, error) {
	if keyID == "" {
		return nil, fmt.Errorf("key ID is required")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("ed25519 private key must be %d bytes", ed25519.PrivateKeySize)
	}
	return &LocalSigner{keyID: keyID, privateKey: append(ed25519.PrivateKey(nil), privateKey...)}, nil
}

func (s *LocalSigner) KeyID() string {
	return s.keyID
}

func (s *LocalSigner) Algorithm() string {
	return AlgorithmEd25519
}

func (s *LocalSigner) PublicKey() ed25519.PublicKey {
	publicKey := s.privateKey.Public().(ed25519.PublicKey)
	return append(ed25519.PublicKey(nil), publicKey...)
}

func (s *LocalSigner) SignDigest(ctx context.Context, digest string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return ed25519.Sign(s.privateKey, []byte(digest)), nil
}

type LocalVerifier struct {
	publicKeys map[string]ed25519.PublicKey
}

func NewLocalVerifier(publicKeys map[string]ed25519.PublicKey) (*LocalVerifier, error) {
	if len(publicKeys) == 0 {
		return nil, fmt.Errorf("at least one public key is required")
	}
	copied := make(map[string]ed25519.PublicKey, len(publicKeys))
	for keyID, publicKey := range publicKeys {
		if keyID == "" {
			return nil, fmt.Errorf("key ID is required")
		}
		if len(publicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("ed25519 public key for %q must be %d bytes", keyID, ed25519.PublicKeySize)
		}
		copied[keyID] = append(ed25519.PublicKey(nil), publicKey...)
	}
	return &LocalVerifier{publicKeys: copied}, nil
}

func (v *LocalVerifier) VerifyDigest(ctx context.Context, digest string, signature schema.Signature) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if signature.Algorithm != AlgorithmEd25519 {
		return fmt.Errorf("unsupported signature algorithm %q", signature.Algorithm)
	}
	publicKey, ok := v.publicKeys[signature.KeyID]
	if !ok {
		return fmt.Errorf("unknown key ID %q", signature.KeyID)
	}
	raw, err := DecodeSignature(signature.Signature)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, []byte(digest), raw) {
		return fmt.Errorf("invalid signature for key ID %q", signature.KeyID)
	}
	return nil
}
