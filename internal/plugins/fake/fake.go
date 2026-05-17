package fake

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/s1liconcow/skiff/internal/plugins"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

const (
	Name              = "skiff-fake-plugin"
	Version           = "0.1.0"
	SagaStepKind      = "plugin.fake-step"
	DoctorFindingCode = "FAKE_PLUGIN_CHECK"
)

type Runner struct{}

func Plugin() plugins.Plugin {
	return plugins.Plugin{
		Manifest: Manifest(),
		Source: plugins.Source{
			Kind: plugins.SourcePath,
			Path: "internal/plugins/fake",
		},
	}
}

func Manifest() pluginapi.Manifest {
	return pluginapi.Manifest{
		APIVersion:  pluginapi.APIVersion,
		Kind:        pluginapi.KindPlugin,
		Name:        Name,
		Version:     Version,
		Description: "Reference fake plugin used by Skiff plugin conformance tests.",
		Runtime: pluginapi.RuntimeSpec{
			Kind:    pluginapi.RuntimeCommand,
			Command: []string{"skiff-fake-plugin"},
		},
		Hooks: []pluginapi.Hook{
			pluginapi.HookValidate,
			pluginapi.HookMutateIR,
			pluginapi.HookRuntimeAddons,
			pluginapi.HookDoctorChecks,
			pluginapi.HookSagaStep,
		},
		Permissions: pluginapi.Permissions{
			AllowedPatchKinds: []string{plugins.PatchKindSecurityGroupRule},
			RuntimeAddons:     true,
			DoctorChecks:      true,
			SagaStepKinds:     []string{SagaStepKind},
		},
		Capabilities: []pluginapi.Capability{
			{
				Kind:       pluginapi.CapabilityIRPatch,
				Name:       "security-group-egress",
				PatchKinds: []string{plugins.PatchKindSecurityGroupRule},
			},
			{
				Kind:          pluginapi.CapabilityRuntimeAddon,
				Name:          "systemd-dropin",
				RuntimeAddons: []string{"systemd-dropin"},
			},
			{
				Kind:         pluginapi.CapabilityDoctorCheck,
				Name:         "fake-health",
				DoctorChecks: []string{DoctorFindingCode},
			},
			{
				Kind:          pluginapi.CapabilitySagaStep,
				Name:          "fake-step",
				SagaStepKinds: []string{SagaStepKind},
			},
		},
	}
}

func (Runner) RunPluginHook(ctx context.Context, plugin plugins.Plugin, hook pluginapi.Hook, request any, response any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if plugin.Manifest.Name != Name {
		return fmt.Errorf("fake plugin runner received plugin %q", plugin.Manifest.Name)
	}
	switch hook {
	case pluginapi.HookValidate:
		return assign(response, pluginapi.ValidateResponse{Diagnostics: []pluginapi.Diagnostic{{
			Code:     "FAKE_PLUGIN_VALIDATED",
			Severity: "info",
			Summary:  "fake plugin validate hook ran",
		}}})
	case pluginapi.HookMutateIR:
		return assign(response, pluginapi.MutateIRResponse{Patches: []pluginapi.IRPatch{SecurityGroupPatch()}})
	case pluginapi.HookRuntimeAddons:
		return assign(response, pluginapi.RuntimeAddonsResponse{Addons: []pluginapi.RuntimeAddon{{
			Kind:    "systemd-dropin",
			Name:    "fake-hardening",
			Target:  "workload",
			Summary: "fake plugin supplied a systemd hardening drop-in",
			Config:  json.RawMessage(`{"NoNewPrivileges":true}`),
		}}})
	case pluginapi.HookDoctorChecks:
		return assign(response, pluginapi.DoctorChecksResponse{Findings: []pluginapi.DoctorFinding{{
			Code:       DoctorFindingCode,
			Severity:   "low",
			Service:    "payments-api",
			Summary:    "fake plugin doctor check passed",
			Confidence: 1,
		}}})
	case pluginapi.HookSagaStep:
		req, _ := request.(pluginapi.SagaStepRequest)
		return runSagaPhase(req, response)
	default:
		return nil
	}
}

func SecurityGroupPatch() pluginapi.IRPatch {
	return pluginapi.IRPatch{
		Op:      pluginapi.PatchAdd,
		Path:    "/resources/security_groups/security-group:payments-api/rules/-",
		Kind:    plugins.PatchKindSecurityGroupRule,
		Value:   json.RawMessage(`{"direction":"egress","protocol":"tcp","from_port":8443,"to_port":8443,"destination":"10.0.0.0/8","description":"fake plugin mTLS egress"}`),
		Summary: "allow fake plugin mTLS egress",
		Source: pluginapi.PatchSource{
			Plugin:     Name,
			Version:    Version,
			Capability: "security-group-egress",
		},
	}
}

func runSagaPhase(req pluginapi.SagaStepRequest, response any) error {
	switch req.Phase {
	case "plan":
		return assign(response, pluginapi.SagaStepPlanResponse{
			Summary:       "fake plugin saga step will verify external readiness",
			Risk:          string(schema.RiskLow),
			Reversibility: string(schema.Reversible),
		})
	case "doctor":
		return assign(response, struct {
			Findings []steps.Finding `json:"findings,omitempty"`
		}{Findings: []steps.Finding{{
			Code:     "FAKE_PLUGIN_STEP_OK",
			Severity: "low",
			Summary:  "fake plugin saga step is healthy",
		}}})
	default:
		return assign(response, pluginapi.SagaStepResultResponse{
			Status:  string(steps.StatusSucceeded),
			Result:  json.RawMessage(`{"phase":"` + req.Phase + `"}`),
			Summary: "fake plugin saga step " + req.Phase + " completed",
			ProviderOperations: []pluginapi.ProviderOperationRef{{
				Provider:    "fake",
				Kind:        "plugin-step",
				ID:          "fake-plugin-step-" + req.Phase,
				ObservedAt:  "2026-05-17T00:00:00Z",
				Description: "reference plugin operation",
			}},
		})
	}
}

func assign(dst any, src any) error {
	body, err := json.Marshal(src)
	if err != nil {
		return err
	}
	if dst == nil {
		return nil
	}
	return json.Unmarshal(body, dst)
}
