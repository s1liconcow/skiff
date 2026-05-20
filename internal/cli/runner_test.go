package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/runner"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRunnerBootstrapCommandUsesUserDataAndObjectState(t *testing.T) {
	userData := writeRunnerUserData(t)
	store := memory.New()

	restoreOpen := openRunnerObjectStore
	restoreBootstrap := runRunnerBootstrapFn
	restoreMetadata := newRunnerMetadataProvider
	t.Cleanup(func() {
		openRunnerObjectStore = restoreOpen
		runRunnerBootstrapFn = restoreBootstrap
		newRunnerMetadataProvider = restoreMetadata
	})

	openRunnerObjectStore = func(cfg config.Config) (objstore.ObjectStore, error) {
		if cfg.Service != "payments-api" || cfg.StateBucket != "memory://runner" {
			t.Fatalf("unexpected runner config: %+v", cfg)
		}
		return store, nil
	}
	newRunnerMetadataProvider = func(cfg config.Config) runner.MetadataProvider {
		return runner.StaticMetadataProvider{Value: runner.Identity{Provider: cfg.Provider, Region: cfg.Region, InstanceID: "i-test"}}
	}
	runRunnerBootstrapFn = func(ctx context.Context, req runner.BootstrapRequest) (*runner.BootstrapResult, error) {
		if req.Store != store {
			t.Fatal("bootstrap did not receive opened object store")
		}
		if req.TraceID != "tr_runner" {
			t.Fatalf("trace ID = %q", req.TraceID)
		}
		return &runner.BootstrapResult{
			Service:    req.Config.Service,
			Env:        req.Config.Env,
			ReleaseID:  "rel_01JABC",
			ControlKey: req.Config.ControlKey,
		}, nil
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff-runner", []string{
		"bootstrap",
		"--user-data", userData,
		"--state-path", filepath.Join(t.TempDir(), "state.json"),
		"--events-path", filepath.Join(t.TempDir(), "events.jsonl"),
		"--format", "json",
		"--trace-id", "tr_runner",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var got runnerBootstrapOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("runner bootstrap output is not JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_runner" || got.Result.ReleaseID != "rel_01JABC" {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func TestRunnerRunCommandLoadsManifestsAndStartsLifecycle(t *testing.T) {
	userData := writeRunnerUserData(t)
	store := memory.New()
	releaseKey, err := paths.ReleaseManifest("payments-api", "rel_01JABC")
	if err != nil {
		t.Fatal(err)
	}
	runtimeKey, err := paths.RuntimeManifest("payments-api", "rel_01JABC")
	if err != nil {
		t.Fatal(err)
	}
	createRunnerTestObject(t, store, releaseKey, schema.ReleaseManifest{
		SchemaVersion:      schema.Version,
		Service:            "payments-api",
		Env:                "prod",
		ReleaseID:          "rel_01JABC",
		Artifact:           schema.ArtifactRef{Type: "binary", URI: "file:///tmp/payments-api", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		RuntimeManifestKey: runtimeKey,
		CreatedAt:          "2026-05-20T20:00:00Z",
	})
	createRunnerTestObject(t, store, runtimeKey, schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       "payments-api",
		Env:           "prod",
		ReleaseID:     "rel_01JABC",
		Command:       []string{"/opt/skiff/workloads/payments-api/current/payments-api"},
		CreatedAt:     "2026-05-20T20:00:00Z",
	})
	statePath := filepath.Join(t.TempDir(), "state.json")
	stateStore := runner.FileStateStore{Path: statePath}
	if err := stateStore.SaveState(context.Background(), runner.LocalState{
		SchemaVersion:       runner.RunnerStateSchemaVersion,
		Service:             "payments-api",
		Env:                 "prod",
		CurrentState:        runner.StatePreparingArtifact,
		LastAcceptedRelease: "rel_01JABC",
		ControlKey:          "services/payments-api/control.json",
		ReleaseKey:          releaseKey,
		RuntimeManifestKey:  runtimeKey,
		UpdatedAt:           "2026-05-20T20:00:00Z",
		Identity:            &runner.Identity{Provider: "aws", Region: "us-west-2", InstanceID: "i-test"},
	}); err != nil {
		t.Fatal(err)
	}

	restoreOpen := openRunnerObjectStore
	restoreLifecycle := runRunnerLifecycleFn
	t.Cleanup(func() {
		openRunnerObjectStore = restoreOpen
		runRunnerLifecycleFn = restoreLifecycle
	})
	openRunnerObjectStore = func(cfg config.Config) (objstore.ObjectStore, error) { return store, nil }
	runRunnerLifecycleFn = func(ctx context.Context, req runner.LifecycleRequest) (*runner.LifecycleResult, error) {
		if req.RuntimeManifest.ReleaseID != "rel_01JABC" || req.Artifact.Type != "binary" {
			t.Fatalf("unexpected lifecycle request: %+v artifact=%+v", req.RuntimeManifest, req.Artifact)
		}
		if req.Identity == nil || req.Identity.InstanceID != "i-test" {
			t.Fatalf("missing identity: %+v", req.Identity)
		}
		return &runner.LifecycleResult{
			UnitName: "skiff-payments-api-prod.service",
			Status: runner.RunnerStatus{
				SchemaVersion: runner.RunnerStatusSchemaVersion,
				Service:       "payments-api",
				Env:           "prod",
				ReleaseID:     "rel_01JABC",
				State:         runner.StateServing,
			},
		}, nil
	}

	var stdout, stderr bytes.Buffer
	code := Run("skiff-runner", []string{
		"run",
		"--user-data", userData,
		"--state-path", statePath,
		"--events-path", filepath.Join(t.TempDir(), "events.jsonl"),
		"--format", "json",
		"--trace-id", "tr_runner",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	var got runnerRunOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("runner run output is not JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.Result.Status.State != runner.StateServing {
		t.Fatalf("unexpected output: %+v", got)
	}
}

func writeRunnerUserData(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner.json")
	body := []byte(`{"skiff":{"env":"prod","service":"payments-api","provider":"aws","region":"us-west-2","state_bucket":"memory://runner","control_key":"services/payments-api/control.json","release_id":"rel_01JABC"}}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func createRunnerTestObject(t *testing.T, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}
