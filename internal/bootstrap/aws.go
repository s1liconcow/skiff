package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	ProviderAWS = "aws"

	NetworkNone    = "none"
	NetworkManaged = "managed"

	IngressPrivate      = "private"
	IngressPublic       = "public"
	IngressInternalHTTP = "internal-http"

	DefaultRunnerAMISSMParameter  = "/skiff/runner/ami/al2023/x86_64/stable"
	FallbackRunnerAMISSMParameter = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
	DefaultRunnerInstallVersion   = "v0.1.0"
	defaultRunnerInstallScriptURL = "https://raw.githubusercontent.com/s1liconcow/skiff/%s/scripts/install.sh"
)

const EnvironmentRootSchemaVersion = schema.EnvironmentRootSchemaVersion

type EnvironmentRoot = schema.EnvironmentRoot
type EnvironmentNetwork = schema.EnvironmentNetwork
type EnvironmentIngress = schema.EnvironmentIngress
type EnvironmentLoadBalancerDefaults = schema.EnvironmentLoadBalancerDefaults
type EnvironmentRunner = schema.EnvironmentRunner
type ReleaseTrust = schema.ReleaseTrust
type ReleaseTrustKey = schema.ReleaseTrustKey

type AWSOptions struct {
	Env                     string
	EnvironmentClass        string
	Region                  string
	StateBucket             string
	KMSAlias                string
	DeveloperRole           string
	DeployerRole            string
	RunnerRole              string
	SkiffdRole              string
	Network                 string
	Ingress                 string
	CompanyName             string
	DomainName              string
	HostName                string
	HostedZoneID            string
	CertificateARN          string
	RunnerAMIID             string
	RunnerAMISSMParameter   string
	RunnerInstallVersion    string
	RunnerInstallBaseURL    string
	RunnerInstallScriptURL  string
	ReleaseSigningKeyID     string
	ReleaseSigningKeyRef    string
	ReleaseSigningAlgorithm string
	ReleaseSigningEncoding  string
	ReleaseSigningPublicKey string
	Now                     time.Time
}

type AWSPlan struct {
	Provider             string                    `json:"provider"`
	Env                  string                    `json:"env"`
	Region               string                    `json:"region"`
	StateBucket          string                    `json:"state_bucket"`
	StateBucketURI       string                    `json:"state_bucket_uri"`
	KMSAlias             string                    `json:"kms_alias"`
	CompanyName          string                    `json:"company_name,omitempty"`
	CompanySlug          string                    `json:"company_slug,omitempty"`
	DomainName           string                    `json:"domain_name,omitempty"`
	PublicBaseDomain     string                    `json:"public_base_domain,omitempty"`
	DefaultHostTemplate  string                    `json:"default_host_template,omitempty"`
	HostedZoneID         string                    `json:"hosted_zone_id,omitempty"`
	CertificateARN       string                    `json:"certificate_arn,omitempty"`
	NamePrefix           string                    `json:"name_prefix"`
	PublicLBName         string                    `json:"public_load_balancer_name,omitempty"`
	InternalLBName       string                    `json:"internal_load_balancer_name,omitempty"`
	ReleaseSigningKeyID  string                    `json:"release_signing_key_id,omitempty"`
	ReleaseSigningKeyRef string                    `json:"release_signing_key_ref,omitempty"`
	RootObjectKey        string                    `json:"root_object_key"`
	Resources            []AWSResourcePlan         `json:"resources"`
	BucketPolicy         PolicyDocument            `json:"bucket_policy"`
	IAMPolicies          map[string]PolicyDocument `json:"iam_policies"`
	RootConfig           EnvironmentRoot           `json:"root_config"`
}

type AWSResourcePlan struct {
	Kind     string         `json:"kind"`
	Name     string         `json:"name"`
	Action   string         `json:"action"`
	Summary  string         `json:"summary"`
	Settings map[string]any `json:"settings,omitempty"`
}

type AWSBootstrapClient interface {
	EnsureKMSKey(ctx context.Context, spec KMSKeySpec) (ApplyAction, error)
	EnsureStateBucket(ctx context.Context, spec StateBucketSpec) (ApplyAction, error)
	EnsureIAMRole(ctx context.Context, spec IAMRoleSpec) (ApplyAction, error)
	PutBucketPolicy(ctx context.Context, spec BucketPolicySpec) (ApplyAction, error)
	EnsureManagedNetwork(ctx context.Context, spec ManagedNetworkSpec) (*ManagedNetworkResult, error)
	ResolveHostedZone(ctx context.Context, spec HostedZoneSpec) (*HostedZoneResult, error)
	EnsureCertificate(ctx context.Context, spec CertificateSpec) (*CertificateResult, error)
	EnsureLoadBalancerSecurityGroup(ctx context.Context, spec LoadBalancerSecurityGroupSpec) (*SecurityGroupResult, error)
	EnsureLoadBalancer(ctx context.Context, spec LoadBalancerSpec) (*LoadBalancerResult, error)
	EnsureListener(ctx context.Context, spec ListenerSpec) (*ListenerResult, error)
	EnsureDNSAlias(ctx context.Context, spec DNSAliasSpec) (ApplyAction, error)
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
	Name                      string            `json:"name"`
	Purpose                   string            `json:"purpose"`
	Trust                     string            `json:"trust"`
	TrustPolicy               PolicyDocument    `json:"trust_policy"`
	MaxSessionDurationSeconds int32             `json:"max_session_duration_seconds,omitempty"`
	PolicyName                string            `json:"policy_name"`
	Policy                    PolicyDocument    `json:"policy"`
	Tags                      map[string]string `json:"tags,omitempty"`
}

type BucketPolicySpec struct {
	Bucket string         `json:"bucket"`
	Policy PolicyDocument `json:"policy"`
}

type EnvironmentRootSpec struct {
	Key    string          `json:"key"`
	Config EnvironmentRoot `json:"config"`
}

type ManagedNetworkSpec struct {
	NamePrefix         string            `json:"name_prefix"`
	Env                string            `json:"env"`
	Region             string            `json:"region"`
	VPCCIDR            string            `json:"vpc_cidr"`
	PublicSubnetCIDRs  []string          `json:"public_subnet_cidrs"`
	PrivateSubnetCIDRs []string          `json:"private_subnet_cidrs"`
	Tags               map[string]string `json:"tags,omitempty"`
}

type ManagedNetworkResult struct {
	Actions          []ApplyAction `json:"actions"`
	VPCID            string        `json:"vpc_id"`
	PublicSubnetIDs  []string      `json:"public_subnet_ids"`
	PrivateSubnetIDs []string      `json:"private_subnet_ids"`
}

