package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
)

type ASGRolloutClient interface {
	StartInstanceRefresh(ctx context.Context, req StartInstanceRefreshRequest) (*InstanceRefresh, error)
	DescribeInstanceRefresh(ctx context.Context, req DescribeInstanceRefreshRequest) (*InstanceRefresh, error)
}

type StartInstanceRefreshRequest struct {
	AutoScalingGroupName string `json:"auto_scaling_group_name"`
	ReleaseID            string `json:"release_id,omitempty"`
	MinHealthyPercentage int    `json:"min_healthy_percentage,omitempty"`
	InstanceWarmup       int    `json:"instance_warmup,omitempty"`
}

type DescribeInstanceRefreshRequest struct {
	AutoScalingGroupName string `json:"auto_scaling_group_name"`
	InstanceRefreshID    string `json:"instance_refresh_id"`
}

type InstanceRefresh struct {
	ID                   string    `json:"id"`
	AutoScalingGroupName string    `json:"auto_scaling_group_name"`
	Status               string    `json:"status"`
	StatusReason         string    `json:"status_reason,omitempty"`
	PercentageComplete   int       `json:"percentage_complete,omitempty"`
	StartedAt            time.Time `json:"started_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}

const RolloutKindASGInstanceRefresh = "asg-instance-refresh"

func (p *Provider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.clients.Rollouts == nil {
		return nil, provider.Unsupported(Name, "start_rollout")
	}
	asgName, err := rolloutASGName(req.Service, req.Env)
	if err != nil {
		return nil, err
	}
	var refresh *InstanceRefresh
	if err := retryProviderCall(ctx, "start_instance_refresh", func() error {
		var startErr error
		refresh, startErr = p.clients.Rollouts.StartInstanceRefresh(ctx, StartInstanceRefreshRequest{
			AutoScalingGroupName: asgName,
			ReleaseID:            req.ReleaseID,
			MinHealthyPercentage: defaultMinHealthy(req.MinHealthyPercentage),
			InstanceWarmup:       defaultInstanceWarmup(req.InstanceWarmup),
		})
		return startErr
	}); err != nil {
		return nil, err
	}
	if refresh == nil || strings.TrimSpace(refresh.ID) == "" {
		return nil, &provider.Error{Code: provider.CodeProvider, Provider: Name, Op: "start_instance_refresh", Summary: "aws returned no instance refresh ID"}
	}
	startedAt := refresh.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	return &provider.Rollout{
		ID:         firstNonEmpty(req.OperationID, "rollout-"+refresh.ID),
		Provider:   Name,
		Service:    req.Service,
		Env:        req.Env,
		ProviderID: refresh.ID,
		StartedAt:  startedAt.UTC(),
	}, nil
}

func (p *Provider) WatchRollout(ctx context.Context, req provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.clients.Rollouts == nil {
		return nil, provider.Unsupported(Name, "watch_rollout")
	}
	asgName, err := rolloutASGName(req.Service, req.Env)
	if err != nil {
		return nil, err
	}
	refreshID := firstNonEmpty(req.ProviderID, req.RolloutID)
	if strings.TrimSpace(refreshID) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "watch_rollout", Summary: "rollout provider ID is required"}
	}
	var refresh *InstanceRefresh
	if err := retryProviderCall(ctx, "describe_instance_refresh", func() error {
		var describeErr error
		refresh, describeErr = p.clients.Rollouts.DescribeInstanceRefresh(ctx, DescribeInstanceRefreshRequest{
			AutoScalingGroupName: asgName,
			InstanceRefreshID:    refreshID,
		})
		return describeErr
	}); err != nil {
		return nil, err
	}
	if refresh == nil {
		return nil, &provider.Error{Code: provider.CodeNotFound, Provider: Name, Op: "describe_instance_refresh", Summary: "instance refresh was not found"}
	}
	updatedAt := refresh.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	return &provider.RolloutStatus{
		RolloutID:  req.RolloutID,
		Status:     mapInstanceRefreshStatus(refresh.Status),
		ProviderID: refresh.ID,
		UpdatedAt:  updatedAt.UTC(),
	}, nil
}

func rolloutASGName(service, env string) (string, error) {
	if strings.TrimSpace(service) == "" || strings.TrimSpace(env) == "" {
		return "", &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "rollout", Summary: "service and env are required"}
	}
	return ResourceName(NameInput{
		Service: service,
		Env:     env,
		Kind:    ResourceKindAutoScalingGroup,
		Base:    fmt.Sprintf("skiff-%s-%s-asg", env, service),
	})
}

func defaultMinHealthy(value int) int {
	if value <= 0 {
		return 90
	}
	return value
}

func defaultInstanceWarmup(value int) int {
	if value <= 0 {
		return 60
	}
	return value
}

func mapInstanceRefreshStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending":
		return "starting"
	case "inprogress", "in_progress":
		return "rolling_out"
	case "successful", "succeeded", "success":
		return "succeeded"
	case "failed":
		return "failed"
	case "cancelling", "cancelled", "canceled":
		return "cancelled"
	case "rollbackinprogress", "rollback_in_progress", "rollbacksuccessful", "rollbackfailed":
		return "rolling_back"
	default:
		if status == "" {
			return "unknown"
		}
		return strings.ToLower(status)
	}
}
