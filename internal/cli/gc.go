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
	skiffgc "github.com/s1liconcow/skiff/internal/gc"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type gcPlanOutput struct {
	OK      bool         `json:"ok"`
	TraceID string       `json:"trace_id,omitempty"`
	Plan    skiffgc.Plan `json:"plan"`
}

type gcApplyOutput struct {
	OK      bool                `json:"ok"`
	TraceID string              `json:"trace_id,omitempty"`
	Result  skiffgc.ApplyResult `json:"result"`
}

func runGC(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printGCUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "plan":
		return runGCPlan(binary, args[1:], root, stdout, stderr)
	case "apply":
		return runGCApply(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printGCUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "gc", root.Format, root.TraceID, fmt.Errorf("unknown gc command %q", args[0]), stdout, stderr)
	}
}

func runGCPlan(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	flags, service, retention, errCode := parseGCFlags(binary, "gc plan", args, root, stdout, stderr)
	if errCode != nil {
		return *errCode
	}
	loaded, err := flags.load(binary, root, flags.flagSet)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "gc plan", *flags.format, *flags.traceID, errors.New("gc plan currently requires --direct mode"), stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "gc plan", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	plan, err := (skiffgc.Planner{Store: store}).Plan(nilContext(), skiffgc.PlanRequest{Service: *service, Env: loaded.Config.Env, Retention: *retention})
	if err != nil {
		return writeClientCommandError(binary, "gc plan", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeGCPlan(binary, *flags.format, *flags.traceID, *plan, stdout, stderr)
}

func runGCApply(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	flags, service, retention, errCode := parseGCFlags(binary, "gc apply", args, root, stdout, stderr)
	if errCode != nil {
		return *errCode
	}
	approvalID := flags.flagSet.Lookup("approval-id").Value.String()
	loaded, err := flags.load(binary, root, flags.flagSet)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "gc apply", *flags.format, *flags.traceID, errors.New("gc apply currently requires --direct mode"), stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "gc apply", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	planner := skiffgc.Planner{Store: store}
	plan, err := planner.Plan(nilContext(), skiffgc.PlanRequest{Service: *service, Env: loaded.Config.Env, Retention: *retention})
	if err != nil {
		return writeClientCommandError(binary, "gc apply", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := planner.Apply(nilContext(), *plan, skiffgc.ApplyRequest{
		Actor:      schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:    *flags.traceID,
		ApprovalID: approvalID,
		Yes:        *flags.yes,
	})
	if err != nil {
		return writeClientCommandError(binary, "gc apply", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		fmt.Fprintf(stdout, "gc applied %d action(s), skipped %d\n", len(result.Applied), len(result.Skipped))
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(gcApplyOutput{OK: true, TraceID: *flags.traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s gc apply: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "gc apply", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

type gcFlagSet struct {
	clientFlagSet
	flagSet *flag.FlagSet
}

func parseGCFlags(binary, command string, args []string, root rootOptions, stdout, stderr io.Writer) (gcFlagSet, *string, *time.Duration, *int) {
	fs := flag.NewFlagSet(binary+" "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	retention := fs.Duration("retention", 30*24*time.Hour, "retention window before cleanup actions are planned")
	fs.String("approval-id", "", "approval ID for production GC apply")
	flagArgs, positionals, err := splitGCArgs(args)
	if err != nil {
		code := writeClientCommandError(binary, command, defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
		return gcFlagSet{}, nil, nil, &code
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		code := ExitSuccess
		return gcFlagSet{}, nil, nil, &code
	} else if err != nil {
		code := writeClientCommandError(binary, command, *flags.format, *flags.traceID, err, stdout, stderr)
		return gcFlagSet{}, nil, nil, &code
	}
	if len(positionals) > 1 {
		code := writeClientCommandError(binary, command, *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
		return gcFlagSet{}, nil, nil, &code
	}
	if *service == "" && len(positionals) == 1 {
		*service = positionals[0]
	}
	_ = flags.noColor
	return gcFlagSet{clientFlagSet: flags, flagSet: fs}, service, retention, nil
}

func writeGCPlan(binary, format, traceID string, plan skiffgc.Plan, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "gc plan: %d action(s)\n", len(plan.Actions))
		for _, action := range plan.Actions {
			fmt.Fprintf(stdout, "- %s %s: %s\n", action.Kind, action.ID, action.Summary)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(gcPlanOutput{OK: true, TraceID: traceID, Plan: plan}); err != nil {
			fmt.Fprintf(stderr, "%s gc plan: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "gc plan", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func splitGCArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"approval-id":  true,
		"config":       true,
		"env":          true,
		"format":       true,
		"mode":         true,
		"provider":     true,
		"region":       true,
		"retention":    true,
		"service":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func printGCUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s gc <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  plan   Plan conservative cleanup actions")
	fmt.Fprintln(w, "  apply  Audit and apply approved cleanup actions")
}
