package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type authzExplainOutput struct {
	OK       bool           `json:"ok"`
	TraceID  string         `json:"trace_id,omitempty"`
	Decision authz.Decision `json:"decision"`
}

func runAuthz(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printAuthzUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "explain":
		return runAuthzExplain(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printAuthzUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "authz", root.Format, root.TraceID, fmt.Errorf("unknown authz command %q", args[0]), stdout, stderr)
	}
}

func runAuthzExplain(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" authz explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	action := fs.String("action", "", "action: read, plan, deploy, backup, rollback, approve, debug, rotate, restore, failover, or gc")
	service := fs.String("service", "", "service name")
	env := fs.String("env", root.Env, "environment name")
	risk := fs.String("risk", "", "risk: low, medium, high, or critical")
	approvalID := fs.String("approval-id", "", "approval context ID")
	approvalRole := fs.String("approval-role", "", "required approval role override")
	actorID := fs.String("actor-id", "skiff-cli", "actor identifier")
	actorType := fs.String("actor-type", "user", "actor type: user, ci, agent, skiffd, worker, or break-glass")
	targetKind := fs.String("target-kind", "service", "target kind")
	targetName := fs.String("target", "", "target name")
	dryRun := fs.Bool("dry-run", false, "explain as a dry-run request")
	planOnly := fs.Bool("plan-only", false, "explain as a plan-only request")
	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "authz explain", *format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeClientCommandError(binary, "authz explain", *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	if *action == "" {
		return writeClientCommandError(binary, "authz explain", *format, *traceID, errors.New("--action is required"), stdout, stderr)
	}
	_ = noColor
	_ = yes
	if *targetName == "" {
		*targetName = *service
	}
	decision := authz.Explain(authz.DefaultPolicy{}, nilContext(), authz.Request{
		Actor:        schema.Actor{ID: *actorID, Type: *actorType},
		Action:       authz.Action(*action),
		Target:       schema.Target{Kind: *targetKind, Name: *targetName},
		Env:          *env,
		Service:      *service,
		Risk:         schema.Risk(*risk),
		ApprovalID:   *approvalID,
		ApprovalRole: *approvalRole,
		DryRun:       *dryRun,
		PlanOnly:     *planOnly,
		TraceID:      *traceID,
	})
	switch *format {
	case "human", "text":
		if decision.Allowed {
			fmt.Fprintf(stdout, "allowed: %s on %s/%s\n", decision.Action, decision.Target.Kind, decision.Target.Name)
		} else {
			fmt.Fprintf(stdout, "denied: %s on %s/%s\n", decision.Action, decision.Target.Kind, decision.Target.Name)
		}
		if decision.RequiresApproval {
			fmt.Fprintf(stdout, "approval_required: %s\n", decision.ApprovalRole)
		}
		for _, reason := range decision.Reasons {
			fmt.Fprintf(stdout, "- %s\n", reason)
		}
		for _, denial := range decision.Denials {
			fmt.Fprintf(stdout, "- %s\n", denial)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(authzExplainOutput{OK: true, TraceID: *traceID, Decision: decision}); err != nil {
			fmt.Fprintf(stderr, "%s authz explain: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "authz explain", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func printAuthzUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s authz <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  explain  Explain whether an actor may perform a proposed operation")
}
