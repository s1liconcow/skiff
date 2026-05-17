package e2e_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestLocalCLIEndToEndCapabilityMatrix(t *testing.T) {
	resetSkiffEnv(t)
	ctx := context.Background()
	stateDir := t.TempDir()
	store, err := file.New(stateDir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	const (
		service       = "http-hello"
		env           = "prod"
		region        = "local"
		traceID       = "tr_local_e2e"
		keyID         = "local-e2e"
		firstRelease  = "rel_local_01"
		secondRelease = "rel_local_02"
		canaryRelease = "rel_local_canary"
		firstOp       = "op_local_deploy_01"
		secondOp      = "op_local_deploy_02"
		canaryOp      = "op_local_canary"
		rollbackOp    = "op_local_rollback"
	)
	report := newE2EReport(t, "local", service, env, traceID)
	report.CleanupStatus = "file object state isolated in test tempdir"
	defer writeE2EReport(t, report)

	specPath := filepath.Join("..", "..", "examples", "service", "http-hello", "skiff.yaml")
	stateURI := "file://" + stateDir
	seed := []byte(strings.Repeat("L", ed25519.SeedSize))
	signingSeed := base64.StdEncoding.EncodeToString(seed)
	signer, err := signing.NewLocalSignerFromSeed(keyID, seed)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}
	publicKeyArg := keyID + "=" + base64.StdEncoding.EncodeToString(signer.PublicKey())

	var ok basicOK
	decodeCLIJSON(t, runSkiffCLI(t, report, "validate", specPath, "--format", "json", "--trace-id", traceID), &ok)
	if !ok.OK {
		t.Fatal("validate returned ok=false")
	}

	var compiled localCompileOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "compile", specPath, "--format", "json", "--trace-id", traceID), &compiled)
	if !compiled.OK || compiled.Graph.Service != service {
		t.Fatalf("unexpected compile output: %+v", compiled)
	}

	var planned localPlanOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "plan", specPath, "--provider", "aws", "--region", "us-west-2", "--state", stateURI, "--format", "json", "--trace-id", traceID), &planned)
	if !planned.OK || planned.Plan.Service != service || len(planned.Plan.Resources) == 0 {
		t.Fatalf("unexpected plan output: %+v", planned)
	}
	for _, resource := range planned.Plan.Resources {
		report.addProviderID(resource.ProviderID)
	}

	var explained localExplainOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "explain", specPath, "--provider", "aws", "--region", "us-west-2", "--state", stateURI, "--release-id", firstRelease, "--format", "json", "--trace-id", traceID), &explained)
	if !explained.OK || explained.Result.Service != service || len(explained.Result.Resources) == 0 {
		t.Fatalf("unexpected explain output: %+v", explained)
	}

	var cost localCostOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "cost", "explain", service, "--file", specPath, "--cpu-p95", "18", "--memory-p95", "41", "--request-rps", "25", "--warm-capacity", "1", "--log-mb-per-hour", "64", "--format", "json", "--trace-id", traceID), &cost)
	if !cost.OK || cost.Result.Service != service || len(cost.Result.Recommendations) == 0 || len(cost.Result.Limitations) == 0 {
		t.Fatalf("unexpected cost explain output: %+v", cost)
	}

	deployRelease(t, report, specPath, stateURI, env, region, keyID, signingSeed, firstRelease, firstOp, traceID)
	firstProviderID := startLocalRollout(t, ctx, store, service, env, firstOp, firstRelease, traceID)
	report.addProviderID(firstProviderID)
	watchRollout(t, report, service, stateURI, env, region, firstOp, firstProviderID, traceID)
	assertStableRelease(t, store, service, firstRelease)

	secondDeploy := deployRelease(t, report, specPath, stateURI, env, region, keyID, signingSeed, secondRelease, secondOp, traceID)
	if !secondDeploy.OK || !secondDeploy.Result.OK {
		t.Fatalf("unexpected deploy output: %+v", secondDeploy)
	}
	verifyReleaseObjects(t, report, stateDir, service, env, secondRelease, publicKeyArg, traceID)

	secondProviderID := startLocalRollout(t, ctx, store, service, env, secondOp, secondRelease, traceID)
	report.addProviderID(secondProviderID)
	watchRollout(t, report, service, stateURI, env, region, secondOp, secondProviderID, traceID)
	assertStableRelease(t, store, service, secondRelease)

	var status localStatusOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "status", service, "--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region, "--format", "json", "--trace-id", traceID), &status)
	if !status.OK || len(status.Status.Services) != 1 || status.Status.Services[0].Service != service {
		t.Fatalf("unexpected status output: %+v", status)
	}

	var events localEventsOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "events", "--scope", "service", "--service", service, "--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region, "--format", "json", "--trace-id", traceID), &events)
	if !events.OK || len(events.Result.Events) == 0 {
		t.Fatalf("unexpected events output: %+v", events)
	}

	var logs localLogsOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "logs", service, "--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region, "--format", "json", "--trace-id", traceID), &logs)
	if !logs.OK || len(logs.Entries) == 0 {
		t.Fatalf("unexpected logs output: %+v", logs)
	}

	var metrics localMetricsOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "metrics", service, "--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region, "--format", "json", "--trace-id", traceID), &metrics)
	if !metrics.OK || len(metrics.Series) == 0 {
		t.Fatalf("unexpected metrics output: %+v", metrics)
	}

	var doctor localDoctorOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "doctor", service, "--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region, "--format", "json", "--trace-id", traceID), &doctor)
	if !doctor.OK {
		t.Fatalf("unexpected doctor output: %+v", doctor)
	}
	for _, finding := range doctor.Doctor.Findings {
		if finding.Severity == "critical" {
			t.Fatalf("doctor returned critical finding: %+v", doctor.Doctor.Findings)
		}
	}

	var debug localDebugOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "debug", "collect", service, "--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region, "--instance", "i-local-debug", "--approval-id", "approval_local_debug", "--format", "json", "--trace-id", traceID), &debug)
	if !debug.OK || !debug.Bundle.OK || debug.Bundle.BundleID == "" || debug.Bundle.ReleaseDigest == "" || len(debug.Bundle.Logs) == 0 {
		t.Fatalf("unexpected debug collect output: %+v", debug)
	}

	var canary localCanaryOutput
	canaryBody := runSkiffCLI(t, report,
		"deploy", specPath,
		"--canary",
		"--canary-stages", "100",
		"--canary-bake", "0s",
		"--canary-metric", "request_count",
		"--canary-threshold", "1",
		"--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region,
		"--release-id", canaryRelease,
		"--operation-id", canaryOp,
		"--key-id", keyID,
		"--signing-seed-base64", signingSeed,
		"--format", "json",
		"--trace-id", traceID,
	)
	decodeCLIJSON(t, canaryBody, &canary)
	if !canary.OK || canary.Result.SagaID == "" || canary.Result.Status != string(schema.SagaSucceeded) {
		t.Fatalf("unexpected canary output: %+v\n%s", canary, string(canaryBody))
	}
	report.addOperationID(canary.Result.OperationID)
	report.addSagaID(canary.Result.SagaID)
	assertStableRelease(t, store, service, canaryRelease)

	var rolledBack localRollbackOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"rollback", service,
		"--to", secondRelease,
		"--operation-id", rollbackOp,
		"--saga-id", "saga_local_rollback",
		"--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region,
		"--format", "json",
		"--trace-id", traceID,
	), &rolledBack)
	if !rolledBack.OK || !rolledBack.Result.OK || rolledBack.Result.ToRelease != secondRelease {
		t.Fatalf("unexpected rollback output: %+v", rolledBack)
	}
	report.addOperationID(rolledBack.Result.OperationID)
	report.addSagaID(rolledBack.Result.SagaID)
	assertStableRelease(t, store, service, secondRelease)

	var drift localDriftOutput
	decodeCLIJSON(t, runSkiffCLI(t, report, "drift", service, "--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region, "--format", "json", "--trace-id", traceID), &drift)
	if !drift.OK || len(drift.Result.Findings) == 0 {
		t.Fatalf("unexpected drift output: %+v", drift)
	}

	var plugin basicOK
	decodeCLIJSON(t, runSkiffCLI(t, report, "plugin", "validate", filepath.Join("..", "fixtures", "plugins", "fake"), "--format", "json", "--trace-id", traceID), &plugin)
	if !plugin.OK {
		t.Fatal("plugin validate returned ok=false")
	}

	report.fact("local_e2e", "validated compile/plan/explain/cost/deploy/rollout/status/events/logs/metrics/doctor/debug/canary/rollback/drift/plugin paths")
}

func deployRelease(t *testing.T, report *e2eReport, specPath, stateURI, env, region, keyID, signingSeed, releaseID, operationID, traceID string) localDeployOutput {
	t.Helper()
	var deployed localDeployOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"deploy", specPath,
		"--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region,
		"--release-id", releaseID,
		"--operation-id", operationID,
		"--key-id", keyID,
		"--signing-seed-base64", signingSeed,
		"--format", "json",
		"--trace-id", traceID,
	), &deployed)
	if !deployed.OK || !deployed.Result.OK || deployed.Result.ReleaseID != releaseID {
		t.Fatalf("unexpected deploy output: %+v", deployed)
	}
	report.addOperationID(deployed.Result.OperationID)
	for _, resource := range deployed.Result.Plan.Resources {
		report.addProviderID(resource.ProviderID)
	}
	releaseKey, err := paths.ReleaseManifest(deployed.Result.Plan.Service, releaseID)
	if err == nil {
		report.addObjectPath(releaseKey)
	}
	runtimeKey, err := paths.RuntimeManifest(deployed.Result.Plan.Service, releaseID)
	if err == nil {
		report.addObjectPath(runtimeKey)
	}
	return deployed
}

