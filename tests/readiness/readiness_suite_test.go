package readiness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/cli"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	servicedoctor "github.com/s1liconcow/skiff/internal/doctor"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/ops"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/readiness"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
	"github.com/s1liconcow/skiff/internal/worker"
)

func TestProductionReadinessSuite(t *testing.T) {
	clearSkiffEnv(t)
	reportPath := os.Getenv("SKIFF_READINESS_REPORT")
	if reportPath == "" {
		reportPath = filepath.Join(t.TempDir(), "production-readiness.json")
	}
	report := readiness.New(readiness.Options{
		TraceID:     "tr_readiness",
		GeneratedAt: readinessNow(),
		Mode:        string(config.ModeDirect),
		Provider:    fakeprovider.Name,
		Region:      "local",
	})
	defer func() {
		if err := report.WriteJSON(reportPath); err != nil {
			t.Errorf("write readiness report: %v", err)
			return
		}
		t.Logf("wrote readiness report %s", reportPath)
	}()

	cases := []struct {
		id  string
		run func(t *testing.T) readiness.Scenario
	}{
		{id: "direct_recovery", run: directRecoveryScenario},
		{id: "operation_resume", run: operationResumeScenario},
		{id: "saga_resume", run: sagaResumeScenario},
		{id: "stale_cache_direct_refresh", run: staleCacheDirectRefreshScenario},
		{id: "lease_contention", run: leaseContentionScenario},
		{id: "failed_rollout_diagnosis", run: failedRolloutDiagnosisScenario},
		{id: "bad_release_rollback_recommendation", run: badReleaseRollbackRecommendationScenario},
		{id: "observability_outage_diagnosis", run: observabilityOutageDiagnosisScenario},
		{id: "debug_denial", run: debugDenialScenario},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			scenario := tc.run(t)
			report.AddScenario(scenario)
			if !scenario.OK {
				t.Fatalf("%s failed: %s", scenario.ID, scenario.Failure)
			}
		})
	}
	report.Finalize()
	if !report.OK {
		t.Fatalf("readiness report failed: %+v", report.Summary)
	}
}

func directRecoveryScenario(t *testing.T) readiness.Scenario {
	t.Helper()
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		return failedScenario("direct_recovery", "Direct mode diagnoses and recovers without skiffd", "recovery", err)
	}
	const service = "payments-api"
	if err := seedRollbackState(context.Background(), store, service, "prod", "rel_bad", "rel_good"); err != nil {
		return failedScenario("direct_recovery", "Direct mode diagnoses and recovers without skiffd", "recovery", err)
	}
	stateURI := "file://" + dir
	var status struct {
		OK     bool `json:"ok"`
		Status struct {
			Services []struct {
				Service string `json:"service"`
			} `json:"services"`
		} `json:"status"`
	}
	if err := runSkiffJSON(&status, "status", service, "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_readiness_direct"); err != nil {
		return failedScenario("direct_recovery", "Direct mode diagnoses and recovers without skiffd", "recovery", err)
	}
	if !status.OK || len(status.Status.Services) != 1 || status.Status.Services[0].Service != service {
		return failedScenario("direct_recovery", "Direct mode diagnoses and recovers without skiffd", "recovery", fmt.Errorf("unexpected status output: %+v", status))
	}
	var rolledBack struct {
		OK     bool `json:"ok"`
		Result struct {
			OK          bool   `json:"ok"`
			OperationID string `json:"operation_id"`
			SagaID      string `json:"saga_id"`
			ToRelease   string `json:"to_release"`
		} `json:"result"`
	}
	if err := runSkiffJSON(&rolledBack, "rollback", service, "--to", "rel_good", "--operation-id", "op_readiness_rollback", "--saga-id", "saga_readiness_rollback", "--direct", "--state", stateURI, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_readiness_direct"); err != nil {
		return failedScenario("direct_recovery", "Direct mode diagnoses and recovers without skiffd", "recovery", err)
	}
	if !rolledBack.OK || !rolledBack.Result.OK || rolledBack.Result.ToRelease != "rel_good" {
		return failedScenario("direct_recovery", "Direct mode diagnoses and recovers without skiffd", "recovery", fmt.Errorf("unexpected rollback output: %+v", rolledBack))
	}
	serviceKey, _ := paths.ServiceControl(service)
	releaseKey, _ := paths.ReleaseManifest(service, "rel_good")
	return readiness.Scenario{
		ID:       "direct_recovery",
		Title:    "Direct mode diagnoses and recovers without skiffd",
		Category: "recovery",
		OK:       true,
		Facts: []readiness.Fact{
			{Type: "status", Source: "direct_object_store", Message: "direct CLI status read service control without skiffd"},
			{Type: "rollback", Source: "direct_object_store", Message: "direct CLI rollback restored the stable release"},
		},
		ObjectPaths:  []string{serviceKey, releaseKey},
		ProviderIDs:  []string{"rollback-rel_good"},
		OperationIDs: []string{rolledBack.Result.OperationID},
		SagaIDs:      []string{rolledBack.Result.SagaID},
		Commands: []readiness.RecommendedAction{
			{ID: "inspect_status", Command: "skiff status payments-api --direct --state <state> --format json", Mutating: false},
			{ID: "rollback", Command: "skiff rollback payments-api --to rel_good --direct --state <state> --format json", Mutating: true, Safety: "reversible", Reversibility: string(schema.Reversible), Risk: string(schema.RiskMedium)},
		},
	}
}