type HostedZoneSpec struct {
	HostedZoneID string `json:"hosted_zone_id,omitempty"`
	DomainName   string `json:"domain_name,omitempty"`
}

type HostedZoneResult struct {
	Action       ApplyAction `json:"action"`
	HostedZoneID string      `json:"hosted_zone_id"`
	Name         string      `json:"name,omitempty"`
}

type CertificateSpec struct {
	DomainName       string            `json:"domain_name"`
	AlternativeNames []string          `json:"alternative_names,omitempty"`
	HostedZoneID     string            `json:"hosted_zone_id"`
	Tags             map[string]string `json:"tags,omitempty"`
}

type CertificateResult struct {
	Action         ApplyAction `json:"action"`
	CertificateARN string      `json:"certificate_arn"`
}

type LoadBalancerSecurityGroupSpec struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	VPCID       string              `json:"vpc_id"`
	Ingress     []SecurityGroupRule `json:"ingress"`
	Egress      []SecurityGroupRule `json:"egress"`
	Tags        map[string]string   `json:"tags,omitempty"`
}

type SecurityGroupRule struct {
	Protocol    string   `json:"protocol"`
	FromPort    int32    `json:"from_port"`
	ToPort      int32    `json:"to_port"`
	CIDRs       []string `json:"cidrs,omitempty"`
	Description string   `json:"description,omitempty"`
}

type SecurityGroupResult struct {
	Action  ApplyAction `json:"action"`
	GroupID string      `json:"group_id"`
}

type LoadBalancerSpec struct {
	Name             string            `json:"name"`
	Internal         bool              `json:"internal"`
	SecurityGroupIDs []string          `json:"security_group_ids"`
	SubnetIDs        []string          `json:"subnet_ids"`
	Tags             map[string]string `json:"tags,omitempty"`
}

type LoadBalancerResult struct {
	Action       ApplyAction `json:"action"`
	ARN          string      `json:"arn"`
	DNSName      string      `json:"dns_name"`
	HostedZoneID string      `json:"hosted_zone_id"`
}

type ListenerSpec struct {
	Name             string `json:"name"`
	LoadBalancerARN  string `json:"load_balancer_arn"`
	Port             int32  `json:"port"`
	Protocol         string `json:"protocol"`
	CertificateARN   string `json:"certificate_arn,omitempty"`
	DefaultAction    string `json:"default_action"`
	RedirectPort     int32  `json:"redirect_port,omitempty"`
	RedirectProtocol string `json:"redirect_protocol,omitempty"`
}

type ListenerResult struct {
	Action ApplyAction `json:"action"`
	ARN    string      `json:"arn"`
}

type DNSAliasSpec struct {
	Name                 string `json:"name"`
	HostedZoneID         string `json:"hosted_zone_id"`
	TargetDNSName        string `json:"target_dns_name"`
	TargetHostedZoneID   string `json:"target_hosted_zone_id"`
	EvaluateTargetHealth bool   `json:"evaluate_target_health"`
}

