package terraform

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/s1liconcow/skiff/internal/adopt"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

type Options struct {
	Region        string
	StateBucket   string
	OwnershipMode string
}

type Module struct {
	Files   map[string]string
	Mapping adopt.TerraformMapping
}

type outputEntry struct {
	Key       string
	Kind      string
	LogicalID string
	NameExpr  string
	IDExpr    string
	Tags      map[string]string
}

func RenderAWSService(resources *aws.ServiceResources, opts Options) (*Module, error) {
	if resources == nil {
		return nil, fmt.Errorf("service resources are required")
	}
	labels := labelsFor(resources)
	var main strings.Builder
	var outputs []outputEntry
	writeHeader(&main, opts.Region)
	writeVariables(&main, resources)
	writeLocals(&main, resources)
	writeIAM(&main, resources, labels, &outputs)
	writeSecurityGroups(&main, resources, labels, &outputs)
	writeLogsAndMetrics(&main, resources, labels, &outputs)
	writeTargetGroups(&main, resources, labels, &outputs)
	writeListenerRules(&main, resources, labels, &outputs)
	writeLaunchTemplates(&main, resources, labels, &outputs)
	writeStatefulResources(&main, resources, labels, &outputs)
	writeAutoScalingGroups(&main, resources, labels, &outputs)

	files := map[string]string{
		"main.tf":      main.String(),
		"variables.tf": renderVariables(resources),
		"outputs.tf":   renderOutputs(resources, opts, outputs),
		"README.md":    renderReadme(resources),
	}
	return &Module{Files: files, Mapping: adopt.MappingFromAWSResources(resources, opts.OwnershipMode)}, nil
}

func writeHeader(b *strings.Builder, region string) {
	b.WriteString("terraform {\n")
	b.WriteString("  required_providers {\n")
	b.WriteString("    aws = {\n")
	b.WriteString("      source  = \"hashicorp/aws\"\n")
	b.WriteString("      version = \">= 5.0\"\n")
	b.WriteString("    }\n")
	b.WriteString("  }\n")
	b.WriteString("}\n\n")
	b.WriteString("provider \"aws\" {\n")
	if region == "" {
		b.WriteString("  region = var.region\n")
	} else {
		fmt.Fprintf(b, "  region = %s\n", hclString(region))
	}
	b.WriteString("}\n\n")
}

func writeVariables(b *strings.Builder, resources *aws.ServiceResources) {
	b.WriteString("variable \"vpc_id\" {\n  type = string\n}\n\n")
	b.WriteString("variable \"subnet_ids\" {\n  type = list(string)\n}\n\n")
	b.WriteString("variable \"ami_id\" {\n  type = string\n}\n\n")
	b.WriteString("variable \"load_balancer_security_group_id\" {\n  type = string\n  default = \"\"\n}\n\n")
	if len(resources.ListenerRules) > 0 {
		b.WriteString("variable \"alb_listener_arn\" {\n  type = string\n}\n\n")
	}
	if len(resources.Route53Records) > 0 {
		b.WriteString("variable \"route53_zone_id\" {\n  type = string\n}\n\n")
	}
}

