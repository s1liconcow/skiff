package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	sdka "github.com/aws/aws-sdk-go-v2/aws"
	sdkconfig "github.com/aws/aws-sdk-go-v2/config"
	sdkcredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/s1liconcow/skiff/internal/bootstrap"
	"github.com/s1liconcow/skiff/internal/state/canonical"
)

type SDKBootstrapClient struct {
	region  string
	acm     *acm.Client
	ec2     *ec2.Client
	elb     *elasticloadbalancingv2.Client
	iam     *iam.Client
	kms     *kms.Client
	route53 *route53.Client
	s3      *s3.Client
}

func NewSDKBootstrapClient(ctx context.Context, cfg Config) (bootstrap.AWSBootstrapClient, error) {
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
	return &SDKBootstrapClient{
		region:  cfg.Region,
		acm:     acm.NewFromConfig(loaded),
		ec2:     ec2.NewFromConfig(loaded),
		elb:     elasticloadbalancingv2.NewFromConfig(loaded),
		iam:     iam.NewFromConfig(loaded),
		kms:     kms.NewFromConfig(loaded),
		route53: route53.NewFromConfig(loaded),
		s3:      s3.NewFromConfig(loaded),
	}, nil
}

func (c *SDKBootstrapClient) EnsureKMSKey(ctx context.Context, spec bootstrap.KMSKeySpec) (bootstrap.ApplyAction, error) {
	if out, err := c.kms.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: sdka.String(spec.Alias)}); err == nil {
		keyID := sdka.ToString(out.KeyMetadata.KeyId)
		_, _ = c.kms.EnableKeyRotation(ctx, &kms.EnableKeyRotationInput{KeyId: sdka.String(keyID)})
		return bootstrap.ApplyAction{Kind: "kms-key", Name: spec.Alias, Action: "unchanged", ProviderID: sdka.ToString(out.KeyMetadata.Arn)}, nil
	} else if !sdkNotFound(err) {
		return bootstrap.ApplyAction{}, err
	}
	out, err := c.kms.CreateKey(ctx, &kms.CreateKeyInput{
		Description: sdka.String(spec.Description),
		Tags:        kmsTags(spec.Tags),
	})
	if err != nil {
		return bootstrap.ApplyAction{}, err
	}
	keyID := sdka.ToString(out.KeyMetadata.KeyId)
	if spec.EnableKeyRotation {
		if _, err := c.kms.EnableKeyRotation(ctx, &kms.EnableKeyRotationInput{KeyId: sdka.String(keyID)}); err != nil {
			return bootstrap.ApplyAction{}, err
		}
	}
	if _, err := c.kms.CreateAlias(ctx, &kms.CreateAliasInput{AliasName: sdka.String(spec.Alias), TargetKeyId: sdka.String(keyID)}); err != nil && !sdkAlreadyExists(err) {
		return bootstrap.ApplyAction{}, err
	}
	return bootstrap.ApplyAction{Kind: "kms-key", Name: spec.Alias, Action: "created", ProviderID: sdka.ToString(out.KeyMetadata.Arn)}, nil
}

func (c *SDKBootstrapClient) EnsureStateBucket(ctx context.Context, spec bootstrap.StateBucketSpec) (bootstrap.ApplyAction, error) {
	action := "unchanged"
	if _, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: sdka.String(spec.Name)}); err != nil {
		if !sdkNotFound(err) {
			return bootstrap.ApplyAction{}, err
		}
		input := &s3.CreateBucketInput{Bucket: sdka.String(spec.Name)}
		if spec.Region != "us-east-1" {
			input.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
				LocationConstraint: s3types.BucketLocationConstraint(spec.Region),
			}
		}
		if _, err := c.s3.CreateBucket(ctx, input); err != nil && !sdkAlreadyExists(err) {
			return bootstrap.ApplyAction{}, err
		}
		action = "created"
	}
	if spec.Versioning {
		if _, err := c.s3.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
			Bucket: sdka.String(spec.Name),
			VersioningConfiguration: &s3types.VersioningConfiguration{
				Status: s3types.BucketVersioningStatusEnabled,
			},
		}); err != nil {
			return bootstrap.ApplyAction{}, err
		}
	}
	if spec.PublicAccessBlock {
		if _, err := c.s3.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
			Bucket: sdka.String(spec.Name),
			PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
				BlockPublicAcls:       sdka.Bool(true),
				BlockPublicPolicy:     sdka.Bool(true),
				IgnorePublicAcls:      sdka.Bool(true),
				RestrictPublicBuckets: sdka.Bool(true),
			},
		}); err != nil {
			return bootstrap.ApplyAction{}, err
		}
	}
	if spec.Encryption == "aws:kms" {
		if _, err := c.s3.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
			Bucket: sdka.String(spec.Name),
			ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
				Rules: []s3types.ServerSideEncryptionRule{{
					ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
						KMSMasterKeyID: sdka.String(spec.KMSAlias),
						SSEAlgorithm:   s3types.ServerSideEncryptionAwsKms,
					},
				}},
			},
		}); err != nil {
			return bootstrap.ApplyAction{}, err
		}
	}
	return bootstrap.ApplyAction{Kind: "s3-bucket", Name: spec.Name, Action: action, ProviderID: "s3://" + spec.Name}, nil
}

