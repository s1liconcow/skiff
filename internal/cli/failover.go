package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/config"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps/builtin"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type failoverOutput struct {
	OK      bool           `json:"ok"`
	TraceID string         `json:"trace_id,omitempty"`
	Result  failoverResult `json:"result"`
}

type failoverResult struct {
	SagaID        string                     `json:"saga_id"`
	OperationID   string                     `json:"operation_id,omitempty"`
	Stack         string                     `json:"stack"`
	Service       string                     `json:"service,omitempty"`
	Database      string                     `json:"database"`
	Env           string                     `json:"env,omitempty"`
	FromRegion    string                     `json:"from_region"`
	ToRegion      string                     `json:"to_region"`
	MaxReplicaLag string                     `json:"max_replica_lag,omitempty"`
	Status        schema.SagaStatus          `json:"status"`
	NextAction    string                     `json:"next_action,omitempty"`
	CurrentSteps  []string                   `json:"current_steps,omitempty"`
	Execution     *sagastate.ExecutionResult `json:"execution,omitempty"`
	Inspect       *sagastate.InspectResult   `json:"inspect,omitempty"`
	Plan          *sagastate.CreateRequest   `json:"plan,omitempty"`
}

var (
	openFailoverObjectStore = openSagaObjectStore
	newFailoverProvider     = newSagaProvider
)

func runFailover(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" failover", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addDatabaseClientFlags(fs, root)
	stack := fs.String("stack", "", "multi-region stack name")
	service := fs.String("service", "", "service status target")
	database := fs.String("database", "", "managed database name")
	fromRegion := fs.String("from-region", "", "current primary region")
	toRegion := fs.String("to-region", "", "target primary region")
	trafficHost := fs.String("traffic-host", "", "global traffic host")
	maxReplicaLag := fs.String("max-replica-lag", templates.DefaultFailoverMaxReplicaLag, "maximum replica lag before failover")
	freezeWrites := fs.Bool("freeze-writes", false, "include a write freeze step before promotion")
	operationID := fs.String("operation-id", "", "operation ID to use")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	approvalID := fs.String("approval-id", "", "approval ID for policy-gated production operations")
	run := fs.Bool("run", true, "run the saga after creating it")
	dryRun := fs.Bool("dry-run", false, "render the failover saga plan without writing object state")

	flagArgs, positionals, err := splitDatabaseArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "failover", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "failover", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "failover", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *stack == "" {
		*stack = positionals[0]
	}
	if *stack == "" || *database == "" || *fromRegion == "" || *toRegion == "" {
		return writeClientCommandError(binary, "failover", *flags.format, *flags.traceID, errors.New("stack, --database, --from-region, and --to-region are required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect && !*dryRun {
		return writeClientCommandError(binary, "failover", *flags.format, *flags.traceID, errors.New("failover currently requires --direct mode"), stdout, stderr)
	}
	req := templates.RegionalFailoverRequest{
		SagaID:        *sagaID,
		OperationID:   *operationID,
		Stack:         *stack,
		Service:       *service,
		Database:      *database,
		Env:           loaded.Config.Env,
		FromRegion:    *fromRegion,
		ToRegion:      *toRegion,
		TrafficHost:   *trafficHost,
		MaxReplicaLag: *maxReplicaLag,
		FreezeWrites:  *freezeWrites,
		Actor:         schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:       *flags.traceID,
	}
	if _, err := authz.MustAuthorize(nilContext(), authz.DefaultPolicy{}, authz.Request{
		Actor:      req.Actor,
		Action:     authz.ActionFailover,
		Target:     schema.Target{Kind: "multi-region-stack", Name: req.Stack},
		Env:        req.Env,
		Service:    req.Service,
		Risk:       schema.RiskCritical,
		ApprovalID: *approvalID,
		DryRun:     *dryRun,
		TraceID:    req.TraceID,
	}); err != nil {
		return writeClientCommandError(binary, "failover", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := createAndMaybeRunFailover(nilContext(), binary, loaded.Config, req, *run, *dryRun)
	if err != nil {
		return writeClientError(binary, "failover", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeFailoverResult(binary, *flags.format, *flags.traceID, *result, stdout, stderr)
}

func createAndMaybeRunFailover(ctx context.Context, binary string, cfg config.Config, req templates.RegionalFailoverRequest, run, dryRun bool) (*failoverResult, error) {
	req = templates.NormalizeRegionalFailoverRequest(req)
	createReq, err := templates.RegionalFailover(req)
	if err != nil {
		return nil, err
	}
	result := failoverResultFromRequest(req, schema.SagaPending, nil, nil)
	if dryRun {
		result.NextAction = "create_saga"
		result.CurrentSteps = []string{"preflight"}
		result.Plan = &createReq
		return &result, nil
	}
	store, err := openFailoverObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		return nil, err
	}
	_ = appendDatabaseAudit(ctx, store, req.Actor, schema.Target{Kind: "multi-region-stack", Name: req.Stack}, "regional_failover", req.OperationID, req.SagaID, req.TraceID, schema.RiskCritical, "created regional failover saga")
	var execution *sagastate.ExecutionResult
	if run {
		cloud, err := newFailoverProvider(cfg, store)
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
	result = failoverResultFromInspect(*inspect, execution)
	return &result, nil
}

func failoverResultFromRequest(req templates.RegionalFailoverRequest, status schema.SagaStatus, execution *sagastate.ExecutionResult, inspect *sagastate.InspectResult) failoverResult {
	return failoverResult{
		SagaID:        req.SagaID,
		OperationID:   req.OperationID,
		Stack:         req.Stack,
		Service:       req.Service,
		Database:      req.Database,
		Env:           req.Env,
		FromRegion:    req.FromRegion,
		ToRegion:      req.ToRegion,
		MaxReplicaLag: req.MaxReplicaLag,
		Status:        status,
		Execution:     execution,
		Inspect:       inspect,
	}
}

func failoverResultFromInspect(inspect sagastate.InspectResult, execution *sagastate.ExecutionResult) failoverResult {
	var params templates.RegionalFailoverRequest
	_ = json.Unmarshal(inspect.Intent.Params, &params)
	result := failoverResultFromRequest(params, inspect.Status, execution, &inspect)
	result.SagaID = inspect.SagaID
	result.CurrentSteps = append([]string(nil), inspect.CurrentSteps...)
	switch inspect.Status {
	case schema.SagaSucceeded:
		result.NextAction = "complete"
	case schema.SagaFailed:
		result.NextAction = "inspect_failure"
	default:
		result.NextAction = "approve_or_resume"
	}
	return result
}

func writeFailoverResult(binary, format, traceID string, result failoverResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "failover saga %s status=%s\n", result.SagaID, result.Status)
		fmt.Fprintf(stdout, "stack: %s\n", result.Stack)
		fmt.Fprintf(stdout, "regions: %s -> %s\n", result.FromRegion, result.ToRegion)
		if result.NextAction != "" {
			fmt.Fprintf(stdout, "next: %s\n", result.NextAction)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(failoverOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s failover: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "failover", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}