func writeLocals(b *strings.Builder, resources *aws.ServiceResources) {
	if len(resources.LaunchTemplates) == 0 {
		return
	}
	b.WriteString("locals {\n")
	userData := resources.LaunchTemplates[0].UserData
	b.WriteString("  runner_user_data = <<USERDATA\n")
	b.WriteString(userData)
	if !strings.HasSuffix(userData, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("USERDATA\n")
	b.WriteString("}\n\n")
}

func writeIAM(b *strings.Builder, resources *aws.ServiceResources, labels map[string]string, outputs *[]outputEntry) {
	for _, role := range resources.IAMRoles {
		label := labels[role.LogicalID]
		fmt.Fprintf(b, "resource \"aws_iam_role\" \"%s\" {\n", label)
		fmt.Fprintf(b, "  name = %s\n", hclString(role.Name))
		writeHeredocJSON(b, "assume_role_policy", role.AssumeRolePolicy)
		b.WriteString("  inline_policy {\n")
		b.WriteString("    name = \"skiff-runner\"\n")
		writeIndentedHeredocJSON(b, "policy", role.InlinePolicy, "    ")
		b.WriteString("  }\n")
		writeTags(b, role.Tags, "  ")
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(role.LogicalID), Kind: aws.ResourceKindIAMRole, LogicalID: role.LogicalID, NameExpr: fmt.Sprintf("aws_iam_role.%s.name", label), IDExpr: fmt.Sprintf("aws_iam_role.%s.name", label), Tags: role.Tags})
	}
	for _, profile := range resources.InstanceProfiles {
		label := labels[profile.LogicalID]
		roleLabel := labels[profile.RoleRef]
		fmt.Fprintf(b, "resource \"aws_iam_instance_profile\" \"%s\" {\n", label)
		fmt.Fprintf(b, "  name = %s\n", hclString(profile.Name))
		if roleLabel != "" {
			fmt.Fprintf(b, "  role = aws_iam_role.%s.name\n", roleLabel)
		}
		writeTags(b, profile.Tags, "  ")
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(profile.LogicalID), Kind: aws.ResourceKindIAMInstanceProfile, LogicalID: profile.LogicalID, NameExpr: fmt.Sprintf("aws_iam_instance_profile.%s.name", label), IDExpr: fmt.Sprintf("aws_iam_instance_profile.%s.name", label), Tags: profile.Tags})
	}
}

func writeSecurityGroups(b *strings.Builder, resources *aws.ServiceResources, labels map[string]string, outputs *[]outputEntry) {
	for _, sg := range resources.SecurityGroups {
		label := labels[sg.LogicalID]
		fmt.Fprintf(b, "resource \"aws_security_group\" \"%s\" {\n", label)
		fmt.Fprintf(b, "  name        = %s\n", hclString(sg.Name))
		fmt.Fprintf(b, "  description = %s\n", hclString(sg.Description))
		b.WriteString("  vpc_id      = var.vpc_id\n")
		writeTags(b, sg.Tags, "  ")
		b.WriteString("}\n\n")
		for i, rule := range sg.Ingress {
			writeSecurityGroupRule(b, "ingress", label, i, rule)
		}
		for i, rule := range sg.Egress {
			writeSecurityGroupRule(b, "egress", label, i, rule)
		}
		*outputs = append(*outputs, outputEntry{Key: keyFor(sg.LogicalID), Kind: aws.ResourceKindSecurityGroup, LogicalID: sg.LogicalID, NameExpr: fmt.Sprintf("aws_security_group.%s.name", label), IDExpr: fmt.Sprintf("aws_security_group.%s.id", label), Tags: sg.Tags})
	}
}

func writeSecurityGroupRule(b *strings.Builder, direction, sgLabel string, index int, rule aws.SecurityGroupRule) {
	fmt.Fprintf(b, "resource \"aws_security_group_rule\" \"%s_%s_%d\" {\n", sgLabel, direction, index)
	fmt.Fprintf(b, "  type              = %s\n", hclString(direction))
	fmt.Fprintf(b, "  security_group_id = aws_security_group.%s.id\n", sgLabel)
	fmt.Fprintf(b, "  protocol          = %s\n", hclString(firstNonEmpty(rule.Protocol, "tcp")))
	if rule.FromPort > 0 || rule.ToPort > 0 {
		fmt.Fprintf(b, "  from_port         = %d\n", rule.FromPort)
		fmt.Fprintf(b, "  to_port           = %d\n", rule.ToPort)
	} else {
		b.WriteString("  from_port         = 0\n  to_port           = 0\n")
	}
	if rule.CIDR != "" {
		fmt.Fprintf(b, "  cidr_blocks       = [%s]\n", hclString(rule.CIDR))
	} else if direction == "ingress" && rule.SourceSecurityGroupRef != "" {
		b.WriteString("  source_security_group_id = var.load_balancer_security_group_id\n")
	} else {
		b.WriteString("  cidr_blocks       = [\"0.0.0.0/0\"]\n")
	}
	if rule.Description != "" {
		fmt.Fprintf(b, "  description       = %s\n", hclString(rule.Description))
	}
	b.WriteString("}\n\n")
}

