package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/internal/stateful"
)

type StatefulVolumeRequest struct {
	Runtime StatefulRuntime `json:"runtime"`
}

type StatefulVolumeResult struct {
	VolumeID  string `json:"volume_id"`
	MountPath string `json:"mount_path"`
}

type StatefulVolumeManager interface {
	PrepareStatefulVolume(ctx context.Context, req StatefulVolumeRequest) (*StatefulVolumeResult, error)
}

type HostStatefulVolumeManager struct{}

func (HostStatefulVolumeManager) PrepareStatefulVolume(ctx context.Context, req StatefulVolumeRequest) (*StatefulVolumeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Runtime.VolumeID == "" {
		return nil, errors.New("stateful volume ID is required")
	}
	if req.Runtime.VolumeMountPath == "" {
		return nil, errors.New("stateful volume mount path is required")
	}
	if err := os.MkdirAll(req.Runtime.VolumeMountPath, 0o750); err != nil {
		return nil, err
	}
	info, err := os.Stat(req.Runtime.VolumeMountPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("stateful volume mount path %q is not a directory", req.Runtime.VolumeMountPath)
	}
	return &StatefulVolumeResult{VolumeID: req.Runtime.VolumeID, MountPath: req.Runtime.VolumeMountPath}, nil
}

type StatefulRecipeAction string

const (
	StatefulRecipeStart   StatefulRecipeAction = "start"
	StatefulRecipeStop    StatefulRecipeAction = "stop"
	StatefulRecipeHealth  StatefulRecipeAction = "health"
	StatefulRecipeBackup  StatefulRecipeAction = "backup"
	StatefulRecipeRestore StatefulRecipeAction = "restore"
)

func RunStatefulRecipeHook(ctx context.Context, recipe stateful.Recipe, runtime StatefulRuntime, action StatefulRecipeAction, operationID, traceID string) (*stateful.RecipeResult, error) {
	if recipe == nil {
		if runtime.Recipe != "" {
			return nil, fmt.Errorf("stateful recipe %q is configured but no recipe runner is available", runtime.Recipe)
		}
		return nil, nil
	}
	if err := validateRecipeName(recipe, runtime); err != nil {
		return nil, err
	}
	req := statefulRecipeRequest(runtime, operationID, traceID)
	var (
		result *stateful.RecipeResult
		err    error
	)
	switch action {
	case StatefulRecipeStart:
		result, err = recipe.Start(ctx, req)
	case StatefulRecipeStop:
		result, err = recipe.Stop(ctx, req)
	case StatefulRecipeHealth:
		result, err = recipe.Health(ctx, req)
	case StatefulRecipeBackup:
		result, err = recipe.Backup(ctx, req)
	case StatefulRecipeRestore:
		result, err = recipe.Restore(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported stateful recipe action %q", action)
	}
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("stateful recipe %s hook returned no result", action)
	}
	if !result.OK {
		summary := result.Summary
		if summary == "" {
			summary = fmt.Sprintf("stateful recipe %s hook reported not ok", action)
		}
		return result, errors.New(summary)
	}
	return result, nil
}

func DetectStatefulRecipeRole(ctx context.Context, recipe stateful.Recipe, runtime StatefulRuntime, operationID, traceID string) (*stateful.RoleResult, error) {
	if recipe == nil {
		if runtime.Recipe != "" {
			return nil, fmt.Errorf("stateful recipe %q is configured but no recipe runner is available", runtime.Recipe)
		}
		return nil, nil
	}
	if err := validateRecipeName(recipe, runtime); err != nil {
		return nil, err
	}
	result, err := recipe.DetectRole(ctx, statefulRecipeRequest(runtime, operationID, traceID))
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("stateful recipe role detection returned no result")
	}
	return result, nil
}

func currentStatefulRuntime(ctx context.Context, current LocalState, requested *StatefulRuntime, store objstore.ObjectStore, identity *Identity, now func() time.Time) (*StatefulRuntime, error) {
	runtime := requested
	if runtime == nil {
		runtime = current.Stateful
	}
	if runtime == nil {
		return nil, nil
	}
	if store == nil {
		return nil, errors.New("stateful runner requires an object store to validate member control")
	}
	if identity == nil {
		identity = current.Identity
	}
	key := runtime.ControlKey
	if key == "" {
		var err error
		key, err = paths.StatefulMemberControl(runtime.Group, runtime.Member)
		if err != nil {
			return nil, err
		}
	}
	doc, err := readStatefulMemberControl(ctx, store, key)
	if err != nil {
		return nil, err
	}
	return statefulRuntimeFromExistingControl(doc, *runtime, identity, now())
}