func (c *SDKBootstrapClient) EnsureIAMRole(ctx context.Context, spec bootstrap.IAMRoleSpec) (bootstrap.ApplyAction, error) {
	if out, err := c.iam.GetRole(ctx, &iam.GetRoleInput{RoleName: sdka.String(spec.Name)}); err == nil {
		if err := c.putRolePolicy(ctx, spec); err != nil {
			return bootstrap.ApplyAction{}, err
		}
		_, _ = c.iam.TagRole(ctx, &iam.TagRoleInput{RoleName: sdka.String(spec.Name), Tags: iamTags(spec.Tags)})
		return bootstrap.ApplyAction{Kind: "iam-role", Name: spec.Name, Action: "unchanged", ProviderID: sdka.ToString(out.Role.Arn)}, nil
	} else if !sdkNotFound(err) {
		return bootstrap.ApplyAction{}, err
	}
	out, err := c.iam.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 sdka.String(spec.Name),
		AssumeRolePolicyDocument: sdka.String(bootstrapTrustPolicy()),
		Tags:                     iamTags(spec.Tags),
	})
	if err != nil && !sdkAlreadyExists(err) {
		return bootstrap.ApplyAction{}, err
	}
	if err := c.putRolePolicy(ctx, spec); err != nil {
		return bootstrap.ApplyAction{}, err
	}
	arn := ""
	if out != nil && out.Role != nil {
		arn = sdka.ToString(out.Role.Arn)
	}
	if arn == "" {
		found, err := c.iam.GetRole(ctx, &iam.GetRoleInput{RoleName: sdka.String(spec.Name)})
		if err != nil {
			return bootstrap.ApplyAction{}, err
		}
		arn = sdka.ToString(found.Role.Arn)
	}
	return bootstrap.ApplyAction{Kind: "iam-role", Name: spec.Name, Action: "created", ProviderID: arn}, nil
}

func (c *SDKBootstrapClient) putRolePolicy(ctx context.Context, spec bootstrap.IAMRoleSpec) error {
	body, err := bootstrap.PolicyJSON(spec.Policy)
	if err != nil {
		return err
	}
	_, err = c.iam.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       sdka.String(spec.Name),
		PolicyName:     sdka.String(spec.PolicyName),
		PolicyDocument: sdka.String(body),
	})
	return err
}

func (c *SDKBootstrapClient) PutBucketPolicy(ctx context.Context, spec bootstrap.BucketPolicySpec) (bootstrap.ApplyAction, error) {
	body, err := bootstrap.PolicyJSON(spec.Policy)
	if err != nil {
		return bootstrap.ApplyAction{}, err
	}
	if _, err := c.s3.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: sdka.String(spec.Bucket), Policy: sdka.String(body)}); err != nil {
		return bootstrap.ApplyAction{}, err
	}
	return bootstrap.ApplyAction{Kind: "s3-bucket-policy", Name: spec.Bucket, Action: "put", ProviderID: "s3://" + spec.Bucket}, nil
}

func (c *SDKBootstrapClient) EnsureManagedNetwork(ctx context.Context, spec bootstrap.ManagedNetworkSpec) (*bootstrap.ManagedNetworkResult, error) {
	var actions []bootstrap.ApplyAction
	vpcID, action, err := c.ensureVPC(ctx, spec)
	if err != nil {
		return nil, err
	}
	actions = append(actions, action)
	zones, err := c.availabilityZones(ctx, len(spec.PublicSubnetCIDRs))
	if err != nil {
		return nil, err
	}
	publicSubnets, subnetActions, err := c.ensureSubnets(ctx, vpcID, spec.NamePrefix+"-public", spec.PublicSubnetCIDRs, zones, true, spec.Tags)
	if err != nil {
		return nil, err
	}
	actions = append(actions, subnetActions...)
	privateSubnets, subnetActions, err := c.ensureSubnets(ctx, vpcID, spec.NamePrefix+"-private", spec.PrivateSubnetCIDRs, zones, false, spec.Tags)
	if err != nil {
		return nil, err
	}
	actions = append(actions, subnetActions...)
	igwID, action, err := c.ensureInternetGateway(ctx, vpcID, spec.NamePrefix, spec.Tags)
	if err != nil {
		return nil, err
	}
	actions = append(actions, action)
	natID, action, err := c.ensureNATGateway(ctx, spec.NamePrefix, publicSubnets[0], igwID, spec.Tags)
	if err != nil {
		return nil, err
	}
	actions = append(actions, action)
	routeActions, err := c.ensureRouteTables(ctx, spec.NamePrefix, vpcID, igwID, natID, publicSubnets, privateSubnets, spec.Tags)
	if err != nil {
		return nil, err
	}
	actions = append(actions, routeActions...)
	return &bootstrap.ManagedNetworkResult{
		Actions:          actions,
		VPCID:            vpcID,
		PublicSubnetIDs:  publicSubnets,
		PrivateSubnetIDs: privateSubnets,
	}, nil
}