func operationResumeScenario(t *testing.T) readiness.Scenario {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	if err := seedInterruptedOperation(ctx, store); err != nil {
		return failedScenario("operation_resume", "Interrupted operation resumes from object state", "resilience", err)
	}
	result, err := (ops.Resumer{Store: store, Provider: fakeprovider.New(fakeprovider.WithRolloutStatus("succeeded")), Clock: readinessNow}).Resume(ctx, ops.ResumeRequest{
		Service:     "payments-api",
		OperationID: "op_readiness_resume",
		Actor:       schema.Actor{ID: "readiness", Type: "agent"},
		TraceID:     "tr_readiness_resume",
		Owner:       "readiness",
	})
	if err != nil {
		return failedScenario("operation_resume", "Interrupted operation resumes from object state", "resilience", err)
	}
	if !result.Resumed || result.Status != schema.OperationSucceeded {
		return failedScenario("operation_resume", "Interrupted operation resumes from object state", "resilience", fmt.Errorf("unexpected resume result: %+v", result))
	}
	opKey, _ := paths.OperationControl("payments-api", "op_readiness_resume")
	return readiness.Scenario{
		ID:           "operation_resume",
		Title:        "Interrupted operation resumes from object state",
		Category:     "resilience",
		OK:           true,
		Facts:        []readiness.Fact{{Type: "operation_resume", Source: "object_state", Message: "stored provider operation ID was used to watch rollout and mark stable"}},
		ObjectPaths:  []string{opKey},
		ProviderIDs:  []string{"ir-readiness"},
		OperationIDs: []string{result.OperationID},
		Commands:     []readiness.RecommendedAction{{ID: "resume_operation", Command: "skiff ops resume op_readiness_resume --service payments-api --direct --state <state> --format json", Mutating: true, Safety: "resumes stored rollout status", Risk: string(schema.RiskLow)}},
	}
}