func writeLogsAndMetrics(b *strings.Builder, resources *aws.ServiceResources, labels map[string]string, outputs *[]outputEntry) {
	for _, logGroup := range resources.LogGroups {
		label := labels[logGroup.LogicalID]
		fmt.Fprintf(b, "resource \"aws_cloudwatch_log_group\" \"%s\" {\n", label)
		fmt.Fprintf(b, "  name              = %s\n", hclString(logGroup.Name))
		fmt.Fprintf(b, "  retention_in_days = %d\n", logGroup.RetentionDays)
		writeTags(b, logGroup.Tags, "  ")
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(logGroup.LogicalID), Kind: aws.ResourceKindLogGroup, LogicalID: logGroup.LogicalID, NameExpr: fmt.Sprintf("aws_cloudwatch_log_group.%s.name", label), IDExpr: fmt.Sprintf("aws_cloudwatch_log_group.%s.name", label), Tags: logGroup.Tags})
	}
	for _, metric := range resources.MetricConfigs {
		*outputs = append(*outputs, outputEntry{Key: keyFor(metric.LogicalID), Kind: aws.ResourceKindMetricConfig, LogicalID: metric.LogicalID, NameExpr: hclString(metric.Namespace), IDExpr: hclString(metric.Namespace), Tags: metric.Tags})
	}
}

func writeTargetGroups(b *strings.Builder, resources *aws.ServiceResources, labels map[string]string, outputs *[]outputEntry) {
	for _, tg := range resources.TargetGroups {
		label := labels[tg.LogicalID]
		fmt.Fprintf(b, "resource \"aws_lb_target_group\" \"%s\" {\n", label)
		fmt.Fprintf(b, "  name        = %s\n", hclString(tg.Name))
		fmt.Fprintf(b, "  port        = %d\n", tg.Port)
		fmt.Fprintf(b, "  protocol    = %s\n", hclString(firstNonEmpty(tg.Protocol, "HTTP")))
		fmt.Fprintf(b, "  target_type = %s\n", hclString(firstNonEmpty(tg.TargetType, "instance")))
		b.WriteString("  vpc_id      = var.vpc_id\n")
		if tg.HealthCheck.Path != "" {
			b.WriteString("  health_check {\n")
			fmt.Fprintf(b, "    path = %s\n", hclString(tg.HealthCheck.Path))
			if tg.HealthCheck.Port > 0 {
				fmt.Fprintf(b, "    port = %s\n", hclString(fmt.Sprintf("%d", tg.HealthCheck.Port)))
			}
			b.WriteString("  }\n")
		}
		writeTags(b, tg.Tags, "  ")
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(tg.LogicalID), Kind: aws.ResourceKindTargetGroup, LogicalID: tg.LogicalID, NameExpr: fmt.Sprintf("aws_lb_target_group.%s.name", label), IDExpr: fmt.Sprintf("aws_lb_target_group.%s.arn", label), Tags: tg.Tags})
	}
}