func (c *SDKBootstrapClient) ResolveHostedZone(ctx context.Context, spec bootstrap.HostedZoneSpec) (*bootstrap.HostedZoneResult, error) {
	if spec.HostedZoneID != "" {
		id := normalizeHostedZoneID(spec.HostedZoneID)
		return &bootstrap.HostedZoneResult{
			Action:       bootstrap.ApplyAction{Kind: "hosted-zone", Name: id, Action: "unchanged", ProviderID: id},
			HostedZoneID: id,
		}, nil
	}
	if strings.TrimSpace(spec.DomainName) == "" {
		return nil, errors.New("domain name or hosted zone id is required for public DNS")
	}
	resp, err := c.route53.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{
		DNSName:  sdka.String(trailingDot(spec.DomainName)),
		MaxItems: sdka.Int32(1),
	})
	if err != nil {
		return nil, err
	}
	for _, zone := range resp.HostedZones {
		name := trailingDot(sdka.ToString(zone.Name))
		if name == trailingDot(spec.DomainName) && !zone.Config.PrivateZone {
			id := normalizeHostedZoneID(sdka.ToString(zone.Id))
			return &bootstrap.HostedZoneResult{
				Action:       bootstrap.ApplyAction{Kind: "hosted-zone", Name: spec.DomainName, Action: "unchanged", ProviderID: id},
				HostedZoneID: id,
				Name:         name,
			}, nil
		}
	}
	return nil, fmt.Errorf("route53 public hosted zone not found for %s", spec.DomainName)
}

func (c *SDKBootstrapClient) EnsureCertificate(ctx context.Context, spec bootstrap.CertificateSpec) (*bootstrap.CertificateResult, error) {
	arn, found, err := c.findCertificate(ctx, spec.DomainName)
	if err != nil {
		return nil, err
	}
	action := "unchanged"
	if !found {
		resp, err := c.acm.RequestCertificate(ctx, &acm.RequestCertificateInput{
			DomainName:              sdka.String(spec.DomainName),
			SubjectAlternativeNames: spec.AlternativeNames,
			ValidationMethod:        acmtypes.ValidationMethodDns,
			IdempotencyToken:        sdka.String(idempotencyToken(spec.DomainName)),
			Tags:                    acmTags(spec.Tags),
		})
		if err != nil {
			return nil, err
		}
		arn = sdka.ToString(resp.CertificateArn)
		action = "created"
	}
	if err := c.upsertCertificateValidationRecords(ctx, arn, spec.HostedZoneID); err != nil {
		return nil, err
	}
	waiter := acm.NewCertificateValidatedWaiter(c.acm)
	if err := waiter.Wait(ctx, &acm.DescribeCertificateInput{CertificateArn: sdka.String(arn)}, 45*time.Minute); err != nil {
		return nil, err
	}
	return &bootstrap.CertificateResult{
		Action:         bootstrap.ApplyAction{Kind: "certificate", Name: spec.DomainName, Action: action, ProviderID: arn},
		CertificateARN: arn,
	}, nil
}

func (c *SDKBootstrapClient) EnsureLoadBalancerSecurityGroup(ctx context.Context, spec bootstrap.LoadBalancerSecurityGroupSpec) (*bootstrap.SecurityGroupResult, error) {
	groupID, found, err := c.findSecurityGroup(ctx, spec.VPCID, spec.Name)
	if err != nil {
		return nil, err
	}
	action := "unchanged"
	if !found {
		out, err := c.ec2.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
			GroupName:   sdka.String(spec.Name),
			Description: sdka.String(spec.Description),
			VpcId:       sdka.String(spec.VPCID),
			TagSpecifications: []ec2types.TagSpecification{{
				ResourceType: ec2types.ResourceTypeSecurityGroup,
				Tags:         ec2Tags(namedTags(spec.Tags, spec.Name)),
			}},
		})
		if err != nil && !sdkAlreadyExists(err) {
			return nil, err
		}
		if out != nil {
			groupID = sdka.ToString(out.GroupId)
		}
		if groupID == "" {
			groupID, _, err = c.findSecurityGroup(ctx, spec.VPCID, spec.Name)
			if err != nil {
				return nil, err
			}
		}
		action = "created"
	}
	for _, rule := range spec.Ingress {
		if err := c.authorizeIngress(ctx, groupID, rule); err != nil {
			return nil, err
		}
	}
	for _, rule := range spec.Egress {
		if err := c.authorizeEgress(ctx, groupID, rule); err != nil {
			return nil, err
		}
	}
	return &bootstrap.SecurityGroupResult{
		Action:  bootstrap.ApplyAction{Kind: "security-group", Name: spec.Name, Action: action, ProviderID: groupID},
		GroupID: groupID,
	}, nil
}

