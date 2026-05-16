package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
)

const (
	ProviderAWS                  = "aws"
	EnvironmentRootSchemaVersion = "skiff.environment-root/v1"
)

type AWSOptions struct {
	Env          string
	Region       string
	StateBucket  string
	KMSAlias     string
	DeployerRole string
	RunnerRole   string
	SkiffdRole   string
	Now          time.Time
}

type AWSPlan struct {
	Provider       string                    `json:"provider"`
	Env            string                    `json:"env"`
	Region         string                    `json:"region"`
	StateBucket    string                    `json:"state_bucket"`
	StateBucketURI string                    `json:"state_bucket_uri"`
	KMSAlias       string                    `json:"kms_alias"`
	RootObjectKey  string                    `json:"root_object_key"`
	Resources      []AWSResourcePlan         `json:"resources"`
	BucketPolicy   PolicyDocument            `json:"bucket_policy"`
	IAMPolicies    map[string]PolicyDocument `json:"iam_policies"`
	RootConfig     EnvironmentRoot           `json:"root_config"`
}

type AWSResourcePlan struct {
	Kind     string         `json:"kind"`
	Name     string         `json:"name"`
	Action   string         `json:"action"`
	Summary  string         `json:"summary"`
	Settings map[string]any `json:"settings,omitempty"`
}

