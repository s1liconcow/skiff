package aws

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/state/paths"
)

const (
	ResourceKindSecurityGroup      = "security-group"
	ResourceKindListenerRule       = "listener-rule"
	ResourceKindIAMInstanceProfile = "iam-instance-profile"
	ResourceKindMetricConfig       = "metric-config"
)

type LowerOptions struct {
	Region                       string
	StateBucket                  string
	ReleaseID                    string
	ControlKey                   string
	LoadBalancerSecurityGroupRef string
}

type ServiceResources struct {
	Provider          string             `json:"provider"`
	Service           string             `json:"service"`
	Env               string             `json:"env"`
	Region            string             `json:"region,omitempty"`
	IAMRoles          []IAMRoleResource  `json:"iam_roles,omitempty"`
	InstanceProfiles  []InstanceProfile  `json:"instance_profiles,omitempty"`
	SecurityGroups    []SecurityGroupAWS `json:"security_groups,omitempty"`
	LogGroups         []LogGroup         `json:"log_groups,omitempty"`
	MetricConfigs     []MetricConfigAWS  `json:"metric_configs,omitempty"`
	TargetGroups      []TargetGroupAWS   `json:"target_groups,omitempty"`
	ListenerRules     []ListenerRule     `json:"listener_rules,omitempty"`
	LaunchTemplates   []LaunchTemplate   `json:"launch_templates,omitempty"`
	AutoScalingGroups []AutoScalingGroup `json:"auto_scaling_groups,omitempty"`
}

type IAMRoleResource struct {
	LogicalID        string            `json:"logical_id"`
	Name             string            `json:"name"`
	AssumeRolePolicy PolicyDocument    `json:"assume_role_policy"`
	InlinePolicy     PolicyDocument    `json:"inline_policy"`
	Tags             map[string]string `json:"tags,omitempty"`
	Source           []ir.SourceRef    `json:"source,omitempty"`
}

type InstanceProfile struct {
	LogicalID string            `json:"logical_id"`
	Name      string            `json:"name"`
	RoleRef   string            `json:"role_ref"`
	Tags      map[string]string `json:"tags,omitempty"`
}

type PolicyDocument struct {
	Version   string            `json:"Version"`
	Statement []PolicyStatement `json:"Statement"`
}

type PolicyStatement struct {
	Sid       string            `json:"Sid,omitempty"`
	Effect    string            `json:"Effect"`
	Action    []string          `json:"Action"`
	Principal map[string]string `json:"Principal,omitempty"`
	Resource  []string          `json:"Resource,omitempty"`
}

type SecurityGroupAWS struct {
	LogicalID   string              `json:"logical_id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Ingress     []SecurityGroupRule `json:"ingress,omitempty"`
	Egress      []SecurityGroupRule `json:"egress,omitempty"`
	Tags        map[string]string   `json:"tags,omitempty"`
	Source      []ir.SourceRef      `json:"source,omitempty"`
}

type SecurityGroupRule struct {
	Protocol               string `json:"protocol"`
	FromPort               int    `json:"from_port,omitempty"`
	ToPort                 int    `json:"to_port,omitempty"`
	CIDR                   string `json:"cidr,omitempty"`
	SourceSecurityGroupRef string `json:"source_security_group_ref,omitempty"`
	Description            string `json:"description,omitempty"`
}

type LogGroup struct {
	LogicalID     string            `json:"logical_id"`
	Name          string            `json:"name"`
	RetentionDays int               `json:"retention_days"`
	Tags          map[string]string `json:"tags,omitempty"`
	Source        []ir.SourceRef    `json:"source,omitempty"`
}

type MetricConfigAWS struct {
	LogicalID  string            `json:"logical_id"`
	Namespace  string            `json:"namespace"`
	Path       string            `json:"path,omitempty"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Source     []ir.SourceRef    `json:"source,omitempty"`
}

