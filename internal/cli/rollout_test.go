package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRolloutStartJSONUsesDesiredRelease(t *testing.T) {
	clearSkiffEnv(t)
	store := memory.New()
	createCLIRolloutService(t, store, "caddy-web", "rel_blue", "rel_green", "op_deploy")
	createCLIRolloutOperation(t, store, "caddy-web", "op_deploy", schema.OperationSucceeded)
	restoreStore := openRolloutObjectStore
	restoreProvider := newRolloutProvider
	openRolloutObjectStore = func(cfg config.Config) (objstore.ObjectStore, error) { return store, nil }
	newRolloutProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return fakeprovider.New(fakeprovider.WithStateStore(store)), nil
	}
	t.Cleanup(func() {
		openRolloutObjectStore = restoreStore
		newRolloutProvider = restoreProvider
	})

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"rollout", "start", "caddy-web",
		"--operation", "op_deploy",
		"--direct",
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--state", "memory://rollout",
		"--format", "json",
		"--trace-id", "tr_rollout",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var out rolloutStartOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if !out.OK || out.TraceID != "tr_rollout" || out.ReleaseID != "rel_blue" || out.Rollout.ProviderID == "" {
		t.Fatalf("unexpected rollout start output: %+v", out)
	}
	var control schema.OperationControl
	key, err := paths.OperationControl("caddy-web", "op_deploy")
	if err != nil {
		t.Fatal(err)
	}
	obj, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		t.Fatal(err)
	}
	if control.Status != schema.OperationRunning || len(control.ProviderOperations) != 1 {
		t.Fatalf("rollout start did not mark operation running with provider op: %+v", control)
	}
}

func createCLIRolloutService(t *testing.T, store objstore.ObjectStore, service, desired, stable, operationID string) {
	t.Helper()
	control := schema.NewServiceControl(service, "prod", "2026-05-18T21:00:00Z", schema.Actor{ID: "skiff-cli", Type: "user"})
	control.DesiredRelease = desired
	control.StableRelease = stable
	control.Operation = &schema.ActiveOperation{ID: operationID, Kind: "deploy", State: string(schema.OperationSucceeded)}
	key, err := paths.ServiceControl(service)
	if err != nil {
		t.Fatal(err)
	}
	createCLIJSON(t, store, key, control)
}

func createCLIRolloutOperation(t *testing.T, store objstore.ObjectStore, service, operationID string, status schema.OperationStatus) {
	t.Helper()
	control := schema.OperationControl{
		SchemaVersion: schema.Version,
		OperationID:   operationID,
		Service:       service,
		Env:           "prod",
		Status:        status,
		UpdatedAt:     "2026-05-18T21:00:00Z",
		TraceID:       "tr_rollout",
	}
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		t.Fatal(err)
	}
	createCLIJSON(t, store, key, control)
}
