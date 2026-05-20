package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	opsstate "github.com/s1liconcow/skiff/internal/ops"
	"github.com/s1liconcow/skiff/internal/packages"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type opsListOutput struct {
	OK         bool               `json:"ok"`
	TraceID    string             `json:"trace_id,omitempty"`
	Operations []opsstate.Summary `json:"operations"`
}

type opsCatalogOutput struct {
	OK                 bool                  `json:"ok"`
	TraceID            string                `json:"trace_id,omitempty"`
	Target             string                `json:"target"`
	TargetKind         string                `json:"target_kind"`
	Operations         []opsCatalogOperation `json:"operations"`
	RecommendedActions []recommendedAction   `json:"recommended_actions,omitempty"`
	Facts              []schema.Fact         `json:"facts,omitempty"`
}

type opsCatalogOperation struct {
	Name          string                      `json:"name"`
	Kind          string                      `json:"kind"`
	Summary       string                      `json:"summary"`
	Risk          string                      `json:"risk"`
	Reversibility schema.Reversibility        `json:"reversibility"`
	Package       *schema.PackageProvenance   `json:"package,omitempty"`
	Params        []opsstate.ParamExplanation `json:"params,omitempty"`
	Command       string                      `json:"command"`
	Mutating      bool                        `json:"mutating"`
}

type opsPlanOutput struct {
	OK                 bool                        `json:"ok"`
	TraceID            string                      `json:"trace_id,omitempty"`
	Target             string                      `json:"target"`
	TargetKind         string                      `json:"target_kind"`
	Operation          string                      `json:"operation"`
	OperationID        string                      `json:"operation_id"`
	SagaID             string                      `json:"saga_id"`
	PlanOnly           bool                        `json:"plan_only,omitempty"`
	DryRun             bool                        `json:"dry_run,omitempty"`
	WouldWrite         bool                        `json:"would_write"`
	Profile            opsstate.ProfileExplanation `json:"profile"`
	Package            *schema.PackageProvenance   `json:"package,omitempty"`
	Params             json.RawMessage             `json:"params,omitempty"`
	Paths              map[string]string           `json:"paths,omitempty"`
	Facts              []schema.Fact               `json:"facts,omitempty"`
	RecommendedActions []recommendedAction         `json:"recommended_actions,omitempty"`
}

type paramFlag []string

func (f *paramFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *paramFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type opsInspectOutput struct {
	OK      bool                   `json:"ok"`
	TraceID string                 `json:"trace_id,omitempty"`
	Result  opsstate.InspectResult `json:"result"`
}

type opsResumeOutput struct {
	OK      bool                  `json:"ok"`
	TraceID string                `json:"trace_id,omitempty"`
	Result  opsstate.ResumeResult `json:"result"`
}

type opsRuntimeErrorOutput struct {
	OK                 bool                   `json:"ok"`
	Code               string                 `json:"code"`
	Summary            string                 `json:"summary"`
	TraceID            string                 `json:"trace_id,omitempty"`
	Result             *opsstate.ResumeResult `json:"result,omitempty"`
	RecommendedActions []recommendedAction    `json:"recommended_actions,omitempty"`
}

var (
	openOpsObjectStore = client.OpenObjectStore
	newOpsProvider     = func(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
		opts := []aws.Option{}
		if store != nil {
			opts = append(opts, aws.WithStateStore(store))
		}
		return aws.NewFromConfig(cfg, opts...)
	}
)

func runOps(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return writeClientCommandError(binary, "ops", root.Format, root.TraceID, errors.New("expected ops command list, plan, run, inspect, events, watch, approve, reject, or resume"), stdout, stderr)
	}
	switch args[0] {
	case "list":
		return runOpsList(binary, args[1:], root, stdout, stderr)
	case "plan":
		return runOpsPlan(binary, args[1:], root, stdout, stderr)
	case "run":
		return runOpsRun(binary, args[1:], root, stdout, stderr)
	case "inspect":
		return runOpsInspect(binary, args[1:], root, stdout, stderr)
	case "events":
		return runOpsEvents(binary, args[1:], root, stdout, stderr)
	case "resume":
		return runOpsResume(binary, args[1:], root, stdout, stderr)
	case "watch":
		return runOpsWatch(binary, args[1:], root, stdout, stderr)
	case "approve":
		return runSagaApproval(binary, "approve", args[1:], root, stdout, stderr)
	case "reject":
		return runSagaApproval(binary, "reject", args[1:], root, stdout, stderr)
	case "cancel", "compensate":
		return runSagaSkeleton(binary, args[0], args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printOpsUsage(stdout, binary)
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "ops", root.Format, root.TraceID, fmt.Errorf("unknown ops command %q", args[0]), stdout, stderr)
	}
}

