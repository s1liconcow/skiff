package aws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdka "github.com/aws/aws-sdk-go-v2/aws"
	sdkconfig "github.com/aws/aws-sdk-go-v2/config"
	sdkcredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	asgtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	smithy "github.com/aws/smithy-go"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider"
)

const (
	tagFingerprint = "skiff.dev/fingerprint"
	tagLogicalID   = "skiff.dev/logical-id"
)

type sdkAutoScalingAPI interface {
	DescribeAutoScalingGroups(context.Context, *autoscaling.DescribeAutoScalingGroupsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
	CreateAutoScalingGroup(context.Context, *autoscaling.CreateAutoScalingGroupInput, ...func(*autoscaling.Options)) (*autoscaling.CreateAutoScalingGroupOutput, error)
	UpdateAutoScalingGroup(context.Context, *autoscaling.UpdateAutoScalingGroupInput, ...func(*autoscaling.Options)) (*autoscaling.UpdateAutoScalingGroupOutput, error)
	AttachLoadBalancerTargetGroups(context.Context, *autoscaling.AttachLoadBalancerTargetGroupsInput, ...func(*autoscaling.Options)) (*autoscaling.AttachLoadBalancerTargetGroupsOutput, error)
	CreateOrUpdateTags(context.Context, *autoscaling.CreateOrUpdateTagsInput, ...func(*autoscaling.Options)) (*autoscaling.CreateOrUpdateTagsOutput, error)
}

type sdkEC2API interface {
	DescribeSecurityGroups(context.Context, *ec2.DescribeSecurityGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	CreateSecurityGroup(context.Context, *ec2.CreateSecurityGroupInput, ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error)
	AuthorizeSecurityGroupIngress(context.Context, *ec2.AuthorizeSecurityGroupIngressInput, ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error)
	AuthorizeSecurityGroupEgress(context.Context, *ec2.AuthorizeSecurityGroupEgressInput, ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupEgressOutput, error)
	DescribeLaunchTemplates(context.Context, *ec2.DescribeLaunchTemplatesInput, ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error)
	CreateLaunchTemplate(context.Context, *ec2.CreateLaunchTemplateInput, ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error)
	CreateLaunchTemplateVersion(context.Context, *ec2.CreateLaunchTemplateVersionInput, ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error)
	ModifyLaunchTemplate(context.Context, *ec2.ModifyLaunchTemplateInput, ...func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error)
	CreateTags(context.Context, *ec2.CreateTagsInput, ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
}

type sdkELBV2API interface {
	DescribeTargetGroups(context.Context, *elasticloadbalancingv2.DescribeTargetGroupsInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetGroupsOutput, error)
	CreateTargetGroup(context.Context, *elasticloadbalancingv2.CreateTargetGroupInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.CreateTargetGroupOutput, error)
	ModifyTargetGroup(context.Context, *elasticloadbalancingv2.ModifyTargetGroupInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.ModifyTargetGroupOutput, error)
	DescribeRules(context.Context, *elasticloadbalancingv2.DescribeRulesInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeRulesOutput, error)
	CreateRule(context.Context, *elasticloadbalancingv2.CreateRuleInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.CreateRuleOutput, error)
	ModifyRule(context.Context, *elasticloadbalancingv2.ModifyRuleInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.ModifyRuleOutput, error)
	DescribeTags(context.Context, *elasticloadbalancingv2.DescribeTagsInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error)
	AddTags(context.Context, *elasticloadbalancingv2.AddTagsInput, ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.AddTagsOutput, error)
}

type sdkIAMAPI interface {
	GetRole(context.Context, *iam.GetRoleInput, ...func(*iam.Options)) (*iam.GetRoleOutput, error)
	CreateRole(context.Context, *iam.CreateRoleInput, ...func(*iam.Options)) (*iam.CreateRoleOutput, error)
	PutRolePolicy(context.Context, *iam.PutRolePolicyInput, ...func(*iam.Options)) (*iam.PutRolePolicyOutput, error)
	TagRole(context.Context, *iam.TagRoleInput, ...func(*iam.Options)) (*iam.TagRoleOutput, error)
	GetInstanceProfile(context.Context, *iam.GetInstanceProfileInput, ...func(*iam.Options)) (*iam.GetInstanceProfileOutput, error)
	CreateInstanceProfile(context.Context, *iam.CreateInstanceProfileInput, ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error)
	AddRoleToInstanceProfile(context.Context, *iam.AddRoleToInstanceProfileInput, ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error)
	TagInstanceProfile(context.Context, *iam.TagInstanceProfileInput, ...func(*iam.Options)) (*iam.TagInstanceProfileOutput, error)
	ListRoles(context.Context, *iam.ListRolesInput, ...func(*iam.Options)) (*iam.ListRolesOutput, error)
	ListRoleTags(context.Context, *iam.ListRoleTagsInput, ...func(*iam.Options)) (*iam.ListRoleTagsOutput, error)
}

type sdkLogsAPI interface {
	DescribeLogGroups(context.Context, *cloudwatchlogs.DescribeLogGroupsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error)
	CreateLogGroup(context.Context, *cloudwatchlogs.CreateLogGroupInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogGroupOutput, error)
	PutRetentionPolicy(context.Context, *cloudwatchlogs.PutRetentionPolicyInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutRetentionPolicyOutput, error)
	TagResource(context.Context, *cloudwatchlogs.TagResourceInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.TagResourceOutput, error)
	ListTagsForResource(context.Context, *cloudwatchlogs.ListTagsForResourceInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.ListTagsForResourceOutput, error)
}

type SDKServiceResourceManager struct {
	asg  sdkAutoScalingAPI
	ec2  sdkEC2API
	elb  sdkELBV2API
	iam  sdkIAMAPI
	logs sdkLogsAPI

	mu   sync.Mutex
	refs map[string]sdkResourceRef
}

type sdkResourceRef struct {
	Kind       string
	LogicalID  string
	Name       string
	ProviderID string
	ARN        string
	Status     string
	Tags       map[string]string
}

func NewSDKClients(ctx context.Context, cfg Config) (Clients, error) {
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
		return Clients{}, &provider.Error{
			Code:     provider.CodeInvalidConfig,
			Provider: Name,
			Op:       "construct_live_aws_clients",
			Summary:  err.Error(),
			Cause:    err,
		}
	}
	manager := NewSDKServiceResourceManager(
		autoscaling.NewFromConfig(loaded),
		ec2.NewFromConfig(loaded),
		elasticloadbalancingv2.NewFromConfig(loaded),
		iam.NewFromConfig(loaded),
		cloudwatchlogs.NewFromConfig(loaded),
	)
	return Clients{
		AutoScaling:      manager,
		EC2:              manager,
		ELBV2:            manager,
		IAM:              manager,
		Logs:             manager,
		ServiceResources: manager,
	}, nil
}

func NewSDKServiceResourceManager(asg sdkAutoScalingAPI, ec2Client sdkEC2API, elb sdkELBV2API, iamClient sdkIAMAPI, logs sdkLogsAPI) *SDKServiceResourceManager {
	return &SDKServiceResourceManager{
		asg:  asg,
		ec2:  ec2Client,
		elb:  elb,
		iam:  iamClient,
		logs: logs,
		refs: map[string]sdkResourceRef{},
	}
}

func (m *SDKServiceResourceManager) PlanResource(ctx context.Context, desired DesiredServiceResource) (*ResourcePlan, error) {
	ref, ok, err := m.findDesired(ctx, desired)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &ResourcePlan{Action: provider.ActionCreate, Summary: "create " + desired.Summary}, nil
	}
	m.remember(ref)
	action := provider.ActionUpdate
	summary := "update " + desired.Summary
	if ref.Tags[tagFingerprint] == desired.Fingerprint {
		action = provider.ActionNoop
		summary = desired.Summary + " is current"
	}
	return &ResourcePlan{
		Action:      action,
		ProviderID:  ref.ProviderID,
		Summary:     summary,
		Fingerprint: desired.Fingerprint,
	}, nil
}

func (m *SDKServiceResourceManager) ApplyResource(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	switch desired.Kind {
	case ResourceKindIAMRole:
		return m.applyIAMRole(ctx, desired)
	case ResourceKindIAMInstanceProfile:
		return m.applyInstanceProfile(ctx, desired)
	case ResourceKindSecurityGroup:
		return m.applySecurityGroup(ctx, desired)
	case ResourceKindLogGroup:
		return m.applyLogGroup(ctx, desired)
	case ResourceKindMetricConfig:
		return m.applyMetricConfig(desired)
	case ResourceKindTargetGroup:
		return m.applyTargetGroup(ctx, desired)
	case ResourceKindListenerRule:
		return m.applyListenerRule(ctx, desired)
	case ResourceKindLaunchTemplate:
		return m.applyLaunchTemplate(ctx, desired)
	case ResourceKindAutoScalingGroup:
		return m.applyAutoScalingGroup(ctx, desired)
	case ResourceKindRDSInstance, ResourceKindSecret:
		return nil, provider.Unsupported(Name, "live apply "+desired.Kind)
	default:
		return nil, &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "apply_" + desired.Kind,
			Resource: desired.LogicalID,
			Summary:  "unsupported aws live resource kind " + desired.Kind,
		}
	}
}