type TargetGroupAWS struct {
	LogicalID   string            `json:"logical_id"`
	Name        string            `json:"name"`
	Protocol    string            `json:"protocol"`
	Port        int               `json:"port"`
	TargetType  string            `json:"target_type"`
	HealthCheck ir.HealthCheck    `json:"health_check"`
	Tags        map[string]string `json:"tags,omitempty"`
	Source      []ir.SourceRef    `json:"source,omitempty"`
}

type ListenerRule struct {
	LogicalID      string            `json:"logical_id"`
	Name           string            `json:"name"`
	Visibility     string            `json:"visibility"`
	Protocol       string            `json:"protocol"`
	Port           int               `json:"port"`
	Host           string            `json:"host,omitempty"`
	CertificateRef string            `json:"certificate_ref,omitempty"`
	TargetGroupRef string            `json:"target_group_ref"`
	Tags           map[string]string `json:"tags,omitempty"`
	Source         []ir.SourceRef    `json:"source,omitempty"`
}

type LaunchTemplate struct {
	LogicalID          string            `json:"logical_id"`
	Name               string            `json:"name"`
	InstanceType       string            `json:"instance_type"`
	Architecture       string            `json:"architecture"`
	IAMInstanceProfile string            `json:"iam_instance_profile"`
	SecurityGroupRefs  []string          `json:"security_group_refs,omitempty"`
	UserData           string            `json:"user_data"`
	Tags               map[string]string `json:"tags,omitempty"`
	Source             []ir.SourceRef    `json:"source,omitempty"`
}

type AutoScalingGroup struct {
	LogicalID         string            `json:"logical_id"`
	Name              string            `json:"name"`
	MinSize           int               `json:"min_size"`
	MaxSize           int               `json:"max_size"`
	DesiredCapacity   int               `json:"desired_capacity"`
	LaunchTemplateRef string            `json:"launch_template_ref"`
	TargetGroupRefs   []string          `json:"target_group_refs,omitempty"`
	HealthCheckType   string            `json:"health_check_type"`
	Tags              map[string]string `json:"tags,omitempty"`
	Source            []ir.SourceRef    `json:"source,omitempty"`
}