func (c *SDKBootstrapClient) EnsureLoadBalancer(ctx context.Context, spec bootstrap.LoadBalancerSpec) (*bootstrap.LoadBalancerResult, error) {
	if lb, ok, err := c.findLoadBalancer(ctx, spec.Name); err != nil {
		return nil, err
	} else if ok {
		_, _ = c.elb.AddTags(ctx, &elasticloadbalancingv2.AddTagsInput{ResourceArns: []string{sdka.ToString(lb.LoadBalancerArn)}, Tags: elbTags(namedTags(spec.Tags, spec.Name))})
		return &bootstrap.LoadBalancerResult{
			Action:       bootstrap.ApplyAction{Kind: "load-balancer", Name: spec.Name, Action: "unchanged", ProviderID: sdka.ToString(lb.LoadBalancerArn)},
			ARN:          sdka.ToString(lb.LoadBalancerArn),
			DNSName:      sdka.ToString(lb.DNSName),
			HostedZoneID: sdka.ToString(lb.CanonicalHostedZoneId),
		}, nil
	}
	scheme := elbv2types.LoadBalancerSchemeEnumInternetFacing
	if spec.Internal {
		scheme = elbv2types.LoadBalancerSchemeEnumInternal
	}
	out, err := c.elb.CreateLoadBalancer(ctx, &elasticloadbalancingv2.CreateLoadBalancerInput{
		Name:           sdka.String(spec.Name),
		Type:           elbv2types.LoadBalancerTypeEnumApplication,
		Scheme:         scheme,
		SecurityGroups: spec.SecurityGroupIDs,
		Subnets:        spec.SubnetIDs,
		Tags:           elbTags(namedTags(spec.Tags, spec.Name)),
	})
	if err != nil && !sdkAlreadyExists(err) {
		return nil, err
	}
	lb := elbv2types.LoadBalancer{}
	if out != nil && len(out.LoadBalancers) > 0 {
		lb = out.LoadBalancers[0]
	} else {
		found, ok, err := c.findLoadBalancer(ctx, spec.Name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("load balancer %s was not found after create", spec.Name)
		}
		lb = found
	}
	return &bootstrap.LoadBalancerResult{
		Action:       bootstrap.ApplyAction{Kind: "load-balancer", Name: spec.Name, Action: "created", ProviderID: sdka.ToString(lb.LoadBalancerArn)},
		ARN:          sdka.ToString(lb.LoadBalancerArn),
		DNSName:      sdka.ToString(lb.DNSName),
		HostedZoneID: sdka.ToString(lb.CanonicalHostedZoneId),
	}, nil
}

func (c *SDKBootstrapClient) EnsureListener(ctx context.Context, spec bootstrap.ListenerSpec) (*bootstrap.ListenerResult, error) {
	if arn, ok, err := c.findListener(ctx, spec.LoadBalancerARN, spec.Port, spec.Protocol); err != nil {
		return nil, err
	} else if ok {
		return &bootstrap.ListenerResult{Action: bootstrap.ApplyAction{Kind: "listener", Name: spec.Name, Action: "unchanged", ProviderID: arn}, ARN: arn}, nil
	}
	input := &elasticloadbalancingv2.CreateListenerInput{
		LoadBalancerArn: sdka.String(spec.LoadBalancerARN),
		Port:            sdka.Int32(spec.Port),
		Protocol:        elbv2types.ProtocolEnum(strings.ToUpper(spec.Protocol)),
		DefaultActions:  listenerActions(spec),
	}
	if strings.EqualFold(spec.Protocol, "https") {
		input.SslPolicy = sdka.String("ELBSecurityPolicy-TLS13-1-2-2021-06")
		input.Certificates = []elbv2types.Certificate{{CertificateArn: sdka.String(spec.CertificateARN)}}
	}
	out, err := c.elb.CreateListener(ctx, input)
	if err != nil && !sdkAlreadyExists(err) {
		return nil, err
	}
	arn := ""
	if out != nil && len(out.Listeners) > 0 {
		arn = sdka.ToString(out.Listeners[0].ListenerArn)
	}
	if arn == "" {
		var ok bool
		arn, ok, err = c.findListener(ctx, spec.LoadBalancerARN, spec.Port, spec.Protocol)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("listener %s was not found after create", spec.Name)
		}
	}
	return &bootstrap.ListenerResult{Action: bootstrap.ApplyAction{Kind: "listener", Name: spec.Name, Action: "created", ProviderID: arn}, ARN: arn}, nil
}

func (c *SDKBootstrapClient) EnsureDNSAlias(ctx context.Context, spec bootstrap.DNSAliasSpec) (bootstrap.ApplyAction, error) {
	if spec.HostedZoneID == "" {
		return bootstrap.ApplyAction{}, errors.New("hosted zone id is required for DNS alias")
	}
	if spec.TargetDNSName == "" || spec.TargetHostedZoneID == "" {
		return bootstrap.ApplyAction{}, errors.New("load balancer DNS name and hosted zone id are required for DNS alias")
	}
	_, err := c.route53.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: sdka.String(normalizeHostedZoneID(spec.HostedZoneID)),
		ChangeBatch: &route53types.ChangeBatch{
			Changes: []route53types.Change{{
				Action: route53types.ChangeActionUpsert,
				ResourceRecordSet: &route53types.ResourceRecordSet{
					Name: sdka.String(trailingDot(spec.Name)),
					Type: route53types.RRTypeA,
					AliasTarget: &route53types.AliasTarget{
						DNSName:              sdka.String(spec.TargetDNSName),
						HostedZoneId:         sdka.String(spec.TargetHostedZoneID),
						EvaluateTargetHealth: spec.EvaluateTargetHealth,
					},
				},
			}},
		},
	})
	if err != nil {
		return bootstrap.ApplyAction{}, err
	}
	return bootstrap.ApplyAction{Kind: "dns-record", Name: spec.Name, Action: "upserted", ProviderID: normalizeHostedZoneID(spec.HostedZoneID) + "/" + spec.Name}, nil
}

