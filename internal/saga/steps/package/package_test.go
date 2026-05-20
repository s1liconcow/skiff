package packagestep_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/saga/steps"
	packagestep "github.com/s1liconcow/skiff/internal/saga/steps/package"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

func TestPackageStepPlanRunResumeCompensateDoctor(t *testing.T) {
	step, phases := testStep(t, func(req pluginapi.PackageStepRequest, response any) error {
		switch req.Phase {
		case sagaapi.StepPhasePlan:
			return assign(response, sagaapi.PackageStepPlanResponse{Summary: "plan package step", Risk: sagaapi.RiskLow, Reversibility: sagaapi.Reversible})
		case sagaapi.StepPhaseDoctor:
			return assign(response, sagaapi.PackageStepDoctorResponse{Findings: []sagaapi.PackageStepFinding{{Code: "PACKAGE_STEP_OK", Summary: "healthy"}}})
		default:
			return assign(response, sagaapi.PackageStepResultResponse{
				Status:  sagaapi.StepStatusSucceeded,
				Result:  json.RawMessage(`{"ok":true}`),
				Summary: "completed",
				ProviderOperations: []sagaapi.ProviderOperationRef{{
					Provider: "fake",
					Kind:     "package-step",
					ID:       "op-123",
				}},
			})
		}
	})
	req := packageStepRequest(json.RawMessage(`{"target":"payments","admin_password":"secret-value"}`))

	plan, err := step.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Summary != "plan package step" || plan.Risk != schema.RiskLow || plan.Reversibility != schema.Reversible {
		t.Fatalf("plan = %+v", plan)
	}
	if result, err := step.Run(context.Background(), req); err != nil || result.Status != steps.StatusSucceeded || len(result.ProviderOperations) != 1 {
		t.Fatalf("Run result = %+v err=%v", result, err)
	}
	if result, err := step.Resume(context.Background(), req); err != nil || result.Status != steps.StatusSucceeded {
		t.Fatalf("Resume result = %+v err=%v", result, err)
	}
	if result, err := step.Compensate(context.Background(), req, schema.StepResult{Status: string(steps.StatusSucceeded), Result: json.RawMessage(`{"ok":true}`)}); err != nil || result.Status != steps.StatusSucceeded {
		t.Fatalf("Compensate result = %+v err=%v", result, err)
	}
	findings, err := step.Doctor(context.Background(), req)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(findings) != 1 || findings[0].Code != "PACKAGE_STEP_OK" {
		t.Fatalf("findings = %+v", findings)
	}
	want := []sagaapi.StepPhase{sagaapi.StepPhasePlan, sagaapi.StepPhaseRun, sagaapi.StepPhaseResume, sagaapi.StepPhaseCompensate, sagaapi.StepPhaseDoctor}
	if got := *phases; len(got) != len(want) {
		t.Fatalf("phases = %+v", got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("phases = %+v, want %+v", got, want)
			}
		}
	}
}

func TestPackageStepRedactsSecretsAndPreservesFailureShape(t *testing.T) {
	const secret = "super-secret-token"
	var captured []byte
	step, _ := testStep(t, func(req pluginapi.PackageStepRequest, response any) error {
		body, err := json.Marshal(req)
		if err != nil {
			return err
		}
		captured = body
		switch req.Phase {
		case sagaapi.StepPhaseDoctor:
			return assign(response, sagaapi.PackageStepDoctorResponse{Findings: []sagaapi.PackageStepFinding{{
				Code:    "LEAK",
				Summary: "summary contains " + secret,
			}}})
		default:
			return assign(response, sagaapi.PackageStepResultResponse{
				Status:  sagaapi.StepStatusFailed,
				Result:  json.RawMessage(`{"admin_password":"` + secret + `","safe":"ok"}`),
				Summary: "failed with " + secret,
				Failure: &sagaapi.StepFailure{
					Code:      "PACKAGE_STEP_FAILED",
					Summary:   "failure " + secret,
					Cause:     "cause " + secret,
					Retriable: true,
				},
				ProviderOperations: []sagaapi.ProviderOperationRef{{
					Provider:    "fake",
					Kind:        "package-step",
					ID:          "op-redact",
					Description: "operation saw " + secret,
				}},
			})
		}
	})
	req := packageStepRequest(json.RawMessage(`{"target":"payments","admin_password":"` + secret + `"}`))
	result, err := step.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if bytes.Contains(captured, []byte(secret)) {
		t.Fatalf("hook request leaked secret: %s", string(captured))
	}
	if bytes.Contains(captured, []byte("provider_client")) || bytes.Contains(captured, []byte("cloud_sdk")) {
		t.Fatalf("hook request exposed provider internals: %s", string(captured))
	}
	body, _ := json.Marshal(result)
	if bytes.Contains(body, []byte(secret)) {
		t.Fatalf("step result leaked secret: %s", string(body))
	}
	if result.Status != steps.StatusFailed || result.Failure == nil || !result.Failure.Retriable {
		t.Fatalf("failure shape = %+v", result)
	}
	findings, err := step.Doctor(context.Background(), req)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(findings) != 1 || strings.Contains(findings[0].Summary, secret) {
		t.Fatalf("doctor finding leaked secret: %+v", findings)
	}
}

func TestPackageStepRejectsUnsupportedParams(t *testing.T) {
	step, _ := testStep(t, func(req pluginapi.PackageStepRequest, response any) error {
		return nil
	})
	if err := step.ValidateParams(context.Background(), json.RawMessage(`{"target":"payments","unknown":true}`)); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("ValidateParams err = %v, want undeclared param", err)
	}
}

func testStep(t *testing.T, run func(pluginapi.PackageStepRequest, any) error) (steps.Step, *[]sagaapi.StepPhase) {
	t.Helper()
	phases := []sagaapi.StepPhase{}
	step, err := packagestep.New(pluginapi.Manifest{Name: "fake-package", Version: "1.0.0"}, sagaapi.PackageStepCapability{
		Kind:    "fake.verify-ready",
		Summary: "verify readiness",
		Params: map[string]sagaapi.ParamSchema{
			"target":         {Type: sagaapi.ParamString, Required: true},
			"admin_password": {Type: sagaapi.ParamString, Secret: true},
		},
		Result: map[string]sagaapi.ParamSchema{
			"admin_password": {Type: sagaapi.ParamString, Secret: true},
		},
		Risk:          sagaapi.RiskLow,
		Reversibility: sagaapi.Reversible,
	}, func(ctx context.Context, hook pluginapi.Hook, request any, response any) error {
		if hook != pluginapi.HookPackageStep {
			t.Fatalf("hook = %s, want package_step", hook)
		}
		req, ok := request.(pluginapi.PackageStepRequest)
		if !ok {
			t.Fatalf("request type = %T", request)
		}
		phases = append(phases, req.Phase)
		return run(req, response)
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return step, &phases
}

func packageStepRequest(params json.RawMessage) steps.StepRequest {
	return steps.StepRequest{
		SagaID:  "saga_pkg",
		TraceID: "tr_pkg",
		Intent: schema.SagaIntent{
			SagaID: "saga_pkg",
			Target: schema.Target{
				Kind: "StatefulGroup",
				Name: "payments",
			},
			Package: &schema.PackageProvenance{
				Ref:    "skiff.dev/fake-package",
				Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
		Node: schema.SagaNode{
			ID:     "verify",
			Kind:   "fake.verify-ready",
			Params: params,
		},
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