type EnvironmentRoot struct {
	SchemaVersion string            `json:"schema_version"`
	Env           string            `json:"env"`
	Provider      string            `json:"provider"`
	Region        string            `json:"region"`
	StateBucket   string            `json:"state_bucket"`
	KMSAlias      string            `json:"kms_alias"`
	Roles         map[string]string `json:"roles"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
}

type AWSBootstrapClient interface {
	EnsureStateBucket(ctx context.Context, spec StateBucketSpec) (ApplyAction, error)
	EnsureKMSKey(ctx context.Context, spec KMSKeySpec) (ApplyAction, error)
	EnsureIAMRole(ctx context.Context, spec IAMRoleSpec) (ApplyAction, error)
	PutBucketPolicy(ctx context.Context, spec BucketPolicySpec) (ApplyAction, error)
	PutEnvironmentRoot(ctx context.Context, spec EnvironmentRootSpec) (ApplyAction, error)
}

type StateBucketSpec struct {
	Name              string            `json:"name"`
	Region            string            `json:"region"`
	KMSAlias          string            `json:"kms_alias"`
	Versioning        bool              `json:"versioning"`
	PublicAccessBlock bool              `json:"public_access_block"`
	Encryption        string            `json:"encryption"`
	Lifecycle         string            `json:"lifecycle"`
	Tags              map[string]string `json:"tags,omitempty"`
}

type KMSKeySpec struct {
	Alias             string            `json:"alias"`
	Description       string            `json:"description"`
	EnableKeyRotation bool              `json:"enable_key_rotation"`
	Tags              map[string]string `json:"tags,omitempty"`
}

type IAMRoleSpec struct {
	Name       string            `json:"name"`
	Purpose    string            `json:"purpose"`
	Trust      string            `json:"trust"`
	PolicyName string            `json:"policy_name"`
	Policy     PolicyDocument    `json:"policy"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type BucketPolicySpec struct {
	Bucket string         `json:"bucket"`
	Policy PolicyDocument `json:"policy"`
}

type EnvironmentRootSpec struct {
	Key    string          `json:"key"`
	Config EnvironmentRoot `json:"config"`
}

type AWSApplyResult struct {
	Provider string        `json:"provider"`
	Env      string        `json:"env"`
	Actions  []ApplyAction `json:"actions"`
}

type ApplyAction struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Action     string `json:"action"`
	ProviderID string `json:"provider_id,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

func PlanAWS(opts AWSOptions) (*AWSPlan, error) {
	opts = opts.withDefaults()
	if err := validateAWSOptions(opts); err != nil {
		return nil, err
	}

	rootKey, err := paths.EnvironmentRoot(opts.Env)
	if err != nil {
		return nil, err
	}
	createdAt := canonical.Time(opts.Now)
	tags := map[string]string{
		"skiff.dev/env":     opts.Env,
		"skiff.dev/managed": "true",
		"skiff.dev/graph":   "environment/" + opts.Env,
	}
	roles := map[string]string{
		"deployer": opts.DeployerRole,
		"runner":   opts.RunnerRole,
		"skiffd":   opts.SkiffdRole,
	}
	stateBucketURI := "s3://" + opts.StateBucket
	root := EnvironmentRoot{
		SchemaVersion: EnvironmentRootSchemaVersion,
		Env:           opts.Env,
		Provider:      ProviderAWS,
		Region:        opts.Region,
		StateBucket:   stateBucketURI,
		KMSAlias:      opts.KMSAlias,
		Roles:         roles,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}

	bucketPolicy := StateBucketPolicy(opts.StateBucket)
	iamPolicies := map[string]PolicyDocument{
		"deployer": DeployerPolicy(opts.StateBucket, opts.KMSAlias),
		"runner":   RunnerPolicy(opts.StateBucket, opts.KMSAlias),
		"skiffd":   SkiffdPolicy(opts.StateBucket, opts.KMSAlias),
	}

	return &AWSPlan{
		Provider:       ProviderAWS,
		Env:            opts.Env,
		Region:         opts.Region,
		StateBucket:    opts.StateBucket,
		StateBucketURI: stateBucketURI,
		KMSAlias:       opts.KMSAlias,
		RootObjectKey:  rootKey,
		Resources: []AWSResourcePlan{
			{
				Kind:    "s3-bucket",
				Name:    opts.StateBucket,
				Action:  "create_or_update",
				Summary: "state bucket with versioning, KMS encryption, public access block, and lifecycle placeholder",
				Settings: map[string]any{
					"region":              opts.Region,
					"versioning":          true,
					"public_access_block": true,
					"encryption":          "aws:kms",
					"kms_alias":           opts.KMSAlias,
					"lifecycle":           "placeholder",
					"tags":                tags,
				},
			},
			{
				Kind:    "kms-key",
				Name:    opts.KMSAlias,
				Action:  "create_or_discover",
				Summary: "KMS key alias for state bucket encryption",
				Settings: map[string]any{
					"enable_key_rotation": true,
					"tags":                tags,
				},
			},
			iamResource("iam-role", opts.DeployerRole, "deployer", tags),
			iamResource("iam-role", opts.RunnerRole, "runner", tags),
			iamResource("iam-role", opts.SkiffdRole, "skiffd", tags),
			{
				Kind:    "s3-bucket-policy",
				Name:    opts.StateBucket,
				Action:  "put",
				Summary: "bucket policy requiring TLS and conditional writes on Skiff state prefixes",
			},
			{
				Kind:    "environment-root",
				Name:    rootKey,
				Action:  "create_or_verify",
				Summary: "root environment config object written after bootstrap resources exist",
			},
		},
		BucketPolicy: bucketPolicy,
		IAMPolicies:  iamPolicies,
		RootConfig:   root,
	}, nil
}

func ApplyAWS(ctx context.Context, client AWSBootstrapClient, plan *AWSPlan) (*AWSApplyResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("aws bootstrap client is required")
	}
	if plan == nil {
		return nil, errors.New("aws bootstrap plan is required")
	}

	var actions []ApplyAction
	add := func(action ApplyAction, err error) error {
		if err != nil {
			return err
		}
		actions = append(actions, action)
		return nil
	}

	if err := add(client.EnsureStateBucket(ctx, StateBucketSpec{
		Name:              plan.StateBucket,
		Region:            plan.Region,
		KMSAlias:          plan.KMSAlias,
		Versioning:        true,
		PublicAccessBlock: true,
		Encryption:        "aws:kms",
		Lifecycle:         "placeholder",
		Tags:              tagsForEnv(plan.Env),
	})); err != nil {
		return nil, err
	}
	if err := add(client.EnsureKMSKey(ctx, KMSKeySpec{
		Alias:             plan.KMSAlias,
		Description:       "Skiff state encryption key for " + plan.Env,
		EnableKeyRotation: true,
		Tags:              tagsForEnv(plan.Env),
	})); err != nil {
		return nil, err
	}
	roleSpecs := []IAMRoleSpec{
		roleSpec("deployer", plan.RootConfig.Roles["deployer"], plan.IAMPolicies["deployer"], plan.Env),
		roleSpec("runner", plan.RootConfig.Roles["runner"], plan.IAMPolicies["runner"], plan.Env),
		roleSpec("skiffd", plan.RootConfig.Roles["skiffd"], plan.IAMPolicies["skiffd"], plan.Env),
	}
	for _, spec := range roleSpecs {
		if err := add(client.EnsureIAMRole(ctx, spec)); err != nil {
			return nil, err
		}
	}
	if err := add(client.PutBucketPolicy(ctx, BucketPolicySpec{
		Bucket: plan.StateBucket,
		Policy: plan.BucketPolicy,
	})); err != nil {
		return nil, err
	}
	if err := add(client.PutEnvironmentRoot(ctx, EnvironmentRootSpec{
		Key:    plan.RootObjectKey,
		Config: plan.RootConfig,
	})); err != nil {
		return nil, err
	}

	return &AWSApplyResult{Provider: ProviderAWS, Env: plan.Env, Actions: actions}, nil
}

func (opts AWSOptions) withDefaults() AWSOptions {
	opts.Env = strings.TrimSpace(opts.Env)
	opts.Region = strings.TrimSpace(opts.Region)
	opts.StateBucket = strings.TrimSpace(opts.StateBucket)
	if opts.KMSAlias == "" && opts.Env != "" {
		opts.KMSAlias = "alias/skiff-" + opts.Env + "-state"
	}
	if opts.DeployerRole == "" && opts.Env != "" {
		opts.DeployerRole = "skiff-" + opts.Env + "-deployer"
	}
	if opts.RunnerRole == "" && opts.Env != "" {
		opts.RunnerRole = "skiff-" + opts.Env + "-runner"
	}
	if opts.SkiffdRole == "" && opts.Env != "" {
		opts.SkiffdRole = "skiff-" + opts.Env + "-skiffd"
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	return opts
}

func validateAWSOptions(opts AWSOptions) error {
	if err := paths.ValidateName("env", opts.Env); err != nil {
		return err
	}
	if opts.Region == "" {
		return errors.New("region is required")
	}
	if err := validateBucketName(opts.StateBucket); err != nil {
		return err
	}
	for field, value := range map[string]string{
		"deployer_role": opts.DeployerRole,
		"runner_role":   opts.RunnerRole,
		"skiffd_role":   opts.SkiffdRole,
	} {
		if err := validateIAMName(field, value); err != nil {
			return err
		}
	}
	if !strings.HasPrefix(opts.KMSAlias, "alias/") || len(opts.KMSAlias) <= len("alias/") {
		return errors.New("kms alias must start with alias/")
	}
	return nil
}

func validateBucketName(value string) error {
	if len(value) < 3 || len(value) > 63 {
		return fmt.Errorf("bucket must be 3-63 characters")
	}
	if value[0] == '.' || value[0] == '-' || value[len(value)-1] == '.' || value[len(value)-1] == '-' {
		return fmt.Errorf("bucket must start and end with a lowercase letter or digit")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' {
			continue
		}
		return fmt.Errorf("bucket must contain only lowercase letters, digits, dots, and hyphens")
	}
	if strings.Contains(value, "..") || strings.Contains(value, ".-") || strings.Contains(value, "-.") {
		return fmt.Errorf("bucket must not contain adjacent dot/hyphen separators")
	}
	return nil
}

func validateIAMName(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("+=,.@_-", r) {
			continue
		}
		return fmt.Errorf("%s contains unsupported IAM role name character %q", field, r)
	}
	return nil
}

func iamResource(kind, name, purpose string, tags map[string]string) AWSResourcePlan {
	return AWSResourcePlan{
		Kind:    kind,
		Name:    name,
		Action:  "create_or_update",
		Summary: "least-privilege " + purpose + " role and inline policy template",
		Settings: map[string]any{
			"purpose": purpose,
			"tags":    cloneStringMap(tags),
		},
	}
}

func roleSpec(purpose, name string, policy PolicyDocument, env string) IAMRoleSpec {
	return IAMRoleSpec{
		Name:       name,
		Purpose:    purpose,
		Trust:      purpose,
		PolicyName: "skiff-" + env + "-" + purpose,
		Policy:     policy,
		Tags:       tagsForEnv(env),
	}
}

func tagsForEnv(env string) map[string]string {
	return map[string]string{
		"skiff.dev/env":     env,
		"skiff.dev/managed": "true",
		"skiff.dev/graph":   "environment/" + env,
	}
}

func sortedPolicyNames(policies map[string]PolicyDocument) []string {
	names := make([]string, 0, len(policies))
	for name := range policies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
