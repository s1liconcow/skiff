package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/file"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	opsstate "github.com/s1liconcow/skiff/internal/ops"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestOpsListJSONReadsOperationControls(t *testing.T) {
	clearSkiffEnv(t)
	store := memory.New()
	createCLIOperation(t, store, "payments-api", "op_cli", schema.OperationRunning)
	createCLIOperation(t, store, "payments-api", "op_done", schema.OperationSucceeded)
	restoreStore := openOpsObjectStore
	openOpsObjectStore = func(cfg config.Config) (objstore.ObjectStore, error) { return store, nil }
	t.Cleanup(func() { openOpsObjectStore = restoreStore })

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"ops", "list",
		"--direct",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--state", "memory://ops",
		"--format", "json",
		"--trace-id", "tr_ops",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var out opsListOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !out.OK || out.TraceID != "tr_ops" || len(out.Operations) != 2 {
		t.Fatalf("unexpected output: %+v", out)
	}
	if out.Operations[0].OperationID != "op_cli" || out.Operations[1].OperationID != "op_done" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestOpsListActiveFiltersTerminalOperations(t *testing.T) {
	clearSkiffEnv(t)
	store := memory.New()
	createCLIOperation(t, store, "payments-api", "op_running", schema.OperationRunning)
	createCLIOperation(t, store, "payments-api", "op_failed", schema.OperationFailed)
	restoreStore := openOpsObjectStore
	openOpsObjectStore = func(cfg config.Config) (objstore.ObjectStore, error) { return store, nil }
	t.Cleanup(func() { openOpsObjectStore = restoreStore })

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"ops", "list",
		"--active",
		"--direct",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--state", "memory://ops",
		"--format", "json",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var out opsListOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if len(out.Operations) != 1 || out.Operations[0].OperationID != "op_running" {
		t.Fatalf("unexpected active operations: %+v", out.Operations)
	}
}

func TestOpsResumeJSONContinuesRollout(t *testing.T) {
	clearSkiffEnv(t)
	store := memory.New()
	createCLIOperation(t, store, "payments-api", "op_cli", schema.OperationRunning)
	restoreStore := openOpsObjectStore
	restoreProvider := newOpsProvider
	openOpsObjectStore = func(cfg config.Config) (objstore.ObjectStore, error) { return store, nil }
	newOpsProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return &cliOpsProvider{status: &provider.RolloutStatus{Status: "rolling_out", ProviderID: "ir-cli", UpdatedAt: cliOpsNow()}}, nil
	}
	t.Cleanup(func() {
		openOpsObjectStore = restoreStore
		newOpsProvider = restoreProvider
	})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"ops", "resume", "op_cli",
		"--service", "payments-api",
		"--direct",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--state", "memory://ops",
		"--format", "json",
		"--trace-id", "tr_ops",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var out opsResumeOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !out.OK || !out.Result.Resumed || out.Result.RolloutStatus == nil || out.Result.RolloutStatus.Status != "rolling_out" {
		t.Fatalf("unexpected output: %+v", out)
	}
	control, err := opsstate.NewStore(store).Inspect(context.Background(), "payments-api", "op_cli")
	if err != nil {
		t.Fatal(err)
	}
	if control.Control.Lease != nil {
		t.Fatalf("operation lease was not released: %+v", control.Control.Lease)
	}
}

