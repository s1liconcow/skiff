package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps/approval"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type sagaInspectOutput struct {
	OK      bool                    `json:"ok"`
	TraceID string                  `json:"trace_id,omitempty"`
	Result  sagastate.InspectResult `json:"result"`
}

type sagaCommandOutput struct {
	OK          bool   `json:"ok"`
	TraceID     string `json:"trace_id,omitempty"`
	Command     string `json:"command"`
	Saga        string `json:"saga,omitempty"`
	Implemented bool   `json:"implemented"`
	Summary     string `json:"summary"`
}

type sagaApprovalOutput struct {
	OK      bool                    `json:"ok"`
	TraceID string                  `json:"trace_id,omitempty"`
	Result  approval.DecisionResult `json:"result"`
}

func runSaga(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return writeClientCommandError(binary, "saga", root.Format, root.TraceID, errors.New("expected saga command inspect"), stdout, stderr)
	}
	switch args[0] {
	case "inspect":
		return runSagaInspect(binary, args[1:], root, stdout, stderr)
	case "approve":
		return runSagaApproval(binary, args[0], args[1:], root, stdout, stderr)
	case "reject":
		return runSagaApproval(binary, args[0], args[1:], root, stdout, stderr)
	case "start", "watch", "resume", "cancel", "compensate":
		return runSagaSkeleton(binary, args[0], args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printSagaUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "saga", root.Format, root.TraceID, fmt.Errorf("unknown saga command %q", args[0]), stdout, stderr)
	}
}

func runSagaApproval(binary, command string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" saga "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	sagaID := fs.String("saga", "", "saga ID")
	stepID := fs.String("step", "", "approval step ID")
	reason := fs.String("reason", "", "decision reason")

	flagArgs, positionals, err := splitSagaApprovalArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *sagaID == "" {
		*sagaID = positionals[0]
	}
	if *sagaID == "" || *stepID == "" {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New("saga ID and --step are required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New("saga approval currently requires --direct mode"), stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	req := approval.DecisionRequest{
		SagaID:  *sagaID,
		StepID:  *stepID,
		Actor:   schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID: *flags.traceID,
		Reason:  *reason,
		Binary:  binary,
	}
	var result *approval.DecisionResult
	switch command {
	case "approve":
		result, err = approval.Approve(nilContext(), sagastate.NewStore(store), req)
	case "reject":
		result, err = approval.Reject(nilContext(), sagastate.NewStore(store), req)
	default:
		err = fmt.Errorf("unsupported approval command %q", command)
	}
	if err != nil {
		return writeClientError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		fmt.Fprintf(stdout, "saga %s %s step %s\n", result.SagaID, result.Decision, result.StepID)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(sagaApprovalOutput{OK: true, TraceID: *flags.traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s saga %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func runSagaSkeleton(binary, command string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" saga "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	sagaID := fs.String("saga", "", "saga ID")
	flagArgs, positionals, err := splitSagaInspectArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *sagaID == "" {
		*sagaID = positionals[0]
	}
	_ = flags.noColor
	_ = flags.yes
	summary := "saga " + command + " command is registered; execution wiring will be enabled by concrete saga templates"
	switch *flags.format {
	case "human", "text":
		fmt.Fprintln(stdout, summary)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(sagaCommandOutput{
			OK:          true,
			TraceID:     *flags.traceID,
			Command:     command,
			Saga:        *sagaID,
			Implemented: false,
			Summary:     summary,
		}); err != nil {
			fmt.Fprintf(stderr, "%s saga %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func runSagaInspect(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" saga inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	sagaID := fs.String("saga", "", "saga ID to inspect")

	flagArgs, positionals, err := splitSagaInspectArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *sagaID == "" {
		*sagaID = positionals[0]
	}
	if *sagaID == "" {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New("saga ID is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New("saga inspect currently requires --direct mode"), stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := sagastate.NewStore(store).Inspect(nilContext(), *sagaID)
	if err != nil {
		return writeClientError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		printSagaInspectHuman(stdout, *result)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(sagaInspectOutput{OK: true, TraceID: *flags.traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s saga inspect: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func splitSagaInspectArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"env":          true,
		"format":       true,
		"mode":         true,
		"provider":     true,
		"region":       true,
		"saga":         true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func splitSagaApprovalArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"env":          true,
		"format":       true,
		"mode":         true,
		"provider":     true,
		"reason":       true,
		"region":       true,
		"saga":         true,
		"state":        true,
		"state-bucket": true,
		"step":         true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func printSagaInspectHuman(w io.Writer, result sagastate.InspectResult) {
	fmt.Fprintf(w, "saga: %s status=%s", result.SagaID, result.Status)
	if result.Kind != "" {
		fmt.Fprintf(w, " kind=%s", result.Kind)
	}
	fmt.Fprintln(w)
	if result.Risk != "" || result.Reversibility != "" {
		fmt.Fprintf(w, "risk: %s reversibility: %s\n", result.Risk, result.Reversibility)
	}
	if len(result.CurrentSteps) > 0 {
		fmt.Fprintf(w, "current_steps: %v\n", result.CurrentSteps)
	}
	for _, step := range result.Control.StepResults {
		if step.Status != "waiting" {
			continue
		}
		var waiting struct {
			State          string `json:"state"`
			ApproveCommand string `json:"approve_command"`
			RejectCommand  string `json:"reject_command"`
			Summary        string `json:"summary"`
		}
		if err := json.Unmarshal(step.Result, &waiting); err != nil || waiting.State != "waiting_for_approval" {
			continue
		}
		fmt.Fprintf(w, "approval_required: %s\n", step.StepID)
		if waiting.Summary != "" {
			fmt.Fprintf(w, "summary: %s\n", waiting.Summary)
		}
		if waiting.ApproveCommand != "" {
			fmt.Fprintf(w, "approve: %s\n", waiting.ApproveCommand)
		}
		if waiting.RejectCommand != "" {
			fmt.Fprintf(w, "reject: %s\n", waiting.RejectCommand)
		}
	}
	if len(result.Nodes) > 0 {
		fmt.Fprintln(w, "nodes:")
		for _, node := range result.Nodes {
			fmt.Fprintf(w, "- %s kind=%s risk=%s reversibility=%s\n", node.ID, node.Kind, node.Risk, node.Reversibility)
		}
	}
}

func printSagaUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s saga inspect <saga> [flags]\n", binary)
	fmt.Fprintln(w, "       "+binary+" saga approve|reject <saga> --step <step> [flags]")
	fmt.Fprintln(w, "       "+binary+" saga start|watch|resume|cancel|compensate <saga> [flags]")
}
