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
	awsprovider "github.com/s1liconcow/skiff/internal/provider/aws"
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

var newAWSBootstrapClient = func(ctx context.Context, region string) (bootstrap.AWSBootstrapClient, error) {
	return awsprovider.NewSDKBootstrapClient(ctx, awsprovider.Config{Region: region})
}

var newAWSReleaseSignerStore = func(ctx context.Context, region string) (signing.ReleaseSignerStore, error) {
	return awsprovider.NewKMSReleaseSignerStore(ctx, awsprovider.Config{Region: region})
}

var releaseSignerStore signing.ReleaseSignerStore = signing.DefaultReleaseSignerStore()

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
	root.Args, root.Format, stdout = prepareJSONPrettyOutput(root.Args, root.Format, root.NoColor, stdout)
	defer flushJSONPrettyOutput(stdout)
	if root.Command == "" {
		printUsage(stderr, binary)
		return ExitUserError
	}

	switch root.Command {
	case "adopt":
		return runAdopt(binary, root.Args, root, stdout, stderr)
	case "authz":
		return runAuthz(binary, root.Args, root, stdout, stderr)
	case "bootstrap":
		if binary == "skiff-runner" {
			return runRunnerBootstrap(binary, root.Args, root, stdout, stderr)
		}
		return runBootstrap(binary, root.Args, stdout, stderr)
	case "compile":
		return runCompile(binary, root.Args, root, stdout, stderr)
	case "config":
		return runConfig(binary, root.Args, root, stdout, stderr)
	case "completion":
		return runCompletion(binary, root.Args, stdout, stderr)
	case "ci":
		return runCI(binary, root.Args, root, stdout, stderr)
	case "contract":
		return runContract(binary, root.Args, root, stdout, stderr)
	case "cost":
		return runCost(binary, root.Args, root, stdout, stderr)
	case "cutover":
		return runCutover(binary, root.Args, root, stdout, stderr)
	case "deploy":
		return runDeploy(binary, root.Args, root, stdout, stderr)
	case "database":
		return runDatabase(binary, root.Args, root, stdout, stderr)
	case "debug":
		return runDebug(binary, root.Args, root, stdout, stderr)
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
	case "import":
		return runImport(binary, root.Args, root, stdout, stderr)
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
	case "pkg":
		return runPkg(binary, root.Args, root, stdout, stderr)
	case "plugin":
		return runPlugin(binary, root.Args, root, stdout, stderr)
	case "policy":
		return runPolicy(binary, root.Args, stdout, stderr)
	case "release":
		return runRelease(binary, root.Args, root, stdout, stderr)
	case "rotate":
		return runRotate(binary, root.Args, root, stdout, stderr)
	case "promote":
		return runPromote(binary, root.Args, root, stdout, stderr)
	case "rollback":
		return runRollback(binary, root.Args, root, stdout, stderr)
	case "rollout":
		return runRollout(binary, root.Args, root, stdout, stderr)
	case "run":
		if binary == "skiff-runner" {
			return runRunnerRun(binary, root.Args, root, stdout, stderr)
		}
		fmt.Fprintf(stderr, "%s: unknown command %q\n", binary, root.Command)
		printUsage(stderr, binary)
		return ExitUserError
	case "saga":
		return runSaga(binary, root.Args, root, stdout, stderr)
	case "solve":
		return runSolve(binary, root.Args, root, stdout, stderr)
	case "state":
		return runState(binary, root.Args, stdout, stderr)
	case "stateful":
		return runStateful(binary, root.Args, root, stdout, stderr)
	case "status":
		return runStatus(binary, root.Args, root, stdout, stderr)
	case "sudo":
		return runSudo(binary, root.Args, root, stdout, stderr)
	case "tui":
		return runTUI(binary, root.Args, root, stdout, stderr)
	case "terraform":
		return runTerraform(binary, root.Args, root, stdout, stderr)
	case "validate":
		return runValidate(binary, root.Args, root, stdout, stderr)
	case "version":
		return runVersion(binary, root.Args, root, stdout, stderr)
	case "help", "-h", "--help":
		return printHelp(stdout, binary, root.Args)
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n", binary, root.Command)
		printUsage(stderr, binary)
		return ExitUserError
	}
}

