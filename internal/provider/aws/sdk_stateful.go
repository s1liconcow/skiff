package aws

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	sdka "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type sdkStatefulEC2API interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	RunInstances(context.Context, *ec2.RunInstancesInput, ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	CreateVolume(context.Context, *ec2.CreateVolumeInput, ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error)
	AttachVolume(context.Context, *ec2.AttachVolumeInput, ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error)
	DetachVolume(context.Context, *ec2.DetachVolumeInput, ...func(*ec2.Options)) (*ec2.DetachVolumeOutput, error)
	CreateSnapshot(context.Context, *ec2.CreateSnapshotInput, ...func(*ec2.Options)) (*ec2.CreateSnapshotOutput, error)
	CreateTags(context.Context, *ec2.CreateTagsInput, ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
}

func (m *SDKServiceResourceManager) ApplyStatefulResource(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	switch desired.Kind {
	case ResourceKindEC2Instance:
		return m.applyStatefulInstance(ctx, desired)
	case ResourceKindEBSVolume:
		return m.applyEBSVolume(ctx, desired)
	case ResourceKindEBSAttachment:
		return m.applyEBSAttachment(ctx, desired)
	case ResourceKindSnapshotPolicy:
		return m.applySnapshotPolicy(desired)
	case ResourceKindFencingPolicy:
		return m.applyFencingPolicy(desired)
	case ResourceKindRoute53Record:
		return nil, provider.Unsupported(Name, "live apply "+desired.Kind)
	default:
		return nil, provider.Unsupported(Name, "live apply "+desired.Kind)
	}
}

func (m *SDKServiceResourceManager) FindStatefulResources(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error) {
	client, err := m.statefulEC2("discover_stateful_resources")
	if err != nil {
		return nil, err
	}
	var out []DiscoveredResource
	var instanceToken *string
	for {
		resp, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: ec2Filters(filters), NextToken: instanceToken})
		if err != nil {
			return nil, err
		}
		for _, reservation := range resp.Reservations {
			for _, instance := range reservation.Instances {
				tags := ec2TagsMap(instance.Tags)
				ref := sdkResourceRef{
					Kind:       ResourceKindEC2Instance,
					LogicalID:  tags[tagLogicalID],
					Name:       tags["Name"],
					ProviderID: sdka.ToString(instance.InstanceId),
					Status:     string(instance.State.Name),
					Tags:       tags,
				}
				m.remember(ref)
				out = append(out, ref.discovery())
			}
		}
		instanceToken = resp.NextToken
		if instanceToken == nil || *instanceToken == "" {
			break
		}
	}
	var volumeToken *string
	for {
		resp, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{Filters: ec2Filters(filters), NextToken: volumeToken})
		if err != nil {
			return nil, err
		}
		for _, volume := range resp.Volumes {
			tags := ec2TagsMap(volume.Tags)
			volumeID := sdka.ToString(volume.VolumeId)
			ref := sdkResourceRef{
				Kind:       ResourceKindEBSVolume,
				LogicalID:  tags[tagLogicalID],
				Name:       tags["Name"],
				ProviderID: volumeID,
				Status:     string(volume.State),
				Tags:       tags,
			}
			m.remember(ref)
			out = append(out, ref.discovery())
			for _, attachment := range volume.Attachments {
				instanceID := sdka.ToString(attachment.InstanceId)
				if instanceID == "" {
					continue
				}
				out = append(out, DiscoveredResource{
					Kind:       ResourceKindEBSAttachment,
					LogicalID:  tags[tagLogicalID] + ":attachment",
					Name:       volumeID + ":" + instanceID,
					ProviderID: volumeID + ":" + instanceID,
					Status:     string(attachment.State),
					Tags:       tags,
				})
			}
		}
		volumeToken = resp.NextToken
		if volumeToken == nil || *volumeToken == "" {
			break
		}
	}
	return out, nil
}

func (m *SDKServiceResourceManager) FenceInstance(ctx context.Context, req provider.FenceInstanceRequest) (*provider.FenceInstanceResult, error) {
	client, err := m.statefulEC2("fence_stateful_instance")
	if err != nil {
		return nil, err
	}
	if _, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{req.InstanceID}}); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &provider.FenceInstanceResult{
		ProviderOperation: statefulProviderOperation(StatefulOperationFenceInstance, statefulOperationID("terminate", req.InstanceID), statefulOperationDescription("terminated instance "+req.InstanceID, req.Ref), now),
		FencedAt:          now,
	}, nil
}

