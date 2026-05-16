package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/buildinfo"
	"github.com/s1liconcow/skiff/internal/config"
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
