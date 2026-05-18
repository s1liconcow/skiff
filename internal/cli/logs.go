package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	skifferrors "github.com/s1liconcow/skiff/internal/errors"
	"github.com/s1liconcow/skiff/internal/provider"
)

type logsProvider interface {
	Logs(ctx context.Context, req provider.LogsRequest) (*provider.LogsResult, error)
}

type logsOutput struct {
	OK      bool                `json:"ok"`
	TraceID string              `json:"trace_id,omitempty"`
	Entries []provider.LogEntry `json:"entries"`
}

var (
	newLogsProvider = func(cfg config.Config) (logsProvider, error) {
		return newCLIProviderNoStore(cfg)
	}
	logsContext        = nilContext
	logsFollowInterval = 2 * time.Second
)

func runLogs(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	releaseID := fs.String("release", "", "release ID filter")
	instanceID := fs.String("instance", "", "instance ID filter")
	sinceValue := fs.String("since", "", "duration like 20m or RFC3339 timestamp")
	limit := fs.Int("limit", 100, "maximum log entries")
	follow := fs.Bool("follow", false, "follow log output")

	flagArgs, positionals, err := splitLogsArgs(args)
	if err != nil {
		return writeSpecError(binary, "LOGS_INVALID", root.Format, root.TraceID, err, nil, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeSpecError(binary, "LOGS_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeSpecError(binary, "LOGS_INVALID", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), nil, stdout, stderr)
	}
	if *service == "" && len(positionals) == 1 {
		*service = positionals[0]
	}
	if *service == "" {
		return writeSpecError(binary, "LOGS_INVALID", *flags.format, *flags.traceID, errors.New("service is required"), nil, stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeSpecError(binary, "LOGS_INVALID", *flags.format, *flags.traceID, errors.New("logs currently requires --direct mode"), nil, stdout, stderr)
	}
	since, err := parseSince(*sinceValue)
	if err != nil {
		return writeSpecError(binary, "LOGS_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	logProvider, err := newLogsProvider(loaded.Config)
	if err != nil {
		return writeSpecError(binary, "LOGS_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	return runLogsQuery(logsContext(), logProvider, provider.LogsRequest{
		Service:    *service,
		Env:        loaded.Config.Env,
		ReleaseID:  *releaseID,
		InstanceID: *instanceID,
		Since:      since,
		Limit:      *limit,
	}, *follow, binary, *flags.format, *flags.traceID, stdout, stderr)
}

func runLogsQuery(ctx context.Context, logProvider logsProvider, req provider.LogsRequest, follow bool, binary, format, traceID string, stdout, stderr io.Writer) int {
	if !follow {
		result, err := logProvider.Logs(ctx, req)
		if err != nil {
			return writeLogsError(binary, format, traceID, req.Service, err, stdout, stderr)
		}
		return writeLogsResult(binary, format, traceID, result.Entries, stdout, stderr)
	}

	result, err := logProvider.Logs(ctx, req)
	if err != nil {
		return writeLogsError(binary, format, traceID, req.Service, err, stdout, stderr)
	}
	entries := result.Entries
	advanceLogsCursor(&req, entries)
	if req.Since.IsZero() {
		req.Since = time.Now().UTC()
	}

	switch format {
	case "human", "text":
		writeHumanLogEntries(stdout, entries)
		for waitForNextLogPoll(ctx) {
			result, err := logProvider.Logs(ctx, req)
			if err != nil {
				return writeLogsError(binary, format, traceID, req.Service, err, stdout, stderr)
			}
			writeHumanLogEntries(stdout, result.Entries)
			advanceLogsCursor(&req, result.Entries)
		}
		return ExitSuccess
	case "json":
		if err := writeFollowJSONLogs(ctx, logProvider, req, traceID, entries, stdout); err != nil {
			fmt.Fprintf(stderr, "%s logs: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeSpecError(binary, "LOGS_INVALID", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), nil, stdout, stderr)
	}
}

func writeLogsResult(binary, format, traceID string, entries []provider.LogEntry, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		writeHumanLogEntries(stdout, entries)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(logsOutput{OK: true, TraceID: traceID, Entries: entries}); err != nil {
			fmt.Fprintf(stderr, "%s logs: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeSpecError(binary, "LOGS_INVALID", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), nil, stdout, stderr)
	}
}

func writeFollowJSONLogs(ctx context.Context, logProvider logsProvider, req provider.LogsRequest, traceID string, firstEntries []provider.LogEntry, stdout io.Writer) error {
	fmt.Fprint(stdout, `{"ok":true`)
	if traceID != "" {
		body, err := json.Marshal(traceID)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, `,"trace_id":%s`, body)
	}
	fmt.Fprint(stdout, `,"entries":[`)
	first := true
	writeEntry := func(entry provider.LogEntry) error {
		body, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if !first {
			fmt.Fprint(stdout, ",")
		}
		first = false
		_, err = stdout.Write(body)
		return err
	}
	for _, entry := range firstEntries {
		if err := writeEntry(entry); err != nil {
			return err
		}
	}
	for waitForNextLogPoll(ctx) {
		result, err := logProvider.Logs(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return err
		}
		for _, entry := range result.Entries {
			if err := writeEntry(entry); err != nil {
				return err
			}
		}
		advanceLogsCursor(&req, result.Entries)
	}
	fmt.Fprint(stdout, "]}\n")
	return nil
}

func waitForNextLogPoll(ctx context.Context) bool {
	interval := logsFollowInterval
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func advanceLogsCursor(req *provider.LogsRequest, entries []provider.LogEntry) {
	for _, entry := range entries {
		if entry.Timestamp.After(req.Since) || req.Since.IsZero() {
			req.Since = entry.Timestamp.UTC().Add(time.Nanosecond)
		}
	}
}

func writeHumanLogEntries(stdout io.Writer, entries []provider.LogEntry) {
	for _, entry := range entries {
		source := strings.TrimSpace(entry.Source)
		if source == "" {
			fmt.Fprintf(stdout, "%s %s\n", entry.Timestamp.Format(time.RFC3339Nano), entry.Message)
			continue
		}
		fmt.Fprintf(stdout, "%s %s %s\n", entry.Timestamp.Format(time.RFC3339Nano), source, entry.Message)
	}
}

func writeLogsError(binary, format, traceID, service string, err error, stdout, stderr io.Writer) int {
	code := string(skifferrors.ObservabilityUnavailable)
	summary := err.Error()
	exitCode := ExitProviderError
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		code = string(skifferrors.FromProviderCode(providerErr.Code, true))
		if providerErr.Summary != "" {
			summary = providerErr.Summary
		}
		if providerErr.Code == provider.CodeValidation {
			exitCode = ExitUserError
		}
	}
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(commandErrorOutput{
			OK:      false,
			Code:    code,
			Summary: summary,
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{ID: "inspect_status", Command: binary + " status " + service + " --format json", Mutating: false},
				{ID: "inspect_recent_logs", Command: binary + " logs " + service + " --since 20m --format json", Mutating: false},
			},
		})
		return exitCode
	}
	fmt.Fprintf(stderr, "%s logs: %v\n", binary, err)
	return exitCode
}

func splitLogsArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"instance":     true,
		"limit":        true,
		"mode":         true,
		"provider":     true,
		"region":       true,
		"release":      true,
		"service":      true,
		"since":        true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func parseSince(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if duration, err := time.ParseDuration(value); err == nil {
		return time.Now().UTC().Add(-duration), nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since must be a duration or RFC3339 timestamp")
	}
	return t, nil
}