func (m *SDKServiceResourceManager) FindAutoScalingGroups(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error) {
	if m.asg == nil {
		return nil, missingClients("discover_autoscaling_groups")
	}
	var out []DiscoveredResource
	var token *string
	for {
		resp, err := m.asg.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{NextToken: token})
		if err != nil {
			return nil, err
		}
		for _, group := range resp.AutoScalingGroups {
			tags := asgTagsMap(group.Tags)
			if !matchesTagFilters(tags, filters) {
				continue
			}
			ref := sdkResourceRef{
				Kind:       ResourceKindAutoScalingGroup,
				LogicalID:  tags[tagLogicalID],
				Name:       sdka.ToString(group.AutoScalingGroupName),
				ProviderID: sdka.ToString(group.AutoScalingGroupName),
				ARN:        sdka.ToString(group.AutoScalingGroupARN),
				Status:     sdka.ToString(group.Status),
				Tags:       tags,
			}
			m.remember(ref)
			out = append(out, ref.discovery())
		}
		token = resp.NextToken
		if token == nil || *token == "" {
			break
		}
	}
	return out, nil
}

func (m *SDKServiceResourceManager) FindLaunchTemplates(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error) {
	if m.ec2 == nil {
		return nil, missingClients("discover_launch_templates")
	}
	var out []DiscoveredResource
	var token *string
	for {
		resp, err := m.ec2.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{
			Filters:   ec2Filters(filters),
			NextToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, tmpl := range resp.LaunchTemplates {
			tags := ec2TagsMap(tmpl.Tags)
			ref := sdkResourceRef{
				Kind:       ResourceKindLaunchTemplate,
				LogicalID:  tags[tagLogicalID],
				Name:       sdka.ToString(tmpl.LaunchTemplateName),
				ProviderID: sdka.ToString(tmpl.LaunchTemplateId),
				Tags:       tags,
			}
			m.remember(ref)
			out = append(out, ref.discovery())
		}
		token = resp.NextToken
		if token == nil || *token == "" {
			break
		}
	}
	return out, nil
}

func (m *SDKServiceResourceManager) FindTargetGroups(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error) {
	if m.elb == nil {
		return nil, missingClients("discover_target_groups")
	}
	var out []DiscoveredResource
	var token *string
	for {
		resp, err := m.elb.DescribeTargetGroups(ctx, &elasticloadbalancingv2.DescribeTargetGroupsInput{Marker: token})
		if err != nil {
			return nil, err
		}
		tagged, err := m.elbTags(ctx, targetGroupARNs(resp.TargetGroups))
		if err != nil {
			return nil, err
		}
		for _, group := range resp.TargetGroups {
			arn := sdka.ToString(group.TargetGroupArn)
			tags := tagged[arn]
			if !matchesTagFilters(tags, filters) {
				continue
			}
			ref := sdkResourceRef{
				Kind:       ResourceKindTargetGroup,
				LogicalID:  tags[tagLogicalID],
				Name:       sdka.ToString(group.TargetGroupName),
				ProviderID: arn,
				ARN:        arn,
				Status:     string(group.TargetType),
				Tags:       tags,
			}
			m.remember(ref)
			out = append(out, ref.discovery())
		}
		token = resp.NextMarker
		if token == nil || *token == "" {
			break
		}
	}
	return out, nil
}

func (m *SDKServiceResourceManager) FindRoles(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error) {
	if m.iam == nil {
		return nil, missingClients("discover_iam_roles")
	}
	var out []DiscoveredResource
	var marker *string
	for {
		resp, err := m.iam.ListRoles(ctx, &iam.ListRolesInput{Marker: marker})
		if err != nil {
			return nil, err
		}
		for _, role := range resp.Roles {
			name := sdka.ToString(role.RoleName)
			tagResp, err := m.iam.ListRoleTags(ctx, &iam.ListRoleTagsInput{RoleName: sdka.String(name)})
			if err != nil {
				return nil, err
			}
			tags := iamTagsMap(tagResp.Tags)
			if !matchesTagFilters(tags, filters) {
				continue
			}
			ref := sdkResourceRef{
				Kind:       ResourceKindIAMRole,
				LogicalID:  tags[tagLogicalID],
				Name:       name,
				ProviderID: name,
				ARN:        sdka.ToString(role.Arn),
				Tags:       tags,
			}
			m.remember(ref)
			out = append(out, ref.discovery())
		}
		marker = resp.Marker
		if marker == nil || *marker == "" || !resp.IsTruncated {
			break
		}
	}
	return out, nil
}

func (m *SDKServiceResourceManager) FindLogGroups(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error) {
	if m.logs == nil {
		return nil, missingClients("discover_log_groups")
	}
	var out []DiscoveredResource
	var token *string
	for {
		resp, err := m.logs.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{NextToken: token})
		if err != nil {
			return nil, err
		}
		for _, group := range resp.LogGroups {
			arn := firstNonEmpty(sdka.ToString(group.LogGroupArn), strings.TrimSuffix(sdka.ToString(group.Arn), ":*"))
			tags, err := m.logTags(ctx, arn)
			if err != nil {
				return nil, err
			}
			if !matchesTagFilters(tags, filters) {
				continue
			}
			ref := sdkResourceRef{
				Kind:       ResourceKindLogGroup,
				LogicalID:  tags[tagLogicalID],
				Name:       sdka.ToString(group.LogGroupName),
				ProviderID: sdka.ToString(group.LogGroupName),
				ARN:        arn,
				Tags:       tags,
			}
			m.remember(ref)
			out = append(out, ref.discovery())
		}
		token = resp.NextToken
		if token == nil || *token == "" {
			break
		}
	}
	return out, nil
}