func (c *SDKBootstrapClient) PutEnvironmentRoot(ctx context.Context, spec bootstrap.EnvironmentRootSpec) (bootstrap.ApplyAction, error) {
	body, err := canonical.Marshal(spec.Config)
	if err != nil {
		return bootstrap.ApplyAction{}, err
	}
	bucket := strings.TrimPrefix(spec.Config.StateBucket, "s3://")
	if before, _, ok := strings.Cut(bucket, "/"); ok {
		bucket = before
	}
	if bucket == "" {
		return bootstrap.ApplyAction{}, errors.New("environment root state bucket is required")
	}
	_, err = c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      sdka.String(bucket),
		Key:         sdka.String(spec.Key),
		Body:        strings.NewReader(string(body)),
		ContentType: sdka.String("application/json"),
	})
	if err != nil {
		return bootstrap.ApplyAction{}, err
	}
	return bootstrap.ApplyAction{Kind: "environment-root", Name: spec.Key, Action: "put", ProviderID: "s3://" + bucket + "/" + spec.Key}, nil
}

func (c *SDKBootstrapClient) ensureVPC(ctx context.Context, spec bootstrap.ManagedNetworkSpec) (string, bootstrap.ApplyAction, error) {
	if id, ok, err := c.findVPC(ctx, spec.NamePrefix); err != nil {
		return "", bootstrap.ApplyAction{}, err
	} else if ok {
		return id, bootstrap.ApplyAction{Kind: "vpc", Name: spec.NamePrefix, Action: "unchanged", ProviderID: id}, nil
	}
	out, err := c.ec2.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: sdka.String(spec.VPCCIDR),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeVpc,
			Tags:         ec2Tags(namedTags(spec.Tags, spec.NamePrefix)),
		}},
	})
	if err != nil {
		return "", bootstrap.ApplyAction{}, err
	}
	id := sdka.ToString(out.Vpc.VpcId)
	_, _ = c.ec2.ModifyVpcAttribute(ctx, &ec2.ModifyVpcAttributeInput{VpcId: sdka.String(id), EnableDnsSupport: &ec2types.AttributeBooleanValue{Value: sdka.Bool(true)}})
	_, _ = c.ec2.ModifyVpcAttribute(ctx, &ec2.ModifyVpcAttributeInput{VpcId: sdka.String(id), EnableDnsHostnames: &ec2types.AttributeBooleanValue{Value: sdka.Bool(true)}})
	return id, bootstrap.ApplyAction{Kind: "vpc", Name: spec.NamePrefix, Action: "created", ProviderID: id}, nil
}

func (c *SDKBootstrapClient) findVPC(ctx context.Context, name string) (string, bool, error) {
	resp, err := c.ec2.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{Filters: []ec2types.Filter{{Name: sdka.String("tag:Name"), Values: []string{name}}}})
	if err != nil {
		return "", false, err
	}
	for _, vpc := range resp.Vpcs {
		return sdka.ToString(vpc.VpcId), true, nil
	}
	return "", false, nil
}

func (c *SDKBootstrapClient) availabilityZones(ctx context.Context, count int) ([]string, error) {
	resp, err := c.ec2.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{
		Filters: []ec2types.Filter{{Name: sdka.String("state"), Values: []string{"available"}}},
	})
	if err != nil {
		return nil, err
	}
	var zones []string
	for _, zone := range resp.AvailabilityZones {
		if name := sdka.ToString(zone.ZoneName); name != "" {
			zones = append(zones, name)
		}
	}
	sort.Strings(zones)
	if len(zones) < count {
		return nil, fmt.Errorf("region %s has %d available zones, need %d", c.region, len(zones), count)
	}
	return zones[:count], nil
}

func (c *SDKBootstrapClient) ensureSubnets(ctx context.Context, vpcID, prefix string, cidrs, zones []string, public bool, tags map[string]string) ([]string, []bootstrap.ApplyAction, error) {
	var ids []string
	var actions []bootstrap.ApplyAction
	for i, cidr := range cidrs {
		name := fmt.Sprintf("%s-%d", prefix, i+1)
		id, found, err := c.findSubnet(ctx, vpcID, name)
		if err != nil {
			return nil, nil, err
		}
		action := "unchanged"
		if !found {
			out, err := c.ec2.CreateSubnet(ctx, &ec2.CreateSubnetInput{
				VpcId:            sdka.String(vpcID),
				CidrBlock:        sdka.String(cidr),
				AvailabilityZone: sdka.String(zones[i]),
				TagSpecifications: []ec2types.TagSpecification{{
					ResourceType: ec2types.ResourceTypeSubnet,
					Tags:         ec2Tags(namedTags(tags, name)),
				}},
			})
			if err != nil {
				return nil, nil, err
			}
			id = sdka.ToString(out.Subnet.SubnetId)
			action = "created"
		}
		if public {
			_, _ = c.ec2.ModifySubnetAttribute(ctx, &ec2.ModifySubnetAttributeInput{SubnetId: sdka.String(id), MapPublicIpOnLaunch: &ec2types.AttributeBooleanValue{Value: sdka.Bool(true)}})
		}
		ids = append(ids, id)
		actions = append(actions, bootstrap.ApplyAction{Kind: "subnet", Name: name, Action: action, ProviderID: id})
	}
	return ids, actions, nil
}

func (c *SDKBootstrapClient) findSubnet(ctx context.Context, vpcID, name string) (string, bool, error) {
	resp, err := c.ec2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{Filters: []ec2types.Filter{
		{Name: sdka.String("vpc-id"), Values: []string{vpcID}},
		{Name: sdka.String("tag:Name"), Values: []string{name}},
	}})
	if err != nil {
		return "", false, err
	}
	for _, subnet := range resp.Subnets {
		return sdka.ToString(subnet.SubnetId), true, nil
	}
	return "", false, nil
}

