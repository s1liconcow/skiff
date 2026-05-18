package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/steps/builtin"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type databaseSagaOutput struct {
	OK      bool               `json:"ok"`
	TraceID string             `json:"trace_id,omitempty"`
	Result  databaseSagaResult `json:"result"`
}

type databaseSagaResult struct {
	SagaID           string                     `json:"saga_id"`
	OperationID      string                     `json:"operation_id,omitempty"`
	Command          string                     `json:"command"`
	Database         string                     `json:"database"`
	Env              string                     `json:"env,omitempty"`
	Service          string                     `json:"service,omitempty"`
	Mode             string                     `json:"mode,omitempty"`
	RestorePoint     string                     `json:"restore_point,omitempty"`
	RestoreTime      string                     `json:"restore_time,omitempty"`
	RestoredDatabase string                     `json:"restored_database,omitempty"`
	SecretRef        string                     `json:"secret_ref,omitempty"`
	Status           schema.SagaStatus          `json:"status"`
	NextAction       string                     `json:"next_action,omitempty"`
	CurrentSteps     []string                   `json:"current_steps,omitempty"`
	Execution        *sagastate.ExecutionResult `json:"execution,omitempty"`
	Inspect          *sagastate.InspectResult   `json:"inspect,omitempty"`
	Plan             *sagastate.CreateRequest   `json:"plan,omitempty"`
}

var (
	openDatabaseObjectStore = openSagaObjectStore
	newDatabaseProvider     = newSagaProvider
)

func runDatabase(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printDatabaseUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "backup":
		return runDatabaseBackup(binary, args[1:], root, stdout, stderr)
	case "restore":
		return runDatabaseRestore(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printDatabaseUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "database", root.Format, root.TraceID, fmt.Errorf("unknown database command %q", args[0]), stdout, stderr)
	}
}

