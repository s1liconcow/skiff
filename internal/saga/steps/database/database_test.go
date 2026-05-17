package database_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	dbsteps "github.com/s1liconcow/skiff/internal/saga/steps/database"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRestoreCompensationRetiresRestoredDatabaseBeforeCutover(t *testing.T) {
	fake := &fakeDatabaseProvider{}
	step := dbsteps.RestoreToNewInstance{Provider: fake, Clock: fixedClock}
	req := stepRequest("restore-new-db", dbsteps.Params{
		Database:         "orders-db",
		Env:              "prod",
		OperationID:      "op_restore",
		RestorePoint:     "snap-123",
		RestoredDatabase: "orders-db-restore",
	})

	result, err := step.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.ProviderOperations) != 1 || result.ProviderOperations[0].ID != "restore-123" {
		t.Fatalf("provider operation not recorded: %+v", result.ProviderOperations)
	}
	_, err = step.Compensate(context.Background(), req, schema.StepResult{Result: result.Result})
	if err != nil {
		t.Fatalf("Compensate() error = %v", err)
	}
	if len(fake.retired) != 1 {
		t.Fatalf("retire calls = %d", len(fake.retired))
	}
	if fake.retired[0].Ref.Database != "orders-db-restore" || fake.retired[0].Ref.ProviderID != "rds-restore-123" {
		t.Fatalf("unexpected retire request: %+v", fake.retired[0])
	}
}

func TestSecretPointerCompensationRestoresPreviousVersion(t *testing.T) {
	fake := &fakeDatabaseProvider{}
	step := dbsteps.SecretUpdatePointer{Provider: fake}
	req := stepRequest("update-secret-pointer", dbsteps.Params{
		Database:         "orders-db",
		Env:              "prod",
		Service:          "orders-api",
		OperationID:      "op_restore",
		RestoredDatabase: "orders-db-restore",
		SecretRef:        "secret://managed-database/orders-db/connection-url",
	})
	req.PreviousResults = map[string]schema.StepResult{
		"restore-new-db": {
			Result: rawJSON(map[string]string{
				"restored_database": "orders-db-restore",
				"provider_id":       "rds-restore-123",
			}),
		},
	}

	result, err := step.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(fake.secretUpdates) != 1 || fake.secretUpdates[0].TargetDatabase != "orders-db-restore" {
		t.Fatalf("cutover update missing: %+v", fake.secretUpdates)
	}
	_, err = step.Compensate(context.Background(), req, schema.StepResult{Result: result.Result})
	if err != nil {
		t.Fatalf("Compensate() error = %v", err)
	}
	if len(fake.secretUpdates) != 2 {
		t.Fatalf("secret update calls = %d", len(fake.secretUpdates))
	}
	comp := fake.secretUpdates[1]
	if comp.TargetDatabase != "orders-db" || comp.PreviousVersion != "v1" || comp.Reason != "database restore compensation" {
		t.Fatalf("unexpected compensation update: %+v", comp)
	}
}

func stepRequest(stepID string, params dbsteps.Params) steps.StepRequest {
	return steps.StepRequest{
		SagaID:  "saga_restore",
		TraceID: "tr_restore",
		Node: schema.SagaNode{
			ID:     stepID,
			Params: rawJSON(params),
		},
	}
}

type fakeDatabaseProvider struct {
	retired       []provider.DatabaseRetireRequest
	secretUpdates []provider.SecretPointerRequest
}

func (p *fakeDatabaseProvider) SnapshotDatabase(context.Context, provider.DatabaseSnapshotRequest) (*provider.DatabaseSnapshot, error) {
	return &provider.DatabaseSnapshot{ID: "snapshot-123", Provider: "aws", ProviderID: "rds-snapshot-123", Database: "orders-db", Status: "available", StartedAt: fixedClock()}, nil
}

func (p *fakeDatabaseProvider) VerifyRestorePoint(context.Context, provider.RestorePointRequest) (*provider.RestorePoint, error) {
	return &provider.RestorePoint{ID: "snap-123", Provider: "aws", ProviderID: "rds-snapshot-123", Database: "orders-db", Status: "available"}, nil
}

func (p *fakeDatabaseProvider) RestoreDatabase(context.Context, provider.DatabaseRestoreRequest) (*provider.DatabaseRestore, error) {
	return &provider.DatabaseRestore{ID: "restore-123", Provider: "aws", ProviderID: "rds-restore-123", Database: "orders-db-restore", Status: "creating", StartedAt: fixedClock()}, nil
}

func (p *fakeDatabaseProvider) InspectDatabase(context.Context, provider.DatabaseRef) (*provider.DatabaseInspection, error) {
	return &provider.DatabaseInspection{Provider: "aws", Status: "available", ProviderID: "rds-restore-123", FreshAt: fixedClock()}, nil
}

func (p *fakeDatabaseProvider) RunDatabaseSmokeQuery(context.Context, provider.DatabaseSmokeQueryRequest) (*provider.DatabaseSmokeQueryResult, error) {
	return &provider.DatabaseSmokeQueryResult{OK: true, Summary: "ok", Rows: 1}, nil
}

func (p *fakeDatabaseProvider) RunShadowServiceTest(context.Context, provider.ShadowServiceTestRequest) (*provider.ShadowServiceTestResult, error) {
	return &provider.ShadowServiceTestResult{OK: true, Summary: "ok"}, nil
}

func (p *fakeDatabaseProvider) UpdateSecretPointer(_ context.Context, req provider.SecretPointerRequest) (*provider.SecretPointerUpdate, error) {
	p.secretUpdates = append(p.secretUpdates, req)
	if req.Reason == "database restore compensation" {
		return &provider.SecretPointerUpdate{SecretRef: req.SecretRef, NewVersion: "v3", PreviousVersion: "v2", PreviousDatabase: "orders-db-restore", NewDatabase: req.TargetDatabase, UpdatedAt: fixedClock()}, nil
	}
	return &provider.SecretPointerUpdate{SecretRef: req.SecretRef, PreviousVersion: "v1", NewVersion: "v2", PreviousDatabase: "orders-db", NewDatabase: req.TargetDatabase, UpdatedAt: fixedClock()}, nil
}

func (p *fakeDatabaseProvider) RestartService(context.Context, provider.ServiceRestartRequest) (*provider.Rollout, error) {
	return &provider.Rollout{ID: "restart-123", Provider: "aws", Service: "orders-api", Env: "prod", ProviderID: "ir-123", StartedAt: fixedClock()}, nil
}

func (p *fakeDatabaseProvider) RetireDatabase(_ context.Context, req provider.DatabaseRetireRequest) (*provider.DatabaseRetireResult, error) {
	p.retired = append(p.retired, req)
	return &provider.DatabaseRetireResult{Database: req.Ref.Database, Provider: "aws", ProviderID: req.Ref.ProviderID, Status: "retired", RetiredAt: fixedClock()}, nil
}

func rawJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}

func fixedClock() time.Time {
	return time.Date(2026, 5, 17, 3, 30, 0, 0, time.UTC)
}