func (m *SDKServiceResourceManager) DetachVolume(ctx context.Context, req provider.DetachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	client, err := m.statefulEC2("detach_stateful_volume")
	if err != nil {
		return nil, err
	}
	input := &ec2.DetachVolumeInput{VolumeId: sdka.String(req.VolumeID)}
	if req.InstanceID != "" {
		input.InstanceId = sdka.String(req.InstanceID)
	}
	if _, err := client.DetachVolume(ctx, input); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &provider.VolumeAttachmentResult{
		ProviderOperation: statefulProviderOperation(StatefulOperationDetachVolume, statefulOperationID("detach", req.VolumeID, req.InstanceID), statefulOperationDescription("detached volume "+req.VolumeID, req.Ref), now),
		VolumeID:          req.VolumeID,
		InstanceID:        req.InstanceID,
		CompletedAt:       now,
	}, nil
}

func (m *SDKServiceResourceManager) LaunchReplacement(ctx context.Context, req provider.LaunchReplacementRequest) (*provider.ReplacementInstance, error) {
	client, err := m.statefulEC2("launch_stateful_replacement")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.PreviousID) == "" {
		return nil, validationError("launch_stateful_replacement", "previous_id is required to clone the prior AWS instance shape")
	}
	previous, err := m.describeInstance(ctx, client, req.PreviousID)
	if err != nil {
		return nil, err
	}
	input := runReplacementInput(previous, req)
	resp, err := client.RunInstances(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(resp.Instances) == 0 || sdka.ToString(resp.Instances[0].InstanceId) == "" {
		return nil, &provider.Error{Code: provider.CodeProvider, Provider: Name, Op: "launch_stateful_replacement", Summary: "EC2 RunInstances returned no instance ID"}
	}
	instance := resp.Instances[0]
	instanceID := sdka.ToString(instance.InstanceId)
	ref := sdkResourceRef{
		Kind:       ResourceKindEC2Instance,
		LogicalID:  replacementLogicalID(req.Ref),
		Name:       replacementName(req),
		ProviderID: instanceID,
		Status:     string(instance.State.Name),
		Tags:       statefulOperationTags(req.Ref, replacementLogicalID(req.Ref), replacementName(req)),
	}
	m.remember(ref)
	now := time.Now().UTC()
	return &provider.ReplacementInstance{
		ProviderOperation: statefulProviderOperation(StatefulOperationLaunchReplacement, statefulOperationID("run-instances", instanceID), statefulOperationDescription("launched replacement instance "+instanceID, req.Ref), now),
		InstanceID:        instanceID,
		Zone:              firstNonEmpty(sdka.ToString(instance.Placement.AvailabilityZone), req.Zone),
		LaunchedAt:        now,
	}, nil
}

func (m *SDKServiceResourceManager) AttachVolume(ctx context.Context, req provider.AttachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	client, err := m.statefulEC2("attach_stateful_volume")
	if err != nil {
		return nil, err
	}
	device := firstNonEmpty(req.Device, "/dev/xvdf")
	if _, err := client.AttachVolume(ctx, &ec2.AttachVolumeInput{InstanceId: sdka.String(req.InstanceID), VolumeId: sdka.String(req.VolumeID), Device: sdka.String(device)}); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &provider.VolumeAttachmentResult{
		ProviderOperation: statefulProviderOperation(StatefulOperationAttachVolume, statefulOperationID("attach", req.VolumeID, req.InstanceID), statefulOperationDescription("attached volume "+req.VolumeID, req.Ref), now),
		VolumeID:          req.VolumeID,
		InstanceID:        req.InstanceID,
		CompletedAt:       now,
	}, nil
}

func (m *SDKServiceResourceManager) UpdateMemberDNS(ctx context.Context, req provider.UpdateMemberDNSRequest) (*provider.DNSUpdateResult, error) {
	return nil, provider.Unsupported(Name, "stateful update dns requires a Route53 client")
}

