package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const Name = "fake"

type Option func(*Provider)

type Provider struct {
	mu       sync.Mutex
	store    objstore.ObjectStore
	clock    func() time.Time
	services map[string][]provider.ResourceInspection
	rollouts map[string]provider.Rollout
}

func New(opts ...Option) *Provider {
	p := &Provider{
		clock:    func() time.Time { return time.Now().UTC() },
		services: map[string][]provider.ResourceInspection{},
		rollouts: map[string]provider.Rollout{},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func WithStateStore(store objstore.ObjectStore) Option {
	return func(p *Provider) {
		p.store = store
	}
}

func WithClock(clock func() time.Time) Option {
	return func(p *Provider) {
		if clock != nil {
			p.clock = clock
		}
	}
}

func (p *Provider) Name() string { return Name }

func (p *Provider) Plan(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "plan", Summary: "graph is required"}
	}
	if strings.TrimSpace(graph.Service) == "" || strings.TrimSpace(graph.Env) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "plan", Summary: "graph service and env are required"}
	}
	metas := graphMetas(graph)
	changes := make([]provider.PlannedChange, 0, len(metas))
	for _, meta := range metas {
		tags := cloneTags(meta.Tags)
		for key, value := range ir.RequiredTags(graph.Service, graph.Env) {
			if tags[key] == "" {
				tags[key] = value
			}
		}
		desired, err := json.Marshal(meta)
		if err != nil {
			return nil, err
		}
		changes = append(changes, provider.PlannedChange{
			Action:     provider.ActionCreate,
			Kind:       meta.Kind,
			LogicalID:  meta.LogicalID,
			Name:       meta.Name,
			ProviderID: providerID(meta.Kind, firstNonEmpty(meta.LogicalID, meta.Name)),
			Tags:       tags,
			Summary:    fmt.Sprintf("fake provider will ensure %s %s", meta.Kind, firstNonEmpty(meta.Name, meta.LogicalID)),
			Desired:    desired,
		})
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Kind == changes[j].Kind {
			return changes[i].LogicalID < changes[j].LogicalID
		}
		return changes[i].Kind < changes[j].Kind
	})
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
	if err := validatePlan(plan); err != nil {
		return nil, err
	}
	now := p.now()
	result := &provider.ApplyResult{
		Provider:  Name,
		Service:   plan.Service,
		Env:       plan.Env,
		AppliedAt: now,
	}
	resources := make([]provider.ResourceInspection, 0, len(plan.Resources))
	for _, change := range plan.Resources {
		switch change.Action {
		case provider.ActionCreate, provider.ActionUpdate, provider.ActionNoop:
		case provider.ActionDeleteNotSupported:
			continue
		default:
			return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "apply", Resource: change.LogicalID, Summary: "unsupported planned action " + change.Action}
		}
		inspection := provider.ResourceInspection{
			Kind:       change.Kind,
			LogicalID:  change.LogicalID,
			Name:       change.Name,
			ProviderID: firstNonEmpty(change.ProviderID, providerID(change.Kind, firstNonEmpty(change.LogicalID, change.Name))),
			Status:     "available",
			Tags:       cloneTags(change.Tags),
		}
		if err := p.recordResource(ctx, plan, inspection, now); err != nil {
			return nil, err
		}
		result.ResourceIDs = append(result.ResourceIDs, inspection.ProviderID)
		result.Resources = append(result.Resources, inspection)
		resources = append(resources, inspection)
	}
	p.mu.Lock()
	p.services[serviceKey(plan.Service, plan.Env)] = cloneInspections(resources)
	p.mu.Unlock()
	return result, nil
}

func (p *Provider) InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(ref.Service) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "inspect_service", Summary: "service is required"}
	}
	p.mu.Lock()
	resources := cloneInspections(p.services[serviceKey(ref.Service, ref.Env)])
	p.mu.Unlock()
	return &provider.ServiceInspection{
		Ref:       ref,
		Provider:  Name,
		FreshAt:   p.now(),
		Resources: resources,
	}, nil
}

func (p *Provider) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	var pools [][]provider.ResourceInspection
	if ref.Service != "" {
		pools = append(pools, p.services[serviceKey(ref.Service, ref.Env)])
	} else {
		for _, resources := range p.services {
			pools = append(pools, resources)
		}
	}
	p.mu.Unlock()
	for _, resources := range pools {
		for _, resource := range resources {
			if resourceMatches(resource, ref) {
				copy := cloneInspection(resource)
				return &copy, nil
			}
		}
	}
	return nil, &provider.Error{Code: provider.CodeNotFound, Provider: Name, Op: "inspect_resource", Resource: firstNonEmpty(ref.ProviderID, ref.LogicalID, ref.Name), Summary: "resource is not present in fake provider state"}
}

