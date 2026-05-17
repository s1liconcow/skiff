package explain

import (
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

type Result struct {
	Provider  string                `json:"provider"`
	Service   string                `json:"service"`
	Env       string                `json:"env"`
	Resources []ResourceExplanation `json:"resources"`
}

type ResourceExplanation struct {
	Kind           string         `json:"kind"`
	LogicalID      string         `json:"logical_id"`
	Name           string         `json:"name"`
	CloudPrimitive string         `json:"cloud_primitive"`
	Why            string         `json:"why"`
	Source         []ir.SourceRef `json:"source,omitempty"`
}

func AWS(resources *aws.ServiceResources) Result {
	if resources == nil {
		return Result{Provider: aws.Name}
	}
	out := Result{
		Provider: resources.Provider,
		Service:  resources.Service,
		Env:      resources.Env,
	}
	for _, item := range resources.IAMRoles {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindIAMRole,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "IAM role",
			Why:            "gives each workload VM least-privilege access to its service state and referenced secrets",
			Source:         item.Source,
		})
	}
	for _, item := range resources.InstanceProfiles {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindIAMInstanceProfile,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "IAM instance profile",
			Why:            "attaches the service IAM role to EC2 instances in the service pool",
		})
	}
	for _, item := range resources.SecurityGroups {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindSecurityGroup,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "EC2 security group",
			Why:            securityGroupWhy(item),
			Source:         item.Source,
		})
	}
	for _, item := range resources.LogGroups {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindLogGroup,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "CloudWatch log group",
			Why:            "stores service logs under a visible provider log group",
			Source:         item.Source,
		})
	}
	for _, item := range resources.MetricConfigs {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindMetricConfig,
			LogicalID:      item.LogicalID,
			Name:           item.Namespace,
			CloudPrimitive: "CloudWatch metric config",
			Why:            "records the namespace and scrape path used for service metrics",
			Source:         item.Source,
		})
	}
	for _, item := range resources.TargetGroups {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindTargetGroup,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "load balancer target group",
			Why:            "routes service traffic to the EC2 instances that are healthy for this workload",
			Source:         item.Source,
		})
	}
	for _, item := range resources.ListenerRules {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindListenerRule,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "load balancer listener rule",
			Why:            "connects public or internal HTTP ingress to the service target group",
			Source:         item.Source,
		})
	}
	for _, item := range resources.Databases {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindRDSInstance,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "RDS managed database",
			Why:            "provides the private relational database for the bound API service with encrypted storage, backups, and deletion protection",
			Source:         item.Source,
		})
	}
	for _, item := range resources.Secrets {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindSecret,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "Secrets Manager secret",
			Why:            "stores the database connection reference without putting plaintext credentials in Skiff object state",
			Source:         item.Source,
		})
	}
	for _, item := range resources.LaunchTemplates {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindLaunchTemplate,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "EC2 launch template",
			Why:            "boots one workload replica per VM and configures skiff-runner from object state",
			Source:         item.Source,
		})
	}
	for _, item := range resources.AutoScalingGroups {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindAutoScalingGroup,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "Auto Scaling Group",
			Why:            "represents the service replica pool using cloud autoscaling primitives",
			Source:         item.Source,
		})
	}
	return out
}

func securityGroupWhy(item aws.SecurityGroupAWS) string {
	if item.Tags[ir.TagDatabase] != "" {
		return "allows the bound service security group to reach the managed database port while keeping the database private"
	}
	return "allows only load balancer ingress to the workload port and explicit workload egress"
}
