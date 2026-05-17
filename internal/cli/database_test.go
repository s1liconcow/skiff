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
	"github.com/s1liconcow/skiff/internal/objstore/file"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestDatabaseRestoreJSONCreatesSagaAndStopsBeforeCutoverApproval(t *testing.T) {
	clearSkiffEnv(t)
	dir := t.TempDir()
	store, err := file.New(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	control := schema.NewServiceControl("orders-api", "staging", canonical.Time(time.Date(2026, 5, 17, 3, 40, 0, 0, time.UTC)), schema.Actor{ID: "agent-one", Type: "agent"})
	control.DesiredRelease = "rel_stable"
	control.StableRelease = "rel_stable"
	if _, err := state.NewClient(store).CreateServiceControl(context.Background(), control); err != nil {
		t.Fatalf("create service control: %v", err)
	}
	fake := &fakeDatabaseCLIProvider{}
	oldProvider := newDatabaseProvider
	newDatabaseProvider = func(config.Config, objstore.ObjectStore) (provider.Provider, error) {
		return fake, nil
	}
	t.Cleanup(func() { newDatabaseProvider = oldProvider })

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"database", "restore", "orders-db",
		"--to", "2026-05-17T02:00:00Z",
		"--secret-ref", "secret://managed-database/orders-db/connection-url",
		"--restored-database", "orders-db-restore",
		"--service", "orders-api",
		"--direct",
		"--state", "file://" + dir,
		"--env", "staging",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_restore_cli",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got databaseSagaOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("database restore output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_restore_cli" || got.Result.Status != schema.SagaRunning || got.Result.NextAction != "approve_or_reject" {
		t.Fatalf("unexpected restore output: %+v", got)
	}
	if len(got.Result.CurrentSteps) != 1 || got.Result.CurrentSteps[0] != "approve-cutover" {
		t.Fatalf("restore should wait at approval before cutover: %+v", got.Result.CurrentSteps)
	}
	if len(fake.secretUpdates) != 0 || len(fake.restarts) != 0 {
		t.Fatalf("cutover happened before approval: secret=%+v restarts=%+v", fake.secretUpdates, fake.restarts)
	}
}

func TestDatabaseRestoreDryRunJSONIncludesApprovalPlan(t *testing.T) {
	clearSkiffEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"database", "restore", "orders-db",
		"--to", "restore-point-123",
		"--secret-ref", "secret://managed-database/orders-db/connection-url",
		"--dry-run",
		"--direct",
		"--state", "memory://database-dry-run",
		"--env", "staging",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_restore_plan",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got databaseSagaOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("dry-run output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.Result.Plan == nil {
		t.Fatalf("dry-run did not include plan")
	}
	approval := false
	secretAfterApproval := false
	for _, node := range got.Result.Plan.Graph.Nodes {
		if node.ID == "approve-cutover" && node.Kind == "approval.manual" {
			approval = true
		}
		if node.ID == "update-secret-pointer" && approval {
			secretAfterApproval = true
		}
	}
	if !approval || !secretAfterApproval {
		t.Fatalf("restore plan missing approval before secret cutover: %+v", got.Result.Plan.Graph.Nodes)
	}
}

type fakeDatabaseCLIProvider struct {
	secretUpdates []provider.SecretPointerRequest
	restarts      []provider.ServiceRestartRequest
}

func (p *fakeDatabaseCLIProvider) Name() string { return "aws" }

func (p *fakeDatabaseCLIProvider) Plan(ctx context.Context, graph *ir.Graph) (*provider.Plan, error) {
	return &provider.Plan{Provider: "aws", Service: graph.Service, Env: graph.Env}, nil
}

func (p *fakeDatabaseCLIProvider) Apply(ctx context.Context, plan *provider.Plan) (*provider.ApplyResult, error) {
	return &provider.ApplyResult{Provider: "aws", Service: plan.Service, Env: plan.Env, AppliedAt: time.Now().UTC()}, nil
}

func (p *fakeDatabaseCLIProvider) InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error) {
	return &provider.ServiceInspection{Ref: ref, Provider: "aws", FreshAt: time.Now().UTC(), Resources: []provider.ResourceInspection{{Kind: "service", ProviderID: "svc-123", Status: "healthy"}}}, nil
}

func (p *fakeDatabaseCLIProvider) InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error) {
	return &provider.ResourceInspection{Kind: ref.Kind, LogicalID: ref.LogicalID, ProviderID: "resource-123", Status: "healthy"}, nil
}

func (p *fakeDatabaseCLIProvider) Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error) {
	return &provider.LogsResult{}, nil
}

func (p *fakeDatabaseCLIProvider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	return &provider.MetricsResult{}, nil
}