func sagaResumeScenario(t *testing.T) readiness.Scenario {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	sagas := sagastate.NewStore(store, sagastate.WithClock(readinessNow))
	_, err := sagas.Create(ctx, sagastate.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        "saga_readiness_resume",
			Kind:          "readiness.saga",
			Target:        schema.Target{Kind: "service", Name: "payments-api"},
			Actor:         schema.Actor{ID: "readiness", Type: "agent"},
			TraceID:       "tr_readiness_saga",
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
			Summary:       "readiness saga",
			CreatedAt:     canonical.Time(readinessNow()),
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        "saga_readiness_resume",
			Nodes:         []schema.SagaNode{{ID: "resume-step", Kind: "readiness.step"}},
			CreatedAt:     canonical.Time(readinessNow()),
		},
		Control: schema.NewSagaControl("saga_readiness_resume", schema.SagaPending, canonical.Time(readinessNow())),
	})
	if err != nil {
		return failedScenario("saga_resume", "Interrupted saga resumes from object state", "resilience", err)
	}
	step := &readinessStep{}
	result, err := (worker.Worker{
		Store:     store,
		Provider:  fakeprovider.New(),
		SagaSteps: map[string]steps.Step{"readiness.step": step},
		Owner:     "readiness-worker",
		Clock:     readinessNow,
	}).RunOnce(ctx)
	if err != nil {
		return failedScenario("saga_resume", "Interrupted saga resumes from object state", "resilience", err)
	}
	if result.SagaResumed != 1 || step.count != 1 {
		return failedScenario("saga_resume", "Interrupted saga resumes from object state", "resilience", fmt.Errorf("unexpected worker result: %+v step=%d", result, step.count))
	}
	inspect, err := sagas.Inspect(ctx, "saga_readiness_resume")
	if err != nil {
		return failedScenario("saga_resume", "Interrupted saga resumes from object state", "resilience", err)
	}
	if inspect.Status != schema.SagaSucceeded {
		return failedScenario("saga_resume", "Interrupted saga resumes from object state", "resilience", fmt.Errorf("saga status = %s", inspect.Status))
	}
	controlKey, _ := paths.SagaControl("saga_readiness_resume")
	return readiness.Scenario{
		ID:          "saga_resume",
		Title:       "Interrupted saga resumes from object state",
		Category:    "resilience",
		OK:          true,
		Facts:       []readiness.Fact{{Type: "saga_resume", Source: "worker", Message: "worker resumed pending saga graph and completed typed step"}},
		ObjectPaths: []string{controlKey},
		SagaIDs:     []string{"saga_readiness_resume"},
		Commands:    []readiness.RecommendedAction{{ID: "resume_saga", Command: "skiff ops resume saga_readiness_resume --direct --state <state> --format json", Mutating: true, Safety: "continues typed saga graph", Risk: string(schema.RiskLow)}},
	}
}

func staleCacheDirectRefreshScenario(t *testing.T) readiness.Scenario {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	direct, err := client.NewDirect(config.Config{Mode: config.ModeDirect, Env: "prod", Provider: fakeprovider.Name, Region: "local", StateBucket: "memory://readiness"}, client.DirectOptions{Store: store, Clock: readinessNow})
	if err != nil {
		return failedScenario("stale_cache_direct_refresh", "Direct mode rebuilds from durable state when cached views are stale", "recovery", err)
	}
	before, err := direct.Status(ctx, client.StatusOptions{TraceID: "tr_readiness_stale"})
	if err != nil {
		return failedScenario("stale_cache_direct_refresh", "Direct mode rebuilds from durable state when cached views are stale", "recovery", err)
	}
	if len(before.Services) != 0 {
		return failedScenario("stale_cache_direct_refresh", "Direct mode rebuilds from durable state when cached views are stale", "recovery", fmt.Errorf("expected empty initial snapshot, got %+v", before.Services))
	}
	if err := seedRollbackState(ctx, store, "payments-api", "prod", "rel_current", "rel_current"); err != nil {
		return failedScenario("stale_cache_direct_refresh", "Direct mode rebuilds from durable state when cached views are stale", "recovery", err)
	}
	after, err := direct.Status(ctx, client.StatusOptions{Service: "payments-api", TraceID: "tr_readiness_stale"})
	if err != nil {
		return failedScenario("stale_cache_direct_refresh", "Direct mode rebuilds from durable state when cached views are stale", "recovery", err)
	}
	if len(after.Services) != 1 || after.Freshness.Source != "direct_object_store" {
		return failedScenario("stale_cache_direct_refresh", "Direct mode rebuilds from durable state when cached views are stale", "recovery", fmt.Errorf("unexpected fresh status: %+v", after))
	}
	serviceKey, _ := paths.ServiceControl("payments-api")
	return readiness.Scenario{
		ID:          "stale_cache_direct_refresh",
		Title:       "Direct mode rebuilds from durable state when cached views are stale",
		Category:    "recovery",
		OK:          true,
		Facts:       []readiness.Fact{{Type: "freshness", Source: "direct_object_store", Message: "fresh direct status saw service added after an earlier empty snapshot"}},
		ObjectPaths: []string{serviceKey},
	}
}

