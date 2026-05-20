package packageconformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	opsstate "github.com/s1liconcow/skiff/internal/ops"
	"github.com/s1liconcow/skiff/internal/packages"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

type Suite struct {
	Ref                string
	CacheRoot          string
	AllowUnsignedLocal bool
}

func Run(t *testing.T, suite Suite) packages.ConformanceResult {
	t.Helper()
	if suite.Ref == "" {
		t.Fatal("package ref is required")
	}
	resolved, err := packages.Resolve(context.Background(), suite.Ref, packages.ResolveOptions{
		Cache:              packages.Cache{Root: suite.CacheRoot},
		AllowUnsignedLocal: suite.AllowUnsignedLocal,
		Clock:              func() time.Time { return time.Date(2026, 5, 20, 4, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	result := packages.RunConformance(context.Background(), *resolved, packages.ConformanceOptions{
		AllowUnsignedLocal:   suite.AllowUnsignedLocal,
		OperationProfileHook: operationProfileHook,
	})
	if !result.OK {
		t.Fatalf("conformance failed: %+v", result.Diagnostics)
	}
	requireCheck(t, result, "package_manifest")
	requireCheck(t, result, "lockfile_compatibility")
	requireCheck(t, result, "plugin_manifest")
	requireCheck(t, result, "package_steps")
	requireCheck(t, result, "doctor_checks")
	requireCheck(t, result, "operation_profile.primary-switchover-update.explain")
	requireCheck(t, result, "operation_profile.primary-switchover-update.render")
	requireCheck(t, result, "cli_examples")
	return result
}

func operationProfileHook(ctx context.Context, name string, provenance schema.PackageProvenance) []packages.ConformanceCheck {
	profile, ok := operationProfile(name)
	if !ok {
		return []packages.ConformanceCheck{{
			ID:      "operation_profile." + name,
			Status:  packages.ConformanceFailed,
			Summary: "operation profile export is not available",
			Diagnostics: []spec.Diagnostic{{
				Path:     "$.exports.operation_profiles",
				Code:     "OPERATION_PROFILE_NOT_FOUND",
				Severity: spec.SeverityError,
				Message:  "operation profile is not available",
			}},
		}}
	}
	explained, err := opsstate.ExplainProfile(profile)
	if err != nil {
		return failedProfileCheck(name, "explain", err)
	}
	explainDetails, _ := json.Marshal(explained)
	rendered, err := opsstate.RenderProfile(opsstate.ProfileRenderRequest{
		Profile:   profile,
		SagaID:    "saga_conformance",
		Target:    schema.Target{Kind: "StatefulGroup", Name: "fake-postgres"},
		Actor:     schema.Actor{ID: "package-conformance", Type: "agent"},
		TraceID:   "tr_package_conformance",
		Params:    sampleParams(profile),
		Package:   provenance,
		CreatedAt: time.Date(2026, 5, 20, 4, 30, 0, 0, time.UTC),
	})
	if err != nil {
		return failedProfileCheck(name, "render", err)
	}
	renderDetails, _ := json.Marshal(map[string]any{"saga_id": rendered.Intent.SagaID, "steps": len(rendered.Graph.Nodes)})
	return []packages.ConformanceCheck{
		{ID: "operation_profile." + name + ".explain", Status: packages.ConformancePassed, Summary: "operation profile explain output is valid", Details: explainDetails},
		{ID: "operation_profile." + name + ".render", Status: packages.ConformancePassed, Summary: "operation profile renders a saga graph", Details: renderDetails},
	}
}

func operationProfile(name string) (sagaapi.OperationProfile, bool) {
	for _, profile := range opsstate.BuiltInProfiles() {
		if profile.Name == name || string(profile.Kind) == name {
			return profile, true
		}
	}
	return sagaapi.OperationProfile{}, false
}

func sampleParams(profile sagaapi.OperationProfile) map[string]json.RawMessage {
	params := make(map[string]json.RawMessage, len(profile.Params))
	for name, param := range profile.Params {
		if len(param.Default) > 0 {
			params[name] = append(json.RawMessage(nil), param.Default...)
			continue
		}
		switch param.Type {
		case sagaapi.ParamBoolean:
			params[name] = json.RawMessage(`false`)
		case sagaapi.ParamInteger, sagaapi.ParamNumber:
			params[name] = json.RawMessage(`1`)
		case sagaapi.ParamObject:
			params[name] = json.RawMessage(`{}`)
		case sagaapi.ParamArray:
			params[name] = json.RawMessage(`[]`)
		default:
			params[name] = json.RawMessage(`"fixture"`)
		}
	}
	return params
}

func failedProfileCheck(name, phase string, err error) []packages.ConformanceCheck {
	return []packages.ConformanceCheck{{
		ID:      "operation_profile." + name + "." + phase,
		Status:  packages.ConformanceFailed,
		Summary: "operation profile " + phase + " failed",
		Diagnostics: []spec.Diagnostic{{
			Path:     "$.exports.operation_profiles",
			Code:     "OPERATION_PROFILE_" + phase,
			Severity: spec.SeverityError,
			Message:  err.Error(),
		}},
	}}
}

func requireCheck(t *testing.T, result packages.ConformanceResult, id string) {
	t.Helper()
	for _, check := range result.Checks {
		if check.ID == id && check.Status == packages.ConformancePassed {
			return
		}
	}
	t.Fatalf("missing passed check %s in %+v", id, result.Checks)
}
