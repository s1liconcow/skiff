package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/deploy"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type rollbackOutput struct {
	OK      bool                  `json:"ok"`
	TraceID string                `json:"trace_id,omitempty"`
	Result  deploy.RollbackResult `json:"result"`
}

type rollbackErrorOutput struct {
	OK                 bool                   `json:"ok"`
	Code               string                 `json:"code"`
	Summary            string                 `json:"summary"`
	TraceID            string                 `json:"trace_id,omitempty"`
	Result             *deploy.RollbackResult `json:"result,omitempty"`
	RecommendedActions []recommendedAction    `json:"recommended_actions,omitempty"`
}

var (
	openRollbackObjectStore = client.OpenObjectStore
	newRollbackProvider     = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		opts := []aws.Option{}
		if store != nil {
			opts = append(opts, aws.WithStateStore(store))
		}
		return aws.NewFromConfig(cfg, opts...)
	}
)

func runRollback(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" rollback", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	target := fs.String("to", deploy.RollbackPreviousStable, "release ID to roll back to, or previous-stable")
	operationID := fs.String("operation-id", "", "operation ID to use")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	approvalID := fs.String("approval-id", "", "approval ID for policy-gated production operations")
	watch := fs.Bool("watch", true, "watch rollout once after starting rollback")
	minHealthy := fs.Int("min-healthy-percentage", 0, "minimum healthy percentage for ASG instance refresh")
	instanceWarmup := fs.Int("instance-warmup", 0, "instance warmup seconds for ASG instance refresh")

	flagArgs, positionals, err := splitRollbackArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "rollback", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "rollback", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "rollback", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	if *service == "" {
		return writeClientCommandError(binary, "rollback", *flags.format, *flags.traceID, errors.New("service is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return writeClientCommandError(binary, "rollback", *flags.format, *flags.traceID, errors.New("rollback currently requires --direct mode"), stdout, stderr)
	}
	store, err := openRollbackObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "rollback", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	cloud, err := newRollbackProvider(loaded.Config, store)
	if err != nil {
		return writeClientCommandError(binary, "rollback", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := deploy.Deployer{Store: store, Provider: cloud}.Rollback(nilContext(), deploy.RollbackRequest{
		Actor:                schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:              *flags.traceID,
		Service:              *service,
		Env:                  loaded.Config.Env,
		TargetRelease:        *target,
		OperationID:          *operationID,
		SagaID:               *sagaID,
		ApprovalID:           *approvalID,
		NoWatch:              !*watch,
		MinHealthyPercentage: *minHealthy,
		InstanceWarmup:       *instanceWarmup,
	})
	if err != nil {
		return writeRollbackError(binary, *flags.format, *flags.traceID, err, result, stdout, stderr)
	}

	switch *flags.format {
	case "human", "text":
		if result.RolloutStatus != nil && result.RolloutStatus.Status == "succeeded" {
			fmt.Fprintf(stdout, "rollback %s succeeded\n", result.OperationID)
		} else {
			fmt.Fprintf(stdout, "rollback %s started\n", result.OperationID)
		}
		fmt.Fprintf(stdout, "release: %s\n", result.ToRelease)
		if result.SagaID != "" {
			fmt.Fprintf(stdout, "saga: %s\n", result.SagaID)
		}
		if result.RolloutStatus != nil {
			fmt.Fprintf(stdout, "rollout: %s\n", result.RolloutStatus.Status)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(rollbackOutput{OK: result.OK, TraceID: *flags.traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s rollback: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "rollback", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func splitRollbackArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":                true,
		"approval-id":            true,
		"config":                 true,
		"env":                    true,
		"format":                 true,
		"instance-warmup":        true,
		"min-healthy-percentage": true,
		"mode":                   true,
		"operation-id":           true,
		"provider":               true,
		"region":                 true,
		"saga-id":                true,
		"service":                true,
		"state":                  true,
		"state-bucket":           true,
		"to":                     true,
		"trace-id":               true,
	}
	return splitArgs(args, valueFlags)
}

func writeRollbackError(binary, format, traceID string, err error, result *deploy.RollbackResult, stdout, stderr io.Writer) int {
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(rollbackErrorOutput{
			OK:                 false,
			Code:               "ROLLBACK_FAILED",
			Summary:            err.Error(),
			TraceID:            traceID,
			Result:             result,
			RecommendedActions: rollbackRecommendedActions(binary, result),
		})
		return ExitRolloutFailed
	}
	fmt.Fprintf(stderr, "%s rollback: %v\n", binary, err)
	return ExitRolloutFailed
}

func rollbackRecommendedActions(binary string, result *deploy.RollbackResult) []recommendedAction {
	if result == nil || len(result.NextCommands) == 0 {
		return []recommendedAction{{ID: "inspect_status", Command: binary + " status <service> --format json", Mutating: false}}
	}
	actions := make([]recommendedAction, 0, len(result.NextCommands))
	for i, command := range result.NextCommands {
		actions = append(actions, recommendedAction{
			ID:       fmt.Sprintf("inspect_%d", i+1),
			Command:  command,
			Mutating: false,
		})
	}
	return actions
}
