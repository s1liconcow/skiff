package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	skifferrors "github.com/s1liconcow/skiff/internal/errors"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/plugins"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

type pluginPathsFlag []string

func (f *pluginPathsFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *pluginPathsFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("plugin path cannot be empty")
	}
	*f = append(*f, value)
	return nil
}

type pluginListOutput struct {
	OK      bool            `json:"ok"`
	TraceID string          `json:"trace_id,omitempty"`
	Plugins []pluginSummary `json:"plugins"`
}

type pluginValidateOutput struct {
	OK          bool                   `json:"ok"`
	TraceID     string                 `json:"trace_id,omitempty"`
	Plugin      pluginSummary          `json:"plugin"`
	Diagnostics []pluginapi.Diagnostic `json:"diagnostics,omitempty"`
}

type pluginExplainOutput struct {
	OK           bool                       `json:"ok"`
	TraceID      string                     `json:"trace_id,omitempty"`
	Plugin       pluginSummary              `json:"plugin"`
	Permissions  pluginapi.Permissions      `json:"permissions"`
	Capabilities []pluginapi.Capability     `json:"capabilities,omitempty"`
	Patches      []plugins.PatchExplanation `json:"patches,omitempty"`
	Diagnostics  []pluginapi.Diagnostic     `json:"diagnostics,omitempty"`
}

type pluginDevOutput struct {
	OK       bool            `json:"ok"`
	TraceID  string          `json:"trace_id,omitempty"`
	Plugin   pluginSummary   `json:"plugin"`
	Hook     pluginapi.Hook  `json:"hook"`
	Response json.RawMessage `json:"response,omitempty"`
}

type pluginSummary struct {
	Name         string                `json:"name"`
	Version      string                `json:"version"`
	Source       plugins.Source        `json:"source"`
	Hooks        []pluginapi.Hook      `json:"hooks,omitempty"`
	Runtime      pluginapi.RuntimeSpec `json:"runtime,omitempty"`
	Capabilities []string              `json:"capabilities,omitempty"`
	Package      *pluginapi.PackageRef `json:"package,omitempty"`
}

func runPlugin(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printPluginUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "list":
		return runPluginList(binary, args[1:], root, stdout, stderr)
	case "validate":
		return runPluginValidate(binary, args[1:], root, stdout, stderr)
	case "explain":
		return runPluginExplain(binary, args[1:], root, stdout, stderr)
	case "dev":
		return runPluginDev(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printPluginUsage(stdout, binary)
		return ExitSuccess
	default:
		return writePluginError(binary, "plugin", root.Format, root.TraceID, fmt.Errorf("unknown plugin command %q", args[0]), stdout, stderr)
	}
}

