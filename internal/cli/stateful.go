package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type statefulPlanOutput struct {
	OK      bool          `json:"ok"`
	TraceID string        `json:"trace_id,omitempty"`
	Plan    provider.Plan `json:"plan"`
}

type statefulApplyOutput struct {
	OK      bool                  `json:"ok"`
	TraceID string                `json:"trace_id,omitempty"`
	Result  deploy.StatefulResult `json:"result"`
}

type statefulInspectOutput struct {
	OK      bool                         `json:"ok"`
	TraceID string                       `json:"trace_id,omitempty"`
	Result  deploy.StatefulInspectResult `json:"result"`
}

var newStatefulProvider = newCLIProvider

func runStateful(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printStatefulUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "plan":
		return runStatefulPlan(binary, args[1:], root, stdout, stderr)
	case "apply":
		return runStatefulApply(binary, args[1:], root, stdout, stderr)
	case "inspect":
		return runStatefulInspect(binary, args[1:], root, stdout, stderr)
	case "replace-member":
		return runStatefulReplaceMember(binary, args[1:], root, stdout, stderr)
	case "resume":
		return runSagaResume(binary, args[1:], root, stdout, stderr)
	case "watch":
		return runSagaWatch(binary, args[1:], root, stdout, stderr)
	case "cancel", "compensate":
		return runSagaSkeleton(binary, args[0], args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printStatefulUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeStatefulCommandError(binary, "stateful", defaultString(root.Format, "human"), root.TraceID, fmt.Errorf("unknown stateful command %q", args[0]), stdout, stderr)
	}
}

