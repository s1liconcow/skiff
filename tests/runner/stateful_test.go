package runner_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/runner"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/internal/stateful"
)

func TestParseRunnerUserDataStatefulConfig(t *testing.T) {
	cfg, err := runner.ParseUserData([]byte(`{
		"skiff": {
			"env": "prod",
			"service": "payments-api",
			"provider": "aws",
			"region": "us-west-2",
			"state_bucket": "s3://skiff-state-prod",
			"control_key": "stateful/payments-api/members/0/control.json",
			"release_id": "rel_01JABC",
			"release_manifest_key": "services/payments-api/releases/rel_01JABC/release.json",
			"runtime_manifest_key": "services/payments-api/releases/rel_01JABC/runtime-manifest.json",
			"stateful": {
				"group": "payments-api",
				"member": 0,
				"generation": 2,
				"volume_mount_path": "/var/lib/payments-api/state",
				"stable_hostname": "payments-api-0.internal.example.com",
				"recipe": "postgres"
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != config.ModeRunner || cfg.StatefulGroup != "payments-api" || cfg.StatefulMember != 0 || cfg.StatefulGeneration != 2 {
		t.Fatalf("unexpected stateful runner config: %+v", cfg)
	}
	if cfg.StatefulVolumeMountPath != "/var/lib/payments-api/state" || cfg.StatefulStableHostname != "payments-api-0.internal.example.com" || cfg.StatefulRecipe != "postgres" {
		t.Fatalf("stateful runner fields were not parsed: %+v", cfg)
	}
	if cfg.ReleaseManifestKey == "" || cfg.RuntimeManifestKey == "" {
		t.Fatalf("manifest refs were not parsed: %+v", cfg)
	}
}

func TestStatefulBootstrapFetchesReleaseAndPersistsMemberRuntime(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	signer := testSigner(t, "local-test", "A")
	verifier := verifierFor(t, signer)
	cfg := statefulRunnerConfig(t)
	putStatefulMemberControl(t, ctx, store, statefulControlFixture())
	putSignedRelease(t, ctx, store, signer, releaseFixture{
		service:   cfg.Service,
		env:       cfg.Env,
		releaseID: cfg.ReleaseID,
		createdAt: "2026-05-17T00:00:00Z",
		expiresAt: "2026-06-17T00:00:00Z",
	})

	stateStore := &memoryStateStore{}
	events := &collectingSink{}
	result, err := runner.Bootstrap(ctx, runner.BootstrapRequest{
		Config:   cfg,
		Store:    store,
		Verifier: verifier,
		MetadataProvider: runner.StaticMetadataProvider{Value: runner.Identity{
			Provider:   "aws",
			Region:     "us-west-2",
			InstanceID: "i-abc123",
			Zone:       "us-west-2a",
		}},
		StateStore: stateStore,
		EventSink:  events,
		TraceID:    "tr_stateful_boot",
		Now:        fixedNow,
	})
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if result.Stateful == nil || result.Stateful.Group != "payments-api" || result.Stateful.Member != 0 || result.Stateful.VolumeID != "vol-123" {
		t.Fatalf("unexpected stateful bootstrap result: %+v", result.Stateful)
	}
	if stateStore.state.Stateful == nil || stateStore.state.Stateful.ReleaseManifest == "" || stateStore.state.Stateful.RuntimeManifest == "" {
		t.Fatalf("stateful runtime was not persisted: %+v", stateStore.state)
	}
	wantStates := []runner.State{runner.StateBooting, runner.StateFetchingManifest, runner.StateVerifyingRelease, runner.StatePreparingArtifact}
	if got := eventStates(events.events); !reflect.DeepEqual(got, wantStates) {
		t.Fatalf("event states = %v, want %v", got, wantStates)
	}
	if events.events[len(events.events)-1].Stateful == nil {
		t.Fatalf("stateful runtime was not included in final event: %+v", events.events[len(events.events)-1])
	}
}

func TestStatefulBootstrapRejectsUnsafeMemberControls(t *testing.T) {
	ctx := context.Background()
	signer := testSigner(t, "local-test", "A")
	verifier := verifierFor(t, signer)

	tests := []struct {
		name   string
		mutate func(*schema.StatefulMemberControl)
	}{
		{name: "generation mismatch", mutate: func(control *schema.StatefulMemberControl) { control.Generation = 3 }},
		{name: "active lease", mutate: func(control *schema.StatefulMemberControl) {
			control.Lease = &schema.Lease{Owner: "replace-op", Token: "lease-token", Generation: 2, ExpiresAt: "2026-05-18T01:00:00Z"}
		}},
		{name: "missing volume", mutate: func(control *schema.StatefulMemberControl) { control.VolumeID = "" }},
		{name: "identity mismatch", mutate: func(control *schema.StatefulMemberControl) { control.InstanceID = "i-other" }},
		{name: "not ready", mutate: func(control *schema.StatefulMemberControl) { control.Phase = state.StatefulMemberReplacing }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := memory.New()
			cfg := statefulRunnerConfig(t)
			control := statefulControlFixture()
			tt.mutate(&control)
			putStatefulMemberControl(t, ctx, store, control)
			putSignedRelease(t, ctx, store, signer, releaseFixture{
				service:   cfg.Service,
				env:       cfg.Env,
				releaseID: cfg.ReleaseID,
				createdAt: "2026-05-17T00:00:00Z",
				expiresAt: "2026-06-17T00:00:00Z",
			})

			_, err := runner.Bootstrap(ctx, runner.BootstrapRequest{
				Config:   cfg,
				Store:    store,
				Verifier: verifier,
				MetadataProvider: runner.StaticMetadataProvider{Value: runner.Identity{
					Provider:   "aws",
					Region:     "us-west-2",
					InstanceID: "i-abc123",
					Zone:       "us-west-2a",
				}},
				StateStore: &memoryStateStore{},
				Now:        fixedNow,
			})
			assertBootstrapCode(t, err, runner.CodeTargetMismatch)
		})
	}
}

func TestRunLifecyclePreparesStatefulVolumeAndRunsRecipeHooks(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	putStatefulMemberControl(t, ctx, store, statefulControlFixture())
	stateStore := &memoryStateStore{state: statefulLocalStateFixture(t)}
	systemd := &fakeSystemd{}
	volume := &recordingStatefulVolume{}
	recipe := &recordingStatefulRecipe{name: "postgres"}
	events := &collectingSink{}

	result, err := runner.RunLifecycle(ctx, runner.LifecycleRequest{
		RuntimeManifest: runtimeManifestFixture(),
		ControlStore:    store,
		StateStore:      stateStore,
		EventSink:       events,
		Systemd:         systemd,
		StatefulVolume:  volume,
		StatefulRecipe:  recipe,
		HealthChecker:   &scriptedHealthChecker{results: []runner.HealthResult{{Status: runner.HealthHealthy, Summary: "ready"}}},
		TraceID:         "tr_stateful_run",
		OperationID:     "op_stateful_run",
		Identity:        &runner.Identity{InstanceID: "i-abc123", Zone: "us-west-2a"},
		HealthAttempts:  1,
		Now:             fixedNow,
	})
	if err != nil {
		t.Fatalf("RunLifecycle returned error: %v", err)
	}
	if result.Status.State != runner.StateServing || result.Status.Stateful == nil || result.Status.Stateful.Generation != 2 {
		t.Fatalf("unexpected status: %+v", result.Status)
	}
	if len(volume.requests) != 1 || volume.requests[0].Runtime.VolumeMountPath != "/mnt/stateful" {
		t.Fatalf("volume was not prepared correctly: %+v", volume.requests)
	}
	if !reflect.DeepEqual(recipe.calls, []string{"start", "health"}) {
		t.Fatalf("recipe calls = %v, want start and health", recipe.calls)
	}
	if recipe.lastReq.OperationID != "op_stateful_run" || recipe.lastReq.TraceID != "tr_stateful_run" || recipe.lastReq.VolumeID != "vol-123" {
		t.Fatalf("recipe request lost runtime context: %+v", recipe.lastReq)
	}
	if !strings.Contains(systemd.unitBody, `SKIFF_STATEFUL_MEMBER=0`) || !strings.Contains(systemd.unitBody, `SKIFF_STATEFUL_VOLUME_MOUNT_PATH=/mnt/stateful`) {
		t.Fatalf("rendered unit missing stateful env vars:\n%s", systemd.unitBody)
	}
	if events.events[len(events.events)-1].Stateful == nil {
		t.Fatalf("stateful runtime was not included in serving event: %+v", events.events[len(events.events)-1])
	}
}

func TestRunLifecycleRejectsStaleStatefulGenerationBeforeServing(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	control := statefulControlFixture()
	control.Generation = 3
	putStatefulMemberControl(t, ctx, store, control)
	volume := &recordingStatefulVolume{}

	_, err := runner.RunLifecycle(ctx, runner.LifecycleRequest{
		RuntimeManifest: runtimeManifestFixture(),
		ControlStore:    store,
		StateStore:      &memoryStateStore{state: statefulLocalStateFixture(t)},
		Systemd:         &fakeSystemd{},
		StatefulVolume:  volume,
		HealthChecker:   &scriptedHealthChecker{results: []runner.HealthResult{{Status: runner.HealthHealthy}}},
		Identity:        &runner.Identity{InstanceID: "i-abc123", Zone: "us-west-2a"},
		HealthAttempts:  1,
		Now:             fixedNow,
	})
	var lifecycleErr *runner.LifecycleError
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != runner.CodeStatefulFenceFailed {
		t.Fatalf("RunLifecycle error = %v, want %s", err, runner.CodeStatefulFenceFailed)
	}
	if len(volume.requests) != 0 {
		t.Fatalf("volume should not be prepared after stale generation: %+v", volume.requests)
	}
}

func TestRunLifecycleFailsRecipeHook(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	putStatefulMemberControl(t, ctx, store, statefulControlFixture())
	_, err := runner.RunLifecycle(ctx, runner.LifecycleRequest{
		RuntimeManifest: runtimeManifestFixture(),
		ControlStore:    store,
		StateStore:      &memoryStateStore{state: statefulLocalStateFixture(t)},
		Systemd:         &fakeSystemd{},
		StatefulVolume:  &recordingStatefulVolume{},
		StatefulRecipe:  &recordingStatefulRecipe{name: "postgres", failAction: "start"},
		HealthChecker:   &scriptedHealthChecker{results: []runner.HealthResult{{Status: runner.HealthHealthy}}},
		Identity:        &runner.Identity{InstanceID: "i-abc123", Zone: "us-west-2a"},
		HealthAttempts:  1,
		Now:             fixedNow,
	})
	var lifecycleErr *runner.LifecycleError
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != runner.CodeStatefulRecipeFailed {
		t.Fatalf("RunLifecycle error = %v, want %s", err, runner.CodeStatefulRecipeFailed)
	}
}

func TestStopWorkloadRunsStatefulStopHookFromLocalState(t *testing.T) {
	stateStore := &memoryStateStore{state: statefulLocalStateFixture(t)}
	stateStore.state.CurrentState = runner.StateServing
	stateStore.state.Health = runner.HealthHealthy
	recipe := &recordingStatefulRecipe{name: "postgres"}
	systemd := &fakeSystemd{}

	err := runner.StopWorkload(context.Background(), runner.StopRequest{
		Service:        "payments-api",
		Env:            "prod",
		ReleaseID:      "rel_01JABC",
		StateStore:     stateStore,
		Systemd:        systemd,
		StatefulRecipe: recipe,
		TraceID:        "tr_stop",
		OperationID:    "op_stop",
		Now:            fixedNow,
	})
	if err != nil {
		t.Fatalf("StopWorkload returned error: %v", err)
	}
	if !reflect.DeepEqual(recipe.calls, []string{"stop"}) || recipe.lastReq.OperationID != "op_stop" {
		t.Fatalf("stateful stop hook was not called correctly: calls=%v req=%+v", recipe.calls, recipe.lastReq)
	}
	if stateStore.state.CurrentState != runner.StateStopped || systemd.stopped != "skiff-payments-api-prod.service" {
		t.Fatalf("unexpected stop result: state=%+v systemd=%+v", stateStore.state, systemd)
	}
}

func TestStatefulRecipeHooksExposeBackupRestoreAndRoleDetection(t *testing.T) {
	recipe := &recordingStatefulRecipe{name: "postgres"}
	runtime := statefulRuntimeFixture(t)
	if _, err := runner.RunStatefulRecipeHook(context.Background(), recipe, runtime, runner.StatefulRecipeBackup, "op_backup", "tr_hooks"); err != nil {
		t.Fatalf("backup hook returned error: %v", err)
	}
	if _, err := runner.RunStatefulRecipeHook(context.Background(), recipe, runtime, runner.StatefulRecipeRestore, "op_restore", "tr_hooks"); err != nil {
		t.Fatalf("restore hook returned error: %v", err)
	}
	role, err := runner.DetectStatefulRecipeRole(context.Background(), recipe, runtime, "op_role", "tr_hooks")
	if err != nil {
		t.Fatalf("role detection returned error: %v", err)
	}
	if role.Role != "primary" || !reflect.DeepEqual(recipe.calls, []string{"backup", "restore", "detect_role"}) {
		t.Fatalf("unexpected role/calls: role=%+v calls=%v", role, recipe.calls)
	}
}

func statefulRunnerConfig(t *testing.T) config.Config {
	t.Helper()
	key, err := paths.StatefulMemberControl("payments-api", 0)
	if err != nil {
		t.Fatalf("StatefulMemberControl path returned error: %v", err)
	}
	return config.Config{
		Mode:                    config.ModeRunner,
		Env:                     "prod",
		Service:                 "payments-api",
		Provider:                "aws",
		Region:                  "us-west-2",
		StateBucket:             "memory://runner-test",
		ControlKey:              key,
		ReleaseID:               "rel_01JABC",
		StatefulGroup:           "payments-api",
		StatefulMember:          0,
		StatefulGeneration:      2,
		StatefulVolumeMountPath: "/mnt/stateful",
		StatefulStableHostname:  "payments-api-0.internal.example.com",
		StatefulRecipe:          "postgres",
	}
}

func statefulLocalStateFixture(t *testing.T) runner.LocalState {
	runtime := statefulRuntimeFixture(t)
	return runner.LocalState{
		SchemaVersion:       runner.RunnerStateSchemaVersion,
		Service:             "payments-api",
		Env:                 "prod",
		CurrentState:        runner.StatePreparingArtifact,
		Health:              runner.HealthUnknown,
		LastAcceptedRelease: "rel_01JABC",
		ControlKey:          runtime.ControlKey,
		UpdatedAt:           "2026-05-18T00:00:00Z",
		Stateful:            &runtime,
	}
}

func statefulRuntimeFixture(t *testing.T) runner.StatefulRuntime {
	t.Helper()
	key, err := paths.StatefulMemberControl("payments-api", 0)
	if err != nil {
		t.Fatalf("StatefulMemberControl path returned error: %v", err)
	}
	return runner.StatefulRuntime{
		Group:           "payments-api",
		Env:             "prod",
		Member:          0,
		Generation:      2,
		InstanceID:      "i-abc123",
		VolumeID:        "vol-123",
		VolumeMountPath: "/mnt/stateful",
		StableHostname:  "payments-api-0.internal.example.com",
		Recipe:          "postgres",
		ControlKey:      key,
		ControlVersion:  1,
	}
}

func statefulControlFixture() schema.StatefulMemberControl {
	return schema.StatefulMemberControl{
		SchemaVersion: schema.Version,
		Group:         "payments-api",
		Env:           "prod",
		Member:        0,
		Zone:          "us-west-2a",
		InstanceID:    "i-abc123",
		VolumeID:      "vol-123",
		DNSName:       "payments-api-0.internal.example.com",
		Generation:    2,
		Phase:         state.StatefulMemberReady,
		Version:       1,
		UpdatedAt:     "2026-05-18T00:00:00Z",
		UpdatedBy:     schema.Actor{ID: "test", Type: "agent"},
	}
}

func putStatefulMemberControl(t *testing.T, ctx context.Context, store *memory.Store, control schema.StatefulMemberControl) {
	t.Helper()
	key, err := paths.StatefulMemberControl(control.Group, control.Member)
	if err != nil {
		t.Fatalf("StatefulMemberControl path returned error: %v", err)
	}
	putJSON(t, ctx, store, key, control)
}

type recordingStatefulVolume struct {
	requests []runner.StatefulVolumeRequest
	err      error
}

func (v *recordingStatefulVolume) PrepareStatefulVolume(ctx context.Context, req runner.StatefulVolumeRequest) (*runner.StatefulVolumeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v.requests = append(v.requests, req)
	if v.err != nil {
		return nil, v.err
	}
	return &runner.StatefulVolumeResult{VolumeID: req.Runtime.VolumeID, MountPath: req.Runtime.VolumeMountPath}, nil
}

type recordingStatefulRecipe struct {
	name       string
	calls      []string
	lastReq    stateful.RecipeRequest
	failAction string
}

func (r *recordingStatefulRecipe) Name() string { return r.name }

func (r *recordingStatefulRecipe) Stop(ctx context.Context, req stateful.RecipeRequest) (*stateful.RecipeResult, error) {
	return r.record(ctx, "stop", req)
}

func (r *recordingStatefulRecipe) Start(ctx context.Context, req stateful.RecipeRequest) (*stateful.RecipeResult, error) {
	return r.record(ctx, "start", req)
}

func (r *recordingStatefulRecipe) Health(ctx context.Context, req stateful.RecipeRequest) (*stateful.RecipeResult, error) {
	return r.record(ctx, "health", req)
}

func (r *recordingStatefulRecipe) Backup(ctx context.Context, req stateful.RecipeRequest) (*stateful.RecipeResult, error) {
	return r.record(ctx, "backup", req)
}

func (r *recordingStatefulRecipe) Restore(ctx context.Context, req stateful.RecipeRequest) (*stateful.RecipeResult, error) {
	return r.record(ctx, "restore", req)
}

func (r *recordingStatefulRecipe) DetectRole(ctx context.Context, req stateful.RecipeRequest) (*stateful.RoleResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.calls = append(r.calls, "detect_role")
	r.lastReq = req
	return &stateful.RoleResult{Role: "primary", Primary: true}, nil
}

func (r *recordingStatefulRecipe) record(ctx context.Context, action string, req stateful.RecipeRequest) (*stateful.RecipeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.calls = append(r.calls, action)
	r.lastReq = req
	if r.failAction == action {
		return &stateful.RecipeResult{OK: false, Summary: action + " failed"}, nil
	}
	return &stateful.RecipeResult{OK: true, Summary: action + " ok"}, nil
}