func (c *SDKBootstrapClient) ensureInternetGateway(ctx context.Context, vpcID, name string, tags map[string]string) (string, bootstrap.ApplyAction, error) {
	if id, ok, err := c.findInternetGateway(ctx, name); err != nil {
		return "", bootstrap.ApplyAction{}, err
	} else if ok {
		if err := c.attachInternetGateway(ctx, id, vpcID); err != nil {
			return "", bootstrap.ApplyAction{}, err
		}
		return id, bootstrap.ApplyAction{Kind: "internet-gateway", Name: name, Action: "unchanged", ProviderID: id}, nil
	}
	out, err := c.ec2.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInternetGateway,
			Tags:         ec2Tags(namedTags(tags, name)),
		}},
	})
	if err != nil {
		return "", bootstrap.ApplyAction{}, err
	}
	id := sdka.ToString(out.InternetGateway.InternetGatewayId)
	if err := c.attachInternetGateway(ctx, id, vpcID); err != nil {
		return "", bootstrap.ApplyAction{}, err
	}
	return id, bootstrap.ApplyAction{Kind: "internet-gateway", Name: name, Action: "created", ProviderID: id}, nil
}

func (c *SDKBootstrapClient) findInternetGateway(ctx context.Context, name string) (string, bool, error) {
	resp, err := c.ec2.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{Filters: []ec2types.Filter{{Name: sdka.String("tag:Name"), Values: []string{name}}}})
	if err != nil {
		return "", false, err
	}
	for _, gateway := range resp.InternetGateways {
		return sdka.ToString(gateway.InternetGatewayId), true, nil
	}
	return "", false, nil
}

func (c *SDKBootstrapClient) attachInternetGateway(ctx context.Context, gatewayID, vpcID string) error {
	_, err := c.ec2.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{InternetGatewayId: sdka.String(gatewayID), VpcId: sdka.String(vpcID)})
	if err != nil && !sdkAlreadyExists(err) && !sdkDuplicate(err) {
		return err
	}
	return nil
}

func (c *SDKBootstrapClient) ensureNATGateway(ctx context.Context, name, subnetID, igwID string, tags map[string]string) (string, bootstrap.ApplyAction, error) {
	if id, ok, err := c.findNATGateway(ctx, name); err != nil {
		return "", bootstrap.ApplyAction{}, err
	} else if ok {
		return id, bootstrap.ApplyAction{Kind: "nat-gateway", Name: name, Action: "unchanged", ProviderID: id}, nil
	}
	allocationID, err := c.ensureEIP(ctx, name+"-nat", tags)
	if err != nil {
		return "", bootstrap.ApplyAction{}, err
	}
	out, err := c.ec2.CreateNatGateway(ctx, &ec2.CreateNatGatewayInput{
		AllocationId: sdka.String(allocationID),
		SubnetId:     sdka.String(subnetID),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeNatgateway,
			Tags:         ec2Tags(namedTags(tags, name)),
		}},
	})
	if err != nil && !sdkAlreadyExists(err) {
		return "", bootstrap.ApplyAction{}, err
	}
	if out == nil || out.NatGateway == nil {
		id, ok, err := c.findNATGateway(ctx, name)
		if err != nil {
			return "", bootstrap.ApplyAction{}, err
		}
		if !ok {
			return "", bootstrap.ApplyAction{}, fmt.Errorf("NAT gateway %s was not found after create", name)
		}
		return id, bootstrap.ApplyAction{Kind: "nat-gateway", Name: name, Action: "created", ProviderID: id}, nil
	}
	return sdka.ToString(out.NatGateway.NatGatewayId), bootstrap.ApplyAction{Kind: "nat-gateway", Name: name, Action: "created", ProviderID: sdka.ToString(out.NatGateway.NatGatewayId)}, nil
}

func (c *SDKBootstrapClient) ensureEIP(ctx context.Context, name string, tags map[string]string) (string, error) {
	resp, err := c.ec2.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{Filters: []ec2types.Filter{{Name: sdka.String("tag:Name"), Values: []string{name}}}})
	if err != nil {
		return "", err
	}
	for _, addr := range resp.Addresses {
		if id := sdka.ToString(addr.AllocationId); id != "" {
			return id, nil
		}
	}
	out, err := c.ec2.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: ec2types.DomainTypeVpc,
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeElasticIp,
			Tags:         ec2Tags(namedTags(tags, name)),
		}},
	})
	if err != nil {
		return "", err
	}
	return sdka.ToString(out.AllocationId), nil
}

func (c *SDKBootstrapClient) findNATGateway(ctx context.Context, name string) (string, bool, error) {
	resp, err := c.ec2.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{Filter: []ec2types.Filter{{Name: sdka.String("tag:Name"), Values: []string{name}}}})
	if err != nil {
		return "", false, err
	}
	for _, gateway := range resp.NatGateways {
		state := gateway.State
		if state == ec2types.NatGatewayStateDeleted || state == ec2types.NatGatewayStateDeleting || state == ec2types.NatGatewayStateFailed {
			continue
		}
		return sdka.ToString(gateway.NatGatewayId), true, nil
	}
	return "", false, nil
}

