package aws

import (
	"context"
	"errors"
	"strings"
	"time"

	baseaws "github.com/s1liconcow/skiff/internal/aws"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider"
)

const Name = "aws"

type Config struct {
	Region         string
	StateBucket    string
	Endpoint       string
	ForcePathStyle bool
	Credentials    baseaws.Credentials
}

type Option func(*Provider)

type Provider struct {
	cfg     Config
	clients Clients
}

func New(cfg Config, opts ...Option) (*Provider, error) {
	if strings.TrimSpace(cfg.Region) == "" {
		return nil, &provider.Error{
			Code:     provider.CodeInvalidConfig,
			Provider: Name,
			Op:       "construct",
			Summary:  "aws region is required",
		}
	}
	p := &Provider{cfg: cfg}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func NewFromConfig(cfg config.Config, opts ...Option) (*Provider, error) {
	awsCfg := Config{Region: cfg.Region, StateBucket: cfg.StateBucket}
	return New(awsCfg, opts...)
}

func NewFromEnv(defaultRegion string, opts ...Option) (*Provider, error) {
	cfg, err := baseaws.LoadConfigFromEnv(defaultRegion)
	if err != nil {
		return nil, &provider.Error{
			Code:     provider.CodeInvalidConfig,
			Provider: Name,
			Op:       "construct",
			Summary:  err.Error(),
			Cause:    err,
		}
	}
	return New(Config{
		Region:         cfg.Region,
		Endpoint:       cfg.Endpoint,
		ForcePathStyle: cfg.ForcePathStyle,
		Credentials:    cfg.Credentials,
	}, opts...)
}

func WithClients(clients Clients) Option {
	return func(p *Provider) {
		p.clients = clients
	}
}

func (p *Provider) Name() string {
	return Name
}

func (p *Provider) Config() Config {
	return p.cfg
}

func (p *Provider) Plan(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "plan",
			Summary:  "graph is required",
		}
	}

	resources, err := LowerService(graph, LowerOptions{
		Region:      p.cfg.Region,
		StateBucket: p.cfg.StateBucket,
	})
	if err != nil {
		return nil, &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "plan",
			Summary:  err.Error(),
			Cause:    err,
		}
	}
	planned := resources.PlannedResources()
	changes := make([]provider.PlannedChange, 0, len(planned))
	for _, resource := range planned {
		changes = append(changes, provider.PlannedChange{
			Action:    "ensure",
			Kind:      resource.Kind,
			LogicalID: resource.LogicalID,
			Name:      resource.Name,
			Tags:      cloneTags(resource.Tags),
			Summary:   resource.Summary,
		})
	}
	return &provider.Plan{
		Provider:  Name,
		Service:   graph.Service,
		Env:       graph.Env,
		Resources: changes,
	}, nil
}

func (p *Provider) Apply(ctx context.Context, plan *provider.Plan) (*provider.ApplyResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, provider.Unsupported(Name, "apply")
}

func (p *Provider) InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error) {
	if strings.TrimSpace(ref.Service) == "" || strings.TrimSpace(ref.Env) == "" {
		return nil, &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "inspect_service",
			Summary:  "service and env are required",
		}
	}
	discovered, err := p.DiscoverService(ctx, ref.Service, ref.Env)
	if err != nil {
		return nil, err
	}
	resources := make([]provider.ResourceInspection, 0, len(discovered))
	for _, resource := range discovered {
		resources = append(resources, resource.Inspection())
	}
	return &provider.ServiceInspection{
		Ref:       ref,
		Provider:  Name,
		FreshAt:   time.Now().UTC(),
		Resources: resources,
	}, nil
}

func (p *Provider) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	if strings.TrimSpace(ref.ProviderID) == "" && strings.TrimSpace(ref.Name) == "" && strings.TrimSpace(ref.LogicalID) == "" {
		return nil, &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "inspect_resource",
			Summary:  "provider_id, name, or logical_id is required",
		}
	}
	if strings.TrimSpace(ref.Service) == "" || strings.TrimSpace(ref.Env) == "" {
		return nil, &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "inspect_resource",
			Summary:  "service and env are required for tagged discovery",
		}
	}
	discovered, err := p.DiscoverService(ctx, ref.Service, ref.Env)
	if err != nil {
		return nil, err
	}
	for _, resource := range discovered {
		if ref.Kind != "" && resource.Kind != ref.Kind {
			continue
		}
		if ref.ProviderID != "" && resource.ProviderID == ref.ProviderID {
			inspection := resource.Inspection()
			return &inspection, nil
		}
		if ref.Name != "" && resource.Name == ref.Name {
			inspection := resource.Inspection()
			return &inspection, nil
		}
		if ref.LogicalID != "" && resource.LogicalID == ref.LogicalID {
			inspection := resource.Inspection()
			return &inspection, nil
		}
	}
	return nil, &provider.Error{
		Code:     provider.CodeNotFound,
		Provider: Name,
		Op:       "inspect_resource",
		Summary:  "resource was not found by Skiff tags",
	}
}

func (p *Provider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, provider.Unsupported(Name, "logs")
}

func (p *Provider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, provider.Unsupported(Name, "metrics")
}

func (p *Provider) Debug(ctx context.Context, req provider.DebugRequest) (*provider.DebugSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, provider.Unsupported(Name, "debug")
}

func (p *Provider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, provider.Unsupported(Name, "start_rollout")
}

func (p *Provider) WatchRollout(ctx context.Context, req provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, provider.Unsupported(Name, "watch_rollout")
}

func (p *Provider) Rollback(ctx context.Context, req provider.RollbackRequest) (*provider.Rollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, provider.Unsupported(Name, "rollback")
}

func missingClients(op string) error {
	return &provider.Error{
		Code:     provider.CodeUnsupported,
		Provider: Name,
		Op:       op,
		Summary:  "aws client adapters are required for read-only discovery",
		Cause:    errors.New("missing aws client adapters"),
	}
}