func parseCommandFlags(fs *flag.FlagSet, args []string, stdout io.Writer) (bool, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printFlagSetUsage(stdout, fs)
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// ParseCommandFlags parses a FlagSet and converts -h/--help into stdout usage and success.
func ParseCommandFlags(fs *flag.FlagSet, args []string, stdout io.Writer) (bool, error) {
	return parseCommandFlags(fs, args, stdout)
}

func printFlagSetUsage(w io.Writer, fs *flag.FlagSet) {
	previous := fs.Output()
	fs.SetOutput(w)
	fs.Usage()
	fs.SetOutput(previous)
}

func isHelpArg(arg string) bool {
	return arg == "help" || arg == "-h" || arg == "--help"
}

func runVersion(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")

	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
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
			Context:     root.Context,
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
		return writeRootError(binary, *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func printHelp(w io.Writer, binary string, args []string) int {
	if len(args) == 0 {
		printUsage(w, binary)
		return ExitSuccess
	}
	switch args[0] {
	case "all":
		printAllUsage(w, binary)
		return ExitSuccess
	case "workflows":
		printWorkflowsHelp(w, binary)
		return ExitSuccess
	case "adoption":
		printAdoptionHelp(w, binary)
		return ExitSuccess
	case "dev":
		printDevHelp(w, binary)
		return ExitSuccess
	case "flags":
		printGlobalFlags(w, binary)
		return ExitSuccess
	case "help", "-h", "--help":
		printUsage(w, binary)
		return ExitSuccess
	default:
		fmt.Fprintf(w, "%s help: unknown topic %q\n\n", binary, args[0])
		printUsage(w, binary)
		return ExitUserError
	}
}

func printUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  init       Create starter specs and recipes")
	fmt.Fprintln(w, "  validate   Check a Skiff spec")
	fmt.Fprintln(w, "  plan       Preview cloud primitives and changes")
	fmt.Fprintln(w, "  explain    Explain provider cloud primitives for a spec")
	fmt.Fprintln(w, "  deploy     Publish and deploy a service release")
	fmt.Fprintln(w, "  release    Manage candidates, promotion, and release verification")
	fmt.Fprintln(w, "  status     Show service status through direct or API mode")
	fmt.Fprintln(w, "  logs       Query service logs through the cloud provider")
	fmt.Fprintln(w, "  metrics    Query service metrics through the cloud provider")
	fmt.Fprintln(w, "  doctor     Diagnose service health and recommend actions")
	fmt.Fprintln(w, "  cost       Explain service shape and capacity recommendations")
	fmt.Fprintln(w, "  rollback   Roll a service back to a stable release")
	fmt.Fprintln(w, "  ops        List, run, inspect, watch, approve, and resume operations")
	fmt.Fprintln(w, "  pkg        Add, verify, explain, and lock packages")
	fmt.Fprintln(w, "  config     Inspect and switch Skiff configuration contexts")
	fmt.Fprintln(w, "  sudo       Assume temporary auditable write credentials")
	fmt.Fprintln(w, "  tui        Open the terminal operations dashboard")
	if binary == "skiffd" {
		fmt.Fprintln(w, "  serve      Start the stateless skiffd API server")
	}
	fmt.Fprintln(w, "  version    Print version, commit, and build date")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "More:")
	fmt.Fprintf(w, "  %s help workflows   Backup, restore, rotation, failover, debug, GC, stateful\n", binary)
	fmt.Fprintf(w, "  %s help adoption    Import, Terraform, CI, and release promotion\n", binary)
	fmt.Fprintf(w, "  %s help dev         Developer and low-level recovery helpers\n", binary)
	fmt.Fprintf(w, "  %s help all         Full command list\n", binary)
	fmt.Fprintf(w, "  %s help flags       Global flags\n", binary)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --config <path> --context <name> --env <env> --provider <provider> --region <region>")
	fmt.Fprintln(w, "  --state <uri> --api --direct --format human|json|json-pretty --no-color --yes --trace-id <id>")
}

func printAllUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  adopt      Record externally managed resources in object state")
	fmt.Fprintln(w, "  authz      Explain authorization and approval decisions")
	fmt.Fprintln(w, "  bootstrap  Bootstrap initial cloud state substrate")
	fmt.Fprintln(w, "  compile    Compile a Skiff spec to provider-neutral IR")
	fmt.Fprintln(w, "  config     Inspect effective configuration")
	fmt.Fprintln(w, "  completion Generate shell completions")
	fmt.Fprintln(w, "  ci         Generate CI/CD templates")
	fmt.Fprintln(w, "  contract   Run CI contract checks")
	fmt.Fprintln(w, "  cost       Explain service shape and capacity recommendations")
	fmt.Fprintln(w, "  cutover    Create a weighted traffic cutover saga")
	fmt.Fprintln(w, "  database   Run managed database backup and restore sagas")
	fmt.Fprintln(w, "  debug      Collect scoped diagnostic bundles")
	fmt.Fprintln(w, "  deploy     Publish and deploy a service release")
	fmt.Fprintln(w, "  doctor     Diagnose service health and recommend actions")
	fmt.Fprintln(w, "  drift      Detect provider drift from Skiff resource records")
	fmt.Fprintln(w, "  events     List local service, operation, or saga events")
	fmt.Fprintln(w, "  explain    Explain provider cloud primitives for a spec")
	fmt.Fprintln(w, "  failover   Run a regional failover saga")
	fmt.Fprintln(w, "  gc         Plan and apply conservative cleanup actions")
	fmt.Fprintln(w, "  init       Generate starter Skiff specs and recipes")
	fmt.Fprintln(w, "  import     Convert external workload manifests into Skiff specs")
	fmt.Fprintln(w, "  logs       Query service logs through the cloud provider")
	fmt.Fprintln(w, "  metrics    Query service metrics through the cloud provider")
	fmt.Fprintln(w, "  object     Verify signed immutable objects")
	fmt.Fprintln(w, "  ops        List, run, inspect, watch, approve, and resume operations")
	fmt.Fprintln(w, "  plan       Dry-run provider resource changes for a spec")
	fmt.Fprintln(w, "  pkg        Add, verify, explain, and lock packages")
	fmt.Fprintln(w, "  plugin     Inspect, validate, and run trusted Skiff plugins")
	fmt.Fprintln(w, "  policy     Explain generated state security policies")
	fmt.Fprintln(w, "  promote    Validate and record release promotion intent")
	fmt.Fprintln(w, "  release    Manage candidates, promotion, and release verification")
	fmt.Fprintln(w, "  rotate     Run secret and credential rotation sagas")
	fmt.Fprintln(w, "  rollback   Roll a service back to a stable release")
	fmt.Fprintln(w, "  rollout    Watch rollout progress")
	fmt.Fprintln(w, "  saga       Inspect saga object state")
	fmt.Fprintln(w, "  solve      Build an agent action graph for service recovery")
	if binary == "skiffd" {
		fmt.Fprintln(w, "  serve      Start the stateless skiffd API server")
	}
	fmt.Fprintln(w, "  state      Inspect object-state paths and developer helpers")
	fmt.Fprintln(w, "  stateful   Plan, apply, and inspect StatefulGroups")
	fmt.Fprintln(w, "  status     Show service status through direct or API mode")
	fmt.Fprintln(w, "  sudo       Assume temporary auditable write credentials")
	fmt.Fprintln(w, "  terraform  Generate Terraform modules for Skiff specs")
	fmt.Fprintln(w, "  tui        Open the terminal operations dashboard")
	fmt.Fprintln(w, "  validate   Parse, default, and validate a Skiff spec")
	fmt.Fprintln(w, "  version    Print version, commit, and build date")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --config <path> --context <name> --env <env> --provider <provider> --region <region>")
	fmt.Fprintln(w, "  --state <uri> --api --direct --format human|json|json-pretty --no-color --yes --trace-id <id>")
}

func printWorkflowsHelp(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s <workflow-command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Workflow commands:")
	fmt.Fprintln(w, "  database   Run managed database backup and restore sagas")
	fmt.Fprintln(w, "  rotate     Run secret, key, and certificate rotation sagas")
	fmt.Fprintln(w, "  failover   Run a regional failover saga")
	fmt.Fprintln(w, "  cutover    Create a weighted traffic cutover saga")
	fmt.Fprintln(w, "  debug      Collect bundles and create audited debug sessions")
	fmt.Fprintln(w, "  drift      Detect provider drift from Skiff resource records")
	fmt.Fprintln(w, "  gc         Plan and apply conservative cleanup actions")
	fmt.Fprintln(w, "  stateful   Plan, apply, inspect, and diagnose StatefulGroups")
	fmt.Fprintln(w, "  ops        Discover and run stateful/package operations")
}

func printAdoptionHelp(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s <adoption-command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Adoption and release commands:")
	fmt.Fprintln(w, "  import kube                 Convert Kubernetes manifests into Skiff specs")
	fmt.Fprintln(w, "  terraform generate          Generate Terraform modules for Skiff specs")
	fmt.Fprintln(w, "  adopt terraform             Record externally managed resources in object state")
	fmt.Fprintln(w, "  ci generate                 Generate CI/CD templates")
	fmt.Fprintln(w, "  contract test               Run CI contract checks")
	fmt.Fprintln(w, "  release candidate create    Record immutable release candidate evidence")
	fmt.Fprintln(w, "  release promote             Validate and record release promotion intent")
}

func printDevHelp(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s <dev-command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Developer and low-level helpers:")
	fmt.Fprintln(w, "  compile    Compile a Skiff spec to provider-neutral IR")
	fmt.Fprintln(w, "  completion Generate shell completions")
	fmt.Fprintln(w, "  authz      Explain authorization and approval decisions")
	fmt.Fprintln(w, "  policy     Explain generated state security policies")
	fmt.Fprintln(w, "  sudo       Assume temporary auditable write credentials")
	fmt.Fprintln(w, "  pkg        Add, verify, explain, and lock packages")
	fmt.Fprintln(w, "  plugin     Inspect, validate, and run trusted Skiff plugins")
	fmt.Fprintln(w, "  object     Verify signed immutable objects")
	fmt.Fprintln(w, "  state      Inspect object-state paths")
	fmt.Fprintln(w, "  events     List local service, operation, or saga events")
	fmt.Fprintln(w, "  saga       Advanced recovery: inspect and operate directly on saga object state")
	fmt.Fprintln(w, "  solve      Build an agent action graph for service recovery")
}