func TestOpsInspectAPIModeUsesSkiffd(t *testing.T) {
	clearSkiffEnv(t)
	restoreTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/ops/inspect" {
			t.Fatalf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("service") != "payments-api" || r.URL.Query().Get("operation") != "op_api" {
			t.Fatalf("unexpected API query: %s", r.URL.RawQuery)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"operation_id":"op_api","service":"payments-api","status":"running","trace_id":"tr_ops_api_inspect","control":{"schema_version":"skiff.state/v1","operation_id":"op_api","service":"payments-api","status":"running"}}}`)),
			Request:    r,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = restoreTransport })

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"ops", "inspect", "op_api",
		"--service", "payments-api",
		"--api",
		"--api-url", "http://skiffd.test",
		"--format", "json",
		"--trace-id", "tr_ops_api_inspect",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var out opsInspectOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !out.OK || out.Result.OperationID != "op_api" || out.Result.Service != "payments-api" {
		t.Fatalf("unexpected API inspect output: %+v", out)
	}
}

func TestOpsCatalogPlanAndRunPackageProfileJSON(t *testing.T) {
	clearSkiffEnv(t)
	dir := writePkgCLIFixture(t, "postgres-ha")
	root := t.TempDir()
	lockfile := root + "/skiff.lock.json"
	cache := root + "/cache"
	_ = runPkgJSON(t, []string{"pkg", "add", "file://" + dir, "--lockfile", lockfile, "--cache", cache, "--format", "json"})

	list := runOpsJSON(t, []string{"ops", "list", "postgres-ha", "--lockfile", lockfile, "--cache", cache, "--format", "json", "--trace-id", "tr_ops_list"})
	var catalog opsCatalogOutput
	if err := json.Unmarshal(list, &catalog); err != nil {
		t.Fatalf("decode catalog: %v\n%s", err, string(list))
	}
	if !catalog.OK || catalog.TraceID != "tr_ops_list" || catalog.Target != "postgres-ha" {
		t.Fatalf("unexpected catalog output: %+v", catalog)
	}
	foundPackageOperation := false
	for _, operation := range catalog.Operations {
		if operation.Name == "primary-switchover-update" && operation.Package != nil && operation.Package.Digest != "" {
			foundPackageOperation = true
		}
	}
	if !foundPackageOperation {
		t.Fatalf("catalog did not include package-provided primary switchover: %+v", catalog.Operations)
	}

	plan := runOpsJSON(t, []string{
		"ops", "plan", "postgres-ha", "primary-switchover-update",
		"--lockfile", lockfile,
		"--cache", cache,
		"--operation-id", "op_pkg_plan",
		"--saga-id", "saga_pkg_plan",
		"--param", "release_id=rel_1",
		"--param", "candidate=postgres-ha-1",
		"--format", "json",
		"--trace-id", "tr_ops_plan",
	})
	var planned opsPlanOutput
	if err := json.Unmarshal(plan, &planned); err != nil {
		t.Fatalf("decode plan: %v\n%s", err, string(plan))
	}
	if !planned.OK || planned.WouldWrite || planned.OperationID != "op_pkg_plan" || planned.SagaID != "saga_pkg_plan" || planned.Package == nil {
		t.Fatalf("unexpected plan output: %+v", planned)
	}
	if planned.Profile.Risk != "high" || len(planned.Profile.Params) == 0 {
		t.Fatalf("unexpected profile explanation: %+v", planned.Profile)
	}
	apiPlan := runOpsJSON(t, []string{
		"ops", "plan", "postgres-ha", "primary-switchover-update",
		"--api",
		"--api-url", "http://127.0.0.1:65535",
		"--lockfile", lockfile,
		"--cache", cache,
		"--operation-id", "op_pkg_plan_api",
		"--saga-id", "saga_pkg_plan_api",
		"--param", "release_id=rel_1",
		"--param", "candidate=postgres-ha-1",
		"--format", "json",
		"--trace-id", "tr_ops_plan_api",
	})
	var apiPlanned opsPlanOutput
	if err := json.Unmarshal(apiPlan, &apiPlanned); err != nil {
		t.Fatalf("decode api plan: %v\n%s", err, string(apiPlan))
	}
	if !apiPlanned.OK || apiPlanned.WouldWrite || apiPlanned.Profile.Name != planned.Profile.Name {
		t.Fatalf("unexpected api plan output: %+v", apiPlanned)
	}

	planOnly := runOpsJSON(t, []string{
		"ops", "run", "postgres-ha", "primary-switchover-update",
		"--plan-only",
		"--lockfile", lockfile,
		"--cache", cache,
		"--operation-id", "op_pkg_plan_only",
		"--saga-id", "saga_pkg_plan_only",
		"--param", "release_id=rel_1",
		"--param", "candidate=postgres-ha-1",
		"--format", "json",
		"--trace-id", "tr_ops_plan_only",
	})
	var planOnlyOut opsPlanOutput
	if err := json.Unmarshal(planOnly, &planOnlyOut); err != nil {
		t.Fatalf("decode plan-only: %v\n%s", err, string(planOnly))
	}
	if !planOnlyOut.OK || !planOnlyOut.PlanOnly || planOnlyOut.WouldWrite {
		t.Fatalf("unexpected plan-only output: %+v", planOnlyOut)
	}

	store := memory.New()
	restoreStore := openOpsObjectStore
	openOpsObjectStore = func(cfg config.Config) (objstore.ObjectStore, error) { return store, nil }
	t.Cleanup(func() { openOpsObjectStore = restoreStore })
	run := runOpsJSON(t, []string{
		"ops", "run", "postgres-ha", "primary-switchover-update",
		"--direct",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--state", "memory://ops",
		"--lockfile", lockfile,
		"--cache", cache,
		"--operation-id", "op_pkg_run",
		"--saga-id", "saga_pkg_run",
		"--param", "release_id=rel_1",
		"--param", "candidate=postgres-ha-1",
		"--format", "json",
		"--trace-id", "tr_ops_run",
		"--yes",
	})
	var runOut opsPlanOutput
	if err := json.Unmarshal(run, &runOut); err != nil {
		t.Fatalf("decode run: %v\n%s", err, string(run))
	}
	if !runOut.OK || !runOut.WouldWrite || runOut.OperationID != "op_pkg_run" || runOut.SagaID != "saga_pkg_run" {
		t.Fatalf("unexpected run output: %+v", runOut)
	}
	inspect, err := opsstate.NewStore(store).Inspect(context.Background(), "postgres-ha", "op_pkg_run")
	if err != nil {
		t.Fatalf("inspect created operation: %v", err)
	}
	if inspect.Status != schema.OperationPending || inspect.Kind != "primary-switchover-update" || inspect.Risk != schema.RiskHigh {
		t.Fatalf("unexpected created operation: %+v", inspect)
	}
	if _, err := store.Get(context.Background(), "sagas/saga_pkg_run/intent.json"); err != nil {
		t.Fatalf("saga intent not written: %v", err)
	}

	restoreTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/ops/profile-run" {
			t.Fatalf("unexpected API request %s %s", r.Method, r.URL.Path)
		}
		var req opsstate.ProfileOperationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode API request: %v", err)
		}
		if req.OperationID != "op_pkg_api" || req.Render.SagaID != "saga_pkg_api" || req.Render.Package.Digest == "" {
			t.Fatalf("unexpected API request body: %+v", req)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"operation_id":"op_pkg_api","saga_id":"saga_pkg_api","trace_id":"tr_ops_api","paths":{"operation_intent":"services/postgres-ha/operations/op_pkg_api/intent.json"}}}`)),
			Request:    r,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = restoreTransport })
	apiRun := runOpsJSON(t, []string{
		"ops", "run", "postgres-ha", "primary-switchover-update",
		"--api",
		"--api-url", "http://skiffd.test",
		"--env", "prod",
		"--lockfile", lockfile,
		"--cache", cache,
		"--operation-id", "op_pkg_api",
		"--saga-id", "saga_pkg_api",
		"--param", "release_id=rel_1",
		"--param", "candidate=postgres-ha-1",
		"--format", "json",
		"--trace-id", "tr_ops_api",
		"--yes",
	})
	var apiRunOut opsPlanOutput
	if err := json.Unmarshal(apiRun, &apiRunOut); err != nil {
		t.Fatalf("decode api run: %v\n%s", err, string(apiRun))
	}
	if !apiRunOut.OK || !apiRunOut.WouldWrite || apiRunOut.Paths["operation_intent"] == "" {
		t.Fatalf("unexpected api run output: %+v", apiRunOut)
	}
}

