package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/runner"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRunLifecycleMovesToServingAfterHealthSucceeds(t *testing.T) {
	ctx := context.Background()
	stateStore := &memoryStateStore{}
	systemd := &fakeSystemd{}
	events := &collectingSink{}
	health := &scriptedHealthChecker{results: []runner.HealthResult{
		{Status: runner.HealthUnhealthy, Summary: "warming up"},
		{Status: runner.HealthHealthy, Summary: "ready"},
	}}

	result, err := runner.RunLifecycle(ctx, runner.LifecycleRequest{
		RuntimeManifest: runtimeManifestFixture(),
		StateStore:      stateStore,
		EventSink:       events,
		Systemd:         systemd,
		HealthChecker:   health,
		TraceID:         "tr_lifecycle",
		HealthAttempts:  3,
		HealthInterval:  time.Nanosecond,
		Sleep:           func(context.Context, time.Duration) error { return nil },
		Now:             fixedNow,
	})
	if err != nil {
		t.Fatalf("RunLifecycle returned error: %v", err)
	}
	if result.Status.State != runner.StateServing || result.Status.Health != runner.HealthHealthy {
		t.Fatalf("unexpected status: %+v", result.Status)
	}
	if stateStore.state.CurrentState != runner.StateServing || stateStore.state.Health != runner.HealthHealthy {
		t.Fatalf("unexpected persisted state: %+v", stateStore.state)
	}
	if systemd.unitName != "skiff-payments-api-prod.service" || !systemd.daemonReloaded || systemd.restarted != systemd.unitName {
		t.Fatalf("systemd calls were not recorded correctly: %+v", systemd)
	}
	for _, want := range []string{
		`Environment="PORT=8080" "RACK_ENV=production"`,
		`ExecStart="./payments-api" "serve"`,
		"NoNewPrivileges=yes",
		"ProtectSystem=strict",
		"CapabilityBoundingSet=",
	} {
		if !strings.Contains(systemd.unitBody, want) {
			t.Fatalf("rendered unit missing %q:\n%s", want, systemd.unitBody)
		}
	}
	wantStates := []runner.State{
		runner.StatePreparingArtifact,
		runner.StateRenderingConfig,
		runner.StateStartingWorkload,
		runner.StateWaitingForHealth,
		runner.StateServing,
	}
	if got := eventStates(events.events); !reflect.DeepEqual(got, wantStates) {
		t.Fatalf("event states = %v, want %v", got, wantStates)
	}
}

func TestRunLifecycleFailsWhenHealthNeverSucceeds(t *testing.T) {
	stateStore := &memoryStateStore{}
	systemd := &fakeSystemd{}
	_, err := runner.RunLifecycle(context.Background(), runner.LifecycleRequest{
		RuntimeManifest: runtimeManifestFixture(),
		StateStore:      stateStore,
		Systemd:         systemd,
		HealthChecker: &scriptedHealthChecker{results: []runner.HealthResult{
			{Status: runner.HealthUnhealthy, Summary: "not ready"},
			{Status: runner.HealthUnhealthy, Summary: "still not ready"},
		}},
		HealthAttempts: 2,
		Sleep:          func(context.Context, time.Duration) error { return nil },
		Now:            fixedNow,
	})
	if err == nil {
		t.Fatal("RunLifecycle succeeded, want health failure")
	}
	var lifecycleErr *runner.LifecycleError
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != runner.CodeHealthCheckFailed {
		t.Fatalf("error = %v, want %s", err, runner.CodeHealthCheckFailed)
	}
	if stateStore.state.CurrentState != runner.StateFailed || stateStore.state.Health != runner.HealthUnhealthy {
		t.Fatalf("unexpected failed state: %+v", stateStore.state)
	}
}

func TestRenderWorkloadUnitIsDeterministic(t *testing.T) {
	unit, err := runner.RenderWorkloadUnit(runner.WorkloadUnitSpec{
		Service:          "payments-api",
		Env:              "prod",
		ReleaseID:        "rel_01JABC",
		Command:          []string{"/opt/skiff/workloads/payments-api/bin/server", "--port", "8080"},
		WorkingDirectory: "/opt/skiff/workloads/payments-api",
		EnvVars:          map[string]string{"ZED": "last", "ALPHA": "first"},
		StopTimeout:      45 * time.Second,
	})
	if err != nil {
		t.Fatalf("RenderWorkloadUnit returned error: %v", err)
	}
	want := `[Unit]
Description=Skiff workload prod/payments-api release rel_01JABC
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory="/opt/skiff/workloads/payments-api"
Environment="ALPHA=first" "ZED=last"
ExecStart="/opt/skiff/workloads/payments-api/bin/server" "--port" "8080"
Restart=always
RestartSec=5s
KillSignal=SIGTERM
TimeoutStopSec=45s
SyslogIdentifier=skiff-payments-api-prod.service
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
CapabilityBoundingSet=
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
`
	if unit != want {
		t.Fatalf("unit mismatch\nwant:\n%s\ngot:\n%s", want, unit)
	}
}