func startLocalRollout(t *testing.T, ctx context.Context, store *file.Store, service, env, operationID, releaseID, traceID string) string {
	t.Helper()
	rollout, err := (deploy.Deployer{
		Store:    store,
		Provider: fakeprovider.New(fakeprovider.WithStateStore(store)),
	}).StartRollout(ctx, deploy.StartRolloutRequest{
		Service:     service,
		Env:         env,
		OperationID: operationID,
		ReleaseID:   releaseID,
		TraceID:     traceID,
		Actor:       schema.Actor{ID: "e2e", Type: "agent"},
	})
	if err != nil {
		t.Fatalf("start rollout: %v", err)
	}
	return rollout.ProviderID
}

func watchRollout(t *testing.T, report *e2eReport, service, stateURI, env, region, operationID, providerID, traceID string) {
	t.Helper()
	var rollout localRolloutOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"rollout", "watch",
		"--service", service,
		"--operation", operationID,
		"--provider-id", providerID,
		"--direct", "--state", stateURI, "--env", env, "--provider", fakeprovider.Name, "--region", region,
		"--format", "json",
		"--trace-id", traceID,
	), &rollout)
	if !rollout.OK || rollout.Status.Status != "succeeded" {
		t.Fatalf("unexpected rollout output: %+v", rollout)
	}
	report.addProviderID(rollout.Status.ProviderID)
}

