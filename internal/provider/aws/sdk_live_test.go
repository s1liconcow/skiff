package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strconv"
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

func TestSDKApplyStatefulEC2AndEBSResources(t *testing.T) {
	fake := &fakeEC2Live{}
	manager := NewSDKServiceResourceManager(nil, fake, nil, nil, nil)
	manager.remember(sdkResourceRef{Kind: ResourceKindLaunchTemplate, LogicalID: "stateful-launch-template:orders-stream", ProviderID: "lt-stateful"})

	volume := EBSVolume{
		LogicalID:        "stateful-volume:orders-stream:0",
		Name:             "skiff-prod-orders-volume-0",
		MemberOrdinal:    0,
		AvailabilityZone: "us-west-2a",
		Size:             "20Gi",
		VolumeType:       "gp3",
		Encrypted:        true,
		KMSKeyID:         "alias/skiff-stateful",
		Tags:             map[string]string{"skiff.dev/service": "orders-stream", "skiff.dev/env": "prod"},
	}
	volumeDesired, err := desiredServiceResource(ResourceKindEBSVolume, volume.LogicalID, volume.Name, volume.Tags, "volume", volume)
	if err != nil {
		t.Fatal(err)
	}
	appliedVolume, err := manager.ApplyStatefulResource(context.Background(), volumeDesired)
	if err != nil {
		t.Fatalf("ApplyStatefulResource(volume): %v", err)
	}
	if appliedVolume.ProviderID != "vol-new" || fake.createdVolume == nil || sdka.ToInt32(fake.createdVolume.Size) != 20 || sdka.ToString(fake.createdVolume.KmsKeyId) != "alias/skiff-stateful" {
		t.Fatalf("unexpected volume apply: applied=%+v input=%+v", appliedVolume, fake.createdVolume)
	}

	member := StatefulMemberAWS{
		LogicalID:         "stateful-member:orders-stream:0",
		Name:              "skiff-prod-orders-member-0",
		MemberOrdinal:     0,
		Zone:              "us-west-2a",
		LaunchTemplateRef: "stateful-launch-template:orders-stream",
		SubnetIDs:         []string{"subnet-a"},
		Tags:              map[string]string{"skiff.dev/service": "orders-stream", "skiff.dev/env": "prod"},
	}
	memberDesired, err := desiredServiceResource(ResourceKindEC2Instance, member.LogicalID, member.Name, member.Tags, "member", member)
	if err != nil {
		t.Fatal(err)
	}
	appliedMember, err := manager.ApplyStatefulResource(context.Background(), memberDesired)
	if err != nil {
		t.Fatalf("ApplyStatefulResource(member): %v", err)
	}
	if appliedMember.ProviderID != "i-new" || len(fake.runInstances) != 1 || sdka.ToString(fake.runInstances[0].LaunchTemplate.LaunchTemplateId) != "lt-stateful" || sdka.ToString(fake.runInstances[0].SubnetId) != "subnet-a" {
		t.Fatalf("unexpected member apply: applied=%+v input=%+v", appliedMember, fake.runInstances)
	}

	attachment := VolumeAttachment{
		LogicalID:   "stateful-volume-attachment:orders-stream:0",
		Name:        "skiff-prod-orders-attach-0",
		InstanceRef: member.LogicalID,
		VolumeRef:   volume.LogicalID,
		Device:      "/dev/xvdf",
		Tags:        map[string]string{"skiff.dev/service": "orders-stream", "skiff.dev/env": "prod"},
	}
	attachmentDesired, err := desiredServiceResource(ResourceKindEBSAttachment, attachment.LogicalID, attachment.Name, attachment.Tags, "attachment", attachment)
	if err != nil {
		t.Fatal(err)
	}
	appliedAttachment, err := manager.ApplyStatefulResource(context.Background(), attachmentDesired)
	if err != nil {
		t.Fatalf("ApplyStatefulResource(attachment): %v", err)
	}
	if appliedAttachment.ProviderID != "vol-new:i-new" || fake.attachedVolume == nil || sdka.ToString(fake.attachedVolume.VolumeId) != "vol-new" || sdka.ToString(fake.attachedVolume.InstanceId) != "i-new" {
		t.Fatalf("unexpected attachment apply: applied=%+v input=%+v", appliedAttachment, fake.attachedVolume)
	}
}

