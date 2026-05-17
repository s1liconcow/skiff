package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/s1liconcow/skiff/internal/doctor"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

type fakeRunner struct {
	mutate pluginapi.MutateIRResponse
	doctor pluginapi.DoctorChecksResponse
	saga   pluginapi.SagaStepResultResponse
}

func (r fakeRunner) RunPluginHook(ctx context.Context, plugin Plugin, hook pluginapi.Hook, request any, response any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch hook {
	case pluginapi.HookMutateIR:
		body, _ := json.Marshal(r.mutate)
		return json.Unmarshal(body, response)
	case pluginapi.HookDoctorChecks:
		body, _ := json.Marshal(r.doctor)
		return json.Unmarshal(body, response)
	case pluginapi.HookSagaStep:
		body, _ := json.Marshal(r.saga)
		return json.Unmarshal(body, response)
	default:
		return nil
	}
}

func TestValidateManifestRejectsDeniedRuntimeAddons(t *testing.T) {
	manifest := baseManifest()
	manifest.Hooks = []pluginapi.Hook{pluginapi.HookRuntimeAddons}
	manifest.Capabilities = []pluginapi.Capability{{
		Kind:          pluginapi.CapabilityRuntimeAddon,
		Name:          "sidecar",
		RuntimeAddons: []string{"systemd-dropin"},
	}}

	diagnostics := ValidateManifest(manifest)
	if len(diagnostics) == 0 {
		t.Fatalf("expected diagnostics")
	}
	if diagnostics[len(diagnostics)-1].Code != "PLUGIN_PERMISSION_RUNTIME_ADDONS_REQUIRED" {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestValidateManifestRejectsAPIVersionMismatch(t *testing.T) {
	manifest := baseManifest()
	manifest.APIVersion = "skiff.dev/plugin/v9"

	diagnostics := ValidateManifest(manifest)
	if len(diagnostics) == 0 || diagnostics[0].Code != "PLUGIN_API_VERSION_UNSUPPORTED" {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestEnforcePatchesDeniesUndeclaredKind(t *testing.T) {
	plugin := Plugin{Manifest: baseManifest()}
	plugin.Manifest.Permissions.AllowedPatchKinds = []string{ir.ResourceKindRuntimeManifest}
	patch := pluginapi.IRPatch{
		Op:    pluginapi.PatchAdd,
		Path:  "/resources/security_groups/security-group:payments-api/rules/-",
		Kind:  PatchKindSecurityGroupRule,
		Value: json.RawMessage(`{"direction":"egress","protocol":"tcp","from_port":8443,"destination":"10.0.0.0/8"}`),
	}

	_, err := EnforcePatches(plugin, []pluginapi.IRPatch{patch})
	var permissionErr PermissionError
	if !errors.As(err, &permissionErr) {
		t.Fatalf("err = %v, want PermissionError", err)
	}
}

func TestHostAppliesAllowedSecurityGroupRulePatch(t *testing.T) {
	patch := pluginapi.IRPatch{
		Op:      pluginapi.PatchAdd,
		Path:    "/resources/security_groups/security-group:payments-api/rules/-",
		Kind:    PatchKindSecurityGroupRule,
		Value:   json.RawMessage(`{"direction":"egress","protocol":"tcp","from_port":8443,"destination":"10.0.0.0/8"}`),
		Summary: "allow mTLS egress",
	}
	host := NewHost(&Registry{Plugins: []Plugin{{
		Manifest: baseManifest(),
		Source:   Source{Kind: SourcePath, Path: "fixture"},
	}}}, fakeRunner{mutate: pluginapi.MutateIRResponse{Patches: []pluginapi.IRPatch{patch}}})
	graph := pluginTestGraph()

	sets, err := host.MutateIR(context.Background(), graph, nil, "tr_plugin")
	if err != nil {
		t.Fatalf("MutateIR: %v", err)
	}
	if err := ApplyIRPatches(graph, sets); err != nil {
		t.Fatalf("ApplyIRPatches: %v", err)
	}
	if len(graph.Resources.SecurityGroups[0].Rules) != 1 {
		t.Fatalf("rules = %+v", graph.Resources.SecurityGroups[0].Rules)
	}
	if graph.Resources.SecurityGroups[0].Rules[0].Destination != "10.0.0.0/8" {
		t.Fatalf("rule = %+v", graph.Resources.SecurityGroups[0].Rules[0])
	}
	explanations := ExplainPatchSets(sets)
	if len(explanations) != 1 || explanations[0].Plugin != "mtls-egress" {
		t.Fatalf("explanations = %+v", explanations)
	}
}

func TestRegistryLoadsSignedPackageReference(t *testing.T) {
	registry, err := LoadRegistry(context.Background(), RegistryOptions{PackageRefs: []PackageSource{{
		Ref:          "oci://registry.example.com/skiff/mtls-egress:0.1.0",
		Digest:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SignatureRef: "oci://registry.example.com/skiff/mtls-egress.sig",
	}}})
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(registry.Plugins) != 1 || registry.Plugins[0].Source.Kind != SourcePackageRef || registry.Plugins[0].Manifest.Name != "mtls-egress" {
		t.Fatalf("registry = %+v", registry)
	}
}

func TestRegistryRejectsUnsignedPackageReference(t *testing.T) {
	_, err := LoadRegistry(context.Background(), RegistryOptions{PackageRefs: []PackageSource{{
		Ref: "oci://registry.example.com/skiff/mtls-egress:0.1.0",
	}}})
	var manifestErr ManifestError
	if !errors.As(err, &manifestErr) {
		t.Fatalf("err = %v, want ManifestError", err)
	}
}

func TestDoctorHookRegistersPluginFindings(t *testing.T) {
	host := NewHost(&Registry{Plugins: []Plugin{{
		Manifest: doctorManifest(),
		Source:   Source{Kind: SourcePath, Path: "fixture"},
	}}}, fakeRunner{doctor: pluginapi.DoctorChecksResponse{Findings: []pluginapi.DoctorFinding{{
		Code:       "PLUGIN_CHECK",
		Severity:   "low",
		Summary:    "plugin observed diagnostic context",
		Confidence: 0.9,
	}}}})
	hook := DoctorHook{Host: host}

	findings, err := hook.Check(context.Background(), doctor.PluginRequest{
		Status:  servicestatus.Result{},
		Service: servicestatus.Service{Service: "payments-api"},
		TraceID: "tr_doctor",
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(findings) != 1 || findings[0].Code != "PLUGIN_CHECK" || findings[0].Service != "payments-api" {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestSagaStepRegistrationRunsThroughHost(t *testing.T) {
	host := NewHost(&Registry{Plugins: []Plugin{{
		Manifest: sagaManifest(),
		Source:   Source{Kind: SourcePath, Path: "fixture"},
	}}}, fakeRunner{saga: pluginapi.SagaStepResultResponse{Status: string(steps.StatusSucceeded), Summary: "done"}})

	registered := SagaSteps(host)
	step := registered["plugin.wait-for-cert"]
	if step == nil {
		t.Fatalf("registered steps = %+v", registered)
	}
	result, err := step.Run(context.Background(), steps.StepRequest{
		SagaID:  "saga_01",
		TraceID: "tr_saga",
		Node:    schema.SagaNode{ID: "wait", Kind: "plugin.wait-for-cert", Params: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != steps.StatusSucceeded || result.Summary != "done" {
		t.Fatalf("result = %+v", result)
	}
}

func baseManifest() pluginapi.Manifest {
	return pluginapi.Manifest{
		APIVersion: pluginapi.APIVersion,
		Kind:       pluginapi.KindPlugin,
		Name:       "mtls-egress",
		Version:    "0.1.0",
		Runtime:    pluginapi.RuntimeSpec{Kind: pluginapi.RuntimeCommand, Command: []string{"fixture"}},
		Hooks:      []pluginapi.Hook{pluginapi.HookMutateIR},
		Permissions: pluginapi.Permissions{
			AllowedPatchKinds: []string{PatchKindSecurityGroupRule},
		},
		Capabilities: []pluginapi.Capability{{
			Kind:       pluginapi.CapabilityIRPatch,
			Name:       "mtls-egress-rule",
			PatchKinds: []string{PatchKindSecurityGroupRule},
		}},
	}
}

func doctorManifest() pluginapi.Manifest {
	manifest := baseManifest()
	manifest.Name = "doctor-check"
	manifest.Hooks = []pluginapi.Hook{pluginapi.HookDoctorChecks}
	manifest.Permissions = pluginapi.Permissions{DoctorChecks: true}
	manifest.Capabilities = []pluginapi.Capability{{
		Kind:         pluginapi.CapabilityDoctorCheck,
		Name:         "doctor-check",
		DoctorChecks: []string{"PLUGIN_CHECK"},
	}}
	return manifest
}

func sagaManifest() pluginapi.Manifest {
	manifest := baseManifest()
	manifest.Name = "saga-step"
	manifest.Hooks = []pluginapi.Hook{pluginapi.HookSagaStep}
	manifest.Permissions = pluginapi.Permissions{SagaStepKinds: []string{"plugin.wait-for-cert"}}
	manifest.Capabilities = []pluginapi.Capability{{
		Kind:          pluginapi.CapabilitySagaStep,
		Name:          "wait-for-cert",
		SagaStepKinds: []string{"plugin.wait-for-cert"},
	}}
	return manifest
}

func pluginTestGraph() *ir.Graph {
	return &ir.Graph{
		SchemaVersion: ir.SchemaVersion,
		Service:       "payments-api",
		Env:           "prod",
		Resources: ir.Resources{
			SecurityGroups: []ir.SecurityGroup{{
				Meta: ir.ResourceMeta{
					LogicalID: "security-group:payments-api",
					Kind:      ir.ResourceKindSecurityGroup,
					Name:      "prod-payments-api-sg",
				},
			}},
		},
	}
}