func leaseContentionScenario(t *testing.T) readiness.Scenario {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	if err := seedRollbackState(ctx, store, "payments-api", "prod", "rel_current", "rel_current"); err != nil {
		return failedScenario("lease_contention", "Lease contention fences concurrent writers", "state", err)
	}
	client := state.NewClient(store, state.WithClock(readinessClock{}))
	if _, _, err := client.AcquireLease(ctx, "payments-api", state.LeaseOptions{Owner: "writer-one", Duration: time.Minute, Actor: schema.Actor{ID: "writer-one", Type: "agent"}}); err != nil {
		return failedScenario("lease_contention", "Lease contention fences concurrent writers", "state", err)
	}
	_, _, err := client.AcquireLease(ctx, "payments-api", state.LeaseOptions{Owner: "writer-two", Duration: time.Minute, Actor: schema.Actor{ID: "writer-two", Type: "agent"}})
	if !errors.Is(err, state.ErrLeaseHeld) && !strings.Contains(fmt.Sprint(err), "LEASE_HELD") {
		return failedScenario("lease_contention", "Lease contention fences concurrent writers", "state", fmt.Errorf("second writer err = %v, want LEASE_HELD", err))
	}
	serviceKey, _ := paths.ServiceControl("payments-api")
	return readiness.Scenario{
		ID:          "lease_contention",
		Title:       "Lease contention fences concurrent writers",
		Category:    "state",
		OK:          true,
		Facts:       []readiness.Fact{{Type: "lease", Source: "service_control", Message: "second writer received LEASE_HELD from the protected control document"}},
		ObjectPaths: []string{serviceKey},
	}
}

func failedRolloutDiagnosisScenario(t *testing.T) readiness.Scenario {
	t.Helper()
	status := readinessStatus("payments-api")
	status.Services[0].OperationID = "op_failed_rollout"
	status.Services[0].OperationState = string(schema.OperationFailed)
	status.Services[0].StableRelease = "rel_stable"
	status.Services[0].Health = "degraded"
	status.Services[0].Operation = &servicestatus.Operation{
		ID:    "op_failed_rollout",
		Kind:  "deploy",
		State: string(schema.OperationFailed),
		ProviderOperations: []schema.ProviderOperationRef{{
			Provider: aws.Name,
			Kind:     aws.RolloutKindASGInstanceRefresh,
			ID:       "ir-failed-rollout",
		}},
	}
	result, err := servicedoctor.Diagnose(context.Background(), status, servicedoctor.Options{Service: "payments-api", TraceID: "tr_readiness_doctor", Binary: "skiff"})
	if err != nil {
		return failedScenario("failed_rollout_diagnosis", "Failed rollout is diagnosable with safe next commands", "operations", err)
	}
	if !hasFinding(result.Findings, "ROLLOUT_FAILED_OR_DEGRADED") || !hasMutatingAction(result.RecommendedActions, "rollback") {
		return failedScenario("failed_rollout_diagnosis", "Failed rollout is diagnosable with safe next commands", "operations", fmt.Errorf("doctor result missing rollout diagnosis or rollback action: %+v", result))
	}
	return readiness.Scenario{
		ID:           "failed_rollout_diagnosis",
		Title:        "Failed rollout is diagnosable with safe next commands",
		Category:     "operations",
		OK:           true,
		Facts:        []readiness.Fact{{Type: "doctor", Source: "status", Message: "doctor emitted rollout failure finding and rollback recommendation"}},
		ProviderIDs:  []string{"ir-failed-rollout"},
		OperationIDs: []string{"op_failed_rollout"},
	}
}