func (m *SDKServiceResourceManager) applyIAMRole(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	if m.iam == nil {
		return nil, missingClients("apply_iam_role")
	}
	var role IAMRoleResource
	if err := decodeDesired(desired, &role); err != nil {
		return nil, err
	}
	tags := tagsWithFingerprint(desired)
	assume, err := json.Marshal(role.AssumeRolePolicy)
	if err != nil {
		return nil, err
	}
	inline, err := json.Marshal(role.InlinePolicy)
	if err != nil {
		return nil, err
	}
	found, ok, err := m.findIAMRole(ctx, role.Name)
	if err != nil {
		return nil, err
	}
	if !ok {
		resp, err := m.iam.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 sdka.String(role.Name),
			AssumeRolePolicyDocument: sdka.String(string(assume)),
			Description:              sdka.String("Skiff managed workload role " + role.LogicalID),
			Tags:                     iamTags(tags),
		})
		if err != nil && !sdkAlreadyExists(err) {
			return nil, err
		}
		if err == nil && resp.Role != nil {
			found = sdkResourceRef{
				Kind:       ResourceKindIAMRole,
				LogicalID:  role.LogicalID,
				Name:       role.Name,
				ProviderID: role.Name,
				ARN:        sdka.ToString(resp.Role.Arn),
				Tags:       tags,
			}
		} else {
			found, _, err = m.findIAMRole(ctx, role.Name)
			if err != nil {
				return nil, err
			}
		}
	}
	if _, err := m.iam.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       sdka.String(role.Name),
		PolicyName:     sdka.String("skiff-inline"),
		PolicyDocument: sdka.String(string(inline)),
	}); err != nil {
		return nil, err
	}
	if _, err := m.iam.TagRole(ctx, &iam.TagRoleInput{RoleName: sdka.String(role.Name), Tags: iamTags(tags)}); err != nil {
		return nil, err
	}
	found.LogicalID = role.LogicalID
	found.Tags = tags
	m.remember(found)
	return found.applied("available", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applyInstanceProfile(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	if m.iam == nil {
		return nil, missingClients("apply_instance_profile")
	}
	var profile InstanceProfile
	if err := decodeDesired(desired, &profile); err != nil {
		return nil, err
	}
	tags := tagsWithFingerprint(desired)
	found, ok, err := m.findInstanceProfile(ctx, profile.Name)
	if err != nil {
		return nil, err
	}
	if !ok {
		resp, err := m.iam.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
			InstanceProfileName: sdka.String(profile.Name),
			Tags:                iamTags(tags),
		})
		if err != nil && !sdkAlreadyExists(err) {
			return nil, err
		}
		if err == nil && resp.InstanceProfile != nil {
			found = instanceProfileRef(*resp.InstanceProfile)
		} else {
			found, _, err = m.findInstanceProfile(ctx, profile.Name)
			if err != nil {
				return nil, err
			}
		}
	}
	roleName := m.resolveName(ResourceKindIAMRole, profile.RoleRef)
	if roleName == "" {
		roleName = profile.RoleRef
	}
	if roleName != "" {
		if _, err := m.iam.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
			InstanceProfileName: sdka.String(profile.Name),
			RoleName:            sdka.String(roleName),
		}); err != nil && !sdkAlreadyExists(err) {
			return nil, err
		}
	}
	if _, err := m.iam.TagInstanceProfile(ctx, &iam.TagInstanceProfileInput{
		InstanceProfileName: sdka.String(profile.Name),
		Tags:                iamTags(tags),
	}); err != nil {
		return nil, err
	}
	found.Kind = ResourceKindIAMInstanceProfile
	found.LogicalID = profile.LogicalID
	found.Name = profile.Name
	found.ProviderID = profile.Name
	found.Tags = tags
	m.remember(found)
	return found.applied("available", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applySecurityGroup(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	if m.ec2 == nil {
		return nil, missingClients("apply_security_group")
	}
	var group SecurityGroupAWS
	if err := decodeDesired(desired, &group); err != nil {
		return nil, err
	}
	tags := tagsWithFingerprint(desired)
	found, ok, err := m.findSecurityGroup(ctx, group.Name, group.VPCID)
	if err != nil {
		return nil, err
	}
	if !ok {
		resp, err := m.ec2.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
			GroupName:         sdka.String(group.Name),
			Description:       sdka.String(group.Description),
			VpcId:             sdka.String(group.VPCID),
			TagSpecifications: []ec2types.TagSpecification{{ResourceType: ec2types.ResourceTypeSecurityGroup, Tags: ec2Tags(tags)}},
		})
		if err != nil && !sdkAlreadyExists(err) {
			return nil, err
		}
		if err == nil {
			found = sdkResourceRef{
				Kind:       ResourceKindSecurityGroup,
				LogicalID:  group.LogicalID,
				Name:       group.Name,
				ProviderID: sdka.ToString(resp.GroupId),
				ARN:        sdka.ToString(resp.SecurityGroupArn),
				Tags:       tags,
			}
		} else {
			found, _, err = m.findSecurityGroup(ctx, group.Name, group.VPCID)
			if err != nil {
				return nil, err
			}
		}
	}
	if found.ProviderID != "" {
		if _, err := m.ec2.CreateTags(ctx, &ec2.CreateTagsInput{Resources: []string{found.ProviderID}, Tags: ec2Tags(tags)}); err != nil {
			return nil, err
		}
		if err := m.authorizeSecurityGroupRules(ctx, found.ProviderID, group.Ingress, true); err != nil {
			return nil, err
		}
		if err := m.authorizeSecurityGroupRules(ctx, found.ProviderID, group.Egress, false); err != nil {
			return nil, err
		}
	}
	found.LogicalID = group.LogicalID
	found.Name = group.Name
	found.Tags = tags
	m.remember(found)
	return found.applied("available", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applyLogGroup(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	if m.logs == nil {
		return nil, missingClients("apply_log_group")
	}
	var group LogGroup
	if err := decodeDesired(desired, &group); err != nil {
		return nil, err
	}
	tags := tagsWithFingerprint(desired)
	found, ok, err := m.findLogGroup(ctx, group.Name)
	if err != nil {
		return nil, err
	}
	if !ok {
		_, err := m.logs.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
			LogGroupName: sdka.String(group.Name),
			Tags:         tags,
		})
		if err != nil && !sdkAlreadyExists(err) {
			return nil, err
		}
		found, _, err = m.findLogGroup(ctx, group.Name)
		if err != nil {
			return nil, err
		}
	}
	if group.RetentionDays > 0 {
		if _, err := m.logs.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
			LogGroupName:    sdka.String(group.Name),
			RetentionInDays: sdka.Int32(int32(group.RetentionDays)),
		}); err != nil {
			return nil, err
		}
	}
	if found.ARN != "" {
		if _, err := m.logs.TagResource(ctx, &cloudwatchlogs.TagResourceInput{
			ResourceArn: sdka.String(found.ARN),
			Tags:        tags,
		}); err != nil {
			return nil, err
		}
	}
	found.LogicalID = group.LogicalID
	found.Name = group.Name
	found.ProviderID = group.Name
	found.Tags = tags
	m.remember(found)
	return found.applied("available", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applyMetricConfig(desired DesiredServiceResource) (*AppliedResource, error) {
	var metric MetricConfigAWS
	if err := decodeDesired(desired, &metric); err != nil {
		return nil, err
	}
	ref := sdkResourceRef{
		Kind:       ResourceKindMetricConfig,
		LogicalID:  metric.LogicalID,
		Name:       metric.Namespace,
		ProviderID: "cloudwatch-metric-config/" + metric.Namespace,
		Status:     "configured",
		Tags:       tagsWithFingerprint(desired),
	}
	m.remember(ref)
	return ref.applied("configured", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applyTargetGroup(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	if m.elb == nil {
		return nil, missingClients("apply_target_group")
	}
	var group TargetGroupAWS
	if err := decodeDesired(desired, &group); err != nil {
		return nil, err
	}
	tags := tagsWithFingerprint(desired)
	found, ok, err := m.findTargetGroup(ctx, group.Name)
	if err != nil {
		return nil, err
	}
	if !ok {
		resp, err := m.elb.CreateTargetGroup(ctx, createTargetGroupInput(group, tags))
		if err != nil && !sdkAlreadyExists(err) {
			return nil, err
		}
		if err == nil && len(resp.TargetGroups) > 0 {
			found = targetGroupRef(resp.TargetGroups[0], tags)
		} else {
			found, _, err = m.findTargetGroup(ctx, group.Name)
			if err != nil {
				return nil, err
			}
		}
	} else if found.ARN != "" {
		if _, err := m.elb.ModifyTargetGroup(ctx, modifyTargetGroupInput(group, found.ARN)); err != nil {
			return nil, err
		}
	}
	if found.ARN != "" {
		if _, err := m.elb.AddTags(ctx, &elasticloadbalancingv2.AddTagsInput{ResourceArns: []string{found.ARN}, Tags: elbTags(tags)}); err != nil {
			return nil, err
		}
	}
	found.LogicalID = group.LogicalID
	found.Name = group.Name
	found.ProviderID = firstNonEmpty(found.ProviderID, found.ARN)
	found.ARN = firstNonEmpty(found.ARN, found.ProviderID)
	found.Tags = tags
	m.remember(found)
	return found.applied("available", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applyListenerRule(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	if m.elb == nil {
		return nil, missingClients("apply_listener_rule")
	}
	var rule ListenerRule
	if err := decodeDesired(desired, &rule); err != nil {
		return nil, err
	}
	tags := tagsWithFingerprint(desired)
	targetARN := m.resolveARN(ResourceKindTargetGroup, rule.TargetGroupRef)
	if targetARN == "" {
		targetARN = rule.TargetGroupRef
	}
	if targetARN == "" {
		return nil, &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "apply_listener_rule",
			Resource: rule.LogicalID,
			Summary:  "listener rule target group could not be resolved",
		}
	}
	found, ok, err := m.findListenerRule(ctx, rule.ListenerARN, rule.LogicalID)
	if err != nil {
		return nil, err
	}
	actions, conditions := listenerRuleShape(rule, targetARN)
	if !ok {
		priority, err := m.availableListenerRulePriority(ctx, rule.ListenerARN, hashPriority(rule.LogicalID))
		if err != nil {
			return nil, err
		}
		resp, err := m.elb.CreateRule(ctx, &elasticloadbalancingv2.CreateRuleInput{
			ListenerArn: sdka.String(rule.ListenerARN),
			Priority:    sdka.Int32(priority),
			Actions:     actions,
			Conditions:  conditions,
			Tags:        elbTags(tags),
		})
		if err != nil {
			return nil, err
		}
		if len(resp.Rules) > 0 {
			found = listenerRuleRef(resp.Rules[0], tags)
		}
	} else if found.ARN != "" {
		resp, err := m.elb.ModifyRule(ctx, &elasticloadbalancingv2.ModifyRuleInput{
			RuleArn:    sdka.String(found.ARN),
			Actions:    actions,
			Conditions: conditions,
		})
		if err != nil {
			return nil, err
		}
		if len(resp.Rules) > 0 {
			found = listenerRuleRef(resp.Rules[0], tags)
		}
	}
	if found.ARN != "" {
		if _, err := m.elb.AddTags(ctx, &elasticloadbalancingv2.AddTagsInput{ResourceArns: []string{found.ARN}, Tags: elbTags(tags)}); err != nil {
			return nil, err
		}
	}
	found.Kind = ResourceKindListenerRule
	found.LogicalID = rule.LogicalID
	found.Name = rule.Name
	found.ProviderID = firstNonEmpty(found.ProviderID, found.ARN)
	found.ARN = firstNonEmpty(found.ARN, found.ProviderID)
	found.Tags = tags
	m.remember(found)
	return found.applied("available", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applyLaunchTemplate(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	if m.ec2 == nil {
		return nil, missingClients("apply_launch_template")
	}
	var tmpl LaunchTemplate
	if err := decodeDesired(desired, &tmpl); err != nil {
		return nil, err
	}
	tags := tagsWithFingerprint(desired)
	data := m.launchTemplateData(tmpl)
	found, ok, err := m.findLaunchTemplate(ctx, tmpl.Name)
	if err != nil {
		return nil, err
	}
	if !ok {
		resp, err := m.ec2.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
			LaunchTemplateName: sdka.String(tmpl.Name),
			LaunchTemplateData: data,
			VersionDescription: sdka.String("skiff " + desired.Fingerprint),
			TagSpecifications: []ec2types.TagSpecification{{
				ResourceType: ec2types.ResourceTypeLaunchTemplate,
				Tags:         ec2Tags(tags),
			}},
		})
		if err != nil && !sdkAlreadyExists(err) {
			return nil, err
		}
		if err == nil && resp.LaunchTemplate != nil {
			found = launchTemplateRef(*resp.LaunchTemplate)
		} else {
			found, _, err = m.findLaunchTemplate(ctx, tmpl.Name)
			if err != nil {
				return nil, err
			}
		}
	} else {
		resp, err := m.ec2.CreateLaunchTemplateVersion(ctx, &ec2.CreateLaunchTemplateVersionInput{
			LaunchTemplateId:   sdka.String(found.ProviderID),
			LaunchTemplateData: data,
			VersionDescription: sdka.String("skiff " + desired.Fingerprint),
		})
		if err != nil {
			return nil, err
		}
		version := "$Latest"
		if resp.LaunchTemplateVersion != nil && resp.LaunchTemplateVersion.VersionNumber != nil {
			version = strconv.FormatInt(*resp.LaunchTemplateVersion.VersionNumber, 10)
		}
		if _, err := m.ec2.ModifyLaunchTemplate(ctx, &ec2.ModifyLaunchTemplateInput{
			LaunchTemplateId: sdka.String(found.ProviderID),
			DefaultVersion:   sdka.String(version),
		}); err != nil {
			return nil, err
		}
	}
	if found.ProviderID != "" {
		if _, err := m.ec2.CreateTags(ctx, &ec2.CreateTagsInput{Resources: []string{found.ProviderID}, Tags: ec2Tags(tags)}); err != nil {
			return nil, err
		}
	}
	found.LogicalID = tmpl.LogicalID
	found.Name = tmpl.Name
	found.Tags = tags
	m.remember(found)
	return found.applied("available", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applyAutoScalingGroup(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	if m.asg == nil {
		return nil, missingClients("apply_autoscaling_group")
	}
	var group AutoScalingGroup
	if err := decodeDesired(desired, &group); err != nil {
		return nil, err
	}
	tags := tagsWithFingerprint(desired)
	found, ok, err := m.findAutoScalingGroup(ctx, group.Name)
	if err != nil {
		return nil, err
	}
	launchTemplateID := m.resolveID(ResourceKindLaunchTemplate, group.LaunchTemplateRef)
	if launchTemplateID == "" {
		launchTemplateID = group.LaunchTemplateRef
	}
	if strings.TrimSpace(launchTemplateID) == "" {
		return nil, &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "apply_autoscaling_group",
			Resource: group.LogicalID,
			Summary:  "Auto Scaling Group launch template could not be resolved",
		}
	}
	input := asgShape(group, tags, launchTemplateID, m.resolveTargetGroupARNs(group.TargetGroupRefs))
	if !ok {
		if _, err := m.asg.CreateAutoScalingGroup(ctx, input.create); err != nil && !sdkAlreadyExists(err) {
			return nil, err
		}
		found, _, err = m.findAutoScalingGroup(ctx, group.Name)
		if err != nil {
			return nil, err
		}
	} else {
		input.update.AutoScalingGroupName = sdka.String(group.Name)
		if _, err := m.asg.UpdateAutoScalingGroup(ctx, input.update); err != nil {
			return nil, err
		}
	}
	if len(input.targetGroupARNs) > 0 {
		if _, err := m.asg.AttachLoadBalancerTargetGroups(ctx, &autoscaling.AttachLoadBalancerTargetGroupsInput{
			AutoScalingGroupName: sdka.String(group.Name),
			TargetGroupARNs:      input.targetGroupARNs,
		}); err != nil && !sdkAlreadyExists(err) {
			return nil, err
		}
	}
	if _, err := m.asg.CreateOrUpdateTags(ctx, &autoscaling.CreateOrUpdateTagsInput{Tags: asgTags(group.Name, tags)}); err != nil {
		return nil, err
	}
	found.Kind = ResourceKindAutoScalingGroup
	found.LogicalID = group.LogicalID
	found.Name = group.Name
	found.ProviderID = group.Name
	found.Tags = tags
	m.remember(found)
	return found.applied("available", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) findDesired(ctx context.Context, desired DesiredServiceResource) (sdkResourceRef, bool, error) {
	switch desired.Kind {
	case ResourceKindIAMRole:
		var role IAMRoleResource
		if err := decodeDesired(desired, &role); err != nil {
			return sdkResourceRef{}, false, err
		}
		return m.findIAMRole(ctx, role.Name)
	case ResourceKindIAMInstanceProfile:
		var profile InstanceProfile
		if err := decodeDesired(desired, &profile); err != nil {
			return sdkResourceRef{}, false, err
		}
		return m.findInstanceProfile(ctx, profile.Name)
	case ResourceKindSecurityGroup:
		var group SecurityGroupAWS
		if err := decodeDesired(desired, &group); err != nil {
			return sdkResourceRef{}, false, err
		}
		return m.findSecurityGroup(ctx, group.Name, group.VPCID)
	case ResourceKindLogGroup:
		var group LogGroup
		if err := decodeDesired(desired, &group); err != nil {
			return sdkResourceRef{}, false, err
		}
		return m.findLogGroup(ctx, group.Name)
	case ResourceKindMetricConfig:
		ref := sdkResourceRef{
			Kind:       desired.Kind,
			LogicalID:  desired.LogicalID,
			Name:       desired.Name,
			ProviderID: "cloudwatch-metric-config/" + desired.Name,
			Status:     "configured",
			Tags:       tagsWithFingerprint(desired),
		}
		return ref, true, nil
	case ResourceKindTargetGroup:
		var group TargetGroupAWS
		if err := decodeDesired(desired, &group); err != nil {
			return sdkResourceRef{}, false, err
		}
		return m.findTargetGroup(ctx, group.Name)
	case ResourceKindListenerRule:
		var rule ListenerRule
		if err := decodeDesired(desired, &rule); err != nil {
			return sdkResourceRef{}, false, err
		}
		return m.findListenerRule(ctx, rule.ListenerARN, rule.LogicalID)
	case ResourceKindLaunchTemplate:
		var tmpl LaunchTemplate
		if err := decodeDesired(desired, &tmpl); err != nil {
			return sdkResourceRef{}, false, err
		}
		return m.findLaunchTemplate(ctx, tmpl.Name)
	case ResourceKindAutoScalingGroup:
		var group AutoScalingGroup
		if err := decodeDesired(desired, &group); err != nil {
			return sdkResourceRef{}, false, err
		}
		return m.findAutoScalingGroup(ctx, group.Name)
	case ResourceKindRDSInstance, ResourceKindSecret:
		return sdkResourceRef{}, false, provider.Unsupported(Name, "live apply "+desired.Kind)
	default:
		return sdkResourceRef{}, false, nil
	}
}

func (m *SDKServiceResourceManager) findIAMRole(ctx context.Context, name string) (sdkResourceRef, bool, error) {
	if m.iam == nil {
		return sdkResourceRef{}, false, missingClients("find_iam_role")
	}
	resp, err := m.iam.GetRole(ctx, &iam.GetRoleInput{RoleName: sdka.String(name)})
	if err != nil {
		if sdkNotFound(err) {
			return sdkResourceRef{}, false, nil
		}
		return sdkResourceRef{}, false, err
	}
	if resp.Role == nil {
		return sdkResourceRef{}, false, nil
	}
	ref := iamRoleRef(*resp.Role)
	ref.Tags = iamTagsMap(resp.Role.Tags)
	return ref, true, nil
}

func (m *SDKServiceResourceManager) findInstanceProfile(ctx context.Context, name string) (sdkResourceRef, bool, error) {
	if m.iam == nil {
		return sdkResourceRef{}, false, missingClients("find_instance_profile")
	}
	resp, err := m.iam.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: sdka.String(name)})
	if err != nil {
		if sdkNotFound(err) {
			return sdkResourceRef{}, false, nil
		}
		return sdkResourceRef{}, false, err
	}
	if resp.InstanceProfile == nil {
		return sdkResourceRef{}, false, nil
	}
	return instanceProfileRef(*resp.InstanceProfile), true, nil
}

func (m *SDKServiceResourceManager) findSecurityGroup(ctx context.Context, name, vpcID string) (sdkResourceRef, bool, error) {
	if m.ec2 == nil {
		return sdkResourceRef{}, false, missingClients("find_security_group")
	}
	filters := []ec2types.Filter{{Name: sdka.String("group-name"), Values: []string{name}}}
	if strings.TrimSpace(vpcID) != "" {
		filters = append(filters, ec2types.Filter{Name: sdka.String("vpc-id"), Values: []string{vpcID}})
	}
	resp, err := m.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: filters})
	if err != nil {
		if sdkNotFound(err) {
			return sdkResourceRef{}, false, nil
		}
		return sdkResourceRef{}, false, err
	}
	for _, group := range resp.SecurityGroups {
		ref := securityGroupRef(group)
		if ref.Name == name {
			return ref, true, nil
		}
	}
	return sdkResourceRef{}, false, nil
}

func (m *SDKServiceResourceManager) findLogGroup(ctx context.Context, name string) (sdkResourceRef, bool, error) {
	if m.logs == nil {
		return sdkResourceRef{}, false, missingClients("find_log_group")
	}
	resp, err := m.logs.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{LogGroupNamePrefix: sdka.String(name)})
	if err != nil {
		if sdkNotFound(err) {
			return sdkResourceRef{}, false, nil
		}
		return sdkResourceRef{}, false, err
	}
	for _, group := range resp.LogGroups {
		if sdka.ToString(group.LogGroupName) != name {
			continue
		}
		arn := firstNonEmpty(sdka.ToString(group.LogGroupArn), strings.TrimSuffix(sdka.ToString(group.Arn), ":*"))
		tags, err := m.logTags(ctx, arn)
		if err != nil {
			return sdkResourceRef{}, false, err
		}
		return sdkResourceRef{
			Kind:       ResourceKindLogGroup,
			LogicalID:  tags[tagLogicalID],
			Name:       name,
			ProviderID: name,
			ARN:        arn,
			Tags:       tags,
		}, true, nil
	}
	return sdkResourceRef{}, false, nil
}

func (m *SDKServiceResourceManager) findTargetGroup(ctx context.Context, name string) (sdkResourceRef, bool, error) {
	if m.elb == nil {
		return sdkResourceRef{}, false, missingClients("find_target_group")
	}
	resp, err := m.elb.DescribeTargetGroups(ctx, &elasticloadbalancingv2.DescribeTargetGroupsInput{Names: []string{name}})
	if err != nil {
		if sdkNotFound(err) {
			return sdkResourceRef{}, false, nil
		}
		return sdkResourceRef{}, false, err
	}
	if len(resp.TargetGroups) == 0 {
		return sdkResourceRef{}, false, nil
	}
	arns := targetGroupARNs(resp.TargetGroups)
	tagged, err := m.elbTags(ctx, arns)
	if err != nil {
		return sdkResourceRef{}, false, err
	}
	ref := targetGroupRef(resp.TargetGroups[0], tagged[sdka.ToString(resp.TargetGroups[0].TargetGroupArn)])
	return ref, true, nil
}

func (m *SDKServiceResourceManager) findListenerRule(ctx context.Context, listenerARN, logicalID string) (sdkResourceRef, bool, error) {
	if m.elb == nil {
		return sdkResourceRef{}, false, missingClients("find_listener_rule")
	}
	resp, err := m.elb.DescribeRules(ctx, &elasticloadbalancingv2.DescribeRulesInput{ListenerArn: sdka.String(listenerARN)})
	if err != nil {
		if sdkNotFound(err) {
			return sdkResourceRef{}, false, nil
		}
		return sdkResourceRef{}, false, err
	}
	arns := ruleARNs(resp.Rules)
	tagged, err := m.elbTags(ctx, arns)
	if err != nil {
		return sdkResourceRef{}, false, err
	}
	for _, rule := range resp.Rules {
		arn := sdka.ToString(rule.RuleArn)
		tags := tagged[arn]
		if tags[tagLogicalID] != logicalID {
			continue
		}
		return listenerRuleRef(rule, tags), true, nil
	}
	return sdkResourceRef{}, false, nil
}

func (m *SDKServiceResourceManager) findLaunchTemplate(ctx context.Context, name string) (sdkResourceRef, bool, error) {
	if m.ec2 == nil {
		return sdkResourceRef{}, false, missingClients("find_launch_template")
	}
	resp, err := m.ec2.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{LaunchTemplateNames: []string{name}})
	if err != nil {
		if sdkNotFound(err) {
			return sdkResourceRef{}, false, nil
		}
		return sdkResourceRef{}, false, err
	}
	if len(resp.LaunchTemplates) == 0 {
		return sdkResourceRef{}, false, nil
	}
	return launchTemplateRef(resp.LaunchTemplates[0]), true, nil
}