func TestOpsRunStatefulUpdateReleaseCompatibilityCreatesInspectableOperation(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	seedStatefulSagaCLIControls(t, store, "vol-0")

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"ops", "run", "orders-stream", "update-release",
		"--release-id", "rel_ops_update",
		"--members", "0",
		"--operation-id", "op_ops_update",
		"--saga-id", "saga_ops_update",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "fake",
		"--region", "local",
		"--format", "json",
		"--trace-id", "tr_ops_update",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var out statefulOrderedSagaOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !out.OK || out.TraceID != "tr_ops_update" || out.Result.OperationID != "op_ops_update" || out.Result.SagaID != "saga_ops_update" || out.Result.Status != schema.SagaSucceeded {
		t.Fatalf("unexpected output: %+v", out)
	}
	operation, err := opsstate.NewStore(store).Inspect(context.Background(), "orders-stream", "op_ops_update")
	if err != nil {
		t.Fatalf("inspect operation: %v", err)
	}
	if operation.Kind != "update-release" || operation.Status != schema.OperationSucceeded || len(operation.StepResults) == 0 {
		t.Fatalf("unexpected operation: %+v", operation)
	}
	member, err := state.NewClient(store).GetStatefulMemberControl(context.Background(), "orders-stream", 0)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.Control.ReleaseID != "rel_ops_update" || member.Control.Generation != 1 {
		t.Fatalf("member was not updated in place: %+v", member.Control)
	}
}

