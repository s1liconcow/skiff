package pluginconformance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/s1liconcow/skiff/internal/doctor"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/plugins"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
)

type Suite struct {
	Plugin          plugins.Plugin
	Runner          plugins.Runner
	ManifestPath    string
	SagaStepKind    string
	PackageStepKind string
}

func Run(t *testing.T, suite Suite) {
	t.Helper()
	if suite.Plugin.Manifest.Name == "" {
		t.Fatal("plugin is required")
	}
	if suite.Runner == nil {
		t.Fatal("plugin runner is required")
	}
	if suite.SagaStepKind == "" {
		t.Fatal("saga step kind is required")
	}
	if suite.PackageStepKind == "" {
		t.Fatal("package step kind is required")
	}
	ctx := context.Background()

	t.Run("manifest validation", func(t *testing.T) {
		if suite.ManifestPath != "" {
			registry, err := plugins.LoadRegistry(ctx, plugins.RegistryOptions{Paths: []string{suite.ManifestPath}})
			if err != nil {
				t.Fatalf("LoadRegistry: %v", err)
			}
			if len(registry.Plugins) != 1 || registry.Plugins[0].Manifest.Name != suite.Plugin.Manifest.Name {
				t.Fatalf("loaded registry = %+v", registry)
			}
		}
		if diagnostics := plugins.ValidateManifest(suite.Plugin.Manifest); len(diagnostics) > 0 {
			t.Fatalf("manifest diagnostics = %+v", diagnostics)
		}
	})

	host := plugins.NewHost(&plugins.Registry{Plugins: []plugins.Plugin{suite.Plugin}}, suite.Runner)
	graph := TestGraph("payments-api", "prod")

	t.Run("validate hook", func(t *testing.T) {
		sets, err := host.Validate(ctx, json.RawMessage(`{"service":"payments-api"}`), "tr_plugin_conformance")
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if len(sets) != 1 || sets[0].Plugin != suite.Plugin.Manifest.Name {
			t.Fatalf("validate patch sets = %+v", sets)
		}
	})

	t.Run("patch permissions and explain", func(t *testing.T) {
		sets, err := host.MutateIR(ctx, graph, nil, "tr_plugin_conformance")
		if err != nil {
			t.Fatalf("MutateIR: %v", err)
		}
		if len(sets) != 1 || len(sets[0].Patches) == 0 {
			t.Fatalf("patch sets = %+v", sets)
		}
		if err := plugins.ApplyIRPatches(graph, sets); err != nil {
			t.Fatalf("ApplyIRPatches: %v", err)
		}
		if len(graph.Resources.SecurityGroups[0].Rules) == 0 {
			t.Fatalf("security group rules were not patched: %+v", graph.Resources.SecurityGroups[0])
		}
		explanations := plugins.ExplainPatchSets(sets)
		if len(explanations) != 1 || explanations[0].Plugin != suite.Plugin.Manifest.Name || explanations[0].Kind != plugins.PatchKindSecurityGroupRule {
			t.Fatalf("patch explanations = %+v", explanations)
		}

		denied := suite.Plugin
		denied.Manifest.Permissions.AllowedPatchKinds = nil
		deniedHost := plugins.NewHost(&plugins.Registry{Plugins: []plugins.Plugin{denied}}, suite.Runner)
		_, err = deniedHost.MutateIR(ctx, TestGraph("payments-api", "prod"), nil, "tr_plugin_conformance")
		var permissionErr plugins.PermissionError
		if !errors.As(err, &permissionErr) {
			t.Fatalf("denied MutateIR error = %v, want PermissionError", err)
		}
	})

	t.Run("runtime addons", func(t *testing.T) {
		addons, diagnostics, err := host.RuntimeAddons(ctx, graph, "tr_plugin_conformance")
		if err != nil {
			t.Fatalf("RuntimeAddons: %v", err)
		}
		if len(diagnostics) != 0 {
			t.Fatalf("runtime addon diagnostics = %+v", diagnostics)
		}
		if len(addons) == 0 || addons[0].Kind == "" || addons[0].Name == "" {
			t.Fatalf("runtime addons = %+v", addons)
		}
	})

	t.Run("doctor checks", func(t *testing.T) {
		findings, err := (plugins.DoctorHook{Host: host}).Check(ctx, doctor.PluginRequest{
			Status: servicestatus.Result{},
			Service: servicestatus.Service{
				Service: "payments-api",
				Env:     "prod",
			},
			TraceID: "tr_plugin_conformance",
		})
		if err != nil {
			t.Fatalf("DoctorHook.Check: %v", err)
		}
		if len(findings) == 0 || findings[0].Code == "" || findings[0].Summary == "" {
			t.Fatalf("doctor findings = %+v", findings)
		}
	})

	t.Run("saga step", func(t *testing.T) {
		registered := plugins.SagaSteps(host)
		step := registered[suite.SagaStepKind]
		if step == nil {
			t.Fatalf("registered saga steps = %+v", registered)
		}
		req := steps.StepRequest{
			SagaID:  "saga_conformance",
			TraceID: "tr_plugin_conformance",
			Node: schema.SagaNode{
				ID:     "fake",
				Kind:   suite.SagaStepKind,
				Params: json.RawMessage(`{"target":"fixture"}`),
			},
		}
		plan, err := step.Plan(ctx, req)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if plan.Summary == "" || plan.Risk == "" || plan.Reversibility == "" {
			t.Fatalf("step plan incomplete: %+v", plan)
		}
		result, err := step.Run(ctx, req)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.Status != steps.StatusSucceeded || result.Summary == "" || len(result.ProviderOperations) == 0 {
			t.Fatalf("step result incomplete: %+v", result)
		}
		resumed, err := step.Resume(ctx, req)
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if resumed.Status != steps.StatusSucceeded {
			t.Fatalf("resume result = %+v", resumed)
		}
		compensated, err := step.Compensate(ctx, req, schema.StepResult{StepID: "fake", Status: string(steps.StatusSucceeded)})
		if err != nil {
			t.Fatalf("Compensate: %v", err)
		}
		if compensated.Status != steps.StatusSucceeded {
			t.Fatalf("compensate result = %+v", compensated)
		}
		findings, err := step.Doctor(ctx, req)
		if err != nil {
			t.Fatalf("Doctor: %v", err)
		}
		if len(findings) == 0 || findings[0].Code == "" {
			t.Fatalf("step doctor findings = %+v", findings)
		}
	})

	t.Run("package step", func(t *testing.T) {
		registered := plugins.PackageSteps(host)
		step := registered[suite.PackageStepKind]
		if step == nil {
			t.Fatalf("registered package steps = %+v", registered)
		}
		req := steps.StepRequest{
			SagaID:  "saga_package_conformance",
			TraceID: "tr_plugin_conformance",
			Intent: schema.SagaIntent{
				SagaID: "saga_package_conformance",
				Target: schema.Target{
					Kind: "StatefulGroup",
					Name: "payments-api",
				},
			},
			Node: schema.SagaNode{
				ID:     "package-fake",
				Kind:   suite.PackageStepKind,
				Params: json.RawMessage(`{"target":"fixture","token":"conformance-secret"}`),
			},
		}
		plan, err := step.Plan(ctx, req)
		if err != nil {
			t.Fatalf("Package Plan: %v", err)
		}
		if plan.Summary == "" || plan.Risk == "" || plan.Reversibility == "" {
			t.Fatalf("package step plan incomplete: %+v", plan)
		}
		result, err := step.Run(ctx, req)
		if err != nil {
			t.Fatalf("Package Run: %v", err)
		}
		if result.Status != steps.StatusSucceeded || result.Summary == "" || len(result.ProviderOperations) == 0 {
			t.Fatalf("package step result incomplete: %+v", result)
		}
		resumed, err := step.Resume(ctx, req)
		if err != nil {
			t.Fatalf("Package Resume: %v", err)
		}
		if resumed.Status != steps.StatusSucceeded {
			t.Fatalf("package resume result = %+v", resumed)
		}
		compensated, err := step.Compensate(ctx, req, schema.StepResult{StepID: "package-fake", Status: string(steps.StatusSucceeded)})
		if err != nil {
			t.Fatalf("Package Compensate: %v", err)
		}
		if compensated.Status != steps.StatusSucceeded {
			t.Fatalf("package compensate result = %+v", compensated)
		}
		findings, err := step.Doctor(ctx, req)
		if err != nil {
			t.Fatalf("Package Doctor: %v", err)
		}
		if len(findings) == 0 || findings[0].Code == "" {
			t.Fatalf("package step doctor findings = %+v", findings)
		}
	})
}

func TestGraph(service, env string) *ir.Graph {
	return &ir.Graph{
		SchemaVersion: ir.SchemaVersion,
		Service:       service,
		Env:           env,
		Resources: ir.Resources{
			SecurityGroups: []ir.SecurityGroup{{
				Meta: ir.ResourceMeta{
					LogicalID: "security-group:" + service,
					Kind:      ir.ResourceKindSecurityGroup,
					Name:      service + "-sg",
					Tags:      ir.RequiredTags(service, env),
				},
			}},
		},
	}
}
