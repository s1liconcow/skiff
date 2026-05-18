package aws

import (
	"fmt"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
)

type StatefulMemberAWS struct {
	LogicalID           string            `json:"logical_id"`
	Name                string            `json:"name"`
	MemberOrdinal       int               `json:"member_ordinal"`
	Zone                string            `json:"zone,omitempty"`
	DNSName             string            `json:"dns_name,omitempty"`
	LaunchTemplateRef   string            `json:"launch_template_ref"`
	IAMProfileRef       string            `json:"iam_instance_profile_ref"`
	SecurityGroupRefs   []string          `json:"security_group_refs,omitempty"`
	SubnetIDs           []string          `json:"subnet_ids,omitempty"`
	TargetGroupRefs     []string          `json:"target_group_refs,omitempty"`
	VolumeRef           string            `json:"volume_ref"`
	DNSRecordRef        string            `json:"dns_record_ref,omitempty"`
	FencingPolicyRef    string            `json:"fencing_policy_ref,omitempty"`
	ReplacementStrategy string            `json:"replacement_strategy"`
	Tags                map[string]string `json:"tags,omitempty"`
	Source              []ir.SourceRef    `json:"source,omitempty"`
}

type EBSVolume struct {
	LogicalID        string            `json:"logical_id"`
	Name             string            `json:"name"`
	MemberOrdinal    int               `json:"member_ordinal"`
	AvailabilityZone string            `json:"availability_zone,omitempty"`
	Size             string            `json:"size"`
	VolumeType       string            `json:"volume_type"`
	Encrypted        bool              `json:"encrypted"`
	KMSKeyID         string            `json:"kms_key_id,omitempty"`
	MountPath        string            `json:"mount_path"`
	DeleteOnDestroy  bool              `json:"delete_on_destroy"`
	Tags             map[string]string `json:"tags,omitempty"`
	Source           []ir.SourceRef    `json:"source,omitempty"`
}

type VolumeAttachment struct {
	LogicalID     string            `json:"logical_id"`
	Name          string            `json:"name"`
	MemberOrdinal int               `json:"member_ordinal"`
	InstanceRef   string            `json:"instance_ref"`
	VolumeRef     string            `json:"volume_ref"`
	Device        string            `json:"device"`
	MountPath     string            `json:"mount_path"`
	Tags          map[string]string `json:"tags,omitempty"`
	Source        []ir.SourceRef    `json:"source,omitempty"`
}

type Route53Record struct {
	LogicalID     string            `json:"logical_id"`
	Name          string            `json:"name"`
	MemberOrdinal int               `json:"member_ordinal"`
	DNSName       string            `json:"dns_name"`
	HostedZoneRef string            `json:"hosted_zone_ref,omitempty"`
	RecordType    string            `json:"record_type"`
	TargetRef     string            `json:"target_ref"`
	TTLSeconds    int               `json:"ttl_seconds"`
	Tags          map[string]string `json:"tags,omitempty"`
	Source        []ir.SourceRef    `json:"source,omitempty"`
}