func printGlobalFlags(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s [global flags] <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --config <path>      Path to Skiff config file")
	fmt.Fprintln(w, "  --context <name>     Skiff config context")
	fmt.Fprintln(w, "  --env <env>          Skiff environment")
	fmt.Fprintln(w, "  --provider <name>    Cloud provider")
	fmt.Fprintln(w, "  --region <region>    Cloud provider region")
	fmt.Fprintln(w, "  --state <uri>        Object-state bucket URI")
	fmt.Fprintln(w, "  --api                Use skiffd API mode")
	fmt.Fprintln(w, "  --direct             Use direct object-state mode")
	fmt.Fprintln(w, "  --format <format>    human, json, or json-pretty")
	fmt.Fprintln(w, "  --no-color           Disable ANSI color output")
	fmt.Fprintln(w, "  --yes                Assume yes for confirmations")
	fmt.Fprintln(w, "  --trace-id <id>      Include a trace ID in machine-readable output")
}

type configShowOutput struct {
	OK      bool              `json:"ok"`
	TraceID string            `json:"trace_id,omitempty"`
	Config  config.Config     `json:"config"`
	Sources map[string]string `json:"sources,omitempty"`
	Context string            `json:"context,omitempty"`
	Path    string            `json:"path,omitempty"`
}

type configContextsOutput struct {
	OK             bool                    `json:"ok"`
	TraceID        string                  `json:"trace_id,omitempty"`
	Path           string                  `json:"path,omitempty"`
	CurrentContext string                  `json:"current_context,omitempty"`
	Contexts       []config.ContextSummary `json:"contexts,omitempty"`
}

type configUseContextOutput struct {
	OK      bool   `json:"ok"`
	TraceID string `json:"trace_id,omitempty"`
	Path    string `json:"path,omitempty"`
	Context string `json:"context"`
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
	OK            bool                      `json:"ok"`
	TraceID       string                    `json:"trace_id,omitempty"`
	DryRun        bool                      `json:"dry_run"`
	Terraform     string                    `json:"terraform,omitempty"`
	TerraformPath string                    `json:"terraform_path,omitempty"`
	TeardownPath  string                    `json:"teardown_path,omitempty"`
	ConfigPath    string                    `json:"config_path,omitempty"`
	Context       string                    `json:"context,omitempty"`
	Plan          *bootstrap.AWSPlan        `json:"plan"`
	Apply         *bootstrap.AWSApplyResult `json:"apply,omitempty"`
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
	case "get-contexts", "contexts":
		return runConfigGetContexts(binary, args[1:], root, stdout, stderr)
	case "current-context":
		return runConfigCurrentContext(binary, args[1:], root, stdout, stderr)
	case "use-context":
		return runConfigUseContext(binary, args[1:], root, stdout, stderr)
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

	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	contextName := fs.String("context", root.Context, "Skiff config context name")
	userDataPath := fs.String("user-data", "", "path to runner cloud-init/user-data JSON")

	fs.String("env", root.Env, "Skiff environment name")
	fs.String("provider", root.Provider, "cloud provider name")
	fs.String("region", root.Region, "cloud provider region")
	fs.String("state-bucket", root.State, "object-state bucket URI")
	fs.String("kms-key", "", "KMS key ID or alias")
	fs.String("write-role-arn", "", "IAM role ARN for temporary write escalation")
	fs.Bool("aws-live-apply", false, "validate AWS live apply provider inputs")
	fs.String("aws-vpc-id", "", "AWS VPC ID for live apply resources")
	fs.String("aws-subnet-ids", "", "comma-separated AWS subnet IDs for live apply Auto Scaling Groups")
	fs.String("aws-ami-id", "", "AWS AMI ID for live apply launch templates")
	fs.String("aws-alb-listener-arn", "", "AWS ALB listener ARN for live apply listener rules")
	fs.String("aws-load-balancer-security-group-ref", "", "AWS load balancer security group ID/ref for live apply instance ingress")
	fs.String("auth-mode", "", "auth mode")
	fs.String("log-level", "", "log level")
	fs.String("mode", string(root.Mode), "config mode: api, direct, skiffd, or runner")
	fs.String("api-url", root.APIURL, "skiffd API URL")
	fs.String("service", "", "runner service name")
	fs.String("control-key", "", "runner service control object key")
	fs.String("state", root.State, "alias for --state-bucket")
	direct := fs.Bool("direct", root.directSet, "use direct object-state mode")
	apiMode := fs.Bool("api", root.apiSet, "use skiffd API mode")

	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeConfigError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), nil, stdout, stderr)
	}
	_ = noColor
	_ = yes

	overrides := root.configOverrides()
	flagToField := map[string]string{
		"env":                                  config.FieldEnv,
		"provider":                             config.FieldProvider,
		"region":                               config.FieldRegion,
		"state-bucket":                         config.FieldStateBucket,
		"state":                                config.FieldStateBucket,
		"kms-key":                              config.FieldKMSKey,
		"write-role-arn":                       config.FieldWriteRoleARN,
		"aws-live-apply":                       config.FieldAWSLiveApply,
		"aws-vpc-id":                           config.FieldAWSVPCID,
		"aws-subnet-ids":                       config.FieldAWSSubnetIDs,
		"aws-ami-id":                           config.FieldAWSAMIID,
		"aws-alb-listener-arn":                 config.FieldAWSALBListenerARN,
		"aws-load-balancer-security-group-ref": config.FieldAWSLoadBalancerSecurityGroupRef,
		"auth-mode":                            config.FieldAuthMode,
		"log-level":                            config.FieldLogLevel,
		"mode":                                 config.FieldMode,
		"api-url":                              config.FieldAPIURL,
		"service":                              config.FieldService,
		"control-key":                          config.FieldControlKey,
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
		Context:      *contextName,
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
			Context: redacted.Context,
			Path:    redacted.ConfigPath,
		}); err != nil {
			fmt.Fprintf(stderr, "%s config show: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	default:
		return writeConfigError(binary, *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), redacted.Sources, stdout, stderr)
	}
}

func runConfigGetContexts(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" config get-contexts", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	contextName := fs.String("context", root.Context, "Skiff config context name")
	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeConfigError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), nil, stdout, stderr)
	}
	_ = noColor
	path, effective := config.ResolveConfigSelection(*configPath, *contextName, nil)
	if path == "" {
		path = config.DefaultConfigFilename
	}
	file, err := config.LoadSkiffConfigFile(path)
	if err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	if effective == "" {
		effective = file.Current()
	}
	summaries := file.Summaries(effective)
	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "config: %s\n", path)
		if effective != "" {
			fmt.Fprintf(stdout, "current-context: %s\n", effective)
		}
		fmt.Fprintln(stdout, "contexts:")
		for _, summary := range summaries {
			marker := " "
			if summary.Current {
				marker = "*"
			}
			fmt.Fprintf(stdout, "%s %s", marker, summary.Name)
			if summary.Mode != "" {
				fmt.Fprintf(stdout, " mode=%s", summary.Mode)
			}
			if summary.Env != "" {
				fmt.Fprintf(stdout, " env=%s", summary.Env)
			}
			if summary.Provider != "" || summary.Region != "" {
				fmt.Fprintf(stdout, " provider=%s region=%s", summary.Provider, summary.Region)
			}
			if summary.StateBucket != "" {
				fmt.Fprintf(stdout, " state=%s", summary.StateBucket)
			}
			if summary.APIURL != "" {
				fmt.Fprintf(stdout, " api=%s", summary.APIURL)
			}
			fmt.Fprintln(stdout)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(configContextsOutput{OK: true, TraceID: *traceID, Path: path, CurrentContext: effective, Contexts: summaries}); err != nil {
			fmt.Fprintf(stderr, "%s config get-contexts: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	default:
		return writeConfigError(binary, *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), nil, stdout, stderr)
	}
}

