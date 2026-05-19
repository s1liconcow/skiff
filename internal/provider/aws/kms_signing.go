package aws

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	sdka "github.com/aws/aws-sdk-go-v2/aws"
	sdkconfig "github.com/aws/aws-sdk-go-v2/config"
	sdkcredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/s1liconcow/skiff/internal/security/signing"
)

const (
	KMSReleaseSigningScheme = "aws-kms"
)

type KMSReleaseSignerStore struct {
	region string
	kms    *kms.Client
}

type KMSSigner struct {
	keyID    string
	keyRef   string
	kmsKeyID string
	kms      *kms.Client
}

func NewKMSReleaseSignerStore(ctx context.Context, cfg Config) (signing.ReleaseSignerStore, error) {
	loadOpts := []func(*sdkconfig.LoadOptions) error{sdkconfig.WithRegion(cfg.Region)}
	if !cfg.Credentials.Empty() {
		loadOpts = append(loadOpts, sdkconfig.WithCredentialsProvider(sdkcredentials.NewStaticCredentialsProvider(
			cfg.Credentials.AccessKeyID,
			cfg.Credentials.SecretAccessKey,
			cfg.Credentials.SessionToken,
		)))
	}
	loaded, err := sdkconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}
	return &KMSReleaseSignerStore{
		region: cfg.Region,
		kms:    kms.NewFromConfig(loaded),
	}, nil
}

func (s *KMSReleaseSignerStore) Ensure(ctx context.Context, env string) (*signing.ReleaseSignerRecord, signing.Signer, error) {
	env = strings.Trim(strings.ToLower(strings.TrimSpace(env)), "-")
	if env == "" {
		return nil, nil, fmt.Errorf("env is required")
	}
	alias := KMSReleaseSigningAlias(env)
	if _, err := s.kms.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: sdka.String(alias)}); err != nil {
		if !sdkNotFound(err) {
			return nil, nil, err
		}
		out, err := s.kms.CreateKey(ctx, &kms.CreateKeyInput{
			Description: sdka.String("Skiff release signing key for " + env),
			KeySpec:     kmstypes.KeySpecEccNistP256,
			KeyUsage:    kmstypes.KeyUsageTypeSignVerify,
			Tags: []kmstypes.Tag{
				{TagKey: sdka.String("skiff.dev/env"), TagValue: sdka.String(env)},
				{TagKey: sdka.String("skiff.dev/managed"), TagValue: sdka.String("true")},
				{TagKey: sdka.String("skiff.dev/graph"), TagValue: sdka.String("environment/" + env)},
			},
		})
		if err != nil {
			return nil, nil, err
		}
		if _, err := s.kms.CreateAlias(ctx, &kms.CreateAliasInput{AliasName: sdka.String(alias), TargetKeyId: out.KeyMetadata.KeyId}); err != nil && !sdkAlreadyExists(err) {
			return nil, nil, err
		}
	}
	return s.resolveKMSKey(ctx, alias)
}

func (s *KMSReleaseSignerStore) Resolve(ctx context.Context, keyRef string) (*signing.ReleaseSignerRecord, signing.Signer, error) {
	keyID, err := ParseKMSReleaseSigningRef(keyRef)
	if err != nil {
		return nil, nil, err
	}
	return s.resolveKMSKey(ctx, keyID)
}

func (s *KMSReleaseSignerStore) Signer(ctx context.Context, keyRef string) (signing.Signer, error) {
	_, signer, err := s.Resolve(ctx, keyRef)
	return signer, err
}