func LowerService(graph *ir.Graph, opts LowerOptions) (*ServiceResources, error) {
	if graph == nil {
		return nil, fmt.Errorf("graph is required")
	}
	controlKey := strings.TrimSpace(opts.ControlKey)
	if controlKey == "" {
		key, err := paths.ServiceControl(graph.Service)
		if err != nil {
			return nil, err
		}
		controlKey = key
	}
	releaseID := strings.TrimSpace(opts.ReleaseID)
	if releaseID == "" {
		releaseID = "desired"
	}
	lbSG := strings.TrimSpace(opts.LoadBalancerSecurityGroupRef)
	if lbSG == "" {
		lbSG = "load-balancer"
	}

	out := &ServiceResources{
		Provider: Name,
		Service:  graph.Service,
		Env:      graph.Env,
		Region:   opts.Region,
	}
	roleNames := make(map[string]string)
	for _, role := range graph.Resources.IAMRoles {
		name, err := NameForResource(graph.Service, graph.Env, role.Meta)
		if err != nil {
			return nil, err
		}
		roleNames[role.Meta.LogicalID] = name
		out.IAMRoles = append(out.IAMRoles, IAMRoleResource{
			LogicalID:        role.Meta.LogicalID,
			Name:             name,
			AssumeRolePolicy: ec2AssumeRolePolicy(),
			InlinePolicy:     workloadPolicy(opts.StateBucket, graph.Service, controlKey, role.SecretRefs),
			Tags:             TagsMap(role.Meta, nil),
			Source:           append([]ir.SourceRef(nil), role.Meta.Source...),
		})
		out.InstanceProfiles = append(out.InstanceProfiles, InstanceProfile{
			LogicalID: role.Meta.LogicalID + ":profile",
			Name:      name,
			RoleRef:   role.Meta.LogicalID,
			Tags:      TagsMap(role.Meta, nil),
		})
	}
	for _, sg := range graph.Resources.SecurityGroups {
		name, err := NameForResource(graph.Service, graph.Env, sg.Meta)
		if err != nil {
			return nil, err
		}
		ingress, egress := lowerSecurityRules(sg.Rules, lbSG)
		out.SecurityGroups = append(out.SecurityGroups, SecurityGroupAWS{
			LogicalID:   sg.Meta.LogicalID,
			Name:        name,
			Description: "Skiff service security group for " + graph.Service,
			Ingress:     ingress,
			Egress:      egress,
			Tags:        TagsMap(sg.Meta, nil),
			Source:      append([]ir.SourceRef(nil), sg.Meta.Source...),
		})
	}
	for _, logs := range graph.Resources.LogConfigs {
		if !logs.Enabled {
			continue
		}
		out.LogGroups = append(out.LogGroups, LogGroup{
			LogicalID:     logs.Meta.LogicalID,
			Name:          "/skiff/" + graph.Env + "/" + graph.Service,
			RetentionDays: 30,
			Tags:          TagsMap(logs.Meta, nil),
			Source:        append([]ir.SourceRef(nil), logs.Meta.Source...),
		})
	}
	for _, metrics := range graph.Resources.MetricConfigs {
		if !metrics.Enabled {
			continue
		}
		out.MetricConfigs = append(out.MetricConfigs, MetricConfigAWS{
			LogicalID: metrics.Meta.LogicalID,
			Namespace: "Skiff/" + graph.Env + "/" + graph.Service,
			Path:      metrics.Path,
			Dimensions: map[string]string{
				"Service": graph.Service,
				"Env":     graph.Env,
			},
			Tags:   TagsMap(metrics.Meta, nil),
			Source: append([]ir.SourceRef(nil), metrics.Meta.Source...),
		})
	}
	for _, tg := range graph.Resources.TargetGroups {
		name, err := NameForResource(graph.Service, graph.Env, tg.Meta)
		if err != nil {
			return nil, err
		}
		out.TargetGroups = append(out.TargetGroups, TargetGroupAWS{
			LogicalID:   tg.Meta.LogicalID,
			Name:        name,
			Protocol:    tg.Protocol,
			Port:        tg.Port,
			TargetType:  "instance",
			HealthCheck: tg.HealthCheck,
			Tags:        TagsMap(tg.Meta, nil),
			Source:      append([]ir.SourceRef(nil), tg.Meta.Source...),
		})
	}
	for _, listener := range graph.Resources.Listeners {
		name, err := NameForResource(graph.Service, graph.Env, listener.Meta)
		if err != nil {
			return nil, err
		}
		out.ListenerRules = append(out.ListenerRules, ListenerRule{
			LogicalID:      listener.Meta.LogicalID,
			Name:           name,
			Visibility:     listener.Visibility,
			Protocol:       listener.Protocol,
			Port:           listener.Port,
			Host:           listener.Host,
			CertificateRef: listener.TLS.CertRef,
			TargetGroupRef: listener.TargetGroupRef,
			Tags:           TagsMap(listener.Meta, nil),
			Source:         append([]ir.SourceRef(nil), listener.Meta.Source...),
		})
	}
	for _, tmpl := range graph.Resources.InstanceTemplates {
		name, err := NameForResource(graph.Service, graph.Env, tmpl.Meta)
		if err != nil {
			return nil, err
		}
		out.LaunchTemplates = append(out.LaunchTemplates, LaunchTemplate{
			LogicalID:          tmpl.Meta.LogicalID,
			Name:               name,
			InstanceType:       instanceTypeForMachine(tmpl.Machine),
			Architecture:       tmpl.Machine.Arch,
			IAMInstanceProfile: roleNames[tmpl.IAMRoleRef],
			SecurityGroupRefs:  append([]string(nil), tmpl.SecurityGroupRefs...),
			UserData:           runnerUserData(graph, opts, releaseID, controlKey),
			Tags:               TagsMap(tmpl.Meta, nil),
			Source:             append([]ir.SourceRef(nil), tmpl.Meta.Source...),
		})
	}
	for _, asg := range graph.Resources.AutoscalingGroups {
		name, err := NameForResource(graph.Service, graph.Env, asg.Meta)
		if err != nil {
			return nil, err
		}
		out.AutoScalingGroups = append(out.AutoScalingGroups, AutoScalingGroup{
			LogicalID:         asg.Meta.LogicalID,
			Name:              name,
			MinSize:           asg.Min,
			MaxSize:           asg.Max,
			DesiredCapacity:   asg.Min,
			LaunchTemplateRef: asg.InstanceTemplateRef,
			TargetGroupRefs:   append([]string(nil), asg.TargetGroupRefs...),
			HealthCheckType:   healthCheckType(asg.TargetGroupRefs),
			Tags:              TagsMap(asg.Meta, nil),
			Source:            append([]ir.SourceRef(nil), asg.Meta.Source...),
		})
	}
	sortServiceResources(out)
	return out, nil
}

