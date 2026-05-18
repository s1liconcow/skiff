package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/buildinfo"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
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
	Stateful     *StatefulRuntime           `json:"stateful,omitempty"`
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
			Stateful:      result.Stateful,
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
	if req.Config.StatefulGroup != "" {
		return bootstrapStateful(ctx, req, result, identity, now, stateStore, emit, fail)
	}
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
		Service:       req.Config.Service,
		Env:           req.Config.Env,
		ReleaseID:     control.DesiredRelease,
		Verifier:      req.Verifier,
		Now:           now(),
		RunnerVersion: buildinfo.Version,
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

func bootstrapStateful(ctx context.Context, req BootstrapRequest, result *BootstrapResult, identity Identity, now func() time.Time, stateStore StateStore, emit func(State, string, string, error, *Identity) error, fail func(string, string, error, *release.VerificationResult, *Identity, string) (*BootstrapResult, error)) (*BootstrapResult, error) {
	if req.Config.ReleaseID == "" {
		return fail(CodeNoDesiredRelease, "stateful runner release_id is required", nil, nil, &identity, "")
	}
	if err := emit(StateFetchingManifest, req.Config.ReleaseID, "fetching stateful member control and release", nil, &identity); err != nil {
		return nil, err
	}
	previous, err := stateStore.LoadState(ctx)
	if err != nil && !errors.Is(err, ErrStateNotFound) {
		return fail(CodeLocalStateReadFailed, "read runner local state", err, nil, &identity, req.Config.ReleaseID)
	}
	member, err := readStatefulMemberControl(ctx, req.Store, statefulControlKey(req.Config))
	if err != nil {
		var bootErr *BootstrapError
		if errors.As(err, &bootErr) {
			return fail(bootErr.Code, bootErr.Summary, bootErr.Err, nil, &identity, req.Config.ReleaseID)
		}
		return fail(CodeInvalidServiceControl, err.Error(), err, nil, &identity, req.Config.ReleaseID)
	}
	runtime, err := statefulRuntimeFromControl(member, req.Config, identity, now())
	if err != nil {
		return fail(CodeTargetMismatch, err.Error(), err, nil, &identity, req.Config.ReleaseID)
	}
	if err := emit(StateVerifyingRelease, req.Config.ReleaseID, "verifying stateful release and runtime manifest", nil, &identity); err != nil {
		return nil, err
	}
	fetched, err := release.Fetch(ctx, req.Store, release.FetchOptions{
		Service:       req.Config.Service,
		Env:           req.Config.Env,
		ReleaseID:     req.Config.ReleaseID,
		ReleaseKey:    req.Config.ReleaseManifestKey,
		Verifier:      req.Verifier,
		Now:           now(),
		RunnerVersion: buildinfo.Version,
	})
	if err != nil {
		var fetchErr *release.FetchError
		if errors.As(err, &fetchErr) {
			return fail(fetchErr.Code, fetchErr.Summary, err, fetchErr.Verification, &identity, req.Config.ReleaseID)
		}
		return fail(release.CodeReleaseFetchInvalid, err.Error(), err, nil, &identity, req.Config.ReleaseID)
	}
	if err := checkStatefulStaleRelease(previous, fetched.ReleaseManifest); err != nil {
		return fail(CodeStaleReleaseRejected, err.Error(), err, nil, &identity, fetched.ReleaseManifest.ReleaseID)
	}
	runtime.ReleaseManifest = firstNonEmpty(req.Config.ReleaseManifestKey, fetched.ReleaseKey)
	runtime.RuntimeManifest = firstNonEmpty(req.Config.RuntimeManifestKey, fetched.RuntimeManifestKey)
	localState := LocalState{
		SchemaVersion:              RunnerStateSchemaVersion,
		Service:                    req.Config.Service,
		Env:                        req.Config.Env,
		CurrentState:               StatePreparingArtifact,
		LastAcceptedRelease:        fetched.ReleaseManifest.ReleaseID,
		LastAcceptedReleaseCreated: fetched.ReleaseManifest.CreatedAt,
		ReleaseDigest:              fetched.Verification.Digest,
		RuntimeManifestDigest:      fetched.Verification.RuntimeManifestDigest,
		ControlKey:                 runtime.ControlKey,
		ReleaseKey:                 fetched.ReleaseKey,
		RuntimeManifestKey:         fetched.RuntimeManifestKey,
		TraceID:                    req.TraceID,
		UpdatedAt:                  canonical.Time(now()),
		Identity:                   &identity,
		Stateful:                   runtime,
		Verification:               fetched.Verification,
	}
	if err := stateStore.SaveState(ctx, localState); err != nil {
		return fail(CodeLocalStateWriteFailed, "write runner local state", err, nil, &identity, fetched.ReleaseManifest.ReleaseID)
	}
	result.ReleaseID = fetched.ReleaseManifest.ReleaseID
	result.ReleaseKey = fetched.ReleaseKey
	result.RuntimeKey = fetched.RuntimeManifestKey
	result.ControlKey = runtime.ControlKey
	result.Stateful = runtime
	result.LocalState = localState
	result.Verification = fetched.Verification
	if err := emit(StatePreparingArtifact, fetched.ReleaseManifest.ReleaseID, "stateful release verified; ready to prepare volume and artifact", nil, &identity); err != nil {
		return nil, err
	}
	return result, nil
}

