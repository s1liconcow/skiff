package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/drift"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

type driftOutput struct {
	OK      bool         `json:"ok"`
	TraceID string       `json:"trace_id,omitempty"`
	Result  drift.Result `json:"result"`
}

var newDriftProvider = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
	opts := []aws.Option{}
	if store != nil {
		opts = append(opts, aws.WithStateStore(store))
	}
	return aws.NewFromConfig(cfg, opts...)
}

func runDrift(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" drift", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	flagArgs, positionals, err := splitDriftArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "drift", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "drift", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "drift", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *service == "" && len(positionals) == 1 {
		*service = positionals[0]
	}
	if *service == "" {
		return writeClientCommandError(binary, "drift", *flags.format, *flags.traceID, errors.New("service is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "drift", *flags.format, *flags.traceID, errors.New("drift currently requires --direct mode"), stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "drift", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	cloud, err := newDriftProvider(loaded.Config, store)
	if err != nil {
		return writeClientCommandError(binary, "drift", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := drift.Detector{Store: store, Provider: cloud}.Detect(nilContext(), drift.Request{Service: *service, Env: loaded.Config.Env})
	if err != nil {
		return writeClientCommandError(binary, "drift", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		fmt.Fprintf(stdout, "drift for %s/%s:\n", result.Env, result.Service)
		for _, finding := range result.Findings {
			fmt.Fprintf(stdout, "- %s %s: %s\n", finding.Class, finding.Code, finding.Summary)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(driftOutput{OK: true, TraceID: *flags.traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s drift: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "drift", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func splitDriftArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"env":          true,
		"format":       true,
		"mode":         true,
		"provider":     true,
		"region":       true,
		"service":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}
