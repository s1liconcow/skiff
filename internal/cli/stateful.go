package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/s1liconcow/skiff/internal/agent"
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
	"github.com/s1liconcow/skiff/internal/state"
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

type statefulStatusOutput struct {
	OK      bool                 `json:"ok"`
	TraceID string               `json:"trace_id,omitempty"`
	Result  client.StatefulGroup `json:"result"`
}

type statefulDoctorOutput struct {
	OK      bool          `json:"ok"`
	TraceID string        `json:"trace_id,omitempty"`
	Doctor  client.Doctor `json:"doctor"`
}

type statefulSolveOutput struct {
	OK bool `json:"ok"`
	agent.ActionGraph
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
	case "status":
		return runStatefulStatus(binary, args[1:], root, stdout, stderr)
	case "doctor":
		return runStatefulDoctor(binary, args[1:], root, stdout, stderr)
	case "solve":
		return runStatefulSolve(binary, args[1:], root, stdout, stderr)
	case "logs":
		return runStatefulLogs(binary, args[1:], root, stdout, stderr)
	case "metrics":
		return runStatefulMetrics(binary, args[1:], root, stdout, stderr)
	case "replace-member":
		return runStatefulReplaceMember(binary, args[1:], root, stdout, stderr)
	case "snapshot":
		return runStatefulSnapshot(binary, args[1:], root, stdout, stderr)
	case "backup":
		return runStatefulBackup(binary, args[1:], root, stdout, stderr)
	case "restore":
		return runStatefulRestore(binary, args[1:], root, stdout, stderr)
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

func runStatefulStatus(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" stateful status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	group := fs.String("group", "", "StatefulGroup name")
	fresh := fs.Bool("fresh", false, "bypass cached API views where supported")

	flagArgs, positionals, err := splitStatefulStatusArgs(args)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful status", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful status", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful status", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *group == "" && len(positionals) == 1 {
		*group = positionals[0]
	}
	if *group == "" {
		return writeStatefulCommandError(binary, "stateful status", *flags.format, *flags.traceID, errors.New("StatefulGroup name is required"), stdout, stderr)
	}
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := newStatusClient(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "stateful status", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	status, err := skiffClient.Status(nilContext(), client.StatusOptions{Service: *group, Fresh: *fresh, TraceID: *flags.traceID})
	if err != nil {
		return writeClientError(binary, "stateful status", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	groupStatus, ok := statefulGroupFromStatus(*status, *group)
	if !ok {
		return writeStatefulCommandError(binary, "stateful status", *flags.format, *flags.traceID, fmt.Errorf("StatefulGroup %q was not found", *group), stdout, stderr)
	}
	return writeStatefulStatusResult(binary, *flags.format, *flags.traceID, groupStatus, stdout, stderr)
}

func runStatefulDoctor(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" stateful doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	group := fs.String("group", "", "StatefulGroup name")
	fresh := fs.Bool("fresh", true, "bypass cached API views where supported")
	flagArgs, positionals, err := splitStatefulStatusArgs(args)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful doctor", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful doctor", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful doctor", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *group == "" && len(positionals) == 1 {
		*group = positionals[0]
	}
	if *group == "" {
		return writeStatefulCommandError(binary, "stateful doctor", *flags.format, *flags.traceID, errors.New("StatefulGroup name is required"), stdout, stderr)
	}
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := newDoctorClient(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "stateful doctor", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := skiffClient.Doctor(nilContext(), client.DoctorOptions{Service: *group, Fresh: *fresh, TraceID: *flags.traceID})
	if err != nil {
		return writeClientError(binary, "stateful doctor", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeStatefulDoctorResult(binary, *flags.format, *flags.traceID, *result, stdout, stderr)
}

func runStatefulSolve(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" stateful solve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	group := fs.String("group", "", "StatefulGroup name")
	goal := fs.String("goal", agent.GoalRestoreHealth, "goal to solve: restore-health")
	fresh := fs.Bool("fresh", true, "bypass cached API views where supported")
	flagArgs, positionals, err := splitStatefulSolveArgs(args)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful solve", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful solve", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful solve", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *group == "" && len(positionals) == 1 {
		*group = positionals[0]
	}
	if *group == "" {
		return writeStatefulCommandError(binary, "stateful solve", *flags.format, *flags.traceID, errors.New("StatefulGroup name is required"), stdout, stderr)
	}
	if *goal != agent.GoalRestoreHealth {
		return writeStatefulCommandError(binary, "stateful solve", *flags.format, *flags.traceID, fmt.Errorf("unsupported goal %q; expected %q", *goal, agent.GoalRestoreHealth), stdout, stderr)
	}
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := newSolveClient(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "stateful solve", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	diagnosis, err := skiffClient.Doctor(nilContext(), client.DoctorOptions{Service: *group, Fresh: *fresh, TraceID: *flags.traceID})
	if err != nil {
		return writeClientError(binary, "stateful solve", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	graph := agent.Solve(*diagnosis, agent.SolveOptions{Goal: *goal, Service: *group, TraceID: *flags.traceID, Binary: binary})
	return writeStatefulSolveResult(binary, *flags.format, graph, stdout, stderr)
}

func runStatefulLogs(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" stateful logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	group := fs.String("group", "", "StatefulGroup name")
	member := fs.Int("member", -1, "stateful member ordinal")
	sinceValue := fs.String("since", "", "duration like 20m or RFC3339 timestamp")
	limit := fs.Int("limit", 100, "maximum log entries")
	follow := fs.Bool("follow", false, "follow log output")
	flagArgs, positionals, err := splitStatefulLogsArgs(args)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful logs", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful logs", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful logs", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *group == "" && len(positionals) == 1 {
		*group = positionals[0]
	}
	if *group == "" {
		return writeStatefulCommandError(binary, "stateful logs", *flags.format, *flags.traceID, errors.New("StatefulGroup name is required"), stdout, stderr)
	}
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeStatefulCommandError(binary, "stateful logs", *flags.format, *flags.traceID, errors.New("stateful logs currently requires --direct mode"), stdout, stderr)
	}
	since, err := parseSince(*sinceValue)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful logs", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	instanceID, err := statefulMemberInstanceID(nilContext(), loaded.Config, *group, *member)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful logs", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	logProvider, err := newLogsProvider(loaded.Config)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful logs", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return runLogsQuery(logsContext(), logProvider, provider.LogsRequest{
		Service:    *group,
		Env:        loaded.Config.Env,
		InstanceID: instanceID,
		Since:      since,
		Limit:      *limit,
	}, *follow, binary, *flags.format, *flags.traceID, stdout, stderr)
}

func runStatefulMetrics(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" stateful metrics", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	group := fs.String("group", "", "StatefulGroup name")
	member := fs.Int("member", -1, "stateful member ordinal")
	metricNames := fs.String("metric", "", "comma-separated metric names")
	sinceValue := fs.String("since", "", "duration like 15m or RFC3339 start timestamp")
	fromValue := fs.String("from", "", "RFC3339 start timestamp")
	toValue := fs.String("to", "", "RFC3339 end timestamp")
	period := fs.Int("period", 60, "metric period in seconds")
	flagArgs, positionals, err := splitStatefulMetricsArgs(args)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful metrics", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful metrics", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful metrics", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *group == "" && len(positionals) == 1 {
		*group = positionals[0]
	}
	if *group == "" {
		return writeStatefulCommandError(binary, "stateful metrics", *flags.format, *flags.traceID, errors.New("StatefulGroup name is required"), stdout, stderr)
	}
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeStatefulCommandError(binary, "stateful metrics", *flags.format, *flags.traceID, errors.New("stateful metrics currently requires --direct mode"), stdout, stderr)
	}
	from, to, err := parseMetricWindow(*sinceValue, *fromValue, *toValue)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful metrics", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	instanceID, err := statefulMemberInstanceID(nilContext(), loaded.Config, *group, *member)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful metrics", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	metricProvider, err := newMetricsProviderForCLI(loaded.Config)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful metrics", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := metricProvider.Metrics(nilContext(), provider.MetricsRequest{
		Service:       *group,
		Env:           loaded.Config.Env,
		InstanceID:    instanceID,
		Names:         splitMetricNames(*metricNames),
		From:          from,
		To:            to,
		PeriodSeconds: *period,
	})
	if err != nil {
		return writeMetricsError(binary, *flags.format, *flags.traceID, *group, err, stdout, stderr)
	}
	return writeMetricsResult(binary, *flags.format, *flags.traceID, result.Series, stdout, stderr)
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

