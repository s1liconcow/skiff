package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/artifact"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	skifferrors "github.com/s1liconcow/skiff/internal/errors"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/runner"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

var (
	openRunnerObjectStore     = client.OpenObjectStore
	runRunnerBootstrapFn      = runner.Bootstrap
	runRunnerLifecycleFn      = runner.RunLifecycle
	newRunnerSystemdManager   = func() runner.SystemdManager { return runner.FileSystemdManager{} }
	newRunnerMetadataProvider = func(cfg config.Config) runner.MetadataProvider {
		if strings.TrimSpace(cfg.Provider) == "aws" {
			return runner.EC2MetadataProvider{}
		}
		return runner.StaticMetadataProvider{Value: runner.Identity{Provider: cfg.Provider, Region: cfg.Region}}
	}
)

type runnerBootstrapOutput struct {
	OK      bool                    `json:"ok"`
	TraceID string                  `json:"trace_id,omitempty"`
	Result  *runner.BootstrapResult `json:"result"`
}

type runnerRunOutput struct {
	OK      bool                    `json:"ok"`
	TraceID string                  `json:"trace_id,omitempty"`
	Result  *runner.LifecycleResult `json:"result"`
}

func runRunnerBootstrap(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" bootstrap", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	userData := fs.String("user-data", strings.TrimSpace(os.Getenv("SKIFF_RUNNER_USER_DATA")), "path to runner user-data JSON")
	statePath := fs.String("state-path", runner.DefaultStatePath, "path to runner local state JSON")
	eventsPath := fs.String("events-path", runner.DefaultEventsPath, "path to runner local event JSONL")

	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeRunnerError(binary, "bootstrap", *format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeRunnerError(binary, "bootstrap", *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}

	cfg, exit := loadRunnerUserData(binary, "bootstrap", *format, *traceID, *userData, stdout, stderr)
	if exit != ExitSuccess {
		return exit
	}
	store, err := openRunnerObjectStore(cfg)
	if err != nil {
		return writeRunnerError(binary, "bootstrap", *format, *traceID, err, stdout, stderr)
	}
	result, err := runRunnerBootstrapFn(context.Background(), runner.BootstrapRequest{
		Config:           cfg,
		Store:            store,
		MetadataProvider: newRunnerMetadataProvider(cfg),
		StateStore:       runner.FileStateStore{Path: *statePath},
		EventSink:        &runner.FileEventSink{Path: *eventsPath},
		TraceID:          *traceID,
		IdentityOptions:  runner.IdentityOptions{Attempts: 5, Backoff: time.Second},
	})
	if err != nil {
		return writeRunnerError(binary, "bootstrap", *format, *traceID, err, stdout, stderr)
	}
	if isJSONFormat(*format) {
		_ = writeJSON(stdout, *format, runnerBootstrapOutput{OK: true, TraceID: *traceID, Result: result})
		return ExitSuccess
	}
	fmt.Fprintf(stdout, "runner accepted %s/%s release %s\n", result.Env, result.Service, result.ReleaseID)
	return ExitSuccess
}

func runRunnerRun(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	userData := fs.String("user-data", strings.TrimSpace(os.Getenv("SKIFF_RUNNER_USER_DATA")), "path to runner user-data JSON")
	statePath := fs.String("state-path", runner.DefaultStatePath, "path to runner local state JSON")
	eventsPath := fs.String("events-path", runner.DefaultEventsPath, "path to runner local event JSONL")
	artifactRoot := fs.String("artifact-root", artifact.DefaultRootDir, "path to prepared workload artifact root")
	unitDir := fs.String("unit-dir", runner.DefaultUnitDir, "path to systemd unit directory")
	systemctl := fs.String("systemctl", runner.DefaultSystemctlPath, "path to systemctl")
	healthAttempts := fs.Int("health-attempts", 30, "maximum health check attempts before failing")
	healthInterval := fs.Duration("health-interval", 10*time.Second, "interval between health check attempts")

	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeRunnerError(binary, "run", *format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeRunnerError(binary, "run", *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}

	cfg, exit := loadRunnerUserData(binary, "run", *format, *traceID, *userData, stdout, stderr)
	if exit != ExitSuccess {
		return exit
	}
	store, err := openRunnerObjectStore(cfg)
	if err != nil {
		return writeRunnerError(binary, "run", *format, *traceID, err, stdout, stderr)
	}
	stateStore := runner.FileStateStore{Path: *statePath}
	localState, err := stateStore.LoadState(context.Background())
	if err != nil {
		return writeRunnerError(binary, "run", *format, *traceID, err, stdout, stderr)
	}
	releaseManifest, runtimeManifest, err := loadRunnerManifests(context.Background(), store, cfg, *localState)
	if err != nil {
		return writeRunnerError(binary, "run", *format, *traceID, err, stdout, stderr)
	}
	systemd := newRunnerSystemdManager()
	if fileSystemd, ok := systemd.(runner.FileSystemdManager); ok {
		fileSystemd.UnitDir = *unitDir
		fileSystemd.SystemctlPath = *systemctl
		systemd = fileSystemd
	}
	result, err := runRunnerLifecycleFn(context.Background(), runner.LifecycleRequest{
		RuntimeManifest:  runtimeManifest,
		Artifact:         releaseManifest.Artifact,
		ControlStore:     store,
		StateStore:       stateStore,
		EventSink:        &runner.FileEventSink{Path: *eventsPath},
		Systemd:          systemd,
		ArtifactPreparer: runner.WorkloadArtifactPreparer{RootDir: *artifactRoot},
		TraceID:          *traceID,
		Identity:         localState.Identity,
		HealthAttempts:   *healthAttempts,
		HealthInterval:   *healthInterval,
	})
	if err != nil {
		return writeRunnerError(binary, "run", *format, *traceID, err, stdout, stderr)
	}
	if isJSONFormat(*format) {
		_ = writeJSON(stdout, *format, runnerRunOutput{OK: true, TraceID: *traceID, Result: result})
		return ExitSuccess
	}
	fmt.Fprintf(stdout, "runner started %s/%s release %s as %s\n", runtimeManifest.Env, runtimeManifest.Service, runtimeManifest.ReleaseID, result.UnitName)
	return ExitSuccess
}

func loadRunnerUserData(binary, command, format, traceID, path string, stdout, stderr io.Writer) (config.Config, int) {
	if strings.TrimSpace(path) == "" {
		err := errors.New("--user-data is required or SKIFF_RUNNER_USER_DATA must be set")
		return config.Config{}, writeRunnerError(binary, command, format, traceID, err, stdout, stderr)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return config.Config{}, writeRunnerError(binary, command, format, traceID, err, stdout, stderr)
	}
	cfg, err := runner.ParseUserData(body)
	if err != nil {
		return config.Config{}, writeRunnerError(binary, command, format, traceID, err, stdout, stderr)
	}
	return cfg, ExitSuccess
}

func loadRunnerManifests(ctx context.Context, store objstore.ObjectStore, cfg config.Config, localState runner.LocalState) (schema.ReleaseManifest, schema.RuntimeManifest, error) {
	releaseKey := strings.TrimSpace(localState.ReleaseKey)
	if releaseKey == "" {
		key, err := paths.ReleaseManifest(cfg.Service, localState.LastAcceptedRelease)
		if err != nil {
			return schema.ReleaseManifest{}, schema.RuntimeManifest{}, err
		}
		releaseKey = key
	}
	runtimeKey := strings.TrimSpace(localState.RuntimeManifestKey)
	if runtimeKey == "" {
		key, err := paths.RuntimeManifest(cfg.Service, localState.LastAcceptedRelease)
		if err != nil {
			return schema.ReleaseManifest{}, schema.RuntimeManifest{}, err
		}
		runtimeKey = key
	}

	releaseObj, err := store.Get(ctx, releaseKey)
	if err != nil {
		return schema.ReleaseManifest{}, schema.RuntimeManifest{}, err
	}
	var releaseManifest schema.ReleaseManifest
	if err := canonical.UnmarshalStrict(releaseObj.Body, &releaseManifest); err != nil {
		return schema.ReleaseManifest{}, schema.RuntimeManifest{}, fmt.Errorf("parse release manifest %s: %w", releaseKey, err)
	}
	runtimeObj, err := store.Get(ctx, runtimeKey)
	if err != nil {
		return schema.ReleaseManifest{}, schema.RuntimeManifest{}, err
	}
	var runtimeManifest schema.RuntimeManifest
	if err := canonical.UnmarshalStrict(runtimeObj.Body, &runtimeManifest); err != nil {
		return schema.ReleaseManifest{}, schema.RuntimeManifest{}, fmt.Errorf("parse runtime manifest %s: %w", runtimeKey, err)
	}
	return releaseManifest, runtimeManifest, nil
}

func writeRunnerError(binary, command, format, traceID string, err error, stdout, stderr io.Writer) int {
	code, summary, exitCode := runnerErrorDetails(err)
	if isJSONFormat(format) {
		_ = writeJSON(stdout, format, commandErrorOutput{
			OK:      false,
			Code:    code,
			Summary: summary,
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{ID: "inspect_runner_config", Command: binary + " config show --user-data /etc/skiff/runner.json --format json", Mutating: false},
			},
		})
		return exitCode
	}
	fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
	return exitCode
}

func runnerErrorDetails(err error) (string, string, int) {
	if err == nil {
		return string(skifferrors.InternalError), "unknown runner error", ExitInternalError
	}
	var bootErr *runner.BootstrapError
	if errors.As(err, &bootErr) {
		exitCode := ExitRolloutFailed
		if bootErr.Code == runner.CodeBootstrapInvalid {
			exitCode = ExitUserError
		}
		return bootErr.Code, bootErr.Summary, exitCode
	}
	var lifecycleErr *runner.LifecycleError
	if errors.As(err, &lifecycleErr) {
		exitCode := ExitRolloutFailed
		if lifecycleErr.Code == runner.CodeLifecycleInvalid {
			exitCode = ExitUserError
		}
		return lifecycleErr.Code, lifecycleErr.Summary, exitCode
	}
	if errors.Is(err, runner.ErrStateNotFound) {
		return runner.CodeLocalStateReadFailed, "runner local state not found; run bootstrap first", ExitRolloutFailed
	}
	var clientErr *client.Error
	if errors.As(err, &clientErr) {
		return string(skifferrors.FromClientCode(clientErr.Code)), clientErr.Summary, clientErr.ExitCode
	}
	return string(skifferrors.InternalError), err.Error(), ExitInternalError
}
