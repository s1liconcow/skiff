package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/agent"
	"github.com/s1liconcow/skiff/internal/client"
)

type solveOutput struct {
	OK bool `json:"ok"`
	agent.ActionGraph
}

var newSolveClient = client.New

func runSolve(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" solve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name to solve for")
	goal := fs.String("goal", agent.GoalRestoreHealth, "goal to solve: restore-health")
	fresh := fs.Bool("fresh", true, "bypass cached API views where supported")

	flagArgs, positionals, err := splitSolveArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "solve", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "solve", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "solve", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	if *service == "" {
		return writeClientCommandError(binary, "solve", *flags.format, *flags.traceID, errors.New("service is required"), stdout, stderr)
	}
	if *goal != agent.GoalRestoreHealth {
		return writeClientCommandError(binary, "solve", *flags.format, *flags.traceID, fmt.Errorf("unsupported goal %q; expected %q", *goal, agent.GoalRestoreHealth), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := newSolveClient(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "solve", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	diagnosis, err := skiffClient.Doctor(nilContext(), client.DoctorOptions{Service: *service, Fresh: *fresh, TraceID: *flags.traceID})
	if err != nil {
		return writeClientError(binary, "solve", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	graph := agent.Solve(*diagnosis, agent.SolveOptions{Goal: *goal, Service: *service, TraceID: *flags.traceID, Binary: binary})
	switch *flags.format {
	case "human", "text":
		printSolveHuman(stdout, graph)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(solveOutput{OK: true, ActionGraph: graph}); err != nil {
			fmt.Fprintf(stderr, "%s solve: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "solve", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func splitSolveArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"env":          true,
		"format":       true,
		"goal":         true,
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

func printSolveHuman(w io.Writer, graph agent.ActionGraph) {
	fmt.Fprintf(w, "solve: %s goal=%s status=%s confidence=%.2f\n", graph.Service, graph.Goal, graph.Status, graph.Confidence)
	if graph.Health != "" {
		fmt.Fprintf(w, "health: %s\n", graph.Health)
	}
	if len(graph.Findings) == 0 {
		fmt.Fprintln(w, "findings: none")
	} else {
		fmt.Fprintln(w, "findings:")
		for _, finding := range graph.Findings {
			fmt.Fprintf(w, "- %s %s: %s\n", finding.Severity, finding.Code, finding.Summary)
		}
	}
	if len(graph.Steps) == 0 {
		fmt.Fprintln(w, "steps: none")
		return
	}
	fmt.Fprintln(w, "steps:")
	for _, step := range graph.Steps {
		mode := "read"
		if step.Mutating {
			mode = "mutating"
		}
		approval := ""
		if step.RequiresApproval {
			approval = " approval-required"
		}
		fmt.Fprintf(w, "- %s %s%s: %s\n", mode, step.ID, approval, step.Command)
	}
}