func runDatabaseBackup(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" database backup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addDatabaseClientFlags(fs, root)
	database := fs.String("database", "", "database name")
	service := fs.String("service", "", "attached service name")
	operationID := fs.String("operation-id", "", "operation ID to use")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	run := fs.Bool("run", true, "run the saga after creating it")
	dryRun := fs.Bool("dry-run", false, "render the backup saga plan without writing object state")

	flagArgs, positionals, err := splitDatabaseArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "database backup", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "database backup", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "database backup", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *database == "" {
		*database = positionals[0]
	}
	if *database == "" {
		return writeClientCommandError(binary, "database backup", *flags.format, *flags.traceID, errors.New("database is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect && !*dryRun {
		return writeClientCommandError(binary, "database backup", *flags.format, *flags.traceID, errors.New("database backup currently requires --direct mode"), stdout, stderr)
	}
	req := templates.DatabaseBackupRequest{
		SagaID:      *sagaID,
		OperationID: *operationID,
		Database:    *database,
		Env:         loaded.Config.Env,
		Service:     *service,
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:     *flags.traceID,
	}
	if _, err := authz.MustAuthorize(nilContext(), authz.DefaultPolicy{}, authz.Request{
		Actor:   req.Actor,
		Action:  authz.ActionBackup,
		Target:  schema.Target{Kind: "database", Name: req.Database},
		Env:     req.Env,
		Service: req.Service,
		Risk:    schema.RiskMedium,
		DryRun:  *dryRun,
		TraceID: req.TraceID,
	}); err != nil {
		return writeClientCommandError(binary, "database backup", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := createAndMaybeRunDatabaseBackup(nilContext(), binary, loaded.Config, req, *run, *dryRun)
	if err != nil {
		return writeClientError(binary, "database backup", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeDatabaseSagaResult(binary, "database backup", *flags.format, *flags.traceID, *result, stdout, stderr)
}

func runDatabaseRestore(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" database restore", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addDatabaseClientFlags(fs, root)
	database := fs.String("database", "", "database name")
	to := fs.String("to", "", "RFC3339 restore time or provider restore point")
	service := fs.String("service", "", "attached service name")
	releaseID := fs.String("release-id", "", "release ID to restart, when the provider requires one")
	secretRef := fs.String("secret-ref", "", "secret pointer reference to update after approval")
	restoredDatabase := fs.String("restored-database", "", "name for the restored database")
	mode := fs.String("mode", templates.DatabaseRestoreModeNewDB, "restore mode")
	smokeQuery := fs.String("smoke-query", templates.DefaultDatabaseSmokeQuery, "non-destructive smoke query")
	operationID := fs.String("operation-id", "", "operation ID to use")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	approvalID := fs.String("approval-id", "", "approval ID for policy-gated production operations")
	run := fs.Bool("run", true, "run the saga after creating it")
	dryRun := fs.Bool("dry-run", false, "render the restore saga plan without writing object state")

	flagArgs, positionals, err := splitDatabaseArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "database restore", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "database restore", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "database restore", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *database == "" {
		*database = positionals[0]
	}
	if *database == "" || *to == "" || *secretRef == "" {
		return writeClientCommandError(binary, "database restore", *flags.format, *flags.traceID, errors.New("database, --to, and --secret-ref are required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect && !*dryRun {
		return writeClientCommandError(binary, "database restore", *flags.format, *flags.traceID, errors.New("database restore currently requires --direct mode"), stdout, stderr)
	}
	req := templates.DatabaseRestoreRequest{
		SagaID:           *sagaID,
		OperationID:      *operationID,
		Database:         *database,
		Env:              loaded.Config.Env,
		Service:          *service,
		ReleaseID:        *releaseID,
		RestorePoint:     *to,
		Mode:             *mode,
		RestoredDatabase: *restoredDatabase,
		SecretRef:        *secretRef,
		SmokeQuery:       *smokeQuery,
		Actor:            schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:          *flags.traceID,
	}
	if isRFC3339(*to) {
		req.RestorePoint = ""
		req.RestoreTime = *to
	}
	if _, err := authz.MustAuthorize(nilContext(), authz.DefaultPolicy{}, authz.Request{
		Actor:      req.Actor,
		Action:     authz.ActionRestore,
		Target:     schema.Target{Kind: "database", Name: req.Database},
		Env:        req.Env,
		Service:    req.Service,
		Risk:       schema.RiskHigh,
		ApprovalID: *approvalID,
		DryRun:     *dryRun,
		TraceID:    req.TraceID,
	}); err != nil {
		return writeClientCommandError(binary, "database restore", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := createAndMaybeRunDatabaseRestore(nilContext(), binary, loaded.Config, req, *run, *dryRun)
	if err != nil {
		return writeClientError(binary, "database restore", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return writeDatabaseSagaResult(binary, "database restore", *flags.format, *flags.traceID, *result, stdout, stderr)
}

func createAndMaybeRunDatabaseBackup(ctx context.Context, binary string, cfg config.Config, req templates.DatabaseBackupRequest, run, dryRun bool) (*databaseSagaResult, error) {
	req = templates.NormalizeDatabaseBackupRequest(req)
	createReq, err := templates.DatabaseBackup(req)
	if err != nil {
		return nil, err
	}
	result := backupResultFromRequest(req, schema.SagaPending, nil, nil)
	if dryRun {
		result.NextAction = "create_saga"
		result.CurrentSteps = []string{"preflight"}
		result.Plan = &createReq
		return &result, nil
	}
	store, err := openDatabaseObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		return nil, err
	}
	_ = appendDatabaseAudit(ctx, store, req.Actor, schema.Target{Kind: "database", Name: req.Database}, "database_backup", req.OperationID, req.SagaID, req.TraceID, schema.RiskMedium, "created database backup saga")
	var execution *sagastate.ExecutionResult
	if run {
		cloud, err := newDatabaseProvider(cfg, store)
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
	result = backupResultFromInspect(*inspect, execution)
	return &result, nil
}

func createAndMaybeRunDatabaseRestore(ctx context.Context, binary string, cfg config.Config, req templates.DatabaseRestoreRequest, run, dryRun bool) (*databaseSagaResult, error) {
	req = templates.NormalizeDatabaseRestoreRequest(req)
	createReq, err := templates.DatabaseRestore(req)
	if err != nil {
		return nil, err
	}
	result := restoreResultFromRequest(req, schema.SagaPending, nil, nil)
	if dryRun {
		result.NextAction = "create_saga"
		result.CurrentSteps = []string{"preflight"}
		result.Plan = &createReq
		return &result, nil
	}
	store, err := openDatabaseObjectStore(cfg)
	if err != nil {
		return nil, err
	}
	sagas := sagastate.NewStore(store)
	if _, err := sagas.Create(ctx, createReq); err != nil {
		return nil, err
	}
	_ = appendDatabaseAudit(ctx, store, req.Actor, schema.Target{Kind: "database", Name: req.Database}, "database_restore", req.OperationID, req.SagaID, req.TraceID, schema.RiskHigh, "created database restore saga")
	var execution *sagastate.ExecutionResult
	if run {
		cloud, err := newDatabaseProvider(cfg, store)
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
	result = restoreResultFromInspect(*inspect, execution)
	return &result, nil
}

func backupResultFromRequest(req templates.DatabaseBackupRequest, status schema.SagaStatus, execution *sagastate.ExecutionResult, inspect *sagastate.InspectResult) databaseSagaResult {
	return databaseSagaResult{
		SagaID:      req.SagaID,
		OperationID: req.OperationID,
		Command:     "backup",
		Database:    req.Database,
		Env:         req.Env,
		Service:     req.Service,
		Status:      status,
		NextAction:  "resume",
		Execution:   execution,
		Inspect:     inspect,
	}
}

func restoreResultFromRequest(req templates.DatabaseRestoreRequest, status schema.SagaStatus, execution *sagastate.ExecutionResult, inspect *sagastate.InspectResult) databaseSagaResult {
	return databaseSagaResult{
		SagaID:           req.SagaID,
		OperationID:      req.OperationID,
		Command:          "restore",
		Database:         req.Database,
		Env:              req.Env,
		Service:          req.Service,
		Mode:             req.Mode,
		RestorePoint:     req.RestorePoint,
		RestoreTime:      req.RestoreTime,
		RestoredDatabase: req.RestoredDatabase,
		SecretRef:        req.SecretRef,
		Status:           status,
		NextAction:       "resume",
		Execution:        execution,
		Inspect:          inspect,
	}
}

func backupResultFromInspect(inspect sagastate.InspectResult, execution *sagastate.ExecutionResult) databaseSagaResult {
	var params templates.DatabaseBackupRequest
	_ = json.Unmarshal(inspect.Intent.Params, &params)
	result := backupResultFromRequest(params, inspect.Status, execution, &inspect)
	result.SagaID = inspect.SagaID
	result.CurrentSteps = append([]string(nil), inspect.CurrentSteps...)
	result.NextAction = nextActionForSaga(inspect)
	return result
}

func restoreResultFromInspect(inspect sagastate.InspectResult, execution *sagastate.ExecutionResult) databaseSagaResult {
	var params templates.DatabaseRestoreRequest
	_ = json.Unmarshal(inspect.Intent.Params, &params)
	result := restoreResultFromRequest(params, inspect.Status, execution, &inspect)
	result.SagaID = inspect.SagaID
	result.CurrentSteps = append([]string(nil), inspect.CurrentSteps...)
	result.NextAction = nextActionForSaga(inspect)
	return result
}

func nextActionForSaga(inspect sagastate.InspectResult) string {
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

func writeDatabaseSagaResult(binary, command, format, traceID string, result databaseSagaResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "database %s saga %s status=%s\n", result.Command, result.SagaID, result.Status)
		fmt.Fprintf(stdout, "database: %s\n", result.Database)
		if result.RestoredDatabase != "" {
			fmt.Fprintf(stdout, "restored_database: %s\n", result.RestoredDatabase)
		}
		if result.NextAction != "" {
			fmt.Fprintf(stdout, "next: %s\n", result.NextAction)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(databaseSagaOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "database", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func appendDatabaseAudit(ctx context.Context, store objstore.ObjectStore, actor schema.Actor, target schema.Target, action, operationID, sagaID, traceID string, risk schema.Risk, summary string) error {
	log, err := events.NewLog(events.Options{Store: store})
	if err != nil {
		return err
	}
	record := events.NewAuditRecord(actor, target, action, summary, traceID, eventsNow(), sagaID+action+operationID)
	record.Risk = risk
	record.Data = rawJSON(map[string]any{"operation_id": operationID, "saga_id": sagaID})
	_, err = log.AppendAudit(ctx, record)
	return err
}

func eventsNow() time.Time {
	return time.Now().UTC()
}

func isRFC3339(value string) bool {
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}

func rawJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}

func splitDatabaseArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":           true,
		"approval-id":       true,
		"config":            true,
		"database":          true,
		"env":               true,
		"format":            true,
		"from-region":       true,
		"max-replica-lag":   true,
		"mode":              true,
		"operation-id":      true,
		"provider":          true,
		"region":            true,
		"release-id":        true,
		"restored-database": true,
		"saga-id":           true,
		"secret-ref":        true,
		"service":           true,
		"smoke-query":       true,
		"stack":             true,
		"state":             true,
		"state-bucket":      true,
		"to-region":         true,
		"traffic-host":      true,
		"to":                true,
		"trace-id":          true,
	}
	return splitArgs(args, valueFlags)
}

func addDatabaseClientFlags(fs *flag.FlagSet, root rootOptions) clientFlagSet {
	mode := string(root.Mode)
	return clientFlagSet{
		format:      fs.String("format", root.Format, "output format: human, json, or json-pretty"),
		noColor:     fs.Bool("no-color", root.NoColor, "disable ANSI color output"),
		traceID:     fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output"),
		yes:         fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation"),
		configPath:  fs.String("config", root.ConfigPath, "path to Skiff config file"),
		context:     fs.String("context", root.Context, "Skiff config context name"),
		env:         fs.String("env", root.Env, "Skiff environment name"),
		provider:    fs.String("provider", root.Provider, "cloud provider name"),
		region:      fs.String("region", root.Region, "cloud provider region"),
		state:       fs.String("state", root.State, "object-state bucket URI"),
		stateBucket: fs.String("state-bucket", root.State, "object-state bucket URI"),
		apiURL:      fs.String("api-url", root.APIURL, "skiffd API URL"),
		mode:        &mode,
		direct:      fs.Bool("direct", root.directSet, "use direct object-state mode"),
		api:         fs.Bool("api", root.apiSet, "use skiffd API mode"),
	}
}

func printDatabaseUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s database backup <database> [flags]\n", binary)
	fmt.Fprintf(w, "       %s database restore <database> --to <time-or-restore-point> --secret-ref <secret> [flags]\n", binary)
}