func (r ServiceResources) PlannedResources() []plannedAWSResource {
	var out []plannedAWSResource
	for _, item := range r.IAMRoles {
		out = append(out, plannedAWSResource{Kind: ResourceKindIAMRole, LogicalID: item.LogicalID, Name: item.Name, Tags: item.Tags, Summary: "IAM role for workload and runner state access"})
	}
	for _, item := range r.InstanceProfiles {
		out = append(out, plannedAWSResource{Kind: ResourceKindIAMInstanceProfile, LogicalID: item.LogicalID, Name: item.Name, Tags: item.Tags, Summary: "EC2 instance profile attached to workload VMs"})
	}
	for _, item := range r.SecurityGroups {
		out = append(out, plannedAWSResource{Kind: ResourceKindSecurityGroup, LogicalID: item.LogicalID, Name: item.Name, Tags: item.Tags, Summary: "Security group for workload VM ingress and egress"})
	}
	for _, item := range r.LogGroups {
		out = append(out, plannedAWSResource{Kind: ResourceKindLogGroup, LogicalID: item.LogicalID, Name: item.Name, Tags: item.Tags, Summary: "CloudWatch log group for workload logs"})
	}
	for _, item := range r.MetricConfigs {
		out = append(out, plannedAWSResource{Kind: ResourceKindMetricConfig, LogicalID: item.LogicalID, Name: item.Namespace, Tags: item.Tags, Summary: "CloudWatch metric namespace and scraping config"})
	}
	for _, item := range r.TargetGroups {
		out = append(out, plannedAWSResource{Kind: ResourceKindTargetGroup, LogicalID: item.LogicalID, Name: item.Name, Tags: item.Tags, Summary: "Load balancer target group for service instances"})
	}
	for _, item := range r.ListenerRules {
		out = append(out, plannedAWSResource{Kind: ResourceKindListenerRule, LogicalID: item.LogicalID, Name: item.Name, Tags: item.Tags, Summary: "Load balancer listener rule for service ingress"})
	}
	for _, item := range r.LaunchTemplates {
		out = append(out, plannedAWSResource{Kind: ResourceKindLaunchTemplate, LogicalID: item.LogicalID, Name: item.Name, Tags: item.Tags, Summary: "EC2 launch template with skiff-runner user-data"})
	}
	for _, item := range r.AutoScalingGroups {
		out = append(out, plannedAWSResource{Kind: ResourceKindAutoScalingGroup, LogicalID: item.LogicalID, Name: item.Name, Tags: item.Tags, Summary: "Auto Scaling Group representing the service replica pool"})
	}
	return out
}

type plannedAWSResource struct {
	Kind      string
	LogicalID string
	Name      string
	Tags      map[string]string
	Summary   string
}

