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
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

type metricsProvider interface {
	Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error)
}

type metricsOutput struct {
	OK      bool                    `json:"ok"`
	TraceID string                  `json:"trace_id,omitempty"`
	Series  []provider.MetricSeries `json:"series"`
}

var newMetricsProviderForCLI = func(cfg config.Config) (metricsProvider, error) {
	return aws.NewFromConfig(cfg)
}

func runMetrics(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" metrics", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	releaseID := fs.String("release", "", "release ID filter")
	instanceID := fs.String("instance", "", "instance ID filter")
	metricNames := fs.String("metric", "", "comma-separated metric names")
	sinceValue := fs.String("since", "", "duration like 15m or RFC3339 start timestamp")
	fromValue := fs.String("from", "", "RFC3339 start timestamp")
	toValue := fs.String("to", "", "RFC3339 end timestamp")
	period := fs.Int("period", 60, "metric period in seconds")

	flagArgs, positionals, err := splitMetricsArgs(args)
	if err != nil {
		return writeSpecError(binary, "METRICS_INVALID", root.Format, root.TraceID, err, nil, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeSpecError(binary, "METRICS_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeSpecError(binary, "METRICS_INVALID", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), nil, stdout, stderr)
	}
	if *service == "" && len(positionals) == 1 {
		*service = positionals[0]
	}
	if *service == "" {
		return writeSpecError(binary, "METRICS_INVALID", *flags.format, *flags.traceID, errors.New("service is required"), nil, stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeSpecError(binary, "METRICS_INVALID", *flags.format, *flags.traceID, errors.New("metrics currently requires --direct mode"), nil, stdout, stderr)
	}
	from, to, err := parseMetricWindow(*sinceValue, *fromValue, *toValue)
	if err != nil {
		return writeSpecError(binary, "METRICS_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	metricProvider, err := newMetricsProviderForCLI(loaded.Config)
	if err != nil {
		return writeSpecError(binary, "METRICS_INVALID", *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	result, err := metricProvider.Metrics(nilContext(), provider.MetricsRequest{
		Service:       *service,
		Env:           loaded.Config.Env,
		ReleaseID:     *releaseID,
		InstanceID:    *instanceID,
		Names:         splitMetricNames(*metricNames),
		From:          from,
		To:            to,
		PeriodSeconds: *period,
	})
	if err != nil {
		return writeMetricsError(binary, *flags.format, *flags.traceID, *service, err, stdout, stderr)
	}
	return writeMetricsResult(binary, *flags.format, *flags.traceID, result.Series, stdout, stderr)
}

func writeMetricsResult(binary, format, traceID string, series []provider.MetricSeries, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		for _, item := range series {
			fmt.Fprintf(stdout, "%s", item.Name)
			if item.Source != "" {
				fmt.Fprintf(stdout, " %s", item.Source)
			}
			if item.Unit != "" {
				fmt.Fprintf(stdout, " (%s)", item.Unit)
			}
			fmt.Fprintln(stdout)
			for _, point := range item.Points {
				fmt.Fprintf(stdout, "  %s %.6g\n", point.Timestamp.Format(time.RFC3339Nano), point.Value)
			}
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(metricsOutput{OK: true, TraceID: traceID, Series: series}); err != nil {
			fmt.Fprintf(stderr, "%s metrics: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeSpecError(binary, "METRICS_INVALID", format, traceID, errors.New(`unsupported format; expected "human" or "json"`), nil, stdout, stderr)
	}
}

func writeMetricsError(binary, format, traceID, service string, err error, stdout, stderr io.Writer) int {
	code := "METRICS_FAILED"
	summary := err.Error()
	exitCode := ExitProviderError
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		code = string(providerErr.Code)
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
				{ID: "inspect_metrics", Command: binary + " metrics " + service + " --since 15m --format json", Mutating: false},
			},
		})
		return exitCode
	}
	fmt.Fprintf(stderr, "%s metrics: %v\n", binary, err)
	return exitCode
}

func parseMetricWindow(sinceValue, fromValue, toValue string) (time.Time, time.Time, error) {
	if sinceValue != "" && fromValue != "" {
		return time.Time{}, time.Time{}, errors.New("--since and --from cannot both be set")
	}
	var from time.Time
	var err error
	if sinceValue != "" {
		from, err = parseSince(sinceValue)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if fromValue != "" {
		from, err = time.Parse(time.RFC3339Nano, fromValue)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--from must be an RFC3339 timestamp")
		}
	}
	var to time.Time
	if toValue != "" {
		to, err = time.Parse(time.RFC3339Nano, toValue)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to must be an RFC3339 timestamp")
		}
	}
	return from, to, nil
}

func splitMetricNames(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func splitMetricsArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"env":          true,
		"format":       true,
		"from":         true,
		"instance":     true,
		"metric":       true,
		"mode":         true,
		"period":       true,
		"provider":     true,
		"region":       true,
		"release":      true,
		"service":      true,
		"since":        true,
		"state":        true,
		"state-bucket": true,
		"to":           true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}