func TestSDKStatefulLifecycleOperationsUseEC2IDs(t *testing.T) {
	fake := &fakeEC2Live{instances: []ec2types.Instance{{
		InstanceId:   sdka.String("i-old"),
		ImageId:      sdka.String("ami-old"),
		InstanceType: ec2types.InstanceTypeT3Small,
		SubnetId:     sdka.String("subnet-a"),
		Placement:    &ec2types.Placement{AvailabilityZone: sdka.String("us-west-2a")},
		SecurityGroups: []ec2types.GroupIdentifier{{
			GroupId: sdka.String("sg-stateful"),
		}},
		IamInstanceProfile: &ec2types.IamInstanceProfile{Arn: sdka.String("arn:aws:iam::123456789012:instance-profile/skiff-stateful")},
	}}}
	manager := NewSDKServiceResourceManager(nil, fake, nil, nil, nil)
	ref := provider.StatefulMemberRef{Group: "orders-stream", Env: "prod", Member: 0}

	fence, err := manager.FenceInstance(context.Background(), provider.FenceInstanceRequest{Ref: ref, InstanceID: "i-old"})
	if err != nil {
		t.Fatalf("FenceInstance: %v", err)
	}
	if fence.ProviderOperation.ID != "terminate:i-old" || !reflect.DeepEqual(fake.terminatedInstances, []string{"i-old"}) {
		t.Fatalf("unexpected fence result: result=%+v terminated=%+v", fence, fake.terminatedInstances)
	}
	launch, err := manager.LaunchReplacement(context.Background(), provider.LaunchReplacementRequest{Ref: ref, PreviousID: "i-old", Generation: 2, Zone: "us-west-2a"})
	if err != nil {
		t.Fatalf("LaunchReplacement: %v", err)
	}
	if launch.InstanceID != "i-new" || len(fake.runInstances) != 1 || sdka.ToString(fake.runInstances[0].ImageId) != "ami-old" || fake.runInstances[0].InstanceType != ec2types.InstanceTypeT3Small {
		t.Fatalf("unexpected replacement launch: result=%+v input=%+v", launch, fake.runInstances)
	}
	snapshot, err := manager.SnapshotVolume(context.Background(), provider.SnapshotVolumeRequest{Ref: ref, VolumeID: "vol-123"})
	if err != nil {
		t.Fatalf("SnapshotVolume: %v", err)
	}
	if snapshot.SnapshotID != "snap-new" || fake.createdSnapshot == nil || sdka.ToString(fake.createdSnapshot.VolumeId) != "vol-123" {
		t.Fatalf("unexpected snapshot: result=%+v input=%+v", snapshot, fake.createdSnapshot)
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
	instances             []ec2types.Instance
	volumes               []ec2types.Volume
	runInstances          []*ec2.RunInstancesInput
	terminatedInstances   []string
	createdVolume         *ec2.CreateVolumeInput
	attachedVolume        *ec2.AttachVolumeInput
	detachedVolume        *ec2.DetachVolumeInput
	createdSnapshot       *ec2.CreateSnapshotInput
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

func (f *fakeEC2Live) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	instances := append([]ec2types.Instance(nil), f.instances...)
	for i, run := range f.runInstances {
		instances = append(instances, ec2types.Instance{
			InstanceId:   sdka.String("i-new"),
			ImageId:      run.ImageId,
			InstanceType: run.InstanceType,
			SubnetId:     run.SubnetId,
			Placement:    run.Placement,
			State:        &ec2types.InstanceState{Name: ec2types.InstanceStateNamePending},
			Tags:         run.TagSpecifications[0].Tags,
		})
		if i > 0 {
			instances[len(instances)-1].InstanceId = sdka.String("i-new-" + strconv.Itoa(i))
		}
	}
	if len(input.InstanceIds) == 0 {
		return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: instances}}}, nil
	}
	var filtered []ec2types.Instance
	for _, instance := range instances {
		for _, id := range input.InstanceIds {
			if sdka.ToString(instance.InstanceId) == id {
				filtered = append(filtered, instance)
			}
		}
	}
	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: filtered}}}, nil
}

func (f *fakeEC2Live) RunInstances(_ context.Context, input *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	f.runInstances = append(f.runInstances, input)
	return &ec2.RunInstancesOutput{Instances: []ec2types.Instance{{
		InstanceId: sdka.String("i-new"),
		Placement:  input.Placement,
		State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNamePending},
	}}}, nil
}

func (f *fakeEC2Live) TerminateInstances(_ context.Context, input *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	f.terminatedInstances = append([]string(nil), input.InstanceIds...)
	return &ec2.TerminateInstancesOutput{}, nil
}

func (f *fakeEC2Live) DescribeVolumes(_ context.Context, input *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return &ec2.DescribeVolumesOutput{Volumes: append([]ec2types.Volume(nil), f.volumes...)}, nil
}

func (f *fakeEC2Live) CreateVolume(_ context.Context, input *ec2.CreateVolumeInput, _ ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error) {
	f.createdVolume = input
	volume := ec2types.Volume{VolumeId: sdka.String("vol-new"), State: ec2types.VolumeStateCreating, Tags: input.TagSpecifications[0].Tags}
	f.volumes = append(f.volumes, volume)
	return &ec2.CreateVolumeOutput{VolumeId: volume.VolumeId, State: volume.State}, nil
}

func (f *fakeEC2Live) AttachVolume(_ context.Context, input *ec2.AttachVolumeInput, _ ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error) {
	f.attachedVolume = input
	return &ec2.AttachVolumeOutput{}, nil
}

func (f *fakeEC2Live) DetachVolume(_ context.Context, input *ec2.DetachVolumeInput, _ ...func(*ec2.Options)) (*ec2.DetachVolumeOutput, error) {
	f.detachedVolume = input
	return &ec2.DetachVolumeOutput{}, nil
}

func (f *fakeEC2Live) CreateSnapshot(_ context.Context, input *ec2.CreateSnapshotInput, _ ...func(*ec2.Options)) (*ec2.CreateSnapshotOutput, error) {
	f.createdSnapshot = input
	return &ec2.CreateSnapshotOutput{SnapshotId: sdka.String("snap-new")}, nil
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