func (m *SDKServiceResourceManager) findAutoScalingGroup(ctx context.Context, name string) (sdkResourceRef, bool, error) {
	if m.asg == nil {
		return sdkResourceRef{}, false, missingClients("find_autoscaling_group")
	}
	resp, err := m.asg.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{AutoScalingGroupNames: []string{name}})
	if err != nil {
		return sdkResourceRef{}, false, err
	}
	if len(resp.AutoScalingGroups) == 0 {
		return sdkResourceRef{}, false, nil
	}
	group := resp.AutoScalingGroups[0]
	tags := asgTagsMap(group.Tags)
	return sdkResourceRef{
		Kind:       ResourceKindAutoScalingGroup,
		LogicalID:  tags[tagLogicalID],
		Name:       sdka.ToString(group.AutoScalingGroupName),
		ProviderID: sdka.ToString(group.AutoScalingGroupName),
		ARN:        sdka.ToString(group.AutoScalingGroupARN),
		Status:     sdka.ToString(group.Status),
		Tags:       tags,
	}, true, nil
}

func (m *SDKServiceResourceManager) authorizeSecurityGroupRules(ctx context.Context, groupID string, rules []SecurityGroupRule, ingress bool) error {
	if len(rules) == 0 {
		return nil
	}
	permissions := make([]ec2types.IpPermission, 0, len(rules))
	for _, rule := range rules {
		permissions = append(permissions, m.ipPermission(rule))
	}
	if ingress {
		_, err := m.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId:       sdka.String(groupID),
			IpPermissions: permissions,
		})
		if err != nil && !sdkDuplicate(err) {
			return err
		}
		return nil
	}
	_, err := m.ec2.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
		GroupId:       sdka.String(groupID),
		IpPermissions: permissions,
	})
	if err != nil && !sdkDuplicate(err) {
		return err
	}
	return nil
}

