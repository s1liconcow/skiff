package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	opsstate "github.com/s1liconcow/skiff/internal/ops"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
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