func runOpsList(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" ops list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	all := fs.Bool("all", true, "include terminal operations")
	active := fs.Bool("active", false, "only include non-terminal operations")
	limit := fs.Int("limit", 0, "maximum operations to return")
	targetKind := fs.String("target-kind", "StatefulGroup", "operation target kind for catalog mode")
	lockfile := fs.String("lockfile", "skiff.lock.json", "package lockfile for catalog mode")
	cacheRoot := fs.String("cache", packages.DefaultCacheRoot(), "content-addressed package cache for catalog mode")

	flagArgs, positionals, err := splitOpsArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "ops", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 {
		return runOpsCatalogList(binary, positionals[0], *targetKind, *lockfile, *cacheRoot, *flags.format, *flags.traceID, stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	store, loaded, exit := loadOpsStore(binary, root, fs, flags, stdout, stderr)
	if exit != ExitSuccess {
		return exit
	}
	includeTerminal := *all
	if *active {
		includeTerminal = false
	}
	items, err := opsstate.NewStore(store).List(nilContext(), opsstate.ListOptions{Service: *service, IncludeTerminal: includeTerminal, Limit: *limit})
	if err != nil {
		return writeClientError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	_ = loaded
	switch *flags.format {
	case "human", "text":
		for _, item := range items {
			fmt.Fprintf(stdout, "%s %s status=%s", item.Service, item.OperationID, item.Status)
			if item.Kind != "" {
				fmt.Fprintf(stdout, " kind=%s", item.Kind)
			}
			if item.UpdatedAt != "" {
				fmt.Fprintf(stdout, " updated=%s", item.UpdatedAt)
			}
			if item.Lease != nil {
				fmt.Fprintf(stdout, " lease=%s until %s", item.Lease.Owner, item.Lease.ExpiresAt)
			}
			fmt.Fprintln(stdout)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(opsListOutput{OK: true, TraceID: *flags.traceID, Operations: items}); err != nil {
			fmt.Fprintf(stderr, "%s ops list: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runOpsCatalogList(binary, target, targetKind, lockfile, cacheRoot, format, traceID string, stdout, stderr io.Writer) int {
	catalog, err := opsstate.LoadCatalog(nilContext(), opsstate.CatalogOptions{
		Lockfile: lockfile,
		Cache:    packages.Cache{Root: cacheRoot},
	})
	if err != nil {
		return writeClientCommandError(binary, "ops list", format, traceID, err, stdout, stderr)
	}
	items := catalog.List(targetKind)
	operations := make([]opsCatalogOperation, 0, len(items))
	for _, item := range items {
		operation, err := opsCatalogOperationFor(binary, target, item)
		if err != nil {
			return writeClientCommandError(binary, "ops list", format, traceID, err, stdout, stderr)
		}
		operations = append(operations, operation)
	}
	facts := []schema.Fact{}
	if catalog.LockfileDigest != "" {
		facts = append(facts, schema.Fact{Type: "package_lock", Message: catalog.Lockfile + " " + catalog.LockfileDigest})
	}
	out := opsCatalogOutput{
		OK:         true,
		TraceID:    traceID,
		Target:     target,
		TargetKind: targetKind,
		Operations: operations,
		Facts:      facts,
	}
	if len(operations) > 0 {
		out.RecommendedActions = []recommendedAction{{ID: "plan_operation", Command: operations[0].Command, Mutating: false}}
	}
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "Operations for %s\n\n", target)
		if len(operations) == 0 {
			fmt.Fprintln(stdout, "none")
			return ExitSuccess
		}
		for _, operation := range operations {
			fmt.Fprintf(stdout, "%s\n", operation.Name)
			if operation.Summary != "" {
				fmt.Fprintf(stdout, "  summary: %s\n", operation.Summary)
			}
			fmt.Fprintf(stdout, "  risk: %s\n", operation.Risk)
			fmt.Fprintf(stdout, "  reversibility: %s\n", operation.Reversibility)
			if operation.Package != nil {
				fmt.Fprintf(stdout, "  package: %s@%s\n", operation.Package.Ref, operation.Package.Version)
			}
			if len(operation.Params) > 0 {
				names := make([]string, 0, len(operation.Params))
				for _, param := range operation.Params {
					if param.Required {
						names = append(names, param.Name+"*")
					} else {
						names = append(names, param.Name)
					}
				}
				fmt.Fprintf(stdout, "  params: %s\n", strings.Join(names, ", "))
			}
			fmt.Fprintf(stdout, "  command: %s\n\n", operation.Command)
		}
		return ExitSuccess
	case "json", "json-pretty":
		return encodeOpsJSON(stdout, stderr, binary, "ops list", format, out)
	default:
		return writeClientCommandError(binary, "ops list", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func opsCatalogOperationFor(binary, target string, item opsstate.CatalogProfile) (opsCatalogOperation, error) {
	explanation, err := opsstate.ExplainProfile(item.Profile)
	if err != nil {
		return opsCatalogOperation{}, err
	}
	return opsCatalogOperation{
		Name:          explanation.Name,
		Kind:          string(explanation.Kind),
		Summary:       explanation.Summary,
		Risk:          string(explanation.Risk),
		Reversibility: schema.Reversibility(explanation.Reversibility),
		Package:       cloneSchemaPackage(item.Package),
		Params:        append([]opsstate.ParamExplanation(nil), explanation.Params...),
		Command:       fmt.Sprintf("%s ops plan %s %s --format json", binary, target, explanation.Name),
		Mutating:      false,
	}, nil
}

func runOpsPlan(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	return runOpsProfileCommand(binary, "ops plan", args, root, stdout, stderr, false)
}

func runOpsRun(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	return runOpsProfileCommand(binary, "ops run", args, root, stdout, stderr, true)
}

func runOpsProfileCommand(binary, command string, args []string, root rootOptions, stdout, stderr io.Writer, run bool) int {
	fs := flag.NewFlagSet(binary+" "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	targetKind := fs.String("target-kind", "StatefulGroup", "operation target kind")
	lockfile := fs.String("lockfile", "skiff.lock.json", "package lockfile")
	cacheRoot := fs.String("cache", packages.DefaultCacheRoot(), "content-addressed package cache")
	operationID := fs.String("operation-id", "", "operation ID to create")
	sagaID := fs.String("saga-id", "", "saga ID to create")
	planOnly := fs.Bool("plan-only", false, "render the operation plan without writing object state")
	dryRun := fs.Bool("dry-run", false, "render the operation plan without writing object state")
	var params paramFlag
	fs.Var(&params, "param", "operation parameter as name=value; value may be JSON or a string")

	flagArgs, positionals, err := splitOpsArgs(args)
	if err != nil {
		return writeClientCommandError(binary, command, defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) != 2 {
		return writeClientCommandError(binary, command, *flags.format, *flags.traceID, errors.New("target and operation are required"), stdout, stderr)
	}
	target := positionals[0]
	operationName := positionals[1]
	parsedParams, err := parseOpsParams(params)
	if err != nil {
		return writeClientCommandError(binary, command, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	now := time.Now().UTC()
	traceID := defaultString(*flags.traceID, "tr_"+events.NewID(now, target+operationName+"ops"))
	opID := defaultString(*operationID, "op_"+events.NewID(now, traceID+target+operationName))
	saga := defaultString(*sagaID, "saga_"+events.NewID(now, opID+operationName))
	env := defaultString(*flags.env, "prod")

	catalog, err := opsstate.LoadCatalog(nilContext(), opsstate.CatalogOptions{
		Lockfile: *lockfile,
		Cache:    packages.Cache{Root: *cacheRoot},
	})
	if err != nil {
		return writeClientCommandError(binary, command, *flags.format, traceID, err, stdout, stderr)
	}
	catalogProfile, err := catalog.Resolve(operationName)
	if err != nil {
		return writeClientCommandError(binary, command, *flags.format, traceID, err, stdout, stderr)
	}
	if catalogProfile.Package == nil {
		return writeClientCommandError(binary, command, *flags.format, traceID, fmt.Errorf("operation %q is not exported by a locked package; run skiff pkg add or pkg update first", operationName), stdout, stderr)
	}
	renderReq := opsstate.ProfileRenderRequest{
		Profile:   catalogProfile.Profile,
		SagaID:    saga,
		Target:    schema.Target{Kind: *targetKind, Name: target},
		Actor:     schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:   traceID,
		Params:    parsedParams,
		Package:   *catalogProfile.Package,
		CreatedAt: now,
	}
	rendered, err := opsstate.RenderProfile(renderReq)
	if err != nil {
		return writeClientCommandError(binary, command, *flags.format, traceID, err, stdout, stderr)
	}
	out := opsPlanOutput{
		OK:          true,
		TraceID:     traceID,
		Target:      target,
		TargetKind:  *targetKind,
		Operation:   rendered.Explanation.Name,
		OperationID: opID,
		SagaID:      saga,
		PlanOnly:    *planOnly,
		DryRun:      *dryRun,
		WouldWrite:  run && !*planOnly && !*dryRun,
		Profile:     rendered.Explanation,
		Package:     cloneSchemaPackage(catalogProfile.Package),
		Params:      cloneRawMessage(rendered.Params),
		Paths:       plannedOpsPaths(target, opID, saga),
		Facts: []schema.Fact{
			{Type: "risk", Message: string(rendered.Intent.Risk)},
			{Type: "reversibility", Message: string(rendered.Intent.Reversibility)},
			{Type: "package", Message: catalogProfile.Package.Ref + " " + catalogProfile.Package.Digest},
		},
		RecommendedActions: opsProfileRecommendedActions(binary, target, rendered.Explanation.Name, opID, saga, traceID, rendered.Intent.Risk, rendered.Intent.Reversibility, run && !*planOnly && !*dryRun),
	}
	if run && !*planOnly && !*dryRun {
		loaded, err := flags.load(binary, root, fs)
		if err != nil {
			return writeConfigError(binary, *flags.format, traceID, err, loaded.Redacted().Sources, stdout, stderr)
		}
		if loaded.Config.Env != "" {
			env = loaded.Config.Env
		}
		req := opsstate.ProfileOperationRequest{
			Env:         env,
			OperationID: opID,
			Render:      renderReq,
		}
		var result *opsstate.ProfileOperationResult
		switch loaded.Config.Mode {
		case config.ModeDirect:
			store, err := openOpsObjectStore(loaded.Config)
			if err != nil {
				return writeClientError(binary, command, *flags.format, traceID, err, stdout, stderr)
			}
			result, _, err = opsstate.CreateProfileOperation(nilContext(), store, req)
		case config.ModeAPI, "":
			apiClient, err := client.NewAPI(loaded.Config, client.APIOptions{})
			if err != nil {
				return writeClientError(binary, command, *flags.format, traceID, err, stdout, stderr)
			}
			result, err = apiClient.CreateProfileOperation(nilContext(), req)
		default:
			err = fmt.Errorf("ops run does not support mode %q", loaded.Config.Mode)
		}
		if err != nil {
			return writeClientError(binary, command, *flags.format, traceID, err, stdout, stderr)
		}
		for key, value := range result.Paths {
			out.Paths[key] = value
		}
	}
	return writeOpsProfileOutput(stdout, stderr, binary, command, *flags.format, out)
}

func runOpsInspect(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) > 0 && isSagaIDArg(args[0]) {
		return runSagaInspect(binary, args, root, stdout, stderr)
	}
	fs := flag.NewFlagSet(binary+" ops inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	operation := fs.String("operation", "", "operation ID")

	flagArgs, positionals, err := splitOpsArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "ops", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *operation == "" {
		*operation = positionals[0]
	}
	if *operation == "" || *service == "" {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New("operation ID and --service are required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	var result *opsstate.InspectResult
	switch loaded.Config.Mode {
	case config.ModeDirect:
		store, err := openOpsObjectStore(loaded.Config)
		if err != nil {
			return writeClientError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
		}
		result, err = opsstate.NewStore(store).Inspect(nilContext(), *service, *operation)
	case config.ModeAPI, "":
		apiClient, err := client.NewAPI(loaded.Config, client.APIOptions{})
		if err != nil {
			return writeClientError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
		}
		result, err = apiClient.InspectOperation(nilContext(), client.OperationInspectOptions{
			Service:   *service,
			Operation: *operation,
			TraceID:   *flags.traceID,
		})
	default:
		err = fmt.Errorf("ops inspect does not support mode %q", loaded.Config.Mode)
	}
	if err != nil {
		return writeClientError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		fmt.Fprintf(stdout, "operation: %s\n", result.OperationID)
		fmt.Fprintf(stdout, "service: %s\n", result.Service)
		fmt.Fprintf(stdout, "status: %s\n", result.Status)
		if result.Kind != "" {
			fmt.Fprintf(stdout, "kind: %s\n", result.Kind)
		}
		if len(result.ProviderOperations) > 0 {
			fmt.Fprintf(stdout, "provider_operations: %d\n", len(result.ProviderOperations))
		}
		return ExitSuccess
	case "json", "json-pretty":
		return encodeOpsJSON(stdout, stderr, binary, "ops inspect", *flags.format, opsInspectOutput{OK: true, TraceID: *flags.traceID, Result: *result})
	default:
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runOpsResume(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) > 0 && isSagaIDArg(args[0]) {
		return runSagaResume(binary, args, root, stdout, stderr)
	}
	fs := flag.NewFlagSet(binary+" ops resume", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	operation := fs.String("operation", "", "operation ID")
	leaseDuration := fs.Duration("lease-duration", 30*time.Second, "operation lease duration")

	flagArgs, positionals, err := splitOpsArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "ops", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *operation == "" {
		*operation = positionals[0]
	}
	if *operation == "" || *service == "" {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New("operation ID and --service are required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	store, loaded, exit := loadOpsStore(binary, root, fs, flags, stdout, stderr)
	if exit != ExitSuccess {
		return exit
	}
	cloud, err := newOpsProvider(loaded.Config, store)
	if err != nil {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	result, err := (opsstate.Resumer{Store: store, Provider: cloud}).Resume(nilContext(), opsstate.ResumeRequest{
		Service:       *service,
		OperationID:   *operation,
		Actor:         schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:       *flags.traceID,
		Owner:         "skiff-cli",
		LeaseDuration: *leaseDuration,
	})
	if err != nil {
		return writeOpsRuntimeError(binary, *flags.format, *flags.traceID, err, result, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		if result.Resumed {
			fmt.Fprintf(stdout, "operation %s resumed\n", result.OperationID)
		} else {
			fmt.Fprintf(stdout, "operation %s status=%s\n", result.OperationID, result.Status)
		}
		if result.RolloutStatus != nil {
			fmt.Fprintf(stdout, "rollout: %s\n", result.RolloutStatus.Status)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(opsResumeOutput{OK: true, TraceID: *flags.traceID, Result: *result}); err != nil {
			fmt.Fprintf(stderr, "%s ops resume: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runOpsWatch(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) > 0 && isSagaIDArg(args[0]) {
		return runSagaWatch(binary, args, root, stdout, stderr)
	}
	fs := flag.NewFlagSet(binary+" ops watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name")
	operation := fs.String("operation", "", "operation ID")
	limit := fs.Int("limit", 0, "maximum replay events before watching")
	afterID := fs.String("after", "", "resume after event ID")
	once := fs.Bool("once", false, "exit after the first replayed or watched event")

	flagArgs, positionals, err := splitOpsWatchArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "ops", defaultString(root.Format, "human"), root.TraceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 2 {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[2]), stdout, stderr)
	}
	if len(positionals) == 2 {
		if *service == "" {
			*service = positionals[0]
		}
		if *operation == "" {
			*operation = positionals[1]
		}
	} else if len(positionals) == 1 && *service == "" && *operation != "" {
		*service = positionals[0]
	} else if len(positionals) == 1 && *operation == "" && *service != "" {
		*operation = positionals[0]
	} else if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	if *service == "" {
		return writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New("service is required"), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := client.New(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	scope := "service"
	if *operation != "" {
		scope = "operation"
	}
	return runEventsWatch(eventsWatchContext(), binary, skiffClient, client.EventWatchOptions{
		EventOptions: client.EventOptions{
			Scope:     scope,
			Service:   *service,
			Operation: *operation,
			Limit:     *limit,
			TraceID:   *flags.traceID,
		},
		AfterID:      *afterID,
		PollInterval: eventsWatchPollInterval,
		Once:         *once,
	}, *flags.format, *flags.traceID, stdout, stderr)
}

func runOpsEvents(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runEvents(binary, nil, root, stdout, stderr)
	}
	if isHelpArg(args[0]) {
		printOpsEventsUsage(stdout, binary)
		return ExitSuccess
	}
	first := args[0]
	if strings.HasPrefix(first, "-") {
		return runEvents(binary, args, root, stdout, stderr)
	}
	scopeArgs := eventScopeArgs(first)
	transformed := make([]string, 0, len(scopeArgs)+len(args)-1)
	transformed = append(transformed, scopeArgs...)
	transformed = append(transformed, args[1:]...)
	return runEvents(binary, transformed, root, stdout, stderr)
}

func eventScopeArgs(target string) []string {
	if isSagaID(target) {
		return []string{"--scope", "saga", "--saga", target}
	}
	if strings.HasPrefix(target, "op_") || strings.HasPrefix(target, "operation_") {
		return []string{"--scope", "operation", "--operation", target}
	}
	return []string{"--scope", "service", "--service", target}
}

func isSagaIDArg(arg string) bool {
	return !strings.HasPrefix(arg, "-") && isSagaID(arg)
}

func isSagaID(id string) bool {
	return strings.HasPrefix(id, "saga_") || strings.HasPrefix(id, "saga-")
}

func loadOpsStore(binary string, root rootOptions, fs *flag.FlagSet, flags clientFlagSet, stdout, stderr io.Writer) (objstore.ObjectStore, config.Loaded, int) {
	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return nil, loaded, writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	if loaded.Config.Mode != config.ModeDirect {
		return nil, loaded, writeClientCommandError(binary, "ops", *flags.format, *flags.traceID, errors.New("ops list, inspect, and resume currently require --direct mode"), stdout, stderr)
	}
	store, err := openOpsObjectStore(loaded.Config)
	if err != nil {
		return nil, loaded, writeClientError(binary, "ops", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	return store, loaded, ExitSuccess
}

func parseOpsParams(values []string) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(values))
	for _, raw := range values {
		name, value, ok := strings.Cut(raw, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("operation params must use name=value, got %q", raw)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			value = "null"
		}
		if json.Valid([]byte(value)) {
			out[name] = json.RawMessage(value)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		out[name] = encoded
	}
	return out, nil
}

func plannedOpsPaths(service, operationID, sagaID string) map[string]string {
	out := map[string]string{}
	if key, err := paths.OperationIntent(service, operationID); err == nil {
		out["operation_intent"] = key
	}
	if key, err := paths.OperationControl(service, operationID); err == nil {
		out["operation_control"] = key
	}
	if key, err := paths.SagaIntent(sagaID); err == nil {
		out["saga_intent"] = key
	}
	if key, err := paths.SagaGraph(sagaID); err == nil {
		out["saga_graph"] = key
	}
	if key, err := paths.SagaControl(sagaID); err == nil {
		out["saga_control"] = key
	}
	return out
}

func opsProfileRecommendedActions(binary, target, operation, operationID, sagaID, traceID string, risk schema.Risk, reversibility schema.Reversibility, wrote bool) []recommendedAction {
	if wrote {
		return []recommendedAction{
			{ID: "inspect_operation", Command: fmt.Sprintf("%s ops inspect %s --service %s --format json --trace-id %s", binary, operationID, target, traceID), Mutating: false},
			{ID: "watch_saga", Command: fmt.Sprintf("%s ops watch %s --format json --trace-id %s", binary, sagaID, traceID), Mutating: false},
			{ID: "resume_saga", Command: fmt.Sprintf("%s ops resume %s --format json --trace-id %s", binary, sagaID, traceID), Mutating: true, Safety: "continues typed operation saga", Reversibility: reversibility, Risk: risk},
		}
	}
	return []recommendedAction{
		{ID: "run_operation", Command: fmt.Sprintf("%s ops run %s %s --operation-id %s --saga-id %s --yes --format json --trace-id %s", binary, target, operation, operationID, sagaID, traceID), Mutating: true, Safety: "creates operation and saga intent documents", Reversibility: reversibility, Risk: risk},
	}
}

func writeOpsProfileOutput(stdout, stderr io.Writer, binary, command, format string, out opsPlanOutput) int {
	switch format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s %s\n", out.Target, out.Operation)
		fmt.Fprintf(stdout, "operation: %s\n", out.OperationID)
		fmt.Fprintf(stdout, "saga: %s\n", out.SagaID)
		fmt.Fprintf(stdout, "risk: %s\n", out.Profile.Risk)
		fmt.Fprintf(stdout, "reversibility: %s\n", out.Profile.Reversibility)
		if out.Package != nil {
			fmt.Fprintf(stdout, "package: %s@%s\n", out.Package.Ref, out.Package.Version)
		}
		if out.WouldWrite {
			fmt.Fprintln(stdout, "wrote object state")
		} else {
			fmt.Fprintln(stdout, "no object state written")
		}
		return ExitSuccess
	case "json", "json-pretty":
		return encodeOpsJSON(stdout, stderr, binary, command, format, out)
	default:
		return writeClientCommandError(binary, command, format, out.TraceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func encodeOpsJSON(stdout, stderr io.Writer, binary, command, format string, value any) int {
	if err := writeJSON(stdout, format, value); err != nil {
		fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
		return ExitInternalError
	}
	return ExitSuccess
}

func cloneSchemaPackage(value *schema.PackageProvenance) *schema.PackageProvenance {
	if value == nil {
		return nil
	}
	next := *value
	return &next
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func writeOpsRuntimeError(binary, format, traceID string, err error, result *opsstate.ResumeResult, stdout, stderr io.Writer) int {
	code := "ROLLOUT_FAILED"
	exit := ExitRolloutFailed
	if errors.Is(err, state.ErrLeaseHeld) {
		code = "LEASE_HELD"
		exit = ExitUserError
	} else if errors.Is(err, state.ErrPreconditionFailed) {
		code = "PRECONDITION_FAILED"
		exit = ExitUserError
	}
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(opsRuntimeErrorOutput{
			OK:      false,
			Code:    code,
			Summary: err.Error(),
			TraceID: traceID,
			Result:  result,
			RecommendedActions: []recommendedAction{
				{ID: "inspect_operation", Command: binary + " ops inspect <operation> --service <service> --format json", Mutating: false},
			},
		})
		return exit
	}
	fmt.Fprintf(stderr, "%s ops resume: %v\n", binary, err)
	return exit
}

func splitOpsArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":        true,
		"config":         true,
		"context":        true,
		"env":            true,
		"format":         true,
		"lease-duration": true,
		"limit":          true,
		"lockfile":       true,
		"cache":          true,
		"mode":           true,
		"operation":      true,
		"operation-id":   true,
		"param":          true,
		"provider":       true,
		"region":         true,
		"saga-id":        true,
		"service":        true,
		"state":          true,
		"state-bucket":   true,
		"target-kind":    true,
		"trace-id":       true,
	}
	return splitArgs(args, valueFlags)
}

func splitOpsWatchArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"after":        true,
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"limit":        true,
		"mode":         true,
		"once":         false,
		"operation":    true,
		"provider":     true,
		"region":       true,
		"service":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func printOpsUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s ops <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  list      List available operation profiles or stored operations")
	fmt.Fprintln(w, "  plan      Render an operation profile plan")
	fmt.Fprintln(w, "  run       Create an operation profile saga")
	fmt.Fprintln(w, "  inspect   Inspect an operation or saga")
	fmt.Fprintln(w, "  events    List recent, service, operation, or saga events")
	fmt.Fprintln(w, "  watch     Watch service or operation events")
	fmt.Fprintln(w, "  approve   Approve a waiting saga step")
	fmt.Fprintln(w, "  reject    Reject a waiting saga step")
	fmt.Fprintln(w, "  resume    Resume an operation or saga")
	fmt.Fprintln(w, "  cancel    Register saga cancellation intent")
	fmt.Fprintln(w, "  compensate Register saga compensation intent")
}

func printOpsEventsUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s ops events [service|operation|saga] [flags]\n\n", binary)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --state-dir <dir>")
	fmt.Fprintln(w, "  --scope recent|service|operation|saga")
	fmt.Fprintln(w, "  --service <service>")
	fmt.Fprintln(w, "  --operation <operation>")
	fmt.Fprintln(w, "  --saga <saga>")
	fmt.Fprintln(w, "  --limit <n>")
	fmt.Fprintln(w, "  --fresh")
	fmt.Fprintln(w, "  --watch")
	fmt.Fprintln(w, "  --after <event-id>")
	fmt.Fprintln(w, "  --format human|json|json-pretty")
}