func (m *SDKServiceResourceManager) ipPermission(rule SecurityGroupRule) ec2types.IpPermission {
	protocol := strings.TrimSpace(rule.Protocol)
	if protocol == "" {
		protocol = "tcp"
	}
	perm := ec2types.IpPermission{
		IpProtocol: sdka.String(protocol),
	}
	if protocol != "-1" {
		perm.FromPort = sdka.Int32(int32(rule.FromPort))
		perm.ToPort = sdka.Int32(int32(firstPositive(rule.ToPort, rule.FromPort)))
	}
	if cidr := strings.TrimSpace(rule.CIDR); cidr != "" {
		perm.IpRanges = []ec2types.IpRange{{
			CidrIp:      sdka.String(cidr),
			Description: optionalString(rule.Description),
		}}
	}
	ref := firstNonEmpty(rule.SourceSecurityGroupRef, rule.DestinationSecurityGroupRef)
	if ref != "" {
		groupID := m.resolveID(ResourceKindSecurityGroup, ref)
		if groupID == "" {
			groupID = ref
		}
		perm.UserIdGroupPairs = []ec2types.UserIdGroupPair{{
			GroupId:     sdka.String(groupID),
			Description: optionalString(rule.Description),
		}}
	}
	return perm
}

func (m *SDKServiceResourceManager) launchTemplateData(tmpl LaunchTemplate) *ec2types.RequestLaunchTemplateData {
	sgIDs := make([]string, 0, len(tmpl.SecurityGroupRefs))
	for _, ref := range tmpl.SecurityGroupRefs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		id := m.resolveID(ResourceKindSecurityGroup, ref)
		if id == "" {
			id = ref
		}
		sgIDs = append(sgIDs, id)
	}
	userData := base64.StdEncoding.EncodeToString([]byte(tmpl.UserData))
	data := &ec2types.RequestLaunchTemplateData{
		ImageId:          sdka.String(tmpl.AMIID),
		InstanceType:     ec2types.InstanceType(tmpl.InstanceType),
		SecurityGroupIds: sgIDs,
		UserData:         sdka.String(userData),
	}
	if profile := strings.TrimSpace(tmpl.IAMInstanceProfile); profile != "" {
		data.IamInstanceProfile = &ec2types.LaunchTemplateIamInstanceProfileSpecificationRequest{Name: sdka.String(profile)}
	}
	if len(tmpl.Tags) > 0 {
		tags := ec2Tags(tmpl.Tags)
		data.TagSpecifications = []ec2types.LaunchTemplateTagSpecificationRequest{
			{ResourceType: ec2types.ResourceTypeInstance, Tags: tags},
			{ResourceType: ec2types.ResourceTypeVolume, Tags: tags},
		}
	}
	return data
}

