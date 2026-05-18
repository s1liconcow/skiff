package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps/approval"
	"github.com/s1liconcow/skiff/internal/saga/steps/builtin"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state"
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

type canarySagaOutput struct {
	OK      bool             `json:"ok"`
	TraceID string           `json:"trace_id,omitempty"`
	Result  canarySagaResult `json:"result"`
}

type canarySagaResult struct {
	SagaID       string                     `json:"saga_id"`
	OperationID  string                     `json:"operation_id,omitempty"`
	Service      string                     `json:"service"`
	Env          string                     `json:"env"`
	ReleaseID    string                     `json:"release_id"`
	Status       schema.SagaStatus          `json:"status"`
	Stage        int                        `json:"stage,omitempty"`
	Gate         string                     `json:"gate,omitempty"`
	NextAction   string                     `json:"next_action,omitempty"`
	CurrentSteps []string                   `json:"current_steps,omitempty"`
	Execution    *sagastate.ExecutionResult `json:"execution,omitempty"`
	Inspect      *sagastate.InspectResult   `json:"inspect,omitempty"`
}

type statefulOrderedSagaOutput struct {
	OK      bool                      `json:"ok"`
	TraceID string                    `json:"trace_id,omitempty"`
	Result  statefulOrderedSagaResult `json:"result"`
}

type statefulReplacementSagaOutput struct {
	OK      bool                          `json:"ok"`
	TraceID string                        `json:"trace_id,omitempty"`
	Result  statefulReplacementSagaResult `json:"result"`
}

type statefulBackupRestoreOutput struct {
	OK      bool                        `json:"ok"`
	TraceID string                      `json:"trace_id,omitempty"`
	Result  statefulBackupRestoreResult `json:"result"`
}

type statefulOrderedSagaResult struct {
	SagaID       string                     `json:"saga_id"`
	OperationID  string                     `json:"operation_id,omitempty"`
	Group        string                     `json:"group"`
	Env          string                     `json:"env,omitempty"`
	ReleaseID    string                     `json:"release_id"`
	Members      []int                      `json:"members,omitempty"`
	Status       schema.SagaStatus          `json:"status"`
	NextAction   string                     `json:"next_action,omitempty"`
	CurrentSteps []string                   `json:"current_steps,omitempty"`
	Execution    *sagastate.ExecutionResult `json:"execution,omitempty"`
	Inspect      *sagastate.InspectResult   `json:"inspect,omitempty"`
}

type statefulReplacementSagaResult struct {
	SagaID             string                     `json:"saga_id"`
	OperationID        string                     `json:"operation_id,omitempty"`
	Group              string                     `json:"group"`
	Env                string                     `json:"env,omitempty"`
	Member             int                        `json:"member"`
	Status             schema.SagaStatus          `json:"status"`
	NextAction         string                     `json:"next_action,omitempty"`
	CurrentSteps       []string                   `json:"current_steps,omitempty"`
	Facts              []schema.Fact              `json:"facts,omitempty"`
	Hypotheses         []schema.Fact              `json:"hypotheses,omitempty"`
	RecommendedActions []recommendedAction        `json:"recommended_actions,omitempty"`
	Execution          *sagastate.ExecutionResult `json:"execution,omitempty"`
	Inspect            *sagastate.InspectResult   `json:"inspect,omitempty"`
	Replacement        json.RawMessage            `json:"replacement,omitempty"`
}

type statefulBackupRestoreResult struct {
	SagaID       string                     `json:"saga_id"`
	OperationID  string                     `json:"operation_id,omitempty"`
	Command      string                     `json:"command"`
	Group        string                     `json:"group"`
	Env          string                     `json:"env,omitempty"`
	Members      []int                      `json:"members,omitempty"`
	Member       int                        `json:"member,omitempty"`
	BackupID     string                     `json:"backup_id,omitempty"`
	RestoreID    string                     `json:"restore_id,omitempty"`
	Status       schema.SagaStatus          `json:"status"`
	NextAction   string                     `json:"next_action,omitempty"`
	CurrentSteps []string                   `json:"current_steps,omitempty"`
	Execution    *sagastate.ExecutionResult `json:"execution,omitempty"`
	Inspect      *sagastate.InspectResult   `json:"inspect,omitempty"`
	Plan         *sagastate.CreateRequest   `json:"plan,omitempty"`
}