func (p *Provider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Service) == "" || strings.TrimSpace(req.Env) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "logs", Summary: "service and env are required"}
	}
	now := p.now()
	entries := []provider.LogEntry{{
		Timestamp: now,
		Message:   "fake workload is serving requests",
		Source:    firstNonEmpty(req.InstanceID, req.ResourceID, "fake-runner"),
		Fields: map[string]string{
			"service": req.Service,
			"env":     req.Env,
		},
	}}
	if req.Limit > 0 && req.Limit < len(entries) {
		entries = entries[:req.Limit]
	}
	return &provider.LogsResult{Entries: entries}, nil
}

func (p *Provider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Service) == "" || strings.TrimSpace(req.Env) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "metrics", Summary: "service and env are required"}
	}
	names := append([]string(nil), req.Names...)
	if len(names) == 0 {
		names = []string{"request_count"}
	}
	now := p.now()
	series := make([]provider.MetricSeries, 0, len(names))
	for _, name := range names {
		series = append(series, provider.MetricSeries{
			Name:     name,
			Category: "workload",
			Source:   "fake",
			Unit:     "count",
			Labels: map[string]string{
				"service": req.Service,
				"env":     req.Env,
			},
			Points: []provider.MetricPoint{{Timestamp: now, Value: 1}},
		})
	}
	return &provider.MetricsResult{Series: series}, nil
}

func (p *Provider) Debug(ctx context.Context, req provider.DebugRequest) (*provider.DebugSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Service) == "" || strings.TrimSpace(req.Env) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "debug", Summary: "service and env are required"}
	}
	now := p.now()
	return &provider.DebugSession{
		ID:        "debug-" + pathSafeResourceName(req.Env+"-"+req.Service),
		Provider:  Name,
		StartedAt: now,
	}, nil
}

func (p *Provider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Service) == "" || strings.TrimSpace(req.Env) == "" || strings.TrimSpace(req.ReleaseID) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "start_rollout", Summary: "service, env, and release ID are required"}
	}
	now := p.now()
	id := "rollout-" + pathSafeResourceName(firstNonEmpty(req.OperationID, req.ReleaseID))
	rollout := provider.Rollout{
		ID:         id,
		Provider:   Name,
		Service:    req.Service,
		Env:        req.Env,
		ProviderID: id,
		StartedAt:  now,
	}
	p.mu.Lock()
	p.rollouts[id] = rollout
	p.mu.Unlock()
	return &rollout, nil
}

func (p *Provider) WatchRollout(ctx context.Context, req provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.RolloutID) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "watch_rollout", Summary: "rollout ID is required"}
	}
	p.mu.Lock()
	rollout, ok := p.rollouts[req.RolloutID]
	p.mu.Unlock()
	if !ok {
		return nil, &provider.Error{Code: provider.CodeNotFound, Provider: Name, Op: "watch_rollout", Resource: req.RolloutID, Summary: "rollout is not present in fake provider state"}
	}
	return &provider.RolloutStatus{
		RolloutID:  rollout.ID,
		Status:     "succeeded",
		ProviderID: firstNonEmpty(req.ProviderID, rollout.ProviderID),
		UpdatedAt:  p.now(),
	}, nil
}

func (p *Provider) Rollback(ctx context.Context, req provider.RollbackRequest) (*provider.Rollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Service) == "" || strings.TrimSpace(req.Env) == "" || strings.TrimSpace(req.ReleaseID) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "rollback", Summary: "service, env, and release ID are required"}
	}
	now := p.now()
	id := "rollback-" + pathSafeResourceName(req.ReleaseID)
	rollout := provider.Rollout{
		ID:         id,
		Provider:   Name,
		Service:    req.Service,
		Env:        req.Env,
		ProviderID: id,
		StartedAt:  now,
	}
	p.mu.Lock()
	p.rollouts[id] = rollout
	p.mu.Unlock()
	return &rollout, nil
}

func (p *Provider) now() time.Time {
	return p.clock().UTC()
}