func runStatefulSnapshot(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" stateful snapshot", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	group := fs.String("group", "", "StatefulGroup name")
	member := fs.Int("member", -1, "stateful member ordinal")
	backupID := fs.String("backup-id", "", "backup ID to use")
	operationID := fs.String("operation-id", "", "operation ID to use")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	retention := fs.String("retention", templates.DefaultStatefulBackupRetention, "backup retention duration")
	reason := fs.String("reason", "", "snapshot reason")
	run := fs.Bool("run", true, "run the saga after creating it")
	flagArgs, positionals, err := splitStatefulSnapshotArgs(args)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful snapshot", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful snapshot", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful snapshot", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *group == "" && len(positionals) == 1 {
		*group = positionals[0]
	}
	if *group == "" || *member < 0 {
		return writeStatefulCommandError(binary, "stateful snapshot", *flags.format, *flags.traceID, errors.New("StatefulGroup name and --member are required"), stdout, stderr)
	}
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeStatefulCommandError(binary, "stateful snapshot", *flags.format, *flags.traceID, errors.New("stateful snapshot currently requires --direct mode"), stdout, stderr)
	}
	req := templates.StatefulBackupRequest{
		SagaID:      *sagaID,
		OperationID: *operationID,
		BackupID:    *backupID,
		Group:       *group,
		Env:         loaded.Config.Env,
		Members:     []int{*member},
		Member:      *member,
		Reason:      *reason,
		Retention:   *retention,
		TraceID:     *flags.traceID,
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
	}
	result, err := createAndMaybeRunStatefulBackup(nilContext(), binary, loaded.Config, req, *run, false)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful snapshot", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeStatefulBackupRestoreResult(binary, "stateful snapshot", *flags.format, *flags.traceID, *result, stdout, stderr)
}