type SnapshotPolicyAWS struct {
	LogicalID  string            `json:"logical_id"`
	Name       string            `json:"name"`
	Enabled    bool              `json:"enabled"`
	Interval   string            `json:"interval,omitempty"`
	Retention  string            `json:"retention,omitempty"`
	VolumeRefs []string          `json:"volume_refs,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Source     []ir.SourceRef    `json:"source,omitempty"`
}

type FencingPolicyAWS struct {
	LogicalID                   string            `json:"logical_id"`
	Name                        string            `json:"name"`
	MemberOrdinal               int               `json:"member_ordinal"`
	InstanceRef                 string            `json:"instance_ref"`
	VolumeRef                   string            `json:"volume_ref"`
	RequiresInstanceTermination bool              `json:"requires_instance_termination"`
	RequiresVolumeDetach        bool              `json:"requires_volume_detach"`
	Tags                        map[string]string `json:"tags,omitempty"`
	Source                      []ir.SourceRef    `json:"source,omitempty"`
}

func lowerStatefulResources(out *ServiceResources, graph *ir.Graph, opts LowerOptions, releaseID, controlKey string) error {
	if graph == nil || len(graph.Resources.StatefulGroups) == 0 {
		return nil
	}
	group := graph.Resources.StatefulGroups[0]
	tags := TagsMap(group.Meta, nil)
	roleLogicalID := "stateful-iam-role:" + graph.Service
	profileLogicalID := roleLogicalID + ":profile"
	securityGroupLogicalID := "stateful-security-group:" + graph.Service
	logicalLogID := "stateful-logs:" + graph.Service
	metricLogicalID := "stateful-metrics:" + graph.Service
	launchTemplateLogicalID := "stateful-launch-template:" + graph.Service
	targetGroupLogicalID := "stateful-target-group:" + graph.Service

	roleName, err := statefulAWSName(graph, ResourceKindIAMRole, roleLogicalID, "stateful-role")
	if err != nil {
		return err
	}
	out.IAMRoles = append(out.IAMRoles, IAMRoleResource{
		LogicalID:        roleLogicalID,
		Name:             roleName,
		AssumeRolePolicy: ec2AssumeRolePolicy(),
		InlinePolicy:     workloadPolicy(opts.StateBucket, graph.Service, controlKey, nil, nil),
		Tags:             cloneTags(tags),
		Source:           append([]ir.SourceRef(nil), group.Meta.Source...),
	})
	out.InstanceProfiles = append(out.InstanceProfiles, InstanceProfile{
		LogicalID: profileLogicalID,
		Name:      roleName,
		RoleRef:   roleLogicalID,
		Tags:      cloneTags(tags),
	})

	securityGroupName, err := statefulAWSName(graph, ResourceKindSecurityGroup, securityGroupLogicalID, "stateful-sg")
	if err != nil {
		return err
	}
	out.SecurityGroups = append(out.SecurityGroups, SecurityGroupAWS{
		LogicalID:   securityGroupLogicalID,
		Name:        securityGroupName,
		Description: "Skiff StatefulGroup security group for " + graph.Service,
		VPCID:       strings.TrimSpace(opts.VPCID),
		Ingress:     statefulIngressRules(graph.Resources.StatefulRecipes, securityGroupLogicalID),
		Egress: []SecurityGroupRule{{
			Protocol:    "-1",
			CIDR:        "0.0.0.0/0",
			Description: "allow stateful member egress",
		}},
		Tags:   cloneTags(tags),
		Source: append([]ir.SourceRef(nil), group.Meta.Source...),
	})

	out.LogGroups = append(out.LogGroups, LogGroup{
		LogicalID:     logicalLogID,
		Name:          LogGroupName(graph.Env, graph.Service),
		RetentionDays: 30,
		Tags:          cloneTags(tags),
		Source:        append([]ir.SourceRef(nil), group.Meta.Source...),
	})
	if recipe := firstStatefulRecipe(graph); recipe != nil && recipe.Metrics.Enabled {
		out.MetricConfigs = append(out.MetricConfigs, MetricConfigAWS{
			LogicalID: metricLogicalID,
			Namespace: "Skiff/" + graph.Env + "/" + graph.Service + "/stateful",
			Path:      recipe.Metrics.Path,
			Dimensions: map[string]string{
				"Service":       graph.Service,
				"Env":           graph.Env,
				"StatefulGroup": graph.Service,
			},
			Tags:   cloneTags(tags),
			Source: append([]ir.SourceRef(nil), recipe.Meta.Source...),
		})
	}

	launchTemplateName, err := statefulAWSName(graph, ResourceKindLaunchTemplate, launchTemplateLogicalID, "stateful-lt")
	if err != nil {
		return err
	}
	out.LaunchTemplates = append(out.LaunchTemplates, LaunchTemplate{
		LogicalID:          launchTemplateLogicalID,
		Name:               launchTemplateName,
		AMIID:              strings.TrimSpace(opts.AMIID),
		InstanceType:       instanceTypeForMachine(ir.Machine{Size: "small", Arch: "x86_64"}),
		Architecture:       "x86_64",
		IAMInstanceProfile: roleName,
		SecurityGroupRefs:  []string{securityGroupLogicalID},
		UserData:           runnerUserData(graph, opts, releaseID, controlKey),
		Tags:               cloneTags(tags),
		Source:             append([]ir.SourceRef(nil), group.Meta.Source...),
	})

	targetGroupRefs := []string(nil)
	if recipe := firstStatefulRecipe(graph); recipe != nil && recipe.HealthCheck.Port != 0 {
		targetGroupName, err := statefulAWSName(graph, ResourceKindTargetGroup, targetGroupLogicalID, "stateful-tg")
		if err != nil {
			return err
		}
		out.TargetGroups = append(out.TargetGroups, TargetGroupAWS{
			LogicalID:   targetGroupLogicalID,
			Name:        targetGroupName,
			VPCID:       strings.TrimSpace(opts.VPCID),
			Protocol:    "HTTP",
			Port:        recipe.HealthCheck.Port,
			TargetType:  "instance",
			HealthCheck: recipe.HealthCheck,
			Tags:        cloneTags(tags),
			Source:      append([]ir.SourceRef(nil), recipe.Meta.Source...),
		})
		targetGroupRefs = append(targetGroupRefs, targetGroupLogicalID)
	}

	volumes := statefulVolumesByOrdinal(graph.Resources.StatefulVolumes)
	dnsRecords := statefulDNSByOrdinal(graph.Resources.StatefulDNS)
	var snapshotVolumeRefs []string
	for _, member := range graph.Resources.StatefulMembers {
		memberName, err := statefulAWSName(graph, ResourceKindEC2Instance, member.Meta.LogicalID, fmt.Sprintf("member-%d", member.Ordinal))
		if err != nil {
			return err
		}
		memberTags := TagsMap(member.Meta, nil)
		volume := volumes[member.Ordinal]
		dns := dnsRecords[member.Ordinal]
		volumeRef := ""
		if volume != nil {
			volumeName, err := statefulAWSName(graph, ResourceKindEBSVolume, volume.Meta.LogicalID, fmt.Sprintf("volume-%d", member.Ordinal))
			if err != nil {
				return err
			}
			volumeRef = volume.Meta.LogicalID
			snapshotVolumeRefs = append(snapshotVolumeRefs, volumeRef)
			out.EBSVolumes = append(out.EBSVolumes, EBSVolume{
				LogicalID:        volume.Meta.LogicalID,
				Name:             volumeName,
				MemberOrdinal:    member.Ordinal,
				AvailabilityZone: member.Zone,
				Size:             volume.Size,
				VolumeType:       volume.Type,
				Encrypted:        volume.Encrypted,
				KMSKeyID:         strings.TrimSpace(opts.KMSKey),
				MountPath:        volume.MountPath,
				DeleteOnDestroy:  false,
				Tags:             TagsMap(volume.Meta, nil),
				Source:           append([]ir.SourceRef(nil), volume.Meta.Source...),
			})
			attachmentLogicalID := "stateful-volume-attachment:" + graph.Service + fmt.Sprintf(":%d", member.Ordinal)
			attachmentName, err := statefulAWSName(graph, ResourceKindEBSAttachment, attachmentLogicalID, fmt.Sprintf("volume-attachment-%d", member.Ordinal))
			if err != nil {
				return err
			}
			out.VolumeAttachments = append(out.VolumeAttachments, VolumeAttachment{
				LogicalID:     attachmentLogicalID,
				Name:          attachmentName,
				MemberOrdinal: member.Ordinal,
				InstanceRef:   member.Meta.LogicalID,
				VolumeRef:     volume.Meta.LogicalID,
				Device:        "/dev/xvdf",
				MountPath:     volume.MountPath,
				Tags:          TagsMap(volume.Meta, nil),
				Source:        append(append([]ir.SourceRef(nil), member.Meta.Source...), volume.Meta.Source...),
			})
		}
		dnsRef := ""
		if dns != nil {
			dnsRef = dns.Meta.LogicalID
			out.Route53Records = append(out.Route53Records, Route53Record{
				LogicalID:     dns.Meta.LogicalID,
				Name:          firstNonEmpty(dns.DNSName, dns.Meta.Name),
				MemberOrdinal: member.Ordinal,
				DNSName:       firstNonEmpty(dns.DNSName, member.DNSName),
				HostedZoneRef: dns.DNSZoneRef,
				RecordType:    "A",
				TargetRef:     member.Meta.LogicalID,
				TTLSeconds:    30,
				Tags:          TagsMap(dns.Meta, nil),
				Source:        append([]ir.SourceRef(nil), dns.Meta.Source...),
			})
		}
		fencingLogicalID := "stateful-fencing-policy:" + graph.Service + fmt.Sprintf(":%d", member.Ordinal)
		fencingName, err := statefulAWSName(graph, ResourceKindFencingPolicy, fencingLogicalID, fmt.Sprintf("fencing-%d", member.Ordinal))
		if err != nil {
			return err
		}
		out.FencingPolicies = append(out.FencingPolicies, FencingPolicyAWS{
			LogicalID:                   fencingLogicalID,
			Name:                        fencingName,
			MemberOrdinal:               member.Ordinal,
			InstanceRef:                 member.Meta.LogicalID,
			VolumeRef:                   volumeRef,
			RequiresInstanceTermination: true,
			RequiresVolumeDetach:        true,
			Tags:                        cloneTags(memberTags),
			Source:                      append([]ir.SourceRef(nil), member.Meta.Source...),
		})
		out.StatefulMembers = append(out.StatefulMembers, StatefulMemberAWS{
			LogicalID:           member.Meta.LogicalID,
			Name:                memberName,
			MemberOrdinal:       member.Ordinal,
			Zone:                member.Zone,
			DNSName:             firstNonEmpty(member.DNSName, routeName(dns)),
			LaunchTemplateRef:   launchTemplateLogicalID,
			IAMProfileRef:       profileLogicalID,
			SecurityGroupRefs:   []string{securityGroupLogicalID},
			SubnetIDs:           cleanStringSlice(opts.SubnetIDs),
			TargetGroupRefs:     append([]string(nil), targetGroupRefs...),
			VolumeRef:           volumeRef,
			DNSRecordRef:        dnsRef,
			FencingPolicyRef:    fencingLogicalID,
			ReplacementStrategy: "fence-detach-replace-attach",
			Tags:                cloneTags(memberTags),
			Source:              append([]ir.SourceRef(nil), member.Meta.Source...),
		})
	}
	sort.Strings(snapshotVolumeRefs)
	for _, policy := range graph.Resources.SnapshotPolicies {
		name, err := statefulAWSName(graph, ResourceKindSnapshotPolicy, policy.Meta.LogicalID, "snapshot-policy")
		if err != nil {
			return err
		}
		out.SnapshotPolicies = append(out.SnapshotPolicies, SnapshotPolicyAWS{
			LogicalID:  policy.Meta.LogicalID,
			Name:       name,
			Enabled:    policy.Enabled,
			Interval:   policy.Interval,
			Retention:  policy.Retention,
			VolumeRefs: append([]string(nil), snapshotVolumeRefs...),
			Tags:       TagsMap(policy.Meta, nil),
			Source:     append([]ir.SourceRef(nil), policy.Meta.Source...),
		})
	}
	return nil
}

func statefulAWSName(graph *ir.Graph, kind, logicalID, suffix string) (string, error) {
	return ResourceName(NameInput{
		Service:   graph.Service,
		Env:       graph.Env,
		Kind:      kind,
		LogicalID: logicalID,
		Base:      strings.Join(nonEmpty("skiff", graph.Env, graph.Service, suffix), "-"),
	})
}

func statefulIngressRules(recipes []ir.StatefulRecipe, securityGroupRef string) []SecurityGroupRule {
	ports := map[int]struct{}{}
	for _, recipe := range recipes {
		for _, port := range recipe.Ports {
			if port > 0 {
				ports[port] = struct{}{}
			}
		}
		if recipe.HealthCheck.Port > 0 {
			ports[recipe.HealthCheck.Port] = struct{}{}
		}
	}
	ordered := make([]int, 0, len(ports))
	for port := range ports {
		ordered = append(ordered, port)
	}
	sort.Ints(ordered)
	rules := make([]SecurityGroupRule, 0, len(ordered))
	for _, port := range ordered {
		rules = append(rules, SecurityGroupRule{
			Protocol:               "tcp",
			FromPort:               port,
			ToPort:                 port,
			SourceSecurityGroupRef: securityGroupRef,
			Description:            fmt.Sprintf("allow StatefulGroup member traffic on port %d", port),
		})
	}
	return rules
}

func firstStatefulRecipe(graph *ir.Graph) *ir.StatefulRecipe {
	if graph == nil || len(graph.Resources.StatefulRecipes) == 0 {
		return nil
	}
	return &graph.Resources.StatefulRecipes[0]
}

func statefulVolumesByOrdinal(volumes []ir.StatefulVolume) map[int]*ir.StatefulVolume {
	out := make(map[int]*ir.StatefulVolume, len(volumes))
	for i := range volumes {
		volume := &volumes[i]
		out[volume.MemberOrdinal] = volume
	}
	return out
}

func statefulDNSByOrdinal(records []ir.StatefulDNS) map[int]*ir.StatefulDNS {
	out := make(map[int]*ir.StatefulDNS, len(records))
	for i := range records {
		record := &records[i]
		out[record.MemberOrdinal] = record
	}
	return out
}

func routeName(record *ir.StatefulDNS) string {
	if record == nil {
		return ""
	}
	return record.DNSName
}

func isStatefulPlanKind(kind string) bool {
	switch kind {
	case ResourceKindEC2Instance, ResourceKindEBSVolume, ResourceKindEBSAttachment, ResourceKindRoute53Record, ResourceKindSnapshotPolicy, ResourceKindFencingPolicy:
		return true
	default:
		return false
	}
}