func (p *Provider) recordResource(ctx context.Context, plan *provider.Plan, resource provider.ResourceInspection, observedAt time.Time) error {
	if p.store == nil {
		return nil
	}
	record := schema.ResourceRecord{
		SchemaVersion: schema.Version,
		Logical: schema.ResourceLogicalRef{
			Kind: resource.Kind,
			Name: firstNonEmpty(resource.LogicalID, resource.Name),
		},
		Provider: schema.ResourceProviderRef{
			Provider: Name,
			Kind:     resource.Kind,
			ID:       resource.ProviderID,
		},
		Service:    plan.Service,
		Env:        plan.Env,
		Tags:       cloneTags(resource.Tags),
		ObservedAt: canonical.Time(observedAt),
	}
	body, err := canonical.Marshal(record)
	if err != nil {
		return err
	}
	opts := objstore.PutOptions{
		ContentType: canonical.ContentType,
		Metadata: map[string]string{
			"schema_version": record.SchemaVersion,
			"provider":       record.Provider.Provider,
			"provider_kind":  record.Provider.Kind,
			"provider_id":    record.Provider.ID,
			"logical_kind":   record.Logical.Kind,
		},
	}
	pathKind := pathSafeResourceName(resource.Kind)
	logicalKey, err := paths.LogicalResource(pathKind, pathSafeResourceName(record.Logical.Name))
	if err != nil {
		return err
	}
	if err := upsert(ctx, p.store, logicalKey, body, opts); err != nil {
		return err
	}
	providerKey, err := paths.ProviderResource(Name, pathKind, resource.ProviderID)
	if err != nil {
		return err
	}
	return upsert(ctx, p.store, providerKey, body, opts)
}

func graphMetas(graph *ir.Graph) []ir.ResourceMeta {
	var metas []ir.ResourceMeta
	for _, item := range graph.Resources.WorkloadIdentities {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.IAMRoles {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.SecurityGroups {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.LogConfigs {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.MetricConfigs {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.TargetGroups {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.Listeners {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.ManagedDatabases {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.DatabaseSecrets {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.DatabaseBindings {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.GlobalTraffic {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.InstanceTemplates {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.AutoscalingGroups {
		metas = append(metas, item.Meta)
	}
	for _, item := range graph.Resources.RuntimeManifests {
		metas = append(metas, item.Meta)
	}
	out := metas[:0]
	for _, meta := range metas {
		if strings.TrimSpace(meta.Kind) == "" || strings.TrimSpace(firstNonEmpty(meta.LogicalID, meta.Name)) == "" {
			continue
		}
		out = append(out, meta)
	}
	return out
}

func validatePlan(plan *provider.Plan) error {
	if plan == nil {
		return &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "apply", Summary: "plan is required"}
	}
	if plan.Provider != Name {
		return &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "apply", Summary: fmt.Sprintf("plan provider must be %q", Name)}
	}
	if strings.TrimSpace(plan.Service) == "" || strings.TrimSpace(plan.Env) == "" {
		return &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "apply", Summary: "plan service and env are required"}
	}
	return nil
}

func upsert(ctx context.Context, store objstore.ObjectStore, key string, body []byte, opts objstore.PutOptions) error {
	for attempt := 0; attempt < 5; attempt++ {
		_, err := store.Create(ctx, key, body, opts)
		if err == nil {
			return nil
		}
		if !errors.Is(err, objstore.ErrAlreadyExists) {
			return err
		}
		current, err := store.Get(ctx, key)
		if err != nil {
			return err
		}
		_, err = store.CompareAndSwap(ctx, key, current.ETag, body, opts)
		if err == nil {
			return nil
		}
		if errors.Is(err, objstore.ErrPreconditionFailed) {
			continue
		}
		return err
	}
	return objstore.WrapError("compare-and-swap", key, objstore.ErrPreconditionFailed)
}

func serviceKey(service, env string) string {
	return env + "/" + service
}

func providerID(kind, logical string) string {
	return "fake-" + pathSafeResourceName(kind) + "-" + pathSafeResourceName(logical)
}

func resourceMatches(resource provider.ResourceInspection, ref provider.ResourceRef) bool {
	if ref.ProviderID != "" && resource.ProviderID != ref.ProviderID {
		return false
	}
	if ref.Kind != "" && !strings.EqualFold(resource.Kind, ref.Kind) {
		return false
	}
	if ref.LogicalID != "" && resource.LogicalID != ref.LogicalID {
		return false
	}
	if ref.Name != "" && resource.Name != ref.Name {
		return false
	}
	return ref.ProviderID != "" || ref.Kind != "" || ref.LogicalID != "" || ref.Name != ""
}

func cloneInspections(in []provider.ResourceInspection) []provider.ResourceInspection {
	out := make([]provider.ResourceInspection, len(in))
	for i, item := range in {
		out[i] = cloneInspection(item)
	}
	return out
}

func cloneInspection(in provider.ResourceInspection) provider.ResourceInspection {
	out := in
	out.Tags = cloneTags(in.Tags)
	return out
}

func cloneTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func pathSafeResourceName(value string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "resource"
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
