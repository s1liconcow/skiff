package aws

import (
	"context"
	"errors"
	"strings"
	"time"

	baseaws "github.com/s1liconcow/skiff/internal/aws"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
)

const Name = "aws"

type Config struct {
	Region         string
	StateBucket    string
	Endpoint       string
	ForcePathStyle bool
	Credentials    baseaws.Credentials
	LiveApply      bool
	Live           LiveConfig
}

type LiveConfig struct {
	VPCID                        string
	SubnetIDs                    []string
	AMIID                        string
	ALBListenerARN               string
	LoadBalancerSecurityGroupRef string
}

type Option func(*Provider)

type Provider struct {
	cfg        Config
	clients    Clients
	stateStore objstore.ObjectStore
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
	awsCfg := Config{
		Region:      cfg.Region,
		StateBucket: cfg.StateBucket,
		LiveApply:   cfg.AWSLiveApply,
		Live: LiveConfig{
			VPCID:                        cfg.AWSVPCID,
			SubnetIDs:                    append([]string(nil), cfg.AWSSubnetIDs...),
			AMIID:                        cfg.AWSAMIID,
			ALBListenerARN:               cfg.AWSALBListenerARN,
			LoadBalancerSecurityGroupRef: cfg.AWSLoadBalancerSecurityGroupRef,
		},
	}
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

func WithStateStore(store objstore.ObjectStore) Option {
	return func(p *Provider) {
		p.stateStore = store
	}
}

func (p *Provider) Name() string {
	return Name
}

func (p *Provider) Config() Config {
	return p.cfg
}

func (p *Provider) lowerOptions() LowerOptions {
	return LowerOptions{
		Region:                       p.cfg.Region,
		StateBucket:                  p.cfg.StateBucket,
		VPCID:                        p.cfg.Live.VPCID,
		SubnetIDs:                    append([]string(nil), p.cfg.Live.SubnetIDs...),
		AMIID:                        p.cfg.Live.AMIID,
		ALBListenerARN:               p.cfg.Live.ALBListenerARN,
		LoadBalancerSecurityGroupRef: p.cfg.Live.LoadBalancerSecurityGroupRef,
	}
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

	resources, err := LowerService(graph, p.lowerOptions())
	if err != nil {
		return nil, &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "plan",
			Summary:  err.Error(),
			Cause:    err,
		}
	}
	if p.cfg.LiveApply {
		if err := ValidateLiveApplyInputs(resources); err != nil {
			return nil, &provider.Error{
				Code:     provider.CodeValidation,
				Provider: Name,
				Op:       "plan",
				Summary:  err.Error(),
				Cause:    err,
			}
		}
	}
	desired, err := desiredServiceResources(resources)
	if err != nil {
		return nil, &provider.Error{
			Code:     provider.CodeValidation,
			Provider: Name,
			Op:       "plan",
			Summary:  err.Error(),
			Cause:    err,
		}
	}
	changes := make([]provider.PlannedChange, 0, len(desired))
	for _, resource := range desired {
		var resourcePlan *ResourcePlan
		if p.clients.ServiceResources != nil {
			op := "plan_" + resource.Kind
			if err := retryProviderCall(ctx, op, func() error {
				var planErr error
				resourcePlan, planErr = p.clients.ServiceResources.PlanResource(ctx, resource)
				return planErr
			}); err != nil {
				return nil, err
			}
		}
		change := plannedChangeFromDesired(resource, resourcePlan)
		adopted, ok, err := p.adoptedPlannedChange(ctx, change)
		if err != nil {
			return nil, err
		}
		if ok {
			change = adopted
		}
		changes = append(changes, change)
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
	if p.clients.ServiceResources == nil {
		if err := validatePlanForApply(plan); err != nil {
			return nil, provider.Unsupported(Name, "apply")
		}
		result := &provider.ApplyResult{
			Provider:  Name,
			Service:   plan.Service,
			Env:       plan.Env,
			AppliedAt: time.Now().UTC(),
		}
		for _, change := range plan.Resources {
			if change.Action == provider.ActionDeleteNotSupported {
				continue
			}
			if change.Action != provider.ActionNoop || change.ProviderID == "" {
				return nil, provider.Unsupported(Name, "apply")
			}
			applied := AppliedResource{
				Kind:        change.Kind,
				LogicalID:   change.LogicalID,
				Name:        change.Name,
				ProviderID:  change.ProviderID,
				Status:      "terraform-owned",
				Tags:        cloneTags(change.Tags),
				Fingerprint: change.Fingerprint,
			}
			if err := p.recordAppliedResource(ctx, plan, change, applied); err != nil {
				return nil, err
			}
			result.ResourceIDs = append(result.ResourceIDs, applied.ProviderID)
			result.Resources = append(result.Resources, appliedInspection(change, applied))
		}
		return result, nil
	}
	if err := validatePlanForApply(plan); err != nil {
		return nil, err
	}
	result := &provider.ApplyResult{
		Provider:  Name,
		Service:   plan.Service,
		Env:       plan.Env,
		AppliedAt: time.Now().UTC(),
	}
	for _, change := range plan.Resources {
		switch change.Action {
		case provider.ActionDeleteNotSupported:
			continue
		case provider.ActionNoop:
			if change.ProviderID == "" {
				continue
			}
			applied := AppliedResource{
				Kind:        change.Kind,
				LogicalID:   change.LogicalID,
				Name:        change.Name,
				ProviderID:  change.ProviderID,
				Status:      "unchanged",
				Tags:        cloneTags(change.Tags),
				Fingerprint: change.Fingerprint,
			}
			if err := p.recordAppliedResource(ctx, plan, change, applied); err != nil {
				return nil, err
			}
			result.ResourceIDs = append(result.ResourceIDs, applied.ProviderID)
			result.Resources = append(result.Resources, appliedInspection(change, applied))
			continue
		case provider.ActionCreate, provider.ActionUpdate:
		default:
			return nil, &provider.Error{
				Code:     provider.CodeValidation,
				Provider: Name,
				Op:       "apply",
				Resource: change.LogicalID,
				Summary:  "unsupported planned action " + change.Action,
			}
		}
		desired := desiredFromPlannedChange(change)
		var applied *AppliedResource
		op := "apply_" + change.Kind
		if err := retryProviderCall(ctx, op, func() error {
			var applyErr error
			applied, applyErr = p.clients.ServiceResources.ApplyResource(ctx, desired)
			return applyErr
		}); err != nil {
			return nil, err
		}
		if applied == nil {
			return nil, &provider.Error{
				Code:     provider.CodeProvider,
				Provider: Name,
				Op:       op,
				Resource: change.LogicalID,
				Summary:  "aws resource manager returned no applied resource",
			}
		}
		if applied.ProviderID == "" {
			applied.ProviderID = firstNonEmpty(applied.Name, change.Name)
		}
		if err := p.recordAppliedResource(ctx, plan, change, *applied); err != nil {
			return nil, err
		}
		result.ResourceIDs = append(result.ResourceIDs, applied.ProviderID)
		result.Resources = append(result.Resources, appliedInspection(change, *applied))
	}
	return result, nil
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
