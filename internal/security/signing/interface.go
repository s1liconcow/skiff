package signing

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

const AlgorithmEd25519 = "ed25519"

type Signer interface {
	KeyID() string
	Algorithm() string
	SignDigest(ctx context.Context, digest string) ([]byte, error)
}

type Verifier interface {
	VerifyDigest(ctx context.Context, digest string, signature schema.Signature) error
}

var ErrNoValidSignature = errors.New("no valid signature")

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
