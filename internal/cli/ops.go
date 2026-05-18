package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	opsstate "github.com/s1liconcow/skiff/internal/ops"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type opsListOutput struct {
	OK         bool               `json:"ok"`
	TraceID    string             `json:"trace_id,omitempty"`
	Operations []opsstate.Summary `json:"operations"`
}

type opsInspectOutput struct {
	OK      bool                   `json:"ok"`
	TraceID string                 `json:"trace_id,omitempty"`
	Result  opsstate.InspectResult `json:"result"`
}

type opsResumeOutput struct {
	OK      bool                  `json:"ok"`
	TraceID string                `json:"trace_id,omitempty"`
	Result  opsstate.ResumeResult `json:"result"`
}

type opsRuntimeErrorOutput struct {
	OK                 bool                   `json:"ok"`
	Code               string                 `json:"code"`
	Summary            string                 `json:"summary"`
	TraceID            string                 `json:"trace_id,omitempty"`
	Result             *opsstate.ResumeResult `json:"result,omitempty"`
	RecommendedActions []recommendedAction    `json:"recommended_actions,omitempty"`
}

var (
	openOpsObjectStore = client.OpenObjectStore
	newOpsProvider     = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		opts := []aws.Option{}
		if store != nil {
			opts = append(opts, aws.WithStateStore(store))
		}
		return aws.NewFromConfig(cfg, opts...)
	}
)

func runOps(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return writeClientCommandError(binary, "ops", root.Format, root.TraceID, errors.New("expected ops command list, inspect, resume, or watch"), stdout, stderr)
	}
	switch args[0] {
	case "list":
		return runOpsList(binary, args[1:], root, stdout, stderr)
	case "inspect":
		return runOpsInspect(binary, args[1:], root, stdout, stderr)
	case "resume":
		return runOpsResume(binary, args[1:], root, stdout, stderr)
	case "watch":
		return runOpsWatch(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printOpsUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "ops", root.Format, root.TraceID, fmt.Errorf("unknown ops command %q", args[0]), stdout, stderr)
	}
}

