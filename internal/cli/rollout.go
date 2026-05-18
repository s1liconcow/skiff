package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type rolloutWatchOutput struct {
	OK      bool                   `json:"ok"`
	TraceID string                 `json:"trace_id,omitempty"`
	Status  provider.RolloutStatus `json:"status"`
}

var newRolloutProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
	return newCLIProvider(cfg, store)
}

func runRollout(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return writeSpecError(binary, "ROLLOUT_INVALID", root.Format, root.TraceID, errors.New("expected rollout command watch"), nil, stdout, stderr)
	}
	switch args[0] {
	case "watch":
		return runRolloutWatch(binary, args[1:], root, stdout, stderr)
	default:
		return writeSpecError(binary, "ROLLOUT_INVALID", root.Format, root.TraceID, fmt.Errorf("unknown rollout command %q", args[0]), nil, stdout, stderr)
	}
}

func runRolloutWatch(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" rollout watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	operationID := fs.String("operation", "", "operation ID")
	rolloutID := fs.String("rollout-id", "", "Skiff rollout ID")
	providerID := fs.String("provider-id", "", "provider rollout ID")

	if err := fs.Parse(args); err != nil {
		return writeSpecError(binary, "ROLLOUT_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	if fs.NArg() > 1 {
		return writeSpecError(binary, "ROLLOUT_INVALID", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", fs.Arg(1)), nil, stdout, stderr)
	}
	if fs.NArg() == 1 && *service == "" {
		*service = fs.Arg(0)
	}
	if *service == "" || *operationID == "" {
		return writeSpecError(binary, "ROLLOUT_INVALID", *flags.format, *flags.traceID, errors.New("--service and --operation are required"), nil, stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeSpecError(binary, "ROLLOUT_INVALID", *flags.format, *flags.traceID, errors.New("rollout watch currently requires --direct mode"), nil, stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "rollout", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	cloud, err := newRolloutProvider(loaded.Config, store)
	if err != nil {
		return writeSpecError(binary, "ROLLOUT_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	status, err := deploy.Deployer{Store: store, Provider: cloud}.WatchRollout(nilContext(), deploy.WatchRolloutRequest{
		Service:     *service,
		Env:         loaded.Config.Env,
		OperationID: *operationID,
		RolloutID:   *rolloutID,
		ProviderID:  *providerID,
		TraceID:     *flags.traceID,
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
	})
	if err != nil {
		return writeSpecError(binary, "ROLLOUT_FAILED", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		fmt.Fprintf(stdout, "rollout %s status: %s\n", status.RolloutID, status.Status)
		if status.ProviderID != "" {
			fmt.Fprintf(stdout, "provider_id: %s\n", status.ProviderID)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(rolloutWatchOutput{OK: true, TraceID: *flags.traceID, Status: *status}); err != nil {
			fmt.Fprintf(stderr, "%s rollout watch: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeSpecError(binary, "ROLLOUT_INVALID", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), nil, stdout, stderr)
	}
}