type AWSApplyResult struct {
	Provider   string          `json:"provider"`
	Env        string          `json:"env"`
	Actions    []ApplyAction   `json:"actions"`
	RootConfig EnvironmentRoot `json:"root_config"`
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
	releasePolicy, err := schema.DefaultEnvironmentReleasePolicy(opts.EnvironmentClass)
	if err != nil {
		return nil, err
	}
	companySlug := companySlugForNames(opts.CompanyName)
	namePrefix := environmentNamePrefix(opts.Env, companySlug)
	publicBaseDomain := publicBaseDomainForOptions(opts)
	defaultHostTemplate := defaultServiceHostTemplate(publicBaseDomain)
	publicLBName := awsLimitedName(namePrefix+"-public", 32)
	internalLBName := awsLimitedName(namePrefix+"-internal", 32)
	planDomainName := ""
	planHostedZoneID := ""
	planCertificateARN := ""
	if opts.Ingress == IngressPublic {
		planDomainName = opts.DomainName
		planHostedZoneID = opts.HostedZoneID
		planCertificateARN = opts.CertificateARN
	}

	rootKey, err := paths.EnvironmentRoot(opts.Env)
	if err != nil {
		return nil, err
	}
	createdAt := canonical.Time(opts.Now)
	tags := map[string]string{
		"skiff.dev/env":               opts.Env,
		"skiff.dev/environment-class": opts.EnvironmentClass,
		"skiff.dev/managed":           "true",
		"skiff.dev/graph":             "environment/" + opts.Env,
	}
	if companySlug != "" {
		tags["skiff.dev/company"] = companySlug
	}
	roles := map[string]string{
		"developer": opts.DeveloperRole,
		"deployer":  opts.DeployerRole,
		"runner":    opts.RunnerRole,
		"skiffd":    opts.SkiffdRole,
	}
	stateBucketURI := "s3://" + opts.StateBucket
	root := EnvironmentRoot{
		SchemaVersion:    EnvironmentRootSchemaVersion,
		Env:              opts.Env,
		EnvironmentClass: opts.EnvironmentClass,
		Provider:         ProviderAWS,
		Region:           opts.Region,
		StateBucket:      stateBucketURI,
		KMSAlias:         opts.KMSAlias,
		Roles:            roles,
		ReleasePolicy:    &releasePolicy,
		CreatedAt:        createdAt,
		UpdatedAt:        createdAt,
	}
	if opts.ReleaseSigningKeyID != "" && opts.ReleaseSigningPublicKey != "" {
		root.ReleaseTrust = &ReleaseTrust{
			ActiveKeyIDs: []string{opts.ReleaseSigningKeyID},
			Keys: []ReleaseTrustKey{{
				KeyID:     opts.ReleaseSigningKeyID,
				Backend:   releaseSigningBackend(opts.ReleaseSigningKeyRef),
				Algorithm: opts.ReleaseSigningAlgorithm,
				Encoding:  opts.ReleaseSigningEncoding,
				KeyRef:    opts.ReleaseSigningKeyRef,
				PublicKey: opts.ReleaseSigningPublicKey,
				Status:    "active",
				CreatedAt: createdAt,
			}},
		}
	}
	if opts.Network == NetworkManaged {
		root.Network = &EnvironmentNetwork{
			Mode:             NetworkManaged,
			VPCID:            "${aws_vpc.skiff.id}",
			PrivateSubnetIDs: []string{"${aws_subnet.skiff_private[0].id}", "${aws_subnet.skiff_private[1].id}"},
			PublicSubnetIDs:  []string{"${aws_subnet.skiff_public[0].id}", "${aws_subnet.skiff_public[1].id}"},
		}
		root.Ingress = &EnvironmentIngress{Type: opts.Ingress}
		switch opts.Ingress {
		case IngressInternalHTTP:
			root.Ingress.LoadBalancer = &EnvironmentLoadBalancerDefaults{
				ARN:             "${aws_lb.skiff_internal.arn}",
				DNSName:         "${aws_lb.skiff_internal.dns_name}",
				ProviderDNSName: "${aws_lb.skiff_internal.dns_name}",
				HostedZoneID:    "${aws_lb.skiff_internal.zone_id}",
				SecurityGroupID: "${aws_security_group.skiff_internal_lb.id}",
				HTTPListenerARN: "${aws_lb_listener.skiff_internal_http.arn}",
			}
		case IngressPublic:
			root.Ingress.Host = publicBaseDomain
			root.Ingress.BaseDomain = publicBaseDomain
			root.Ingress.DefaultHostTemplate = defaultHostTemplate
			root.Ingress.DomainName = opts.DomainName
			root.Ingress.Route53ZoneID = opts.HostedZoneID
			dnsName := "${aws_lb.skiff_public.dns_name}"
			if publicBaseDomain != "" {
				dnsName = publicBaseDomain
			}
			certificateARN := opts.CertificateARN
			httpsListenerARN := ""
			if publicBaseDomain != "" && certificateARN == "" {
				certificateARN = "${aws_acm_certificate_validation.skiff_public.certificate_arn}"
			}
			if certificateARN != "" {
				httpsListenerARN = "${aws_lb_listener.skiff_public_https.arn}"
			}
			root.Ingress.LoadBalancer = &EnvironmentLoadBalancerDefaults{
				ARN:              "${aws_lb.skiff_public.arn}",
				DNSName:          dnsName,
				ProviderDNSName:  "${aws_lb.skiff_public.dns_name}",
				HostedZoneID:     "${aws_lb.skiff_public.zone_id}",
				SecurityGroupID:  "${aws_security_group.skiff_public_lb.id}",
				HTTPListenerARN:  "${aws_lb_listener.skiff_public_http.arn}",
				HTTPSListenerARN: httpsListenerARN,
				CertificateARN:   certificateARN,
			}
		}
	}
	root.Runner = runnerDefaults(opts)

	bucketPolicy := StateBucketPolicy(opts.StateBucket)
	iamPolicies := map[string]PolicyDocument{
		"developer": DeveloperPolicy(opts.StateBucket, opts.KMSAlias),
		"deployer":  DeployerPolicy(opts.StateBucket, opts.KMSAlias),
		"runner":    RunnerPolicy(opts.StateBucket, opts.KMSAlias),
		"skiffd":    SkiffdPolicy(opts.StateBucket, opts.KMSAlias),
	}
	if releaseSigningAlias := releaseSigningKMSAlias(opts.ReleaseSigningKeyRef); releaseSigningAlias != "" {
		iamPolicies["deployer"] = withReleaseSigningKMSAccess(iamPolicies["deployer"], releaseSigningAlias)
		iamPolicies["skiffd"] = withReleaseSigningKMSAccess(iamPolicies["skiffd"], releaseSigningAlias)
	}

	resources := []AWSResourcePlan{
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
		iamResource("iam-role", opts.DeveloperRole, "developer", tags),
		iamResource("iam-role", opts.DeployerRole, "deployer", tags),
		iamResource("iam-role", opts.RunnerRole, "runner", tags),
		iamResource("iam-role", opts.SkiffdRole, "skiffd", tags),
		{
			Kind:    "s3-bucket-policy",
			Name:    opts.StateBucket,
			Action:  "put",
			Summary: "bucket policy requiring TLS and conditional writes on Skiff state prefixes",
		},
	}
	if root.ReleaseTrust != nil {
		resources = append(resources, AWSResourcePlan{
			Kind:    "release-trust",
			Name:    opts.ReleaseSigningKeyID,
			Action:  "write",
			Summary: "public release signing trust metadata stored in the environment root",
			Settings: map[string]any{
				"key_id":    opts.ReleaseSigningKeyID,
				"backend":   releaseSigningBackend(opts.ReleaseSigningKeyRef),
				"algorithm": opts.ReleaseSigningAlgorithm,
			},
		})
	}
	if releaseSigningAlias := releaseSigningKMSAlias(opts.ReleaseSigningKeyRef); releaseSigningAlias != "" {
		resources = append(resources, AWSResourcePlan{
			Kind:    "kms-key",
			Name:    releaseSigningAlias,
			Action:  "create_or_discover",
			Summary: "asymmetric AWS KMS key for release signing",
			Settings: map[string]any{
				"algorithm": "ECDSA_SHA_256",
				"key_spec":  "ECC_NIST_P256",
				"key_usage": "SIGN_VERIFY",
				"purpose":   "release-signing",
				"tags":      tags,
			},
		})
	}
	resources = append(resources, managedNetworkResources(opts, tags)...)
	resources = append(resources, AWSResourcePlan{
		Kind:    "environment-root",
		Name:    rootKey,
		Action:  "create_or_verify",
		Summary: "root environment config object written after bootstrap resources exist",
	})

	return &AWSPlan{
		Provider:             ProviderAWS,
		Env:                  opts.Env,
		Region:               opts.Region,
		StateBucket:          opts.StateBucket,
		StateBucketURI:       stateBucketURI,
		KMSAlias:             opts.KMSAlias,
		CompanyName:          opts.CompanyName,
		CompanySlug:          companySlug,
		DomainName:           planDomainName,
		PublicBaseDomain:     publicBaseDomain,
		DefaultHostTemplate:  defaultHostTemplate,
		HostedZoneID:         planHostedZoneID,
		CertificateARN:       planCertificateARN,
		NamePrefix:           namePrefix,
		PublicLBName:         publicLBName,
		InternalLBName:       internalLBName,
		ReleaseSigningKeyID:  opts.ReleaseSigningKeyID,
		ReleaseSigningKeyRef: opts.ReleaseSigningKeyRef,
		RootObjectKey:        rootKey,
		Resources:            resources,
		BucketPolicy:         bucketPolicy,
		IAMPolicies:          iamPolicies,
		RootConfig:           root,
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
	root := cloneEnvironmentRoot(plan.RootConfig)
	add := func(action ApplyAction, err error) error {
		if err != nil {
			return err
		}
		actions = append(actions, action)
		return nil
	}
	addResult := func(action ApplyAction) {
		actions = append(actions, action)
	}

	if err := add(client.EnsureKMSKey(ctx, KMSKeySpec{
		Alias:             plan.KMSAlias,
		Description:       "Skiff state encryption key for " + plan.Env,
		EnableKeyRotation: true,
		Tags:              tagsForPlan(plan),
	})); err != nil {
		return nil, err
	}
	if err := add(client.EnsureStateBucket(ctx, StateBucketSpec{
		Name:              plan.StateBucket,
		Region:            plan.Region,
		KMSAlias:          plan.KMSAlias,
		Versioning:        true,
		PublicAccessBlock: true,
		Encryption:        "aws:kms",
		Lifecycle:         "placeholder",
		Tags:              tagsForPlan(plan),
	})); err != nil {
		return nil, err
	}
	roleSpecs := []IAMRoleSpec{
		roleSpec("developer", plan.RootConfig.Roles["developer"], plan.IAMPolicies["developer"], plan.Env),
		roleSpec("deployer", plan.RootConfig.Roles["deployer"], plan.IAMPolicies["deployer"], plan.Env),
		roleSpec("runner", plan.RootConfig.Roles["runner"], plan.IAMPolicies["runner"], plan.Env),
		roleSpec("skiffd", plan.RootConfig.Roles["skiffd"], plan.IAMPolicies["skiffd"], plan.Env),
	}
	for _, spec := range roleSpecs {
		action, err := client.EnsureIAMRole(ctx, spec)
		if err != nil {
			return nil, err
		}
		addResult(action)
		if action.ProviderID != "" {
			if root.Roles == nil {
				root.Roles = map[string]string{}
			}
			root.Roles[spec.Purpose] = action.ProviderID
		}
	}

	var network *ManagedNetworkResult
	if plan.RootConfig.Network != nil && plan.RootConfig.Network.Mode == NetworkManaged {
		var err error
		network, err = client.EnsureManagedNetwork(ctx, ManagedNetworkSpec{
			NamePrefix:         plan.NamePrefix,
			Env:                plan.Env,
			Region:             plan.Region,
			VPCCIDR:            "10.76.0.0/16",
			PublicSubnetCIDRs:  []string{"10.76.0.0/24", "10.76.1.0/24"},
			PrivateSubnetCIDRs: []string{"10.76.10.0/24", "10.76.11.0/24"},
			Tags:               tagsForPlan(plan),
		})
		if err != nil {
			return nil, err
		}
		actions = append(actions, network.Actions...)
		if err := validateManagedNetworkResult(network); err != nil {
			return nil, err
		}
		root.Network = &EnvironmentNetwork{
			Mode:             NetworkManaged,
			VPCID:            network.VPCID,
			PublicSubnetIDs:  append([]string(nil), network.PublicSubnetIDs...),
			PrivateSubnetIDs: append([]string(nil), network.PrivateSubnetIDs...),
		}
	}
	if root.Ingress != nil {
		if err := applyAWSIngress(ctx, client, plan, &root, network, addResult); err != nil {
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
		Config: root,
	})); err != nil {
		return nil, err
	}

	return &AWSApplyResult{Provider: ProviderAWS, Env: plan.Env, Actions: actions, RootConfig: root}, nil
}