func TestStatusHandlerReturnsMachineReadableRunnerState(t *testing.T) {
	store := &memoryStateStore{state: runner.LocalState{
		SchemaVersion:       runner.RunnerStateSchemaVersion,
		Service:             "payments-api",
		Env:                 "prod",
		CurrentState:        runner.StateServing,
		Health:              runner.HealthHealthy,
		LastAcceptedRelease: "rel_01JABC",
		WorkloadUnit:        "skiff-payments-api-prod.service",
		TraceID:             "tr_status",
		UpdatedAt:           "2026-05-18T00:00:00Z",
	}}
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	runner.NewStatusHandler(store).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got runner.RunnerStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("status body is not JSON: %v\n%s", err, rec.Body.String())
	}
	if !got.OK || got.Service != "payments-api" || got.ReleaseID != "rel_01JABC" || got.State != runner.StateServing || got.Health != runner.HealthHealthy {
		t.Fatalf("unexpected status: %+v", got)
	}
}

func TestLocalStatusServerRejectsPublicBindAddress(t *testing.T) {
	err := runner.ListenAndServeLocalStatus(context.Background(), "0.0.0.0:0", &memoryStateStore{})
	if err == nil || !strings.Contains(err.Error(), "must bind to localhost or loopback") {
		t.Fatalf("ListenAndServeLocalStatus error = %v, want public bind rejection", err)
	}
}

func TestStopWorkloadTransitionsThroughDrainAndStop(t *testing.T) {
	stateStore := &memoryStateStore{state: runner.LocalState{
		SchemaVersion:       runner.RunnerStateSchemaVersion,
		Service:             "payments-api",
		Env:                 "prod",
		CurrentState:        runner.StateServing,
		Health:              runner.HealthHealthy,
		LastAcceptedRelease: "rel_01JABC",
		UpdatedAt:           "2026-05-18T00:00:00Z",
	}}
	events := &collectingSink{}
	systemd := &fakeSystemd{}

	err := runner.StopWorkload(context.Background(), runner.StopRequest{
		Service:    "payments-api",
		Env:        "prod",
		ReleaseID:  "rel_01JABC",
		StateStore: stateStore,
		EventSink:  events,
		Systemd:    systemd,
		Now:        fixedNow,
	})
	if err != nil {
		t.Fatalf("StopWorkload returned error: %v", err)
	}
	if stateStore.state.CurrentState != runner.StateStopped || systemd.stopped != "skiff-payments-api-prod.service" {
		t.Fatalf("unexpected stop result: state=%+v systemd=%+v", stateStore.state, systemd)
	}
	wantStates := []runner.State{runner.StateDraining, runner.StateStopping, runner.StateStopped}
	if got := eventStates(events.events); !reflect.DeepEqual(got, wantStates) {
		t.Fatalf("event states = %v, want %v", got, wantStates)
	}
}

func runtimeManifestFixture() schema.RuntimeManifest {
	return schema.RuntimeManifest{
		SchemaVersion: schema.Version,
		Service:       "payments-api",
		Env:           "prod",
		ReleaseID:     "rel_01JABC",
		Command:       []string{"./payments-api", "serve"},
		EnvVars:       map[string]string{"RACK_ENV": "production", "PORT": "8080"},
		HealthCheck:   &schema.HealthCheck{Type: "http", Path: "/healthz", Port: 8080, Interval: "10s", Timeout: "2s"},
		CreatedAt:     "2026-05-18T00:00:00Z",
	}
}

type memoryStateStore struct {
	state runner.LocalState
}

func (s *memoryStateStore) LoadState(ctx context.Context) (*runner.LocalState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.state.SchemaVersion == "" {
		return nil, runner.ErrStateNotFound
	}
	state := s.state
	return &state, nil
}

func (s *memoryStateStore) SaveState(ctx context.Context, state runner.LocalState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.state = state
	return nil
}

type fakeSystemd struct {
	unitName       string
	unitBody       string
	daemonReloaded bool
	started        string
	restarted      string
	stopped        string
}

func (s *fakeSystemd) WriteUnit(ctx context.Context, name string, contents []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.unitName = name
	s.unitBody = string(contents)
	return nil
}

func (s *fakeSystemd) DaemonReload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.daemonReloaded = true
	return nil
}

func (s *fakeSystemd) StartUnit(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.started = name
	return nil
}

func (s *fakeSystemd) RestartUnit(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.restarted = name
	return nil
}

func (s *fakeSystemd) StopUnit(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.stopped = name
	return nil
}

type scriptedHealthChecker struct {
	results []runner.HealthResult
	calls   int
}

func (c *scriptedHealthChecker) Check(ctx context.Context, check *schema.HealthCheck) runner.HealthResult {
	if err := ctx.Err(); err != nil {
		return runner.HealthResult{Status: runner.HealthUnhealthy, Error: err.Error()}
	}
	if c.calls >= len(c.results) {
		return c.results[len(c.results)-1]
	}
	result := c.results[c.calls]
	c.calls++
	return result
}
