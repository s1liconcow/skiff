package providerconformance

import (
	"context"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/drift"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
)

type Suite struct {
	Provider provider.Provider
	Store    objstore.ObjectStore
	Graph    *ir.Graph
}

func Run(t *testing.T, suite Suite) {
	t.Helper()
	if suite.Provider == nil {
		t.Fatal("provider is required")
	}
	if suite.Store == nil {
		t.Fatal("object store is required")
	}
	graph := suite.Graph
	if graph == nil {
		graph = TestGraph("payments-api", "prod")
	}
	ctx := context.Background()

	var plan *provider.Plan
	t.Run("plan", func(t *testing.T) {
		var err error
		plan, err = suite.Provider.Plan(ctx, graph)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if plan.Provider != suite.Provider.Name() {
			t.Fatalf("plan provider = %q, want %q", plan.Provider, suite.Provider.Name())
		}
		if plan.Service != graph.Service || plan.Env != graph.Env {
			t.Fatalf("plan target = %s/%s, want %s/%s", plan.Env, plan.Service, graph.Env, graph.Service)
		}
		if len(plan.Resources) == 0 {
			t.Fatalf("plan contains no resources")
		}
		for _, change := range plan.Resources {
			if change.Action == "" || change.Kind == "" || change.LogicalID == "" {
				t.Fatalf("incomplete planned change: %+v", change)
			}
			for _, tag := range []string{ir.TagService, ir.TagEnv, ir.TagManaged} {
				if change.Tags[tag] == "" {
					t.Fatalf("planned change %s missing tag %s: %+v", change.LogicalID, tag, change.Tags)
				}
			}
		}
	})

	var applied *provider.ApplyResult
	t.Run("apply", func(t *testing.T) {
		var err error
		applied, err = suite.Provider.Apply(ctx, plan)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if applied.Provider != suite.Provider.Name() || applied.Service != graph.Service || applied.Env != graph.Env {
			t.Fatalf("apply result target/provider mismatch: %+v", applied)
		}
		if applied.AppliedAt.IsZero() {
			t.Fatalf("apply result missing AppliedAt")
		}
		if len(applied.Resources) == 0 || len(applied.ResourceIDs) != len(applied.Resources) {
			t.Fatalf("apply resources/ids mismatch: %+v", applied)
		}
		for _, resource := range applied.Resources {
			if resource.ProviderID == "" || resource.Kind == "" || resource.LogicalID == "" {
				t.Fatalf("applied resource missing provider identity: %+v", resource)
			}
		}
	})

	t.Run("resource discovery", func(t *testing.T) {
		inspection, err := suite.Provider.InspectService(ctx, provider.ServiceRef{Service: graph.Service, Env: graph.Env})
		if err != nil {
			t.Fatalf("InspectService: %v", err)
		}
		if inspection.Provider != suite.Provider.Name() || inspection.FreshAt.IsZero() {
			t.Fatalf("inspection missing provider/freshness: %+v", inspection)
		}
		if len(inspection.Resources) != len(applied.Resources) {
			t.Fatalf("inspection resources = %d, want %d", len(inspection.Resources), len(applied.Resources))
		}
		for _, resource := range inspection.Resources {
			inspected, err := suite.Provider.InspectResource(ctx, provider.ResourceRef{
				Service:    graph.Service,
				Env:        graph.Env,
				ProviderID: resource.ProviderID,
			})
			if err != nil {
				t.Fatalf("InspectResource(%s): %v", resource.ProviderID, err)
			}
			if inspected.ProviderID != resource.ProviderID || inspected.Kind != resource.Kind {
				t.Fatalf("InspectResource returned %+v, want %+v", inspected, resource)
			}
		}
	})

	t.Run("rollout and rollback", func(t *testing.T) {
		rollout, err := suite.Provider.StartRollout(ctx, provider.RolloutRequest{
			Service:     graph.Service,
			Env:         graph.Env,
			ReleaseID:   "rel-conformance",
			OperationID: "op-conformance",
		})
		if err != nil {
			t.Fatalf("StartRollout: %v", err)
		}
		if rollout.ID == "" || rollout.ProviderID == "" || rollout.StartedAt.IsZero() {
			t.Fatalf("rollout missing resumable provider identity: %+v", rollout)
		}
		status, err := suite.Provider.WatchRollout(ctx, provider.WatchRolloutRequest{
			Service:    graph.Service,
			Env:        graph.Env,
			RolloutID:  rollout.ID,
			ProviderID: rollout.ProviderID,
		})
		if err != nil {
			t.Fatalf("WatchRollout: %v", err)
		}
		if status.RolloutID != rollout.ID || status.Status == "" || status.UpdatedAt.IsZero() {
			t.Fatalf("rollout status incomplete: %+v", status)
		}
		rollback, err := suite.Provider.Rollback(ctx, provider.RollbackRequest{
			Service:   graph.Service,
			Env:       graph.Env,
			ReleaseID: "rel-previous",
		})
		if err != nil {
			t.Fatalf("Rollback: %v", err)
		}
		if rollback.ID == "" || rollback.ProviderID == "" || rollback.Provider != suite.Provider.Name() {
			t.Fatalf("rollback result incomplete: %+v", rollback)
		}
	})

	t.Run("observability and debug", func(t *testing.T) {
		logs, err := suite.Provider.Logs(ctx, provider.LogsRequest{
			Service: graph.Service,
			Env:     graph.Env,
			Since:   time.Now().Add(-20 * time.Minute),
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("Logs: %v", err)
		}
		if len(logs.Entries) == 0 || logs.Entries[0].Message == "" || logs.Entries[0].Timestamp.IsZero() {
			t.Fatalf("logs are incomplete: %+v", logs)
		}
		metrics, err := suite.Provider.Metrics(ctx, provider.MetricsRequest{
			Service: graph.Service,
			Env:     graph.Env,
			Names:   []string{"request_count"},
		})
		if err != nil {
			t.Fatalf("Metrics: %v", err)
		}
		if len(metrics.Series) == 0 || len(metrics.Series[0].Points) == 0 {
			t.Fatalf("metrics are incomplete: %+v", metrics)
		}
		session, err := suite.Provider.Debug(ctx, provider.DebugRequest{
			Service: graph.Service,
			Env:     graph.Env,
			Reason:  "provider conformance",
		})
		if err != nil {
			t.Fatalf("Debug: %v", err)
		}
		if session.ID == "" || session.Provider != suite.Provider.Name() || session.StartedAt.IsZero() {
			t.Fatalf("debug session incomplete: %+v", session)
		}
	})

	t.Run("drift", func(t *testing.T) {
		result, err := drift.Detector{Store: suite.Store, Provider: suite.Provider}.Detect(ctx, drift.Request{
			Service: graph.Service,
			Env:     graph.Env,
		})
		if err != nil {
			t.Fatalf("Detect: %v", err)
		}
		if !result.OK || result.Provider != suite.Provider.Name() {
			t.Fatalf("drift result mismatch: %+v", result)
		}
		if len(result.Findings) != 1 || result.Findings[0].Code != "NO_DRIFT_DETECTED" {
			t.Fatalf("drift findings = %+v, want only NO_DRIFT_DETECTED", result.Findings)
		}
	})
}

func TestGraph(service, env string) *ir.Graph {
	tags := ir.RequiredTags(service, env)
	return &ir.Graph{
		SchemaVersion: ir.SchemaVersion,
		Service:       service,
		Env:           env,
		Resources: ir.Resources{
			WorkloadIdentities: []ir.WorkloadIdentity{{
				Meta: meta(ir.ResourceKindWorkloadIdentity, "workload-identity:"+service, service+"-identity", tags),
			}},
			IAMRoles: []ir.IAMRole{{
				Meta:                meta(ir.ResourceKindIAMRole, "iam-role:"+service, service+"-role", tags),
				WorkloadIdentityRef: "workload-identity:" + service,
			}},
			SecurityGroups: []ir.SecurityGroup{{
				Meta: meta(ir.ResourceKindSecurityGroup, "security-group:"+service, service+"-sg", tags),
				Rules: []ir.SecurityRule{{
					Direction:   "ingress",
					Protocol:    "tcp",
					FromPort:    8080,
					ToPort:      8080,
					Source:      "0.0.0.0/0",
					Description: "public service ingress",
				}},
			}},
			LogConfigs: []ir.LogConfig{{
				Meta:    meta(ir.ResourceKindLogConfig, "log-config:"+service, service+"-logs", tags),
				Enabled: true,
				Format:  "json",
			}},
			MetricConfigs: []ir.MetricConfig{{
				Meta:    meta(ir.ResourceKindMetricConfig, "metric-config:"+service, service+"-metrics", tags),
				Enabled: true,
				Path:    "/metrics",
			}},
			TargetGroups: []ir.TargetGroup{{
				Meta:     meta(ir.ResourceKindTargetGroup, "target-group:"+service, service+"-tg", tags),
				Protocol: "HTTP",
				Port:     8080,
				HealthCheck: ir.HealthCheck{
					Type: "http",
					Path: "/healthz",
					Port: 8080,
				},
			}},
			Listeners: []ir.Listener{{
				Meta:           meta(ir.ResourceKindListener, "listener:"+service, service+"-listener", tags),
				Visibility:     "public",
				Protocol:       "HTTP",
				Port:           80,
				TargetGroupRef: "target-group:" + service,
			}},
			InstanceTemplates: []ir.InstanceTemplate{{
				Meta: meta(ir.ResourceKindInstanceTemplate, "instance-template:"+service, service+"-template", tags),
				Machine: ir.Machine{
					Size: "small",
					Arch: "amd64",
				},
				Artifact: ir.Artifact{
					Type:   "oci",
					Ref:    "registry.example.com/skiff/conformance:0.1.0",
					Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				},
				Runtime: ir.Runtime{
					Port: 8080,
					HealthCheck: ir.HealthCheck{
						Type: "http",
						Path: "/healthz",
						Port: 8080,
					},
				},
				WorkloadIdentityRef: "workload-identity:" + service,
				IAMRoleRef:          "iam-role:" + service,
				SecurityGroupRefs:   []string{"security-group:" + service},
				LogConfigRef:        "log-config:" + service,
				MetricConfigRef:     "metric-config:" + service,
			}},
			AutoscalingGroups: []ir.AutoscalingGroup{{
				Meta:                meta(ir.ResourceKindAutoscalingGroup, "autoscaling-group:"+service, service+"-asg", tags),
				Min:                 1,
				Max:                 2,
				InstanceTemplateRef: "instance-template:" + service,
				TargetGroupRefs:     []string{"target-group:" + service},
				Rollout: ir.Rollout{
					Strategy:  "rolling",
					BatchSize: 1,
				},
			}},
			RuntimeManifests: []ir.RuntimeManifest{{
				Meta: meta(ir.ResourceKindRuntimeManifest, "runtime-manifest:"+service, service+"-runtime", tags),
				Artifact: ir.Artifact{
					Type:   "oci",
					Ref:    "registry.example.com/skiff/conformance:0.1.0",
					Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				},
				Command: []string{"/usr/local/bin/conformance"},
				HealthCheck: ir.HealthCheck{
					Type: "http",
					Path: "/healthz",
					Port: 8080,
				},
			}},
		},
	}
}

func meta(kind, logicalID, name string, tags map[string]string) ir.ResourceMeta {
	return ir.ResourceMeta{
		LogicalID: logicalID,
		Kind:      kind,
		Name:      name,
		Tags:      cloneTags(tags),
	}
}

func cloneTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