func lowerSecurityRules(rules []ir.SecurityRule, loadBalancerSecurityGroupRef string) ([]SecurityGroupRule, []SecurityGroupRule) {
	var ingress []SecurityGroupRule
	var egress []SecurityGroupRule
	for _, rule := range rules {
		lowered := SecurityGroupRule{
			Protocol:    rule.Protocol,
			FromPort:    rule.FromPort,
			ToPort:      rule.ToPort,
			Description: rule.Description,
		}
		if lowered.Protocol == "all" {
			lowered.Protocol = "-1"
		}
		if rule.Source == "load-balancer" {
			lowered.SourceSecurityGroupRef = loadBalancerSecurityGroupRef
		} else {
			lowered.CIDR = rule.Source
		}
		if rule.Destination != "" {
			lowered.CIDR = rule.Destination
		}
		switch rule.Direction {
		case "ingress":
			ingress = append(ingress, lowered)
		case "egress":
			egress = append(egress, lowered)
		}
	}
	return ingress, egress
}

func ec2AssumeRolePolicy() PolicyDocument {
	return PolicyDocument{
		Version: "2012-10-17",
		Statement: []PolicyStatement{{
			Sid:       "EC2AssumeRole",
			Effect:    "Allow",
			Action:    []string{"sts:AssumeRole"},
			Principal: map[string]string{"Service": "ec2.amazonaws.com"},
		}},
	}
}

func workloadPolicy(stateBucket, service, controlKey string, secrets []ir.SecretRef) PolicyDocument {
	statements := []PolicyStatement{}
	if bucket := bucketNameFromURI(stateBucket); bucket != "" {
		statements = append(statements, PolicyStatement{
			Sid:      "ReadServiceState",
			Effect:   "Allow",
			Action:   []string{"s3:GetObject"},
			Resource: stateObjectResourceARNs(bucket, service, controlKey),
		})
	}
	secretResources, parameterResources := secretResourceRefs(secrets)
	if len(secretResources) > 0 {
		statements = append(statements, PolicyStatement{
			Sid:      "ReadReferencedSecretsManagerSecrets",
			Effect:   "Allow",
			Action:   []string{"secretsmanager:GetSecretValue"},
			Resource: secretResources,
		})
	}
	if len(parameterResources) > 0 {
		statements = append(statements, PolicyStatement{
			Sid:      "ReadReferencedSSMParameters",
			Effect:   "Allow",
			Action:   []string{"ssm:GetParameter"},
			Resource: parameterResources,
		})
	}
	return PolicyDocument{Version: "2012-10-17", Statement: statements}
}

func secretResourceRefs(secrets []ir.SecretRef) ([]string, []string) {
	seenSecrets := map[string]struct{}{}
	seenParameters := map[string]struct{}{}
	var secretResources []string
	var parameterResources []string
	for _, secret := range secrets {
		kind, ref := awsSecretResourceRef(secret.Ref)
		if ref == "" {
			continue
		}
		switch kind {
		case "secretsmanager":
			if _, ok := seenSecrets[ref]; ok {
				continue
			}
			seenSecrets[ref] = struct{}{}
			secretResources = append(secretResources, ref)
		case "ssm":
			if _, ok := seenParameters[ref]; ok {
				continue
			}
			seenParameters[ref] = struct{}{}
			parameterResources = append(parameterResources, ref)
		}
	}
	sort.Strings(secretResources)
	sort.Strings(parameterResources)
	return secretResources, parameterResources
}

func awsSecretResourceRef(ref string) (string, string) {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "aws-secretsmanager://"):
		arn := strings.TrimPrefix(ref, "aws-secretsmanager://")
		if arn == "" {
			return "", ""
		}
		return "secretsmanager", arn
	case strings.HasPrefix(ref, "aws-ssm://"):
		arn := strings.TrimPrefix(ref, "aws-ssm://")
		if arn == "" {
			return "", ""
		}
		return "ssm", arn
	case strings.HasPrefix(ref, "arn:aws:secretsmanager:"):
		return "secretsmanager", ref
	case strings.HasPrefix(ref, "arn:aws:ssm:"):
		return "ssm", ref
	default:
		return "", ""
	}
}