func verifyReleaseObjects(t *testing.T, report *e2eReport, stateDir, service, env, releaseID, publicKeyArg, traceID string) {
	t.Helper()
	releaseKey, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeKey, err := paths.RuntimeManifest(service, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(stateDir, filepath.FromSlash(releaseKey))
	runtimePath := filepath.Join(stateDir, filepath.FromSlash(runtimeKey))
	var verified localVerifyOutput
	decodeCLIJSON(t, runSkiffCLI(t, report,
		"release", "verify", releasePath,
		"--runtime-manifest", runtimePath,
		"--public-key", publicKeyArg,
		"--service", service,
		"--env", env,
		"--format", "json",
		"--trace-id", traceID,
	), &verified)
	if !verified.OK || !verified.Result.OK {
		t.Fatalf("unexpected release verify output: %+v", verified)
	}
	report.addObjectPath(releaseKey)
	report.addObjectPath(runtimeKey)
}

func assertStableRelease(t *testing.T, store *file.Store, service, releaseID string) {
	t.Helper()
	doc, err := state.NewClient(store).GetServiceControl(context.Background(), service)
	if err != nil {
		t.Fatalf("get service control: %v", err)
	}
	if doc.Control.StableRelease != releaseID || doc.Control.DesiredRelease != releaseID {
		t.Fatalf("service control release mismatch: desired=%q stable=%q want=%q", doc.Control.DesiredRelease, doc.Control.StableRelease, releaseID)
	}
}

type basicOK struct {
	OK bool `json:"ok"`
}

type localCompileOutput struct {
	OK    bool `json:"ok"`
	Graph struct {
		Service string `json:"service"`
		Env     string `json:"env"`
	} `json:"graph"`
}

type localPlanOutput struct {
	OK   bool `json:"ok"`
	Plan struct {
		Service   string `json:"service"`
		Resources []struct {
			Kind       string `json:"kind"`
			ProviderID string `json:"provider_id"`
		} `json:"resources"`
	} `json:"plan"`
}

type localExplainOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		Service   string `json:"service"`
		Resources []struct {
			CloudPrimitive string `json:"cloud_primitive"`
			Name           string `json:"name"`
		} `json:"resources"`
	} `json:"result"`
}

type localCostOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		Service         string `json:"service"`
		Recommendations []struct {
			ID         string `json:"id"`
			Confidence string `json:"confidence"`
		} `json:"recommendations"`
		Limitations []string `json:"limitations"`
	} `json:"result"`
}

type localDeployOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		OK          bool   `json:"ok"`
		OperationID string `json:"operation_id"`
		ReleaseID   string `json:"release_id"`
		Plan        struct {
			Service   string `json:"service"`
			Resources []struct {
				ProviderID string `json:"provider_id"`
			} `json:"resources"`
		} `json:"plan"`
	} `json:"result"`
}

type localVerifyOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		OK bool `json:"ok"`
	} `json:"result"`
}

type localRolloutOutput struct {
	OK     bool `json:"ok"`
	Status struct {
		Status     string `json:"status"`
		ProviderID string `json:"provider_id"`
	} `json:"status"`
}

type localStatusOutput struct {
	OK     bool `json:"ok"`
	Status struct {
		Services []struct {
			Service string `json:"service"`
			Health  string `json:"health"`
		} `json:"services"`
	} `json:"status"`
}

type localEventsOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		Events []schema.Event `json:"events"`
		Scope  map[string]any `json:"scope"`
	} `json:"result"`
}

type localLogsOutput struct {
	OK      bool `json:"ok"`
	Entries []struct {
		Message string `json:"message"`
	} `json:"entries"`
}

type localMetricsOutput struct {
	OK     bool `json:"ok"`
	Series []struct {
		Name string `json:"name"`
	} `json:"series"`
}

type localDoctorOutput struct {
	OK     bool `json:"ok"`
	Doctor struct {
		Findings []struct {
			Severity string `json:"severity"`
			Code     string `json:"code"`
		} `json:"findings"`
	} `json:"doctor"`
}

type localDebugOutput struct {
	OK     bool `json:"ok"`
	Bundle struct {
		OK            bool   `json:"ok"`
		BundleID      string `json:"bundle_id"`
		ReleaseDigest string `json:"release_digest"`
		Logs          []struct {
			Message string `json:"message"`
		} `json:"logs"`
	} `json:"bundle"`
}

type localCanaryOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		SagaID      string `json:"saga_id"`
		OperationID string `json:"operation_id"`
		Status      string `json:"status"`
	} `json:"result"`
}

type localRollbackOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		OK          bool   `json:"ok"`
		OperationID string `json:"operation_id"`
		SagaID      string `json:"saga_id"`
		ToRelease   string `json:"to_release"`
	} `json:"result"`
}

type localDriftOutput struct {
	OK     bool `json:"ok"`
	Result struct {
		Findings []struct {
			Code string `json:"code"`
		} `json:"findings"`
	} `json:"result"`
}
