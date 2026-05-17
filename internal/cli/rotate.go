package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/config"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps/builtin"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type rotateOutput struct {
	OK      bool         `json:"ok"`
	TraceID string       `json:"trace_id,omitempty"`
	Result  rotateResult `json:"result"`
}

type rotateResult struct {
	SagaID         string                     `json:"saga_id"`
	OperationID    string                     `json:"operation_id,omitempty"`
	Command        string                     `json:"command"`
	SecretRef      string                     `json:"secret_ref"`
	Env            string                     `json:"env,omitempty"`
	Consumers      []string                   `json:"consumers"`
	CanaryConsumer string                     `json:"canary_consumer,omitempty"`
	Database       string                     `json:"database,omitempty"`
	DisableAfter   string                     `json:"disable_after,omitempty"`
	Status         schema.SagaStatus          `json:"status"`
	NextAction     string                     `json:"next_action,omitempty"`
	CurrentSteps   []string                   `json:"current_steps,omitempty"`
	Execution      *sagastate.ExecutionResult `json:"execution,omitempty"`
	Inspect        *sagastate.InspectResult   `json:"inspect,omitempty"`
	Plan           *sagastate.CreateRequest   `json:"plan,omitempty"`
}

var (
	openRotateObjectStore = openSagaObjectStore
	newRotateProvider     = newSagaProvider
)

func runRotate(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printRotateUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "secret":
		return runRotateSecret(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printRotateUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "rotate", root.Format, root.TraceID, fmt.Errorf("unknown rotate command %q", args[0]), stdout, stderr)
	}
}

