package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	AlgorithmEd25519         = "ed25519"
	AlgorithmECDSAP256SHA256 = "ecdsa-p256-sha256"
	PublicKeyEncodingRaw     = "raw"
	PublicKeyEncodingPKIXDER = "pkix-der"
)

type Signer interface {
	KeyID() string
	Algorithm() string
	SignDigest(ctx context.Context, digest string) ([]byte, error)
}

type Verifier interface {
	VerifyDigest(ctx context.Context, digest string, signature schema.Signature) error
}

var ErrNoValidSignature = errors.New("no valid signature")

type TrustedPublicKey struct {
	KeyID     string
	Algorithm string
	Encoding  string
	PublicKey []byte
}

type PublicKeyVerifier struct {
	keys map[string]TrustedPublicKey
}

func NewPublicKeyVerifier(keys []TrustedPublicKey) (*PublicKeyVerifier, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one public key is required")
	}
	copied := make(map[string]TrustedPublicKey, len(keys))
	for _, key := range keys {
		if key.KeyID == "" {
			return nil, fmt.Errorf("key ID is required")
		}
		if key.Algorithm == "" {
			return nil, fmt.Errorf("algorithm is required for key %q", key.KeyID)
		}
		if key.Encoding == "" {
			key.Encoding = defaultPublicKeyEncoding(key.Algorithm)
		}
		if len(key.PublicKey) == 0 {
			return nil, fmt.Errorf("public key is required for key %q", key.KeyID)
		}
		key.PublicKey = append([]byte(nil), key.PublicKey...)
		copied[key.KeyID] = key
	}
	return &PublicKeyVerifier{keys: copied}, nil
}

func (v *PublicKeyVerifier) VerifyDigest(ctx context.Context, digest string, signature schema.Signature) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	key, ok := v.keys[signature.KeyID]
	if !ok {
		return fmt.Errorf("unknown key ID %q", signature.KeyID)
	}
	if signature.Algorithm != key.Algorithm {
		return fmt.Errorf("signature algorithm %q does not match trusted key algorithm %q", signature.Algorithm, key.Algorithm)
	}
	rawSig, err := DecodeSignature(signature.Signature)
	if err != nil {
		return err
	}
	switch signature.Algorithm {
	case AlgorithmEd25519:
		if key.Encoding != PublicKeyEncodingRaw {
			return fmt.Errorf("ed25519 public key for %q uses encoding %q, want %q", signature.KeyID, key.Encoding, PublicKeyEncodingRaw)
		}
		if len(key.PublicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("ed25519 public key for %q must be %d bytes", signature.KeyID, ed25519.PublicKeySize)
		}
		if !ed25519.Verify(ed25519.PublicKey(key.PublicKey), []byte(digest), rawSig) {
			return fmt.Errorf("invalid signature for key ID %q", signature.KeyID)
		}
		return nil
	case AlgorithmECDSAP256SHA256:
		if key.Encoding != PublicKeyEncodingPKIXDER {
			return fmt.Errorf("ECDSA public key for %q uses encoding %q, want %q", signature.KeyID, key.Encoding, PublicKeyEncodingPKIXDER)
		}
		parsed, err := x509.ParsePKIXPublicKey(key.PublicKey)
		if err != nil {
			return fmt.Errorf("parse ECDSA public key for %q: %w", signature.KeyID, err)
		}
		publicKey, ok := parsed.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("public key for %q is %T, want ECDSA", signature.KeyID, parsed)
		}
		sum := sha256.Sum256([]byte(digest))
		if !ecdsa.VerifyASN1(publicKey, sum[:], rawSig) {
			return fmt.Errorf("invalid signature for key ID %q", signature.KeyID)
		}
		return nil
	default:
		return fmt.Errorf("unsupported signature algorithm %q", signature.Algorithm)
	}
}

func SignDigest(ctx context.Context, signer Signer, digest string, actor schema.Actor, signedAt time.Time) (schema.Signature, error) {
	if signer == nil {
		return schema.Signature{}, fmt.Errorf("signer is required")
	}
	raw, err := signer.SignDigest(ctx, digest)
	if err != nil {
		return schema.Signature{}, err
	}
	return schema.Signature{
		KeyID:     signer.KeyID(),
		Algorithm: signer.Algorithm(),
		Signature: base64.StdEncoding.EncodeToString(raw),
		SignedAt:  signedAt.UTC().Round(0).Format(time.RFC3339Nano),
		Signer:    &actor,
	}, nil
}

func DecodeSignature(value string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("signature must be base64: %w", err)
	}
	return raw, nil
}

func VerifyAny(ctx context.Context, verifier Verifier, digest string, signatures []schema.Signature) (schema.Signature, error) {
	if verifier == nil {
		return schema.Signature{}, fmt.Errorf("verifier is required")
	}
	if len(signatures) == 0 {
		return schema.Signature{}, ErrNoValidSignature
	}
	var lastErr error
	for _, signature := range signatures {
		if err := verifier.VerifyDigest(ctx, digest, signature); err != nil {
			lastErr = err
			continue
		}
		return signature, nil
	}
	if lastErr != nil {
		return schema.Signature{}, fmt.Errorf("%w: %v", ErrNoValidSignature, lastErr)
	}
	return schema.Signature{}, ErrNoValidSignature
}

func defaultPublicKeyEncoding(algorithm string) string {
	switch algorithm {
	case AlgorithmEd25519:
		return PublicKeyEncodingRaw
	case AlgorithmECDSAP256SHA256:
		return PublicKeyEncodingPKIXDER
	default:
		return ""
	}
}