func (c *SDKBootstrapClient) ensureRouteTables(ctx context.Context, prefix, vpcID, igwID, natID string, publicSubnets, privateSubnets []string, tags map[string]string) ([]bootstrap.ApplyAction, error) {
	publicRT, publicCreated, err := c.ensureRouteTable(ctx, vpcID, prefix+"-public", tags)
	if err != nil {
		return nil, err
	}
	privateRT, privateCreated, err := c.ensureRouteTable(ctx, vpcID, prefix+"-private", tags)
	if err != nil {
		return nil, err
	}
	_, err = c.ec2.CreateRoute(ctx, &ec2.CreateRouteInput{RouteTableId: sdka.String(publicRT), DestinationCidrBlock: sdka.String("0.0.0.0/0"), GatewayId: sdka.String(igwID)})
	if err != nil && !sdkAlreadyExists(err) {
		return nil, err
	}
	_, err = c.ec2.CreateRoute(ctx, &ec2.CreateRouteInput{RouteTableId: sdka.String(privateRT), DestinationCidrBlock: sdka.String("0.0.0.0/0"), NatGatewayId: sdka.String(natID)})
	if err != nil && !sdkAlreadyExists(err) {
		return nil, err
	}
	for _, subnetID := range publicSubnets {
		if err := c.ensureRouteTableAssociation(ctx, publicRT, subnetID); err != nil {
			return nil, err
		}
	}
	for _, subnetID := range privateSubnets {
		if err := c.ensureRouteTableAssociation(ctx, privateRT, subnetID); err != nil {
			return nil, err
		}
	}
	publicAction := "unchanged"
	if publicCreated {
		publicAction = "created"
	}
	privateAction := "unchanged"
	if privateCreated {
		privateAction = "created"
	}
	return []bootstrap.ApplyAction{
		{Kind: "route-table", Name: prefix + "-public", Action: publicAction, ProviderID: publicRT},
		{Kind: "route-table", Name: prefix + "-private", Action: privateAction, ProviderID: privateRT},
	}, nil
}

func (c *SDKBootstrapClient) ensureRouteTable(ctx context.Context, vpcID, name string, tags map[string]string) (string, bool, error) {
	resp, err := c.ec2.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{Filters: []ec2types.Filter{
		{Name: sdka.String("vpc-id"), Values: []string{vpcID}},
		{Name: sdka.String("tag:Name"), Values: []string{name}},
	}})
	if err != nil {
		return "", false, err
	}
	for _, table := range resp.RouteTables {
		return sdka.ToString(table.RouteTableId), false, nil
	}
	out, err := c.ec2.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{
		VpcId: sdka.String(vpcID),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeRouteTable,
			Tags:         ec2Tags(namedTags(tags, name)),
		}},
	})
	if err != nil {
		return "", false, err
	}
	return sdka.ToString(out.RouteTable.RouteTableId), true, nil
}

func (c *SDKBootstrapClient) ensureRouteTableAssociation(ctx context.Context, routeTableID, subnetID string) error {
	resp, err := c.ec2.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{Filters: []ec2types.Filter{{Name: sdka.String("association.subnet-id"), Values: []string{subnetID}}}})
	if err != nil {
		return err
	}
	if len(resp.RouteTables) > 0 {
		return nil
	}
	_, err = c.ec2.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{RouteTableId: sdka.String(routeTableID), SubnetId: sdka.String(subnetID)})
	if err != nil && !sdkAlreadyExists(err) {
		return err
	}
	return nil
}

func (c *SDKBootstrapClient) findSecurityGroup(ctx context.Context, vpcID, name string) (string, bool, error) {
	resp, err := c.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: []ec2types.Filter{
		{Name: sdka.String("vpc-id"), Values: []string{vpcID}},
		{Name: sdka.String("group-name"), Values: []string{name}},
	}})
	if err != nil {
		return "", false, err
	}
	for _, group := range resp.SecurityGroups {
		return sdka.ToString(group.GroupId), true, nil
	}
	return "", false, nil
}

func (c *SDKBootstrapClient) authorizeIngress(ctx context.Context, groupID string, rule bootstrap.SecurityGroupRule) error {
	_, err := c.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       sdka.String(groupID),
		IpPermissions: []ec2types.IpPermission{ec2Permission(rule)},
	})
	if err != nil && !sdkDuplicate(err) {
		return err
	}
	return nil
}

func (c *SDKBootstrapClient) authorizeEgress(ctx context.Context, groupID string, rule bootstrap.SecurityGroupRule) error {
	_, err := c.ec2.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
		GroupId:       sdka.String(groupID),
		IpPermissions: []ec2types.IpPermission{ec2Permission(rule)},
	})
	if err != nil && !sdkDuplicate(err) {
		return err
	}
	return nil
}

func (c *SDKBootstrapClient) findLoadBalancer(ctx context.Context, name string) (elbv2types.LoadBalancer, bool, error) {
	resp, err := c.elb.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
	if err != nil {
		if sdkNotFound(err) {
			return elbv2types.LoadBalancer{}, false, nil
		}
		return elbv2types.LoadBalancer{}, false, err
	}
	for _, lb := range resp.LoadBalancers {
		return lb, true, nil
	}
	return elbv2types.LoadBalancer{}, false, nil
}