func (m *SDKServiceResourceManager) availableListenerRulePriority(ctx context.Context, listenerARN string, preferred int32) (int32, error) {
	resp, err := m.elb.DescribeRules(ctx, &elasticloadbalancingv2.DescribeRulesInput{ListenerArn: sdka.String(listenerARN)})
	if err != nil {
		return 0, err
	}
	used := map[int32]struct{}{}
	for _, rule := range resp.Rules {
		priority, err := strconv.Atoi(sdka.ToString(rule.Priority))
		if err == nil {
			used[int32(priority)] = struct{}{}
		}
	}
	if _, ok := used[preferred]; !ok {
		return preferred, nil
	}
	for priority := int32(100); priority < 50000; priority++ {
		if _, ok := used[priority]; !ok {
			return priority, nil
		}
	}
	return 0, &provider.Error{
		Code:     provider.CodeValidation,
		Provider: Name,
		Op:       "apply_listener_rule",
		Summary:  "no available ALB listener rule priority",
	}
}

func (m *SDKServiceResourceManager) elbTags(ctx context.Context, arns []string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	arns = nonEmptyStrings(arns)
	if len(arns) == 0 {
		return out, nil
	}
	for start := 0; start < len(arns); start += 20 {
		end := start + 20
		if end > len(arns) {
			end = len(arns)
		}
		resp, err := m.elb.DescribeTags(ctx, &elasticloadbalancingv2.DescribeTagsInput{ResourceArns: arns[start:end]})
		if err != nil {
			return nil, err
		}
		for _, desc := range resp.TagDescriptions {
			out[sdka.ToString(desc.ResourceArn)] = elbTagsMap(desc.Tags)
		}
	}
	return out, nil
}

func (m *SDKServiceResourceManager) logTags(ctx context.Context, arn string) (map[string]string, error) {
	if arn == "" {
		return nil, nil
	}
	resp, err := m.logs.ListTagsForResource(ctx, &cloudwatchlogs.ListTagsForResourceInput{ResourceArn: sdka.String(arn)})
	if err != nil {
		if sdkNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return cloneTags(resp.Tags), nil
}

func (m *SDKServiceResourceManager) remember(ref sdkResourceRef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refs == nil {
		m.refs = map[string]sdkResourceRef{}
	}
	ref.Tags = cloneTags(ref.Tags)
	for _, key := range refKeys(ref) {
		m.refs[key] = ref
	}
}

func (m *SDKServiceResourceManager) resolveID(kind, value string) string {
	ref := m.resolve(kind, value)
	return ref.ProviderID
}

func (m *SDKServiceResourceManager) resolveARN(kind, value string) string {
	ref := m.resolve(kind, value)
	return firstNonEmpty(ref.ARN, ref.ProviderID)
}

func (m *SDKServiceResourceManager) resolveName(kind, value string) string {
	ref := m.resolve(kind, value)
	return ref.Name
}

func (m *SDKServiceResourceManager) resolve(kind, value string) sdkResourceRef {
	value = strings.TrimSpace(value)
	if value == "" {
		return sdkResourceRef{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range []string{refKey(kind, value), refKey(kind, pathSafeResourceName(value)), value} {
		if ref, ok := m.refs[key]; ok {
			return ref
		}
	}
	return sdkResourceRef{}
}

func (m *SDKServiceResourceManager) resolveTargetGroupARNs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		arn := m.resolveARN(ResourceKindTargetGroup, ref)
		if arn == "" {
			arn = strings.TrimSpace(ref)
		}
		if arn != "" {
			out = append(out, arn)
		}
	}
	sort.Strings(out)
	return out
}

func decodeDesired[T any](desired DesiredServiceResource, out *T) error {
	if err := json.Unmarshal(desired.Desired, out); err != nil {
		return &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "decode_" + desired.Kind,
			Resource: desired.LogicalID,
			Summary:  err.Error(),
			Cause:    err,
		}
	}
	return nil
}