func TestOpsRunStatefulReplaceMemberCompatibilityCreatesInspectableOperation(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	seedStatefulSagaCLIControls(t, store, "vol-0")

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"ops", "run", "orders-stream", "replace-member",
		"--member", "0",
		"--operation-id", "op_ops_replace",
		"--saga-id", "saga_ops_replace",
		"--yes",
		"--direct",
		"--state", "file://" + dir,
		"--env", "prod",
		"--provider", "fake",
		"--region", "local",
		"--format", "json",
		"--trace-id", "tr_ops_replace",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var out statefulReplacementSagaOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !out.OK || out.TraceID != "tr_ops_replace" || out.Result.OperationID != "op_ops_replace" || out.Result.SagaID != "saga_ops_replace" || out.Result.Status != schema.SagaSucceeded {
		t.Fatalf("unexpected output: %+v", out)
	}
	operation, err := opsstate.NewStore(store).Inspect(context.Background(), "orders-stream", "op_ops_replace")
	if err != nil {
		t.Fatalf("inspect operation: %v", err)
	}
	if operation.Kind != "replace-member" || operation.Status != schema.OperationSucceeded || len(operation.StepResults) == 0 || len(operation.ProviderOperations) == 0 {
		t.Fatalf("unexpected operation: %+v", operation)
	}
}

func createCLIOperation(t *testing.T, store objstore.ObjectStore, service, operationID string, status schema.OperationStatus) {
	t.Helper()
	intent := schema.NewOperationIntent(operationID, service, "prod", "rollback", schema.Target{Kind: "service", Name: service}, schema.Actor{ID: "agent-one", Type: "agent"}, "tr_ops", canonical.Time(cliOpsNow()))
	createCLIJSON(t, store, mustCLIOperationIntentKey(t, service, operationID), intent)
	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   operationID,
		Service:       service,
		Env:           "prod",
		Status:        status,
		ProviderOperations: []schema.ProviderOperationRef{{
			Provider: aws.Name,
			Kind:     aws.RolloutKindASGInstanceRefresh,
			ID:       "ir-cli",
		}},
		UpdatedAt: canonical.Time(cliOpsNow()),
		TraceID:   "tr_ops",
	}
	createCLIJSON(t, store, mustCLIOperationControlKey(t, service, operationID), control)
}

func runOpsJSON(t *testing.T, args []string) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run("skiff", args, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("%v exit=%d stderr=%s stdout=%s", args, code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("%v stderr=%q, want empty", args, stderr.String())
	}
	return stdout.Bytes()
}

func createCLIJSON(t *testing.T, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}

func mustCLIOperationIntentKey(t *testing.T, service, operationID string) string {
	t.Helper()
	key, err := paths.OperationIntent(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustCLIOperationControlKey(t *testing.T, service, operationID string) string {
	t.Helper()
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type cliOpsProvider struct {
	status *provider.RolloutStatus
}

func (p *cliOpsProvider) Name() string { return "fake" }

func (p *cliOpsProvider) Plan(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	return &provider.Plan{}, nil
}

func (p *cliOpsProvider) Apply(ctx context.Context, plan *provider.Plan) (*provider.ApplyResult, error) {
	return &provider.ApplyResult{}, nil
}

func (p *cliOpsProvider) InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error) {
	return nil, provider.Unsupported("fake", "inspect-service")
}

func (p *cliOpsProvider) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	return nil, provider.Unsupported("fake", "inspect-resource")
}

func (p *cliOpsProvider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	return nil, provider.Unsupported("fake", "logs")
}

func (p *cliOpsProvider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	return nil, provider.Unsupported("fake", "metrics")
}

func (p *cliOpsProvider) Debug(ctx context.Context, req provider.DebugRequest) (*provider.DebugSession, error) {
	return nil, provider.Unsupported("fake", "debug")
}

func (p *cliOpsProvider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	return nil, provider.Unsupported("fake", "start-rollout")
}

func (p *cliOpsProvider) WatchRollout(ctx context.Context, req provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	return p.status, nil
}

func (p *cliOpsProvider) Rollback(ctx context.Context, req provider.RollbackRequest) (*provider.Rollout, error) {
	return nil, provider.Unsupported("fake", "rollback")
}

func cliOpsNow() time.Time {
	return time.Date(2026, 5, 17, 2, 15, 0, 0, time.UTC)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
