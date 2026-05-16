package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	CodeLifecycleInvalid      = "RUNNER_LIFECYCLE_INVALID"
	CodeArtifactPrepareFailed = "ARTIFACT_PREPARE_FAILED"
	CodeSystemdRenderFailed   = "SYSTEMD_RENDER_FAILED"
	CodeSystemdStartFailed    = "SYSTEMD_START_FAILED"
	CodeSystemdStopFailed     = "SYSTEMD_STOP_FAILED"
	CodeHealthCheckFailed     = "HEALTH_CHECK_FAILED"
)

type ArtifactRequest struct {
	Service         string
	Env             string
	ReleaseID       string
	Artifact        schema.ArtifactRef
	RuntimeManifest schema.RuntimeManifest
}

type ArtifactResult struct {
	Command          []string
	EnvVars          map[string]string
	WorkingDirectory string
}

type ArtifactPreparer interface {
	PrepareArtifact(ctx context.Context, req ArtifactRequest) (*ArtifactResult, error)
}

type NoopArtifactPreparer struct{}

func (NoopArtifactPreparer) PrepareArtifact(ctx context.Context, req ArtifactRequest) (*ArtifactResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &ArtifactResult{
		Command: append([]string(nil), req.RuntimeManifest.Command...),
		EnvVars: cloneStringMap(req.RuntimeManifest.EnvVars),
	}, nil
}

type LifecycleRequest struct {
	RuntimeManifest  schema.RuntimeManifest
	Artifact         schema.ArtifactRef
	StateStore       StateStore
	EventSink        EventSink
	Systemd          SystemdManager
	ArtifactPreparer ArtifactPreparer
	HealthChecker    HealthChecker
	TraceID          string
	Identity         *Identity
	UnitName         string
	HealthAttempts   int
	HealthInterval   time.Duration
	StopTimeout      time.Duration
	Now              func() time.Time
	Sleep            func(context.Context, time.Duration) error
}

type LifecycleResult struct {
	Status   RunnerStatus `json:"status"`
	UnitName string       `json:"unit_name"`
	Events   []StateEvent `json:"events"`
}

type LifecycleError struct {
	Code    string
	Summary string
	State   State
	Err     error
}

func (e *LifecycleError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Summary)
}