func tagsWithFingerprint(desired DesiredServiceResource) map[string]string {
	tags := cloneTags(desired.Tags)
	if tags == nil {
		tags = map[string]string{}
	}
	tags[tagFingerprint] = desired.Fingerprint
	if desired.LogicalID != "" {
		tags[tagLogicalID] = desired.LogicalID
	}
	return tags
}

func (r sdkResourceRef) applied(status, fingerprint string) *AppliedResource {
	return &AppliedResource{
		Kind:        r.Kind,
		LogicalID:   r.LogicalID,
		Name:        r.Name,
		ProviderID:  r.ProviderID,
		ARN:         r.ARN,
		Status:      firstNonEmpty(r.Status, status),
		Tags:        cloneTags(r.Tags),
		Fingerprint: fingerprint,
	}
}

func (r sdkResourceRef) discovery() DiscoveredResource {
	return DiscoveredResource{
		Kind:       r.Kind,
		LogicalID:  r.LogicalID,
		Name:       r.Name,
		ProviderID: r.ProviderID,
		ARN:        r.ARN,
		Status:     r.Status,
		Tags:       cloneTags(r.Tags),
	}
}

func refKeys(ref sdkResourceRef) []string {
	var keys []string
	for _, value := range []string{ref.LogicalID, ref.Name, ref.ProviderID, ref.ARN} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		keys = append(keys, refKey(ref.Kind, value), value)
	}
	return keys
}

func refKey(kind, value string) string {
	return kind + "\x00" + strings.TrimSpace(value)
}

func iamRoleRef(role iamtypes.Role) sdkResourceRef {
	tags := iamTagsMap(role.Tags)
	return sdkResourceRef{
		Kind:       ResourceKindIAMRole,
		LogicalID:  tags[tagLogicalID],
		Name:       sdka.ToString(role.RoleName),
		ProviderID: sdka.ToString(role.RoleName),
		ARN:        sdka.ToString(role.Arn),
		Tags:       tags,
	}
}

func instanceProfileRef(profile iamtypes.InstanceProfile) sdkResourceRef {
	tags := iamTagsMap(profile.Tags)
	return sdkResourceRef{
		Kind:       ResourceKindIAMInstanceProfile,
		LogicalID:  tags[tagLogicalID],
		Name:       sdka.ToString(profile.InstanceProfileName),
		ProviderID: sdka.ToString(profile.InstanceProfileName),
		ARN:        sdka.ToString(profile.Arn),
		Tags:       tags,
	}
}

func securityGroupRef(group ec2types.SecurityGroup) sdkResourceRef {
	tags := ec2TagsMap(group.Tags)
	return sdkResourceRef{
		Kind:       ResourceKindSecurityGroup,
		LogicalID:  tags[tagLogicalID],
		Name:       sdka.ToString(group.GroupName),
		ProviderID: sdka.ToString(group.GroupId),
		ARN:        sdka.ToString(group.SecurityGroupArn),
		Tags:       tags,
	}
}

func targetGroupRef(group elbv2types.TargetGroup, tags map[string]string) sdkResourceRef {
	return sdkResourceRef{
		Kind:       ResourceKindTargetGroup,
		LogicalID:  tags[tagLogicalID],
		Name:       sdka.ToString(group.TargetGroupName),
		ProviderID: sdka.ToString(group.TargetGroupArn),
		ARN:        sdka.ToString(group.TargetGroupArn),
		Status:     string(group.TargetType),
		Tags:       tags,
	}
}

func listenerRuleRef(rule elbv2types.Rule, tags map[string]string) sdkResourceRef {
	return sdkResourceRef{
		Kind:       ResourceKindListenerRule,
		LogicalID:  tags[tagLogicalID],
		Name:       tags[tagLogicalID],
		ProviderID: sdka.ToString(rule.RuleArn),
		ARN:        sdka.ToString(rule.RuleArn),
		Status:     "priority " + sdka.ToString(rule.Priority),
		Tags:       tags,
	}
}

func launchTemplateRef(tmpl ec2types.LaunchTemplate) sdkResourceRef {
	tags := ec2TagsMap(tmpl.Tags)
	return sdkResourceRef{
		Kind:       ResourceKindLaunchTemplate,
		LogicalID:  tags[tagLogicalID],
		Name:       sdka.ToString(tmpl.LaunchTemplateName),
		ProviderID: sdka.ToString(tmpl.LaunchTemplateId),
		Status:     fmt.Sprintf("default-version %d", sdka.ToInt64(tmpl.DefaultVersionNumber)),
		Tags:       tags,
	}
}

func createTargetGroupInput(group TargetGroupAWS, tags map[string]string) *elasticloadbalancingv2.CreateTargetGroupInput {
	input := &elasticloadbalancingv2.CreateTargetGroupInput{
		Name:       sdka.String(group.Name),
		Port:       sdka.Int32(int32(group.Port)),
		Protocol:   elbv2Protocol(group.Protocol),
		TargetType: elbv2types.TargetTypeEnum(group.TargetType),
		VpcId:      sdka.String(group.VPCID),
		Tags:       elbTags(tags),
	}
	applyHealthCheck(group.HealthCheck, input)
	return input
}

func modifyTargetGroupInput(group TargetGroupAWS, arn string) *elasticloadbalancingv2.ModifyTargetGroupInput {
	input := &elasticloadbalancingv2.ModifyTargetGroupInput{TargetGroupArn: sdka.String(arn)}
	applyHealthCheck(group.HealthCheck, input)
	return input
}

func applyHealthCheck(check ir.HealthCheck, input any) {
	path := strings.TrimSpace(check.Path)
	port := ""
	if check.Port > 0 {
		port = strconv.Itoa(check.Port)
	}
	protocol := elbv2HealthProtocol(check)
	interval := secondsPtr(check.Interval)
	timeout := secondsPtr(check.Timeout)
	switch target := input.(type) {
	case *elasticloadbalancingv2.CreateTargetGroupInput:
		if path != "" {
			target.HealthCheckPath = sdka.String(path)
		}
		if port != "" {
			target.HealthCheckPort = sdka.String(port)
		}
		target.HealthCheckProtocol = protocol
		target.HealthCheckIntervalSeconds = interval
		target.HealthCheckTimeoutSeconds = timeout
	case *elasticloadbalancingv2.ModifyTargetGroupInput:
		if path != "" {
			target.HealthCheckPath = sdka.String(path)
		}
		if port != "" {
			target.HealthCheckPort = sdka.String(port)
		}
		target.HealthCheckProtocol = protocol
		target.HealthCheckIntervalSeconds = interval
		target.HealthCheckTimeoutSeconds = timeout
	}
}

func listenerRuleShape(rule ListenerRule, targetARN string) ([]elbv2types.Action, []elbv2types.RuleCondition) {
	actions := []elbv2types.Action{{
		Type:           elbv2types.ActionTypeEnumForward,
		TargetGroupArn: sdka.String(targetARN),
	}}
	var conditions []elbv2types.RuleCondition
	if host := strings.TrimSpace(rule.Host); host != "" {
		conditions = append(conditions, elbv2types.RuleCondition{
			Field:            sdka.String("host-header"),
			HostHeaderConfig: &elbv2types.HostHeaderConditionConfig{Values: []string{host}},
		})
	} else {
		conditions = append(conditions, elbv2types.RuleCondition{
			Field:             sdka.String("path-pattern"),
			PathPatternConfig: &elbv2types.PathPatternConditionConfig{Values: []string{"/*"}},
		})
	}
	return actions, conditions
}