func badReleaseRollbackRecommendationScenario(t *testing.T) readiness.Scenario {
	t.Helper()
	status := readinessStatus("payments-api")
	status.Services[0].StableRelease = "rel_stable"
	status.Services[0].RecentEvents = []schema.Event{serviceEvent("payments-api", "runner.failed", "runner failed WaitingForHealth after application panic on bad release")}
	result, err := servicedoctor.Diagnose(context.Background(), status, servicedoctor.Options{Service: "payments-api", TraceID: "tr_readiness_bad_release", Binary: "skiff"})
	if err != nil {
		return failedScenario("bad_release_rollback_recommendation", "Bad release evidence recommends rollback to stable", "operations", err)
	}
	if !hasFinding(result.Findings, "RUNNER_NOT_SERVING") || !hasMutatingAction(result.RecommendedActions, "rollback") {
		return failedScenario("bad_release_rollback_recommendation", "Bad release evidence recommends rollback to stable", "operations", fmt.Errorf("doctor result missing runner diagnosis or rollback action: %+v", result))
	}
	return readiness.Scenario{
		ID:       "bad_release_rollback_recommendation",
		Title:    "Bad release evidence recommends rollback to stable",
		Category: "operations",
		OK:       true,
		Facts:    []readiness.Fact{{Type: "bad_release", Source: "event", Message: "runner health failure produced rollback guidance without naming compensation as rollback"}},
	}
}

func observabilityOutageDiagnosisScenario(t *testing.T) readiness.Scenario {
	t.Helper()
	status := readinessStatus("payments-api")
	status.Services[0].Health = "nominal"
	status.Services[0].DesiredRelease = status.Services[0].StableRelease
	status.Services[0].Logs = servicestatus.DependencyStatus{Status: "unknown", Source: "logs", Summary: "CloudWatch Logs outage in fake provider"}
	status.Services[0].Metrics = servicestatus.DependencyStatus{Status: "unknown", Source: "metrics", Summary: "CloudWatch Metrics outage in fake provider"}
	result, err := servicedoctor.Diagnose(context.Background(), status, servicedoctor.Options{Service: "payments-api", TraceID: "tr_readiness_observability", Binary: "skiff"})
	if err != nil {
		return failedScenario("observability_outage_diagnosis", "Observability outage is diagnosed without mutating the service", "observability", err)
	}
	if !hasFinding(result.Findings, "LOG_DELIVERY_UNAVAILABLE") || !hasFinding(result.Findings, "METRIC_DELIVERY_UNAVAILABLE") {
		return failedScenario("observability_outage_diagnosis", "Observability outage is diagnosed without mutating the service", "observability", fmt.Errorf("doctor result missing observability findings: %+v", result))
	}
	if hasMutatingAction(result.RecommendedActions, "") {
		return failedScenario("observability_outage_diagnosis", "Observability outage is diagnosed without mutating the service", "observability", fmt.Errorf("observability diagnosis should not recommend mutation: %+v", result.RecommendedActions))
	}
	return readiness.Scenario{
		ID:       "observability_outage_diagnosis",
		Title:    "Observability outage is diagnosed without mutating the service",
		Category: "observability",
		OK:       true,
		Facts:    []readiness.Fact{{Type: "observability", Source: "doctor", Message: "log and metric backend outages produced non-mutating diagnostics while service health remained nominal"}},
	}
}