func runStatefulBackup(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "plan" {
		return writeStatefulCommandError(binary, "stateful backup", defaultString(root.Format, "human"), root.TraceID, errors.New("expected stateful backup plan"), stdout, stderr)
	}
	fs := flag.NewFlagSet(binary+" stateful backup plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	group := fs.String("group", "", "StatefulGroup name")
	membersValue := fs.String("members", "", "comma-separated StatefulGroup member ordinals")
	backupID := fs.String("backup-id", "", "backup ID to use")
	operationID := fs.String("operation-id", "", "operation ID to use")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	retention := fs.String("retention", templates.DefaultStatefulBackupRetention, "backup retention duration")
	flagArgs, positionals, err := splitStatefulBackupArgs(args[1:])
	if err != nil {
		return writeStatefulCommandError(binary, "stateful backup plan", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful backup plan", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful backup plan", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *group == "" && len(positionals) == 1 {
		*group = positionals[0]
	}
	members, err := parseMemberOrdinals(*membersValue)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful backup plan", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if *group == "" || len(members) == 0 {
		return writeStatefulCommandError(binary, "stateful backup plan", *flags.format, *flags.traceID, errors.New("StatefulGroup name and --members are required"), stdout, stderr)
	}
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	req := templates.StatefulBackupRequest{
		SagaID:      *sagaID,
		OperationID: *operationID,
		BackupID:    *backupID,
		Group:       *group,
		Env:         loaded.Config.Env,
		Members:     members,
		Retention:   *retention,
		TraceID:     *flags.traceID,
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
	}
	result, err := createAndMaybeRunStatefulBackup(nilContext(), binary, loaded.Config, req, false, true)
	if err != nil {
		return writeStatefulCommandError(binary, "stateful backup plan", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeStatefulBackupRestoreResult(binary, "stateful backup plan", *flags.format, *flags.traceID, *result, stdout, stderr)
}

func runStatefulRestore(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "apply") {
		return writeStatefulCommandError(binary, "stateful restore", defaultString(root.Format, "human"), root.TraceID, errors.New("expected stateful restore plan or apply"), stdout, stderr)
	}
	command := args[0]
	fs := flag.NewFlagSet(binary+" stateful restore "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	group := fs.String("group", "", "StatefulGroup name")
	member := fs.Int("member", -1, "stateful member ordinal")
	backupID := fs.String("backup-id", "", "backup ID to restore")
	restoreID := fs.String("restore-id", "", "restore ID to use")
	operationID := fs.String("operation-id", "", "operation ID to use")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	reason := fs.String("reason", "", "restore reason")
	approvalID := fs.String("approval-id", "", "approval ID for restore apply")
	run := fs.Bool("run", true, "run the saga after creating it")
	flagArgs, positionals, err := splitStatefulRestoreArgs(args[1:])
	if err != nil {
		return writeStatefulCommandError(binary, "stateful restore "+command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeStatefulCommandError(binary, "stateful restore "+command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeStatefulCommandError(binary, "stateful restore "+command, *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *group == "" && len(positionals) == 1 {
		*group = positionals[0]
	}
	if *group == "" || *member < 0 || *backupID == "" {
		return writeStatefulCommandError(binary, "stateful restore "+command, *flags.format, *flags.traceID, errors.New("StatefulGroup name, --member, and --backup-id are required"), stdout, stderr)
	}
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if command == "apply" && loaded.Config.Mode != config.ModeDirect {
		return writeStatefulCommandError(binary, "stateful restore apply", *flags.format, *flags.traceID, errors.New("stateful restore apply currently requires --direct mode"), stdout, stderr)
	}
	req := templates.StatefulRestoreRequest{
		SagaID:      *sagaID,
		OperationID: *operationID,
		RestoreID:   *restoreID,
		BackupID:    *backupID,
		Group:       *group,
		Env:         loaded.Config.Env,
		Member:      *member,
		Reason:      *reason,
		ApprovalID:  *approvalID,
		TraceID:     *flags.traceID,
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
	}
	result, err := createAndMaybeRunStatefulRestore(nilContext(), binary, loaded.Config, req, *run, command == "plan")
	if err != nil {
		return writeStatefulCommandError(binary, "stateful restore "+command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeStatefulBackupRestoreResult(binary, "stateful restore "+command, *flags.format, *flags.traceID, *result, stdout, stderr)
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

func writeStatefulStatusResult(binary, format, traceID string, result client.StatefulGroup, stdout, stderr io.Writer) int {
	switch {
	case format == "human" || format == "text":
		fmt.Fprintf(stdout, "StatefulGroup %s", result.Group)
		if result.Env != "" {
			fmt.Fprintf(stdout, " env=%s", result.Env)
		}
		fmt.Fprintf(stdout, " health=%s replicas=%d\n", firstNonEmptyCLI(result.Health, "unknown"), result.Replicas)
		if result.OperationID != "" {
			fmt.Fprintf(stdout, "operation: %s", result.OperationID)
			if result.OperationKind != "" {
				fmt.Fprintf(stdout, " kind=%s", result.OperationKind)
			}
			if result.OperationState != "" {
				fmt.Fprintf(stdout, " state=%s", result.OperationState)
			}
			fmt.Fprintln(stdout)
		}
		if result.Lease != nil {
			fmt.Fprintf(stdout, "lease: owner=%s token=%s generation=%d expires=%s\n", result.Lease.Owner, result.Lease.Token, result.Lease.Generation, result.Lease.ExpiresAt)
		}
		if len(result.Members) == 0 {
			fmt.Fprintln(stdout, "members: none")
		} else {
			fmt.Fprintln(stdout, "members:")
			for _, member := range result.Members {
				fmt.Fprintf(stdout, "- member %d health=%s phase=%s generation=%d", member.Member, firstNonEmptyCLI(member.Health, "unknown"), firstNonEmptyCLI(member.Phase, "unknown"), member.Generation)
				if member.ExpectedGeneration > 0 && member.ExpectedGeneration != member.Generation {
					fmt.Fprintf(stdout, " expected_generation=%d", member.ExpectedGeneration)
				}
				if member.Role != "" {
					fmt.Fprintf(stdout, " role=%s", member.Role)
				}
				if member.Zone != "" {
					fmt.Fprintf(stdout, " zone=%s", member.Zone)
				}
				if member.InstanceID != "" {
					fmt.Fprintf(stdout, " instance=%s", member.InstanceID)
				}
				if member.VolumeID != "" {
					fmt.Fprintf(stdout, " volume=%s", member.VolumeID)
				}
				if member.DNSName != "" {
					fmt.Fprintf(stdout, " dns=%s", member.DNSName)
				}
				if member.ReleaseID != "" {
					fmt.Fprintf(stdout, " release=%s", member.ReleaseID)
				}
				if member.RecipeStatus != "" {
					fmt.Fprintf(stdout, " recipe=%s", member.RecipeStatus)
				}
				fmt.Fprintln(stdout)
				if member.Lease != nil {
					fmt.Fprintf(stdout, "  lease: owner=%s token=%s generation=%d expires=%s\n", member.Lease.Owner, member.Lease.Token, member.Lease.Generation, member.Lease.ExpiresAt)
				}
				for _, op := range member.ProviderOperations {
					fmt.Fprintf(stdout, "  provider-op: %s %s %s", op.Provider, op.Kind, op.ID)
					if op.Description != "" {
						fmt.Fprintf(stdout, " %s", op.Description)
					}
					fmt.Fprintln(stdout)
				}
				for _, finding := range member.Findings {
					fmt.Fprintf(stdout, "  finding: %s %s\n", finding.Code, finding.Summary)
				}
			}
		}
		if len(result.Backups) == 0 {
			fmt.Fprintln(stdout, "backups: none")
		} else {
			fmt.Fprintln(stdout, "backups:")
			for _, backup := range result.Backups {
				fmt.Fprintf(stdout, "- %s member=%d status=%s", backup.BackupID, backup.Member, firstNonEmptyCLI(backup.Status, "unknown"))
				if backup.SnapshotID != "" {
					fmt.Fprintf(stdout, " snapshot=%s", backup.SnapshotID)
				}
				if backup.VolumeID != "" {
					fmt.Fprintf(stdout, " volume=%s", backup.VolumeID)
				}
				if backup.Stale {
					fmt.Fprint(stdout, " stale=true")
				}
				if backup.ExpiresAt != "" {
					fmt.Fprintf(stdout, " expires=%s", backup.ExpiresAt)
				}
				fmt.Fprintln(stdout)
			}
		}
		if len(result.Findings) > 0 {
			fmt.Fprintln(stdout, "findings:")
			for _, finding := range result.Findings {
				fmt.Fprintf(stdout, "- %s: %s\n", finding.Code, finding.Summary)
			}
		}
		return ExitSuccess
	case isJSONFormat(format):
		if err := writeJSON(stdout, format, statefulStatusOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s stateful status: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeStatefulCommandError(binary, "stateful status", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeStatefulDoctorResult(binary, format, traceID string, result client.Doctor, stdout, stderr io.Writer) int {
	switch {
	case format == "human" || format == "text":
		printDoctorHuman(stdout, result)
		return ExitSuccess
	case isJSONFormat(format):
		if err := writeJSON(stdout, format, statefulDoctorOutput{OK: true, TraceID: traceID, Doctor: result}); err != nil {
			fmt.Fprintf(stderr, "%s stateful doctor: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeStatefulCommandError(binary, "stateful doctor", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeStatefulSolveResult(binary, format string, graph agent.ActionGraph, stdout, stderr io.Writer) int {
	switch {
	case format == "human" || format == "text":
		printSolveHuman(stdout, graph)
		return ExitSuccess
	case isJSONFormat(format):
		if err := writeJSON(stdout, format, statefulSolveOutput{OK: true, ActionGraph: graph}); err != nil {
			fmt.Fprintf(stderr, "%s stateful solve: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeStatefulCommandError(binary, "stateful solve", format, graph.TraceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func statefulGroupFromStatus(status client.Status, group string) (client.StatefulGroup, bool) {
	for _, item := range status.StatefulGroups {
		if item.Group == group {
			return item, true
		}
	}
	return client.StatefulGroup{}, false
}

func statefulMemberInstanceID(ctx context.Context, cfg config.Config, group string, member int) (string, error) {
	if member < 0 {
		return "", nil
	}
	store, err := client.OpenObjectStore(cfg)
	if err != nil {
		return "", err
	}
	doc, err := state.NewClient(store).GetStatefulMemberControl(ctx, group, member)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(doc.Control.InstanceID) == "" {
		return "", fmt.Errorf("StatefulGroup %q member %d has no instance provider ID in object state", group, member)
	}
	return doc.Control.InstanceID, nil
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

func splitStatefulStatusArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"group":        true,
		"mode":         true,
		"provider":     true,
		"region":       true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func splitStatefulSolveArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"goal":         true,
		"group":        true,
		"mode":         true,
		"provider":     true,
		"region":       true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func splitStatefulLogsArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"group":        true,
		"limit":        true,
		"member":       true,
		"mode":         true,
		"provider":     true,
		"region":       true,
		"since":        true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func splitStatefulMetricsArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"from":         true,
		"group":        true,
		"member":       true,
		"metric":       true,
		"mode":         true,
		"period":       true,
		"provider":     true,
		"region":       true,
		"since":        true,
		"state":        true,
		"state-bucket": true,
		"to":           true,
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

func splitStatefulSnapshotArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"backup-id":    true,
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
		"retention":    true,
		"saga-id":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func splitStatefulBackupArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"backup-id":    true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"group":        true,
		"members":      true,
		"mode":         true,
		"operation-id": true,
		"provider":     true,
		"region":       true,
		"retention":    true,
		"saga-id":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func splitStatefulRestoreArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"approval-id":  true,
		"backup-id":    true,
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
		"restore-id":   true,
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
	fmt.Fprintln(w, "  status     Show StatefulGroup health, members, backups, leases, and provider IDs")
	fmt.Fprintln(w, "  doctor     Diagnose StatefulGroup health from object state")
	fmt.Fprintln(w, "  solve      Render an agent action graph for StatefulGroup recovery")
	fmt.Fprintln(w, "  logs       Query StatefulGroup or member logs")
	fmt.Fprintln(w, "  metrics    Query StatefulGroup or member metrics")
	fmt.Fprintln(w, "  replace-member  Replace one failed StatefulGroup member through a saga")
	fmt.Fprintln(w, "  snapshot   Snapshot one StatefulGroup member volume")
	fmt.Fprintln(w, "  backup     Render StatefulGroup backup saga plans")
	fmt.Fprintln(w, "  restore    Plan or apply StatefulGroup restore sagas")
	fmt.Fprintln(w, "  resume     Resume a stateful saga")
	fmt.Fprintln(w, "  watch      Watch stateful saga events")
	fmt.Fprintln(w, "  cancel     Cancel a registered stateful saga")
	fmt.Fprintln(w, "  compensate Compensate a registered stateful saga")
}
