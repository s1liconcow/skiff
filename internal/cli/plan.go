package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	skiffcost "github.com/s1liconcow/skiff/internal/cost"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
)

type planOutput struct {
	OK              bool                       `json:"ok"`
	TraceID         string                     `json:"trace_id,omitempty"`
	Plan            provider.Plan              `json:"plan"`
	AdvisorWarnings []skiffcost.Recommendation `json:"advisor_warnings,omitempty"`
}

func runPlan(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	contextName := fs.String("context", root.Context, "Skiff config context name")
	filePath := fs.String("file", "", "Skiff YAML or JSON spec file")
	providerName := fs.String("provider", root.Provider, "provider to plan")
	region := fs.String("region", root.Region, "cloud provider region")
	stateBucket := fs.String("state", root.State, "object-state bucket URI")

	flagArgs, positionals, err := splitPlanArgs(args)
	if err != nil {
		return writeSpecError(binary, "PLAN_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeSpecError(binary, "PLAN_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeSpecError(binary, "PLAN_INVALID", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), nil, stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeSpecError(binary, "PLAN_INVALID", *format, *traceID, errors.New("spec file is required"), nil, stdout, stderr)
	}
	_ = noColor
	_ = yes

	overrides := root.configOverrides()
	flagToField := map[string]string{
		"provider": config.FieldProvider,
		"region":   config.FieldRegion,
		"state":    config.FieldStateBucket,
	}
	fs.Visit(func(flag *flag.Flag) {
		if field := flagToField[flag.Name]; field != "" {
			overrides[field] = flag.Value.String()
		}
	})
	loaded, err := config.Load(config.LoadOptions{
		ModeDefault: defaultMode(binary),
		ConfigPath:  *configPath,
		Context:     *contextName,
		Overrides:   overrides,
	})
	if err != nil {
		return writeSpecError(binary, "PLAN_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	*providerName = firstNonEmptyString(*providerName, loaded.Config.Provider, aws.Name)
	*region = firstNonEmptyString(*region, loaded.Config.Region)
	*stateBucket = firstNonEmptyString(*stateBucket, loaded.Config.StateBucket)

	doc, err := spec.LoadFile(*filePath, spec.DecodeOptions{})
	if err != nil {
		return writeSpecError(binary, "SPEC_DECODE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		var validation spec.ValidationError
		if errors.As(err, &validation) {
			return writeSpecError(binary, "SPEC_INVALID", *format, *traceID, errors.New("spec validation failed"), validation.Diagnostics, stdout, stderr)
		}
		return writeSpecError(binary, "SPEC_COMPILE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	if *providerName != aws.Name {
		return writeSpecError(binary, "PLAN_INVALID", *format, *traceID, fmt.Errorf("unsupported provider %q; expected aws", *providerName), nil, stdout, stderr)
	}
	var plan *provider.Plan
	if doc.Kind == spec.KindStatefulGroup && *region == "" {
		plan = statefulReadOnlyPlan(*providerName, graph)
	} else {
		awsProvider, err := aws.NewFromConfig(config.Config{Region: *region, StateBucket: *stateBucket})
		if err != nil {
			return writeSpecError(binary, "PLAN_INVALID", *format, *traceID, err, nil, stdout, stderr)
		}
		plan, err = awsProvider.Plan(nilContext(), graph)
		if err != nil {
			return writeSpecError(binary, "PLAN_FAILED", *format, *traceID, err, nil, stdout, stderr)
		}
	}
	advisorWarnings := skiffcost.PlanWarnings(skiffcost.InputFromGraph(graph))

	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s service %s/%s plan:\n", plan.Provider, plan.Env, plan.Service)
		for _, resource := range plan.Resources {
			fmt.Fprintf(stdout, "- %s %s %s: %s\n", resource.Action, resource.Kind, resource.Name, resource.Summary)
		}
		if len(advisorWarnings) > 0 {
			fmt.Fprintln(stdout, "advisor warnings:")
			for _, warning := range advisorWarnings {
				fmt.Fprintf(stdout, "- %s [%s confidence]: %s\n", warning.ID, warning.Confidence, warning.Summary)
			}
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(planOutput{OK: true, TraceID: *traceID, Plan: *plan, AdvisorWarnings: advisorWarnings}); err != nil {
			fmt.Fprintf(stderr, "%s plan: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeSpecError(binary, "PLAN_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), nil, stdout, stderr)
	}
}

func splitPlanArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"file":     true,
		"format":   true,
		"config":   true,
		"context":  true,
		"provider": true,
		"region":   true,
		"state":    true,
		"trace-id": true,
	}
	return splitArgs(args, valueFlags)
}
