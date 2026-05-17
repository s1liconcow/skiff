package cli

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/bootstrap"
	"github.com/s1liconcow/skiff/internal/buildinfo"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/compat"
	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	skifferrors "github.com/s1liconcow/skiff/internal/errors"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/release"
	securitypolicy "github.com/s1liconcow/skiff/internal/security/policy"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	ExitSuccess       = 0
	ExitUserError     = 1
	ExitPolicyDenied  = 2
	ExitProviderError = 3
	ExitRolloutFailed = 4
	ExitPartial       = 5
	ExitAuthError     = 6
	ExitTimeout       = 7
	ExitInternalError = 8
)

type versionOutput struct {
	OK            bool             `json:"ok"`
	Binary        string           `json:"binary"`
	Version       string           `json:"version"`
	Commit        string           `json:"commit"`
	BuildDate     string           `json:"build_date"`
	TraceID       string           `json:"trace_id,omitempty"`
	Server        *client.Version  `json:"server,omitempty"`
	Compatibility []compat.Finding `json:"compatibility,omitempty"`
}

func Run(binary string, args []string, stdout, stderr io.Writer) int {
	root, err := parseRootArgs(args)
	if err != nil {
		return writeRootError(binary, root.Format, root.TraceID, err, stdout, stderr)
	}
	if root.Command == "" {
		printUsage(stderr, binary)
		return ExitUserError
	}

	switch root.Command {
	case "authz":
		return runAuthz(binary, root.Args, root, stdout, stderr)
	case "bootstrap":
		return runBootstrap(binary, root.Args, stdout, stderr)
	case "compile":
		return runCompile(binary, root.Args, stdout, stderr)
	case "config":
		return runConfig(binary, root.Args, root, stdout, stderr)
	case "completion":
		return runCompletion(binary, root.Args, stdout, stderr)
	case "deploy":
		return runDeploy(binary, root.Args, root, stdout, stderr)
	case "database":
		return runDatabase(binary, root.Args, root, stdout, stderr)
	case "doctor":
		return runDoctor(binary, root.Args, root, stdout, stderr)
	case "drift":
		return runDrift(binary, root.Args, root, stdout, stderr)
	case "events":
		return runEvents(binary, root.Args, root, stdout, stderr)
	case "explain":
		return runExplain(binary, root.Args, root, stdout, stderr)
	case "failover":
		return runFailover(binary, root.Args, root, stdout, stderr)
	case "init":
		return runInit(binary, root.Args, root, stdout, stderr)
	case "logs":
		return runLogs(binary, root.Args, root, stdout, stderr)
	case "gc":
		return runGC(binary, root.Args, root, stdout, stderr)
	case "metrics":
		return runMetrics(binary, root.Args, root, stdout, stderr)
	case "object":
		return runObject(binary, root.Args, stdout, stderr)
	case "ops":
		return runOps(binary, root.Args, root, stdout, stderr)
	case "plan":
		return runPlan(binary, root.Args, root, stdout, stderr)
	case "plugin":
		return runPlugin(binary, root.Args, root, stdout, stderr)
	case "policy":
		return runPolicy(binary, root.Args, stdout, stderr)
	case "release":
		return runRelease(binary, root.Args, root, stdout, stderr)
	case "promote":
		return runPromote(binary, root.Args, root, stdout, stderr)
	case "rollback":
		return runRollback(binary, root.Args, root, stdout, stderr)
	case "rollout":
		return runRollout(binary, root.Args, root, stdout, stderr)
	case "saga":
		return runSaga(binary, root.Args, root, stdout, stderr)
	case "solve":
		return runSolve(binary, root.Args, root, stdout, stderr)
	case "state":
		return runState(binary, root.Args, stdout, stderr)
	case "status":
		return runStatus(binary, root.Args, root, stdout, stderr)
	case "tui":
		return runTUI(binary, root.Args, root, stdout, stderr)
	case "validate":
		return runValidate(binary, root.Args, stdout, stderr)
	case "version":
		return runVersion(binary, root.Args, root, stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n", binary, root.Command)
		printUsage(stderr, binary)
		return ExitUserError
	}
}

func runVersion(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", root.Format, "output format: human or json")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")

	if err := fs.Parse(args); err != nil {
		return writeRootError(binary, *format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeRootError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	_ = noColor
	_ = yes

	info := buildinfo.Current(binary)
	var server *client.Version
	var compatibility []compat.Finding
	if root.apiSet || root.Mode == config.ModeAPI {
		loaded, err := config.Load(config.LoadOptions{
			ModeDefault: defaultMode(binary),
			ConfigPath:  root.ConfigPath,
			Overrides:   root.configOverrides(),
		})
		if err != nil {
			return writeConfigError(binary, *format, *traceID, err, loaded.Redacted().Sources, stdout, stderr)
		}
		if err := config.Validate(loaded); err != nil {
			return writeConfigError(binary, *format, *traceID, err, loaded.Redacted().Sources, stdout, stderr)
		}
		apiClient, err := client.New(loaded.Config, client.Options{})
		if err != nil {
			return writeClientError(binary, "version", *format, *traceID, err, stdout, stderr)
		}
		server, err = apiClient.Version(nilContext(), client.VersionOptions{Binary: "skiffd", TraceID: *traceID})
		if err != nil {
			return writeClientError(binary, "version", *format, *traceID, err, stdout, stderr)
		}
		compatibility = compat.CheckClientServer(info.Version, server.Version)
	}
	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s version %s\n", info.Binary, info.Version)
		fmt.Fprintf(stdout, "commit: %s\n", info.Commit)
		fmt.Fprintf(stdout, "build_date: %s\n", info.BuildDate)
		if server != nil {
			fmt.Fprintf(stdout, "skiffd: %s\n", server.Version)
		}
		for _, finding := range compatibility {
			fmt.Fprintf(stdout, "warning: %s: %s\n", finding.Code, finding.Summary)
		}
		if *traceID != "" {
			fmt.Fprintf(stdout, "trace_id: %s\n", *traceID)
		}
		return ExitSuccess
	case "json":
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(versionOutput{
			OK:            true,
			Binary:        info.Binary,
			Version:       info.Version,
			Commit:        info.Commit,
			BuildDate:     info.BuildDate,
			TraceID:       *traceID,
			Server:        server,
			Compatibility: compatibility,
		}); err != nil {
			fmt.Fprintf(stderr, "%s version: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeRootError(binary, *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func printUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  authz      Explain authorization and approval decisions")
	fmt.Fprintln(w, "  bootstrap  Bootstrap initial cloud state substrate")
	fmt.Fprintln(w, "  compile    Compile a Skiff spec to provider-neutral IR")
	fmt.Fprintln(w, "  config     Inspect effective configuration")
	fmt.Fprintln(w, "  completion Generate shell completions")
	fmt.Fprintln(w, "  deploy     Publish and deploy a service release")
	fmt.Fprintln(w, "  database   Run managed database backup and restore sagas")
	fmt.Fprintln(w, "  doctor     Diagnose service health and recommend actions")
	fmt.Fprintln(w, "  drift      Detect provider drift from Skiff resource records")
	fmt.Fprintln(w, "  events     List local service, operation, or saga events")
	fmt.Fprintln(w, "  explain    Explain provider cloud primitives for a spec")
	fmt.Fprintln(w, "  failover   Run a regional failover saga")
	fmt.Fprintln(w, "  gc         Plan and apply conservative cleanup actions")
	fmt.Fprintln(w, "  init       Generate starter Skiff specs and recipes")
	fmt.Fprintln(w, "  logs       Query service logs through the cloud provider")
	fmt.Fprintln(w, "  metrics    Query service metrics through the cloud provider")
	fmt.Fprintln(w, "  object     Verify signed immutable objects")
	fmt.Fprintln(w, "  ops        Inspect, resume, and watch operations")
	fmt.Fprintln(w, "  plan       Dry-run provider resource changes for a spec")
	fmt.Fprintln(w, "  plugin     Inspect, validate, and run trusted Skiff plugins")
	fmt.Fprintln(w, "  policy     Explain generated state security policies")
	fmt.Fprintln(w, "  promote    Validate and record release promotion intent")
	fmt.Fprintln(w, "  release    Verify release manifests")
	fmt.Fprintln(w, "  rollback   Roll a service back to a stable release")
	fmt.Fprintln(w, "  rollout    Watch rollout progress")
	fmt.Fprintln(w, "  saga       Inspect saga object state")
	fmt.Fprintln(w, "  solve      Build an agent action graph for service recovery")
	if binary == "skiffd" {
		fmt.Fprintln(w, "  serve      Start the stateless skiffd API server")
	}
	fmt.Fprintln(w, "  state      Inspect object-state paths and developer helpers")
	fmt.Fprintln(w, "  status     Show service status through direct or API mode")
	fmt.Fprintln(w, "  tui        Open the terminal operations dashboard")
	fmt.Fprintln(w, "  validate   Parse, default, and validate a Skiff spec")
	fmt.Fprintln(w, "  version    Print version, commit, and build date")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --config <path> --env <env> --provider <provider> --region <region>")
	fmt.Fprintln(w, "  --state <uri> --api --direct --format human|json --no-color --yes --trace-id <id>")
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
	ID            string               `json:"id"`
	Command       string               `json:"command"`
	Mutating      bool                 `json:"mutating"`
	Safety        string               `json:"safety,omitempty"`
	Reversibility schema.Reversibility `json:"reversibility,omitempty"`
	Risk          schema.Risk          `json:"risk,omitempty"`
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

type verifyErrorOutput struct {
	OK                 bool                `json:"ok"`
	Code               string              `json:"code"`
	Summary            string              `json:"summary"`
	TraceID            string              `json:"trace_id,omitempty"`
	RecommendedActions []recommendedAction `json:"recommended_actions,omitempty"`
}

type releaseVerifyOutput struct {
	OK      bool                       `json:"ok"`
	TraceID string                     `json:"trace_id,omitempty"`
	Result  release.VerificationResult `json:"result"`
}

type objectVerifyOutput struct {
	OK      bool                       `json:"ok"`
	TraceID string                     `json:"trace_id,omitempty"`
	Result  signing.ObjectVerification `json:"result"`
}

type specValidateOutput struct {
	OK      bool           `json:"ok"`
	TraceID string         `json:"trace_id,omitempty"`
	Result  spec.Result    `json:"result"`
	Spec    *spec.Document `json:"spec,omitempty"`
}

type compileOutput struct {
	OK      bool      `json:"ok"`
	TraceID string    `json:"trace_id,omitempty"`
	Out     string    `json:"out,omitempty"`
	Graph   *ir.Graph `json:"graph,omitempty"`
}

type bootstrapAWSOutput struct {
	OK        bool               `json:"ok"`
	TraceID   string             `json:"trace_id,omitempty"`
	DryRun    bool               `json:"dry_run"`
	Terraform string             `json:"terraform,omitempty"`
	Plan      *bootstrap.AWSPlan `json:"plan"`
}

type policyExplainOutput struct {
	OK           bool                         `json:"ok"`
	TraceID      string                       `json:"trace_id,omitempty"`
	Role         securitypolicy.Role          `json:"role"`
	Bucket       string                       `json:"bucket"`
	Policy       securitypolicy.Document      `json:"policy"`
	Explanations []securitypolicy.Explanation `json:"explanations"`
	Findings     []securitypolicy.Finding     `json:"findings,omitempty"`
}

type eventsListOutput struct {
	OK      bool           `json:"ok"`
	TraceID string         `json:"trace_id,omitempty"`
	Scope   events.Scope   `json:"scope"`
	Events  []events.Event `json:"events"`
}

type eventWatchOutput struct {
	OK             bool          `json:"ok"`
	TraceID        string        `json:"trace_id,omitempty"`
	Event          *schema.Event `json:"event,omitempty"`
	ResyncRequired bool          `json:"resync_required,omitempty"`
	LastEventID    string        `json:"last_event_id,omitempty"`
	Code           string        `json:"code,omitempty"`
	Summary        string        `json:"summary,omitempty"`
}

var (
	eventsWatchContext      = nilContext
	eventsWatchPollInterval = 2 * time.Second
)

type specErrorOutput struct {
	OK                 bool                `json:"ok"`
	Code               string              `json:"code"`
	Summary            string              `json:"summary"`
	TraceID            string              `json:"trace_id,omitempty"`
	Fields             []spec.Diagnostic   `json:"fields,omitempty"`
	RecommendedActions []recommendedAction `json:"recommended_actions,omitempty"`
}

func runConfig(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printConfigUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "show":
		return runConfigShow(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printConfigUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s config: unknown command %q\n", binary, args[0])
		printConfigUsage(stderr, binary)
		return ExitUserError
	}
}

func runConfigShow(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" config show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", root.Format, "output format: human or json")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	userDataPath := fs.String("user-data", "", "path to runner cloud-init/user-data JSON")

	fs.String("env", root.Env, "Skiff environment name")
	fs.String("provider", root.Provider, "cloud provider name")
	fs.String("region", root.Region, "cloud provider region")
	fs.String("state-bucket", root.State, "object-state bucket URI")
	fs.String("kms-key", "", "KMS key ID or alias")
	fs.String("auth-mode", "", "auth mode")
	fs.String("log-level", "", "log level")
	fs.String("mode", string(root.Mode), "config mode: api, direct, skiffd, or runner")
	fs.String("api-url", root.APIURL, "skiffd API URL")
	fs.String("service", "", "runner service name")
	fs.String("control-key", "", "runner service control object key")
	fs.String("state", root.State, "alias for --state-bucket")
	direct := fs.Bool("direct", root.directSet, "use direct object-state mode")
	apiMode := fs.Bool("api", root.apiSet, "use skiffd API mode")

	if err := fs.Parse(args); err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeConfigError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), nil, stdout, stderr)
	}
	_ = noColor
	_ = yes

	overrides := root.configOverrides()
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
	if *direct && *apiMode {
		return writeConfigError(binary, *format, *traceID, errors.New("--api and --direct cannot both be set"), nil, stdout, stderr)
	}
	if *direct {
		overrides[config.FieldMode] = string(config.ModeDirect)
	}
	if *apiMode {
		overrides[config.FieldMode] = string(config.ModeAPI)
	}

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
			Code:    string(skifferrors.ValidationFailed),
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

func runBootstrap(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printBootstrapUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "aws":
		return runBootstrapAWS(binary, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printBootstrapUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s bootstrap: unknown command %q\n", binary, args[0])
		printBootstrapUsage(stderr, binary)
		return ExitUserError
	}
}

func runBootstrapAWS(binary string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" bootstrap aws", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "human", "output format: human or json")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "apply bootstrap changes without prompting")
	dryRun := fs.Bool("dry-run", false, "print the bootstrap plan without mutating cloud resources")
	emitTerraform := fs.String("emit", "", "emit generated artifacts; supported value: terraform")
	env := fs.String("env", "", "Skiff environment name")
	region := fs.String("region", "", "AWS region")
	bucket := fs.String("bucket", "", "S3 state bucket name")
	stateBucket := fs.String("state-bucket", "", "S3 state bucket URI or name")
	kmsAlias := fs.String("kms-alias", "", "KMS alias for state bucket encryption")
	deployerRole := fs.String("deployer-role", "", "IAM role name for deployers")
	runnerRole := fs.String("runner-role", "", "IAM role name for runners")
	skiffdRole := fs.String("skiffd-role", "", "IAM role name for skiffd")

	if err := fs.Parse(args); err != nil {
		return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeBootstrapError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	_ = noColor

	if *bucket == "" && *stateBucket != "" {
		*bucket = bucketNameFromStateBucket(*stateBucket)
	}
	plan, err := bootstrap.PlanAWS(bootstrap.AWSOptions{
		Env:          *env,
		Region:       *region,
		StateBucket:  *bucket,
		KMSAlias:     *kmsAlias,
		DeployerRole: *deployerRole,
		RunnerRole:   *runnerRole,
		SkiffdRole:   *skiffdRole,
	})
	if err != nil {
		return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
	}

	var terraform string
	if *emitTerraform != "" {
		if *emitTerraform != "terraform" {
			return writeBootstrapError(binary, *format, *traceID, errors.New(`unsupported emit target; expected "terraform"`), stdout, stderr)
		}
		terraform, err = bootstrap.TerraformAWS(plan)
		if err != nil {
			return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
		}
	}
	if !*dryRun && *emitTerraform == "" {
		if !*yes {
			return writeBootstrapError(binary, *format, *traceID, errors.New("bootstrap apply requires --yes; use --dry-run to inspect the plan"), stdout, stderr)
		}
		return writeBootstrapError(binary, *format, *traceID, errors.New("bootstrap apply requires an AWS bootstrap client; use --dry-run or --emit terraform in this build"), stdout, stderr)
	}

	switch *format {
	case "human", "text":
		if terraform != "" {
			fmt.Fprint(stdout, terraform)
			return ExitSuccess
		}
		printBootstrapAWSPlan(stdout, plan)
		return ExitSuccess
	case "json":
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(bootstrapAWSOutput{
			OK:        true,
			TraceID:   *traceID,
			DryRun:    *dryRun,
			Terraform: terraform,
			Plan:      plan,
		}); err != nil {
			fmt.Fprintf(stderr, "%s bootstrap aws: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	default:
		return writeBootstrapError(binary, *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func printBootstrapAWSPlan(w io.Writer, plan *bootstrap.AWSPlan) {
	fmt.Fprintf(w, "AWS bootstrap plan for %s in %s\n", plan.Env, plan.Region)
	fmt.Fprintf(w, "state_bucket: %s\n", plan.StateBucketURI)
	fmt.Fprintf(w, "kms_alias: %s\n", plan.KMSAlias)
	fmt.Fprintf(w, "root_object: %s\n", plan.RootObjectKey)
	for _, resource := range plan.Resources {
		fmt.Fprintf(w, "- %s %s: %s\n", resource.Kind, resource.Name, resource.Summary)
	}
}

func writeBootstrapError(binary, format, traceID string, err error, stdout, stderr io.Writer) int {
	if format == "json" {
		enc := json.NewEncoder(stdout)
		if encErr := enc.Encode(commandErrorOutput{
			OK:      false,
			Code:    string(skifferrors.ValidationFailed),
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{
					ID:       "dry_run",
					Command:  binary + " bootstrap aws --env <env> --region <region> --bucket <bucket> --dry-run --format json",
					Mutating: false,
				},
			},
		}); encErr != nil {
			fmt.Fprintf(stderr, "%s bootstrap aws: %v\n", binary, encErr)
		}
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s bootstrap aws: %v\n", binary, err)
	return ExitUserError
}

func bucketNameFromStateBucket(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "s3://") {
		withoutScheme := strings.TrimPrefix(value, "s3://")
		if before, _, ok := strings.Cut(withoutScheme, "/"); ok {
			return before
		}
		return withoutScheme
	}
	return value
}

func runPolicy(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printPolicyUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "explain":
		return runPolicyExplain(binary, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printPolicyUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s policy: unknown command %q\n", binary, args[0])
		printPolicyUsage(stderr, binary)
		return ExitUserError
	}
}

func runPolicyExplain(binary string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" policy explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "human", "output format: human or json")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	role := fs.String("role", "", "policy role: state-bucket, runner, deployer, skiffd, or break-glass")
	bucket := fs.String("bucket", "", "S3 state bucket name")
	stateBucket := fs.String("state-bucket", "", "S3 state bucket URI or name")
	kmsAlias := fs.String("kms-alias", "alias/skiff-state", "KMS alias used by IAM role policies")

	if err := fs.Parse(args); err != nil {
		return writePolicyError(binary, *format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writePolicyError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	_ = noColor
	_ = yes

	if *bucket == "" && *stateBucket != "" {
		*bucket = bucketNameFromStateBucket(*stateBucket)
	}
	if strings.TrimSpace(*bucket) == "" {
		return writePolicyError(binary, *format, *traceID, errors.New("policy explain requires --bucket or --state-bucket"), stdout, stderr)
	}
	if strings.TrimSpace(*role) == "" {
		return writePolicyError(binary, *format, *traceID, errors.New("policy explain requires --role"), stdout, stderr)
	}

	policyRole := securitypolicy.Role(*role)
	document, err := securitypolicy.PolicyForRole(policyRole, *bucket, *kmsAlias)
	if err != nil {
		return writePolicyError(binary, *format, *traceID, err, stdout, stderr)
	}
	explanations := securitypolicy.Explain(policyRole, document)
	findings := securitypolicy.Lint(document, securitypolicy.LintOptions{Role: policyRole})

	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "Policy %s for bucket %s\n", policyRole, *bucket)
		for _, explanation := range explanations {
			fmt.Fprintf(stdout, "- %s: %s\n", explanation.Sid, explanation.Reason)
			fmt.Fprintf(stdout, "  safety: %s\n", explanation.Safety)
			fmt.Fprintf(stdout, "  actions: %s\n", strings.Join(explanation.Actions, ", "))
		}
		if len(findings) > 0 {
			fmt.Fprintln(stdout, "findings:")
			for _, finding := range findings {
				fmt.Fprintf(stdout, "- %s: %s\n", finding.Code, finding.Summary)
			}
		}
		return ExitSuccess
	case "json":
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(policyExplainOutput{
			OK:           true,
			TraceID:      *traceID,
			Role:         policyRole,
			Bucket:       *bucket,
			Policy:       document,
			Explanations: explanations,
			Findings:     findings,
		}); err != nil {
			fmt.Fprintf(stderr, "%s policy explain: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	default:
		return writePolicyError(binary, *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func writePolicyError(binary, format, traceID string, err error, stdout, stderr io.Writer) int {
	if format == "json" {
		enc := json.NewEncoder(stdout)
		if encErr := enc.Encode(commandErrorOutput{
			OK:      false,
			Code:    string(skifferrors.ValidationFailed),
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{
					ID:       "explain_runner_policy",
					Command:  binary + " policy explain --role runner --bucket <bucket> --format json",
					Mutating: false,
				},
			},
		}); encErr != nil {
			fmt.Fprintf(stderr, "%s policy explain: %v\n", binary, encErr)
		}
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s policy explain: %v\n", binary, err)
	return ExitUserError
}

func runEvents(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		printEventsUsage(stdout, binary)
		return ExitSuccess
	}

	fs := flag.NewFlagSet(binary+" events", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	flags := addClientFlags(fs, root)
	stateDir := fs.String("state-dir", "", "local object-state directory mirror")
	scopeKind := fs.String("scope", "recent", "event scope: recent, service, operation, or saga")
	service := fs.String("service", "", "service name")
	operation := fs.String("operation", "", "operation ID")
	saga := fs.String("saga", "", "saga ID")
	limit := fs.Int("limit", 0, "maximum events to list")
	fresh := fs.Bool("fresh", false, "bypass cached API views where supported")
	watch := fs.Bool("watch", false, "watch event stream until interrupted")
	afterID := fs.String("after", "", "resume after event ID")

	if err := fs.Parse(args); err != nil {
		return writeEventsError(binary, *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeEventsError(binary, *flags.format, *flags.traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	_ = flags.noColor
	_ = flags.yes

	if *stateDir != "" {
		scope := events.Scope{Kind: events.ScopeKind(*scopeKind), Service: *service, Operation: *operation, Saga: *saga}
		listed, err := readEventsFromStateDir(*stateDir, scope, *limit)
		if err != nil {
			return writeEventsError(binary, *flags.format, *flags.traceID, err, stdout, stderr)
		}
		switch *flags.format {
		case "human", "text":
			for _, event := range listed {
				fmt.Fprintf(stdout, "%s %s %s %s\n", event.Time, event.ID, event.Type, event.Summary)
			}
			return ExitSuccess
		case "json":
			enc := json.NewEncoder(stdout)
			if err := enc.Encode(eventsListOutput{OK: true, TraceID: *flags.traceID, Scope: scope, Events: listed}); err != nil {
				fmt.Fprintf(stderr, "%s events: %v\n", binary, err)
				return ExitInternalError
			}
			return ExitSuccess
		default:
			return writeEventsError(binary, *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
		}
	}

	loaded, err := flags.load(binary, root, fs)
	if err != nil {
		return writeConfigError(binary, *flags.format, *flags.traceID, err, loaded.Redacted().Sources, stdout, stderr)
	}
	skiffClient, err := client.New(loaded.Config, client.Options{})
	if err != nil {
		return writeClientError(binary, "events", *flags.format, *flags.traceID, err, stdout, stderr)
	}
	if *watch {
		return runEventsWatch(eventsWatchContext(), binary, skiffClient, client.EventWatchOptions{
			EventOptions: client.EventOptions{
				Scope:     *scopeKind,
				Service:   *service,
				Operation: *operation,
				Saga:      *saga,
				Limit:     *limit,
				Fresh:     *fresh,
				TraceID:   *flags.traceID,
			},
			AfterID:      *afterID,
			PollInterval: eventsWatchPollInterval,
		}, *flags.format, *flags.traceID, stdout, stderr)
	}
	listed, err := skiffClient.Events(nilContext(), client.EventOptions{
		Scope:     *scopeKind,
		Service:   *service,
		Operation: *operation,
		Saga:      *saga,
		Limit:     *limit,
		Fresh:     *fresh,
		TraceID:   *flags.traceID,
	})
	if err != nil {
		return writeClientError(binary, "events", *flags.format, *flags.traceID, err, stdout, stderr)
	}

	switch *flags.format {
	case "human", "text":
		for _, event := range listed.Events {
			fmt.Fprintf(stdout, "%s %s %s %s\n", event.Time, event.ID, event.Type, event.Summary)
		}
		return ExitSuccess
	case "json":
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(clientEventsOutput{OK: true, TraceID: *flags.traceID, Result: *listed}); err != nil {
			fmt.Fprintf(stderr, "%s events: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeEventsError(binary, *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
}

func runEventsWatch(ctx context.Context, binary string, skiffClient client.Interface, opts client.EventWatchOptions, format, traceID string, stdout, stderr io.Writer) int {
	watcher, ok := skiffClient.(client.EventWatcher)
	if !ok {
		return writeClientCommandError(binary, "events", format, traceID, errors.New("client mode does not support event watch"), stdout, stderr)
	}
	stream, err := watcher.WatchEvents(ctx, opts)
	if err != nil {
		return writeClientError(binary, "events", format, traceID, err, stdout, stderr)
	}
	for delivery := range stream {
		switch format {
		case "human", "text":
			if delivery.ResyncRequired {
				fmt.Fprintf(stdout, "resync required after %s\n", delivery.LastEventID)
				continue
			}
			event := delivery.Event
			fmt.Fprintf(stdout, "%s %s %s %s\n", event.Time, event.ID, event.Type, event.Summary)
		case "json":
			out := eventWatchOutput{OK: true, TraceID: traceID}
			if delivery.ResyncRequired {
				out.OK = false
				out.ResyncRequired = true
				out.LastEventID = delivery.LastEventID
				out.Code = "RESYNC_REQUIRED"
				out.Summary = "event stream subscriber fell behind; reconnect with --after " + delivery.LastEventID
			} else {
				event := delivery.Event
				out.Event = &event
				out.LastEventID = delivery.LastEventID
			}
			if err := json.NewEncoder(stdout).Encode(out); err != nil {
				fmt.Fprintf(stderr, "%s events watch: %v\n", binary, err)
				return ExitInternalError
			}
		default:
			return writeEventsError(binary, format, traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
		}
	}
	return ExitSuccess
}

func readEventsFromStateDir(root string, scope events.Scope, limit int) ([]events.Event, error) {
	prefix, err := eventPrefix(scope)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, filepath.FromSlash(prefix))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(files)
	if limit > 0 && limit < len(files) {
		files = files[:limit]
	}
	out := make([]events.Event, 0, len(files))
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var event events.Event
		if err := canonical.UnmarshalStrict(body, &event); err != nil {
			return nil, fmt.Errorf("decode event %q: %w", file, err)
		}
		out = append(out, event)
	}
	return out, nil
}

func eventPrefix(scope events.Scope) (string, error) {
	switch scope.Kind {
	case events.ScopeService:
		return paths.ServiceEventsPrefix(scope.Service)
	case events.ScopeOperation:
		return paths.OperationEventsPrefix(scope.Service, scope.Operation)
	case events.ScopeSaga:
		return paths.SagaEventsPrefix(scope.Saga)
	default:
		return "", fmt.Errorf("unknown events scope %q", scope.Kind)
	}
}

func writeEventsError(binary, format, traceID string, err error, stdout, stderr io.Writer) int {
	if format == "json" {
		enc := json.NewEncoder(stdout)
		if encErr := enc.Encode(commandErrorOutput{
			OK:      false,
			Code:    string(skifferrors.ValidationFailed),
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{
					ID:       "list_service_events",
					Command:  binary + " events --scope service --service <service> --state-dir <dir> --format json",
					Mutating: false,
				},
			},
		}); encErr != nil {
			fmt.Fprintf(stderr, "%s events: %v\n", binary, encErr)
		}
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s events: %v\n", binary, err)
	return ExitUserError
}

func runCompile(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCompileUsage(stderr, binary)
		return ExitUserError
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printCompileUsage(stdout, binary)
		return ExitSuccess
	}

	fs := flag.NewFlagSet(binary+" compile", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "human", "output format: human or json")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	filePath := fs.String("file", "", "Skiff YAML or JSON spec file")
	outPath := fs.String("out", "", "write canonical IR JSON to path, or - for stdout")
	allowUnknown := fs.Bool("allow-unknown-fields", false, "accept unknown fields for compatibility checks")

	flagArgs, positionals, err := splitCompileArgs(args)
	if err != nil {
		return writeCompileError(binary, "SPEC_COMPILE_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeCompileError(binary, "SPEC_COMPILE_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeCompileError(binary, "SPEC_COMPILE_INVALID", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), nil, stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeCompileError(binary, "SPEC_COMPILE_INVALID", *format, *traceID, errors.New("spec file is required"), nil, stdout, stderr)
	}
	if *format != "human" && *format != "text" && *format != "json" {
		return writeCompileError(binary, "SPEC_COMPILE_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), nil, stdout, stderr)
	}
	_ = noColor
	_ = yes

	doc, err := spec.LoadFile(*filePath, spec.DecodeOptions{AllowUnknownFields: *allowUnknown})
	if err != nil {
		return writeCompileError(binary, "SPEC_DECODE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		var validation spec.ValidationError
		if errors.As(err, &validation) {
			return writeCompileError(binary, "SPEC_INVALID", *format, *traceID, errors.New("spec validation failed"), validation.Diagnostics, stdout, stderr)
		}
		return writeCompileError(binary, "SPEC_COMPILE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}

	if *outPath == "-" {
		body, err := canonical.Marshal(graph)
		if err != nil {
			return writeCompileError(binary, "SPEC_COMPILE_FAILED", *format, *traceID, err, nil, stdout, stderr)
		}
		fmt.Fprintln(stdout, string(body))
		return ExitSuccess
	}
	if *outPath != "" {
		body, err := canonical.Marshal(graph)
		if err != nil {
			return writeCompileError(binary, "SPEC_COMPILE_FAILED", *format, *traceID, err, nil, stdout, stderr)
		}
		if err := os.WriteFile(*outPath, append(body, '\n'), 0o644); err != nil {
			return writeCompileError(binary, "SPEC_COMPILE_FAILED", *format, *traceID, err, nil, stdout, stderr)
		}
	}

	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "compiled Service %s/%s to IR", graph.Env, graph.Service)
		if *outPath != "" {
			fmt.Fprintf(stdout, " at %s", *outPath)
		}
		fmt.Fprintln(stdout)
		return ExitSuccess
	case "json":
		out := compileOutput{OK: true, TraceID: *traceID, Out: *outPath}
		if *outPath == "" {
			out.Graph = graph
		}
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "%s compile: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	}
	return writeCompileError(binary, "SPEC_COMPILE_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), nil, stdout, stderr)
}

func writeCompileError(binary, code, format, traceID string, err error, fields []spec.Diagnostic, stdout, stderr io.Writer) int {
	if format == "json" {
		enc := json.NewEncoder(stdout)
		if encErr := enc.Encode(specErrorOutput{
			OK:      false,
			Code:    string(skifferrors.FromSpecCode(code)),
			Summary: err.Error(),
			TraceID: traceID,
			Fields:  fields,
			RecommendedActions: []recommendedAction{
				{
					ID:       "inspect_spec",
					Command:  binary + " validate <skiff.yaml> --format json --show-defaulted",
					Mutating: false,
				},
			},
		}); encErr != nil {
			fmt.Fprintf(stderr, "%s compile: %v\n", binary, encErr)
		}
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s compile: %v\n", binary, err)
	for _, field := range fields {
		fmt.Fprintf(stderr, "- %s %s: %s\n", field.Path, field.Code, field.Message)
	}
	return ExitUserError
}

func runValidate(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printValidateUsage(stderr, binary)
		return ExitUserError
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printValidateUsage(stdout, binary)
		return ExitSuccess
	}

	fs := flag.NewFlagSet(binary+" validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "human", "output format: human, json, or yaml")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	filePath := fs.String("file", "", "Skiff YAML or JSON spec file")
	allowUnknown := fs.Bool("allow-unknown-fields", false, "accept unknown fields for compatibility checks")
	showDefaulted := fs.Bool("show-defaulted", false, "include the defaulted spec in output")

	flagArgs, positionals, err := splitValidateArgs(args)
	if err != nil {
		return writeSpecError(binary, "SPEC_VALIDATE_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeSpecError(binary, "SPEC_VALIDATE_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeSpecError(binary, "SPEC_VALIDATE_INVALID", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), nil, stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeSpecError(binary, "SPEC_VALIDATE_INVALID", *format, *traceID, errors.New("spec file is required"), nil, stdout, stderr)
	}
	_ = noColor
	_ = yes

	doc, err := spec.LoadFile(*filePath, spec.DecodeOptions{AllowUnknownFields: *allowUnknown})
	if err != nil {
		return writeSpecError(binary, "SPEC_DECODE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	result := spec.Validate(*doc)
	if !result.OK {
		return writeSpecError(binary, "SPEC_INVALID", *format, *traceID, errors.New("spec validation failed"), result.Diagnostics, stdout, stderr)
	}

	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s %s/%s valid\n", result.Kind, result.Env, result.Name)
		if *showDefaulted {
			body, err := spec.MarshalYAML(*doc)
			if err != nil {
				return writeSpecError(binary, "SPEC_RENDER_FAILED", *format, *traceID, err, nil, stdout, stderr)
			}
			fmt.Fprint(stdout, string(body))
		}
		return ExitSuccess
	case "json":
		out := specValidateOutput{OK: true, TraceID: *traceID, Result: result}
		if *showDefaulted {
			out.Spec = doc
		}
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "%s validate: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	case "yaml":
		body, err := spec.MarshalYAML(*doc)
		if err != nil {
			return writeSpecError(binary, "SPEC_RENDER_FAILED", *format, *traceID, err, nil, stdout, stderr)
		}
		fmt.Fprint(stdout, string(body))
		return ExitSuccess
	default:
		return writeSpecError(binary, "SPEC_VALIDATE_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "yaml"`), nil, stdout, stderr)
	}
}

func writeSpecError(binary, code, format, traceID string, err error, fields []spec.Diagnostic, stdout, stderr io.Writer) int {
	if format == "json" {
		enc := json.NewEncoder(stdout)
		if encErr := enc.Encode(specErrorOutput{
			OK:      false,
			Code:    string(skifferrors.FromSpecCode(code)),
			Summary: err.Error(),
			TraceID: traceID,
			Fields:  fields,
			RecommendedActions: []recommendedAction{
				{
					ID:       "inspect_spec",
					Command:  binary + " validate <skiff.yaml> --format json --show-defaulted",
					Mutating: false,
				},
			},
		}); encErr != nil {
			fmt.Fprintf(stderr, "%s validate: %v\n", binary, encErr)
		}
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s validate: %v\n", binary, err)
	for _, field := range fields {
		fmt.Fprintf(stderr, "- %s %s: %s\n", field.Path, field.Code, field.Message)
	}
	return ExitUserError
}

func printConfigUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s config <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  show       Print effective configuration")
}

func printBootstrapUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s bootstrap <provider> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  aws        Plan or emit AWS state substrate bootstrap")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "AWS flags:")
	fmt.Fprintln(w, "  --env <env>")
	fmt.Fprintln(w, "  --region <region>")
	fmt.Fprintln(w, "  --bucket <bucket>")
	fmt.Fprintln(w, "  --dry-run")
	fmt.Fprintln(w, "  --emit terraform")
	fmt.Fprintln(w, "  --format human|json")
}

func printPolicyUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s policy <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  explain    Explain a generated state bucket or IAM policy")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Explain flags:")
	fmt.Fprintln(w, "  --role state-bucket|runner|deployer|skiffd|break-glass")
	fmt.Fprintln(w, "  --bucket <bucket>")
	fmt.Fprintln(w, "  --kms-alias <alias/name>")
	fmt.Fprintln(w, "  --format human|json")
}

func printEventsUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s events [flags]\n\n", binary)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --state-dir <dir>")
	fmt.Fprintln(w, "  --scope service|operation|saga")
	fmt.Fprintln(w, "  --service <service>")
	fmt.Fprintln(w, "  --operation <operation>")
	fmt.Fprintln(w, "  --saga <saga>")
	fmt.Fprintln(w, "  --limit <n>")
	fmt.Fprintln(w, "  --format human|json")
}

func printValidateUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s validate <skiff.yaml> [flags]\n\n", binary)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --format human|json|yaml")
	fmt.Fprintln(w, "  --show-defaulted")
	fmt.Fprintln(w, "  --allow-unknown-fields")
	fmt.Fprintln(w, "  --trace-id <id>")
}

func printCompileUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s compile <skiff.yaml> [flags]\n\n", binary)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --format human|json")
	fmt.Fprintln(w, "  --out <path>")
	fmt.Fprintln(w, "  --allow-unknown-fields")
	fmt.Fprintln(w, "  --trace-id <id>")
}

func runRelease(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printReleaseUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "candidate":
		return runReleaseCandidate(binary, args[1:], root, stdout, stderr)
	case "verify":
		return runReleaseVerify(binary, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printReleaseUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s release: unknown command %q\n", binary, args[0])
		printReleaseUsage(stderr, binary)
		return ExitUserError
	}
}

func runReleaseVerify(binary string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" release verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "human", "output format: human or json")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	filePath := fs.String("file", "", "release manifest JSON file")
	runtimePath := fs.String("runtime-manifest", "", "runtime manifest JSON file")
	service := fs.String("service", "", "expected service name")
	env := fs.String("env", "", "expected environment name")
	nowValue := fs.String("now", "", "RFC3339 verification time")
	var publicKeys publicKeyFlags
	fs.Var(&publicKeys, "public-key", "trusted public key as key_id=base64-ed25519-public-key; may be repeated")

	flagArgs, positionals, err := splitVerifyArgs(args)
	if err != nil {
		return writeVerifyError(binary, "RELEASE_VERIFY_INVALID", *format, *traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeVerifyError(binary, "RELEASE_VERIFY_INVALID", *format, *traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeVerifyError(binary, "RELEASE_VERIFY_INVALID", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeVerifyError(binary, "RELEASE_VERIFY_INVALID", *format, *traceID, errors.New("release manifest file is required"), stdout, stderr)
	}
	_ = noColor
	_ = yes

	manifest, err := readReleaseManifest(*filePath)
	if err != nil {
		return writeVerifyError(binary, "RELEASE_VERIFY_INVALID", *format, *traceID, err, stdout, stderr)
	}
	var runtimeManifest *schema.RuntimeManifest
	if *runtimePath != "" {
		runtimeManifest, err = readRuntimeManifest(*runtimePath)
		if err != nil {
			return writeVerifyError(binary, "RELEASE_VERIFY_INVALID", *format, *traceID, err, stdout, stderr)
		}
	}
	verifier, err := publicKeys.verifier()
	if err != nil {
		return writeVerifyError(binary, "RELEASE_VERIFY_INVALID", *format, *traceID, err, stdout, stderr)
	}
	now, err := parseVerifyTime(*nowValue)
	if err != nil {
		return writeVerifyError(binary, "RELEASE_VERIFY_INVALID", *format, *traceID, err, stdout, stderr)
	}
	result := release.VerifyManifest(nilContext(), manifest, release.VerifyOptions{
		Service:         *service,
		Env:             *env,
		RuntimeManifest: runtimeManifest,
		Verifier:        verifier,
		Now:             now,
	})
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(releaseVerifyOutput{OK: result.OK, TraceID: *traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s release verify: %v\n", binary, err)
			return ExitUserError
		}
		if result.OK {
			return ExitSuccess
		}
		return ExitUserError
	}
	if *format != "human" && *format != "text" {
		return writeVerifyError(binary, "RELEASE_VERIFY_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
	printReleaseVerification(stdout, result)
	if result.OK {
		return ExitSuccess
	}
	return ExitUserError
}

func runObject(binary string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printObjectUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "verify":
		return runObjectVerify(binary, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printObjectUsage(stdout, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(stderr, "%s object: unknown command %q\n", binary, args[0])
		printObjectUsage(stderr, binary)
		return ExitUserError
	}
}

func runObjectVerify(binary string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" object verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "human", "output format: human or json")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	filePath := fs.String("file", "", "signed object JSON file")
	var publicKeys publicKeyFlags
	fs.Var(&publicKeys, "public-key", "trusted public key as key_id=base64-ed25519-public-key; may be repeated")

	flagArgs, positionals, err := splitVerifyArgs(args)
	if err != nil {
		return writeVerifyError(binary, "OBJECT_VERIFY_INVALID", *format, *traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writeVerifyError(binary, "OBJECT_VERIFY_INVALID", *format, *traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeVerifyError(binary, "OBJECT_VERIFY_INVALID", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeVerifyError(binary, "OBJECT_VERIFY_INVALID", *format, *traceID, errors.New("signed object file is required"), stdout, stderr)
	}
	_ = noColor
	_ = yes

	body, err := os.ReadFile(*filePath)
	if err != nil {
		return writeVerifyError(binary, "OBJECT_VERIFY_INVALID", *format, *traceID, err, stdout, stderr)
	}
	verifier, err := publicKeys.verifier()
	if err != nil {
		return writeVerifyError(binary, "OBJECT_VERIFY_INVALID", *format, *traceID, err, stdout, stderr)
	}
	result := signing.VerifySignedJSON(nilContext(), body, verifier, schema.Version)
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(objectVerifyOutput{OK: result.OK, TraceID: *traceID, Result: result}); err != nil {
			fmt.Fprintf(stderr, "%s object verify: %v\n", binary, err)
			return ExitUserError
		}
		if result.OK {
			return ExitSuccess
		}
		return ExitUserError
	}
	if *format != "human" && *format != "text" {
		return writeVerifyError(binary, "OBJECT_VERIFY_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human" or "json"`), stdout, stderr)
	}
	printObjectVerification(stdout, result)
	if result.OK {
		return ExitSuccess
	}
	return ExitUserError
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
	candidate := fs.String("candidate", "", "release candidate ID")
	operation := fs.String("operation", "", "operation ID")
	saga := fs.String("saga", "", "saga ID")
	event := fs.String("event", "", "event ID")
	artifact := fs.String("artifact", "", "saga artifact path")
	step := fs.String("step", "", "saga step ID")
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
		candidate:    *candidate,
		operation:    *operation,
		saga:         *saga,
		event:        *event,
		artifact:     *artifact,
		step:         *step,
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
				candidate:    *candidate,
				operation:    *operation,
				saga:         *saga,
				event:        *event,
				artifact:     *artifact,
				step:         *step,
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
	candidate    string
	operation    string
	saga         string
	event        string
	artifact     string
	step         string
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
	case "env":
		return paths.EnvironmentRoot(in.name)
	case "service":
		return paths.ServiceControl(in.service)
	case "release":
		switch defaultString(in.doc, "release") {
		case "release":
			return paths.ReleaseManifest(in.service, in.release)
		case "runtime-manifest":
			return paths.RuntimeManifest(in.service, in.release)
		case "candidate":
			return paths.ReleaseCandidate(in.service, in.candidate)
		default:
			return "", fmt.Errorf("release --doc must be release, runtime-manifest, or candidate")
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
		case "artifact":
			return paths.SagaArtifact(in.saga, in.artifact)
		case "result":
			return paths.SagaStepResult(in.saga, in.step)
		default:
			return "", fmt.Errorf("saga --doc must be intent, graph, control, event, artifact, or result")
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
		"candidate":     in.candidate,
		"operation":     in.operation,
		"saga":          in.saga,
		"event":         in.event,
		"artifact":      in.artifact,
		"step":          in.step,
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

type publicKeyFlags struct {
	keys map[string]ed25519.PublicKey
}

func splitVerifyArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"env":              true,
		"file":             true,
		"format":           true,
		"now":              true,
		"public-key":       true,
		"runtime-manifest": true,
		"service":          true,
		"trace-id":         true,
	}
	return splitArgs(args, valueFlags)
}

func splitValidateArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"file":     true,
		"format":   true,
		"trace-id": true,
	}
	return splitArgs(args, valueFlags)
}

func splitCompileArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"file":     true,
		"format":   true,
		"out":      true,
		"trace-id": true,
	}
	return splitArgs(args, valueFlags)
}

func splitArgs(args []string, valueFlags map[string]bool) ([]string, []string, error) {
	var flagArgs []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		name := strings.TrimLeft(arg, "-")
		if name == "" {
			continue
		}
		if before, _, ok := strings.Cut(name, "="); ok {
			name = before
		}
		if valueFlags[name] && !strings.Contains(arg, "=") {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("missing value for %s", arg)
			}
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return flagArgs, positionals, nil
}

func (f *publicKeyFlags) String() string {
	if f == nil || len(f.keys) == 0 {
		return ""
	}
	return fmt.Sprintf("%d public key(s)", len(f.keys))
}

func (f *publicKeyFlags) Set(value string) error {
	keyID, rawValue, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(keyID) == "" || strings.TrimSpace(rawValue) == "" {
		return fmt.Errorf("public key must be key_id=base64-ed25519-public-key")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawValue))
	if err != nil {
		return fmt.Errorf("public key for %q must be base64: %w", keyID, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return fmt.Errorf("public key for %q must be %d bytes", keyID, ed25519.PublicKeySize)
	}
	if f.keys == nil {
		f.keys = make(map[string]ed25519.PublicKey)
	}
	f.keys[strings.TrimSpace(keyID)] = append(ed25519.PublicKey(nil), raw...)
	return nil
}

func (f publicKeyFlags) verifier() (*signing.LocalVerifier, error) {
	if len(f.keys) == 0 {
		return nil, fmt.Errorf("at least one --public-key is required")
	}
	return signing.NewLocalVerifier(f.keys)
}

func readReleaseManifest(path string) (schema.ReleaseManifest, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return schema.ReleaseManifest{}, err
	}
	var manifest schema.ReleaseManifest
	if err := canonical.UnmarshalStrict(body, &manifest); err != nil {
		return schema.ReleaseManifest{}, fmt.Errorf("read release manifest: %w", err)
	}
	return manifest, nil
}

func readRuntimeManifest(path string) (*schema.RuntimeManifest, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest schema.RuntimeManifest
	if err := canonical.UnmarshalStrict(body, &manifest); err != nil {
		return nil, fmt.Errorf("read runtime manifest: %w", err)
	}
	return &manifest, nil
}

func parseVerifyTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("--now must be RFC3339: %w", err)
	}
	return parsed, nil
}

func printReleaseVerification(w io.Writer, result release.VerificationResult) {
	if result.OK {
		fmt.Fprintf(w, "release %s verified\n", result.ReleaseID)
		return
	}
	fmt.Fprintf(w, "release %s verification failed\n", result.ReleaseID)
	for _, finding := range result.Findings {
		fmt.Fprintf(w, "- %s: %s\n", finding.Code, finding.Summary)
	}
}

func printObjectVerification(w io.Writer, result signing.ObjectVerification) {
	if result.OK {
		fmt.Fprintf(w, "object verified: %s\n", result.Digest)
		return
	}
	fmt.Fprintln(w, "object verification failed")
	for _, finding := range result.Findings {
		fmt.Fprintf(w, "- %s: %s\n", finding.Code, finding.Summary)
	}
}

func writeVerifyError(binary, code, format, traceID string, err error, stdout, stderr io.Writer) int {
	if format == "json" {
		enc := json.NewEncoder(stdout)
		if encErr := enc.Encode(verifyErrorOutput{
			OK:      false,
			Code:    string(skifferrors.FromVerifyCode(code)),
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{
					ID:       "inspect_verify_usage",
					Command:  binary + " release verify <release.json> --public-key <key-id=base64> --format json",
					Mutating: false,
				},
			},
		}); encErr != nil {
			fmt.Fprintf(stderr, "%s verify: %v\n", binary, encErr)
		}
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s verify: %v\n", binary, err)
	return ExitUserError
}

func printReleaseUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s release <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  candidate  Create or inspect release candidate evidence")
	fmt.Fprintln(w, "  verify     Verify a signed release manifest")
}

func printObjectUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s object <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  verify     Verify a signed immutable object")
}

func nilContext() context.Context {
	return context.Background()
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
			Code:    string(skifferrors.ValidationFailed),
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
	fmt.Fprintln(w, "  saga                --saga <id> [--doc intent|graph|control|event|artifact|result] [--event <id>] [--artifact <path>] [--step <id>]")
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