func runPluginList(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" plugin list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	var pluginPaths pluginPathsFlag
	fs.Var(&pluginPaths, "plugin", "plugin manifest path or directory; may be repeated")
	if err := fs.Parse(args); err != nil {
		return writePluginError(binary, "plugin list", *format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writePluginError(binary, "plugin list", *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	_ = noColor
	_ = yes
	registry, err := plugins.LoadRegistry(nilContext(), plugins.RegistryOptions{Paths: pluginPaths})
	if err != nil {
		return writePluginError(binary, "plugin list", *format, *traceID, err, stdout, stderr)
	}
	summaries := pluginSummaries(registry.Plugins)
	switch *format {
	case "human", "text":
		if len(summaries) == 0 {
			fmt.Fprintln(stdout, "plugins: none")
			return ExitSuccess
		}
		for _, plugin := range summaries {
			fmt.Fprintf(stdout, "%s@%s hooks=%s source=%s\n", plugin.Name, plugin.Version, joinHooks(plugin.Hooks), plugin.Source.Path)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(pluginListOutput{OK: true, TraceID: *traceID, Plugins: summaries}); err != nil {
			fmt.Fprintf(stderr, "%s plugin list: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writePluginError(binary, "plugin list", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runPluginValidate(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" plugin validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	pluginPath := fs.String("plugin", "", "plugin manifest path or directory")
	flagArgs, positionals, err := splitPluginPathArgs(args)
	if err != nil {
		return writePluginError(binary, "plugin validate", *format, *traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writePluginError(binary, "plugin validate", *format, *traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writePluginError(binary, "plugin validate", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *pluginPath == "" && len(positionals) == 1 {
		*pluginPath = positionals[0]
	}
	if *pluginPath == "" {
		return writePluginError(binary, "plugin validate", *format, *traceID, errors.New("plugin path is required"), stdout, stderr)
	}
	_ = noColor
	_ = yes
	registry, err := plugins.LoadRegistry(nilContext(), plugins.RegistryOptions{Paths: []string{*pluginPath}})
	if err != nil {
		return writePluginError(binary, "plugin validate", *format, *traceID, err, stdout, stderr)
	}
	summary := pluginSummaryFor(registry.Plugins[0])
	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s@%s valid\n", summary.Name, summary.Version)
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(pluginValidateOutput{OK: true, TraceID: *traceID, Plugin: summary}); err != nil {
			fmt.Fprintf(stderr, "%s plugin validate: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writePluginError(binary, "plugin validate", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runPluginExplain(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" plugin explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	pluginPath := fs.String("plugin", "", "plugin manifest path or directory")
	specPath := fs.String("spec", "", "optional Skiff spec to compile and pass through mutate_ir")
	flagArgs, positionals, err := splitPluginExplainArgs(args)
	if err != nil {
		return writePluginError(binary, "plugin explain", *format, *traceID, err, stdout, stderr)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return writePluginError(binary, "plugin explain", *format, *traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writePluginError(binary, "plugin explain", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	if *pluginPath == "" && len(positionals) == 1 {
		*pluginPath = positionals[0]
	}
	if *pluginPath == "" {
		return writePluginError(binary, "plugin explain", *format, *traceID, errors.New("plugin path is required"), stdout, stderr)
	}
	_ = noColor
	_ = yes
	registry, err := plugins.LoadRegistry(nilContext(), plugins.RegistryOptions{Paths: []string{*pluginPath}})
	if err != nil {
		return writePluginError(binary, "plugin explain", *format, *traceID, err, stdout, stderr)
	}
	var patches []plugins.PatchExplanation
	var diagnostics []pluginapi.Diagnostic
	if *specPath != "" {
		graph, specBody, err := compilePluginSpec(*specPath)
		if err != nil {
			return writePluginError(binary, "plugin explain", *format, *traceID, err, stdout, stderr)
		}
		host := plugins.NewHost(registry, nil)
		sets, err := host.MutateIR(nilContext(), graph, specBody, *traceID)
		if err != nil {
			return writePluginError(binary, "plugin explain", *format, *traceID, err, stdout, stderr)
		}
		patches = plugins.ExplainPatchSets(sets)
		for _, set := range sets {
			diagnostics = append(diagnostics, set.Diagnostics...)
		}
	}
	plugin := registry.Plugins[0]
	out := pluginExplainOutput{
		OK:           true,
		TraceID:      *traceID,
		Plugin:       pluginSummaryFor(plugin),
		Permissions:  plugin.Manifest.Permissions,
		Capabilities: plugin.Manifest.Capabilities,
		Patches:      patches,
		Diagnostics:  diagnostics,
	}
	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s@%s\n", out.Plugin.Name, out.Plugin.Version)
		fmt.Fprintf(stdout, "hooks: %s\n", joinHooks(out.Plugin.Hooks))
		if len(out.Permissions.AllowedPatchKinds) > 0 {
			fmt.Fprintf(stdout, "allowed patches: %s\n", strings.Join(out.Permissions.AllowedPatchKinds, ", "))
		}
		for _, patch := range out.Patches {
			fmt.Fprintf(stdout, "- plugin patch %s %s %s: %s\n", patch.Op, patch.Kind, patch.Path, patch.Summary)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(out); err != nil {
			fmt.Fprintf(stderr, "%s plugin explain: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writePluginError(binary, "plugin explain", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runPluginDev(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" plugin dev", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	pluginPath := fs.String("plugin", "", "plugin manifest path or directory")
	hook := fs.String("hook", "", "hook to run locally")
	requestPath := fs.String("request", "", "JSON request body")
	if err := fs.Parse(args); err != nil {
		return writePluginError(binary, "plugin dev", *format, *traceID, err, stdout, stderr)
	}
	if fs.NArg() != 0 {
		return writePluginError(binary, "plugin dev", *format, *traceID, fmt.Errorf("unexpected argument %q", fs.Arg(0)), stdout, stderr)
	}
	if *pluginPath == "" || *hook == "" {
		return writePluginError(binary, "plugin dev", *format, *traceID, errors.New("--plugin and --hook are required"), stdout, stderr)
	}
	_ = noColor
	_ = yes
	registry, err := plugins.LoadRegistry(nilContext(), plugins.RegistryOptions{Paths: []string{*pluginPath}})
	if err != nil {
		return writePluginError(binary, "plugin dev", *format, *traceID, err, stdout, stderr)
	}
	var request json.RawMessage = []byte(`{}`)
	if *requestPath != "" {
		body, err := os.ReadFile(*requestPath)
		if err != nil {
			return writePluginError(binary, "plugin dev", *format, *traceID, err, stdout, stderr)
		}
		request = append(json.RawMessage(nil), body...)
	}
	plugin := registry.Plugins[0]
	var response json.RawMessage
	runner := plugins.CommandRunner{}
	if err := runner.RunPluginHook(nilContext(), plugin, pluginapi.Hook(*hook), request, &response); err != nil {
		return writePluginError(binary, "plugin dev", *format, *traceID, err, stdout, stderr)
	}
	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s\n", response)
		return ExitSuccess
	case "json":
		out := pluginDevOutput{OK: true, TraceID: *traceID, Plugin: pluginSummaryFor(plugin), Hook: pluginapi.Hook(*hook), Response: response}
		if err := json.NewEncoder(stdout).Encode(out); err != nil {
			fmt.Fprintf(stderr, "%s plugin dev: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writePluginError(binary, "plugin dev", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func compilePluginSpec(path string) (*ir.Graph, []byte, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	doc, err := spec.Decode(body, spec.DecodeOptions{})
	if err != nil {
		return nil, nil, err
	}
	graph, err := compiler.Compile(nilContext(), *doc, compiler.Options{})
	if err != nil {
		return nil, nil, err
	}
	return graph, body, nil
}

func pluginSummaries(items []plugins.Plugin) []pluginSummary {
	out := make([]pluginSummary, 0, len(items))
	for _, item := range items {
		out = append(out, pluginSummaryFor(item))
	}
	return out
}

func pluginSummaryFor(plugin plugins.Plugin) pluginSummary {
	return pluginSummary{
		Name:         plugin.Manifest.Name,
		Version:      plugin.Manifest.Version,
		Source:       plugin.Source,
		Hooks:        append([]pluginapi.Hook(nil), plugin.Manifest.Hooks...),
		Runtime:      plugin.Manifest.Runtime,
		Capabilities: plugin.CapabilityNames(pluginapi.CapabilityIRPatch),
		Package:      plugin.Manifest.Package,
	}
}

func joinHooks(hooks []pluginapi.Hook) string {
	if len(hooks) == 0 {
		return "none"
	}
	values := make([]string, 0, len(hooks))
	for _, hook := range hooks {
		values = append(values, string(hook))
	}
	return strings.Join(values, ",")
}

func splitPluginPathArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"format":   true,
		"plugin":   true,
		"trace-id": true,
	}
	return splitArgs(args, valueFlags)
}

func splitPluginExplainArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"format":   true,
		"plugin":   true,
		"spec":     true,
		"trace-id": true,
	}
	return splitArgs(args, valueFlags)
}

func writePluginError(binary, command, format, traceID string, err error, stdout, stderr io.Writer) int {
	if format == "json" {
		out := commandErrorOutput{
			OK:      false,
			Code:    string(skifferrors.ValidationFailed),
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{ID: "validate_plugin", Command: binary + " plugin validate <path> --format json", Mutating: false},
			},
		}
		var manifestErr plugins.ManifestError
		if errors.As(err, &manifestErr) {
			for _, diagnostic := range manifestErr.Diagnostics {
				out.Fields = append(out.Fields, configFieldFromPluginDiagnostic(diagnostic))
			}
		}
		if encErr := json.NewEncoder(stdout).Encode(out); encErr != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, encErr)
		}
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
	return ExitUserError
}

func configFieldFromPluginDiagnostic(diagnostic pluginapi.Diagnostic) config.FieldError {
	return config.FieldError{
		Field:   diagnostic.Field,
		Source:  "plugin",
		Code:    diagnostic.Code,
		Message: diagnostic.Summary,
	}
}

func printPluginUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s plugin <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  list      List configured plugin manifests")
	fmt.Fprintln(w, "  validate  Validate a plugin manifest")
	fmt.Fprintln(w, "  explain   Explain plugin permissions, capabilities, and emitted patches")
	fmt.Fprintln(w, "  dev       Run a local command plugin hook with a JSON request")
}
