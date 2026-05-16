package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/config"
)

type statusOutput struct {
	OK      bool          `json:"ok"`
	TraceID string        `json:"trace_id,omitempty"`
	Status  client.Status `json:"status"`
}

type clientEventsOutput struct {
	OK      bool             `json:"ok"`
	TraceID string           `json:"trace_id,omitempty"`
	Result  client.EventList `json:"result"`
}

type clientFlagSet struct {
	format      *string
	noColor     *bool
	traceID     *string
	yes         *bool
	configPath  *string
	env         *string
	provider    *string
	region      *string
	state       *string
	stateBucket *string
	apiURL      *string
	mode        *string
	direct      *bool
	api         *bool
}

func addClientFlags(fs *flag.FlagSet, root rootOptions) clientFlagSet {
	return clientFlagSet{
		format:      fs.String("format", root.Format, "output format: human or json"),
		noColor:     fs.Bool("no-color", root.NoColor, "disable ANSI color output"),
		traceID:     fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output"),
		yes:         fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation"),
		configPath:  fs.String("config", root.ConfigPath, "path to Skiff config file"),
		env:         fs.String("env", root.Env, "Skiff environment name"),
		provider:    fs.String("provider", root.Provider, "cloud provider name"),
		region:      fs.String("region", root.Region, "cloud provider region"),
		state:       fs.String("state", root.State, "object-state bucket URI"),
		stateBucket: fs.String("state-bucket", root.State, "object-state bucket URI"),
		apiURL:      fs.String("api-url", root.APIURL, "skiffd API URL"),
		mode:        fs.String("mode", string(root.Mode), "client mode: api or direct"),
		direct:      fs.Bool("direct", root.directSet, "use direct object-state mode"),
		api:         fs.Bool("api", root.apiSet, "use skiffd API mode"),
	}
}

func (f clientFlagSet) load(binary string, root rootOptions, fs *flag.FlagSet) (config.Loaded, error) {
	if *f.direct && *f.api {
		return config.Loaded{}, errors.New("--api and --direct cannot both be set")
	}
	overrides := root.configOverrides()
	flagToField := map[string]string{
		"env":          config.FieldEnv,
		"provider":     config.FieldProvider,
		"region":       config.FieldRegion,
		"state":        config.FieldStateBucket,
		"state-bucket": config.FieldStateBucket,
		"api-url":      config.FieldAPIURL,
		"mode":         config.FieldMode,
	}
	fs.Visit(func(flag *flag.Flag) {
		if field := flagToField[flag.Name]; field != "" {
			overrides[field] = flag.Value.String()
		}
	})
	if *f.direct {
		overrides[config.FieldMode] = string(config.ModeDirect)
	}
	if *f.api {
		overrides[config.FieldMode] = string(config.ModeAPI)
	}
	loaded, err := config.Load(config.LoadOptions{
		ModeDefault: defaultMode(binary),
		ConfigPath:  *f.configPath,
		Overrides:   overrides,
	})
	if err != nil {
		return loaded, err
	}
	if err := config.Validate(loaded); err != nil {
		return loaded, err
	}
	return loaded, nil
}

func runStatus(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags := addClientFlags(fs, root)
	service := fs.String("service", "", "service name to filter")
	fresh := fs.Bool("fresh", false, "bypass cached API views where supported")

	if err := fs.Parse(args); err != nil {
		return writeClientCommandError(binary, "status", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if fs.NArg() > 1 {
		return writeClientCommandError(binary, "status", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", fs.Arg(1)), stdout, stderr)
	}
	if fs.NArg() == 1 && *service == "" {
		*service = fs.Arg(0)
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := client.New(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "status", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	status, err := skiffClient.Status(nilContext(), client.StatusOptions{Service: *service, Fresh: *fresh, TraceID: *flags.traceID})
	if err != nil {
		return writeClientError(binary, "status", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	switch *flags.format {
	case "human", "text":
		printStatusHuman(stdout, *status)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(statusOutput{OK: true, TraceID: *flags.traceID, Status: *status}); err != nil {
			fmt.Fprintf(stderr, "%s status: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "status", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func printStatusHuman(w io.Writer, status client.Status) {
	fmt.Fprintf(w, "mode: %s\n", status.Mode)
	if status.Env != "" {
		fmt.Fprintf(w, "env: %s\n", status.Env)
	}
	if status.Provider != "" || status.Region != "" {
		fmt.Fprintf(w, "provider: %s %s\n", status.Provider, status.Region)
	}
	if status.StateBucket != "" {
		fmt.Fprintf(w, "state: %s\n", status.StateBucket)
	}
	if status.APIURL != "" {
		fmt.Fprintf(w, "api: %s\n", status.APIURL)
	}
	if len(status.Services) == 0 {
		fmt.Fprintln(w, "services: none")
	} else {
		fmt.Fprintln(w, "services:")
		for _, service := range status.Services {
			release := firstNonEmptyCLI(service.DesiredRelease, "<none>")
			fmt.Fprintf(w, "- %s env=%s desired=%s stable=%s", service.Service, service.Env, release, firstNonEmptyCLI(service.StableRelease, "<none>"))
			if service.OperationID != "" {
				fmt.Fprintf(w, " operation=%s:%s", service.OperationID, service.OperationState)
			}
			fmt.Fprintln(w)
		}
	}
	if len(status.Findings) > 0 {
		fmt.Fprintln(w, "findings:")
		for _, finding := range status.Findings {
			fmt.Fprintf(w, "- %s: %s\n", finding.Code, finding.Summary)
		}
	}
}

func writeClientCommandError(binary, command, format, traceID string, err error, stdout, stderr io.Writer) int {
	if format == "json" {
		_ = json.NewEncoder(stdout).Encode(commandErrorOutput{
			OK:      false,
			Code:    strings.ToUpper(command) + "_INVALID",
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{ID: "show_help", Command: binary + " " + command + " --help", Mutating: false},
			},
		})
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
	return ExitUserError
}

func runCompletion(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintf(stderr, "%s completion: expected shell name bash, zsh, or fish\n", binary)
		return ExitUserError
	}
	shell := args[0]
	commands := []string{"bootstrap", "compile", "config", "completion", "events", "explain", "object", "plan", "policy", "release", "state", "status", "validate", "version"}
	sort.Strings(commands)
	switch shell {
	case "bash":
		fmt.Fprintf(stdout, "_%s_completion() {\n", binary)
		fmt.Fprintf(stdout, "  COMPREPLY=($(compgen -W \"%s\" -- \"${COMP_WORDS[1]}\"))\n", strings.Join(commands, " "))
		fmt.Fprintln(stdout, "}")
		fmt.Fprintf(stdout, "complete -F _%s_completion %s\n", binary, binary)
	case "zsh":
		fmt.Fprintf(stdout, "#compdef %s\n", binary)
		fmt.Fprintf(stdout, "_arguments '1:command:(%s)'\n", strings.Join(commands, " "))
	case "fish":
		for _, command := range commands {
			fmt.Fprintf(stdout, "complete -c %s -f -a %s\n", binary, command)
		}
	default:
		fmt.Fprintf(stderr, "%s completion: unsupported shell %q\n", binary, shell)
		return ExitUserError
	}
	return ExitSuccess
}

func firstNonEmptyCLI(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