type asgInputPair struct {
	create          *autoscaling.CreateAutoScalingGroupInput
	update          *autoscaling.UpdateAutoScalingGroupInput
	targetGroupARNs []string
}

func asgShape(group AutoScalingGroup, tags map[string]string, launchTemplateID string, targetGroupARNs []string) asgInputPair {
	minSize := int32(group.MinSize)
	maxSize := int32(group.MaxSize)
	desired := int32(group.DesiredCapacity)
	health := firstNonEmpty(group.HealthCheckType, "EC2")
	version := "$Default"
	vpcZones := strings.Join(group.SubnetIDs, ",")
	ltSpec := &asgtypes.LaunchTemplateSpecification{Version: sdka.String(version)}
	if strings.HasPrefix(launchTemplateID, "lt-") {
		ltSpec.LaunchTemplateId = sdka.String(launchTemplateID)
	} else {
		ltSpec.LaunchTemplateName = sdka.String(launchTemplateID)
	}
	return asgInputPair{
		create: &autoscaling.CreateAutoScalingGroupInput{
			AutoScalingGroupName: sdka.String(group.Name),
			MinSize:              sdka.Int32(minSize),
			MaxSize:              sdka.Int32(maxSize),
			DesiredCapacity:      sdka.Int32(desired),
			HealthCheckType:      sdka.String(health),
			LaunchTemplate:       ltSpec,
			TargetGroupARNs:      targetGroupARNs,
			VPCZoneIdentifier:    sdka.String(vpcZones),
			Tags:                 asgTags(group.Name, tags),
		},
		update: &autoscaling.UpdateAutoScalingGroupInput{
			MinSize:           sdka.Int32(minSize),
			MaxSize:           sdka.Int32(maxSize),
			DesiredCapacity:   sdka.Int32(desired),
			HealthCheckType:   sdka.String(health),
			LaunchTemplate:    ltSpec,
			VPCZoneIdentifier: sdka.String(vpcZones),
		},
		targetGroupARNs: targetGroupARNs,
	}
}

func ec2Tags(tags map[string]string) []ec2types.Tag {
	keys := sortedTagKeys(tags)
	out := make([]ec2types.Tag, 0, len(keys))
	for _, key := range keys {
		out = append(out, ec2types.Tag{Key: sdka.String(key), Value: sdka.String(tags[key])})
	}
	return out
}

func iamTags(tags map[string]string) []iamtypes.Tag {
	keys := sortedTagKeys(tags)
	out := make([]iamtypes.Tag, 0, len(keys))
	for _, key := range keys {
		out = append(out, iamtypes.Tag{Key: sdka.String(key), Value: sdka.String(tags[key])})
	}
	return out
}

func elbTags(tags map[string]string) []elbv2types.Tag {
	keys := sortedTagKeys(tags)
	out := make([]elbv2types.Tag, 0, len(keys))
	for _, key := range keys {
		out = append(out, elbv2types.Tag{Key: sdka.String(key), Value: sdka.String(tags[key])})
	}
	return out
}

func asgTags(name string, tags map[string]string) []asgtypes.Tag {
	keys := sortedTagKeys(tags)
	out := make([]asgtypes.Tag, 0, len(keys))
	for _, key := range keys {
		out = append(out, asgtypes.Tag{
			Key:               sdka.String(key),
			Value:             sdka.String(tags[key]),
			ResourceId:        sdka.String(name),
			ResourceType:      sdka.String("auto-scaling-group"),
			PropagateAtLaunch: sdka.Bool(true),
		})
	}
	return out
}

func ec2TagsMap(tags []ec2types.Tag) map[string]string {
	out := map[string]string{}
	for _, tag := range tags {
		if key := sdka.ToString(tag.Key); key != "" {
			out[key] = sdka.ToString(tag.Value)
		}
	}
	return out
}

func iamTagsMap(tags []iamtypes.Tag) map[string]string {
	out := map[string]string{}
	for _, tag := range tags {
		if key := sdka.ToString(tag.Key); key != "" {
			out[key] = sdka.ToString(tag.Value)
		}
	}
	return out
}

func elbTagsMap(tags []elbv2types.Tag) map[string]string {
	out := map[string]string{}
	for _, tag := range tags {
		if key := sdka.ToString(tag.Key); key != "" {
			out[key] = sdka.ToString(tag.Value)
		}
	}
	return out
}

func asgTagsMap(tags []asgtypes.TagDescription) map[string]string {
	out := map[string]string{}
	for _, tag := range tags {
		if key := sdka.ToString(tag.Key); key != "" {
			out[key] = sdka.ToString(tag.Value)
		}
	}
	return out
}

func sortedTagKeys(tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func ec2Filters(filters []TagFilter) []ec2types.Filter {
	out := make([]ec2types.Filter, 0, len(filters))
	for _, filter := range filters {
		out = append(out, ec2types.Filter{Name: sdka.String("tag:" + filter.Key), Values: []string{filter.Value}})
	}
	return out
}

func matchesTagFilters(tags map[string]string, filters []TagFilter) bool {
	for _, filter := range filters {
		if tags[filter.Key] != filter.Value {
			return false
		}
	}
	return true
}

func targetGroupARNs(groups []elbv2types.TargetGroup) []string {
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		out = append(out, sdka.ToString(group.TargetGroupArn))
	}
	return nonEmptyStrings(out)
}

func ruleARNs(rules []elbv2types.Rule) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, sdka.ToString(rule.RuleArn))
	}
	return nonEmptyStrings(out)
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func elbv2Protocol(value string) elbv2types.ProtocolEnum {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "HTTPS":
		return elbv2types.ProtocolEnumHttps
	case "TCP":
		return elbv2types.ProtocolEnumTcp
	case "TLS":
		return elbv2types.ProtocolEnumTls
	case "UDP":
		return elbv2types.ProtocolEnumUdp
	default:
		return elbv2types.ProtocolEnumHttp
	}
}

func elbv2HealthProtocol(check ir.HealthCheck) elbv2types.ProtocolEnum {
	switch strings.ToLower(strings.TrimSpace(check.Type)) {
	case "tcp":
		return elbv2types.ProtocolEnumTcp
	case "https":
		return elbv2types.ProtocolEnumHttps
	default:
		return elbv2types.ProtocolEnumHttp
	}
}

func sdkNotFound(err error) bool {
	return sdkErrorMatches(err, "notfound", "not found", "nosuchentity", "notfoundexception", "resourcenotfoundexception", "invalidlaunchtemplatename.notfoundexception", "targetgroupnotfound", "rule not found")
}

func sdkAlreadyExists(err error) bool {
	return sdkErrorMatches(err, "alreadyexists", "already exists", "entityalreadyexists", "resourcealreadyexistsexception", "duplicate")
}

func sdkDuplicate(err error) bool {
	return sdkErrorMatches(err, "duplicate", "invalidpermission.duplicate", "already exists")
}

func sdkErrorMatches(err error, needles ...string) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	values := []string{strings.ToLower(err.Error())}
	if errors.As(err, &apiErr) {
		values = append(values, strings.ToLower(apiErr.ErrorCode()), strings.ToLower(apiErr.ErrorMessage()))
	}
	for _, value := range values {
		for _, needle := range needles {
			if strings.Contains(value, strings.ToLower(needle)) {
				return true
			}
		}
	}
	return false
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return sdka.String(value)
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func hashPriority(value string) int32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return int32(100 + h.Sum32()%49000)
}

func secondsPtr(value string) *int32 {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return nil
	}
	seconds := int32(duration / time.Second)
	if seconds <= 0 {
		return nil
	}
	return sdka.Int32(seconds)
}

var _ ServiceResourceManager = (*SDKServiceResourceManager)(nil)
var _ AutoScalingClient = (*SDKServiceResourceManager)(nil)
var _ EC2Client = (*SDKServiceResourceManager)(nil)
var _ ELBV2Client = (*SDKServiceResourceManager)(nil)
var _ IAMClient = (*SDKServiceResourceManager)(nil)
var _ LogsClient = (*SDKServiceResourceManager)(nil)
