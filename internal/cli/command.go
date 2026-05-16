package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/buildinfo"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/state/paths"
)

const (
	ExitSuccess   = 0
	ExitUserError = 1
)

type versionOutput struct {
	OK        bool   `json:"ok"`
	Binary    string `json:"binary"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	TraceID   string `json:"trace_id,omitempty"`
}

func Run(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr, binary)
		return ExitUserError
	}

	switch args[0] {
	case "config":
		return runConfig(binary, args[1:], stdout, stderr)
	case "state":
		return runState(binary, args[1:], stdout, stderr)
	case "version":
		if err := runVersion(binary, args[1:], stdout); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	case "help", "-h", "--help":
		printUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n", binary, args[0])
		printUsage(stderr, binary)
		return ExitUserError
	}
}

func runVersion(binary string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet(binary+" version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "human", "output format: human or json")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	_ = noColor
	_ = yes

	info := buildinfo.Current(binary)
	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s version %s\n", info.Binary, info.Version)
		fmt.Fprintf(stdout, "commit: %s\n", info.Commit)
		fmt.Fprintf(stdout, "build_date: %s\n", info.BuildDate)
		if *traceID != "" {
			fmt.Fprintf(stdout, "trace_id: %s\n", *traceID)
		}
		return nil
	case "json":
		enc := json.NewEncoder(stdout)
		return enc.Encode(versionOutput{
			OK:        true,
			Binary:    info.Binary,
			Version:   info.Version,
			Commit:    info.Commit,
			BuildDate: info.BuildDate,
			TraceID:   *traceID,
		})
	default:
		return errors.New(`unsupported format; expected "human" or "json"`)
	}
}

func printUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  config     Inspect effective configuration")
	fmt.Fprintln(w, "  state      Inspect object-state paths and developer helpers")
	fmt.Fprintln(w, "  version    Print version, commit, and build date")
}

type configShowOutput struct {
	OK      bool              `json:"ok"`
	TraceID string            `json:"trace_id,omitempty"`
	Config  config.Config     `json:"config"`
	Sources map[string]string `json:"sources,omitempty"`
}

type commandErrorOutput struct {
	OK                 bool                `json:"ok"`
	Code               string              `json:"code"`
	Summary            string              `json:"summary"`
	TraceID            string              `json:"trace_id,omitempty"`
	Fields             []config.FieldError `json:"fields,omitempty"`
	Sources            map[string]string   `json:"sources,omitempty"`
	RecommendedActions []recommendedAction `json:"recommended_actions,omitempty"`
}

type recommendedAction struct {
	ID       string `json:"id"`
	Command  string `json:"command"`
	Mutating bool   `json:"mutating"`
}

type statePathOutput struct {
	OK      bool              `json:"ok"`
	TraceID string            `json:"trace_id,omitempty"`
	Kind    string            `json:"kind"`
	Path    string            `json:"path"`
	Inputs  map[string]string `json:"inputs,omitempty"`
}

type stateErrorOutput struct {
	OK                 bool                `json:"ok"`
	Code               string              `json:"code"`
	Summary            string              `json:"summary"`
	TraceID            string              `json:"trace_id,omitempty"`
	Fields             []paths.InputError  `json:"fields,omitempty"`
	RecommendedActions []recommendedAction `json:"recommended_actions,omitempty"`
}

func runConfig(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printConfigUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "show":
		return runConfigShow(binary, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printConfigUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s config: unknown command %q\n", binary, args[0])
		printConfigUsage(stderr, binary)
		return ExitUserError
	}
}

func runConfigShow(binary string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" config show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "human", "output format: human or json")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	configPath := fs.String("config", "", "path to Skiff config file")
	userDataPath := fs.String("user-data", "", "path to runner cloud-init/user-data JSON")

	fs.String("env", "", "Skiff environment name")
	fs.String("provider", "", "cloud provider name")
	fs.String("region", "", "cloud provider region")
	fs.String("state-bucket", "", "object-state bucket URI")
	fs.String("kms-key", "", "KMS key ID or alias")
	fs.String("auth-mode", "", "auth mode")
	fs.String("log-level", "", "log level")
	fs.String("mode", "", "config mode: api, direct, skiffd, or runner")
	fs.String("api-url", "", "skiffd API URL")
	fs.String("service", "", "runner service name")
	fs.String("control-key", "", "runner service control object key")
	fs.String("state", "", "alias for --state-bucket")

	if err := fs.Parse(args); err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeConfigError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), nil, stdout, stderr)
	}
	_ = noColor
	_ = yes

	overrides := make(map[string]string)
	flagToField := map[string]string{
		"env":          config.FieldEnv,
		"provider":     config.FieldProvider,
		"region":       config.FieldRegion,
		"state-bucket": config.FieldStateBucket,
		"state":        config.FieldStateBucket,
		"kms-key":      config.FieldKMSKey,
		"auth-mode":    config.FieldAuthMode,
		"log-level":    config.FieldLogLevel,
		"mode":         config.FieldMode,
		"api-url":      config.FieldAPIURL,
		"service":      config.FieldService,
		"control-key":  config.FieldControlKey,
	}
	fs.Visit(func(f *flag.Flag) {
		if field := flagToField[f.Name]; field != "" {
			overrides[field] = f.Value.String()
		}
	})

	loaded, err := config.Load(config.LoadOptions{
		ModeDefault:  defaultMode(binary),
		ConfigPath:   *configPath,
		UserDataPath: *userDataPath,
		Overrides:    overrides,
	})
	if err == nil {
		err = config.Validate(loaded)
	}
	if err != nil {
		return writeConfigError(binary, *format, *traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}

	redacted := loaded.Redacted()
	switch *format {
	case "human", "text":
		printConfigHuman(stdout, redacted)
		return ExitSuccess
	case "json":
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(configShowOutput{
			OK:      true,
			TraceID: *traceID,
			Config:  redacted.Config,
			Sources: redacted.Sources,
		}); err != nil {
			fmt.Fprintf(stderr, "%s config show: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	default:
		return writeConfigError(binary, *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), redacted.Sources, stdout, stderr)
	}
}

func printConfigHuman(w io.Writer, loaded config.Loaded) {
	for _, field := range config.FieldNames() {
		value := configValue(loaded.Config, field)
		if value == "" {
			value = "<unset>"
		}
		fmt.Fprintf(w, "%s: %s", field, value)
		if source := loaded.Sources[field]; source != "" {
			fmt.Fprintf(w, " (%s)", source)
		}
		fmt.Fprintln(w)
	}
}

func writeConfigError(binary, format, traceID string, err error, sources map[string]string, stdout, stderr io.Writer) int {
	if format == "json" {
		out := commandErrorOutput{
			OK:      false,
			Code:    "CONFIG_LOAD_FAILED",
			Summary: err.Error(),
			TraceID: traceID,
			Sources: sources,
			RecommendedActions: []recommendedAction{
				{
					ID:       "inspect_config",
					Command:  binary + " config show --format json --mode direct --config <path>",
					Mutating: false,
				},
			},
		}
		var validation config.ValidationError
		if errors.As(err, &validation) {
			out.Code = "CONFIG_INVALID"
			out.Fields = validation.Fields
		}
		enc := json.NewEncoder(stdout)
		if encErr := enc.Encode(out); encErr != nil {
			fmt.Fprintf(stderr, "%s config show: %v\n", binary, encErr)
		}
		return ExitUserError
	}

	fmt.Fprintf(stderr, "%s config show: %v\n", binary, err)
	return ExitUserError
}

func printConfigUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s config <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  show       Print effective configuration")
}

func runState(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printStateUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "path":
		return runStatePath(binary, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printStateUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s state: unknown command %q\n", binary, args[0])
		printStateUsage(stderr, binary)
		return ExitUserError
	}
}

func runStatePath(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printStatePathUsage(stderr, binary)
		return ExitUserError
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printStatePathUsage(stdout, binary)
		return ExitSuccess
	}

	pathKind := args[0]
	fs := flag.NewFlagSet(binary+" state path "+pathKind, flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "human", "output format: human or json")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	service := fs.String("service", "", "service name")
	release := fs.String("release", "", "release ID")
	operation := fs.String("operation", "", "operation ID")
	saga := fs.String("saga", "", "saga ID")
	event := fs.String("event", "", "event ID")
	doc := fs.String("doc", "", "document selector")
	resourceKind := fs.String("resource-kind", "", "resource kind")
	name := fs.String("name", "", "logical resource name")
	provider := fs.String("provider", "", "cloud provider")
	providerID := fs.String("id", "", "provider resource ID")
	day := fs.String("day", "", "UTC audit day yyyy-mm-dd")
	observation := fs.String("observation", "", "observation ID")

	if err := fs.Parse(args[1:]); err != nil {
		return writeStateError(binary, *format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeStateError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	_ = noColor
	_ = yes

	path, err := statePathFor(pathKind, statePathInputs{
		service:      *service,
		release:      *release,
		operation:    *operation,
		saga:         *saga,
		event:        *event,
		doc:          *doc,
		resourceKind: *resourceKind,
		name:         *name,
		provider:     *provider,
		providerID:   *providerID,
		day:          *day,
		observation:  *observation,
	})
	if err != nil {
		return writeStateError(binary, *format, *traceID, err, stdout, stderr)
	}

	switch *format {
	case "human", "text":
		fmt.Fprintln(stdout, path)
		return ExitSuccess
	case "json":
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(statePathOutput{
			OK:      true,
			TraceID: *traceID,
			Kind:    pathKind,
			Path:    path,
			Inputs: statePathInputMap(statePathInputs{
				service:      *service,
				release:      *release,
				operation:    *operation,
				saga:         *saga,
				event:        *event,
				doc:          *doc,
				resourceKind: *resourceKind,
				name:         *name,
				provider:     *provider,
				providerID:   *providerID,
				day:          *day,
				observation:  *observation,
			}),
		}); err != nil {
			fmt.Fprintf(stderr, "%s state path: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	default:
		return writeStateError(binary, *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

type statePathInputs struct {
	service      string
	release      string
	operation    string
	saga         string
	event        string
	doc          string
	resourceKind string
	name         string
	provider     string
	providerID   string
	day          string
	observation  string
}

func statePathFor(kind string, in statePathInputs) (string, error) {
	switch kind {
	case "service":
		return paths.ServiceControl(in.service)
	case "release":
		switch defaultString(in.doc, "release") {
		case "release":
			return paths.ReleaseManifest(in.service, in.release)
		case "runtime-manifest":
			return paths.RuntimeManifest(in.service, in.release)
		default:
			return "", fmt.Errorf("release --doc must be release or runtime-manifest")
		}
	case "operation":
		switch defaultString(in.doc, "intent") {
		case "intent":
			return paths.OperationIntent(in.service, in.operation)
		case "control":
			return paths.OperationControl(in.service, in.operation)
		case "event":
			return paths.OperationEvent(in.service, in.operation, in.event)
		default:
			return "", fmt.Errorf("operation --doc must be intent, control, or event")
		}
	case "saga":
		switch defaultString(in.doc, "intent") {
		case "intent":
			return paths.SagaIntent(in.saga)
		case "graph":
			return paths.SagaGraph(in.saga)
		case "control":
			return paths.SagaControl(in.saga)
		case "event":
			return paths.SagaEvent(in.saga, in.event)
		default:
			return "", fmt.Errorf("saga --doc must be intent, graph, control, or event")
		}
	case "resource-logical":
		return paths.LogicalResource(in.resourceKind, in.name)
	case "resource-provider":
		return paths.ProviderResource(in.provider, in.resourceKind, in.providerID)
	case "index":
		switch in.doc {
		case "services":
			return paths.ServicesIndex(), nil
		case "active-sagas":
			return paths.ActiveSagasIndex(), nil
		case "recent-events":
			return paths.RecentEventsIndex(), nil
		default:
			return "", fmt.Errorf("index --doc must be services, active-sagas, or recent-events")
		}
	case "audit":
		return paths.AuditEvent(in.day, in.event)
	case "observation":
		return paths.ServiceObservation(in.service, in.observation)
	default:
		return "", fmt.Errorf("unknown state path kind %q", kind)
	}
}

func statePathInputMap(in statePathInputs) map[string]string {
	values := map[string]string{
		"service":       in.service,
		"release":       in.release,
		"operation":     in.operation,
		"saga":          in.saga,
		"event":         in.event,
		"doc":           in.doc,
		"resource_kind": in.resourceKind,
		"name":          in.name,
		"provider":      in.provider,
		"id":            in.providerID,
		"day":           in.day,
		"observation":   in.observation,
	}
	out := make(map[string]string)
	for key, value := range values {
		if value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func writeStateError(binary, format, traceID string, err error, stdout, stderr io.Writer) int {
	if format == "json" {
		out := stateErrorOutput{
			OK:      false,
			Code:    "STATE_PATH_INVALID",
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{
					ID:       "inspect_state_paths",
					Command:  binary + " state path service --service <service> --format json",
					Mutating: false,
				},
			},
		}
		var inputErr paths.InputError
		if errors.As(err, &inputErr) {
			out.Fields = []paths.InputError{inputErr}
		}
		enc := json.NewEncoder(stdout)
		if encErr := enc.Encode(out); encErr != nil {
			fmt.Fprintf(stderr, "%s state path: %v\n", binary, encErr)
		}
		return ExitUserError
	}

	fmt.Fprintf(stderr, "%s state path: %v\n", binary, err)
	return ExitUserError
}

func printStateUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s state <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  path       Print canonical object-state keys")
}

func printStatePathUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s state path <kind> [flags]\n\n", binary)
	fmt.Fprintln(w, "Kinds:")
	fmt.Fprintln(w, "  service             --service <name>")
	fmt.Fprintln(w, "  release             --service <name> --release <id> [--doc release|runtime-manifest]")
	fmt.Fprintln(w, "  operation           --service <name> --operation <id> [--doc intent|control|event] [--event <id>]")
	fmt.Fprintln(w, "  saga                --saga <id> [--doc intent|graph|control|event] [--event <id>]")
	fmt.Fprintln(w, "  resource-logical    --resource-kind <kind> --name <name>")
	fmt.Fprintln(w, "  resource-provider   --provider <name> --resource-kind <kind> --id <provider-id>")
	fmt.Fprintln(w, "  index               --doc services|active-sagas|recent-events")
	fmt.Fprintln(w, "  audit               --day <yyyy-mm-dd> --event <id>")
	fmt.Fprintln(w, "  observation         --service <name> --observation <id>")
}

func defaultMode(binary string) config.Mode {
	switch binary {
	case "skiffd":
		return config.ModeSkiffd
	case "skiff-runner":
		return config.ModeRunner
	default:
		return config.ModeAPI
	}
}

func configValue(cfg config.Config, field string) string {
	switch field {
	case config.FieldEnv:
		return cfg.Env
	case config.FieldProvider:
		return cfg.Provider
	case config.FieldRegion:
		return cfg.Region
	case config.FieldStateBucket:
		return cfg.StateBucket
	case config.FieldKMSKey:
		return cfg.KMSKey
	case config.FieldAuthMode:
		return cfg.AuthMode
	case config.FieldLogLevel:
		return cfg.LogLevel
	case config.FieldMode:
		return string(cfg.Mode)
	case config.FieldAPIURL:
		return cfg.APIURL
	case config.FieldService:
		return cfg.Service
	case config.FieldControlKey:
		return cfg.ControlKey
	default:
		return ""
	}
}