func writeListenerRules(b *strings.Builder, resources *aws.ServiceResources, labels map[string]string, outputs *[]outputEntry) {
	for _, listener := range resources.ListenerRules {
		label := labels[listener.LogicalID]
		targetLabel := labels[listener.TargetGroupRef]
		fmt.Fprintf(b, "resource \"aws_lb_listener_rule\" \"%s\" {\n", label)
		b.WriteString("  listener_arn = var.alb_listener_arn\n")
		b.WriteString("  action {\n")
		b.WriteString("    type             = \"forward\"\n")
		if targetLabel != "" {
			fmt.Fprintf(b, "    target_group_arn = aws_lb_target_group.%s.arn\n", targetLabel)
		}
		b.WriteString("  }\n")
		if listener.Host != "" {
			b.WriteString("  condition {\n")
			b.WriteString("    host_header {\n")
			fmt.Fprintf(b, "      values = [%s]\n", hclString(listener.Host))
			b.WriteString("    }\n")
			b.WriteString("  }\n")
		}
		writeTags(b, listener.Tags, "  ")
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(listener.LogicalID), Kind: aws.ResourceKindListenerRule, LogicalID: listener.LogicalID, NameExpr: fmt.Sprintf("aws_lb_listener_rule.%s.id", label), IDExpr: fmt.Sprintf("aws_lb_listener_rule.%s.arn", label), Tags: listener.Tags})
	}
}

func writeLaunchTemplates(b *strings.Builder, resources *aws.ServiceResources, labels map[string]string, outputs *[]outputEntry) {
	for _, lt := range resources.LaunchTemplates {
		label := labels[lt.LogicalID]
		fmt.Fprintf(b, "resource \"aws_launch_template\" \"%s\" {\n", label)
		fmt.Fprintf(b, "  name          = %s\n", hclString(lt.Name))
		b.WriteString("  image_id      = var.ami_id\n")
		fmt.Fprintf(b, "  instance_type = %s\n", hclString(lt.InstanceType))
		if profileLabel := labels[lt.IAMInstanceProfile]; profileLabel != "" {
			b.WriteString("  iam_instance_profile {\n")
			fmt.Fprintf(b, "    name = aws_iam_instance_profile.%s.name\n", profileLabel)
			b.WriteString("  }\n")
		}
		if len(lt.SecurityGroupRefs) > 0 {
			b.WriteString("  vpc_security_group_ids = [\n")
			for _, ref := range lt.SecurityGroupRefs {
				if sgLabel := labels[ref]; sgLabel != "" {
					fmt.Fprintf(b, "    aws_security_group.%s.id,\n", sgLabel)
				}
			}
			b.WriteString("  ]\n")
		}
		b.WriteString("  user_data = base64encode(local.runner_user_data)\n")
		writeTags(b, lt.Tags, "  ")
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(lt.LogicalID), Kind: aws.ResourceKindLaunchTemplate, LogicalID: lt.LogicalID, NameExpr: fmt.Sprintf("aws_launch_template.%s.name", label), IDExpr: fmt.Sprintf("aws_launch_template.%s.id", label), Tags: lt.Tags})
	}
}

func writeAutoScalingGroups(b *strings.Builder, resources *aws.ServiceResources, labels map[string]string, outputs *[]outputEntry) {
	for _, asg := range resources.AutoScalingGroups {
		label := labels[asg.LogicalID]
		ltLabel := labels[asg.LaunchTemplateRef]
		fmt.Fprintf(b, "resource \"aws_autoscaling_group\" \"%s\" {\n", label)
		fmt.Fprintf(b, "  name                = %s\n", hclString(asg.Name))
		b.WriteString("  vpc_zone_identifier = var.subnet_ids\n")
		fmt.Fprintf(b, "  min_size            = %d\n", asg.MinSize)
		fmt.Fprintf(b, "  max_size            = %d\n", asg.MaxSize)
		fmt.Fprintf(b, "  desired_capacity    = %d\n", asg.DesiredCapacity)
		if ltLabel != "" {
			b.WriteString("  launch_template {\n")
			fmt.Fprintf(b, "    id      = aws_launch_template.%s.id\n", ltLabel)
			b.WriteString("    version = \"$Latest\"\n")
			b.WriteString("  }\n")
		}
		if len(asg.TargetGroupRefs) > 0 {
			b.WriteString("  target_group_arns = [\n")
			for _, ref := range asg.TargetGroupRefs {
				if tgLabel := labels[ref]; tgLabel != "" {
					fmt.Fprintf(b, "    aws_lb_target_group.%s.arn,\n", tgLabel)
				}
			}
			b.WriteString("  ]\n")
		}
		writeASGTags(b, asg.Tags)
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(asg.LogicalID), Kind: aws.ResourceKindAutoScalingGroup, LogicalID: asg.LogicalID, NameExpr: fmt.Sprintf("aws_autoscaling_group.%s.name", label), IDExpr: fmt.Sprintf("aws_autoscaling_group.%s.name", label), Tags: asg.Tags})
	}
}