var (
	openSagaObjectStore = client.OpenObjectStore
	newSagaProvider     = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		return newCLIProvider(cfg, store)
	}
)

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
	case "start":
		return runSagaStart(binary, args[1:], root, stdout, stderr)
	case "resume":
		return runSagaResume(binary, args[1:], root, stdout, stderr)
	case "watch":
		return runSagaWatch(binary, args[1:], root, stdout, stderr)
	case "cancel", "compensate":
		return runSagaSkeleton(binary, args[0], args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printSagaUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "saga", root.Format, root.TraceID, fmt.Errorf("unknown saga command %q", args[0]), stdout, stderr)
	}
}

func runSagaWatch(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" saga watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	sagaID := fs.String("saga", "", "saga ID")
	limit := fs.Int("limit", 0, "maximum replay events before watching")
	afterID := fs.String("after", "", "resume after event ID")

	flagArgs, positionals, err := splitSagaWatchArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "saga", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
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
	skiffClient, err := client.New(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return runEventsWatch(eventsWatchContext(), binary, skiffClient, client.EventWatchOptions{
		EventOptions: client.EventOptions{
			Scope:   "saga",
			Saga:    *sagaID,
			Limit:   *limit,
			TraceID: *flags.traceID,
		},
		AfterID:      *afterID,
		PollInterval: eventsWatchPollInterval,
	}, *flags.format, *flags.traceID, stdout, stderr)
}

func runSagaStart(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" saga start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	group := fs.String("group", "", "StatefulGroup name")
	releaseID := fs.String("release-id", "", "release ID to deploy")
	operationID := fs.String("operation-id", "", "operation ID to use")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	stagesValue := fs.String("stages", "5,25,100", "comma-separated canary stages by percent")
	membersValue := fs.String("members", "", "comma-separated StatefulGroup member ordinals")
	maxUnavailable := fs.Int("max-unavailable", 1, "maximum unavailable StatefulGroup members during ordered update")
	bake := fs.String("bake", templates.DefaultCanaryBake, "canary bake duration")
	metric := fs.String("metric", "", "metric gate name")
	comparator := fs.String("comparator", "<=", "metric gate comparator")
	threshold := fs.Float64("threshold", 0, "metric gate threshold")
	run := fs.Bool("run", true, "run the saga after creating it")

	flagArgs, positionals, err := splitSagaStartArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "saga", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) == 0 {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New("saga kind is required"), stdout, stderr)
	}
	kind := positionals[0]
	if kind != templates.CanaryDeployCommand && kind != templates.DeploymentCanaryKind && kind != templates.StatefulOrderedUpdateKind {
		return runSagaSkeleton(binary, "start", args, root, stdout, stderr)
	}
	if len(positionals) > 2 {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[2]), stdout, stderr)
	}
	if len(positionals) == 2 && *service == "" {
		*service = positionals[1]
	}
	if kind == templates.StatefulOrderedUpdateKind {
		if len(positionals) == 2 && *group == "" {
			*group = positionals[1]
		}
		if *group == "" || *releaseID == "" {
			return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New("--group and --release-id are required for stateful.ordered_update"), stdout, stderr)
		}
		loaded, err := flags.load(binary, root, fs)
		if err != nil {
			return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
		}
		if loaded.Config.Mode != config.ModeDirect {
			return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New("stateful ordered update saga start currently requires --direct mode"), stdout, stderr)
		}
		members, err := parseMemberOrdinals(*membersValue)
		if err != nil {
			return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
		}
		req := templates.StatefulOrderedUpdateRequest{
			SagaID:         *sagaID,
			OperationID:    *operationID,
			Group:          *group,
			Env:            loaded.Config.Env,
			ReleaseID:      *releaseID,
			Members:        members,
			MaxUnavailable: *maxUnavailable,
			Actor:          schema.Actor{ID: "skiff-cli", Type: "user"},
			TraceID:        *flags.traceID,
		}
		result, err := createAndMaybeRunStatefulOrdered(nilContext(), binary, loaded.Config, req, *run)
		if err != nil {
			return writeClientError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
		}
		return writeStatefulOrderedSagaResult(binary, "saga start", *flags.format, *flags.traceID, *result, stdout, stderr)
	}
	if *service == "" || *releaseID == "" {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New("--service and --release-id are required for canary-deploy"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New("canary saga start currently requires --direct mode"), stdout, stderr)
	}
	stages, err := parseCanaryStages(*stagesValue)
	if err != nil {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	req := templates.CanaryRequest{
		SagaID:       *sagaID,
		OperationID:  *operationID,
		Service:      *service,
		Env:          loaded.Config.Env,
		ReleaseID:    *releaseID,
		Stages:       stages,
		BakeDuration: *bake,
		Actor:        schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:      *flags.traceID,
	}
	if strings.TrimSpace(*metric) != "" {
		req.MetricGates = []templates.MetricGate{{Metric: *metric, Comparator: *comparator, Threshold: *threshold}}
	}
	result, err := createAndMaybeRunCanary(nilContext(), binary, loaded.Config, req, *run)
	if err != nil {
		return writeClientError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeCanarySagaResult(binary, "saga start", *flags.format, *flags.traceID, *result, stdout, stderr)
}

func runSagaResume(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" saga resume", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	sagaID := fs.String("saga", "", "saga ID")
	stepID := fs.String("step", "", "step ID to resume")
	flagArgs, positionals, err := splitSagaResumeArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "saga", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
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
	_ = stepID
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New("saga resume currently requires --direct mode"), stdout, stderr)
	}
	store, err := openSagaObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	cloud, err := newSagaProvider(loaded.Config, store)
	if err != nil {
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	sagas := sagastate.NewStore(store)
	execution, err := (&sagastate.Executor{
		Store: sagas,
		Steps: builtin.New(builtin.Options{Store: store, Provider: cloud, Binary: binary}),
		Owner: "skiff-cli",
	}).Execute(nilContext(), *sagaID)
	if err != nil {
		return writeClientError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	inspect, err := sagas.Inspect(nilContext(), *sagaID)
	if err != nil {
		return writeClientError(binary, "saga", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result := canaryResultFromInspect(*inspect, execution)
	return writeCanarySagaResult(binary, "saga resume", *flags.format, *flags.traceID, result, stdout, stderr)
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
	store, err := openSagaObjectStore(loaded.Config)
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
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
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
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
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
	store, err := openSagaObjectStore(loaded.Config)
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
		return writeClientCommandError(binary, "saga", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func createAndMaybeRunCanary(ctx context.Context, binary string, cfg config.Config, req templates.CanaryRequest, run bool) (*canarySagaResult, error) {
	store, err := openSagaObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	req = templates.NormalizeCanaryRequest(req)
	createReq, err := templates.DeploymentCanary(req)
	if err != nil {
		return nil, err
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		return nil, err
	}
	var execution *sagastate.ExecutionResult
	if run {
		cloud, err := newSagaProvider(cfg, store)
		if err != nil {
			return nil, err
		}
		execution, err = (&sagastate.Executor{
			Store: sagas,
			Steps: builtin.New(builtin.Options{Store: store, Provider: cloud, Binary: binary}),
			Owner: "skiff-cli",
		}).Execute(ctx, req.SagaID)
		if err != nil {
			return nil, err
		}
	}
	inspect, err := sagas.Inspect(ctx, req.SagaID)
	if err != nil {
		return nil, err
	}
	result := canaryResultFromInspect(*inspect, execution)
	return &result, nil
}

func createAndMaybeRunStatefulOrdered(ctx context.Context, binary string, cfg config.Config, req templates.StatefulOrderedUpdateRequest, run bool) (*statefulOrderedSagaResult, error) {
	store, err := openSagaObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	req = templates.NormalizeStatefulOrderedUpdateRequest(req)
	if len(req.Members) == 0 {
		members, err := orderedUpdateMembersFromControl(ctx, store, req.Group)
		if err != nil {
			return nil, err
		}
		req.Members = members
	}
	createReq, err := templates.StatefulOrderedUpdate(req)
	if err != nil {
		return nil, err
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		return nil, err
	}
	var execution *sagastate.ExecutionResult
	if run {
		cloud, err := newSagaProvider(cfg, store)
		if err != nil {
			return nil, err
		}
		execution, err = (&sagastate.Executor{
			Store: sagas,
			Steps: builtin.New(builtin.Options{Store: store, Provider: cloud, Binary: binary}),
			Owner: "skiff-cli",
		}).Execute(ctx, req.SagaID)
		if err != nil {
			return nil, err
		}
	}
	inspect, err := sagas.Inspect(ctx, req.SagaID)
	if err != nil {
		return nil, err
	}
	result := statefulOrderedResultFromInspect(*inspect, execution)
	return &result, nil
}

func createAndMaybeRunStatefulReplacement(ctx context.Context, binary string, cfg config.Config, req templates.StatefulReplaceMemberRequest, run bool) (*statefulReplacementSagaResult, error) {
	store, err := openSagaObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	req = templates.NormalizeStatefulReplaceMemberRequest(req)
	createReq, err := templates.StatefulReplaceMember(req)
	if err != nil {
		return nil, err
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		return nil, err
	}
	var execution *sagastate.ExecutionResult
	if run {
		cloud, err := newSagaProvider(cfg, store)
		if err != nil {
			return nil, err
		}
		execution, err = (&sagastate.Executor{
			Store: sagas,
			Steps: builtin.New(builtin.Options{Store: store, Provider: cloud, Binary: binary}),
			Owner: "skiff-cli",
		}).Execute(ctx, req.SagaID)
		if err != nil {
			return nil, err
		}
	}
	inspect, err := sagas.Inspect(ctx, req.SagaID)
	if err != nil {
		return nil, err
	}
	result := statefulReplacementResultFromInspect(*inspect, execution)
	return &result, nil
}

func createAndMaybeRunStatefulBackup(ctx context.Context, binary string, cfg config.Config, req templates.StatefulBackupRequest, run, dryRun bool) (*statefulBackupRestoreResult, error) {
	req = templates.NormalizeStatefulBackupRequest(req)
	createReq, err := templates.StatefulBackup(req)
	if err != nil {
		return nil, err
	}
	result := statefulBackupResultFromRequest(req, schema.SagaPending, nil, nil)
	if dryRun {
		result.NextAction = "create_saga"
		result.Plan = &createReq
		return &result, nil
	}
	store, err := openSagaObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		return nil, err
	}
	var execution *sagastate.ExecutionResult
	if run {
		cloud, err := newSagaProvider(cfg, store)
		if err != nil {
			return nil, err
		}
		execution, err = (&sagastate.Executor{
			Store: sagas,
			Steps: builtin.New(builtin.Options{Store: store, Provider: cloud, Binary: binary}),
			Owner: "skiff-cli",
		}).Execute(ctx, req.SagaID)
		if err != nil {
			return nil, err
		}
	}
	inspect, err := sagas.Inspect(ctx, req.SagaID)
	if err != nil {
		return nil, err
	}
	result = statefulBackupResultFromInspect(*inspect, execution)
	return &result, nil
}

func createAndMaybeRunStatefulRestore(ctx context.Context, binary string, cfg config.Config, req templates.StatefulRestoreRequest, run, dryRun bool) (*statefulBackupRestoreResult, error) {
	req = templates.NormalizeStatefulRestoreRequest(req)
	createReq, err := templates.StatefulRestore(req)
	if err != nil {
		return nil, err
	}
	result := statefulRestoreResultFromRequest(req, schema.SagaPending, nil, nil)
	if dryRun {
		result.NextAction = "create_saga"
		result.Plan = &createReq
		return &result, nil
	}
	store, err := openSagaObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		return nil, err
	}
	var execution *sagastate.ExecutionResult
	if run {
		cloud, err := newSagaProvider(cfg, store)
		if err != nil {
			return nil, err
		}
		execution, err = (&sagastate.Executor{
			Store: sagas,
			Steps: builtin.New(builtin.Options{Store: store, Provider: cloud, Binary: binary}),
			Owner: "skiff-cli",
		}).Execute(ctx, req.SagaID)
		if err != nil {
			return nil, err
		}
	}
	inspect, err := sagas.Inspect(ctx, req.SagaID)
	if err != nil {
		return nil, err
	}
	result = statefulRestoreResultFromInspect(*inspect, execution)
	return &result, nil
}

func orderedUpdateMembersFromControl(ctx context.Context, store objstore.ObjectStore, group string) ([]int, error) {
	doc, err := state.NewClient(store).GetStatefulGroupControl(ctx, group)
	if err != nil {
		return nil, err
	}
	members := make([]int, 0, len(doc.Control.Members))
	for _, member := range doc.Control.Members {
		members = append(members, member.Member)
	}
	if len(members) == 0 {
		for i := 0; i < doc.Control.Replicas; i++ {
			members = append(members, i)
		}
	}
	sort.Ints(members)
	return members, nil
}

func writeCanarySagaResult(binary, command, format, traceID string, result canarySagaResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "canary saga %s status=%s\n", result.SagaID, result.Status)
		fmt.Fprintf(stdout, "release: %s\n", result.ReleaseID)
		if result.OperationID != "" {
			fmt.Fprintf(stdout, "operation: %s\n", result.OperationID)
		}
		if result.Stage > 0 {
			fmt.Fprintf(stdout, "stage: %d%%\n", result.Stage)
		}
		if result.Gate != "" {
			fmt.Fprintf(stdout, "gate: %s\n", result.Gate)
		}
		if result.NextAction != "" {
			fmt.Fprintf(stdout, "next: %s\n", result.NextAction)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(canarySagaOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "saga", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeStatefulOrderedSagaResult(binary, command, format, traceID string, result statefulOrderedSagaResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "stateful ordered update saga %s status=%s\n", result.SagaID, result.Status)
		fmt.Fprintf(stdout, "group: %s\n", result.Group)
		fmt.Fprintf(stdout, "release: %s\n", result.ReleaseID)
		if result.OperationID != "" {
			fmt.Fprintf(stdout, "operation: %s\n", result.OperationID)
		}
		if result.NextAction != "" {
			fmt.Fprintf(stdout, "next: %s\n", result.NextAction)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(statefulOrderedSagaOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "saga", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeStatefulReplacementSagaResult(binary, command, format, traceID string, result statefulReplacementSagaResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "stateful replacement saga %s status=%s\n", result.SagaID, result.Status)
		fmt.Fprintf(stdout, "member: %s/%d\n", result.Group, result.Member)
		if result.OperationID != "" {
			fmt.Fprintf(stdout, "operation: %s\n", result.OperationID)
		}
		if result.NextAction != "" {
			fmt.Fprintf(stdout, "next: %s\n", result.NextAction)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(statefulReplacementSagaOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "saga", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func writeStatefulBackupRestoreResult(binary, command, format, traceID string, result statefulBackupRestoreResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "stateful %s saga %s status=%s\n", result.Command, result.SagaID, result.Status)
		fmt.Fprintf(stdout, "group: %s\n", result.Group)
		if result.BackupID != "" {
			fmt.Fprintf(stdout, "backup: %s\n", result.BackupID)
		}
		if result.RestoreID != "" {
			fmt.Fprintf(stdout, "restore: %s\n", result.RestoreID)
		}
		if result.NextAction != "" {
			fmt.Fprintf(stdout, "next: %s\n", result.NextAction)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(statefulBackupRestoreOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "stateful", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func canaryResultFromInspect(inspect sagastate.InspectResult, execution *sagastate.ExecutionResult) canarySagaResult {
	var params struct {
		Service     string `json:"service"`
		Env         string `json:"env"`
		OperationID string `json:"operation_id"`
		ReleaseID   string `json:"release_id"`
	}
	_ = json.Unmarshal(inspect.Intent.Params, &params)
	result := canarySagaResult{
		SagaID:       inspect.SagaID,
		OperationID:  params.OperationID,
		Service:      firstNonEmptyString(params.Service, inspect.Target.Name),
		Env:          params.Env,
		ReleaseID:    params.ReleaseID,
		Status:       inspect.Status,
		CurrentSteps: append([]string(nil), inspect.CurrentSteps...),
		Execution:    execution,
		Inspect:      &inspect,
	}
	for _, ref := range inspect.Control.StepResults {
		stage, gate, next := canaryProgressFromStep(ref)
		if stage > 0 {
			result.Stage = stage
		}
		if gate != "" {
			result.Gate = gate
		}
		if next != "" {
			result.NextAction = next
		}
	}
	switch inspect.Status {
	case schema.SagaSucceeded:
		result.NextAction = "complete"
	case schema.SagaFailed:
		result.NextAction = "inspect_failure"
	default:
		if result.NextAction == "" {
			result.NextAction = "resume"
		}
	}
	return result
}

func statefulOrderedResultFromInspect(inspect sagastate.InspectResult, execution *sagastate.ExecutionResult) statefulOrderedSagaResult {
	var params struct {
		Group       string `json:"group"`
		Env         string `json:"env"`
		OperationID string `json:"operation_id"`
		ReleaseID   string `json:"release_id"`
		Members     []int  `json:"members"`
	}
	_ = json.Unmarshal(inspect.Intent.Params, &params)
	result := statefulOrderedSagaResult{
		SagaID:       inspect.SagaID,
		OperationID:  params.OperationID,
		Group:        firstNonEmptyString(params.Group, inspect.Target.Name),
		Env:          params.Env,
		ReleaseID:    params.ReleaseID,
		Members:      append([]int(nil), params.Members...),
		Status:       inspect.Status,
		CurrentSteps: append([]string(nil), inspect.CurrentSteps...),
		Execution:    execution,
		Inspect:      &inspect,
	}
	switch inspect.Status {
	case schema.SagaSucceeded:
		result.NextAction = "complete"
	case schema.SagaFailed:
		result.NextAction = "inspect_failure"
	default:
		result.NextAction = "resume"
	}
	return result
}

func statefulReplacementResultFromInspect(inspect sagastate.InspectResult, execution *sagastate.ExecutionResult) statefulReplacementSagaResult {
	var params struct {
		Group       string `json:"group"`
		Env         string `json:"env"`
		Member      int    `json:"member"`
		OperationID string `json:"operation_id"`
	}
	_ = json.Unmarshal(inspect.Intent.Params, &params)
	result := statefulReplacementSagaResult{
		SagaID:       inspect.SagaID,
		OperationID:  params.OperationID,
		Group:        firstNonEmptyString(params.Group, inspect.Target.Name),
		Env:          params.Env,
		Member:       params.Member,
		Status:       inspect.Status,
		CurrentSteps: append([]string(nil), inspect.CurrentSteps...),
		Facts: []schema.Fact{
			{Type: "stateful_member", Message: fmt.Sprintf("%s/%d", firstNonEmptyString(params.Group, inspect.Target.Name), params.Member)},
			{Type: "operation", Message: params.OperationID},
		},
		Execution: execution,
		Inspect:   &inspect,
	}
	for _, ref := range inspect.Control.StepResults {
		if ref.StepID == "replace-member" && len(ref.Result) > 0 {
			result.Replacement = append(json.RawMessage(nil), ref.Result...)
			mergeStatefulReplacementStepPayload(&result, ref.Result)
		}
	}
	switch inspect.Status {
	case schema.SagaSucceeded:
		result.NextAction = "complete"
	case schema.SagaFailed:
		result.NextAction = "inspect_failure"
	default:
		result.NextAction = "resume"
	}
	result.RecommendedActions = statefulReplacementRecommendedActions(result)
	return result
}

func mergeStatefulReplacementStepPayload(result *statefulReplacementSagaResult, raw json.RawMessage) {
	var payload struct {
		Facts      []schema.Fact `json:"facts"`
		Hypotheses []schema.Fact `json:"hypotheses"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return
	}
	if len(payload.Facts) > 0 {
		result.Facts = payload.Facts
	}
	if len(payload.Hypotheses) > 0 {
		result.Hypotheses = payload.Hypotheses
	}
}

func statefulReplacementRecommendedActions(result statefulReplacementSagaResult) []recommendedAction {
	actions := []recommendedAction{
		{ID: "inspect_stateful_member", Command: fmt.Sprintf("skiff stateful inspect %s --format json", result.Group), Mutating: false},
		{ID: "inspect_saga", Command: fmt.Sprintf("skiff saga inspect %s --format json", result.SagaID), Mutating: false},
	}
	if result.Status != schema.SagaSucceeded {
		actions = append(actions, recommendedAction{
			ID:            "resume_replacement",
			Command:       fmt.Sprintf("skiff stateful resume %s --format json", result.SagaID),
			Mutating:      true,
			Safety:        "resumes from durable replacement progress",
			Reversibility: schema.Compensatable,
			Risk:          schema.RiskHigh,
		})
	}
	return actions
}

func statefulBackupResultFromRequest(req templates.StatefulBackupRequest, status schema.SagaStatus, execution *sagastate.ExecutionResult, inspect *sagastate.InspectResult) statefulBackupRestoreResult {
	return statefulBackupRestoreResult{
		SagaID:      req.SagaID,
		OperationID: req.OperationID,
		Command:     "backup",
		Group:       req.Group,
		Env:         req.Env,
		Members:     append([]int(nil), req.Members...),
		BackupID:    req.BackupID,
		Status:      status,
		NextAction:  "resume",
		Execution:   execution,
		Inspect:     inspect,
	}
}

func statefulRestoreResultFromRequest(req templates.StatefulRestoreRequest, status schema.SagaStatus, execution *sagastate.ExecutionResult, inspect *sagastate.InspectResult) statefulBackupRestoreResult {
	return statefulBackupRestoreResult{
		SagaID:      req.SagaID,
		OperationID: req.OperationID,
		Command:     "restore",
		Group:       req.Group,
		Env:         req.Env,
		Member:      req.Member,
		BackupID:    req.BackupID,
		RestoreID:   req.RestoreID,
		Status:      status,
		NextAction:  "resume",
		Execution:   execution,
		Inspect:     inspect,
	}
}

func statefulBackupResultFromInspect(inspect sagastate.InspectResult, execution *sagastate.ExecutionResult) statefulBackupRestoreResult {
	var params templates.StatefulBackupRequest
	_ = json.Unmarshal(inspect.Intent.Params, &params)
	result := statefulBackupResultFromRequest(params, inspect.Status, execution, &inspect)
	result.SagaID = inspect.SagaID
	result.CurrentSteps = append([]string(nil), inspect.CurrentSteps...)
	result.NextAction = statefulNextActionForSaga(inspect)
	return result
}

func statefulRestoreResultFromInspect(inspect sagastate.InspectResult, execution *sagastate.ExecutionResult) statefulBackupRestoreResult {
	var params templates.StatefulRestoreRequest
	_ = json.Unmarshal(inspect.Intent.Params, &params)
	result := statefulRestoreResultFromRequest(params, inspect.Status, execution, &inspect)
	result.SagaID = inspect.SagaID
	result.CurrentSteps = append([]string(nil), inspect.CurrentSteps...)
	result.NextAction = statefulNextActionForSaga(inspect)
	return result
}

func statefulNextActionForSaga(inspect sagastate.InspectResult) string {
	switch inspect.Status {
	case schema.SagaSucceeded:
		return "complete"
	case schema.SagaFailed:
		return "inspect_failure"
	default:
		for _, step := range inspect.Control.StepResults {
			if step.Status == "waiting" {
				return "approve_or_reject"
			}
		}
		return "resume"
	}
}

func canaryProgressFromStep(ref schema.StepResultRef) (int, string, string) {
	var body map[string]any
	if err := json.Unmarshal(ref.Result, &body); err != nil {
		return 0, "", ""
	}
	stage := intFromAny(body["stage_percent"])
	gate := ""
	if metric, ok := body["metric"].(string); ok {
		gate = metric
	}
	next := ""
	if value, ok := body["next_action"].(string); ok {
		next = value
	}
	return stage, gate, next
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
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

func splitSagaWatchArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"after":        true,
		"api-url":      true,
		"config":       true,
		"env":          true,
		"format":       true,
		"limit":        true,
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

func splitSagaStartArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":         true,
		"bake":            true,
		"comparator":      true,
		"config":          true,
		"env":             true,
		"format":          true,
		"group":           true,
		"max-unavailable": true,
		"members":         true,
		"metric":          true,
		"mode":            true,
		"operation-id":    true,
		"provider":        true,
		"region":          true,
		"release-id":      true,
		"saga-id":         true,
		"service":         true,
		"stages":          true,
		"state":           true,
		"state-bucket":    true,
		"threshold":       true,
		"trace-id":        true,
	}
	return splitArgs(args, valueFlags)
}

func splitSagaResumeArgs(args []string) ([]string, []string, error) {
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
		"step":         true,
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

func parseCanaryStages(value string) ([]templates.CanaryStage, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("canary stages are required")
	}
	parts := strings.Split(value, ",")
	stages := make([]templates.CanaryStage, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimSuffix(part, "%"))
		if part == "" {
			continue
		}
		percent, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("canary stage %q is invalid", part)
		}
		stages = append(stages, templates.CanaryStage{Percent: percent})
	}
	if len(stages) == 0 {
		return nil, errors.New("at least one canary stage is required")
	}
	return stages, nil
}

func parseMemberOrdinals(value string) ([]int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	members := make([]int, 0, len(parts))
	seen := map[int]bool{}
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		member, err := strconv.Atoi(item)
		if err != nil {
			return nil, fmt.Errorf("invalid member ordinal %q", item)
		}
		if member < 0 {
			return nil, fmt.Errorf("member ordinal %d must be non-negative", member)
		}
		if seen[member] {
			return nil, fmt.Errorf("duplicate member ordinal %d", member)
		}
		seen[member] = true
		members = append(members, member)
	}
	sort.Ints(members)
	return members, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
	fmt.Fprintln(w, "       "+binary+" saga start canary-deploy --service <service> --release-id <release> [flags]")
	fmt.Fprintln(w, "       "+binary+" saga approve|reject <saga> --step <step> [flags]")
	fmt.Fprintln(w, "       "+binary+" saga start|watch|resume|cancel|compensate <saga> [flags]")
}
