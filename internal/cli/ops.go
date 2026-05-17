package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/client"
)

func runOps(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return writeClientCommandError(binary, "ops", root.Format, root.TraceID, errors.New("expected ops command watch"), stdout, stderr)
	}
	switch args[0] {
	case "watch":
		return runOpsWatch(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printOpsUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "ops", root.Format, root.TraceID, fmt.Errorf("unknown ops command %q", args[0]), stdout, stderr)
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

func splitOpsWatchArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"after":        true,
		"api-url":      true,
		"config":       true,
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
	fmt.Fprintf(w, "Usage: %s ops watch <operation> --service <service> [flags]\n", binary)
}