func (opts AWSOptions) withDefaults() AWSOptions {
	opts.Env = strings.TrimSpace(opts.Env)
	opts.EnvironmentClass = strings.TrimSpace(opts.EnvironmentClass)
	opts.Region = strings.TrimSpace(opts.Region)
	opts.StateBucket = strings.TrimSpace(opts.StateBucket)
	opts.Network = strings.TrimSpace(opts.Network)
	opts.Ingress = strings.TrimSpace(opts.Ingress)
	opts.CompanyName = strings.TrimSpace(opts.CompanyName)
	opts.DomainName = normalizeDNSName(opts.DomainName)
	opts.HostName = normalizeDNSName(opts.HostName)
	opts.HostedZoneID = strings.TrimSpace(opts.HostedZoneID)
	opts.CertificateARN = strings.TrimSpace(opts.CertificateARN)
	opts.RunnerAMIID = strings.TrimSpace(opts.RunnerAMIID)
	opts.RunnerAMISSMParameter = normalizeAMISSMParameter(opts.RunnerAMISSMParameter)
	opts.RunnerInstallVersion = strings.TrimSpace(opts.RunnerInstallVersion)
	opts.RunnerInstallBaseURL = strings.TrimSpace(opts.RunnerInstallBaseURL)
	opts.RunnerInstallScriptURL = strings.TrimSpace(opts.RunnerInstallScriptURL)
	opts.ReleaseSigningKeyID = strings.TrimSpace(opts.ReleaseSigningKeyID)
	opts.ReleaseSigningKeyRef = strings.TrimSpace(opts.ReleaseSigningKeyRef)
	opts.ReleaseSigningAlgorithm = strings.TrimSpace(opts.ReleaseSigningAlgorithm)
	opts.ReleaseSigningEncoding = strings.TrimSpace(opts.ReleaseSigningEncoding)
	opts.ReleaseSigningPublicKey = strings.TrimSpace(opts.ReleaseSigningPublicKey)
	if opts.ReleaseSigningKeyRef != "" && opts.ReleaseSigningAlgorithm == "" {
		opts.ReleaseSigningAlgorithm = defaultReleaseSigningAlgorithm(opts.ReleaseSigningKeyRef)
	}
	if opts.ReleaseSigningKeyRef != "" && opts.ReleaseSigningEncoding == "" {
		opts.ReleaseSigningEncoding = defaultReleaseSigningEncoding(opts.ReleaseSigningKeyRef)
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.Network == "" {
		opts.Network = NetworkNone
	}
	if opts.Ingress == "" {
		opts.Ingress = IngressPrivate
	}
	if opts.EnvironmentClass == "" {
		opts.EnvironmentClass = schema.EnvironmentClassDevelopment
	}
	defaultedRunnerAMISSMParameter := false
	if opts.RunnerAMIID == "" && opts.RunnerAMISSMParameter == "" {
		opts.RunnerAMISSMParameter = DefaultRunnerAMISSMParameter
		defaultedRunnerAMISSMParameter = true
	}
	if opts.RunnerAMIID == "" && opts.RunnerInstallVersion == "" && shouldInstallRunnerOnFirstBoot(opts.RunnerAMISSMParameter, defaultedRunnerAMISSMParameter) {
		opts.RunnerInstallVersion = DefaultRunnerInstallVersion
	}
	if opts.RunnerInstallVersion != "" && opts.RunnerInstallScriptURL == "" {
		opts.RunnerInstallScriptURL = defaultInstallScriptURL(opts.RunnerInstallVersion)
	}
	if opts.StateBucket == "" && opts.Env != "" && opts.Region != "" {
		opts.StateBucket = defaultStateBucket(opts.Env, opts.Region, opts.CompanyName, opts.Now)
	}
	if opts.KMSAlias == "" && opts.Env != "" {
		opts.KMSAlias = "alias/skiff-" + opts.Env + "-state"
	}
	if opts.DeveloperRole == "" && opts.Env != "" {
		opts.DeveloperRole = "skiff-" + opts.Env + "-developer"
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
	return opts
}

func validateAWSOptions(opts AWSOptions) error {
	if err := paths.ValidateName("env", opts.Env); err != nil {
		return err
	}
	if opts.Region == "" {
		return errors.New("region is required")
	}
	if normalized, err := schema.NormalizeEnvironmentClass(opts.EnvironmentClass); err != nil {
		return err
	} else if normalized != opts.EnvironmentClass {
		return errors.New("environment class must be one of production, staging, development, or sandbox")
	}
	if err := validateBucketName(opts.StateBucket); err != nil {
		return err
	}
	switch opts.Network {
	case NetworkNone, NetworkManaged:
	default:
		return fmt.Errorf("network must be %q or %q", NetworkNone, NetworkManaged)
	}
	switch opts.Ingress {
	case IngressPrivate, IngressPublic, IngressInternalHTTP:
	default:
		return fmt.Errorf("ingress must be %q, %q, or %q", IngressPrivate, IngressPublic, IngressInternalHTTP)
	}
	if opts.Ingress != IngressPrivate && opts.Network != NetworkManaged {
		return fmt.Errorf("ingress %s requires --network managed", opts.Ingress)
	}
	if err := validateCompanyName(opts.CompanyName); err != nil {
		return err
	}
	if opts.Ingress == IngressPublic {
		if err := validateDNSName("domain_name", opts.DomainName); err != nil {
			return err
		}
		if err := validateDNSName("host_name", opts.HostName); err != nil {
			return err
		}
		if opts.HostName != "" && opts.DomainName != "" && !dnsNameInDomain(opts.HostName, opts.DomainName) {
			return fmt.Errorf("host_name %q must be inside domain_name %q", opts.HostName, opts.DomainName)
		}
		if opts.HostName != "" && opts.DomainName == "" && opts.HostedZoneID == "" {
			return errors.New("host_name requires domain_name or hosted_zone_id")
		}
		if opts.HostedZoneID != "" && opts.DomainName == "" && opts.HostName == "" {
			return errors.New("hosted zone id requires domain_name or host_name")
		}
	} else if opts.HostName != "" || opts.HostedZoneID != "" || opts.CertificateARN != "" {
		return errors.New("host, hosted zone, and certificate options require --ingress public")
	}
	if err := validateCertificateARN(opts.CertificateARN); err != nil {
		return err
	}
	if opts.RunnerAMISSMParameter != "" && strings.ContainsAny(opts.RunnerAMISSMParameter, " \t\r\n") {
		return errors.New("runner AMI SSM parameter must not contain whitespace")
	}
	if opts.RunnerInstallVersion != "" {
		if err := validateRunnerInstallVersion(opts.RunnerInstallVersion); err != nil {
			return err
		}
	}
	if opts.ReleaseSigningKeyID != "" || opts.ReleaseSigningPublicKey != "" {
		if opts.ReleaseSigningKeyID == "" || opts.ReleaseSigningKeyRef == "" || opts.ReleaseSigningPublicKey == "" {
			return errors.New("release signing key ID, ref, and public key must be provided together")
		}
		if opts.ReleaseSigningAlgorithm == "" {
			return errors.New("release signing algorithm is required when release signing trust is provided")
		}
		if opts.ReleaseSigningEncoding == "" {
			return errors.New("release signing public-key encoding is required when release signing trust is provided")
		}
		if err := paths.ValidateID("release_signing_key_id", opts.ReleaseSigningKeyID); err != nil {
			return err
		}
	}
	for field, value := range map[string]string{
		"developer_role": opts.DeveloperRole,
		"deployer_role":  opts.DeployerRole,
		"runner_role":    opts.RunnerRole,
		"skiffd_role":    opts.SkiffdRole,
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

func runnerDefaults(opts AWSOptions) *EnvironmentRunner {
	runner := &EnvironmentRunner{
		AMIID:            opts.RunnerAMIID,
		AMISSMParameter:  opts.RunnerAMISSMParameter,
		InstallVersion:   opts.RunnerInstallVersion,
		InstallBaseURL:   opts.RunnerInstallBaseURL,
		InstallScriptURL: opts.RunnerInstallScriptURL,
	}
	if runner.AMIID != "" {
		runner.AMISSMParameter = ""
	}
	return runner
}

func publicBaseDomainForOptions(opts AWSOptions) string {
	if opts.Ingress != IngressPublic {
		return ""
	}
	if opts.HostName != "" {
		return opts.HostName
	}
	if opts.DomainName != "" {
		return opts.Env + "." + opts.DomainName
	}
	return ""
}

func defaultServiceHostTemplate(baseDomain string) string {
	baseDomain = strings.TrimSpace(baseDomain)
	if baseDomain == "" {
		return ""
	}
	return "{service}." + baseDomain
}

func releaseSigningBackend(keyRef string) string {
	keyRef = strings.TrimSpace(keyRef)
	if before, _, ok := strings.Cut(keyRef, "://"); ok && before != "" {
		return before
	}
	return "unknown"
}

func defaultReleaseSigningAlgorithm(keyRef string) string {
	switch releaseSigningBackend(keyRef) {
	case "keychain":
		return "ed25519"
	case "aws-kms":
		return "ecdsa-p256-sha256"
	default:
		return ""
	}
}

func defaultReleaseSigningEncoding(keyRef string) string {
	switch releaseSigningBackend(keyRef) {
	case "keychain":
		return "raw"
	case "aws-kms":
		return "pkix-der"
	default:
		return ""
	}
}

func releaseSigningKMSAlias(keyRef string) string {
	parsed, err := url.Parse(strings.TrimSpace(keyRef))
	if err != nil || parsed.Scheme != "aws-kms" || parsed.Host != "alias" {
		return ""
	}
	alias := strings.Trim(parsed.Path, "/")
	if alias == "" {
		return ""
	}
	return "alias/" + alias
}

func withReleaseSigningKMSAccess(doc PolicyDocument, alias string) PolicyDocument {
	if strings.TrimSpace(alias) == "" {
		return doc
	}
	doc.Statement = append(doc.Statement, PolicyStatement{
		Sid:      "UseReleaseSigningKMSKey",
		Effect:   "Allow",
		Action:   []string{"kms:DescribeKey", "kms:GetPublicKey", "kms:Sign"},
		Resource: "*",
		Condition: map[string]map[string]string{
			"ForAnyValue:StringEquals": {"kms:ResourceAliases": alias},
		},
	})
	return doc
}

func normalizeAMISSMParameter(value string) string {
	value = strings.TrimSpace(value)
	return strings.TrimPrefix(value, "resolve:ssm:")
}

func defaultInstallScriptURL(version string) string {
	return fmt.Sprintf(defaultRunnerInstallScriptURL, version)
}

func shouldInstallRunnerOnFirstBoot(amiSSMParameter string, defaulted bool) bool {
	if defaulted {
		return false
	}
	return normalizeAMISSMParameter(amiSSMParameter) == FallbackRunnerAMISSMParameter
}

func validateRunnerInstallVersion(value string) error {
	value = strings.TrimSpace(value)
	switch value {
	case "", "dev", "latest":
		return fmt.Errorf("runner install version must be an explicit release tag, not %q", value)
	}
	if strings.ContainsAny(value, " \t\r\n;&|`$<>") {
		return errors.New("runner install version contains unsupported shell metacharacters")
	}
	return nil
}

func defaultStateBucket(env, region, company string, now time.Time) string {
	region = strings.ToLower(strings.TrimSpace(region))
	env = strings.ToLower(strings.TrimSpace(env))
	identity := companySlugForNames(company)
	if identity == "" {
		identity = generatedBucketIdentity(env, region, now)
	}
	parts := []string{"skiff", identity, env, strings.ReplaceAll(region, "_", "-")}
	return fitBucketName(strings.Join(parts, "-"), "-state")
}

func generatedBucketIdentity(env, region string, now time.Time) string {
	suffix := strings.ToLower(events.NewID(now, env+"\x00"+region))
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	return suffix
}

func fitBucketName(prefix, suffix string) string {
	maxPrefix := 63 - len(suffix)
	if len(prefix) > maxPrefix {
		prefix = strings.Trim(prefix[:maxPrefix], "-.")
	}
	return prefix + suffix
}

func validateCompanyName(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if companySlugForNames(value) == "" {
		return errors.New("company name must include at least one ASCII letter or digit")
	}
	return nil
}

func companySlugForNames(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastHyphen := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteByte(c)
			lastHyphen = false
			continue
		}
		if b.Len() > 0 && !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func environmentNamePrefix(env, companySlug string) string {
	parts := []string{"skiff"}
	if companySlug != "" {
		parts = append(parts, companySlug)
	}
	return strings.Join(append(parts, env), "-")
}

func awsLimitedName(value string, max int) string {
	value = strings.Trim(value, "-")
	if len(value) <= max {
		return value
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(value))
	suffix := fmt.Sprintf("%08x", hash.Sum32())
	prefixLen := max - len(suffix) - 1
	if prefixLen < 1 {
		return suffix[:max]
	}
	prefix := strings.Trim(value[:prefixLen], "-")
	if prefix == "" {
		return suffix[:max]
	}
	return prefix + "-" + suffix
}

func normalizeDNSName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	return strings.TrimSuffix(value, ".")
}

func validateDNSName(field, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 253 {
		return fmt.Errorf("%s must be 253 characters or fewer", field)
	}
	if strings.ContainsAny(value, "/:_ \t\r\n") {
		return fmt.Errorf("%s must be a DNS name without scheme, path, port, or whitespace", field)
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return fmt.Errorf("%s must include at least two DNS labels", field)
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("%s contains an empty or overlong DNS label", field)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%s DNS labels must not start or end with '-'", field)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
				continue
			}
			return fmt.Errorf("%s contains unsupported DNS character %q", field, c)
		}
	}
	return nil
}

func dnsNameInDomain(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func validateCertificateARN(value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, " \t\r\n") || !strings.HasPrefix(value, "arn:") || !strings.Contains(value, ":acm:") || !strings.Contains(value, ":certificate/") {
		return errors.New("certificate ARN must be an AWS ACM certificate ARN")
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
	summary := "least-privilege " + purpose + " role and inline policy template"
	if purpose == "developer" {
		summary = "read-only developer role for Skiff state and resource inspection"
	}
	if purpose == "deployer" {
		summary = "write role requiring temporary auditable STS escalation"
	}
	return AWSResourcePlan{
		Kind:    kind,
		Name:    name,
		Action:  "create_or_update",
		Summary: summary,
		Settings: map[string]any{
			"purpose": purpose,
			"tags":    cloneStringMap(tags),
		},
	}
}

func managedNetworkResources(opts AWSOptions, tags map[string]string) []AWSResourcePlan {
	if opts.Network != NetworkManaged {
		return nil
	}
	namePrefix := environmentNamePrefix(opts.Env, companySlugForNames(opts.CompanyName))
	publicBaseDomain := publicBaseDomainForOptions(opts)
	resources := []AWSResourcePlan{
		{
			Kind:    "vpc",
			Name:    namePrefix,
			Action:  "create_or_update",
			Summary: "managed Skiff VPC for workload subnets",
			Settings: map[string]any{
				"cidr": "10.76.0.0/16",
				"tags": cloneStringMap(tags),
			},
		},
		{
			Kind:    "subnet",
			Name:    namePrefix + "-public",
			Action:  "create_or_update",
			Summary: "two managed public subnets for NAT and optional load balancers",
			Settings: map[string]any{
				"cidrs": []string{"10.76.0.0/24", "10.76.1.0/24"},
				"tags":  cloneStringMap(tags),
			},
		},
		{
			Kind:    "subnet",
			Name:    namePrefix + "-private",
			Action:  "create_or_update",
			Summary: "two managed private workload subnets",
			Settings: map[string]any{
				"cidrs": []string{"10.76.10.0/24", "10.76.11.0/24"},
				"tags":  cloneStringMap(tags),
			},
		},
		{
			Kind:    "internet-gateway",
			Name:    namePrefix,
			Action:  "create_or_update",
			Summary: "internet gateway for managed public subnets",
		},
		{
			Kind:    "nat-gateway",
			Name:    namePrefix,
			Action:  "create_or_update",
			Summary: "NAT gateway for private workload egress",
		},
		{
			Kind:    "route-table",
			Name:    namePrefix,
			Action:  "create_or_update",
			Summary: "public and private route tables for managed network",
		},
	}
	switch opts.Ingress {
	case IngressInternalHTTP:
		resources = append(resources,
			AWSResourcePlan{
				Kind:    "security-group",
				Name:    namePrefix + "-internal-lb",
				Action:  "create_or_update",
				Summary: "managed internal load balancer security group",
			},
			AWSResourcePlan{
				Kind:    "load-balancer",
				Name:    awsLimitedName(namePrefix+"-internal", 32),
				Action:  "create_or_update",
				Summary: "managed internal application load balancer",
			},
			AWSResourcePlan{
				Kind:    "listener",
				Name:    namePrefix + "-internal-http",
				Action:  "create_or_update",
				Summary: "managed internal HTTP listener",
			},
		)
	case IngressPublic:
		resources = append(resources,
			AWSResourcePlan{
				Kind:    "security-group",
				Name:    namePrefix + "-public-lb",
				Action:  "create_or_update",
				Summary: "managed public load balancer security group",
			},
			AWSResourcePlan{
				Kind:    "load-balancer",
				Name:    awsLimitedName(namePrefix+"-public", 32),
				Action:  "create_or_update",
				Summary: "managed internet-facing application load balancer",
			},
			AWSResourcePlan{
				Kind:    "listener",
				Name:    namePrefix + "-public-http",
				Action:  "create_or_update",
				Summary: "managed public HTTP listener",
			},
		)
		if publicBaseDomain != "" {
			resources = append(resources,
				AWSResourcePlan{
					Kind:    "dns-record",
					Name:    publicBaseDomain,
					Action:  "create_or_update",
					Summary: "Route53 base-domain alias for the managed public load balancer",
				},
				AWSResourcePlan{
					Kind:    "dns-record",
					Name:    "*." + publicBaseDomain,
					Action:  "create_or_update",
					Summary: "Route53 wildcard alias for services on the shared public load balancer",
				},
			)
			if opts.CertificateARN == "" {
				resources = append(resources,
					AWSResourcePlan{
						Kind:    "certificate",
						Name:    publicBaseDomain,
						Action:  "create_or_update",
						Summary: "ACM certificate with DNS validation for base domain and wildcard service hosts",
					},
				)
			}
		}
		if publicBaseDomain != "" || opts.CertificateARN != "" {
			resources = append(resources, AWSResourcePlan{
				Kind:    "listener",
				Name:    namePrefix + "-public-https",
				Action:  "create_or_update",
				Summary: "managed public HTTPS listener",
			})
		}
	}
	return resources
}

func roleSpec(purpose, name string, policy PolicyDocument, env string) IAMRoleSpec {
	tags := tagsForEnv(env)
	tags["skiff.dev/access"] = purpose
	trustPolicy := DefaultAssumeRoleTrustPolicy()
	var maxSession int32
	if purpose == "deployer" {
		trustPolicy = EscalatedWriteTrustPolicy()
		maxSession = 3600
	}
	return IAMRoleSpec{
		Name:                      name,
		Purpose:                   purpose,
		Trust:                     purpose,
		TrustPolicy:               trustPolicy,
		MaxSessionDurationSeconds: maxSession,
		PolicyName:                "skiff-" + env + "-" + purpose,
		Policy:                    policy,
		Tags:                      tags,
	}
}

func applyAWSIngress(ctx context.Context, client AWSBootstrapClient, plan *AWSPlan, root *EnvironmentRoot, network *ManagedNetworkResult, add func(ApplyAction)) error {
	if root == nil || root.Ingress == nil {
		return nil
	}
	if root.Ingress.Type == IngressPrivate {
		return nil
	}
	if err := validateManagedNetworkResult(network); err != nil {
		return err
	}
	tags := tagsForPlan(plan)
	switch root.Ingress.Type {
	case IngressInternalHTTP:
		sg, err := client.EnsureLoadBalancerSecurityGroup(ctx, LoadBalancerSecurityGroupSpec{
			Name:        plan.NamePrefix + "-internal-lb",
			Description: "Skiff managed internal load balancer",
			VPCID:       network.VPCID,
			Ingress: []SecurityGroupRule{{
				Protocol:    "tcp",
				FromPort:    80,
				ToPort:      80,
				CIDRs:       []string{"10.76.0.0/16"},
				Description: "allow HTTP from managed VPC",
			}},
			Egress: []SecurityGroupRule{allowAllEgressRule()},
			Tags:   tags,
		})
		if err != nil {
			return err
		}
		add(sg.Action)
		lb, err := client.EnsureLoadBalancer(ctx, LoadBalancerSpec{
			Name:             plan.InternalLBName,
			Internal:         true,
			SecurityGroupIDs: []string{sg.GroupID},
			SubnetIDs:        append([]string(nil), network.PrivateSubnetIDs...),
			Tags:             tags,
		})
		if err != nil {
			return err
		}
		add(lb.Action)
		http, err := client.EnsureListener(ctx, ListenerSpec{
			Name:            plan.NamePrefix + "-internal-http",
			LoadBalancerARN: lb.ARN,
			Port:            80,
			Protocol:        "HTTP",
			DefaultAction:   "fixed-response",
		})
		if err != nil {
			return err
		}
		add(http.Action)
		root.Ingress = &EnvironmentIngress{
			Type: IngressInternalHTTP,
			LoadBalancer: &EnvironmentLoadBalancerDefaults{
				ARN:             lb.ARN,
				DNSName:         lb.DNSName,
				ProviderDNSName: lb.DNSName,
				HostedZoneID:    lb.HostedZoneID,
				SecurityGroupID: sg.GroupID,
				HTTPListenerARN: http.ARN,
			},
		}
	case IngressPublic:
		var zoneID string
		if plan.PublicBaseDomain != "" {
			zone, err := client.ResolveHostedZone(ctx, HostedZoneSpec{
				HostedZoneID: plan.HostedZoneID,
				DomainName:   plan.DomainName,
			})
			if err != nil {
				return err
			}
			add(zone.Action)
			zoneID = zone.HostedZoneID
		}
		certificateARN := strings.TrimSpace(plan.CertificateARN)
		if certificateARN == "" && plan.PublicBaseDomain != "" {
			cert, err := client.EnsureCertificate(ctx, CertificateSpec{
				DomainName:       plan.PublicBaseDomain,
				AlternativeNames: []string{"*." + plan.PublicBaseDomain},
				HostedZoneID:     zoneID,
				Tags:             tags,
			})
			if err != nil {
				return err
			}
			add(cert.Action)
			certificateARN = cert.CertificateARN
		}
		sgIngress := []SecurityGroupRule{{
			Protocol:    "tcp",
			FromPort:    80,
			ToPort:      80,
			CIDRs:       []string{"0.0.0.0/0"},
			Description: "allow public HTTP",
		}}
		if certificateARN != "" {
			sgIngress = append(sgIngress, SecurityGroupRule{
				Protocol:    "tcp",
				FromPort:    443,
				ToPort:      443,
				CIDRs:       []string{"0.0.0.0/0"},
				Description: "allow public HTTPS",
			})
		}
		sg, err := client.EnsureLoadBalancerSecurityGroup(ctx, LoadBalancerSecurityGroupSpec{
			Name:        plan.NamePrefix + "-public-lb",
			Description: "Skiff managed public load balancer",
			VPCID:       network.VPCID,
			Ingress:     sgIngress,
			Egress:      []SecurityGroupRule{allowAllEgressRule()},
			Tags:        tags,
		})
		if err != nil {
			return err
		}
		add(sg.Action)
		lb, err := client.EnsureLoadBalancer(ctx, LoadBalancerSpec{
			Name:             plan.PublicLBName,
			Internal:         false,
			SecurityGroupIDs: []string{sg.GroupID},
			SubnetIDs:        append([]string(nil), network.PublicSubnetIDs...),
			Tags:             tags,
		})
		if err != nil {
			return err
		}
		add(lb.Action)
		httpAction := "fixed-response"
		if certificateARN != "" {
			httpAction = "redirect"
		}
		http, err := client.EnsureListener(ctx, ListenerSpec{
			Name:             plan.NamePrefix + "-public-http",
			LoadBalancerARN:  lb.ARN,
			Port:             80,
			Protocol:         "HTTP",
			DefaultAction:    httpAction,
			RedirectPort:     443,
			RedirectProtocol: "HTTPS",
		})
		if err != nil {
			return err
		}
		add(http.Action)
		httpsARN := ""
		if certificateARN != "" {
			https, err := client.EnsureListener(ctx, ListenerSpec{
				Name:            plan.NamePrefix + "-public-https",
				LoadBalancerARN: lb.ARN,
				Port:            443,
				Protocol:        "HTTPS",
				CertificateARN:  certificateARN,
				DefaultAction:   "fixed-response",
			})
			if err != nil {
				return err
			}
			add(https.Action)
			httpsARN = https.ARN
		}
		if plan.PublicBaseDomain != "" {
			for _, name := range []string{plan.PublicBaseDomain, "*." + plan.PublicBaseDomain} {
				action, err := client.EnsureDNSAlias(ctx, DNSAliasSpec{
					Name:                 name,
					HostedZoneID:         zoneID,
					TargetDNSName:        lb.DNSName,
					TargetHostedZoneID:   lb.HostedZoneID,
					EvaluateTargetHealth: true,
				})
				if err != nil {
					return err
				}
				add(action)
			}
		}
		dnsName := lb.DNSName
		if plan.PublicBaseDomain != "" {
			dnsName = plan.PublicBaseDomain
		}
		root.Ingress = &EnvironmentIngress{
			Type:                IngressPublic,
			Host:                plan.PublicBaseDomain,
			BaseDomain:          plan.PublicBaseDomain,
			DefaultHostTemplate: plan.DefaultHostTemplate,
			DomainName:          plan.DomainName,
			Route53ZoneID:       zoneID,
			LoadBalancer: &EnvironmentLoadBalancerDefaults{
				ARN:              lb.ARN,
				DNSName:          dnsName,
				ProviderDNSName:  lb.DNSName,
				HostedZoneID:     lb.HostedZoneID,
				SecurityGroupID:  sg.GroupID,
				HTTPListenerARN:  http.ARN,
				HTTPSListenerARN: httpsARN,
				CertificateARN:   certificateARN,
			},
		}
	default:
		return fmt.Errorf("unsupported ingress type %q", root.Ingress.Type)
	}
	return nil
}

func allowAllEgressRule() SecurityGroupRule {
	return SecurityGroupRule{
		Protocol:    "-1",
		FromPort:    0,
		ToPort:      0,
		CIDRs:       []string{"0.0.0.0/0"},
		Description: "allow load balancer egress",
	}
}

func validateManagedNetworkResult(result *ManagedNetworkResult) error {
	if result == nil {
		return errors.New("managed network result is required")
	}
	if strings.TrimSpace(result.VPCID) == "" {
		return errors.New("managed network result missing VPC ID")
	}
	if len(result.PublicSubnetIDs) == 0 {
		return errors.New("managed network result missing public subnet IDs")
	}
	if len(result.PrivateSubnetIDs) == 0 {
		return errors.New("managed network result missing private subnet IDs")
	}
	return nil
}

func tagsForPlan(plan *AWSPlan) map[string]string {
	if plan == nil {
		return nil
	}
	tags := tagsForEnv(plan.Env)
	if plan.RootConfig.EnvironmentClass != "" {
		tags["skiff.dev/environment-class"] = plan.RootConfig.EnvironmentClass
	}
	if plan.CompanySlug != "" {
		tags["skiff.dev/company"] = plan.CompanySlug
	}
	return tags
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

func roleSpecsForPlan(plan *AWSPlan) []IAMRoleSpec {
	if plan == nil {
		return nil
	}
	specs := make([]IAMRoleSpec, 0, len(plan.IAMPolicies))
	for _, purpose := range sortedPolicyNames(plan.IAMPolicies) {
		specs = append(specs, roleSpec(purpose, plan.RootConfig.Roles[purpose], plan.IAMPolicies[purpose], plan.Env))
	}
	return specs
}

func terraformTags(tags map[string]string) []terraformTag {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]terraformTag, 0, len(keys))
	for _, key := range keys {
		out = append(out, terraformTag{Key: key, Value: tags[key]})
	}
	return out
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

func cloneEnvironmentRoot(root EnvironmentRoot) EnvironmentRoot {
	out := root
	out.Roles = cloneStringMap(root.Roles)
	if root.Network != nil {
		network := *root.Network
		network.PrivateSubnetIDs = append([]string(nil), root.Network.PrivateSubnetIDs...)
		network.PublicSubnetIDs = append([]string(nil), root.Network.PublicSubnetIDs...)
		out.Network = &network
	}
	if root.Ingress != nil {
		ingress := *root.Ingress
		if root.Ingress.LoadBalancer != nil {
			lb := *root.Ingress.LoadBalancer
			ingress.LoadBalancer = &lb
		}
		out.Ingress = &ingress
	}
	if root.Runner != nil {
		runner := *root.Runner
		out.Runner = &runner
	}
	if root.ReleasePolicy != nil {
		policy := *root.ReleasePolicy
		out.ReleasePolicy = &policy
	}
	return out
}
