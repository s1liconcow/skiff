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
	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type promoteOutput struct {
	OK      bool                    `json:"ok"`
	TraceID string                  `json:"trace_id,omitempty"`
	Code    string                  `json:"code,omitempty"`
	Summary string                  `json:"summary,omitempty"`
	Result  release.PromotionResult `json:"result"`
}

func runPromote(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	return runPromoteCommand(binary, "promote", args, root, stdout, stderr)
}

func runPromoteCommand(binary, command string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printPromoteUsage(stderr, binary, command)
		return ExitUserError
	}
	if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		printPromoteUsage(stdout, binary, command)
		return ExitSuccess
	}
	fs := flag.NewFlagSet(binary+" "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	fromEnv := fs.String("from", "", "source environment")
	toEnv := fs.String("to", "", "target environment")
	candidateID := fs.String("candidate", "", "release candidate ID")
	operationID := fs.String("operation-id", "", "operation ID to record")
	approvalID := fs.String("approval-id", "", "approval context ID for protected environments")
	minStable := fs.String("min-stable-duration", "0s", "minimum source stable duration, e.g. 30m")
	dryRun := fs.Bool("dry-run", false, "validate and render promotion plan without writing operation state")
	actorID := fs.String("actor", "skiff-cli", "actor ID requesting promotion")
	actorType := fs.String("actor-type", "ci", "actor type requesting promotion")
	var requiredChecks stringListFlag
	fs.Var(&requiredChecks, "require-check", "required passed evidence check; may be repeated")

	flagArgs, positionals, err := splitPromoteArgs(args)
	if err != nil {
		return writeClientCommandError(binary, command, defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, command, *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	_ = flags.noColor
	_ = flags.yes

	duration, err := time.ParseDuration(*minStable)
	if err != nil {
		return writeClientCommandError(binary, command, *flags.format, *flags.traceID, fmt.Errorf("--min-stable-duration: %w", err), stdout, stderr)
	}
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, command, *flags.format, *flags.traceID, errors.New("release promotion currently requires --direct mode"), stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := (release.Manager{Store: store}).Promote(nilContext(), release.PromotionRequest{
		Service:           *service,
		FromEnv:           *fromEnv,
		ToEnv:             *toEnv,
		CandidateID:       *candidateID,
		OperationID:       *operationID,
		ApprovalID:        *approvalID,
		RequiredChecks:    []string(requiredChecks),
		MinStableDuration: duration,
		DryRun:            *dryRun,
		Actor:             schema.Actor{ID: *actorID, Type: *actorType},
		TraceID:           *flags.traceID,
	})
	if err != nil {
		return writeClientCommandError(binary, command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if result == nil {
		return writeClientCommandError(binary, command, *flags.format, *flags.traceID, errors.New("promotion result is nil"), stdout, stderr)
	}
	exitCode := ExitSuccess
	code := ""
	summary := ""
	if !result.OK {
		exitCode = ExitUserError
		code = "PROMOTION_REQUIREMENTS_FAILED"
		summary = "promotion requirements failed"
	}
	switch *flags.format {
	case "human", "text":
		if result.OK {
			fmt.Fprintf(stdout, "promotion %s validated\n", result.OperationID)
			if !result.DryRun {
				fmt.Fprintf(stdout, "operation: %s\n", result.OperationID)
			}
		} else {
			fmt.Fprintln(stdout, "promotion requirements failed")
		}
		printPromotionRequirements(stdout, result.Requirements)
		return exitCode
	case "markdown":
		_, _ = io.WriteString(stdout, result.PlanMarkdown)
		return exitCode
	case "json":
		if err := json.NewEncoder(stdout).Encode(promoteOutput{OK: result.OK, TraceID: *flags.traceID, Code: code, Summary: summary, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return exitCode
	default:
		return writeClientCommandError(binary, command, *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", "json-pretty", or "markdown"`), stdout, stderr)
	}
}

func printPromotionRequirements(w io.Writer, requirements []release.PromotionRequirement) {
	for _, req := range requirements {
		state := "ok"
		if !req.OK {
			state = "failed"
		}
		fmt.Fprintf(w, "- %s %s", state, req.Summary)
		if !req.OK && req.Detail != "" {
			fmt.Fprintf(w, ": %s", req.Detail)
		}
		fmt.Fprintln(w)
	}
}

func splitPromoteArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"actor":               true,
		"actor-type":          true,
		"api-url":             true,
		"approval-id":         true,
		"candidate":           true,
		"config":              true,
		"env":                 true,
		"format":              true,
		"from":                true,
		"min-stable-duration": true,
		"mode":                true,
		"operation-id":        true,
		"provider":            true,
		"region":              true,
		"require-check":       true,
		"service":             true,
		"state":               true,
		"state-bucket":        true,
		"to":                  true,
		"trace-id":            true,
	}
	return splitArgs(args, valueFlags)
}

func printPromoteUsage(w io.Writer, binary, command string) {
	fmt.Fprintf(w, "Usage: %s %s <service> --from <env> --to <env> --candidate <id> [flags]\n\n", binary, command)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --candidate <id>")
	fmt.Fprintln(w, "  --from <env> --to <env>")
	fmt.Fprintln(w, "  --min-stable-duration <duration>")
	fmt.Fprintln(w, "  --approval-id <id>")
	fmt.Fprintln(w, "  --format human|json|json-pretty|markdown")
}