func statefulRuntimeFromExistingControl(doc *statefulMemberBootstrapDocument, runtime StatefulRuntime, identity *Identity, now time.Time) (*StatefulRuntime, error) {
	if doc == nil {
		return nil, errors.New("stateful member control is required")
	}
	control := doc.Control
	if runtime.Group != "" && control.Group != runtime.Group {
		return nil, fmt.Errorf("stateful member group %q does not match runtime group %q", control.Group, runtime.Group)
	}
	if runtime.Env != "" && control.Env != runtime.Env {
		return nil, fmt.Errorf("stateful member env %q does not match runtime env %q", control.Env, runtime.Env)
	}
	if control.Member != runtime.Member {
		return nil, fmt.Errorf("stateful member ordinal %d does not match runtime member %d", control.Member, runtime.Member)
	}
	if runtime.Generation > 0 && control.Generation != runtime.Generation {
		return nil, fmt.Errorf("stateful member generation %d does not match runtime generation %d", control.Generation, runtime.Generation)
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
	if runtime.VolumeID != "" && control.VolumeID != runtime.VolumeID {
		return nil, fmt.Errorf("stateful member volume %q does not match runtime volume %q", control.VolumeID, runtime.VolumeID)
	}
	if control.InstanceID == "" && identity != nil && identity.InstanceID != "" {
		return nil, fmt.Errorf("stateful member %s/%d has no recorded instance identity", control.Group, control.Member)
	}
	if identity != nil && identity.InstanceID != "" && control.InstanceID != "" && control.InstanceID != identity.InstanceID {
		return nil, fmt.Errorf("stateful member instance %q does not match runner instance %q", control.InstanceID, identity.InstanceID)
	}
	if identity != nil && identity.InstanceID != "" && runtime.InstanceID != "" && runtime.InstanceID != identity.InstanceID {
		return nil, fmt.Errorf("stateful runtime instance %q does not match runner instance %q", runtime.InstanceID, identity.InstanceID)
	}
	if runtime.InstanceID != "" && control.InstanceID != "" && control.InstanceID != runtime.InstanceID {
		return nil, fmt.Errorf("stateful member instance %q does not match runtime instance %q", control.InstanceID, runtime.InstanceID)
	}
	if identity != nil && identity.Zone != "" && control.Zone != "" && control.Zone != identity.Zone {
		return nil, fmt.Errorf("stateful member zone %q does not match runner zone %q", control.Zone, identity.Zone)
	}
	if runtime.StableHostname != "" && control.DNSName != "" && runtime.StableHostname != control.DNSName {
		return nil, fmt.Errorf("stateful stable hostname %q does not match member DNS name %q", runtime.StableHostname, control.DNSName)
	}
	runtime.Group = control.Group
	runtime.Env = control.Env
	runtime.Generation = control.Generation
	runtime.InstanceID = firstNonEmpty(control.InstanceID, runtime.InstanceID)
	runtime.VolumeID = control.VolumeID
	runtime.StableHostname = firstNonEmpty(runtime.StableHostname, control.DNSName)
	runtime.ControlKey = doc.Key
	runtime.ControlETag = doc.ETag
	runtime.ControlVersion = control.Version
	runtime.LastValidatedAt = canonical.Time(now)
	return &runtime, nil
}

func statefulEnvVars(env map[string]string, runtime StatefulRuntime) map[string]string {
	out := cloneStringMap(env)
	if out == nil {
		out = map[string]string{}
	}
	out["SKIFF_STATEFUL_GROUP"] = runtime.Group
	out["SKIFF_STATEFUL_ENV"] = runtime.Env
	out["SKIFF_STATEFUL_MEMBER"] = fmt.Sprintf("%d", runtime.Member)
	out["SKIFF_STATEFUL_GENERATION"] = fmt.Sprintf("%d", runtime.Generation)
	out["SKIFF_STATEFUL_VOLUME_ID"] = runtime.VolumeID
	if runtime.VolumeMountPath != "" {
		out["SKIFF_STATEFUL_VOLUME_MOUNT_PATH"] = runtime.VolumeMountPath
	}
	if runtime.StableHostname != "" {
		out["SKIFF_STATEFUL_STABLE_HOSTNAME"] = runtime.StableHostname
	}
	if runtime.Recipe != "" {
		out["SKIFF_STATEFUL_RECIPE"] = runtime.Recipe
	}
	return out
}

func runStatefulRecipeStart(ctx context.Context, recipe stateful.Recipe, runtime StatefulRuntime, operationID, traceID string) error {
	_, err := RunStatefulRecipeHook(ctx, recipe, runtime, StatefulRecipeStart, operationID, traceID)
	return err
}

func runStatefulRecipeStop(ctx context.Context, recipe stateful.Recipe, runtime StatefulRuntime, operationID, traceID string) error {
	_, err := RunStatefulRecipeHook(ctx, recipe, runtime, StatefulRecipeStop, operationID, traceID)
	return err
}

func runStatefulRecipeHealth(ctx context.Context, recipe stateful.Recipe, runtime StatefulRuntime, operationID, traceID string) error {
	_, err := RunStatefulRecipeHook(ctx, recipe, runtime, StatefulRecipeHealth, operationID, traceID)
	return err
}

func validateRecipeName(recipe stateful.Recipe, runtime StatefulRuntime) error {
	if runtime.Recipe == "" || recipe.Name() == "" || recipe.Name() == runtime.Recipe {
		return nil
	}
	return fmt.Errorf("stateful recipe %q is configured but runner has recipe %q", runtime.Recipe, recipe.Name())
}

func statefulRecipeRequest(runtime StatefulRuntime, operationID, traceID string) stateful.RecipeRequest {
	control := schema.StatefulMemberControl{
		SchemaVersion: schema.Version,
		Group:         runtime.Group,
		Env:           runtime.Env,
		Member:        runtime.Member,
		InstanceID:    runtime.InstanceID,
		VolumeID:      runtime.VolumeID,
		DNSName:       runtime.StableHostname,
		Generation:    runtime.Generation,
		Phase:         state.StatefulMemberReady,
		Version:       runtime.ControlVersion,
	}
	return stateful.RecipeRequest{
		Group:       runtime.Group,
		Env:         runtime.Env,
		Member:      runtime.Member,
		Generation:  runtime.Generation,
		InstanceID:  runtime.InstanceID,
		VolumeID:    runtime.VolumeID,
		DNSName:     runtime.StableHostname,
		Control:     control,
		OperationID: operationID,
		TraceID:     traceID,
	}
}
