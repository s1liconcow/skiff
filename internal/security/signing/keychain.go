package signing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

const (
	KeychainScheme         = "keychain"
	DefaultKeychainService = "dev.skiff.release-signing"
)

type ReleaseSignerRecord struct {
	KeyID     string `json:"key_id"`
	KeyRef    string `json:"key_ref"`
	Backend   string `json:"backend"`
	Algorithm string `json:"algorithm"`
	Encoding  string `json:"encoding"`
	PublicKey string `json:"public_key"`
}

type ReleaseSignerStore interface {
	Ensure(ctx context.Context, env string) (*ReleaseSignerRecord, Signer, error)
	Resolve(ctx context.Context, keyRef string) (*ReleaseSignerRecord, Signer, error)
	Signer(ctx context.Context, keyRef string) (Signer, error)
}

type KeychainReleaseSignerStore struct {
	Service string
}

func DefaultReleaseSignerStore() ReleaseSignerStore {
	return KeychainReleaseSignerStore{Service: DefaultKeychainService}
}

func (s KeychainReleaseSignerStore) Ensure(ctx context.Context, env string) (*ReleaseSignerRecord, Signer, error) {
	env = strings.TrimSpace(env)
	if env == "" {
		return nil, nil, errors.New("env is required")
	}
	service := firstNonEmpty(s.Service, DefaultKeychainService)
	account := env + "/release"
	keyRef := KeychainRef(service, account)
	seed, err := s.readSeed(ctx, service, account)
	if err != nil {
		if !isKeychainNotFound(err) {
			return nil, nil, err
		}
		seed = make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return nil, nil, err
		}
		if err := s.writeSeed(ctx, service, account, seed); err != nil {
			return nil, nil, err
		}
	}
	keyID := ReleaseSigningKeyID(env, ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
	signer, err := NewLocalSignerFromSeed(keyID, seed)
	if err != nil {
		return nil, nil, err
	}
	return ReleaseSignerRecordFor(keyRef, signer), signer, nil
}

func (s KeychainReleaseSignerStore) Signer(ctx context.Context, keyRef string) (Signer, error) {
	_, signer, err := s.Resolve(ctx, keyRef)
	return signer, err
}

func (s KeychainReleaseSignerStore) Resolve(ctx context.Context, keyRef string) (*ReleaseSignerRecord, Signer, error) {
	service, account, err := ParseKeychainRef(keyRef)
	if err != nil {
		return nil, nil, err
	}
	seed, err := s.readSeed(ctx, firstNonEmpty(service, s.Service, DefaultKeychainService), account)
	if err != nil {
		return nil, nil, err
	}
	env := strings.TrimSuffix(account, "/release")
	keyID := ReleaseSigningKeyID(env, ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
	signer, err := NewLocalSignerFromSeed(keyID, seed)
	if err != nil {
		return nil, nil, err
	}
	return ReleaseSignerRecordFor(keyRef, signer), signer, nil
}

func ReleaseSignerRecordFor(keyRef string, signer *LocalSigner) *ReleaseSignerRecord {
	if signer == nil {
		return nil
	}
	return &ReleaseSignerRecord{
		KeyID:     signer.KeyID(),
		KeyRef:    keyRef,
		Backend:   KeychainScheme,
		Algorithm: AlgorithmEd25519,
		Encoding:  PublicKeyEncodingRaw,
		PublicKey: base64.StdEncoding.EncodeToString(signer.PublicKey()),
	}
}

func ReleaseSigningKeyID(env string, publicKey ed25519.PublicKey) string {
	return ReleaseSigningKeyIDFromBytes(env, publicKey)
}

func ReleaseSigningKeyIDFromBytes(env string, publicKey []byte) string {
	env = strings.Trim(strings.ToLower(strings.TrimSpace(env)), "-")
	sum := sha256.Sum256(publicKey)
	fingerprint := hex.EncodeToString(sum[:])[:12]
	if env == "" {
		return "skiff-release-" + fingerprint
	}
	return "skiff-" + env + "-release-" + fingerprint
}

func KeychainRef(service, account string) string {
	service = firstNonEmpty(strings.TrimSpace(service), DefaultKeychainService)
	account = strings.Trim(account, "/")
	return (&url.URL{Scheme: KeychainScheme, Host: service, Path: "/" + account}).String()
}

func ParseKeychainRef(ref string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", "", fmt.Errorf("parse signing key ref: %w", err)
	}
	if parsed.Scheme != KeychainScheme {
		return "", "", fmt.Errorf("unsupported signing key ref %q; expected keychain://...", ref)
	}
	service := strings.TrimSpace(parsed.Host)
	account := strings.Trim(parsed.Path, "/")
	if service == "" {
		service = DefaultKeychainService
	}
	if account == "" {
		return "", "", fmt.Errorf("keychain signing key ref must include an account path")
	}
	return service, account, nil
}

func (s KeychainReleaseSignerStore) readSeed(ctx context.Context, service, account string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", service, "-a", account, "-w").Output()
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, fmt.Errorf("decode keychain signing seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("keychain signing seed must be %d bytes", ed25519.SeedSize)
	}
	return seed, nil
}

func (s KeychainReleaseSignerStore) writeSeed(ctx context.Context, service, account string, seed []byte) error {
	if len(seed) != ed25519.SeedSize {
		return fmt.Errorf("ed25519 seed must be %d bytes", ed25519.SeedSize)
	}
	value := base64.StdEncoding.EncodeToString(seed)
	return exec.CommandContext(ctx, "security", "add-generic-password", "-s", service, "-a", account, "-w", value, "-U").Run()
}

func isKeychainNotFound(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	stderr := strings.ToLower(string(exitErr.Stderr))
	return strings.Contains(stderr, "could not be found") || strings.Contains(stderr, "the specified item could not be found")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
