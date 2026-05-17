package aws

import (
	"fmt"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/internal/config"
)

type LiveApplyMissingInput struct {
	Field     string `json:"field"`
	EnvVar    string `json:"env_var,omitempty"`
	Kind      string `json:"kind,omitempty"`
	LogicalID string `json:"logical_id,omitempty"`
	Reason    string `json:"reason"`
}

type LiveApplyValidationError struct {
	Missing []LiveApplyMissingInput `json:"missing"`
}

func (e LiveApplyValidationError) Error() string {
	if len(e.Missing) == 0 {
		return "aws live apply inputs are incomplete"
	}
	fields := make(map[string]struct{}, len(e.Missing))
	for _, missing := range e.Missing {
		fields[missing.Field] = struct{}{}
	}
	names := make([]string, 0, len(fields))
	for field := range fields {
		names = append(names, field)
	}
	sort.Strings(names)
	return fmt.Sprintf("aws live apply missing required input(s): %s", strings.Join(names, ", "))
}

func ValidateLiveApplyInputs(resources *ServiceResources) error {
	if resources == nil {
		return nil
	}
	var missing []LiveApplyMissingInput
	add := func(field, kind, logicalID, reason string) {
		missing = append(missing, LiveApplyMissingInput{
			Field:     field,
			EnvVar:    envVarForLiveField(field),
			Kind:      kind,
			LogicalID: logicalID,
			Reason:    reason,
		})
	}
	for _, group := range resources.SecurityGroups {
		if strings.TrimSpace(group.VPCID) == "" {
			add(config.FieldAWSVPCID, ResourceKindSecurityGroup, group.LogicalID, "security groups must be created inside a VPC")
		}
		for _, rule := range append(append([]SecurityGroupRule(nil), group.Ingress...), group.Egress...) {
			if strings.TrimSpace(rule.SourceSecurityGroupRef) == "load-balancer" {
				add(config.FieldAWSLoadBalancerSecurityGroupRef, ResourceKindSecurityGroup, group.LogicalID, "load-balancer ingress must resolve to an AWS security group ID/ref")
			}
		}
	}
	for _, group := range resources.TargetGroups {
		if strings.TrimSpace(group.VPCID) == "" {
			add(config.FieldAWSVPCID, ResourceKindTargetGroup, group.LogicalID, "target groups must be created inside a VPC")
		}
	}
	for _, tmpl := range resources.LaunchTemplates {
		if strings.TrimSpace(tmpl.AMIID) == "" {
			add(config.FieldAWSAMIID, ResourceKindLaunchTemplate, tmpl.LogicalID, "launch templates require an AMI ID")
		}
	}
	for _, asg := range resources.AutoScalingGroups {
		if len(cleanStringSlice(asg.SubnetIDs)) == 0 {
			add(config.FieldAWSSubnetIDs, ResourceKindAutoScalingGroup, asg.LogicalID, "Auto Scaling Groups require at least one subnet ID")
		}
	}
	for _, rule := range resources.ListenerRules {
		if strings.TrimSpace(rule.ListenerARN) == "" {
			add(config.FieldAWSALBListenerARN, ResourceKindListenerRule, rule.LogicalID, "listener rules require an existing ALB listener ARN")
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return LiveApplyValidationError{Missing: missing}
}

func envVarForLiveField(field string) string {
	switch field {
	case config.FieldAWSLiveApply:
		return "SKIFF_AWS_LIVE_APPLY"
	case config.FieldAWSVPCID:
		return "SKIFF_AWS_VPC_ID"
	case config.FieldAWSSubnetIDs:
		return "SKIFF_AWS_SUBNET_IDS"
	case config.FieldAWSAMIID:
		return "SKIFF_AWS_AMI_ID"
	case config.FieldAWSALBListenerARN:
		return "SKIFF_AWS_ALB_LISTENER_ARN"
	case config.FieldAWSLoadBalancerSecurityGroupRef:
		return "SKIFF_AWS_LOAD_BALANCER_SECURITY_GROUP_REF"
	default:
		return ""
	}
}
