package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"

	sdka "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	asgtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"

	"github.com/s1liconcow/skiff/internal/provider"
)

func TestSDKApplyLaunchTemplateLowersRequest(t *testing.T) {
	fake := &fakeEC2Live{}
	manager := NewSDKServiceResourceManager(nil, fake, nil, nil, nil)
	manager.remember(sdkResourceRef{
		Kind:       ResourceKindSecurityGroup,
		LogicalID:  "security-group:payments-api",
		Name:       "payments-sg",
		ProviderID: "sg-123",
	})

	tmpl := LaunchTemplate{
		LogicalID:          "instance-template:payments-api",
		Name:               "skiff-prod-payments-lt",
		AMIID:              "ami-123",
		InstanceType:       "t3.small",
		IAMInstanceProfile: "skiff-prod-payments-role",
		SecurityGroupRefs:  []string{"security-group:payments-api"},
		UserData:           "#cloud-config\nruncmd: []\n",
		Tags:               map[string]string{"skiff.dev/service": "payments-api", "skiff.dev/env": "prod"},
	}
	desired, err := desiredServiceResource(ResourceKindLaunchTemplate, tmpl.LogicalID, tmpl.Name, tmpl.Tags, "launch template", tmpl)
	if err != nil {
		t.Fatal(err)
	}

	applied, err := manager.ApplyResource(context.Background(), desired)
	if err != nil {
		t.Fatalf("ApplyResource() error = %v", err)
	}
	if applied.ProviderID != "lt-123" || applied.Name != tmpl.Name {
		t.Fatalf("applied = %+v", applied)
	}
	if fake.createdLaunchTemplate == nil {
		t.Fatal("CreateLaunchTemplate was not called")
	}
	data := fake.createdLaunchTemplate.LaunchTemplateData
	if data == nil {
		t.Fatal("launch template data was nil")
	}
	if got := sdka.ToString(data.ImageId); got != "ami-123" {
		t.Fatalf("ImageId = %q", got)
	}
	if got := string(data.InstanceType); got != "t3.small" {
		t.Fatalf("InstanceType = %q", got)
	}
	if !reflect.DeepEqual(data.SecurityGroupIds, []string{"sg-123"}) {
		t.Fatalf("SecurityGroupIds = %#v", data.SecurityGroupIds)
	}
	if got := sdka.ToString(data.IamInstanceProfile.Name); got != "skiff-prod-payments-role" {
		t.Fatalf("IamInstanceProfile.Name = %q", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(sdka.ToString(data.UserData))
	if err != nil {
		t.Fatalf("UserData is not base64: %v", err)
	}
	if string(decoded) != tmpl.UserData {
		t.Fatalf("UserData decoded = %q", string(decoded))
	}
	if !hasEC2Tag(fake.createdLaunchTemplate.TagSpecifications[0].Tags, tagFingerprint, desired.Fingerprint) {
		t.Fatalf("launch template tags missing fingerprint: %+v", fake.createdLaunchTemplate.TagSpecifications[0].Tags)
	}
	if !hasEC2Tag(data.TagSpecifications[0].Tags, "skiff.dev/service", "payments-api") {
		t.Fatalf("instance tags missing service tag: %+v", data.TagSpecifications[0].Tags)
	}
}

func TestSDKApplyAutoScalingGroupLowersRequestAndTargetGroups(t *testing.T) {
	fake := &fakeASGLive{}
	manager := NewSDKServiceResourceManager(fake, nil, nil, nil, nil)
	manager.remember(sdkResourceRef{Kind: ResourceKindLaunchTemplate, LogicalID: "instance-template:payments-api", ProviderID: "lt-123"})
	manager.remember(sdkResourceRef{Kind: ResourceKindTargetGroup, LogicalID: "target-group:payments-api", ProviderID: "arn:aws:elasticloadbalancing:tg/payments", ARN: "arn:aws:elasticloadbalancing:tg/payments"})

	group := AutoScalingGroup{
		LogicalID:         "autoscaling-group:payments-api",
		Name:              "skiff-prod-payments-asg",
		MinSize:           2,
		MaxSize:           4,
		DesiredCapacity:   2,
		LaunchTemplateRef: "instance-template:payments-api",
		SubnetIDs:         []string{"subnet-a", "subnet-b"},
		TargetGroupRefs:   []string{"target-group:payments-api"},
		HealthCheckType:   "ELB",
		Tags:              map[string]string{"skiff.dev/service": "payments-api", "skiff.dev/env": "prod"},
	}
	desired, err := desiredServiceResource(ResourceKindAutoScalingGroup, group.LogicalID, group.Name, group.Tags, "asg", group)
	if err != nil {
		t.Fatal(err)
	}

	applied, err := manager.ApplyResource(context.Background(), desired)
	if err != nil {
		t.Fatalf("ApplyResource() error = %v", err)
	}
	if applied.ProviderID != group.Name {
		t.Fatalf("applied ProviderID = %q", applied.ProviderID)
	}
	if fake.created == nil {
		t.Fatal("CreateAutoScalingGroup was not called")
	}
	if got := sdka.ToString(fake.created.LaunchTemplate.LaunchTemplateId); got != "lt-123" {
		t.Fatalf("launch template id = %q", got)
	}
	if got := sdka.ToString(fake.created.VPCZoneIdentifier); got != "subnet-a,subnet-b" {
		t.Fatalf("VPCZoneIdentifier = %q", got)
	}
	if !reflect.DeepEqual(fake.attachedTargetGroups, []string{"arn:aws:elasticloadbalancing:tg/payments"}) {
		t.Fatalf("attached target groups = %#v", fake.attachedTargetGroups)
	}
	if !hasASGTag(fake.updatedTags, tagLogicalID, group.LogicalID) || !hasASGTag(fake.updatedTags, tagFingerprint, desired.Fingerprint) {
		t.Fatalf("asg tags missing Skiff metadata: %+v", fake.updatedTags)
	}
}

func TestSDKPlanResourceUsesFingerprintTag(t *testing.T) {
	fake := &fakeEC2Live{
		launchTemplates: []ec2types.LaunchTemplate{{
			LaunchTemplateId:   sdka.String("lt-123"),
			LaunchTemplateName: sdka.String("skiff-prod-payments-lt"),
		}},
	}
	manager := NewSDKServiceResourceManager(nil, fake, nil, nil, nil)
	tmpl := LaunchTemplate{LogicalID: "instance-template:payments-api", Name: "skiff-prod-payments-lt", AMIID: "ami-123", InstanceType: "t3.small"}
	desired, err := desiredServiceResource(ResourceKindLaunchTemplate, tmpl.LogicalID, tmpl.Name, tmpl.Tags, "launch template", tmpl)
	if err != nil {
		t.Fatal(err)
	}
	fake.launchTemplates[0].Tags = []ec2types.Tag{
		{Key: sdka.String(tagLogicalID), Value: sdka.String(tmpl.LogicalID)},
		{Key: sdka.String(tagFingerprint), Value: sdka.String(desired.Fingerprint)},
	}

	plan, err := manager.PlanResource(context.Background(), desired)
	if err != nil {
		t.Fatalf("PlanResource() error = %v", err)
	}
	if plan.Action != provider.ActionNoop || plan.ProviderID != "lt-123" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestClassifyErrorHandlesSmithyThrottle(t *testing.T) {
	err := ClassifyError("sdk_call", &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"})
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("ClassifyError() = %T, want provider.Error", err)
	}
	if providerErr.Code != provider.CodeThrottled {
		t.Fatalf("code = %s, want %s", providerErr.Code, provider.CodeThrottled)
	}
}

type fakeEC2Live struct {
	launchTemplates       []ec2types.LaunchTemplate
	createdLaunchTemplate *ec2.CreateLaunchTemplateInput
}

func (f *fakeEC2Live) DescribeSecurityGroups(context.Context, *ec2.DescribeSecurityGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return &ec2.DescribeSecurityGroupsOutput{}, nil
}

func (f *fakeEC2Live) CreateSecurityGroup(context.Context, *ec2.CreateSecurityGroupInput, ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	return nil, errors.New("unexpected CreateSecurityGroup")
}

func (f *fakeEC2Live) AuthorizeSecurityGroupIngress(context.Context, *ec2.AuthorizeSecurityGroupIngressInput, ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	return nil, errors.New("unexpected AuthorizeSecurityGroupIngress")
}

func (f *fakeEC2Live) AuthorizeSecurityGroupEgress(context.Context, *ec2.AuthorizeSecurityGroupEgressInput, ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupEgressOutput, error) {
	return nil, errors.New("unexpected AuthorizeSecurityGroupEgress")
}

func (f *fakeEC2Live) DescribeLaunchTemplates(_ context.Context, input *ec2.DescribeLaunchTemplatesInput, _ ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
	if len(f.launchTemplates) == 0 {
		return &ec2.DescribeLaunchTemplatesOutput{}, nil
	}
	if len(input.LaunchTemplateNames) == 0 {
		return &ec2.DescribeLaunchTemplatesOutput{LaunchTemplates: f.launchTemplates}, nil
	}
	var out []ec2types.LaunchTemplate
	for _, tmpl := range f.launchTemplates {
		if sdka.ToString(tmpl.LaunchTemplateName) == input.LaunchTemplateNames[0] {
			out = append(out, tmpl)
		}
	}
	return &ec2.DescribeLaunchTemplatesOutput{LaunchTemplates: out}, nil
}

func (f *fakeEC2Live) CreateLaunchTemplate(_ context.Context, input *ec2.CreateLaunchTemplateInput, _ ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error) {
	f.createdLaunchTemplate = input
	tmpl := ec2types.LaunchTemplate{
		LaunchTemplateId:   sdka.String("lt-123"),
		LaunchTemplateName: input.LaunchTemplateName,
	}
	if len(input.TagSpecifications) > 0 {
		tmpl.Tags = input.TagSpecifications[0].Tags
	}
	f.launchTemplates = []ec2types.LaunchTemplate{tmpl}
	return &ec2.CreateLaunchTemplateOutput{LaunchTemplate: &tmpl}, nil
}

func (f *fakeEC2Live) CreateLaunchTemplateVersion(context.Context, *ec2.CreateLaunchTemplateVersionInput, ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
	return nil, errors.New("unexpected CreateLaunchTemplateVersion")
}

func (f *fakeEC2Live) ModifyLaunchTemplate(context.Context, *ec2.ModifyLaunchTemplateInput, ...func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error) {
	return nil, errors.New("unexpected ModifyLaunchTemplate")
}

func (f *fakeEC2Live) CreateTags(context.Context, *ec2.CreateTagsInput, ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return &ec2.CreateTagsOutput{}, nil
}

type fakeASGLive struct {
	created              *autoscaling.CreateAutoScalingGroupInput
	updatedTags          []asgtypes.Tag
	attachedTargetGroups []string
}

func (f *fakeASGLive) DescribeAutoScalingGroups(_ context.Context, input *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	if f.created == nil || len(input.AutoScalingGroupNames) == 0 {
		return &autoscaling.DescribeAutoScalingGroupsOutput{}, nil
	}
	name := sdka.ToString(f.created.AutoScalingGroupName)
	if input.AutoScalingGroupNames[0] != name {
		return &autoscaling.DescribeAutoScalingGroupsOutput{}, nil
	}
	return &autoscaling.DescribeAutoScalingGroupsOutput{AutoScalingGroups: []asgtypes.AutoScalingGroup{{
		AutoScalingGroupName: sdka.String(name),
		AutoScalingGroupARN:  sdka.String("arn:aws:autoscaling:asg/" + name),
		Tags:                 asgTagDescriptions(f.updatedTags),
	}}}, nil
}

func (f *fakeASGLive) CreateAutoScalingGroup(_ context.Context, input *autoscaling.CreateAutoScalingGroupInput, _ ...func(*autoscaling.Options)) (*autoscaling.CreateAutoScalingGroupOutput, error) {
	f.created = input
	return &autoscaling.CreateAutoScalingGroupOutput{}, nil
}

func (f *fakeASGLive) UpdateAutoScalingGroup(context.Context, *autoscaling.UpdateAutoScalingGroupInput, ...func(*autoscaling.Options)) (*autoscaling.UpdateAutoScalingGroupOutput, error) {
	return nil, errors.New("unexpected UpdateAutoScalingGroup")
}

func (f *fakeASGLive) AttachLoadBalancerTargetGroups(_ context.Context, input *autoscaling.AttachLoadBalancerTargetGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.AttachLoadBalancerTargetGroupsOutput, error) {
	f.attachedTargetGroups = append([]string(nil), input.TargetGroupARNs...)
	return &autoscaling.AttachLoadBalancerTargetGroupsOutput{}, nil
}

func (f *fakeASGLive) CreateOrUpdateTags(_ context.Context, input *autoscaling.CreateOrUpdateTagsInput, _ ...func(*autoscaling.Options)) (*autoscaling.CreateOrUpdateTagsOutput, error) {
	f.updatedTags = append([]asgtypes.Tag(nil), input.Tags...)
	return &autoscaling.CreateOrUpdateTagsOutput{}, nil
}

func hasEC2Tag(tags []ec2types.Tag, key, value string) bool {
	for _, tag := range tags {
		if sdka.ToString(tag.Key) == key && sdka.ToString(tag.Value) == value {
			return true
		}
	}
	return false
}

func hasASGTag(tags []asgtypes.Tag, key, value string) bool {
	for _, tag := range tags {
		if sdka.ToString(tag.Key) == key && sdka.ToString(tag.Value) == value {
			return true
		}
	}
	return false
}

func asgTagDescriptions(tags []asgtypes.Tag) []asgtypes.TagDescription {
	out := make([]asgtypes.TagDescription, 0, len(tags))
	for _, tag := range tags {
		out = append(out, asgtypes.TagDescription{
			Key:   tag.Key,
			Value: tag.Value,
		})
	}
	return out
}

var _ sdkEC2API = (*fakeEC2Live)(nil)
var _ sdkAutoScalingAPI = (*fakeASGLive)(nil)