func (s *KMSReleaseSignerStore) resolveKMSKey(ctx context.Context, kmsKeyID string) (*signing.ReleaseSignerRecord, signing.Signer, error) {
	described, err := s.kms.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: sdka.String(kmsKeyID)})
	if err != nil {
		return nil, nil, err
	}
	pub, err := s.kms.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: sdka.String(kmsKeyID)})
	if err != nil {
		return nil, nil, err
	}
	keyID := kmsReleaseSigningKeyID(envFromKMSAlias(kmsKeyID), sdka.ToString(described.KeyMetadata.KeyId))
	keyRef := KMSReleaseSigningRef(kmsKeyID, s.region)
	signer := &KMSSigner{
		keyID:    keyID,
		keyRef:   keyRef,
		kmsKeyID: kmsKeyID,
		kms:      s.kms,
	}
	return &signing.ReleaseSignerRecord{
		KeyID:     keyID,
		KeyRef:    keyRef,
		Backend:   KMSReleaseSigningScheme,
		Algorithm: signing.AlgorithmECDSAP256SHA256,
		Encoding:  signing.PublicKeyEncodingPKIXDER,
		PublicKey: base64.StdEncoding.EncodeToString(pub.PublicKey),
	}, signer, nil
}

func (s *KMSSigner) KeyID() string {
	return s.keyID
}

func (s *KMSSigner) Algorithm() string {
	return signing.AlgorithmECDSAP256SHA256
}

func (s *KMSSigner) SignDigest(ctx context.Context, digest string) ([]byte, error) {
	out, err := s.kms.Sign(ctx, &kms.SignInput{
		KeyId:            sdka.String(s.kmsKeyID),
		Message:          []byte(digest),
		MessageType:      kmstypes.MessageTypeRaw,
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecEcdsaSha256,
	})
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), out.Signature...), nil
}

func KMSReleaseSigningAlias(env string) string {
	env = strings.Trim(strings.ToLower(strings.TrimSpace(env)), "-")
	return "alias/skiff-" + env + "-release-signing"
}

func DefaultKMSReleaseSigningRef(env, region string) string {
	return KMSReleaseSigningRef(KMSReleaseSigningAlias(env), region)
}

func KMSReleaseSigningRef(kmsKeyID, region string) string {
	host, path := "key", "/"+strings.Trim(kmsKeyID, "/")
	if strings.HasPrefix(kmsKeyID, "alias/") {
		host = "alias"
		path = "/" + strings.TrimPrefix(kmsKeyID, "alias/")
	}
	ref := &url.URL{Scheme: KMSReleaseSigningScheme, Host: host, Path: path}
	if strings.TrimSpace(region) != "" {
		query := ref.Query()
		query.Set("region", strings.TrimSpace(region))
		ref.RawQuery = query.Encode()
	}
	return ref.String()
}

func ParseKMSReleaseSigningRef(ref string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", fmt.Errorf("parse KMS signing key ref: %w", err)
	}
	if parsed.Scheme != KMSReleaseSigningScheme {
		return "", fmt.Errorf("unsupported signing key ref %q; expected aws-kms://...", ref)
	}
	host := strings.TrimSpace(parsed.Host)
	path := strings.Trim(parsed.Path, "/")
	switch host {
	case "alias":
		if path == "" {
			return "", fmt.Errorf("KMS signing alias ref must include an alias name")
		}
		return "alias/" + path, nil
	case "key", "arn":
		if path == "" {
			return "", fmt.Errorf("KMS signing key ref must include a key ID or ARN")
		}
		return path, nil
	}
	if path != "" {
		return strings.Trim(host+"/"+path, "/"), nil
	}
	if host != "" {
		return host, nil
	}
	return "", fmt.Errorf("KMS signing key ref must include a key ID, ARN, or alias")
}

func KMSReleaseSigningRegion(ref string) string {
	parsed, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || parsed.Scheme != KMSReleaseSigningScheme {
		return ""
	}
	return strings.TrimSpace(parsed.Query().Get("region"))
}

func envFromKMSAlias(kmsKeyID string) string {
	value := strings.TrimPrefix(strings.TrimSpace(kmsKeyID), "alias/skiff-")
	return strings.TrimSuffix(value, "-release-signing")
}

func kmsReleaseSigningKeyID(env, providerKeyID string) string {
	providerKeyID = strings.TrimSpace(providerKeyID)
	if len(providerKeyID) > 12 {
		providerKeyID = providerKeyID[:12]
	}
	if strings.TrimSpace(env) == "" {
		return "skiff-release-" + providerKeyID
	}
	return "skiff-" + strings.Trim(strings.ToLower(strings.TrimSpace(env)), "-") + "-release-" + providerKeyID
}
