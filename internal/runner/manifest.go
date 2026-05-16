package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	CodeBootstrapInvalid       = "RUNNER_BOOTSTRAP_INVALID"
	CodeIdentityFailed         = "IDENTITY_DISCOVERY_FAILED"
	CodeLocalStateReadFailed   = "LOCAL_STATE_READ_FAILED"
	CodeControlNotFound        = "SERVICE_CONTROL_NOT_FOUND"
	CodeInvalidServiceControl  = "INVALID_SERVICE_CONTROL"
	CodeTargetMismatch         = "RUNNER_TARGET_MISMATCH"
	CodeNoDesiredRelease       = "DESIRED_RELEASE_REQUIRED"
	CodeStaleReleaseRejected   = "STALE_RELEASE_REJECTED"
	CodeLocalStateWriteFailed  = "LOCAL_STATE_WRITE_FAILED"
	CodeRunnerEventWriteFailed = "RUNNER_EVENT_WRITE_FAILED"
)

type BootstrapRequest struct {
	Config           config.Config
	Store            objstore.ObjectStore
	Verifier         signing.Verifier
	MetadataProvider MetadataProvider
	StateStore       StateStore
	EventSink        EventSink
	TraceID          string
	Now              func() time.Time
	IdentityOptions  IdentityOptions
}

type BootstrapResult struct {
	Service      string                     `json:"service"`
	Env          string                     `json:"env"`
	ReleaseID    string                     `json:"release_id"`
	ControlKey   string                     `json:"control_key"`
	ReleaseKey   string                     `json:"release_key"`
	RuntimeKey   string                     `json:"runtime_manifest_key"`
	Identity     Identity                   `json:"identity"`
	LocalState   LocalState                 `json:"local_state"`
	Events       []StateEvent               `json:"events"`
	Verification release.VerificationResult `json:"verification"`
}

type BootstrapError struct {
	Code         string
	Summary      string
	Err          error
	Verification *release.VerificationResult
}

func (e *BootstrapError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Summary)
}