func runRotateSecret(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" rotate secret", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addDatabaseClientFlags(fs, root)
	consumers := fs.String("consumers", "", "comma-separated service consumers to roll after promotion")
	canaryConsumer := fs.String("canary-consumer", "", "consumer to canary before promotion")
	database := fs.String("database", "", "managed database name when rotating database credentials")
	disableAfter := fs.String("disable-after", templates.DefaultSecretDisableAfter, "delay before disabling the old credential version")
	operationID := fs.String("operation-id", "", "operation ID to use")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	approvalID := fs.String("approval-id", "", "approval ID for policy-gated production operations")
	run := fs.Bool("run", true, "run the saga after creating it")
	dryRun := fs.Bool("dry-run", false, "render the rotation saga plan without writing object state")

	flagArgs, positionals, err := splitRotateArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "rotate secret", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "rotate secret", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "rotate secret", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	secretRef := ""
	if len(positionals) == 1 {
		secretRef = positionals[0]
	}
	parsedConsumers := parseCommaList(*consumers)
	if secretRef == "" || len(parsedConsumers) == 0 {
		return writeClientCommandError(binary, "rotate secret", *flags.format, *flags.traceID, errors.New("secret ref and --consumers are required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect && !*dryRun {
		return writeClientCommandError(binary, "rotate secret", *flags.format, *flags.traceID, errors.New("secret rotation currently requires --direct mode"), stdout, stderr)
	}
	req := templates.SecretRotationRequest{
		SagaID:         *sagaID,
		OperationID:    *operationID,
		SecretRef:      secretRef,
		Env:            loaded.Config.Env,
		Consumers:      parsedConsumers,
		CanaryConsumer: *canaryConsumer,
		Database:       *database,
		DisableAfter:   *disableAfter,
		Actor:          schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:        *flags.traceID,
	}
	if _, err := authz.MustAuthorize(nilContext(), authz.DefaultPolicy{}, authz.Request{
		Actor:      req.Actor,
		Action:     authz.ActionRotate,
		Target:     schema.Target{Kind: "secret", Name: req.SecretRef},
		Env:        req.Env,
		Service:    firstListItem(req.Consumers),
		Risk:       schema.RiskHigh,
		ApprovalID: *approvalID,
		DryRun:     *dryRun,
		TraceID:    req.TraceID,
	}); err != nil {
		return writeClientCommandError(binary, "rotate secret", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := createAndMaybeRunSecretRotation(nilContext(), binary, loaded.Config, req, *run, *dryRun)
	if err != nil {
		return writeClientError(binary, "rotate secret", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeRotateResult(binary, "rotate secret", *flags.format, *flags.traceID, *result, stdout, stderr)
}

func createAndMaybeRunSecretRotation(ctx context.Context, binary string, cfg config.Config, req templates.SecretRotationRequest, run, dryRun bool) (*rotateResult, error) {
	req = templates.NormalizeSecretRotationRequest(req)
	createReq, err := templates.SecretRotation(req)
	if err != nil {
		return nil, err
	}
	result := rotateResultFromRequest(req, schema.SagaPending, nil, nil)
	if dryRun {
		result.NextAction = "create_saga"
		result.CurrentSteps = []string{"preflight"}
		result.Plan = &createReq
		return &result, nil
	}
	store, err := openRotateObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		return nil, err
	}
	_ = appendDatabaseAudit(ctx, store, req.Actor, schema.Target{Kind: "secret", Name: req.SecretRef}, "secret_rotation", req.OperationID, req.SagaID, req.TraceID, schema.RiskHigh, "created secret rotation saga")
	var execution *sagastate.ExecutionResult
	if run {
		cloud, err := newRotateProvider(cfg, store)
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
	result = rotateResultFromInspect(*inspect, execution)
	return &result, nil
}

func rotateResultFromRequest(req templates.SecretRotationRequest, status schema.SagaStatus, execution *sagastate.ExecutionResult, inspect *sagastate.InspectResult) rotateResult {
	return rotateResult{
		SagaID:         req.SagaID,
		OperationID:    req.OperationID,
		Command:        "secret",
		SecretRef:      req.SecretRef,
		Env:            req.Env,
		Consumers:      append([]string(nil), req.Consumers...),
		CanaryConsumer: req.CanaryConsumer,
		Database:       req.Database,
		DisableAfter:   req.DisableAfter,
		Status:         status,
		NextAction:     "resume",
		Execution:      execution,
		Inspect:        inspect,
	}
}

func rotateResultFromInspect(inspect sagastate.InspectResult, execution *sagastate.ExecutionResult) rotateResult {
	var params templates.SecretRotationRequest
	_ = json.Unmarshal(inspect.Intent.Params, &params)
	result := rotateResultFromRequest(params, inspect.Status, execution, &inspect)
	result.SagaID = inspect.SagaID
	result.CurrentSteps = append([]string(nil), inspect.CurrentSteps...)
	result.NextAction = nextActionForSaga(inspect)
	return result
}

func writeRotateResult(binary, command, format, traceID string, result rotateResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "rotate %s saga %s status=%s\n", result.Command, result.SagaID, result.Status)
		fmt.Fprintf(stdout, "secret: %s\n", result.SecretRef)
		if result.CanaryConsumer != "" {
			fmt.Fprintf(stdout, "canary_consumer: %s\n", result.CanaryConsumer)
		}
		if result.NextAction != "" {
			fmt.Fprintf(stdout, "next: %s\n", result.NextAction)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(rotateOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "rotate", format, traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func splitRotateArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":         true,
		"approval-id":     true,
		"canary-consumer": true,
		"config":          true,
		"consumers":       true,
		"database":        true,
		"disable-after":   true,
		"env":             true,
		"format":          true,
		"mode":            true,
		"operation-id":    true,
		"provider":        true,
		"region":          true,
		"saga-id":         true,
		"state":           true,
		"state-bucket":    true,
		"trace-id":        true,
	}
	return splitArgs(args, valueFlags)
}

func parseCommaList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, part)
	}
	return out
}

func firstListItem(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func printRotateUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s rotate secret <secret-ref> --consumers <services> [flags]\n", binary)
}
