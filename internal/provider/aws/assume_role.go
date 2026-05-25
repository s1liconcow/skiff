package aws

import (
	"context"
	"errors"
	"strings"
	"time"

	sdka "github.com/aws/aws-sdk-go-v2/aws"
	sdkconfig "github.com/aws/aws-sdk-go-v2/config"
	sdkcredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
)

type AssumeWriteRoleOptions struct {
	RoleARN               string
	RoleSessionName       string
	SourceIdentity        string
	TraceID               string
	BusinessJustification string
	DurationSeconds       int32
}

type TemporaryCredentials struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	SessionToken    string    `json:"session_token"`
	Expiration      time.Time `json:"expiration"`
	AssumedRoleARN  string    `json:"assumed_role_arn,omitempty"`
	SourceIdentity  string    `json:"source_identity,omitempty"`
}

func AssumeWriteRole(ctx context.Context, cfg Config, opts AssumeWriteRoleOptions) (*TemporaryCredentials, error) {
	if strings.TrimSpace(opts.RoleARN) == "" {
		return nil, errors.New("write role ARN is required")
	}
	if strings.TrimSpace(opts.RoleSessionName) == "" {
		return nil, errors.New("role session name is required")
	}
	if strings.TrimSpace(opts.SourceIdentity) == "" {
		return nil, errors.New("source identity is required")
	}
	if strings.TrimSpace(opts.TraceID) == "" {
		return nil, errors.New("trace ID is required")
	}
	if strings.TrimSpace(opts.BusinessJustification) == "" {
		return nil, errors.New("business justification is required")
	}

	loadOpts := []func(*sdkconfig.LoadOptions) error{}
	if strings.TrimSpace(cfg.Region) != "" {
		loadOpts = append(loadOpts, sdkconfig.WithRegion(cfg.Region))
	}
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
	client := sts.NewFromConfig(loaded)
	input := &sts.AssumeRoleInput{
		RoleArn:         sdka.String(strings.TrimSpace(opts.RoleARN)),
		RoleSessionName: sdka.String(strings.TrimSpace(opts.RoleSessionName)),
		SourceIdentity:  sdka.String(strings.TrimSpace(opts.SourceIdentity)),
		Tags: []ststypes.Tag{
			{Key: sdka.String("skiff.dev/trace-id"), Value: sdka.String(strings.TrimSpace(opts.TraceID))},
			{Key: sdka.String("skiff.dev/business-justification"), Value: sdka.String(strings.TrimSpace(opts.BusinessJustification))},
		},
		TransitiveTagKeys: []string{"skiff.dev/trace-id", "skiff.dev/business-justification"},
	}
	if opts.DurationSeconds > 0 {
		input.DurationSeconds = sdka.Int32(opts.DurationSeconds)
	}
	out, err := client.AssumeRole(ctx, input)
	if err != nil {
		return nil, err
	}
	if out.Credentials == nil {
		return nil, errors.New("STS assume-role returned no credentials")
	}
	creds := out.Credentials
	var expiration time.Time
	if creds.Expiration != nil {
		expiration = *creds.Expiration
	}
	result := &TemporaryCredentials{
		AccessKeyID:     sdka.ToString(creds.AccessKeyId),
		SecretAccessKey: sdka.ToString(creds.SecretAccessKey),
		SessionToken:    sdka.ToString(creds.SessionToken),
		Expiration:      expiration,
		SourceIdentity:  sdka.ToString(out.SourceIdentity),
	}
	if out.AssumedRoleUser != nil {
		result.AssumedRoleARN = sdka.ToString(out.AssumedRoleUser.Arn)
	}
	return result, nil
}