func runOpsList(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" ops list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	all := fs.Bool("all", false, "include terminal operations")
	limit := fs.Int("limit", 0, "maximum operations to return")

	flagArgs, positionals, err := splitOpsArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "ops", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 0 {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[0]), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	store, loaded, exit := loadOpsStore(binary, root, fs, flags, stdout, stderr)
	if exit != ExitSuccess {
		return exit
	}
	items, err := opsstate.NewStore(store).List(nilContext(), opsstate.ListOptions{Service: *service, IncludeTerminal: *all, Limit: *limit})
	if err != nil {
		return writeClientError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	_ = loaded
	switch *flags.format {
	case "human", "text":
		for _, item := range items {
			fmt.Fprintf(stdout, "%s %s status=%s", item.Service, item.OperationID, item.Status)
			if item.Kind != "" {
				fmt.Fprintf(stdout, " kind=%s", item.Kind)
			}
			if item.UpdatedAt != "" {
				fmt.Fprintf(stdout, " updated=%s", item.UpdatedAt)
			}
			if item.Lease != nil {
				fmt.Fprintf(stdout, " lease=%s until %s", item.Lease.Owner, item.Lease.ExpiresAt)
			}
			fmt.Fprintln(stdout)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(opsListOutput{OK: true, TraceID: *flags.traceID, Operations: items}); err != nil {
			fmt.Fprintf(stderr, "%s ops list: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runOpsInspect(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" ops inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	operation := fs.String("operation", "", "operation ID")

	flagArgs, positionals, err := splitOpsArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "ops", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *operation == "" {
		*operation = positionals[0]
	}
	if *operation == "" || *service == "" {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New("operation ID and --service are required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	store, _, exit := loadOpsStore(binary, root, fs, flags, stdout, stderr)
	if exit != ExitSuccess {
		return exit
	}
	result, err := opsstate.NewStore(store).Inspect(nilContext(), *service, *operation)
	if err != nil {
		return writeClientError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		fmt.Fprintf(stdout, "operation: %s\n", result.OperationID)
		fmt.Fprintf(stdout, "service: %s\n", result.Service)
		fmt.Fprintf(stdout, "status: %s\n", result.Status)
		if result.Kind != "" {
			fmt.Fprintf(stdout, "kind: %s\n", result.Kind)
		}
		if len(result.ProviderOperations) > 0 {
			fmt.Fprintf(stdout, "provider_operations: %d\n", len(result.ProviderOperations))
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(opsInspectOutput{OK: true, TraceID: *flags.traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s ops inspect: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runOpsResume(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" ops resume", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	operation := fs.String("operation", "", "operation ID")
	leaseDuration := fs.Duration("lease-duration", 30*time.Second, "operation lease duration")

	flagArgs, positionals, err := splitOpsArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "ops", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *operation == "" {
		*operation = positionals[0]
	}
	if *operation == "" || *service == "" {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New("operation ID and --service are required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	store, loaded, exit := loadOpsStore(binary, root, fs, flags, stdout, stderr)
	if exit != ExitSuccess {
		return exit
	}
	cloud, err := newOpsProvider(loaded.Config, store)
	if err != nil {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := (opsstate.Resumer{Store: store, Provider: cloud}).Resume(nilContext(), opsstate.ResumeRequest{
		Service:       *service,
		OperationID:   *operation,
		Actor:         schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:       *flags.traceID,
		Owner:         "skiff-cli",
		LeaseDuration: *leaseDuration,
	})
	if err != nil {
		return writeOpsRuntimeError(binary, *flags.format, *flags.traceID, err, result, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		if result.Resumed {
			fmt.Fprintf(stdout, "operation %s resumed\n", result.OperationID)
		} else {
			fmt.Fprintf(stdout, "operation %s status=%s\n", result.OperationID, result.Status)
		}
		if result.RolloutStatus != nil {
			fmt.Fprintf(stdout, "rollout: %s\n", result.RolloutStatus.Status)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(opsResumeOutput{OK: true, TraceID: *flags.traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s ops resume: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runOpsWatch(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" ops watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	operation := fs.String("operation", "", "operation ID")
	limit := fs.Int("limit", 0, "maximum replay events before watching")
	afterID := fs.String("after", "", "resume after event ID")

	flagArgs, positionals, err := splitOpsWatchArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "ops", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *operation == "" {
		*operation = positionals[0]
	}
	if *operation == "" || *service == "" {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New("operation ID and --service are required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := client.New(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return runEventsWatch(eventsWatchContext(), binary, skiffClient, client.EventWatchOptions{
		EventOptions: client.EventOptions{
			Scope:     "operation",
			Service:   *service,
			Operation: *operation,
			Limit:     *limit,
			TraceID:   *flags.traceID,
		},
		AfterID:      *afterID,
		PollInterval: eventsWatchPollInterval,
	}, *flags.format, *flags.traceID, stdout, stderr)
}

func loadOpsStore(binary string, root rootOptions, fs *flag.FlagSet, flags clientFlagSet, stdout, stderr io.Writer) (objstore.ObjectStore, config.Loaded, int) {
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return nil, loaded, writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return nil, loaded, writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New("ops list, inspect, and resume currently require --direct mode"), stdout, stderr)
	}
	store, err := openOpsObjectStore(loaded.Config)
	if err != nil {
		return nil, loaded, writeClientError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return store, loaded, ExitSuccess
}

func writeOpsRuntimeError(binary, format, traceID string, err error, result *opsstate.ResumeResult, stdout, stderr io.Writer) int {
	code := "ROLLOUT_FAILED"
	exit := ExitRolloutFailed
	if errors.Is(err, state.ErrLeaseHeld) {
		code = "LEASE_HELD"
		exit = ExitUserError
	} else if errors.Is(err, state.ErrPreconditionFailed) {
		code = "PRECONDITION_FAILED"
		exit = ExitUserError
	}
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(opsRuntimeErrorOutput{
			OK:      false,
			Code:    code,
			Summary: err.Error(),
			TraceID: traceID,
			Result:  result,
			RecommendedActions: []recommendedAction{
				{ID: "inspect_operation", Command: binary + " ops inspect <operation> --service <service> --format json", Mutating: false},
			},
		})
		return exit
	}
	fmt.Fprintf(stderr, "%s ops resume: %v\n", binary, err)
	return exit
}

func splitOpsArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":        true,
		"config":         true,
		"context":        true,
		"env":            true,
		"format":         true,
		"lease-duration": true,
		"limit":          true,
		"mode":           true,
		"operation":      true,
		"provider":       true,
		"region":         true,
		"service":        true,
		"state":          true,
		"state-bucket":   true,
		"trace-id":       true,
	}
	return splitArgs(args, valueFlags)
}

func splitOpsWatchArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"after":        true,
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"limit":        true,
		"mode":         true,
		"operation":    true,
		"provider":     true,
		"region":       true,
		"service":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func printOpsUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s ops <list|inspect|resume|watch> [flags]\n", binary)
}