func runStatefulPlan(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" stateful plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	contextName := fs.String("context", root.Context, "Skiff config context name")
	filePath := fs.String("file", "", "StatefulGroup YAML or JSON spec file")
	providerName := fs.String("provider", root.Provider, "provider to plan")
	region := fs.String("region", root.Region, "cloud provider region")
	stateBucket := fs.String("state", root.State, "object-state bucket URI")

	flagArgs, positionals, err := splitStatefulPlanArgs(args)
	if err != nil {
		return writeSpecError(binary, "STATEFUL_PLAN_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeSpecError(binary, "STATEFUL_PLAN_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeSpecError(binary, "STATEFUL_PLAN_INVALID", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), nil, stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeSpecError(binary, "STATEFUL_PLAN_INVALID", *format, *traceID, errors.New("StatefulGroup spec file is required"), nil, stdout, stderr)
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
		return writeSpecError(binary, "STATEFUL_PLAN_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	*providerName = firstNonEmptyString(*providerName, loaded.Config.Provider, aws.Name)
	*region = firstNonEmptyString(*region, loaded.Config.Region)
	*stateBucket = firstNonEmptyString(*stateBucket, loaded.Config.StateBucket)
	graph, err := loadStatefulGraph(*filePath)
	if err != nil {
		return writeStatefulSpecError(binary, "STATEFUL_PLAN_INVALID", *format, *traceID, err, stdout, stderr)
	}
	plan, err := planStatefulGraph(*providerName, *region, *stateBucket, nil, graph)
	if err != nil {
		return writeSpecError(binary, "STATEFUL_PLAN_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	return writeStatefulPlan(binary, *format, *traceID, plan, stdout, stderr)
}

func runStatefulApply(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" stateful apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	filePath := fs.String("file", "", "StatefulGroup YAML or JSON spec file")
	operationID := fs.String("operation-id", "", "operation ID to use")
	approvalID := fs.String("approval-id", "", "approval ID for policy-gated production operations")
	dryRun := fs.Bool("dry-run", false, "plan StatefulGroup apply without writing object state")
	planOnly := fs.Bool("plan-only", false, "render StatefulGroup apply plan without writing object state")

	flagArgs, positionals, err := splitStatefulApplyArgs(args)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful apply", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful apply", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful apply", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeStatefulCommandError(binary, "stateful apply", *flags.format, *flags.traceID, errors.New("StatefulGroup spec file is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeStatefulCommandError(binary, "stateful apply", *flags.format, *flags.traceID, errors.New("stateful apply currently requires --direct mode so object-state writes are durable"), stdout, stderr)
	}
	graph, err := loadStatefulGraph(*filePath)
	if err != nil {
		return writeStatefulSpecError(binary, "STATEFUL_APPLY_INVALID", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	store, cloud, err := openStatefulStoreAndProvider(loaded.Config, !*dryRun && !*planOnly)
	if err != nil {
		return writeClientError(binary, "stateful apply", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := (deploy.StatefulApplier{Store: store, Provider: cloud}).Apply(nilContext(), graph, deploy.StatefulRequest{
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:     *flags.traceID,
		OperationID: *operationID,
		ApprovalID:  *approvalID,
		DryRun:      *dryRun,
		PlanOnly:    *planOnly,
	})
	if err != nil {
		return writeStatefulCommandError(binary, "stateful apply", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeStatefulApplyResult(binary, *flags.format, *flags.traceID, result, stdout, stderr)
}

func runStatefulInspect(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" stateful inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	group := fs.String("group", "", "StatefulGroup name")
	operationID := fs.String("operation-id", "", "operation ID to inspect")

	flagArgs, positionals, err := splitStatefulInspectArgs(args)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful inspect", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful inspect", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful inspect", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *group == "" && len(positionals) == 1 {
		*group = positionals[0]
	}
	if *group == "" {
		return writeStatefulCommandError(binary, "stateful inspect", *flags.format, *flags.traceID, errors.New("StatefulGroup name is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeStatefulCommandError(binary, "stateful inspect", *flags.format, *flags.traceID, errors.New("stateful inspect currently requires --direct mode"), stdout, stderr)
	}
	store, err := client.OpenObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "stateful inspect", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := (deploy.StatefulApplier{Store: store}).Inspect(nilContext(), *group, *operationID, *flags.traceID)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful inspect", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeStatefulInspectResult(binary, *flags.format, *flags.traceID, result, stdout, stderr)
}

func runStatefulReplaceMember(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" stateful replace-member", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	group := fs.String("group", "", "StatefulGroup name")
	member := fs.Int("member", -1, "stateful member ordinal")
	operationID := fs.String("operation-id", "", "operation ID to use")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	reason := fs.String("reason", "", "replacement reason")
	approvalID := fs.String("approval-id", "", "approval ID for high-risk production replacement")
	run := fs.Bool("run", true, "run the saga after creating it")

	flagArgs, positionals, err := splitStatefulReplaceArgs(args)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful replace-member", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful replace-member", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful replace-member", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *group == "" && len(positionals) == 1 {
		*group = positionals[0]
	}
	if *group == "" || *member < 0 {
		return writeStatefulCommandError(binary, "stateful replace-member", *flags.format, *flags.traceID, errors.New("StatefulGroup name and --member are required"), stdout, stderr)
	}
	_ = flags.noColor

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeStatefulCommandError(binary, "stateful replace-member", *flags.format, *flags.traceID, errors.New("stateful replace-member currently requires --direct mode"), stdout, stderr)
	}
	if loaded.Config.Env == "prod" && !*flags.yes && strings.TrimSpace(*approvalID) == "" {
		return writeStatefulApprovalRequired(binary, *flags.format, *flags.traceID, *group, *member, stdout, stderr)
	}
	req := templates.StatefulReplaceMemberRequest{
		SagaID:      *sagaID,
		OperationID: *operationID,
		Group:       *group,
		Env:         loaded.Config.Env,
		Member:      *member,
		Reason:      *reason,
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:     *flags.traceID,
	}
	result, err := createAndMaybeRunStatefulReplacement(nilContext(), binary, loaded.Config, req, *run)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful replace-member", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeStatefulReplacementSagaResult(binary, "stateful replace-member", *flags.format, *flags.traceID, *result, stdout, stderr)
}

func loadStatefulGraph(filePath string) (*ir.Graph, error) {
	doc, err := spec.LoadFile(filePath, spec.DecodeOptions{})
	if err != nil {
		return nil, err
	}
	if doc.Kind != spec.KindStatefulGroup {
		return nil, fmt.Errorf("spec kind is %q; expected StatefulGroup", doc.Kind)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		return nil, err
	}
	return graph, nil
}

func planStatefulGraph(providerName, region, stateBucket string, store objstore.ObjectStore, graph *ir.Graph) (*provider.Plan, error) {
	if strings.EqualFold(providerName, aws.Name) && strings.TrimSpace(region) == "" {
		return statefulReadOnlyPlan(providerName, graph), nil
	}
	cfg := config.Config{Provider: providerName, Region: region, StateBucket: stateBucket}
	cloud, err := newStatefulProvider(cfg, store)
	if err != nil {
		return nil, err
	}
	return cloud.Plan(nilContext(), graph)
}

func openStatefulStoreAndProvider(cfg config.Config, storeNeeded bool) (objstore.ObjectStore, provider.Provider, error) {
	var store objstore.ObjectStore
	var err error
	if storeNeeded {
		store, err = client.OpenObjectStore(cfg)
		if err != nil {
			return nil, nil, err
		}
	}
	cloud, err := newStatefulProvider(cfg, store)
	if err != nil {
		return nil, nil, err
	}
	return store, cloud, nil
}

func writeStatefulPlan(binary, format, traceID string, plan *provider.Plan, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s StatefulGroup %s/%s plan:\n", plan.Provider, plan.Env, plan.Service)
		for _, resource := range plan.Resources {
			fmt.Fprintf(stdout, "- %s %s %s: %s\n", resource.Action, resource.Kind, resource.Name, resource.Summary)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(statefulPlanOutput{OK: true, TraceID: traceID, Plan: *plan}); err != nil {
			fmt.Fprintf(stderr, "%s stateful plan: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeStatefulCommandError(binary, "stateful plan", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeStatefulApplyResult(binary, format, traceID string, result *deploy.StatefulResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		if result.DryRun || result.PlanOnly {
			fmt.Fprintf(stdout, "StatefulGroup apply plan for %s/%s:\n", result.Env, result.Group)
			for _, resource := range result.Plan.Resources {
				fmt.Fprintf(stdout, "- %s %s %s\n", resource.Action, resource.Kind, resource.Name)
			}
			return ExitSuccess
		}
		fmt.Fprintf(stdout, "stateful apply %s succeeded\n", result.OperationID)
		fmt.Fprintf(stdout, "group: %s\n", result.Group)
		fmt.Fprintf(stdout, "members: %d\n", len(result.MemberControls))
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(statefulApplyOutput{OK: result.OK, TraceID: traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s stateful apply: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeStatefulCommandError(binary, "stateful apply", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeStatefulInspectResult(binary, format, traceID string, result *deploy.StatefulInspectResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "StatefulGroup %s/%s\n", result.Env, result.Group)
		if result.OperationID != "" {
			fmt.Fprintf(stdout, "operation: %s %s\n", result.OperationID, result.Status)
		}
		for _, member := range result.MemberControls {
			fmt.Fprintf(stdout, "- member %d phase=%s generation=%d dns=%s instance=%s volume=%s\n", member.Member, member.Phase, member.Generation, member.DNSName, member.InstanceID, member.VolumeID)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(statefulInspectOutput{OK: true, TraceID: traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s stateful inspect: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeStatefulCommandError(binary, "stateful inspect", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeStatefulSpecError(binary, code, format, traceID string, err error, stdout, stderr io.Writer) int {
	var validation spec.ValidationError
	if errors.As(err, &validation) {
		return writeSpecError(binary, code, format, traceID, errors.New("spec validation failed"), validation.Diagnostics, stdout, stderr)
	}
	return writeSpecError(binary, code, format, traceID, err, nil, stdout, stderr)
}

func writeStatefulCommandError(binary, command, format, traceID string, err error, stdout, stderr io.Writer) int {
	if isJSONFormat(format) {
		_ = writeJSON(stdout, format, commandErrorOutput{
			OK:      false,
			Code:    "STATEFUL_COMMAND_FAILED",
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{ID: "inspect_stateful_help", Command: binary + " stateful --help", Mutating: false},
			},
		})
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
	return ExitUserError
}

func splitStatefulPlanArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"config":   true,
		"context":  true,
		"file":     true,
		"format":   true,
		"provider": true,
		"region":   true,
		"state":    true,
		"trace-id": true,
	}
	return splitArgs(args, valueFlags)
}

func splitStatefulApplyArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"file":         true,
		"format":       true,
		"mode":         true,
		"operation-id": true,
		"approval-id":  true,
		"provider":     true,
		"region":       true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func splitStatefulInspectArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"group":        true,
		"mode":         true,
		"operation-id": true,
		"provider":     true,
		"region":       true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func splitStatefulReplaceArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"approval-id":  true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"group":        true,
		"member":       true,
		"mode":         true,
		"operation-id": true,
		"provider":     true,
		"reason":       true,
		"region":       true,
		"saga-id":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func writeStatefulApprovalRequired(binary, format, traceID, group string, member int, stdout, stderr io.Writer) int {
	command := fmt.Sprintf("%s stateful replace-member %s --member %d --yes --format json", binary, group, member)
	if isJSONFormat(format) {
		_ = writeJSON(stdout, format, commandErrorOutput{
			OK:      false,
			Code:    "APPROVAL_REQUIRED",
			Summary: "production stateful replacement is high risk and requires approval",
			TraceID: traceID,
			RecommendedActions: []recommendedAction{{
				ID:            "approve_and_replace_member",
				Command:       command,
				Mutating:      true,
				Safety:        "requires an explicit operator approval signal",
				Reversibility: schema.Compensatable,
				Risk:          schema.RiskHigh,
			}},
		})
		return ExitPolicyDenied
	}
	fmt.Fprintf(stderr, "%s stateful replace-member: production replacement requires --yes or --approval-id\n", binary)
	return ExitPolicyDenied
}

func printStatefulUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s stateful <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  plan       Plan StatefulGroup provider resources")
	fmt.Fprintln(w, "  apply      Write StatefulGroup durable object state")
	fmt.Fprintln(w, "  inspect    Inspect StatefulGroup direct object state")
	fmt.Fprintln(w, "  replace-member  Replace one failed StatefulGroup member through a saga")
	fmt.Fprintln(w, "  resume     Resume a stateful saga")
	fmt.Fprintln(w, "  watch      Watch stateful saga events")
	fmt.Fprintln(w, "  cancel     Cancel a registered stateful saga")
	fmt.Fprintln(w, "  compensate Compensate a registered stateful saga")
}