func debugDenialScenario(t *testing.T) readiness.Scenario {
	t.Helper()
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		return failedScenario("debug_denial", "Production debug is denied without approval", "security", err)
	}
	if err := seedRollbackState(context.Background(), store, "payments-api", "prod", "rel_current", "rel_current"); err != nil {
		return failedScenario("debug_denial", "Production debug is denied without approval", "security", err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", []string{"debug", "collect", "payments-api", "--direct", "--state", "file://" + dir, "--env", "prod", "--provider", fakeprovider.Name, "--region", "local", "--format", "json", "--trace-id", "tr_readiness_debug_denied"}, &stdout, &stderr)
	if code != cli.ExitPolicyDenied {
		return failedScenario("debug_denial", "Production debug is denied without approval", "security", fmt.Errorf("debug exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String()))
	}
	var got struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		return failedScenario("debug_denial", "Production debug is denied without approval", "security", err)
	}
	if got.OK || got.Code != "POLICY_DENIED" || !strings.Contains(got.Summary, "approval required") {
		return failedScenario("debug_denial", "Production debug is denied without approval", "security", fmt.Errorf("unexpected denial output: %+v", got))
	}
	return readiness.Scenario{
		ID:       "debug_denial",
		Title:    "Production debug is denied without approval",
		Category: "security",
		OK:       true,
		Facts:    []readiness.Fact{{Type: "authz", Source: "cli", Message: "debug collect returned POLICY_DENIED before collecting a bundle"}},
	}
}

func readinessStatus(service string) servicestatus.Result {
	return servicestatus.Result{
		Mode:      config.ModeDirect,
		Env:       "prod",
		Provider:  fakeprovider.Name,
		Region:    "local",
		Source:    "direct",
		Freshness: servicestatus.Freshness{Source: "direct_object_store", Ready: true, Generation: 1, RefreshedAt: readinessNow()},
		Services: []servicestatus.Service{{
			Service:        service,
			Env:            "prod",
			DesiredRelease: "rel_current",
			StableRelease:  "rel_stable",
			Health:         "nominal",
			Capacity:       servicestatus.DependencyStatus{Status: "configured", Source: "capacity", ProviderID: "fake-asg-" + service},
			TargetHealth:   servicestatus.DependencyStatus{Status: "configured", Source: "target_health", ProviderID: "fake-target-group-" + service},
			Logs:           servicestatus.DependencyStatus{Status: "configured", Source: "logs", ProviderID: "fake-log-group-" + service},
			Metrics:        servicestatus.DependencyStatus{Status: "configured", Source: "metrics", ProviderID: "fake-metrics-" + service},
		}},
	}
}

func seedInterruptedOperation(ctx context.Context, store objstore.ObjectStore) error {
	if err := seedRollbackState(ctx, store, "payments-api", "prod", "rel_new", "rel_old"); err != nil {
		return err
	}
	intent := schema.NewOperationIntent("op_readiness_resume", "payments-api", "prod", "deploy", schema.Target{Kind: "service", Name: "payments-api"}, schema.Actor{ID: "readiness", Type: "agent"}, "tr_readiness_resume", canonical.Time(readinessNow()))
	if err := createJSON(ctx, store, mustOperationIntentKey("payments-api", "op_readiness_resume"), intent); err != nil {
		return err
	}
	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   "op_readiness_resume",
		Service:       "payments-api",
		Env:           "prod",
		Status:        schema.OperationRunning,
		ProviderOperations: []schema.ProviderOperationRef{{
			Provider: aws.Name,
			Kind:     aws.RolloutKindASGInstanceRefresh,
			ID:       "ir-readiness",
		}},
		UpdatedAt: canonical.Time(readinessNow()),
		TraceID:   "tr_readiness_resume",
	}
	return createJSON(ctx, store, mustOperationControlKey("payments-api", "op_readiness_resume"), control)
}

func seedRollbackState(ctx context.Context, store objstore.ObjectStore, service, env, desired, stable string) error {
	control := schema.NewServiceControl(service, env, canonical.Time(readinessNow()), schema.Actor{ID: "seed", Type: "agent"})
	control.DesiredRelease = desired
	control.StableRelease = stable
	if desired != stable {
		control.Operation = &schema.ActiveOperation{ID: "op_seeded_failed", Kind: "deploy", State: string(schema.OperationFailed)}
	}
	if _, err := state.NewClient(store, state.WithClock(readinessClock{})).CreateServiceControl(ctx, control); err != nil {
		return err
	}
	for _, releaseID := range uniqueNonEmpty(desired, stable) {
		if err := seedReleaseManifest(ctx, store, service, env, releaseID); err != nil {
			return err
		}
	}
	return nil
}

func seedReleaseManifest(ctx context.Context, store objstore.ObjectStore, service, env, releaseID string) error {
	runtimeKey, err := paths.RuntimeManifest(service, releaseID)
	if err != nil {
		return err
	}
	manifest := schema.ReleaseManifest{
		SchemaVersion:      schema.Version,
		Service:            service,
		Env:                env,
		ReleaseID:          releaseID,
		RuntimeManifestKey: runtimeKey,
		Artifact: schema.ArtifactRef{
			Type:   "oci",
			URI:    "registry.example.com/skiff/readiness@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		CreatedAt: canonical.Time(readinessNow()),
	}
	key, err := paths.ReleaseManifest(service, releaseID)
	if err != nil {
		return err
	}
	return createJSON(ctx, store, key, manifest)
}

func createJSON(ctx context.Context, store objstore.ObjectStore, key string, value any) error {
	body, err := canonical.Marshal(value)
	if err != nil {
		return err
	}
	_, err = store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func runSkiffJSON(out any, args ...string) error {
	var stdout, stderr bytes.Buffer
	code := cli.Run("skiff", args, &stdout, &stderr)
	if code != cli.ExitSuccess {
		return fmt.Errorf("skiff %s exit=%d stderr=%s stdout=%s", strings.Join(args, " "), code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		return fmt.Errorf("skiff %s stderr=%s", strings.Join(args, " "), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("decode skiff %s output: %w\n%s", strings.Join(args, " "), err, stdout.String())
	}
	return nil
}

func serviceEvent(service, eventType, summary string) schema.Event {
	event := events.NewServiceEvent(service, eventType, summary, readinessNow(), eventType+summary)
	return schema.Event{
		SchemaVersion: schema.Version,
		ID:            event.ID,
		Time:          event.Time,
		TraceID:       "tr_readiness_event",
		Subject:       schema.Target{Kind: "service", Name: service},
		Type:          eventType,
		Severity:      "high",
		Summary:       summary,
	}
}

func hasFinding(findings []servicedoctor.Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func hasMutatingAction(actions []servicedoctor.RecommendedAction, contains string) bool {
	for _, action := range actions {
		if !action.Mutating {
			continue
		}
		if contains == "" || strings.Contains(action.ID, contains) || strings.Contains(action.Command, contains) {
			return true
		}
	}
	return false
}

func failedScenario(id, title, category string, err error) readiness.Scenario {
	return readiness.Scenario{ID: id, Title: title, Category: category, OK: false, Failure: err.Error()}
}

func uniqueNonEmpty(values ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func mustOperationIntentKey(service, operationID string) string {
	key, err := paths.OperationIntent(service, operationID)
	if err != nil {
		panic(err)
	}
	return key
}

func mustOperationControlKey(service, operationID string) string {
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		panic(err)
	}
	return key
}

func clearSkiffEnv(t *testing.T) {
	t.Helper()
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if !strings.HasPrefix(key, "SKIFF_") || key == "SKIFF_READINESS_REPORT" {
			continue
		}
		old, ok := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv(key, old)
			}
		})
	}
}

func readinessNow() time.Time {
	return time.Date(2026, 5, 17, 18, 0, 0, 0, time.UTC)
}

type readinessClock struct{}

func (readinessClock) Now() time.Time { return readinessNow() }

type readinessStep struct {
	count int
}

func (s *readinessStep) Kind() string { return "readiness.step" }

func (s *readinessStep) ValidateParams(ctx context.Context, params json.RawMessage) error {
	return ctx.Err()
}

func (s *readinessStep) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &steps.StepPlan{Summary: "readiness step", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s *readinessStep) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.count++
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "readiness step completed"}, nil
}

func (s *readinessStep) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s *readinessStep) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "readiness step compensation noop"}, nil
}

func (s *readinessStep) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, ctx.Err()
}

var _ provider.Provider = (*fakeprovider.Provider)(nil)
var _ steps.Step = (*readinessStep)(nil)