func readStatefulMemberControl(ctx context.Context, store objstore.ObjectStore, key string) (*statefulMemberBootstrapDocument, error) {
	object, err := store.Get(ctx, key)
	if err != nil {
		code := CodeInvalidServiceControl
		summary := err.Error()
		if errors.Is(err, objstore.ErrNotFound) {
			code = CodeControlNotFound
			summary = "stateful member control object was not found"
		}
		return nil, &BootstrapError{Code: code, Summary: summary, Err: err}
	}
	var control schema.StatefulMemberControl
	if err := canonical.UnmarshalStrict(object.Body, &control); err != nil {
		return nil, &BootstrapError{Code: CodeInvalidServiceControl, Summary: err.Error(), Err: err}
	}
	if control.SchemaVersion != schema.Version {
		return nil, &BootstrapError{Code: CodeInvalidServiceControl, Summary: fmt.Sprintf("stateful member control schema version %q is not supported", control.SchemaVersion)}
	}
	return &statefulMemberBootstrapDocument{Key: key, ETag: object.ETag, Control: control}, nil
}

type statefulMemberBootstrapDocument struct {
	Key     string
	ETag    string
	Control schema.StatefulMemberControl
}

func statefulControlKey(cfg config.Config) string {
	if strings.TrimSpace(cfg.ControlKey) != "" {
		return cfg.ControlKey
	}
	key, err := paths.StatefulMemberControl(cfg.StatefulGroup, cfg.StatefulMember)
	if err != nil {
		return cfg.ControlKey
	}
	return key
}

func statefulRuntimeFromControl(doc *statefulMemberBootstrapDocument, cfg config.Config, identity Identity, now time.Time) (*StatefulRuntime, error) {
	if doc == nil {
		return nil, errors.New("stateful member control is required")
	}
	control := doc.Control
	if control.Group != cfg.StatefulGroup {
		return nil, fmt.Errorf("stateful member group %q does not match runner group %q", control.Group, cfg.StatefulGroup)
	}
	if control.Env != cfg.Env {
		return nil, fmt.Errorf("stateful member env %q does not match runner env %q", control.Env, cfg.Env)
	}
	if control.Member != cfg.StatefulMember {
		return nil, fmt.Errorf("stateful member ordinal %d does not match runner member %d", control.Member, cfg.StatefulMember)
	}
	if cfg.StatefulGeneration > 0 && control.Generation != cfg.StatefulGeneration {
		return nil, fmt.Errorf("stateful member generation %d does not match runner generation %d", control.Generation, cfg.StatefulGeneration)
	}
	if control.Lease != nil {
		return nil, fmt.Errorf("stateful member %s/%d has an active lease held by %q", control.Group, control.Member, control.Lease.Owner)
	}
	if control.Phase != "" && control.Phase != state.StatefulMemberReady {
		return nil, fmt.Errorf("stateful member %s/%d is %q, not ready", control.Group, control.Member, control.Phase)
	}
	if control.VolumeID == "" {
		return nil, fmt.Errorf("stateful member %s/%d has no durable volume ID", control.Group, control.Member)
	}
	if control.InstanceID == "" && identity.InstanceID != "" {
		return nil, fmt.Errorf("stateful member %s/%d has no recorded instance identity", control.Group, control.Member)
	}
	if control.InstanceID != "" && identity.InstanceID != "" && control.InstanceID != identity.InstanceID {
		return nil, fmt.Errorf("stateful member instance %q does not match runner instance %q", control.InstanceID, identity.InstanceID)
	}
	if control.Zone != "" && identity.Zone != "" && control.Zone != identity.Zone {
		return nil, fmt.Errorf("stateful member zone %q does not match runner zone %q", control.Zone, identity.Zone)
	}
	stableHostname := firstNonEmpty(cfg.StatefulStableHostname, control.DNSName)
	if cfg.StatefulStableHostname != "" && control.DNSName != "" && cfg.StatefulStableHostname != control.DNSName {
		return nil, fmt.Errorf("stateful stable hostname %q does not match member DNS name %q", cfg.StatefulStableHostname, control.DNSName)
	}
	return &StatefulRuntime{
		Group:           control.Group,
		Env:             control.Env,
		Member:          control.Member,
		Generation:      control.Generation,
		InstanceID:      control.InstanceID,
		VolumeID:        control.VolumeID,
		VolumeMountPath: cfg.StatefulVolumeMountPath,
		StableHostname:  stableHostname,
		Recipe:          cfg.StatefulRecipe,
		ControlKey:      doc.Key,
		ControlETag:     doc.ETag,
		ControlVersion:  control.Version,
		LastValidatedAt: canonical.Time(now),
	}, nil
}

func checkStatefulStaleRelease(previous *LocalState, next schema.ReleaseManifest) error {
	if previous == nil || previous.LastAcceptedRelease == "" || previous.LastAcceptedRelease == next.ReleaseID {
		return nil
	}
	if previous.LastAcceptedReleaseCreated == "" || next.CreatedAt == "" {
		return nil
	}
	prev, err := time.Parse(time.RFC3339Nano, previous.LastAcceptedReleaseCreated)
	if err != nil {
		return nil
	}
	created, err := time.Parse(time.RFC3339Nano, next.CreatedAt)
	if err != nil {
		return nil
	}
	if created.Before(prev) {
		return fmt.Errorf("stateful release %s was created before local release %s", next.ReleaseID, previous.LastAcceptedRelease)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