func writeStatefulResources(b *strings.Builder, resources *aws.ServiceResources, labels map[string]string, outputs *[]outputEntry) {
	for _, volume := range resources.EBSVolumes {
		label := labels[volume.LogicalID]
		fmt.Fprintf(b, "resource \"aws_ebs_volume\" \"%s\" {\n", label)
		fmt.Fprintf(b, "  availability_zone = %s\n", hclString(firstNonEmpty(volume.AvailabilityZone, "CHANGE-ME")))
		fmt.Fprintf(b, "  size              = %d\n", ebsSizeGB(volume.Size))
		fmt.Fprintf(b, "  type              = %s\n", hclString(firstNonEmpty(volume.VolumeType, "gp3")))
		fmt.Fprintf(b, "  encrypted         = %t\n", volume.Encrypted)
		if volume.KMSKeyID != "" {
			fmt.Fprintf(b, "  kms_key_id        = %s\n", hclString(volume.KMSKeyID))
		}
		writeTags(b, volume.Tags, "  ")
		b.WriteString("  lifecycle {\n")
		b.WriteString("    prevent_destroy = true\n")
		b.WriteString("  }\n")
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(volume.LogicalID), Kind: aws.ResourceKindEBSVolume, LogicalID: volume.LogicalID, NameExpr: hclString(volume.Name), IDExpr: fmt.Sprintf("aws_ebs_volume.%s.id", label), Tags: volume.Tags})
	}
	for _, member := range resources.StatefulMembers {
		label := labels[member.LogicalID]
		fmt.Fprintf(b, "resource \"aws_instance\" \"%s\" {\n", label)
		fmt.Fprintf(b, "  subnet_id = var.subnet_ids[%d %% length(var.subnet_ids)]\n", member.MemberOrdinal)
		if ltLabel := labels[member.LaunchTemplateRef]; ltLabel != "" {
			b.WriteString("  launch_template {\n")
			fmt.Fprintf(b, "    id      = aws_launch_template.%s.id\n", ltLabel)
			b.WriteString("    version = \"$Latest\"\n")
			b.WriteString("  }\n")
		} else {
			b.WriteString("  ami           = var.ami_id\n")
			b.WriteString("  instance_type = \"t3.small\"\n")
		}
		writeTags(b, member.Tags, "  ")
		b.WriteString("  lifecycle {\n")
		b.WriteString("    prevent_destroy = true\n")
		b.WriteString("  }\n")
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(member.LogicalID), Kind: aws.ResourceKindEC2Instance, LogicalID: member.LogicalID, NameExpr: hclString(member.Name), IDExpr: fmt.Sprintf("aws_instance.%s.id", label), Tags: member.Tags})
	}
	for _, attachment := range resources.VolumeAttachments {
		label := labels[attachment.LogicalID]
		instanceLabel := labels[attachment.InstanceRef]
		volumeLabel := labels[attachment.VolumeRef]
		fmt.Fprintf(b, "resource \"aws_volume_attachment\" \"%s\" {\n", label)
		fmt.Fprintf(b, "  device_name  = %s\n", hclString(firstNonEmpty(attachment.Device, "/dev/xvdf")))
		if volumeLabel != "" {
			fmt.Fprintf(b, "  volume_id    = aws_ebs_volume.%s.id\n", volumeLabel)
		}
		if instanceLabel != "" {
			fmt.Fprintf(b, "  instance_id  = aws_instance.%s.id\n", instanceLabel)
		}
		b.WriteString("  skip_destroy = true\n")
		b.WriteString("}\n\n")
		idExpr := hclString(attachment.Name)
		if instanceLabel != "" && volumeLabel != "" {
			idExpr = fmt.Sprintf("format(\"%%s:%%s\", aws_instance.%s.id, aws_ebs_volume.%s.id)", instanceLabel, volumeLabel)
		}
		*outputs = append(*outputs, outputEntry{Key: keyFor(attachment.LogicalID), Kind: aws.ResourceKindEBSAttachment, LogicalID: attachment.LogicalID, NameExpr: hclString(attachment.Name), IDExpr: idExpr, Tags: attachment.Tags})
	}
	for _, record := range resources.Route53Records {
		label := labels[record.LogicalID]
		targetLabel := labels[record.TargetRef]
		fmt.Fprintf(b, "resource \"aws_route53_record\" \"%s\" {\n", label)
		b.WriteString("  zone_id = var.route53_zone_id\n")
		fmt.Fprintf(b, "  name    = %s\n", hclString(firstNonEmpty(record.DNSName, record.Name)))
		fmt.Fprintf(b, "  type    = %s\n", hclString(firstNonEmpty(record.RecordType, "A")))
		fmt.Fprintf(b, "  ttl     = %d\n", firstPositive(record.TTLSeconds, 30))
		if targetLabel != "" && strings.EqualFold(firstNonEmpty(record.RecordType, "A"), "A") {
			fmt.Fprintf(b, "  records = [aws_instance.%s.private_ip]\n", targetLabel)
		} else {
			b.WriteString("  records = [\"CHANGE-ME\"]\n")
		}
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(record.LogicalID), Kind: aws.ResourceKindRoute53Record, LogicalID: record.LogicalID, NameExpr: fmt.Sprintf("aws_route53_record.%s.fqdn", label), IDExpr: fmt.Sprintf("aws_route53_record.%s.id", label), Tags: record.Tags})
	}
	for _, policy := range resources.SnapshotPolicies {
		label := labels[policy.LogicalID]
		fmt.Fprintf(b, "resource \"terraform_data\" \"%s\" {\n", label)
		b.WriteString("  input = {\n")
		fmt.Fprintf(b, "    name      = %s\n", hclString(policy.Name))
		fmt.Fprintf(b, "    enabled   = %t\n", policy.Enabled)
		fmt.Fprintf(b, "    interval  = %s\n", hclString(policy.Interval))
		fmt.Fprintf(b, "    retention = %s\n", hclString(policy.Retention))
		b.WriteString("    volume_refs = [\n")
		for _, ref := range policy.VolumeRefs {
			fmt.Fprintf(b, "      %s,\n", hclString(ref))
		}
		b.WriteString("    ]\n")
		b.WriteString("  }\n")
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(policy.LogicalID), Kind: aws.ResourceKindSnapshotPolicy, LogicalID: policy.LogicalID, NameExpr: hclString(policy.Name), IDExpr: fmt.Sprintf("terraform_data.%s.id", label), Tags: policy.Tags})
	}
	for _, policy := range resources.FencingPolicies {
		label := labels[policy.LogicalID]
		fmt.Fprintf(b, "resource \"terraform_data\" \"%s\" {\n", label)
		b.WriteString("  input = {\n")
		fmt.Fprintf(b, "    name                          = %s\n", hclString(policy.Name))
		fmt.Fprintf(b, "    member_ordinal                = %d\n", policy.MemberOrdinal)
		fmt.Fprintf(b, "    instance_ref                  = %s\n", hclString(policy.InstanceRef))
		fmt.Fprintf(b, "    volume_ref                    = %s\n", hclString(policy.VolumeRef))
		fmt.Fprintf(b, "    requires_instance_termination = %t\n", policy.RequiresInstanceTermination)
		fmt.Fprintf(b, "    requires_volume_detach        = %t\n", policy.RequiresVolumeDetach)
		b.WriteString("  }\n")
		b.WriteString("}\n\n")
		*outputs = append(*outputs, outputEntry{Key: keyFor(policy.LogicalID), Kind: aws.ResourceKindFencingPolicy, LogicalID: policy.LogicalID, NameExpr: hclString(policy.Name), IDExpr: fmt.Sprintf("terraform_data.%s.id", label), Tags: policy.Tags})
	}
}