func (m *SDKServiceResourceManager) SnapshotVolume(ctx context.Context, req provider.SnapshotVolumeRequest) (*provider.VolumeSnapshot, error) {
	client, err := m.statefulEC2("snapshot_stateful_volume")
	if err != nil {
		return nil, err
	}
	tags := statefulOperationTags(req.Ref, "stateful-volume-snapshot:"+req.Ref.Group+":"+strconv.Itoa(req.Ref.Member), req.Ref.Group+"-"+strconv.Itoa(req.Ref.Member)+"-snapshot")
	resp, err := client.CreateSnapshot(ctx, &ec2.CreateSnapshotInput{
		VolumeId:          sdka.String(req.VolumeID),
		Description:       sdka.String(firstNonEmpty(req.Reason, "Skiff stateful volume snapshot")),
		TagSpecifications: []ec2types.TagSpecification{{ResourceType: ec2types.ResourceTypeSnapshot, Tags: ec2Tags(tags)}},
	})
	if err != nil {
		return nil, err
	}
	snapshotID := sdka.ToString(resp.SnapshotId)
	now := time.Now().UTC()
	return &provider.VolumeSnapshot{
		ProviderOperation: statefulProviderOperation(StatefulOperationSnapshotVolume, statefulOperationID("snapshot", snapshotID), statefulOperationDescription("created snapshot "+snapshotID, req.Ref), now),
		SnapshotID:        snapshotID,
		VolumeID:          req.VolumeID,
		CreatedAt:         now,
	}, nil
}