func (p *fakeDatabaseCLIProvider) Debug(ctx context.Context, req provider.DebugRequest) (*provider.DebugSession, error) {
	return &provider.DebugSession{ID: "debug-1", Provider: "aws", StartedAt: time.Now().UTC()}, nil
}

func (p *fakeDatabaseCLIProvider) StartRollout(ctx context.Context, req provider.RolloutRequest) (*provider.Rollout, error) {
	return &provider.Rollout{ID: req.OperationID, Provider: "aws", Service: req.Service, Env: req.Env, ProviderID: "ir-" + req.ReleaseID, StartedAt: time.Now().UTC()}, nil
}

func (p *fakeDatabaseCLIProvider) WatchRollout(ctx context.Context, req provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	return &provider.RolloutStatus{RolloutID: req.RolloutID, Status: "succeeded", ProviderID: req.ProviderID, UpdatedAt: time.Now().UTC()}, nil
}

func (p *fakeDatabaseCLIProvider) Rollback(ctx context.Context, req provider.RollbackRequest) (*provider.Rollout, error) {
	return &provider.Rollout{ID: "rollback-" + req.ReleaseID, Provider: "aws", Service: req.Service, Env: req.Env, ProviderID: "rb-" + req.ReleaseID, StartedAt: time.Now().UTC()}, nil
}

func (p *fakeDatabaseCLIProvider) SnapshotDatabase(ctx context.Context, req provider.DatabaseSnapshotRequest) (*provider.DatabaseSnapshot, error) {
	return &provider.DatabaseSnapshot{ID: "snapshot-123", Provider: "aws", ProviderID: "rds-snapshot-123", Database: req.Ref.Database, Status: "available", StartedAt: time.Now().UTC()}, nil
}

func (p *fakeDatabaseCLIProvider) VerifyRestorePoint(ctx context.Context, req provider.RestorePointRequest) (*provider.RestorePoint, error) {
	return &provider.RestorePoint{ID: "restore-point-123", Provider: "aws", ProviderID: "rds-snapshot-123", Database: req.Ref.Database, Status: "available"}, nil
}

func (p *fakeDatabaseCLIProvider) RestoreDatabase(ctx context.Context, req provider.DatabaseRestoreRequest) (*provider.DatabaseRestore, error) {
	return &provider.DatabaseRestore{ID: "restore-123", Provider: "aws", ProviderID: "rds-restore-123", Database: req.TargetDatabase, Status: "creating", StartedAt: time.Now().UTC()}, nil
}

func (p *fakeDatabaseCLIProvider) InspectDatabase(ctx context.Context, ref provider.DatabaseRef) (*provider.DatabaseInspection, error) {
	return &provider.DatabaseInspection{Ref: ref, Provider: "aws", Status: "available", Endpoint: "orders-db-restore.example", ProviderID: "rds-restore-123", FreshAt: time.Now().UTC()}, nil
}

func (p *fakeDatabaseCLIProvider) RunDatabaseSmokeQuery(ctx context.Context, req provider.DatabaseSmokeQueryRequest) (*provider.DatabaseSmokeQueryResult, error) {
	return &provider.DatabaseSmokeQueryResult{OK: true, Summary: "smoke query passed", Rows: 1}, nil
}

func (p *fakeDatabaseCLIProvider) RunShadowServiceTest(ctx context.Context, req provider.ShadowServiceTestRequest) (*provider.ShadowServiceTestResult, error) {
	return &provider.ShadowServiceTestResult{OK: true, Summary: "shadow service passed"}, nil
}

func (p *fakeDatabaseCLIProvider) UpdateSecretPointer(_ context.Context, req provider.SecretPointerRequest) (*provider.SecretPointerUpdate, error) {
	p.secretUpdates = append(p.secretUpdates, req)
	return &provider.SecretPointerUpdate{SecretRef: req.SecretRef, PreviousVersion: "v1", NewVersion: "v2", PreviousDatabase: req.Database, NewDatabase: req.TargetDatabase, UpdatedAt: time.Now().UTC()}, nil
}

func (p *fakeDatabaseCLIProvider) RestartService(_ context.Context, req provider.ServiceRestartRequest) (*provider.Rollout, error) {
	p.restarts = append(p.restarts, req)
	return &provider.Rollout{ID: "restart-123", Provider: "aws", Service: req.Service, Env: req.Env, ProviderID: "restart-provider-123", StartedAt: time.Now().UTC()}, nil
}

func (p *fakeDatabaseCLIProvider) RetireDatabase(ctx context.Context, req provider.DatabaseRetireRequest) (*provider.DatabaseRetireResult, error) {
	return &provider.DatabaseRetireResult{Database: req.Ref.Database, Provider: "aws", ProviderID: req.Ref.ProviderID, Status: "retired", RetiredAt: time.Now().UTC()}, nil
}
