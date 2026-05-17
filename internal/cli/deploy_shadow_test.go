package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
)

func TestDeployShadowDryRunOmitsIngressListeners(t *testing.T) {
	clearSkiffEnv(t)
	root := t.TempDir()
	specPath := filepath.Join("..", "..", "examples", "service", "skiff.yaml")
	fake := &shadowDeployProvider{}
	oldProvider := newDeployProvider
	newDeployProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return fake, nil
	}
	t.Cleanup(func() { newDeployProvider = oldProvider })

	var normalOut, normalErr bytes.Buffer
	code := Run("skiff", []string{
		"deploy", specPath,
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--dry-run",
		"--format", "json",
		"--trace-id", "tr_shadow_normal",
	}, &normalOut, &normalErr)
	if code != ExitSuccess {
		t.Fatalf("normal exit=%d stderr=%s stdout=%s", code, normalErr.String(), normalOut.String())
	}
	if len(fake.listenerCounts) != 1 || fake.listenerCounts[0] == 0 {
		t.Fatalf("control deploy did not compile ingress listener: %+v", fake.listenerCounts)
	}

	var shadowOut, shadowErr bytes.Buffer
	code = Run("skiff", []string{
		"deploy", specPath,
		"--direct",
		"--state", "file://" + root,
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--shadow",
		"--dry-run",
		"--format", "json",
		"--trace-id", "tr_shadow",
	}, &shadowOut, &shadowErr)
	if code != ExitSuccess {
		t.Fatalf("shadow exit=%d stderr=%s stdout=%s", code, shadowErr.String(), shadowOut.String())
	}
	if len(fake.listenerCounts) != 2 || fake.listenerCounts[1] != 0 {
		t.Fatalf("shadow deploy should omit ingress listeners: %+v", fake.listenerCounts)
	}
	var got deployOutput
	if err := json.Unmarshal(shadowOut.Bytes(), &got); err != nil {
		t.Fatalf("decode shadow output: %v\n%s", err, shadowOut.String())
	}
	if !got.OK || !got.Result.OK || !got.Result.DryRun || !got.Result.Shadow {
		t.Fatalf("unexpected shadow output: %+v", got)
	}
}

type shadowDeployProvider struct {
	listenerCounts []int
}

func (p *shadowDeployProvider) Name() string { return "aws" }

func (p *shadowDeployProvider) Plan(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	p.listenerCounts = append(p.listenerCounts, len(graph.Resources.Listeners))
	resources := make([]provider.PlannedChange, 0, len(graph.Resources.Listeners))
	for _, listener := range graph.Resources.Listeners {
		resources = append(resources, provider.PlannedChange{
			Action:    provider.ActionCreate,
			Kind:      listener.Meta.Kind,
			LogicalID: listener.Meta.LogicalID,
			Name:      listener.Meta.Name,
		})
	}
	return &provider.Plan{Provider: "aws", Service: graph.Service, Env: graph.Env, Resources: resources}, nil
}

func (p *shadowDeployProvider) Apply(ctx context.Context, plan *provider.Plan) (*provider.ApplyResult, error) {
	return &provider.ApplyResult{Provider: "aws", Service: plan.Service, Env: plan.Env, AppliedAt: time.Now().UTC()}, nil
}

func (p *shadowDeployProvider) InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error) {
	return &provider.ServiceInspection{Ref: ref, Provider: "aws", FreshAt: time.Now().UTC()}, nil
}

func (p *shadowDeployProvider) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	return &provider.ResourceInspection{Kind: ref.Kind, LogicalID: ref.LogicalID, ProviderID: "tg-123", Status: "healthy"}, nil
}

func (p *shadowDeployProvider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	return &provider.LogsResult{}, nil
}

func (p *shadowDeployProvider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	return &provider.MetricsResult{}, nil
}

func (p *shadowDeployProvider) Debug(ctx context.Context, req provider.DebugRequest) (*provider.DebugSession, error) {
	return &provider.DebugSession{ID: "debug-1", Provider: "aws", StartedAt: time.Now().UTC()}, nil
}

func (p *shadowDeployProvider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	return &provider.Rollout{ID: req.OperationID, Provider: "aws", Service: req.Service, Env: req.Env, ProviderID: "ir-" + req.ReleaseID, StartedAt: time.Now().UTC()}, nil
}

func (p *shadowDeployProvider) WatchRollout(ctx context.Context, req provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	return &provider.RolloutStatus{RolloutID: req.RolloutID, Status: "succeeded", ProviderID: req.ProviderID, UpdatedAt: time.Now().UTC()}, nil
}

func (p *shadowDeployProvider) Rollback(ctx context.Context, req provider.RollbackRequest) (*provider.Rollout, error) {
	return &provider.Rollout{ID: "rollback-" + req.ReleaseID, Provider: "aws", Service: req.Service, Env: req.Env, ProviderID: "rb-" + req.ReleaseID, StartedAt: time.Now().UTC()}, nil
}
