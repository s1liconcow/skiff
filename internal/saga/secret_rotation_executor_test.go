package saga_test

import (
	"context"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps/builtin"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestSecretRotationCanaryFailureRestoresPreviousPointer(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	createReq, err := templates.SecretRotation(templates.SecretRotationRequest{
		SagaID:         "saga_secret_rotation",
		OperationID:    "op_secret_rotation",
		SecretRef:      "secret://managed-database/orders-db/connection-url",
		Env:            "staging",
		Consumers:      []string{"orders-api", "orders-worker"},
		CanaryConsumer: "orders-api",
		Database:       "orders-db",
		DisableAfter:   "24h",
		Actor:          schema.Actor{ID: "operator", Type: "user"},
		TraceID:        "tr_secret_rotation",
		CreatedAt:      time.Date(2026, 5, 17, 5, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("SecretRotation: %v", err)
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		t.Fatalf("create saga: %v", err)
	}
	cloud := &canaryFailingSecretProvider{Provider: fakeprovider.New(fakeprovider.WithStateStore(store), fakeprovider.WithClock(func() time.Time {
		return time.Date(2026, 5, 17, 5, 0, 0, 0, time.UTC)
	}))}
	executor := sagastate.Executor{
		Store: sagas,
		Steps: builtin.New(builtin.Options{Store: store, Provider: cloud, Metrics: cloud, Binary: "skiff"}),
		Owner: "test-secret-rotation",
	}
	result, err := executor.Execute(ctx, "saga_secret_rotation")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Status != schema.SagaFailed || result.FailedStep != "canary-consumer" {
		t.Fatalf("execution = %+v", result)
	}
	if !containsStep(result.Compensated, "update-canary-pointer") {
		t.Fatalf("canary pointer update was not compensated: %+v", result.Compensated)
	}
	if len(cloud.restores) == 0 || cloud.restores[0].PreviousVersion != "current" {
		t.Fatalf("previous secret pointer was not restored: %+v", cloud.restores)
	}
	inspect, err := sagas.Inspect(ctx, "saga_secret_rotation")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !hasStepResult(inspect.Control.StepResults, "update-canary-pointer-compensate", "succeeded") {
		t.Fatalf("missing successful canary pointer compensation: %+v", inspect.Control.StepResults)
	}
}

type canaryFailingSecretProvider struct {
	*fakeprovider.Provider
	restores []provider.SecretRestoreRequest
}

func (p *canaryFailingSecretProvider) CanaryServiceWithSecret(ctx context.Context, req provider.SecretCanaryRequest) (*provider.SecretCanaryResult, error) {
	return &provider.SecretCanaryResult{OK: false, Consumer: req.Consumer, Summary: "canary rejected secret version"}, nil
}

func (p *canaryFailingSecretProvider) RestoreSecretVersion(ctx context.Context, req provider.SecretRestoreRequest) (*provider.SecretPointer, error) {
	p.restores = append(p.restores, req)
	return p.Provider.RestoreSecretVersion(ctx, req)
}

func containsStep(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasStepResult(refs []schema.StepResultRef, stepID, status string) bool {
	for _, ref := range refs {
		if ref.StepID == stepID && ref.Status == status {
			return true
		}
	}
	return false
}