func (m *SDKServiceResourceManager) applyStatefulInstance(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	client, err := m.statefulEC2("apply_stateful_instance")
	if err != nil {
		return nil, err
	}
	var member StatefulMemberAWS
	if err := decodeDesired(desired, &member); err != nil {
		return nil, err
	}
	launchTemplateID := m.resolveID(ResourceKindLaunchTemplate, member.LaunchTemplateRef)
	if launchTemplateID == "" {
		launchTemplateID = member.LaunchTemplateRef
	}
	if strings.TrimSpace(launchTemplateID) == "" {
		return nil, validationError("apply_stateful_instance", "launch template ref is required")
	}
	input := &ec2.RunInstancesInput{
		LaunchTemplate:    &ec2types.LaunchTemplateSpecification{LaunchTemplateId: sdka.String(launchTemplateID)},
		MaxCount:          sdka.Int32(1),
		MinCount:          sdka.Int32(1),
		TagSpecifications: []ec2types.TagSpecification{{ResourceType: ec2types.ResourceTypeInstance, Tags: ec2Tags(tagsWithFingerprint(desired))}},
	}
	if len(member.SubnetIDs) > 0 {
		input.SubnetId = sdka.String(member.SubnetIDs[0])
	}
	if member.Zone != "" {
		input.Placement = &ec2types.Placement{AvailabilityZone: sdka.String(member.Zone)}
	}
	resp, err := client.RunInstances(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(resp.Instances) == 0 || sdka.ToString(resp.Instances[0].InstanceId) == "" {
		return nil, &provider.Error{Code: provider.CodeProvider, Provider: Name, Op: "apply_stateful_instance", Resource: desired.LogicalID, Summary: "EC2 RunInstances returned no instance ID"}
	}
	instance := resp.Instances[0]
	ref := sdkResourceRef{
		Kind:       ResourceKindEC2Instance,
		LogicalID:  member.LogicalID,
		Name:       member.Name,
		ProviderID: sdka.ToString(instance.InstanceId),
		Status:     firstNonEmpty(string(instance.State.Name), "pending"),
		Tags:       tagsWithFingerprint(desired),
	}
	m.remember(ref)
	return ref.applied(ref.Status, desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applyEBSVolume(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	client, err := m.statefulEC2("apply_ebs_volume")
	if err != nil {
		return nil, err
	}
	var volume EBSVolume
	if err := decodeDesired(desired, &volume); err != nil {
		return nil, err
	}
	if strings.TrimSpace(volume.AvailabilityZone) == "" {
		return nil, validationError("apply_ebs_volume", "availability_zone is required")
	}
	size, err := ebsSizeGiB(volume.Size)
	if err != nil {
		return nil, err
	}
	input := &ec2.CreateVolumeInput{
		AvailabilityZone:  sdka.String(volume.AvailabilityZone),
		Size:              sdka.Int32(int32(size)),
		VolumeType:        ec2types.VolumeType(volume.VolumeType),
		Encrypted:         sdka.Bool(volume.Encrypted),
		TagSpecifications: []ec2types.TagSpecification{{ResourceType: ec2types.ResourceTypeVolume, Tags: ec2Tags(tagsWithFingerprint(desired))}},
	}
	if volume.KMSKeyID != "" {
		input.KmsKeyId = sdka.String(volume.KMSKeyID)
	}
	resp, err := client.CreateVolume(ctx, input)
	if err != nil {
		return nil, err
	}
	volumeID := sdka.ToString(resp.VolumeId)
	ref := sdkResourceRef{
		Kind:       ResourceKindEBSVolume,
		LogicalID:  volume.LogicalID,
		Name:       volume.Name,
		ProviderID: volumeID,
		Status:     firstNonEmpty(string(resp.State), "creating"),
		Tags:       tagsWithFingerprint(desired),
	}
	m.remember(ref)
	return ref.applied(ref.Status, desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applyEBSAttachment(ctx context.Context, desired DesiredServiceResource) (*AppliedResource, error) {
	client, err := m.statefulEC2("apply_ebs_attachment")
	if err != nil {
		return nil, err
	}
	var attachment VolumeAttachment
	if err := decodeDesired(desired, &attachment); err != nil {
		return nil, err
	}
	instanceID := firstNonEmpty(m.resolveID(ResourceKindEC2Instance, attachment.InstanceRef), attachment.InstanceRef)
	volumeID := firstNonEmpty(m.resolveID(ResourceKindEBSVolume, attachment.VolumeRef), attachment.VolumeRef)
	if strings.TrimSpace(instanceID) == "" || strings.TrimSpace(volumeID) == "" {
		return nil, validationError("apply_ebs_attachment", "instance and volume refs must resolve before attachment")
	}
	device := firstNonEmpty(attachment.Device, "/dev/xvdf")
	if _, err := client.AttachVolume(ctx, &ec2.AttachVolumeInput{InstanceId: sdka.String(instanceID), VolumeId: sdka.String(volumeID), Device: sdka.String(device)}); err != nil {
		return nil, err
	}
	ref := sdkResourceRef{
		Kind:       ResourceKindEBSAttachment,
		LogicalID:  attachment.LogicalID,
		Name:       attachment.Name,
		ProviderID: volumeID + ":" + instanceID,
		Status:     "attaching",
		Tags:       tagsWithFingerprint(desired),
	}
	m.remember(ref)
	return ref.applied(ref.Status, desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applySnapshotPolicy(desired DesiredServiceResource) (*AppliedResource, error) {
	var policy SnapshotPolicyAWS
	if err := decodeDesired(desired, &policy); err != nil {
		return nil, err
	}
	return (&sdkResourceRef{
		Kind:       ResourceKindSnapshotPolicy,
		LogicalID:  policy.LogicalID,
		Name:       policy.Name,
		ProviderID: "snapshot-policy/" + policy.Name,
		Status:     firstNonEmpty(boolStatus(policy.Enabled), "disabled"),
		Tags:       tagsWithFingerprint(desired),
	}).applied("retention-planned", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) applyFencingPolicy(desired DesiredServiceResource) (*AppliedResource, error) {
	var policy FencingPolicyAWS
	if err := decodeDesired(desired, &policy); err != nil {
		return nil, err
	}
	return (&sdkResourceRef{
		Kind:       ResourceKindFencingPolicy,
		LogicalID:  policy.LogicalID,
		Name:       policy.Name,
		ProviderID: "fencing-policy/" + policy.Name,
		Status:     "configured",
		Tags:       tagsWithFingerprint(desired),
	}).applied("configured", desired.Fingerprint), nil
}

func (m *SDKServiceResourceManager) statefulEC2(op string) (sdkStatefulEC2API, error) {
	if m.ec2 == nil {
		return nil, missingClients(op)
	}
	client, ok := m.ec2.(sdkStatefulEC2API)
	if !ok {
		return nil, &provider.Error{Code: provider.CodeUnsupported, Provider: Name, Op: op, Summary: "aws EC2 client does not implement stateful lifecycle operations"}
	}
	return client, nil
}

func (m *SDKServiceResourceManager) describeInstance(ctx context.Context, client sdkStatefulEC2API, instanceID string) (ec2types.Instance, error) {
	resp, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return ec2types.Instance{}, err
	}
	for _, reservation := range resp.Reservations {
		for _, instance := range reservation.Instances {
			if sdka.ToString(instance.InstanceId) == instanceID {
				return instance, nil
			}
		}
	}
	return ec2types.Instance{}, &provider.Error{Code: provider.CodeNotFound, Provider: Name, Op: "describe_instance", Resource: instanceID, Summary: "previous stateful instance was not found"}
}

func runReplacementInput(previous ec2types.Instance, req provider.LaunchReplacementRequest) *ec2.RunInstancesInput {
	tags := statefulOperationTags(req.Ref, replacementLogicalID(req.Ref), replacementName(req))
	input := &ec2.RunInstancesInput{
		ImageId:           previous.ImageId,
		InstanceType:      previous.InstanceType,
		MaxCount:          sdka.Int32(1),
		MinCount:          sdka.Int32(1),
		SubnetId:          previous.SubnetId,
		TagSpecifications: []ec2types.TagSpecification{{ResourceType: ec2types.ResourceTypeInstance, Tags: ec2Tags(tags)}},
	}
	if req.Zone != "" {
		input.Placement = &ec2types.Placement{AvailabilityZone: sdka.String(req.Zone)}
	} else if previous.Placement.AvailabilityZone != nil {
		input.Placement = &ec2types.Placement{AvailabilityZone: previous.Placement.AvailabilityZone}
	}
	if previous.IamInstanceProfile != nil && previous.IamInstanceProfile.Arn != nil {
		input.IamInstanceProfile = &ec2types.IamInstanceProfileSpecification{Arn: previous.IamInstanceProfile.Arn}
	}
	for _, group := range previous.SecurityGroups {
		if id := sdka.ToString(group.GroupId); id != "" {
			input.SecurityGroupIds = append(input.SecurityGroupIds, id)
		}
	}
	return input
}

func ebsSizeGiB(value string) (int, error) {
	clean := strings.TrimSpace(strings.ToLower(value))
	clean = strings.TrimSuffix(clean, "gib")
	clean = strings.TrimSuffix(clean, "gi")
	clean = strings.TrimSuffix(clean, "gb")
	clean = strings.TrimSuffix(clean, "g")
	clean = strings.TrimSpace(clean)
	size, err := strconv.Atoi(clean)
	if err != nil || size <= 0 {
		return 0, validationError("apply_ebs_volume", "volume size must be a positive GiB value")
	}
	return size, nil
}

func statefulOperationTags(ref provider.StatefulMemberRef, logicalID, name string) map[string]string {
	tags := ir.RequiredTags(ref.Group, ref.Env)
	tags[ir.TagStatefulGroup] = ref.Group
	tags[ir.TagMemberOrdinal] = strconv.Itoa(ref.Member)
	tags[tagLogicalID] = logicalID
	if name != "" {
		tags["Name"] = name
	}
	return tags
}

func replacementLogicalID(ref provider.StatefulMemberRef) string {
	return fmt.Sprintf("stateful-member:%s:%d", ref.Group, ref.Member)
}

func replacementName(req provider.LaunchReplacementRequest) string {
	if req.IdentityHint != "" {
		return req.IdentityHint
	}
	return fmt.Sprintf("%s-%d-gen-%d", req.Ref.Group, req.Ref.Member, req.Generation)
}

func statefulProviderOperation(kind, id, description string, observedAt time.Time) schema.ProviderOperationRef {
	return schema.ProviderOperationRef{Provider: Name, Kind: kind, ID: id, ObservedAt: canonical.Time(observedAt), Description: description}
}

func boolStatus(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

var _ StatefulResourceManager = (*SDKServiceResourceManager)(nil)
var _ provider.StatefulOperations = (*SDKServiceResourceManager)(nil)
