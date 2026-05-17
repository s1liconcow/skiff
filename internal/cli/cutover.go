package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type cutoverOutput struct {
	OK      bool          `json:"ok"`
	TraceID string        `json:"trace_id,omitempty"`
	Result  cutoverResult `json:"result"`
}

type cutoverResult struct {
	SagaID        string               `json:"saga_id"`
	OperationID   string               `json:"operation_id"`
	Service       string               `json:"service"`
	Env           string               `json:"env"`
	From          string               `json:"from"`
	To            string               `json:"to"`
	Percent       int                  `json:"percent"`
	Status        schema.SagaStatus    `json:"status"`
	Risk          schema.Risk          `json:"risk"`
	Reversibility schema.Reversibility `json:"reversibility"`
	Paths         map[string]string    `json:"paths,omitempty"`
	NextCommands  []string             `json:"next_commands,omitempty"`
}

func runCutover(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" cutover", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service to cut over")
	from := fs.String("from", "kube", "source traffic target")
	to := fs.String("to", "skiff", "destination traffic target")
	percent := fs.Int("percent", 10, "traffic percentage to send to destination")
	dryRun := fs.Bool("dry-run", false, "render the cutover saga without writing object state")
	sagaID := fs.String("saga-id", "", "saga ID to use")
	operationID := fs.String("operation-id", "", "operation ID to use")

	flagArgs, positionals, err := splitCutoverArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "cutover", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "cutover", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "cutover", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	if *service == "" {
		return writeClientCommandError(binary, "cutover", *flags.format, *flags.traceID, errors.New("service is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	env := *flags.env
	var storeConfigLoaded bool
	var loadedConfigEnv string
	if !*dryRun {
		loaded, err := flags.load(binary, root, fs)
		if err != nil {
			return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
		}
		if loaded.Config.Mode != "direct" {
			return writeClientCommandError(binary, "cutover", *flags.format, *flags.traceID, errors.New("cutover currently requires --direct mode"), stdout, stderr)
		}
		storeConfigLoaded = true
		loadedConfigEnv = loaded.Config.Env
		if env == "" {
			env = loaded.Config.Env
		}
	}
	if env == "" {
		env = root.Env
	}
	if env == "" {
		return writeClientCommandError(binary, "cutover", *flags.format, *flags.traceID, errors.New("env is required"), stdout, stderr)
	}
	req := templates.NormalizeTrafficCutoverRequest(templates.TrafficCutoverRequest{
		SagaID:      *sagaID,
		OperationID: *operationID,
		Service:     *service,
		Env:         env,
		From:        *from,
		To:          *to,
		Percent:     *percent,
		TraceID:     *flags.traceID,
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
	})
	createReq, err := templates.TrafficCutover(req)
	if err != nil {
		return writeClientCommandError(binary, "cutover", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result := cutoverResultFromRequest(req, createReq.Control.Status)
	if *dryRun {
		result.Paths = nil
		return writeCutoverResult(binary, *flags.format, *flags.traceID, result, stdout, stderr)
	}
	if !storeConfigLoaded {
		return writeClientCommandError(binary, "cutover", *flags.format, *flags.traceID, errors.New("internal cutover config was not loaded"), stdout, stderr)
	}
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, nil, stdout, stderr)
	}
	if loadedConfigEnv != "" && req.Env != loadedConfigEnv {
		return writeClientCommandError(binary, "cutover", *flags.format, *flags.traceID, fmt.Errorf("cutover env %s does not match loaded config env %s", req.Env, loadedConfigEnv), stdout, stderr)
	}
	store, err := openSagaObjectStore(loaded.Config)
	if err != nil {
		return writeClientError(binary, "cutover", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	docs, err := sagastate.NewStore(store).Create(nilContext(), createReq)
	if err != nil {
		return writeClientError(binary, "cutover", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result.Paths = map[string]string{
		"intent":  docs.Intent.Key,
		"graph":   docs.Graph.Key,
		"control": docs.Control.Key,
	}
	return writeCutoverResult(binary, *flags.format, *flags.traceID, result, stdout, stderr)
}

func cutoverResultFromRequest(req templates.TrafficCutoverRequest, status schema.SagaStatus) cutoverResult {
	return cutoverResult{
		SagaID:        req.SagaID,
		OperationID:   req.OperationID,
		Service:       req.Service,
		Env:           req.Env,
		From:          req.From,
		To:            req.To,
		Percent:       req.Percent,
		Status:        status,
		Risk:          riskForCutover(req.Percent),
		Reversibility: schema.Compensatable,
		NextCommands: []string{
			fmt.Sprintf("skiff saga inspect %s --direct --format json --trace-id %s", req.SagaID, req.TraceID),
			fmt.Sprintf("skiff saga approve %s --step approve-cutover --direct --format json --trace-id %s", req.SagaID, req.TraceID),
		},
	}
}

func writeCutoverResult(binary, format, traceID string, result cutoverResult, stdout, stderr io.Writer) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "cutover saga %s status=%s percent=%d\n", result.SagaID, result.Status, result.Percent)
		for _, command := range result.NextCommands {
			fmt.Fprintf(stdout, "next: %s\n", command)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(cutoverOutput{OK: true, TraceID: traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s cutover: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "cutover", format, traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func splitCutoverArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"env":          true,
		"format":       true,
		"from":         true,
		"mode":         true,
		"operation-id": true,
		"percent":      true,
		"provider":     true,
		"region":       true,
		"saga-id":      true,
		"service":      true,
		"state":        true,
		"state-bucket": true,
		"to":           true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func riskForCutover(percent int) schema.Risk {
	if percent == 100 {
		return schema.RiskHigh
	}
	return schema.RiskMedium
}