func (c *SDKBootstrapClient) findListener(ctx context.Context, loadBalancerARN string, port int32, protocol string) (string, bool, error) {
	var marker *string
	for {
		resp, err := c.elb.DescribeListeners(ctx, &elasticloadbalancingv2.DescribeListenersInput{LoadBalancerArn: sdka.String(loadBalancerARN), Marker: marker})
		if err != nil {
			return "", false, err
		}
		for _, listener := range resp.Listeners {
			if sdka.ToInt32(listener.Port) == port && strings.EqualFold(string(listener.Protocol), protocol) {
				return sdka.ToString(listener.ListenerArn), true, nil
			}
		}
		marker = resp.NextMarker
		if marker == nil || *marker == "" {
			break
		}
	}
	return "", false, nil
}

func (c *SDKBootstrapClient) findCertificate(ctx context.Context, domain string) (string, bool, error) {
	var token *string
	for {
		resp, err := c.acm.ListCertificates(ctx, &acm.ListCertificatesInput{
			CertificateStatuses: []acmtypes.CertificateStatus{acmtypes.CertificateStatusIssued, acmtypes.CertificateStatusPendingValidation},
			NextToken:           token,
		})
		if err != nil {
			return "", false, err
		}
		for _, cert := range resp.CertificateSummaryList {
			if sdka.ToString(cert.DomainName) == domain {
				return sdka.ToString(cert.CertificateArn), true, nil
			}
		}
		token = resp.NextToken
		if token == nil || *token == "" {
			break
		}
	}
	return "", false, nil
}

func (c *SDKBootstrapClient) upsertCertificateValidationRecords(ctx context.Context, certARN, zoneID string) error {
	resp, err := c.acm.DescribeCertificate(ctx, &acm.DescribeCertificateInput{CertificateArn: sdka.String(certARN)})
	if err != nil {
		return err
	}
	for _, option := range resp.Certificate.DomainValidationOptions {
		if option.ResourceRecord == nil || sdka.ToString(option.ResourceRecord.Name) == "" {
			continue
		}
		_, err := c.route53.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
			HostedZoneId: sdka.String(normalizeHostedZoneID(zoneID)),
			ChangeBatch: &route53types.ChangeBatch{Changes: []route53types.Change{{
				Action: route53types.ChangeActionUpsert,
				ResourceRecordSet: &route53types.ResourceRecordSet{
					Name: sdka.String(sdka.ToString(option.ResourceRecord.Name)),
					Type: route53types.RRType(string(option.ResourceRecord.Type)),
					TTL:  sdka.Int64(60),
					ResourceRecords: []route53types.ResourceRecord{{
						Value: sdka.String(sdka.ToString(option.ResourceRecord.Value)),
					}},
				},
			}}},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func listenerActions(spec bootstrap.ListenerSpec) []elbv2types.Action {
	switch spec.DefaultAction {
	case "redirect":
		return []elbv2types.Action{{
			Type: elbv2types.ActionTypeEnumRedirect,
			RedirectConfig: &elbv2types.RedirectActionConfig{
				Port:       sdka.String(fmt.Sprintf("%d", spec.RedirectPort)),
				Protocol:   sdka.String(spec.RedirectProtocol),
				StatusCode: elbv2types.RedirectActionStatusCodeEnumHttp301,
			},
		}}
	default:
		return []elbv2types.Action{{
			Type: elbv2types.ActionTypeEnumFixedResponse,
			FixedResponseConfig: &elbv2types.FixedResponseActionConfig{
				ContentType: sdka.String("text/plain"),
				MessageBody: sdka.String("skiff"),
				StatusCode:  sdka.String("404"),
			},
		}}
	}
}

func ec2Permission(rule bootstrap.SecurityGroupRule) ec2types.IpPermission {
	permission := ec2types.IpPermission{
		IpProtocol: sdka.String(rule.Protocol),
		FromPort:   sdka.Int32(rule.FromPort),
		ToPort:     sdka.Int32(rule.ToPort),
	}
	for _, cidr := range rule.CIDRs {
		permission.IpRanges = append(permission.IpRanges, ec2types.IpRange{CidrIp: sdka.String(cidr), Description: optionalString(rule.Description)})
	}
	return permission
}

func namedTags(tags map[string]string, name string) map[string]string {
	out := map[string]string{"Name": name}
	for key, value := range tags {
		out[key] = value
	}
	return out
}

func kmsTags(tags map[string]string) []kmstypes.Tag {
	keys := bootstrapSortedKeys(tags)
	out := make([]kmstypes.Tag, 0, len(keys))
	for _, key := range keys {
		out = append(out, kmstypes.Tag{TagKey: sdka.String(key), TagValue: sdka.String(tags[key])})
	}
	return out
}

func acmTags(tags map[string]string) []acmtypes.Tag {
	keys := bootstrapSortedKeys(tags)
	out := make([]acmtypes.Tag, 0, len(keys))
	for _, key := range keys {
		out = append(out, acmtypes.Tag{Key: sdka.String(key), Value: sdka.String(tags[key])})
	}
	return out
}

func bootstrapSortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func bootstrapTrustPolicy() string {
	body, _ := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect":    "Allow",
			"Principal": map[string]string{"AWS": "*"},
			"Action":    "sts:AssumeRole",
		}},
	})
	return string(body)
}

func trailingDot(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasSuffix(value, ".") {
		return value
	}
	return value + "."
}

func normalizeHostedZoneID(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "/hostedzone/")
	return value
}

func idempotencyToken(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 32 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "skiff"
	}
	return out
}