func (e *BootstrapError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func Bootstrap(ctx context.Context, req BootstrapRequest) (*BootstrapResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := req.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	stateStore := req.StateStore
	if stateStore == nil {
		stateStore = FileStateStore{}
	}
	result := &BootstrapResult{
		Service:    req.Config.Service,
		Env:        req.Config.Env,
		ControlKey: req.Config.ControlKey,
	}

	emit := func(state State, releaseID, summary string, cause error, identity *Identity) error {
		event := StateEvent{
			SchemaVersion: RunnerEventSchemaVersion,
			Time:          canonical.Time(now()),
			State:         state,
			Service:       req.Config.Service,
			Env:           req.Config.Env,
			ReleaseID:     releaseID,
			TraceID:       req.TraceID,
			Identity:      identity,
			Summary:       summary,
		}
		if cause != nil {
			event.Error = cause.Error()
		}
		result.Events = append(result.Events, event)
		if req.EventSink == nil {
			return nil
		}
		if err := req.EventSink.EmitRunnerEvent(ctx, event); err != nil {
			return &BootstrapError{Code: CodeRunnerEventWriteFailed, Summary: "write runner state transition event", Err: err}
		}
		return nil
	}

	fail := func(code, summary string, err error, verification *release.VerificationResult, identity *Identity, releaseID string) (*BootstrapResult, error) {
		_ = emit(StateFailed, releaseID, summary, err, identity)
		return nil, &BootstrapError{Code: code, Summary: summary, Err: err, Verification: verification}
	}

	if req.Store == nil {
		return fail(CodeBootstrapInvalid, "object store is required", nil, nil, nil, "")
	}
	if err := ValidateConfig(req.Config); err != nil {
		return fail(CodeBootstrapInvalid, err.Error(), err, nil, nil, "")
	}
	if err := emit(StateBooting, "", "runner boot started", nil, nil); err != nil {
		return nil, err
	}

	identity, err := DiscoverIdentity(ctx, req.MetadataProvider, req.IdentityOptions)
	if err != nil {
		return fail(CodeIdentityFailed, "discover cloud instance identity", err, nil, nil, "")
	}
	if err := validateIdentity(identity, req.Config); err != nil {
		return fail(CodeTargetMismatch, err.Error(), err, nil, &identity, "")
	}
	if identity.ObservedAt == "" {
		identity.ObservedAt = canonical.Time(now())
	}
	result.Identity = identity
	if err := emit(StateFetchingManifest, "", "fetching service control and desired release", nil, &identity); err != nil {
		return nil, err
	}

	previous, err := stateStore.LoadState(ctx)
	if err != nil && !errors.Is(err, ErrStateNotFound) {
		return fail(CodeLocalStateReadFailed, "read runner local state", err, nil, &identity, "")
	}

	control, err := readServiceControl(ctx, req.Store, req.Config.ControlKey)
	if err != nil {
		var bootErr *BootstrapError
		if errors.As(err, &bootErr) {
			return fail(bootErr.Code, bootErr.Summary, bootErr.Err, nil, &identity, "")
		}
		return fail(CodeInvalidServiceControl, err.Error(), err, nil, &identity, "")
	}
	if err := validateControlTarget(control, req.Config); err != nil {
		return fail(CodeTargetMismatch, err.Error(), err, nil, &identity, control.DesiredRelease)
	}
	if control.DesiredRelease == "" {
		return fail(CodeNoDesiredRelease, "service control does not contain desired_release", nil, nil, &identity, "")
	}
	result.ReleaseID = control.DesiredRelease
	if err := emit(StateVerifyingRelease, control.DesiredRelease, "verifying desired release and runtime manifest", nil, &identity); err != nil {
		return nil, err
	}

	fetched, err := release.Fetch(ctx, req.Store, release.FetchOptions{
		Service:   req.Config.Service,
		Env:       req.Config.Env,
		ReleaseID: control.DesiredRelease,
		Verifier:  req.Verifier,
		Now:       now(),
	})
	if err != nil {
		var fetchErr *release.FetchError
		if errors.As(err, &fetchErr) {
			return fail(fetchErr.Code, fetchErr.Summary, err, fetchErr.Verification, &identity, control.DesiredRelease)
		}
		return fail(release.CodeReleaseFetchInvalid, err.Error(), err, nil, &identity, control.DesiredRelease)
	}
	if err := checkStaleRelease(previous, control, fetched.ReleaseManifest); err != nil {
		return fail(CodeStaleReleaseRejected, err.Error(), err, nil, &identity, fetched.ReleaseManifest.ReleaseID)
	}

	localState := LocalState{
		SchemaVersion:              RunnerStateSchemaVersion,
		Service:                    req.Config.Service,
		Env:                        req.Config.Env,
		CurrentState:               StatePreparingArtifact,
		LastAcceptedRelease:        fetched.ReleaseManifest.ReleaseID,
		LastAcceptedReleaseCreated: fetched.ReleaseManifest.CreatedAt,
		ReleaseDigest:              fetched.Verification.Digest,
		RuntimeManifestDigest:      fetched.Verification.RuntimeManifestDigest,
		ControlKey:                 req.Config.ControlKey,
		ReleaseKey:                 fetched.ReleaseKey,
		RuntimeManifestKey:         fetched.RuntimeManifestKey,
		TraceID:                    req.TraceID,
		UpdatedAt:                  canonical.Time(now()),
		Identity:                   &identity,
		Verification:               fetched.Verification,
	}
	if err := stateStore.SaveState(ctx, localState); err != nil {
		return fail(CodeLocalStateWriteFailed, "write runner local state", err, nil, &identity, fetched.ReleaseManifest.ReleaseID)
	}
	if err := emit(StatePreparingArtifact, fetched.ReleaseManifest.ReleaseID, "release verified; ready to prepare artifact", nil, &identity); err != nil {
		return nil, err
	}

	result.ReleaseKey = fetched.ReleaseKey
	result.RuntimeKey = fetched.RuntimeManifestKey
	result.LocalState = localState
	result.Verification = fetched.Verification
	return result, nil
}

func readServiceControl(ctx context.Context, store objstore.ObjectStore, key string) (schema.ServiceControl, error) {
	object, err := store.Get(ctx, key)
	if err != nil {
		code := CodeInvalidServiceControl
		summary := err.Error()
		if errors.Is(err, objstore.ErrNotFound) {
			code = CodeControlNotFound
			summary = "service control object was not found"
		}
		return schema.ServiceControl{}, &BootstrapError{Code: code, Summary: summary, Err: err}
	}
	var control schema.ServiceControl
	if err := canonical.UnmarshalStrict(object.Body, &control); err != nil {
		return schema.ServiceControl{}, &BootstrapError{Code: CodeInvalidServiceControl, Summary: err.Error(), Err: err}
	}
	if control.SchemaVersion != schema.Version {
		return schema.ServiceControl{}, &BootstrapError{
			Code:    CodeInvalidServiceControl,
			Summary: fmt.Sprintf("service control schema version %q is not supported", control.SchemaVersion),
		}
	}
	return control, nil
}

func validateControlTarget(control schema.ServiceControl, cfg config.Config) error {
	if control.Service != cfg.Service {
		return fmt.Errorf("service control service %q does not match runner service %q", control.Service, cfg.Service)
	}
	if control.Env != cfg.Env {
		return fmt.Errorf("service control env %q does not match runner env %q", control.Env, cfg.Env)
	}
	return nil
}

func validateIdentity(identity Identity, cfg config.Config) error {
	if identity.Provider != "" && identity.Provider != cfg.Provider {
		return fmt.Errorf("identity provider %q does not match runner provider %q", identity.Provider, cfg.Provider)
	}
	if identity.Region != "" && identity.Region != cfg.Region {
		return fmt.Errorf("identity region %q does not match runner region %q", identity.Region, cfg.Region)
	}
	return nil
}

func checkStaleRelease(previous *LocalState, control schema.ServiceControl, manifest schema.ReleaseManifest) error {
	if previous == nil || previous.LastAcceptedRelease == "" || previous.LastAcceptedRelease == manifest.ReleaseID {
		return nil
	}
	if previous.LastAcceptedReleaseCreated == "" || manifest.CreatedAt == "" {
		return nil
	}
	previousCreated, err := time.Parse(time.RFC3339Nano, previous.LastAcceptedReleaseCreated)
	if err != nil {
		return fmt.Errorf("previous local state release creation time %q is invalid: %w", previous.LastAcceptedReleaseCreated, err)
	}
	desiredCreated, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt)
	if err != nil {
		return fmt.Errorf("desired release creation time %q is invalid: %w", manifest.CreatedAt, err)
	}
	if !desiredCreated.Before(previousCreated) {
		return nil
	}
	if control.StableRelease != "" && control.StableRelease == manifest.ReleaseID {
		return nil
	}
	return fmt.Errorf("desired release %q was created before last accepted release %q and is not the service stable release", manifest.ReleaseID, previous.LastAcceptedRelease)
}
