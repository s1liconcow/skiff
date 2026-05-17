package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/client"
)

type doctorOutput struct {
	OK      bool          `json:"ok"`
	TraceID string        `json:"trace_id,omitempty"`
	Doctor  client.Doctor `json:"doctor"`
}

var newDoctorClient = client.New

func runDoctor(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name to diagnose")
	fresh := fs.Bool("fresh", false, "bypass cached API views where supported")

	flagArgs, positionals, err := splitDoctorArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "doctor", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "doctor", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "doctor", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	if *service == "" {
		return writeClientCommandError(binary, "doctor", *flags.format, *flags.traceID, errors.New("service is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := newDoctorClient(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "doctor", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := skiffClient.Doctor(nilContext(), client.DoctorOptions{Service: *service, Fresh: *fresh, TraceID: *flags.traceID})
	if err != nil {
		return writeClientError(binary, "doctor", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		printDoctorHuman(stdout, *result)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(doctorOutput{OK: true, TraceID: *flags.traceID, Doctor: *result}); err != nil {
			fmt.Fprintf(stderr, "%s doctor: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "doctor", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func splitDoctorArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
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

func printDoctorHuman(w io.Writer, result client.Doctor) {
	fmt.Fprintf(w, "doctor: %s health=%s\n", firstNonEmptyCLI(result.Service, "all-services"), firstNonEmptyCLI(result.Health, "unknown"))
	if result.Env != "" {
		fmt.Fprintf(w, "env: %s\n", result.Env)
	}
	if result.Provider != "" || result.Region != "" {
		fmt.Fprintf(w, "provider: %s %s\n", result.Provider, result.Region)
	}
	if len(result.Findings) == 0 {
		fmt.Fprintln(w, "findings: none")
	} else {
		fmt.Fprintln(w, "findings:")
		for _, finding := range result.Findings {
			fmt.Fprintf(w, "- %s %s", finding.Severity, finding.Code)
			if finding.Service != "" {
				fmt.Fprintf(w, " %s", finding.Service)
			}
			fmt.Fprintf(w, ": %s (confidence %.2f)\n", finding.Summary, finding.Confidence)
		}
	}
	if len(result.Hypotheses) > 0 {
		fmt.Fprintln(w, "hypotheses:")
		for _, hypothesis := range result.Hypotheses {
			fmt.Fprintf(w, "- %.2f %s\n", hypothesis.Confidence, hypothesis.Message)
		}
	}
	if len(result.RecommendedActions) > 0 {
		fmt.Fprintln(w, "recommended actions:")
		for _, action := range result.RecommendedActions {
			mode := "read"
			if action.Mutating {
				mode = "mutating"
			}
			fmt.Fprintf(w, "- %s %s: %s\n", mode, action.ID, action.Command)
		}
	}
}