func (e *LifecycleError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func RunLifecycle(ctx context.Context, req LifecycleRequest) (*LifecycleResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := req.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	sleep := req.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	stateStore := req.StateStore
	if stateStore == nil {
		stateStore = FileStateStore{}
	}
	if req.Systemd == nil {
		return nil, &LifecycleError{Code: CodeLifecycleInvalid, Summary: "systemd manager is required"}
	}
	preparer := req.ArtifactPreparer
	if preparer == nil {
		preparer = NoopArtifactPreparer{}
	}
	healthChecker := req.HealthChecker
	if healthChecker == nil {
		healthChecker = ProbeHealthChecker{}
	}
	manifest := req.RuntimeManifest
	if err := validateRuntimeManifestForLifecycle(manifest); err != nil {
		return nil, &LifecycleError{Code: CodeLifecycleInvalid, Summary: err.Error(), Err: err}
	}
	unitName := req.UnitName
	if unitName == "" {
		unitName = WorkloadUnitName(manifest.Service, manifest.Env)
	}
	current, err := loadLifecycleState(ctx, stateStore, manifest, req.TraceID, req.Identity, unitName, now)
	if err != nil {
		return nil, &LifecycleError{Code: CodeLocalStateReadFailed, Summary: "read runner local state", Err: err}
	}
	result := &LifecycleResult{UnitName: unitName}

	transition := func(state State, health HealthStatus, summary string, cause error) error {
		current.CurrentState = state
		current.Health = health
		current.WorkloadUnit = unitName
		current.UpdatedAt = canonical.Time(now())
		if req.TraceID != "" {
			current.TraceID = req.TraceID
		}
		if req.Identity != nil {
			identity := *req.Identity
			current.Identity = &identity
		}
		if err := stateStore.SaveState(ctx, current); err != nil {
			return &LifecycleError{Code: CodeLocalStateWriteFailed, Summary: "write runner local state", State: state, Err: err}
		}
		event := StateEvent{
			SchemaVersion: RunnerEventSchemaVersion,
			Time:          canonical.Time(now()),
			State:         state,
			Service:       manifest.Service,
			Env:           manifest.Env,
			ReleaseID:     manifest.ReleaseID,
			TraceID:       current.TraceID,
			Identity:      current.Identity,
			Health:        health,
			UnitName:      unitName,
			Summary:       summary,
		}
		if cause != nil {
			event.Error = cause.Error()
		}
		result.Events = append(result.Events, event)
		if req.EventSink != nil {
			if err := req.EventSink.EmitRunnerEvent(ctx, event); err != nil {
				return &LifecycleError{Code: CodeRunnerEventWriteFailed, Summary: "write runner state transition event", State: state, Err: err}
			}
		}
		return nil
	}
	fail := func(code, summary string, state State, cause error) (*LifecycleResult, error) {
		if err := transition(StateFailed, HealthUnhealthy, summary, cause); err != nil {
			result.Status = StatusFromState(current)
			return result, err
		}
		result.Status = StatusFromState(current)
		return result, &LifecycleError{Code: code, Summary: summary, State: state, Err: cause}
	}

	if err := transition(StatePreparingArtifact, HealthUnknown, "preparing workload artifact", nil); err != nil {
		return nil, err
	}
	artifact, err := preparer.PrepareArtifact(ctx, ArtifactRequest{
		Service:         manifest.Service,
		Env:             manifest.Env,
		ReleaseID:       manifest.ReleaseID,
		Artifact:        req.Artifact,
		RuntimeManifest: manifest,
	})
	if err != nil {
		return fail(CodeArtifactPrepareFailed, "prepare workload artifact", StatePreparingArtifact, err)
	}
	command := append([]string(nil), manifest.Command...)
	envVars := cloneStringMap(manifest.EnvVars)
	workingDirectory := ""
	if artifact != nil {
		if len(artifact.Command) > 0 {
			command = append([]string(nil), artifact.Command...)
		}
		if artifact.EnvVars != nil {
			envVars = cloneStringMap(artifact.EnvVars)
		}
		workingDirectory = artifact.WorkingDirectory
	}

	if err := transition(StateRenderingConfig, HealthUnknown, "rendering workload systemd unit", nil); err != nil {
		return nil, err
	}
	unit, err := RenderWorkloadUnit(WorkloadUnitSpec{
		Service:          manifest.Service,
		Env:              manifest.Env,
		ReleaseID:        manifest.ReleaseID,
		Command:          command,
		EnvVars:          envVars,
		WorkingDirectory: workingDirectory,
		StopTimeout:      req.StopTimeout,
	})
	if err != nil {
		return fail(CodeSystemdRenderFailed, "render workload systemd unit", StateRenderingConfig, err)
	}
	if err := req.Systemd.WriteUnit(ctx, unitName, []byte(unit)); err != nil {
		return fail(CodeSystemdRenderFailed, "write workload systemd unit", StateRenderingConfig, err)
	}
	if err := req.Systemd.DaemonReload(ctx); err != nil {
		return fail(CodeSystemdStartFailed, "reload systemd units", StateRenderingConfig, err)
	}

	if err := transition(StateStartingWorkload, HealthUnknown, "starting workload through systemd", nil); err != nil {
		return nil, err
	}
	if err := req.Systemd.RestartUnit(ctx, unitName); err != nil {
		return fail(CodeSystemdStartFailed, "restart workload unit", StateStartingWorkload, err)
	}

	if err := transition(StateWaitingForHealth, HealthUnknown, "waiting for workload health check", nil); err != nil {
		return nil, err
	}
	attempts := req.HealthAttempts
	if attempts < 1 {
		attempts = 1
	}
	interval := req.HealthInterval
	if interval <= 0 && manifest.HealthCheck != nil {
		interval = durationOrDefault(manifest.HealthCheck.Interval, 10*time.Second)
	}
	var last HealthResult
	for attempt := 1; attempt <= attempts; attempt++ {
		last = healthChecker.Check(ctx, manifest.HealthCheck)
		if last.Status == HealthHealthy {
			if err := transition(StateServing, HealthHealthy, "workload is serving", nil); err != nil {
				return nil, err
			}
			result.Status = StatusFromState(current)
			return result, nil
		}
		if attempt < attempts && interval > 0 {
			if err := sleep(ctx, interval); err != nil {
				return fail(CodeHealthCheckFailed, "wait for workload health check", StateWaitingForHealth, err)
			}
		}
	}
	summary := "workload health check did not become healthy"
	if last.Summary != "" {
		summary = last.Summary
	}
	return fail(CodeHealthCheckFailed, summary, StateWaitingForHealth, healthFailureCause(last))
}

type StopRequest struct {
	Service    string
	Env        string
	ReleaseID  string
	StateStore StateStore
	EventSink  EventSink
	Systemd    SystemdManager
	UnitName   string
	TraceID    string
	Identity   *Identity
	Drain      func(context.Context) error
	Now        func() time.Time
}

func StopWorkload(ctx context.Context, req StopRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if req.Systemd == nil {
		return &LifecycleError{Code: CodeLifecycleInvalid, Summary: "systemd manager is required"}
	}
	stateStore := req.StateStore
	if stateStore == nil {
		stateStore = FileStateStore{}
	}
	now := req.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	unitName := req.UnitName
	if unitName == "" {
		unitName = WorkloadUnitName(req.Service, req.Env)
	}
	current, err := loadLifecycleState(ctx, stateStore, schema.RuntimeManifest{
		Service:   req.Service,
		Env:       req.Env,
		ReleaseID: req.ReleaseID,
	}, req.TraceID, req.Identity, unitName, now)
	if err != nil {
		return &LifecycleError{Code: CodeLocalStateReadFailed, Summary: "read runner local state", Err: err}
	}
	transition := func(state State, summary string, cause error) error {
		current.CurrentState = state
		current.WorkloadUnit = unitName
		current.UpdatedAt = canonical.Time(now())
		if req.TraceID != "" {
			current.TraceID = req.TraceID
		}
		if req.Identity != nil {
			identity := *req.Identity
			current.Identity = &identity
		}
		if err := stateStore.SaveState(ctx, current); err != nil {
			return &LifecycleError{Code: CodeLocalStateWriteFailed, Summary: "write runner local state", State: state, Err: err}
		}
		if req.EventSink == nil {
			return nil
		}
		event := StateEvent{
			SchemaVersion: RunnerEventSchemaVersion,
			Time:          canonical.Time(now()),
			State:         state,
			Service:       current.Service,
			Env:           current.Env,
			ReleaseID:     current.LastAcceptedRelease,
			TraceID:       current.TraceID,
			Identity:      current.Identity,
			Health:        current.Health,
			UnitName:      unitName,
			Summary:       summary,
		}
		if cause != nil {
			event.Error = cause.Error()
		}
		if err := req.EventSink.EmitRunnerEvent(ctx, event); err != nil {
			return &LifecycleError{Code: CodeRunnerEventWriteFailed, Summary: "write runner state transition event", State: state, Err: err}
		}
		return nil
	}
	if err := transition(StateDraining, "draining workload", nil); err != nil {
		return err
	}
	if req.Drain != nil {
		if err := req.Drain(ctx); err != nil {
			_ = transition(StateFailed, "drain workload", err)
			return &LifecycleError{Code: CodeSystemdStopFailed, Summary: "drain workload", State: StateDraining, Err: err}
		}
	}
	if err := transition(StateStopping, "stopping workload through systemd", nil); err != nil {
		return err
	}
	if err := req.Systemd.StopUnit(ctx, unitName); err != nil {
		_ = transition(StateFailed, "stop workload unit", err)
		return &LifecycleError{Code: CodeSystemdStopFailed, Summary: "stop workload unit", State: StateStopping, Err: err}
	}
	current.Health = HealthUnknown
	return transition(StateStopped, "workload stopped", nil)
}

func loadLifecycleState(ctx context.Context, store StateStore, manifest schema.RuntimeManifest, traceID string, identity *Identity, unitName string, now func() time.Time) (LocalState, error) {
	loaded, err := store.LoadState(ctx)
	if err != nil && !errors.Is(err, ErrStateNotFound) {
		return LocalState{}, err
	}
	var state LocalState
	if loaded != nil {
		state = *loaded
	}
	if state.SchemaVersion == "" {
		state.SchemaVersion = RunnerStateSchemaVersion
	}
	if state.Service == "" {
		state.Service = manifest.Service
	}
	if state.Env == "" {
		state.Env = manifest.Env
	}
	if state.LastAcceptedRelease == "" {
		state.LastAcceptedRelease = manifest.ReleaseID
	}
	if state.TraceID == "" {
		state.TraceID = traceID
	}
	if state.UpdatedAt == "" {
		state.UpdatedAt = canonical.Time(now())
	}
	if state.Health == "" {
		state.Health = HealthUnknown
	}
	if state.WorkloadUnit == "" {
		state.WorkloadUnit = unitName
	}
	if state.Identity == nil && identity != nil {
		value := *identity
		state.Identity = &value
	}
	return state, nil
}

func validateRuntimeManifestForLifecycle(manifest schema.RuntimeManifest) error {
	if manifest.Service == "" {
		return errors.New("runtime manifest service is required")
	}
	if manifest.Env == "" {
		return errors.New("runtime manifest env is required")
	}
	if manifest.ReleaseID == "" {
		return errors.New("runtime manifest release_id is required")
	}
	if len(manifest.Command) == 0 {
		return errors.New("runtime manifest command is required")
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func healthFailureCause(result HealthResult) error {
	if result.Error != "" {
		return errors.New(result.Error)
	}
	if result.Summary != "" {
		return errors.New(result.Summary)
	}
	return errors.New("health check did not become healthy")
}
