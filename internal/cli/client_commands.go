package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

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

var (
	newStatusClient     = client.New
	statusContext       = nilContext
	statusWatchInterval = 2 * time.Second
)

type clientFlagSet struct {
	format                          *string
	noColor                         *bool
	traceID                         *string
	yes                             *bool
	configPath                      *string
	context                         *string
	env                             *string
	provider                        *string
	region                          *string
	state                           *string
	stateBucket                     *string
	awsLiveApply                    *bool
	awsVPCID                        *string
	awsSubnetIDs                    *string
	awsAMIID                        *string
	awsALBListenerARN               *string
	awsLoadBalancerSecurityGroupRef *string
	apiURL                          *string
	mode                            *string
	direct                          *bool
	api                             *bool
}

func addClientFlags(fs *flag.FlagSet, root rootOptions) clientFlagSet {
	return clientFlagSet{
		format:                          fs.String("format", root.Format, "output format: human, json, or json-pretty"),
		noColor:                         fs.Bool("no-color", root.NoColor, "disable ANSI color output"),
		traceID:                         fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output"),
		yes:                             fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation"),
		configPath:                      fs.String("config", root.ConfigPath, "path to Skiff config file"),
		context:                         fs.String("context", root.Context, "Skiff config context name"),
		env:                             fs.String("env", root.Env, "Skiff environment name"),
		provider:                        fs.String("provider", root.Provider, "cloud provider name"),
		region:                          fs.String("region", root.Region, "cloud provider region"),
		state:                           fs.String("state", root.State, "object-state bucket URI"),
		stateBucket:                     fs.String("state-bucket", root.State, "object-state bucket URI"),
		awsLiveApply:                    fs.Bool("aws-live-apply", false, "validate AWS live apply provider inputs"),
		awsVPCID:                        fs.String("aws-vpc-id", "", "AWS VPC ID for live apply resources"),
		awsSubnetIDs:                    fs.String("aws-subnet-ids", "", "comma-separated AWS subnet IDs for live apply Auto Scaling Groups"),
		awsAMIID:                        fs.String("aws-ami-id", "", "AWS AMI ID for live apply launch templates"),
		awsALBListenerARN:               fs.String("aws-alb-listener-arn", "", "AWS ALB listener ARN for live apply listener rules"),
		awsLoadBalancerSecurityGroupRef: fs.String("aws-load-balancer-security-group-ref", "", "AWS load balancer security group ID/ref for live apply instance ingress"),
		apiURL:                          fs.String("api-url", root.APIURL, "skiffd API URL"),
		mode:                            fs.String("mode", string(root.Mode), "client mode: api or direct"),
		direct:                          fs.Bool("direct", root.directSet, "use direct object-state mode"),
		api:                             fs.Bool("api", root.apiSet, "use skiffd API mode"),
	}
}

func (f clientFlagSet) load(binary string, root rootOptions, fs *flag.FlagSet) (config.Loaded, error) {
	if *f.direct && *f.api {
		return config.Loaded{}, errors.New("--api and --direct cannot both be set")
	}
	overrides := root.configOverrides()
	flagToField := map[string]string{
		"env":                                  config.FieldEnv,
		"provider":                             config.FieldProvider,
		"region":                               config.FieldRegion,
		"state":                                config.FieldStateBucket,
		"state-bucket":                         config.FieldStateBucket,
		"aws-live-apply":                       config.FieldAWSLiveApply,
		"aws-vpc-id":                           config.FieldAWSVPCID,
		"aws-subnet-ids":                       config.FieldAWSSubnetIDs,
		"aws-ami-id":                           config.FieldAWSAMIID,
		"aws-alb-listener-arn":                 config.FieldAWSALBListenerARN,
		"aws-load-balancer-security-group-ref": config.FieldAWSLoadBalancerSecurityGroupRef,
		"api-url":                              config.FieldAPIURL,
		"mode":                                 config.FieldMode,
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
	contextName := root.Context
	if f.context != nil {
		contextName = *f.context
	}
	loaded, err := config.Load(config.LoadOptions{
		ModeDefault: defaultMode(binary),
		ConfigPath:  *f.configPath,
		Context:     contextName,
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
	watch := fs.Bool("watch", false, "watch status until interrupted")

	flagArgs, positionals, err := splitStatusArgs(args)
	if err != nil {
		return writeClientCommandError(binary, "status", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeClientCommandError(binary, "status", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeClientCommandError(binary, "status", *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if len(positionals) == 1 && *service == "" {
		*service = positionals[0]
	}
	_ = flags.noColor
	_ = flags.yes

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := newStatusClient(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "status", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if *watch {
		return runStatusWatch(statusContext(), binary, skiffClient, client.StatusOptions{Service: *service, Fresh: *fresh, TraceID: *flags.traceID}, *flags.format, *flags.traceID, stdout, stderr)
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
		return writeClientCommandError(binary, "status", *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func splitStatusArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"api-url":      true,
		"config":       true,
		"context":      true,
		"env":          true,
		"format":       true,
		"mode":         true,
		"provider":     true,
		"region":       true,
		"service":      true,
		"state":        true,
		"state-bucket": true,
		"trace-id":     true,
	}
	return splitArgs(args, valueFlags)
}

func runStatusWatch(ctx context.Context, binary string, skiffClient client.Interface, opts client.StatusOptions, format, traceID string, stdout, stderr io.Writer) int {
	for {
		status, err := skiffClient.Status(ctx, opts)
		if err != nil {
			if ctx.Err() != nil {
				return ExitSuccess
			}
			return writeClientError(binary, "status", format, traceID, err, stdout, stderr)
		}
		switch format {
		case "human", "text":
			printStatusHuman(stdout, *status)
		case "json":
			if err := json.NewEncoder(stdout).Encode(statusOutput{OK: true, TraceID: traceID, Status: *status}); err != nil {
				fmt.Fprintf(stderr, "%s status: %v\n", binary, err)
				return ExitInternalError
			}
		default:
			return writeClientCommandError(binary, "status", format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
		}
		if !waitForStatusWatch(ctx) {
			return ExitSuccess
		}
	}
}

func waitForStatusWatch(ctx context.Context) bool {
	interval := statusWatchInterval
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
			fmt.Fprintf(w, "- %s health=%s env=%s desired=%s stable=%s", service.Service, firstNonEmptyCLI(service.Health, "unknown"), service.Env, release, firstNonEmptyCLI(service.StableRelease, "<none>"))
			if service.OperationID != "" {
				fmt.Fprintf(w, " operation=%s:%s", service.OperationID, service.OperationState)
			}
			if service.Database.Status != "" {
				fmt.Fprintf(w, " database=%s", service.Database.Status)
			}
			if service.Logs.Status != "" {
				fmt.Fprintf(w, " logs=%s", service.Logs.Status)
			}
			if service.Metrics.Status != "" {
				fmt.Fprintf(w, " metrics=%s", service.Metrics.Status)
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
	if isJSONFormat(format) {
		_ = writeJSON(stdout, format, commandErrorOutput{
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
	commands := []string{"adopt", "authz", "bootstrap", "ci", "compile", "config", "completion", "contract", "cost", "cutover", "database", "debug", "deploy", "doctor", "drift", "events", "explain", "failover", "gc", "import", "init", "logs", "metrics", "object", "ops", "plan", "plugin", "policy", "promote", "release", "rollback", "rollout", "rotate", "saga", "solve", "state", "stateful", "status", "terraform", "tui", "validate", "version"}
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