func runConfigCurrentContext(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" config current-context", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	contextName := fs.String("context", root.Context, "Skiff config context name")
	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeConfigError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), nil, stdout, stderr)
	}
	_ = noColor
	path, effective := config.ResolveConfigSelection(*configPath, *contextName, nil)
	if path == "" {
		path = config.DefaultConfigFilename
	}
	file, err := config.LoadSkiffConfigFile(path)
	if err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	if effective == "" {
		effective = file.Current()
	}
	if effective == "" {
		return writeConfigError(binary, *format, *traceID, errors.New("current-context is not set"), nil, stdout, stderr)
	}
	switch *format {
	case "human", "text":
		fmt.Fprintln(stdout, effective)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(configUseContextOutput{OK: true, TraceID: *traceID, Path: path, Context: effective}); err != nil {
			fmt.Fprintf(stderr, "%s config current-context: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	default:
		return writeConfigError(binary, *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), nil, stdout, stderr)
	}
}

func runConfigUseContext(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" config use-context", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	flagArgs, positionals, err := splitArgs(args, map[string]bool{
		"config":   true,
		"format":   true,
		"no-color": false,
		"trace-id": true,
	})
	if err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	if len(positionals) != 1 {
		return writeConfigError(binary, *format, *traceID, errors.New("context name is required"), nil, stdout, stderr)
	}
	_ = noColor
	path, _ := config.ResolveConfigSelection(*configPath, "", nil)
	if path == "" {
		path = config.DefaultConfigFilename
	}
	file, err := config.LoadSkiffConfigFile(path)
	if err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	name := positionals[0]
	if err := file.SetCurrentContext(name); err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	if err := config.WriteSkiffConfigFile(path, file); err != nil {
		return writeConfigError(binary, *format, *traceID, err, nil, stdout, stderr)
	}
	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "switched to context %s\n", name)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(configUseContextOutput{OK: true, TraceID: *traceID, Path: path, Context: name}); err != nil {
			fmt.Fprintf(stderr, "%s config use-context: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	default:
		return writeConfigError(binary, *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), nil, stdout, stderr)
	}
}