func renderVariables(resources *aws.ServiceResources) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Variables for %s/%s Terraform-owned infrastructure.\n\n", resources.Env, resources.Service)
	b.WriteString("region = \"us-west-2\"\n")
	b.WriteString("vpc_id = \"vpc-CHANGE-ME\"\n")
	b.WriteString("subnet_ids = [\"subnet-CHANGE-ME-a\", \"subnet-CHANGE-ME-b\"]\n")
	b.WriteString("ami_id = \"ami-CHANGE-ME\"\n")
	b.WriteString("load_balancer_security_group_id = \"sg-CHANGE-ME\"\n")
	if len(resources.ListenerRules) > 0 {
		b.WriteString("alb_listener_arn = \"arn:aws:elasticloadbalancing:REGION:ACCOUNT:listener/app/CHANGE-ME\"\n")
	}
	if len(resources.Route53Records) > 0 {
		b.WriteString("route53_zone_id = \"Z-CHANGE-ME\"\n")
	}
	return b.String()
}

func renderOutputs(resources *aws.ServiceResources, opts Options, entries []outputEntry) string {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	mode := opts.OwnershipMode
	if mode == "" {
		mode = adopt.OwnershipTerraformInfraSkiffRelease
	}
	var b strings.Builder
	b.WriteString("output \"skiff_resources\" {\n")
	b.WriteString("  value = {\n")
	fmt.Fprintf(&b, "    service = %s\n", hclString(resources.Service))
	fmt.Fprintf(&b, "    env = %s\n", hclString(resources.Env))
	b.WriteString("    provider = \"aws\"\n")
	fmt.Fprintf(&b, "    ownership_mode = %s\n", hclString(mode))
	b.WriteString("    resources = {\n")
	for _, entry := range entries {
		fmt.Fprintf(&b, "      %s = {\n", entry.Key)
		fmt.Fprintf(&b, "        kind = %s\n", hclString(entry.Kind))
		fmt.Fprintf(&b, "        logical_id = %s\n", hclString(entry.LogicalID))
		fmt.Fprintf(&b, "        name = %s\n", entry.NameExpr)
		fmt.Fprintf(&b, "        provider_id = %s\n", entry.IDExpr)
		writeOutputTags(&b, entry.Tags)
		b.WriteString("      }\n")
	}
	b.WriteString("    }\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

func renderReadme(resources *aws.ServiceResources) string {
	var b strings.Builder
	b.WriteString("# Terraform-Owned Skiff Infrastructure\n\n")
	fmt.Fprintf(&b, "This module provisions stable AWS infrastructure for `%s/%s`.\n\n", resources.Env, resources.Service)
	b.WriteString("Terraform owns the infrastructure shape. Skiff owns release manifests, runtime\n")
	b.WriteString("manifests, operation/saga state, rollout, rollback, and service control updates.\n\n")
	b.WriteString("## Apply\n\n")
	b.WriteString("```bash\n")
	b.WriteString("terraform init\n")
	b.WriteString("terraform apply -var-file=variables.tfvars\n")
	b.WriteString("terraform output -json skiff_resources > skiff_resources.json\n")
	fmt.Fprintf(&b, "skiff adopt terraform skiff_resources.json --direct --state <state-uri> --env %s --format json\n", resources.Env)
	b.WriteString("```\n\n")
	b.WriteString("After adoption, normal Skiff deploys can publish signed releases and update\n")
	b.WriteString("durable service control without reapplying Terraform-owned infrastructure.\n")
	return b.String()
}

func writeHeredocJSON(b *strings.Builder, name string, value any) {
	writeIndentedHeredocJSON(b, name, value, "  ")
}

func writeIndentedHeredocJSON(b *strings.Builder, name string, value any, indent string) {
	body, _ := json.MarshalIndent(value, "", "  ")
	fmt.Fprintf(b, "%s%s = <<JSON\n", indent, name)
	b.WriteString(string(body))
	if !strings.HasSuffix(string(body), "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("JSON\n")
}

func writeTags(b *strings.Builder, tags map[string]string, indent string) {
	if len(tags) == 0 {
		return
	}
	b.WriteString(indent)
	b.WriteString("tags = {\n")
	keys := sortedKeys(tags)
	for _, key := range keys {
		fmt.Fprintf(b, "%s  %s = %s\n", indent, hclString(key), hclString(tags[key]))
	}
	b.WriteString(indent)
	b.WriteString("}\n")
}

func writeASGTags(b *strings.Builder, tags map[string]string) {
	keys := sortedKeys(tags)
	for _, key := range keys {
		b.WriteString("  tag {\n")
		fmt.Fprintf(b, "    key                 = %s\n", hclString(key))
		fmt.Fprintf(b, "    value               = %s\n", hclString(tags[key]))
		b.WriteString("    propagate_at_launch = true\n")
		b.WriteString("  }\n")
	}
}

func writeOutputTags(b *strings.Builder, tags map[string]string) {
	if len(tags) == 0 {
		return
	}
	b.WriteString("        tags = {\n")
	for _, key := range sortedKeys(tags) {
		fmt.Fprintf(b, "          %s = %s\n", hclString(key), hclString(tags[key]))
	}
	b.WriteString("        }\n")
}

func labelsFor(resources *aws.ServiceResources) map[string]string {
	labels := map[string]string{}
	add := func(logicalID string) {
		if logicalID == "" {
			return
		}
		base := keyFor(logicalID)
		label := base
		for i := 2; ; i++ {
			used := false
			for _, existing := range labels {
				if existing == label {
					used = true
					break
				}
			}
			if !used {
				labels[logicalID] = label
				return
			}
			label = fmt.Sprintf("%s_%d", base, i)
		}
	}
	for _, item := range resources.IAMRoles {
		add(item.LogicalID)
	}
	for _, item := range resources.InstanceProfiles {
		add(item.LogicalID)
	}
	for _, item := range resources.SecurityGroups {
		add(item.LogicalID)
	}
	for _, item := range resources.LogGroups {
		add(item.LogicalID)
	}
	for _, item := range resources.TargetGroups {
		add(item.LogicalID)
	}
	for _, item := range resources.ListenerRules {
		add(item.LogicalID)
	}
	for _, item := range resources.LaunchTemplates {
		add(item.LogicalID)
	}
	for _, item := range resources.AutoScalingGroups {
		add(item.LogicalID)
	}
	for _, item := range resources.StatefulMembers {
		add(item.LogicalID)
	}
	for _, item := range resources.EBSVolumes {
		add(item.LogicalID)
	}
	for _, item := range resources.VolumeAttachments {
		add(item.LogicalID)
	}
	for _, item := range resources.Route53Records {
		add(item.LogicalID)
	}
	for _, item := range resources.SnapshotPolicies {
		add(item.LogicalID)
	}
	for _, item := range resources.FencingPolicies {
		add(item.LogicalID)
	}
	return labels
}

func keyFor(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		allowed := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if allowed {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "resource"
	}
	return out
}

func hclString(value string) string {
	body, _ := json.Marshal(value)
	return string(body)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func ebsSizeGB(value string) int {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, suffix := range []string{"gib", "gi", "gb", "g"} {
		normalized = strings.TrimSuffix(normalized, suffix)
	}
	size, err := strconv.Atoi(strings.TrimSpace(normalized))
	if err != nil || size <= 0 {
		return 20
	}
	return size
}
