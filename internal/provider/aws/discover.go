package aws

import (
	"context"

	"github.com/s1liconcow/skiff/internal/provider"
)

const (
	ResourceKindAutoScalingGroup = "autoscaling-group"
	ResourceKindLaunchTemplate   = "launch-template"
	ResourceKindTargetGroup      = "target-group"
	ResourceKindIAMRole          = "iam-role"
	ResourceKindLogGroup         = "log-group"
)

type Clients struct {
	AutoScaling      AutoScalingClient
	EC2              EC2Client
	ELBV2            ELBV2Client
	IAM              IAMClient
	Logs             LogsClient
	LogQueries       LogQueryClient
	Rollouts         ASGRolloutClient
	ServiceResources ServiceResourceManager
}

type AutoScalingClient interface {
	FindAutoScalingGroups(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error)
}

type EC2Client interface {
	FindLaunchTemplates(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error)
}

type ELBV2Client interface {
	FindTargetGroups(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error)
}

type IAMClient interface {
	FindRoles(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error)
}

type LogsClient interface {
	FindLogGroups(ctx context.Context, filters []TagFilter) ([]DiscoveredResource, error)
}

type DiscoveredResource struct {
	Kind       string            `json:"kind"`
	LogicalID  string            `json:"logical_id,omitempty"`
	Name       string            `json:"name,omitempty"`
	ProviderID string            `json:"provider_id,omitempty"`
	ARN        string            `json:"arn,omitempty"`
	Status     string            `json:"status,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

func (r DiscoveredResource) Inspection() provider.ResourceInspection {
	return provider.ResourceInspection{
		Kind:       r.Kind,
		LogicalID:  r.LogicalID,
		Name:       r.Name,
		ProviderID: r.ProviderID,
		ARN:        r.ARN,
		Status:     r.Status,
		Tags:       cloneTags(r.Tags),
	}
}

func (p *Provider) DiscoverService(ctx context.Context, service, env string) ([]DiscoveredResource, error) {
	if p.clients.Empty() {
		return nil, missingClients("discover_service")
	}
	filters := SkiffTagFilters(service, env)
	var out []DiscoveredResource

	if p.clients.AutoScaling != nil {
		resources, err := p.clients.AutoScaling.FindAutoScalingGroups(ctx, filters)
		if err != nil {
			return nil, ClassifyError("discover_autoscaling_groups", err)
		}
		out = appendKind(out, ResourceKindAutoScalingGroup, resources)
	}
	if p.clients.EC2 != nil {
		resources, err := p.clients.EC2.FindLaunchTemplates(ctx, filters)
		if err != nil {
			return nil, ClassifyError("discover_launch_templates", err)
		}
		out = appendKind(out, ResourceKindLaunchTemplate, resources)
	}
	if p.clients.ELBV2 != nil {
		resources, err := p.clients.ELBV2.FindTargetGroups(ctx, filters)
		if err != nil {
			return nil, ClassifyError("discover_target_groups", err)
		}
		out = appendKind(out, ResourceKindTargetGroup, resources)
	}
	if p.clients.IAM != nil {
		resources, err := p.clients.IAM.FindRoles(ctx, filters)
		if err != nil {
			return nil, ClassifyError("discover_iam_roles", err)
		}
		out = appendKind(out, ResourceKindIAMRole, resources)
	}
	if p.clients.Logs != nil {
		resources, err := p.clients.Logs.FindLogGroups(ctx, filters)
		if err != nil {
			return nil, ClassifyError("discover_log_groups", err)
		}
		out = appendKind(out, ResourceKindLogGroup, resources)
	}
	return out, nil
}

func (c Clients) Empty() bool {
	return c.AutoScaling == nil && c.EC2 == nil && c.ELBV2 == nil && c.IAM == nil && c.Logs == nil && c.LogQueries == nil && c.Rollouts == nil && c.ServiceResources == nil
}

func appendKind(out []DiscoveredResource, kind string, resources []DiscoveredResource) []DiscoveredResource {
	for _, resource := range resources {
		if resource.Kind == "" {
			resource.Kind = kind
		}
		resource.Tags = cloneTags(resource.Tags)
		out = append(out, resource)
	}
	return out
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}