func printConfigHuman(w io.Writer, loaded config.Loaded) {
	if loaded.ConfigPath != "" {
		fmt.Fprintf(w, "config_path: %s\n", loaded.ConfigPath)
	}
	if loaded.Context != "" {
		fmt.Fprintf(w, "context: %s\n", loaded.Context)
	}
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

	format := fs.String("format", "human", "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "apply bootstrap changes without prompting")
	dryRun := fs.Bool("dry-run", false, "print the bootstrap plan without mutating cloud resources")
	emitTerraform := fs.String("emit", "", "emit generated artifacts; supported value: terraform")
	outDir := fs.String("out", "", "directory for emitted bootstrap artifacts")
	configPath := fs.String("config", "", "Skiff config file to create or update with the bootstrapped context")
	contextName := fs.String("context", "", "Skiff context name to create or update")
	noWriteConfig := fs.Bool("no-write-config", false, "do not create or update a Skiff config context")
	env := fs.String("env", defaultSkiffEnvFromEnv(), "Skiff environment name")
	environmentClass := fs.String("class", defaultEnvironmentClassFromEnv(), "environment class: production, staging, development, or sandbox")
	region := fs.String("region", defaultAWSRegionFromEnv(), "AWS region")
	bucket := fs.String("bucket", "", "S3 state bucket name")
	stateBucket := fs.String("state-bucket", strings.TrimSpace(os.Getenv("SKIFF_STATE_BUCKET")), "S3 state bucket URI or name")
	kmsAlias := fs.String("kms-alias", "", "KMS alias for state bucket encryption")
	developerRole := fs.String("developer-role", "", "IAM role name for read-only developers")
	deployerRole := fs.String("deployer-role", "", "IAM role name for deployers")
	runnerRole := fs.String("runner-role", "", "IAM role name for runners")
	skiffdRole := fs.String("skiffd-role", "", "IAM role name for skiffd")
	network := fs.String("network", bootstrap.NetworkNone, "environment network mode: none or managed")
	ingress := fs.String("ingress", bootstrap.IngressPrivate, "environment ingress default: private, public, or internal-http")
	companyName := fs.String("company-name", defaultSkiffCompanyNameFromEnv(), "company name used in generated AWS names, state bucket, and tags")
	domainName := fs.String("domain-name", defaultSkiffDomainNameFromEnv(), "public DNS zone name used to create <env>.<domain> and wildcard service hosts")
	hostName := fs.String("host-name", defaultSkiffHostNameFromEnv(), "public ingress base hostname override")
	hostedZoneID := fs.String("hosted-zone-id", strings.TrimSpace(os.Getenv("SKIFF_AWS_HOSTED_ZONE_ID")), "Route53 hosted zone ID for public ingress DNS")
	certificateARN := fs.String("certificate-arn", strings.TrimSpace(os.Getenv("SKIFF_AWS_CERTIFICATE_ARN")), "existing ACM certificate ARN for public ingress HTTPS")
	runnerAMIID := fs.String("runner-ami-id", "", "default AWS AMI ID for workload VMs")
	runnerAMISSMParameter := fs.String("runner-ami-ssm-parameter", "", "AWS Systems Manager parameter for workload VM AMI when --runner-ami-id is omitted")
	runnerInstallVersion := fs.String("runner-install-version", "", "Skiff release tag installed on generic runner AMIs")
	runnerInstallBaseURL := fs.String("runner-install-base-url", "", "base URL for runner release archives")
	runnerInstallScriptURL := fs.String("runner-install-script-url", "", "URL for the pinned runner install script")
	signingBackend := fs.String("signing-backend", strings.TrimSpace(os.Getenv("SKIFF_RELEASE_SIGNING_BACKEND")), "release signing backend: aws-kms or keychain")
	signingKeyRef := fs.String("signing-key-ref", strings.TrimSpace(os.Getenv("SKIFF_RELEASE_SIGNING_KEY_REF")), "release signing key reference; defaults to a managed AWS KMS key")

	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writeBootstrapError(binary, *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	_ = noColor
	normalizedEnvironmentClass, err := schema.NormalizeEnvironmentClass(*environmentClass)
	if err != nil {
		return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
	}
	if bootstrapProdEnvNeedsClassConfirmation(*env, normalizedEnvironmentClass) && !*yes {
		err := bootstrapProdEnvClassConfirmationError(*env, normalizedEnvironmentClass, bootstrapAWSFlagWasSet(fs, "class"))
		return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
	}

	if *bucket == "" && *stateBucket != "" {
		*bucket = bucketNameFromStateBucket(*stateBucket)
	}
	var signerRecord *signing.ReleaseSignerRecord
	releaseSigningKeyRef := ""
	shouldWriteContext := !*dryRun && !*noWriteConfig && (*outDir != "" || (*emitTerraform == "" && *yes))
	resolvedSigningBackend, err := normalizeReleaseSigningBackend(*signingBackend)
	if err != nil {
		return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
	}
	explicitSigningKeyRef := strings.TrimSpace(*signingKeyRef)
	if explicitSigningKeyRef != "" {
		releaseSigningKeyRef = explicitSigningKeyRef
	}
	if releaseSigningKeyRef == "" && resolvedSigningBackend == awsprovider.KMSReleaseSigningScheme {
		releaseSigningKeyRef = awsprovider.DefaultKMSReleaseSigningRef(*env, *region)
	}
	if !*dryRun {
		switch {
		case explicitSigningKeyRef != "":
			if isAWSKMSReleaseSigningRef(explicitSigningKeyRef) && *emitTerraform == "terraform" {
				break
			}
			store, err := signerStoreForKeyRef(context.Background(), *region, explicitSigningKeyRef)
			if err != nil {
				return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
			}
			record, _, err := store.Resolve(context.Background(), explicitSigningKeyRef)
			if err != nil {
				return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
			}
			signerRecord = record
			releaseSigningKeyRef = record.KeyRef
		case resolvedSigningBackend == "keychain" && (shouldWriteContext || (*emitTerraform == "" && *yes)):
			record, _, err := releaseSignerStore.Ensure(context.Background(), *env)
			if err != nil {
				return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
			}
			signerRecord = record
			releaseSigningKeyRef = record.KeyRef
		case resolvedSigningBackend == awsprovider.KMSReleaseSigningScheme && *emitTerraform == "" && *yes:
			store, err := newAWSReleaseSignerStore(context.Background(), *region)
			if err != nil {
				return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
			}
			record, _, err := store.Ensure(context.Background(), *env)
			if err != nil {
				return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
			}
			signerRecord = record
			releaseSigningKeyRef = record.KeyRef
		}
	}
	plan, err := bootstrap.PlanAWS(bootstrap.AWSOptions{
		Env:                     *env,
		EnvironmentClass:        normalizedEnvironmentClass,
		Region:                  *region,
		StateBucket:             *bucket,
		KMSAlias:                *kmsAlias,
		DeveloperRole:           *developerRole,
		DeployerRole:            *deployerRole,
		RunnerRole:              *runnerRole,
		SkiffdRole:              *skiffdRole,
		Network:                 *network,
		Ingress:                 *ingress,
		CompanyName:             *companyName,
		DomainName:              *domainName,
		HostName:                *hostName,
		HostedZoneID:            *hostedZoneID,
		CertificateARN:          *certificateARN,
		RunnerAMIID:             *runnerAMIID,
		RunnerAMISSMParameter:   *runnerAMISSMParameter,
		RunnerInstallVersion:    *runnerInstallVersion,
		RunnerInstallBaseURL:    *runnerInstallBaseURL,
		RunnerInstallScriptURL:  *runnerInstallScriptURL,
		ReleaseSigningKeyID:     releaseSignerRecordKeyID(signerRecord),
		ReleaseSigningKeyRef:    firstNonEmptyString(releaseSignerRecordKeyRef(signerRecord), releaseSigningKeyRef),
		ReleaseSigningAlgorithm: releaseSignerRecordAlgorithm(signerRecord),
		ReleaseSigningEncoding:  releaseSignerRecordEncoding(signerRecord),
		ReleaseSigningPublicKey: releaseSignerRecordPublicKey(signerRecord),
	})
	if err != nil {
		return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
	}

	var terraform string
	var terraformPath string
	var teardownPath string
	if *emitTerraform != "" {
		if *emitTerraform != "terraform" {
			return writeBootstrapError(binary, *format, *traceID, errors.New(`unsupported emit target; expected "terraform"`), stdout, stderr)
		}
		terraform, err = bootstrap.TerraformAWS(plan)
		if err != nil {
			return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
		}
		if *outDir != "" {
			if err := os.MkdirAll(*outDir, 0o755); err != nil {
				return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
			}
			terraformPath = filepath.Join(*outDir, "main.tf")
			if err := os.WriteFile(terraformPath, []byte(terraform), 0o644); err != nil {
				return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
			}
			teardown, err := bootstrap.AWSTeardownScript(plan)
			if err != nil {
				return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
			}
			teardownPath = filepath.Join(*outDir, "teardown-aws-cli.sh")
			if err := os.WriteFile(teardownPath, []byte(teardown), 0o755); err != nil {
				return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
			}
			terraform = ""
		}
	} else if *outDir != "" && !*dryRun && *yes {
		if err := os.MkdirAll(*outDir, 0o755); err != nil {
			return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
		}
		teardown, err := bootstrap.AWSTeardownScript(plan)
		if err != nil {
			return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
		}
		teardownPath = filepath.Join(*outDir, "teardown-aws-cli.sh")
		if err := os.WriteFile(teardownPath, []byte(teardown), 0o755); err != nil {
			return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
		}
	}
	var applyResult *bootstrap.AWSApplyResult
	if !*dryRun && *emitTerraform == "" {
		if !*yes {
			return writeBootstrapError(binary, *format, *traceID, errors.New("bootstrap apply requires --yes; use --dry-run to inspect the plan"), stdout, stderr)
		}
		client, err := newAWSBootstrapClient(context.Background(), plan.Region)
		if err != nil {
			return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
		}
		applyResult, err = bootstrap.ApplyAWS(context.Background(), client, plan)
		if err != nil {
			return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
		}
	}

	outputPlan := plan
	if applyResult != nil {
		materialized := *plan
		materialized.RootConfig = applyResult.RootConfig
		outputPlan = &materialized
	}

	writtenConfigPath := ""
	writtenContext := ""
	if shouldWriteContext {
		writtenConfigPath = strings.TrimSpace(*configPath)
		if writtenConfigPath == "" {
			writtenConfigPath = config.DefaultConfigFilename
		}
		writtenContext = strings.TrimSpace(*contextName)
		if writtenContext == "" {
			writtenContext = plan.Env
		}
		if err := writeBootstrapAWSContext(writtenConfigPath, writtenContext, outputPlan); err != nil {
			return writeBootstrapError(binary, *format, *traceID, err, stdout, stderr)
		}
	}

	switch *format {
	case "human", "text":
		if terraform != "" {
			fmt.Fprint(stdout, terraform)
			return ExitSuccess
		}
		printBootstrapAWSPlan(stdout, outputPlan, bootstrapAWSLocalArtifacts{
			TerraformPath: terraformPath,
			TeardownPath:  teardownPath,
			ConfigPath:    writtenConfigPath,
			Context:       writtenContext,
			Apply:         applyResult,
		})
		return ExitSuccess
	case "json":
		enc := json.NewEncoder(stdout)
		if err := enc.Encode(bootstrapAWSOutput{
			OK:            true,
			TraceID:       *traceID,
			DryRun:        *dryRun,
			Terraform:     terraform,
			TerraformPath: terraformPath,
			TeardownPath:  teardownPath,
			ConfigPath:    writtenConfigPath,
			Context:       writtenContext,
			Plan:          outputPlan,
			Apply:         applyResult,
		}); err != nil {
			fmt.Fprintf(stderr, "%s bootstrap aws: %v\n", binary, err)
			return ExitUserError
		}
		return ExitSuccess
	default:
		return writeBootstrapError(binary, *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

type bootstrapAWSLocalArtifacts struct {
	TerraformPath string
	TeardownPath  string
	ConfigPath    string
	Context       string
	Apply         *bootstrap.AWSApplyResult
}

func printBootstrapAWSPlan(w io.Writer, plan *bootstrap.AWSPlan, artifacts bootstrapAWSLocalArtifacts) {
	fmt.Fprintf(w, "AWS bootstrap plan for %s in %s\n", plan.Env, plan.Region)
	if plan.RootConfig.EnvironmentClass != "" {
		fmt.Fprintf(w, "environment_class: %s\n", plan.RootConfig.EnvironmentClass)
	}
	fmt.Fprintf(w, "state_bucket: %s\n", plan.StateBucketURI)
	fmt.Fprintf(w, "kms_alias: %s\n", plan.KMSAlias)
	fmt.Fprintf(w, "root_object: %s\n", plan.RootObjectKey)
	if plan.CompanyName != "" {
		fmt.Fprintf(w, "company_name: %s\n", plan.CompanyName)
	}
	if plan.PublicBaseDomain != "" {
		fmt.Fprintf(w, "public_base_domain: %s\n", plan.PublicBaseDomain)
		fmt.Fprintf(w, "default_service_host: %s\n", strings.ReplaceAll(plan.DefaultHostTemplate, "{service}", "<service>"))
	} else if plan.RootConfig.Ingress != nil && plan.RootConfig.Ingress.Type == bootstrap.IngressPublic {
		fmt.Fprintln(w, "public_base_domain: AWS ALB DNS name after apply")
	}
	if plan.CertificateARN != "" {
		fmt.Fprintf(w, "certificate: %s\n", plan.CertificateARN)
	} else if plan.PublicBaseDomain != "" {
		fmt.Fprintln(w, "certificate: Skiff-managed ACM DNS-validated certificate for base and wildcard service hosts")
	}
	if plan.ReleaseSigningKeyID != "" {
		fmt.Fprintf(w, "release_signing_key: %s\n", plan.ReleaseSigningKeyID)
	} else if plan.ReleaseSigningKeyRef != "" {
		fmt.Fprintf(w, "release_signing_key_ref: %s\n", plan.ReleaseSigningKeyRef)
	}
	if plan.RootConfig.ReleasePolicy != nil {
		fmt.Fprintf(w, "release_policy: require_signed_releases=%t allow_unsigned_code=%t\n", plan.RootConfig.ReleasePolicy.RequireSignedReleases, plan.RootConfig.ReleasePolicy.AllowUnsignedCode)
	}
	if developerRole := plan.RootConfig.Roles["developer"]; developerRole != "" {
		fmt.Fprintf(w, "read_role: %s (read-only state inspection)\n", developerRole)
	}
	if writeRole := plan.RootConfig.Roles["deployer"]; writeRole != "" {
		fmt.Fprintf(w, "write_role: %s (temporary STS elevation via skiff sudo <business-justification>)\n", writeRole)
	}
	if artifacts.TerraformPath != "" {
		fmt.Fprintf(w, "terraform: %s\n", artifacts.TerraformPath)
	}
	if artifacts.TeardownPath != "" {
		fmt.Fprintf(w, "aws_cli_teardown: %s\n", artifacts.TeardownPath)
	}
	if artifacts.ConfigPath != "" {
		fmt.Fprintf(w, "config: %s#%s\n", artifacts.ConfigPath, artifacts.Context)
	}
	if artifacts.Apply != nil {
		fmt.Fprintf(w, "applied: %d actions\n", len(artifacts.Apply.Actions))
		for _, action := range artifacts.Apply.Actions {
			if action.ProviderID != "" {
				fmt.Fprintf(w, "  %s %s: %s (%s)\n", action.Kind, action.Name, action.Action, action.ProviderID)
			} else {
				fmt.Fprintf(w, "  %s %s: %s\n", action.Kind, action.Name, action.Action)
			}
		}
	}
	for _, resource := range plan.Resources {
		fmt.Fprintf(w, "- %s %s: %s\n", resource.Kind, resource.Name, resource.Summary)
	}
}

func writeBootstrapAWSContext(path, contextName string, plan *bootstrap.AWSPlan) error {
	if plan == nil {
		return errors.New("aws bootstrap plan is required")
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("config path is required")
	}
	if strings.TrimSpace(contextName) == "" {
		return errors.New("context name is required")
	}
	file := &config.SkiffConfigFile{
		CurrentContext: contextName,
		Contexts:       []config.NamedContext{},
	}
	if existing, err := config.LoadSkiffConfigFile(path); err == nil {
		file = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	next := config.NamedContext{
		Name: contextName,
		Context: config.ContextConfig{
			Mode:             config.ModeDirect,
			Env:              plan.Env,
			EnvironmentClass: plan.RootConfig.EnvironmentClass,
			Provider:         bootstrap.ProviderAWS,
			Region:           plan.Region,
			State:            plan.StateBucketURI,
			WriteRoleARN:     plan.RootConfig.Roles["deployer"],
			AWSLiveApply:     true,
		},
	}
	if plan.RootConfig.ReleasePolicy != nil {
		next.Context.ReleasePolicy = &config.ReleasePolicyConfig{
			RequireSignedReleases: plan.RootConfig.ReleasePolicy.RequireSignedReleases,
			AllowUnsignedCode:     plan.RootConfig.ReleasePolicy.AllowUnsignedCode,
		}
	}
	if plan.ReleaseSigningKeyID != "" || plan.ReleaseSigningKeyRef != "" {
		next.Context.Signing = &config.SigningConfig{Release: &config.ReleaseSigningConfig{
			KeyID:  plan.ReleaseSigningKeyID,
			KeyRef: plan.ReleaseSigningKeyRef,
		}}
	}
	replaced := false
	for i := range file.Contexts {
		if file.Contexts[i].Name == contextName {
			file.Contexts[i] = next
			replaced = true
			break
		}
	}
	if !replaced {
		file.Contexts = append(file.Contexts, next)
	}
	file.CurrentContext = contextName
	file.CurrentContextCamel = ""
	file.CurrentContextSnake = ""
	return config.WriteSkiffConfigFile(path, file)
}

func releaseSignerRecordKeyID(record *signing.ReleaseSignerRecord) string {
	if record == nil {
		return ""
	}
	return record.KeyID
}

func releaseSignerRecordKeyRef(record *signing.ReleaseSignerRecord) string {
	if record == nil {
		return ""
	}
	return record.KeyRef
}

func releaseSignerRecordPublicKey(record *signing.ReleaseSignerRecord) string {
	if record == nil {
		return ""
	}
	return record.PublicKey
}

func releaseSignerRecordAlgorithm(record *signing.ReleaseSignerRecord) string {
	if record == nil {
		return ""
	}
	return record.Algorithm
}

func releaseSignerRecordEncoding(record *signing.ReleaseSignerRecord) string {
	if record == nil {
		return ""
	}
	return record.Encoding
}

func normalizeReleaseSigningBackend(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", awsprovider.KMSReleaseSigningScheme:
		return awsprovider.KMSReleaseSigningScheme, nil
	case "keychain":
		return "keychain", nil
	default:
		return "", fmt.Errorf("signing backend must be %q or %q", awsprovider.KMSReleaseSigningScheme, "keychain")
	}
}

func signerStoreForKeyRef(ctx context.Context, region, keyRef string) (signing.ReleaseSignerStore, error) {
	if isAWSKMSReleaseSigningRef(keyRef) {
		if refRegion := awsprovider.KMSReleaseSigningRegion(keyRef); refRegion != "" {
			region = refRegion
		}
		return newAWSReleaseSignerStore(ctx, region)
	}
	return releaseSignerStore, nil
}

func isAWSKMSReleaseSigningRef(keyRef string) bool {
	return strings.HasPrefix(strings.TrimSpace(keyRef), awsprovider.KMSReleaseSigningScheme+"://")
}

func bootstrapProdEnvNeedsClassConfirmation(env, environmentClass string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", schema.EnvironmentClassProduction:
	default:
		return false
	}
	return environmentClass != schema.EnvironmentClassProduction
}

func bootstrapProdEnvClassConfirmationError(env, environmentClass string, classFlagSet bool) error {
	if classFlagSet {
		return fmt.Errorf("environment %q looks like production but bootstrap class is %q; rerun with --class production or pass --yes to confirm this non-production class", env, environmentClass)
	}
	return fmt.Errorf("environment %q looks like production but --class was not set, so bootstrap defaults to %q; rerun with --class production or pass --yes to confirm this non-production class", env, environmentClass)
}

func bootstrapAWSFlagWasSet(fs *flag.FlagSet, name string) bool {
	wasSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			wasSet = true
		}
	})
	return wasSet
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
					Command:  binary + " bootstrap aws --env <env> --region <region> --network managed --ingress private --dry-run --format json",
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

func defaultSkiffEnvFromEnv() string {
	return strings.TrimSpace(os.Getenv("SKIFF_ENV"))
}

func defaultEnvironmentClassFromEnv() string {
	value := strings.TrimSpace(os.Getenv("SKIFF_ENVIRONMENT_CLASS"))
	if value == "" {
		return schema.EnvironmentClassDevelopment
	}
	return value
}

func defaultSkiffCompanyNameFromEnv() string {
	return firstNonEmptyCLI(
		strings.TrimSpace(os.Getenv("SKIFF_COMPANY_NAME")),
		strings.TrimSpace(os.Getenv("SKIFF_COMPANY")),
	)
}

func defaultSkiffDomainNameFromEnv() string {
	return firstNonEmptyCLI(
		strings.TrimSpace(os.Getenv("SKIFF_DOMAIN_NAME")),
		strings.TrimSpace(os.Getenv("SKIFF_DOMAIN")),
	)
}

func defaultSkiffHostNameFromEnv() string {
	return firstNonEmptyCLI(
		strings.TrimSpace(os.Getenv("SKIFF_HOST_NAME")),
		strings.TrimSpace(os.Getenv("SKIFF_HOSTNAME")),
	)
}

func defaultAWSRegionFromEnv() string {
	return firstNonEmptyCLI(
		strings.TrimSpace(os.Getenv("AWS_REGION")),
		strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION")),
		strings.TrimSpace(os.Getenv("SKIFF_REGION")),
	)
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

	format := fs.String("format", "human", "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", "", "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	role := fs.String("role", "", "policy role: state-bucket, developer, runner, deployer, skiffd, or break-glass")
	bucket := fs.String("bucket", "", "S3 state bucket name")
	stateBucket := fs.String("state-bucket", "", "S3 state bucket URI or name")
	kmsAlias := fs.String("kms-alias", "alias/skiff-state", "KMS alias used by IAM role policies")

	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
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
		return writePolicyError(binary, *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
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

	if handled, err := parseCommandFlags(fs, args, stdout); handled {
		return ExitSuccess
	} else if err != nil {
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
			return writeEventsError(binary, *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
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
		return writeEventsError(binary, *flags.format, *flags.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
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
			return writeEventsError(binary, format, traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
		}
		if opts.Once {
			return ExitSuccess
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
					Command:  binary + " ops events <service> --state-dir <dir> --format json",
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

func runCompile(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
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

	format := fs.String("format", "human", "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", false, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	contextName := fs.String("context", root.Context, "Skiff config context name")
	filePath := fs.String("file", "", "Skiff YAML or JSON spec file")
	outPath := fs.String("out", "", "write canonical IR JSON to path, or - for stdout")
	allowUnknown := fs.Bool("allow-unknown-fields", false, "accept unknown fields for compatibility checks")
	packageFlags := addPackageCompileFlags(fs)

	flagArgs, positionals, err := splitCompileArgs(args)
	if err != nil {
		return writeCompileError(binary, "SPEC_COMPILE_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
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
		return writeCompileError(binary, "SPEC_COMPILE_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), nil, stdout, stderr)
	}
	_ = noColor
	_ = yes
	loaded, err := config.Load(config.LoadOptions{
		ModeDefault: defaultMode(binary),
		ConfigPath:  *configPath,
		Context:     *contextName,
		Overrides:   root.configOverrides(),
	})
	if err != nil {
		return writeCompileError(binary, "CONFIG_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}

	doc, err := spec.LoadFile(*filePath, spec.DecodeOptions{AllowUnknownFields: *allowUnknown})
	if err != nil {
		return writeCompileError(binary, "SPEC_DECODE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	compileOpts, err := compilerOptionsForDocumentWithConfig(*doc, packageFlags, true, loaded.Config)
	if err != nil {
		var validation spec.ValidationError
		if errors.As(err, &validation) {
			return writeCompileError(binary, "SPEC_INVALID", *format, *traceID, errors.New("spec validation failed"), validation.Diagnostics, stdout, stderr)
		}
		return writeCompileError(binary, "SPEC_COMPILE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compileOpts)
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
		fmt.Fprintf(stdout, "compiled %s %s/%s to IR", doc.Kind, graph.Env, graph.Service)
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
	return writeCompileError(binary, "SPEC_COMPILE_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), nil, stdout, stderr)
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

func runValidate(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
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
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", false, "assume yes for commands that ask for confirmation")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	contextName := fs.String("context", root.Context, "Skiff config context name")
	filePath := fs.String("file", "", "Skiff YAML or JSON spec file")
	allowUnknown := fs.Bool("allow-unknown-fields", false, "accept unknown fields for compatibility checks")
	showDefaulted := fs.Bool("show-defaulted", false, "include the defaulted spec in output")
	packageFlags := addPackageCompileFlags(fs)

	flagArgs, positionals, err := splitValidateArgs(args)
	if err != nil {
		return writeSpecError(binary, "SPEC_VALIDATE_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
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
	loaded, err := config.Load(config.LoadOptions{
		ModeDefault: defaultMode(binary),
		ConfigPath:  *configPath,
		Context:     *contextName,
		Overrides:   root.configOverrides(),
	})
	if err != nil {
		return writeSpecError(binary, "CONFIG_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	releasePolicy, err := config.EffectiveReleasePolicy(loaded.Config)
	if err != nil {
		return writeSpecError(binary, "CONFIG_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if packageFlags.environmentClass != nil && *packageFlags.environmentClass != "" {
		classCfg := config.Config{EnvironmentClass: *packageFlags.environmentClass}
		releasePolicy, err = config.EffectiveReleasePolicy(classCfg)
		if err != nil {
			return writeSpecError(binary, "CONFIG_INVALID", *format, *traceID, err, nil, stdout, stderr)
		}
	}

	doc, err := spec.LoadFile(*filePath, spec.DecodeOptions{AllowUnknownFields: *allowUnknown})
	if err != nil {
		return writeSpecError(binary, "SPEC_DECODE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	result := spec.ValidateWithOptions(*doc, spec.ValidationOptions{
		RequireDigestPinnedArtifacts: releasePolicy.RequireSignedReleases,
	})
	if !result.OK {
		return writeSpecError(binary, "SPEC_INVALID", *format, *traceID, errors.New("spec validation failed"), result.Diagnostics, stdout, stderr)
	}
	if _, err := compilerOptionsForDocumentWithConfig(*doc, packageFlags, false, loaded.Config); err != nil {
		var validation spec.ValidationError
		if errors.As(err, &validation) {
			return writeSpecError(binary, "SPEC_INVALID", *format, *traceID, errors.New("spec validation failed"), validation.Diagnostics, stdout, stderr)
		}
		return writeSpecError(binary, "SPEC_INVALID", *format, *traceID, err, nil, stdout, stderr)
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
		return writeSpecError(binary, "SPEC_VALIDATE_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human", "json", "json-pretty", or "yaml"`), nil, stdout, stderr)
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
	fmt.Fprintln(w, "  show             Print effective configuration")
	fmt.Fprintln(w, "  get-contexts     List contexts in a .skiffconfig file")
	fmt.Fprintln(w, "  current-context  Print the active context")
	fmt.Fprintln(w, "  use-context      Switch current-context in a .skiffconfig file")
}

func printBootstrapUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s bootstrap <provider> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  aws        Plan or emit AWS state substrate bootstrap")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "AWS flags:")
	fmt.Fprintln(w, "  --env <env>")
	fmt.Fprintln(w, "  --class production|staging|development|sandbox  Defaults to development")
	fmt.Fprintln(w, "  --region <region>")
	fmt.Fprintln(w, "  --bucket <bucket>      Optional; autogenerated when omitted")
	fmt.Fprintln(w, "  --network none|managed")
	fmt.Fprintln(w, "  --ingress private|public|internal-http")
	fmt.Fprintln(w, "  --company-name <name>             Optional; defaults from SKIFF_COMPANY_NAME")
	fmt.Fprintln(w, "  --domain-name <domain>            Optional; public ingress creates <env>.<domain> and wildcard service hosts")
	fmt.Fprintln(w, "  --host-name <hostname>            Optional; public ingress base hostname override")
	fmt.Fprintln(w, "  --hosted-zone-id <zone>           Optional; Route53 zone override")
	fmt.Fprintln(w, "  --certificate-arn <arn>           Optional; reuse an ACM cert instead of creating one")
	fmt.Fprintln(w, "  --developer-role <name>           Optional; read-only developer IAM role")
	fmt.Fprintln(w, "  --deployer-role <name>            Optional; write IAM role requiring auditable STS escalation")
	fmt.Fprintln(w, "  --runner-role <name>              Optional; runner IAM role")
	fmt.Fprintln(w, "  --skiffd-role <name>              Optional; skiffd IAM role")
	fmt.Fprintln(w, "  --runner-ami-id <ami>              Optional; custom runner AMI")
	fmt.Fprintln(w, "  --runner-ami-ssm-parameter <path>  Optional; defaults to Skiff AL2023 x86_64")
	fmt.Fprintln(w, "  --runner-install-version <tag>     Optional; used with the public AL2023 fallback AMI")
	fmt.Fprintln(w, "  --dry-run")
	fmt.Fprintln(w, "  --yes                            Confirm writes and intentional non-production class for prod env names")
	fmt.Fprintln(w, "  --emit terraform")
	fmt.Fprintln(w, "  --out <dir>                       Write generated artifacts and teardown script")
	fmt.Fprintln(w, "  --config <path>                   Config file to update when --out is set")
	fmt.Fprintln(w, "  --context <name>                  Context name to update; defaults to env")
	fmt.Fprintln(w, "  --no-write-config")
	fmt.Fprintln(w, "  --format human|json|json-pretty")
}

func printPolicyUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s policy <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  explain    Explain a generated state bucket or IAM policy")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Explain flags:")
	fmt.Fprintln(w, "  --role state-bucket|developer|runner|deployer|skiffd|break-glass")
	fmt.Fprintln(w, "  --bucket <bucket>")
	fmt.Fprintln(w, "  --kms-alias <alias/name>")
	fmt.Fprintln(w, "  --format human|json|json-pretty")
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
	fmt.Fprintln(w, "  --format human|json|json-pretty")
}

func printValidateUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s validate <skiff.yaml> [flags]\n\n", binary)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --format human|json|json-pretty|yaml")
	fmt.Fprintln(w, "  --show-defaulted")
	fmt.Fprintln(w, "  --config <path>")
	fmt.Fprintln(w, "  --context <name>")
	fmt.Fprintln(w, "  --environment-class production|staging|development|sandbox")
	fmt.Fprintln(w, "  --allow-unknown-fields")
	fmt.Fprintln(w, "  --trace-id <id>")
}

func printCompileUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s compile <skiff.yaml> [flags]\n\n", binary)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --format human|json|json-pretty")
	fmt.Fprintln(w, "  --out <path>")
	fmt.Fprintln(w, "  --config <path>")
	fmt.Fprintln(w, "  --context <name>")
	fmt.Fprintln(w, "  --environment-class production|staging|development|sandbox")
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
	case "list":
		return runReleaseList(binary, args[1:], root, stdout, stderr)
	case "promote":
		return runPromoteCommand(binary, "release promote", args[1:], root, stdout, stderr)
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

	format := fs.String("format", "human", "output format: human, json, or json-pretty")
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
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
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
		return writeVerifyError(binary, "RELEASE_VERIFY_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
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

	format := fs.String("format", "human", "output format: human, json, or json-pretty")
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
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
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
		return writeVerifyError(binary, "OBJECT_VERIFY_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
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

	format := fs.String("format", "human", "output format: human, json, or json-pretty")
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
	group := fs.String("group", "", "stateful group name")
	member := fs.Int("member", -1, "stateful member ordinal")
	resourceKind := fs.String("resource-kind", "", "resource kind")
	name := fs.String("name", "", "logical resource name")
	provider := fs.String("provider", "", "cloud provider")
	providerID := fs.String("id", "", "provider resource ID")
	day := fs.String("day", "", "UTC audit day yyyy-mm-dd")
	observation := fs.String("observation", "", "observation ID")

	if handled, err := parseCommandFlags(fs, args[1:], stdout); handled {
		return ExitSuccess
	} else if err != nil {
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
		group:        *group,
		member:       *member,
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
				group:        *group,
				member:       *member,
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
		return writeStateError(binary, *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
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
	group        string
	member       int
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
	case "stateful":
		switch defaultString(in.doc, "group") {
		case "group", "control":
			return paths.StatefulGroupControl(in.group)
		case "member":
			return paths.StatefulMemberControl(in.group, in.member)
		default:
			return "", fmt.Errorf("stateful --doc must be group, control, or member")
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
		"group":         in.group,
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
		out = nil
	}
	if in.member >= 0 {
		if out == nil {
			out = map[string]string{}
		}
		out["member"] = fmt.Sprintf("%d", in.member)
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
		"cache":             true,
		"config":            true,
		"context":           true,
		"environment-class": true,
		"file":              true,
		"format":            true,
		"lockfile":          true,
		"trace-id":          true,
	}
	return splitArgs(args, valueFlags)
}

func splitCompileArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"cache":             true,
		"config":            true,
		"context":           true,
		"environment-class": true,
		"file":              true,
		"format":            true,
		"lockfile":          true,
		"out":               true,
		"trace-id":          true,
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
	fmt.Fprintln(w, "  list       List immutable release manifests for a service")
	fmt.Fprintln(w, "  promote    Validate and record release promotion intent")
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
	fmt.Fprintln(w, "  stateful            --group <name> [--doc group|member] [--member <ordinal>]")
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
	case config.FieldReleaseSigningKeyID:
		return cfg.ReleaseSigningKeyID
	case config.FieldReleaseSigningKeyRef:
		return cfg.ReleaseSigningKeyRef
	case config.FieldWriteRoleARN:
		return cfg.WriteRoleARN
	case config.FieldAWSLiveApply:
		if cfg.AWSLiveApply {
			return "true"
		}
		return ""
	case config.FieldAWSVPCID:
		return cfg.AWSVPCID
	case config.FieldAWSSubnetIDs:
		return strings.Join(cfg.AWSSubnetIDs, ",")
	case config.FieldAWSAMIID:
		return cfg.AWSAMIID
	case config.FieldAWSALBListenerARN:
		return cfg.AWSALBListenerARN
	case config.FieldAWSLoadBalancerSecurityGroupRef:
		return cfg.AWSLoadBalancerSecurityGroupRef
	default:
		return ""
	}
}