func stateObjectResourceARNs(bucket, service, controlKey string) []string {
	service = strings.TrimSpace(service)
	if service == "" {
		service = serviceNameFromControlKey(controlKey)
	}
	releasePrefix := fmt.Sprintf("services/%s/releases/*", service)
	if service == "" {
		releasePrefix = "services/*/releases/*"
	}
	return []string{
		fmt.Sprintf("arn:aws:s3:::%s/%s", bucket, controlKey),
		fmt.Sprintf("arn:aws:s3:::%s/%s", bucket, releasePrefix),
	}
}

func serviceNameFromControlKey(controlKey string) string {
	parts := strings.Split(strings.Trim(controlKey, "/"), "/")
	if len(parts) == 3 && parts[0] == "services" && parts[2] == "control.json" {
		return parts[1]
	}
	return ""
}

func runnerUserData(graph *ir.Graph, opts LowerOptions, releaseID, controlKey string) string {
	cfg := map[string]string{
		"env":          graph.Env,
		"provider":     Name,
		"region":       opts.Region,
		"state_bucket": opts.StateBucket,
		"service":      graph.Service,
		"control_key":  controlKey,
		"release_id":   releaseID,
	}
	body, _ := json.Marshal(cfg)
	return "#cloud-config\nwrite_files:\n  - path: /etc/skiff-runner/config.json\n    permissions: '0640'\n    content: |\n      " + string(body) + "\nruncmd:\n  - [ systemctl, enable, --now, skiff-runner ]\n"
}

func instanceTypeForMachine(machine ir.Machine) string {
	switch machine.Size {
	case "small", "":
		return "t3.small"
	case "medium":
		return "t3.medium"
	case "large":
		return "t3.large"
	default:
		return machine.Size
	}
}

func healthCheckType(targetGroups []string) string {
	if len(targetGroups) == 0 {
		return "EC2"
	}
	return "ELB"
}

func bucketNameFromURI(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "s3" {
		return ""
	}
	return parsed.Host
}

func sortServiceResources(resources *ServiceResources) {
	sort.Slice(resources.IAMRoles, func(i, j int) bool { return resources.IAMRoles[i].LogicalID < resources.IAMRoles[j].LogicalID })
	sort.Slice(resources.InstanceProfiles, func(i, j int) bool {
		return resources.InstanceProfiles[i].LogicalID < resources.InstanceProfiles[j].LogicalID
	})
	sort.Slice(resources.SecurityGroups, func(i, j int) bool {
		return resources.SecurityGroups[i].LogicalID < resources.SecurityGroups[j].LogicalID
	})
	sort.Slice(resources.LogGroups, func(i, j int) bool { return resources.LogGroups[i].LogicalID < resources.LogGroups[j].LogicalID })
	sort.Slice(resources.MetricConfigs, func(i, j int) bool {
		return resources.MetricConfigs[i].LogicalID < resources.MetricConfigs[j].LogicalID
	})
	sort.Slice(resources.TargetGroups, func(i, j int) bool { return resources.TargetGroups[i].LogicalID < resources.TargetGroups[j].LogicalID })
	sort.Slice(resources.ListenerRules, func(i, j int) bool {
		return resources.ListenerRules[i].LogicalID < resources.ListenerRules[j].LogicalID
	})
	sort.Slice(resources.LaunchTemplates, func(i, j int) bool {
		return resources.LaunchTemplates[i].LogicalID < resources.LaunchTemplates[j].LogicalID
	})
	sort.Slice(resources.AutoScalingGroups, func(i, j int) bool {
		return resources.AutoScalingGroups[i].LogicalID < resources.AutoScalingGroups[j].LogicalID
	})
}
