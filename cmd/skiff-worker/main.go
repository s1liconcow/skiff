package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/s1liconcow/skiff/internal/cli"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/saga/steps/builtin"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/internal/worker"
)

type workerOutput struct {
	OK      bool             `json:"ok"`
	TraceID string           `json:"trace_id,omitempty"`
	Result  worker.RunResult `json:"result"`
}

type workerErrorOutput struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Summary string `json:"summary"`
	TraceID string `json:"trace_id,omitempty"`
}

var (
	openWorkerObjectStore = client.OpenObjectStore
	newWorkerProvider     = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		opts := []aws.Option{}
		if store != nil {
			opts = append(opts, aws.WithStateStore(store))
		}
		return aws.NewFromConfig(cfg, opts...)
	}
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	args, defaultFormat, stdout := cli.PrepareJSONPrettyOutput(args, "human", false, stdout)
	defer cli.FlushJSONPrettyOutput(stdout)

	fs := flag.NewFlagSet("skiff-worker", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", defaultFormat, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	configPath := fs.String("config", "", "path to Skiff config file")
	contextName := fs.String("context", "", "Skiff config context name")
	env := fs.String("env", "", "Skiff environment name")
	providerName := fs.String("provider", "", "cloud provider name")
	region := fs.String("region", "", "cloud provider region")
	stateBucket := fs.String("state-bucket", "", "object-state bucket URI")
	stateAlias := fs.String("state", "", "alias for --state-bucket")
	mode := fs.String("mode", "", "worker config mode; must be direct")
	once := fs.Bool("once", false, "run one recovery scan and exit")
	interval := fs.Duration("interval", 10*time.Second, "poll interval")
	leaseDuration := fs.Duration("lease-duration", 30*time.Second, "control lease duration")
	workerID := fs.String("worker-id", defaultWorkerID(), "worker identity for leases and audit")

	if handled, err := cli.ParseCommandFlags(fs, args, stdout); handled {
		return cli.ExitSuccess
	} else if err != nil {
		return writeWorkerError(*format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeWorkerError(*format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	_ = noColor
	_ = yes

	overrides := map[string]string{}
	flagToValue := map[string]string{
		config.FieldEnv:         *env,
		config.FieldProvider:    *providerName,
		config.FieldRegion:      *region,
		config.FieldStateBucket: firstNonEmpty(*stateBucket, *stateAlias),
		config.FieldMode:        *mode,
	}
	for field, value := range flagToValue {
		if strings.TrimSpace(value) != "" {
			overrides[field] = value
		}
	}
	loaded, err := config.Load(config.LoadOptions{
		ModeDefault: config.ModeDirect,
		ConfigPath:  *configPath,
		Context:     *contextName,
		Overrides:   overrides,
	})
	if err == nil {
		err = config.Validate(loaded)
	}
	if err != nil {
		return writeWorkerError(*format, *traceID, err, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeWorkerError(*format, *traceID, fmt.Errorf("mode %q cannot run skiff-worker; use mode direct", loaded.Config.Mode), stdout, stderr)
	}
	store, err := openWorkerObjectStore(loaded.Config)
	if err != nil {
		return writeWorkerError(*format, *traceID, err, stdout, stderr)
	}
	cloud, err := newWorkerProvider(loaded.Config, store)
	if err != nil {
		return writeWorkerError(*format, *traceID, err, stdout, stderr)
	}
	w := worker.Worker{
		Store:         store,
		Provider:      cloud,
		SagaSteps:     builtin.New(builtin.Options{Store: store, Provider: cloud, Binary: "skiff-worker"}),
		Owner:         *workerID,
		Actor:         schema.Actor{ID: *workerID, Type: "agent"},
		LeaseDuration: *leaseDuration,
		PollInterval:  *interval,
	}
	if *once {
		result, err := w.RunOnce(context.Background())
		if err != nil {
			return writeWorkerError(*format, *traceID, err, stdout, stderr)
		}
		return writeWorkerResult(*format, *traceID, *result, stdout, stderr)
	}
	if cli.IsJSONFormat(*format) {
		if err := cli.WriteJSONOutput(stdout, *format, map[string]any{"ok": true, "trace_id": *traceID, "summary": "skiff-worker started"}); err != nil {
			return writeWorkerError(*format, *traceID, err, stdout, stderr)
		}
	} else {
		fmt.Fprintf(stdout, "skiff-worker %s started\n", *workerID)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return writeWorkerError(*format, *traceID, err, stdout, stderr)
	}
	return cli.ExitSuccess
}

func writeWorkerResult(format, traceID string, result worker.RunResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "operations resumed: %d\n", result.OperationResumed)
		fmt.Fprintf(stdout, "operations skipped: %d\n", result.OperationSkipped)
		fmt.Fprintf(stdout, "sagas resumed: %d\n", result.SagaResumed)
		fmt.Fprintf(stdout, "sagas skipped: %d\n", result.SagaSkipped)
		return cli.ExitSuccess
	case "json", "json-pretty":
		if err := cli.WriteJSONOutput(stdout, format, workerOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "skiff-worker: %v\n", err)
			return cli.ExitInternalError
		}
		return cli.ExitSuccess
	default:
		return writeWorkerError(format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeWorkerError(format, traceID string, err error, stdout, stderr io.Writer) int {
	if cli.IsJSONFormat(format) {
		_ = cli.WriteJSONOutput(stdout, format, workerErrorOutput{
			OK:      false,
			Code:    "WORKER_FAILED",
			Summary: err.Error(),
			TraceID: traceID,
		})
		return cli.ExitInternalError
	}
	fmt.Fprintf(stderr, "skiff-worker: %v\n", err)
	return cli.ExitInternalError
}

func defaultWorkerID() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "skiff-worker"
	}
	return "skiff-worker-" + name
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
