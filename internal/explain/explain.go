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
	for _, item := range resources.ObjectStores {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindS3Bucket,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "S3 bucket",
			Why:            "stores workload object-store data for embedded object-backed databases without turning skiffd into a database",
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
	for _, item := range resources.StatefulMembers {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindEC2Instance,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "EC2 stateful member",
			Why:            "represents one StatefulGroup member VM with a stable ordinal, DNS identity, and replacement strategy",
			Source:         item.Source,
		})
	}
	for _, item := range resources.EBSVolumes {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindEBSVolume,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "EBS volume",
			Why:            "stores member data independently from EC2 instance replacement and is not planned for automatic deletion",
			Source:         item.Source,
		})
	}
	for _, item := range resources.VolumeAttachments {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindEBSAttachment,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "EBS volume attachment",
			Why:            "attaches the durable member volume to the replacement member VM after fencing has completed",
			Source:         item.Source,
		})
	}
	for _, item := range resources.Route53Records {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindRoute53Record,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "Route53 DNS record",
			Why:            "keeps the stateful member address stable across replacement and recovery operations",
			Source:         item.Source,
		})
	}
	for _, item := range resources.SnapshotPolicies {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindSnapshotPolicy,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "EBS snapshot policy",
			Why:            "makes StatefulGroup backup cadence and retained volume set visible before backup sagas run",
			Source:         item.Source,
		})
	}
	for _, item := range resources.FencingPolicies {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           aws.ResourceKindFencingPolicy,
			LogicalID:      item.LogicalID,
			Name:           item.Name,
			CloudPrimitive: "stateful fencing policy",
			Why:            "records the provider-level termination and detach requirements before a volume can attach to a replacement",
			Source:         item.Source,
		})
	}
	return out
}

func StatefulReadOnly(providerName string, graph *ir.Graph) Result {
	if graph == nil {
		return Result{Provider: providerName}
	}
	out := Result{
		Provider: providerName,
		Service:  graph.Service,
		Env:      graph.Env,
	}
	for _, item := range graph.Resources.StatefulGroups {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           ir.ResourceKindStatefulGroup,
			LogicalID:      item.Meta.LogicalID,
			Name:           item.Meta.Name,
			CloudPrimitive: "Skiff StatefulGroup",
			Why:            "keeps the stateful workload graph explicit before any provider-specific lowering or mutation",
			Source:         item.Meta.Source,
		})
	}
	for _, item := range graph.Resources.StatefulMembers {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           ir.ResourceKindStatefulMember,
			LogicalID:      item.Meta.LogicalID,
			Name:           item.Meta.Name,
			CloudPrimitive: "stateful member VM",
			Why:            "represents one isolated workload member with a stable ordinal, DNS identity, and durable volume",
			Source:         item.Meta.Source,
		})
	}
	for _, item := range graph.Resources.StatefulVolumes {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           ir.ResourceKindStatefulVolume,
			LogicalID:      item.Meta.LogicalID,
			Name:           item.Meta.Name,
			CloudPrimitive: "durable block volume",
			Why:            "preserves member data independently from replacement VM lifecycle",
			Source:         item.Meta.Source,
		})
	}
	for _, item := range graph.Resources.StatefulDNS {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           ir.ResourceKindStatefulDNS,
			LogicalID:      item.Meta.LogicalID,
			Name:           item.Meta.Name,
			CloudPrimitive: "stable DNS identity",
			Why:            "keeps member addressing stable across replacement and recovery operations",
			Source:         item.Meta.Source,
		})
	}
	for _, item := range graph.Resources.StatefulRecipes {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           ir.ResourceKindStatefulRecipe,
			LogicalID:      item.Meta.LogicalID,
			Name:           item.Meta.Name,
			CloudPrimitive: "stateful recipe runtime",
			Why:            "captures the recipe artifact, runtime, health, metrics, and opaque recipe config without hiding them in a controller",
			Source:         item.Meta.Source,
		})
	}
	for _, item := range graph.Resources.SnapshotPolicies {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           ir.ResourceKindSnapshotPolicy,
			LogicalID:      item.Meta.LogicalID,
			Name:           item.Meta.Name,
			CloudPrimitive: "snapshot policy",
			Why:            "makes backup cadence and retention visible before backup sagas are enabled",
			Source:         item.Meta.Source,
		})
	}
	for _, item := range graph.Resources.UpdatePolicies {
		out.Resources = append(out.Resources, ResourceExplanation{
			Kind:           ir.ResourceKindUpdatePolicy,
			LogicalID:      item.Meta.LogicalID,
			Name:           item.Meta.Name,
			CloudPrimitive: "ordered update policy",
			Why:            "records that StatefulGroup updates must proceed explicitly and serially",
			Source:         item.Meta.Source,
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
