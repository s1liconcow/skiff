package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/explain"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/plugins"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
)

type explainOutput struct {
	OK            bool                       `json:"ok"`
	TraceID       string                     `json:"trace_id,omitempty"`
	Result        explain.Result             `json:"result"`
	AWS           any                        `json:"aws,omitempty"`
	PluginPatches []plugins.PatchExplanation `json:"plugin_patches,omitempty"`
}

func runExplain(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "assume yes for commands that ask for confirmation")
	configPath := fs.String("config", root.ConfigPath, "path to Skiff config file")
	contextName := fs.String("context", root.Context, "Skiff config context name")
	filePath := fs.String("file", "", "Skiff YAML or JSON spec file")
	providerName := fs.String("provider", defaultString(root.Provider, "aws"), "provider to explain")
	region := fs.String("region", root.Region, "cloud provider region")
	stateBucket := fs.String("state", root.State, "object-state bucket URI")
	releaseID := fs.String("release-id", "", "release ID to place in runner user-data")
	packageFlags := addPackageCompileFlags(fs)
	var pluginPaths pluginPathsFlag
	fs.Var(&pluginPaths, "plugin", "plugin manifest path or directory; may be repeated")

	flagArgs, positionals, err := splitExplainArgs(args)
	if err != nil {
		return writeSpecError(binary, "EXPLAIN_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeSpecError(binary, "EXPLAIN_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writeSpecError(binary, "EXPLAIN_INVALID", *format, *traceID, fmt.Errorf("unexpected argument %q", positionals[1]), nil, stdout, stderr)
	}
	if *filePath == "" && len(positionals) == 1 {
		*filePath = positionals[0]
	}
	if *filePath == "" {
		return writeSpecError(binary, "EXPLAIN_INVALID", *format, *traceID, errors.New("spec file is required"), nil, stdout, stderr)
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
		return writeSpecError(binary, "EXPLAIN_INVALID", *format, *traceID, err, nil, stdout, stderr)
	}

	doc, err := spec.LoadFile(*filePath, spec.DecodeOptions{})
	if err != nil {
		return writeSpecError(binary, "SPEC_DECODE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	compileOpts, err := compilerOptionsForDocumentWithConfig(*doc, packageFlags, true, loaded.Config)
	if err != nil {
		var validation spec.ValidationError
		if errors.As(err, &validation) {
			return writeSpecError(binary, "SPEC_INVALID", *format, *traceID, errors.New("spec validation failed"), validation.Diagnostics, stdout, stderr)
		}
		return writeSpecError(binary, "SPEC_COMPILE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	graph, err := compiler.Compile(nilContext(), *doc, compileOpts)
	if err != nil {
		var validation spec.ValidationError
		if errors.As(err, &validation) {
			return writeSpecError(binary, "SPEC_INVALID", *format, *traceID, errors.New("spec validation failed"), validation.Diagnostics, stdout, stderr)
		}
		return writeSpecError(binary, "SPEC_COMPILE_FAILED", *format, *traceID, err, nil, stdout, stderr)
	}
	var patchExplanations []plugins.PatchExplanation
	if len(pluginPaths) > 0 {
		registry, err := plugins.LoadRegistry(nilContext(), plugins.RegistryOptions{Paths: pluginPaths})
		if err != nil {
			return writeSpecError(binary, "PLUGIN_INVALID", *format, *traceID, err, nil, stdout, stderr)
		}
		specBody, err := json.Marshal(doc)
		if err != nil {
			return writeSpecError(binary, "PLUGIN_INVALID", *format, *traceID, err, nil, stdout, stderr)
		}
		host := plugins.NewHost(registry, nil)
		sets, err := host.MutateIR(nilContext(), graph, specBody, *traceID)
		if err != nil {
			return writeSpecError(binary, "PLUGIN_DENIED", *format, *traceID, err, nil, stdout, stderr)
		}
		if err := plugins.ApplyIRPatches(graph, sets); err != nil {
			return writeSpecError(binary, "PLUGIN_PATCH_FAILED", *format, *traceID, err, nil, stdout, stderr)
		}
		patchExplanations = plugins.ExplainPatchSets(sets)
	}
	if *providerName != "aws" {
		return writeSpecError(binary, "EXPLAIN_INVALID", *format, *traceID, fmt.Errorf("unsupported provider %q; expected aws", *providerName), nil, stdout, stderr)
	}
	var result explain.Result
	var awsDetails any
	if doc.Kind == spec.KindStatefulGroup && *region == "" {
		result = explain.StatefulReadOnly(*providerName, graph)
	} else {
		lowered, err := aws.LowerService(graph, aws.LowerOptions{
			Region:      *region,
			StateBucket: *stateBucket,
			ReleaseID:   *releaseID,
		})
		if err != nil {
			return writeSpecError(binary, "EXPLAIN_FAILED", *format, *traceID, err, nil, stdout, stderr)
		}
		result = explain.AWS(lowered)
		awsDetails = lowered
	}
	packageDerived := explain.PackageDerived(*providerName, graph)
	result.Resources = append(result.Resources, packageDerived.Resources...)

	switch *format {
	case "human", "text":
		if doc.Kind == spec.KindStatefulGroup {
			fmt.Fprintf(stdout, "%s stateful group %s/%s read-only plan:\n", result.Provider, result.Env, result.Service)
		} else {
			fmt.Fprintf(stdout, "%s service %s/%s maps to AWS primitives:\n", result.Provider, result.Env, result.Service)
		}
		for _, resource := range result.Resources {
			fmt.Fprintf(stdout, "- %s %s: %s\n", resource.CloudPrimitive, resource.Name, resource.Why)
			if source := sourcePackageSummary(resource.Source); source != "" {
				fmt.Fprintf(stdout, "  package: %s\n", source)
			}
		}
		for _, patch := range patchExplanations {
			fmt.Fprintf(stdout, "- plugin %s added %s at %s: %s\n", patch.Plugin, patch.Kind, patch.Path, patch.Summary)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(explainOutput{OK: true, TraceID: *traceID, Result: result, AWS: awsDetails, PluginPatches: patchExplanations}); err != nil {
			fmt.Fprintf(stderr, "%s explain: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeSpecError(binary, "EXPLAIN_INVALID", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), nil, stdout, stderr)
	}
}

func splitExplainArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"cache":             true,
		"config":            true,
		"context":           true,
		"environment-class": true,
		"file":              true,
		"format":            true,
		"lockfile":          true,
		"provider":          true,
		"region":            true,
		"release-id":        true,
		"state":             true,
		"trace-id":          true,
		"plugin":            true,
	}
	return splitArgs(args, valueFlags)
}

func sourcePackageSummary(source []ir.SourceRef) string {
	for _, ref := range source {
		if ref.Package == "" && ref.Digest == "" && ref.LockfileDigest == "" {
			continue
		}
		out := ref.Package
		if ref.Version != "" {
			out += "@" + ref.Version
		}
		if out == "" {
			out = ref.Digest
		}
		return out
	}
	return ""
}
