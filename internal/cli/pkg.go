package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	skifferrors "github.com/s1liconcow/skiff/internal/errors"
	opsstate "github.com/s1liconcow/skiff/internal/ops"
	"github.com/s1liconcow/skiff/internal/packages"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

type pkgCommandOutput struct {
	OK                 bool                    `json:"ok"`
	TraceID            string                  `json:"trace_id,omitempty"`
	Package            packages.PackageSummary `json:"package,omitempty"`
	Entry              *packages.LockEntry     `json:"entry,omitempty"`
	Lockfile           string                  `json:"lockfile,omitempty"`
	Cache              *packages.CacheEntry    `json:"cache,omitempty"`
	Explanation        *packages.Explanation   `json:"explanation,omitempty"`
	RecommendedActions []recommendedAction     `json:"recommended_actions,omitempty"`
}

type pkgListOutput struct {
	OK       bool                 `json:"ok"`
	TraceID  string               `json:"trace_id,omitempty"`
	Lockfile string               `json:"lockfile"`
	Packages []packages.LockEntry `json:"packages"`
}

type pkgVerifyOutput struct {
	OK          bool                        `json:"ok"`
	TraceID     string                      `json:"trace_id,omitempty"`
	Package     packages.PackageSummary     `json:"package,omitempty"`
	Entry       *packages.LockEntry         `json:"entry,omitempty"`
	Conformance *packages.ConformanceResult `json:"conformance,omitempty"`
	Diagnostics []spec.Diagnostic           `json:"diagnostics,omitempty"`
}

type pkgOptions struct {
	format             string
	traceID            string
	noColor            bool
	yes                bool
	lockfile           string
	cache              string
	registryDir        string
	expectedDigest     string
	signatureRef       string
	allowUnsignedLocal bool
}

func runPkg(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printPkgUsage(stderr, binary)
		return ExitUserError
	}
	switch args[0] {
	case "add":
		return runPkgAdd(binary, args[1:], root, stdout, stderr)
	case "update":
		return runPkgUpdate(binary, args[1:], root, stdout, stderr)
	case "list":
		return runPkgList(binary, args[1:], root, stdout, stderr)
	case "explain":
		return runPkgExplain(binary, args[1:], root, stdout, stderr)
	case "verify":
		return runPkgVerify(binary, args[1:], root, stdout, stderr)
	case "bundle":
		return runPkgBundle(binary, args[1:], root, stdout, stderr)
	case "help", "-h", "--help":
		printPkgUsage(stdout, binary)
		return ExitSuccess
	default:
		return writePkgError(binary, "pkg", root.Format, root.TraceID, fmt.Errorf("unknown pkg command %q", args[0]), stdout, stderr)
	}
}

func runPkgAdd(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" pkg add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := addPkgFlags(fs, root)
	flagArgs, positionals, err := splitPkgArgs(args)
	if err != nil {
		return writePkgError(binary, "pkg add", opts.format, opts.traceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writePkgError(binary, "pkg add", opts.format, opts.traceID, err, stdout, stderr)
	}
	if len(positionals) != 1 {
		return writePkgError(binary, "pkg add", opts.format, opts.traceID, errors.New("package ref is required"), stdout, stderr)
	}
	resolved, err := packages.Resolve(context.Background(), positionals[0], resolveOptions(opts))
	if err != nil {
		return writePkgError(binary, "pkg add", opts.format, opts.traceID, err, stdout, stderr)
	}
	lock, _, err := packages.ReadLockFile(opts.lockfile)
	if err != nil {
		return writePkgError(binary, "pkg add", opts.format, opts.traceID, err, stdout, stderr)
	}
	lock, err = packages.AddLockEntry(lock, resolved.Entry)
	if err != nil {
		return writePkgError(binary, "pkg add", opts.format, opts.traceID, err, stdout, stderr)
	}
	if diagnostics := packages.ValidateLock(lock, packages.ValidationOptions{AllowUnsignedLocal: opts.allowUnsignedLocal}); len(diagnostics) > 0 {
		return writePkgDiagnostics(binary, "pkg add", opts.format, opts.traceID, diagnostics, stdout, stderr)
	}
	if err := packages.WriteLockFile(opts.lockfile, lock); err != nil {
		return writePkgError(binary, "pkg add", opts.format, opts.traceID, err, stdout, stderr)
	}
	return writePkgSuccess(stdout, stderr, opts.format, binary, "pkg add", pkgCommandOutput{
		OK:       true,
		TraceID:  opts.traceID,
		Package:  packageSummaryForEntry(resolved.Entry),
		Entry:    &resolved.Entry,
		Lockfile: opts.lockfile,
		Cache:    &resolved.Cache,
		RecommendedActions: []recommendedAction{
			{ID: "explain", Command: fmt.Sprintf("%s pkg explain %s --format json", binary, resolved.Entry.Name), Mutating: false},
			{ID: "verify", Command: fmt.Sprintf("%s pkg verify %s --format json", binary, resolved.Entry.Name), Mutating: false},
		},
	}, func() {
		fmt.Fprintf(stdout, "added package %s@%s\n", resolved.Entry.Name, resolved.Entry.Version)
		fmt.Fprintf(stdout, "lockfile: %s\n", opts.lockfile)
		fmt.Fprintf(stdout, "digest: %s\n", resolved.Entry.Digest)
	})
}

func runPkgUpdate(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" pkg update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := addPkgFlags(fs, root)
	flagArgs, positionals, err := splitPkgArgs(args)
	if err != nil {
		return writePkgError(binary, "pkg update", opts.format, opts.traceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writePkgError(binary, "pkg update", opts.format, opts.traceID, err, stdout, stderr)
	}
	if len(positionals) > 1 {
		return writePkgError(binary, "pkg update", opts.format, opts.traceID, fmt.Errorf("unexpected argument %q", positionals[1]), stdout, stderr)
	}
	lock, exists, err := packages.ReadLockFile(opts.lockfile)
	if err != nil {
		return writePkgError(binary, "pkg update", opts.format, opts.traceID, err, stdout, stderr)
	}
	if !exists || len(lock.Packages) == 0 {
		return writePkgError(binary, "pkg update", opts.format, opts.traceID, errors.New("no locked packages to update"), stdout, stderr)
	}
	name := ""
	if len(positionals) == 1 {
		name = positionals[0]
	}
	updated := 0
	var last *packages.ResolvedPackage
	for _, entry := range append([]packages.LockEntry(nil), lock.Packages...) {
		if name != "" && entry.Name != name {
			continue
		}
		ref := firstNonEmptyCLI(entry.Source, entry.Ref)
		resolved, err := packages.Resolve(context.Background(), ref, resolveOptions(opts))
		if err != nil {
			return writePkgError(binary, "pkg update", opts.format, opts.traceID, err, stdout, stderr)
		}
		lock, err = packages.UpdateLockEntry(lock, resolved.Entry, entry.Name)
		if err != nil {
			return writePkgError(binary, "pkg update", opts.format, opts.traceID, err, stdout, stderr)
		}
		updated++
		last = resolved
	}
	if updated == 0 {
		return writePkgError(binary, "pkg update", opts.format, opts.traceID, fmt.Errorf("package %q is not locked", name), stdout, stderr)
	}
	if diagnostics := packages.ValidateLock(lock, packages.ValidationOptions{AllowUnsignedLocal: opts.allowUnsignedLocal}); len(diagnostics) > 0 {
		return writePkgDiagnostics(binary, "pkg update", opts.format, opts.traceID, diagnostics, stdout, stderr)
	}
	if err := packages.WriteLockFile(opts.lockfile, lock); err != nil {
		return writePkgError(binary, "pkg update", opts.format, opts.traceID, err, stdout, stderr)
	}
	out := pkgCommandOutput{OK: true, TraceID: opts.traceID, Lockfile: opts.lockfile}
	if last != nil {
		out.Package = packageSummaryForEntry(last.Entry)
		out.Entry = &last.Entry
		out.Cache = &last.Cache
	}
	return writePkgSuccess(stdout, stderr, opts.format, binary, "pkg update", out, func() {
		fmt.Fprintf(stdout, "updated packages: %d\n", updated)
		fmt.Fprintf(stdout, "lockfile: %s\n", opts.lockfile)
	})
}

func runPkgList(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" pkg list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := addPkgFlags(fs, root)
	flagArgs, positionals, err := splitPkgArgs(args)
	if err != nil {
		return writePkgError(binary, "pkg list", opts.format, opts.traceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writePkgError(binary, "pkg list", opts.format, opts.traceID, err, stdout, stderr)
	}
	if len(positionals) != 0 {
		return writePkgError(binary, "pkg list", opts.format, opts.traceID, fmt.Errorf("unexpected argument %q", positionals[0]), stdout, stderr)
	}
	lock, _, err := packages.ReadLockFile(opts.lockfile)
	if err != nil {
		return writePkgError(binary, "pkg list", opts.format, opts.traceID, err, stdout, stderr)
	}
	switch opts.format {
	case "human", "text":
		if len(lock.Packages) == 0 {
			fmt.Fprintln(stdout, "packages: none")
			return ExitSuccess
		}
		for _, entry := range lock.Packages {
			fmt.Fprintf(stdout, "%s@%s %s\n", entry.Name, entry.Version, entry.Digest)
		}
		return ExitSuccess
	case "json", "json-pretty":
		return encodePkgJSON(stdout, stderr, binary, "pkg list", opts.format, pkgListOutput{OK: true, TraceID: opts.traceID, Lockfile: opts.lockfile, Packages: lock.Packages})
	default:
		return writePkgError(binary, "pkg list", opts.format, opts.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runPkgExplain(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	return runPkgResolveReadOnly(binary, "pkg explain", args, root, stdout, stderr, func(opts *pkgOptions, resolved *packages.ResolvedPackage) (pkgCommandOutput, func()) {
		explanation, err := packages.ExplainResolved(*resolved)
		if err != nil {
			return pkgCommandOutput{OK: false}, func() {}
		}
		out := pkgCommandOutput{OK: true, TraceID: opts.traceID, Package: packageSummaryForEntry(resolved.Entry), Entry: &resolved.Entry, Cache: &resolved.Cache, Explanation: &explanation}
		return out, func() {
			fmt.Fprintf(stdout, "%s@%s\n", resolved.Entry.Name, resolved.Entry.Version)
			fmt.Fprintf(stdout, "digest: %s\n", resolved.Entry.Digest)
			if len(explanation.Exports.OperationProfiles) > 0 {
				fmt.Fprintf(stdout, "operation_profiles: %s\n", strings.Join(explanation.Exports.OperationProfiles, ", "))
			}
			if len(explanation.Exports.PackageSteps) > 0 {
				fmt.Fprintf(stdout, "package_steps: %s\n", strings.Join(explanation.Exports.PackageSteps, ", "))
			}
			if explanation.Plugin != nil && explanation.Plugin.Runtime.Kind != "" {
				fmt.Fprintf(stdout, "plugin_runtime: %s\n", explanation.Plugin.Runtime.Kind)
			}
		}
	})
}

func runPkgVerify(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" pkg verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := addPkgFlags(fs, root)
	conformance := fs.Bool("conformance", false, "run deterministic package conformance checks")
	flagArgs, positionals, err := splitPkgArgs(args)
	if err != nil {
		return writePkgError(binary, "pkg verify", opts.format, opts.traceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writePkgError(binary, "pkg verify", opts.format, opts.traceID, err, stdout, stderr)
	}
	if len(positionals) != 1 {
		return writePkgError(binary, "pkg verify", opts.format, opts.traceID, errors.New("package name or ref is required"), stdout, stderr)
	}
	resolved, err := resolvePackageTarget(positionals[0], opts)
	if err != nil {
		return writePkgError(binary, "pkg verify", opts.format, opts.traceID, err, stdout, stderr)
	}
	lock := packages.LockFile{Schema: schema.PackageLockSchemaVersion, Packages: []packages.LockEntry{resolved.Entry}}
	diagnostics := packages.ValidateLock(lock, packages.ValidationOptions{AllowUnsignedLocal: opts.allowUnsignedLocal})
	if len(diagnostics) > 0 {
		return writePkgDiagnostics(binary, "pkg verify", opts.format, opts.traceID, diagnostics, stdout, stderr)
	}
	if *conformance {
		result := packages.RunConformance(context.Background(), *resolved, packages.ConformanceOptions{
			AllowUnsignedLocal:   opts.allowUnsignedLocal,
			OperationProfileHook: packageOperationProfileHook,
		})
		out := pkgVerifyOutput{
			OK:          result.OK,
			TraceID:     opts.traceID,
			Package:     packageSummaryForEntry(resolved.Entry),
			Entry:       &resolved.Entry,
			Conformance: &result,
			Diagnostics: result.Diagnostics,
		}
		if !result.OK {
			return writePkgConformanceResult(stdout, stderr, opts.format, binary, "pkg verify", out, ExitUserError)
		}
		return writePkgConformanceResult(stdout, stderr, opts.format, binary, "pkg verify", out, ExitSuccess)
	}
	switch opts.format {
	case "human", "text":
		fmt.Fprintf(stdout, "%s@%s verified\n", resolved.Entry.Name, resolved.Entry.Version)
		return ExitSuccess
	case "json", "json-pretty":
		return encodePkgJSON(stdout, stderr, binary, "pkg verify", opts.format, pkgVerifyOutput{OK: true, TraceID: opts.traceID, Package: packageSummaryForEntry(resolved.Entry), Entry: &resolved.Entry})
	default:
		return writePkgError(binary, "pkg verify", opts.format, opts.traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func runPkgBundle(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(binary+" pkg bundle", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := addPkgFlags(fs, root)
	flagArgs, positionals, err := splitPkgArgs(args)
	if err != nil {
		return writePkgError(binary, "pkg bundle", opts.format, opts.traceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writePkgError(binary, "pkg bundle", opts.format, opts.traceID, err, stdout, stderr)
	}
	if len(positionals) != 1 {
		return writePkgError(binary, "pkg bundle", opts.format, opts.traceID, errors.New("package directory is required"), stdout, stderr)
	}
	dir, err := filepath.Abs(positionals[0])
	if err != nil {
		return writePkgError(binary, "pkg bundle", opts.format, opts.traceID, err, stdout, stderr)
	}
	resolved, err := packages.Resolve(context.Background(), "file://"+dir, resolveOptions(opts))
	if err != nil {
		return writePkgError(binary, "pkg bundle", opts.format, opts.traceID, err, stdout, stderr)
	}
	return writePkgSuccess(stdout, stderr, opts.format, binary, "pkg bundle", pkgCommandOutput{OK: true, TraceID: opts.traceID, Package: packageSummaryForEntry(resolved.Entry), Entry: &resolved.Entry, Cache: &resolved.Cache}, func() {
		fmt.Fprintf(stdout, "bundled package %s@%s\n", resolved.Entry.Name, resolved.Entry.Version)
		fmt.Fprintf(stdout, "cache: %s\n", resolved.Cache.Path)
	})
}

func runPkgResolveReadOnly(binary, command string, args []string, root rootOptions, stdout, stderr io.Writer, build func(*pkgOptions, *packages.ResolvedPackage) (pkgCommandOutput, func())) int {
	fs := flag.NewFlagSet(binary+" "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := addPkgFlags(fs, root)
	flagArgs, positionals, err := splitPkgArgs(args)
	if err != nil {
		return writePkgError(binary, command, opts.format, opts.traceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writePkgError(binary, command, opts.format, opts.traceID, err, stdout, stderr)
	}
	if len(positionals) != 1 {
		return writePkgError(binary, command, opts.format, opts.traceID, errors.New("package name or ref is required"), stdout, stderr)
	}
	resolved, err := resolvePackageTarget(positionals[0], opts)
	if err != nil {
		return writePkgError(binary, command, opts.format, opts.traceID, err, stdout, stderr)
	}
	out, human := build(opts, resolved)
	if !out.OK {
		return writePkgError(binary, command, opts.format, opts.traceID, errors.New("package explain failed"), stdout, stderr)
	}
	return writePkgSuccess(stdout, stderr, opts.format, binary, command, out, human)
}

func splitPkgArgs(args []string) ([]string, []string, error) {
	return splitArgs(args, map[string]bool{
		"format":        true,
		"trace-id":      true,
		"lockfile":      true,
		"cache":         true,
		"registry-dir":  true,
		"digest":        true,
		"signature-ref": true,
	})
}

func addPkgFlags(fs *flag.FlagSet, root rootOptions) *pkgOptions {
	opts := &pkgOptions{}
	fs.StringVar(&opts.format, "format", root.Format, "output format: human, json, or json-pretty")
	fs.BoolVar(&opts.noColor, "no-color", root.NoColor, "disable ANSI color output")
	fs.BoolVar(&opts.yes, "yes", root.Yes, "assume yes for commands that ask for confirmation")
	fs.StringVar(&opts.traceID, "trace-id", root.TraceID, "trace identifier")
	fs.StringVar(&opts.lockfile, "lockfile", "skiff.lock.json", "path to skiff.lock.json")
	fs.StringVar(&opts.cache, "cache", packages.DefaultCacheRoot(), "content-addressed package cache directory")
	fs.StringVar(&opts.registryDir, "registry-dir", "", "local registry directory for skiff.dev/name refs")
	fs.StringVar(&opts.expectedDigest, "digest", "", "expected package digest")
	fs.StringVar(&opts.signatureRef, "signature-ref", "", "signature reference for package verification")
	fs.BoolVar(&opts.allowUnsignedLocal, "allow-unsigned-local", false, "allow unsigned file:// packages for local development")
	return opts
}

func resolveOptions(opts *pkgOptions) packages.ResolveOptions {
	return packages.ResolveOptions{
		Cache:              packages.Cache{Root: opts.cache},
		ExpectedDigest:     opts.expectedDigest,
		SignatureRef:       opts.signatureRef,
		AllowUnsignedLocal: opts.allowUnsignedLocal,
		RegistryDir:        opts.registryDir,
		Clock:              func() time.Time { return time.Now().UTC() },
	}
}

func resolvePackageTarget(target string, opts *pkgOptions) (*packages.ResolvedPackage, error) {
	lock, exists, err := packages.ReadLockFile(opts.lockfile)
	if err != nil {
		return nil, err
	}
	if exists {
		for _, entry := range lock.Packages {
			if entry.Name == target {
				cacheEntry, err := (packages.Cache{Root: opts.cache}).Get(entry.Digest)
				if err == nil {
					resolved, err := packages.Resolve(context.Background(), entry.Source, resolveOptions(opts))
					if err == nil {
						resolved.Entry = entry
						resolved.Cache = cacheEntry
						return resolved, nil
					}
				}
				return packages.Resolve(context.Background(), firstNonEmptyCLI(entry.Source, entry.Ref), resolveOptions(opts))
			}
		}
	}
	return packages.Resolve(context.Background(), target, resolveOptions(opts))
}

func writePkgConformanceResult(stdout, stderr io.Writer, format, binary, command string, out pkgVerifyOutput, exit int) int {
	switch format {
	case "human", "text":
		for _, check := range out.Conformance.Checks {
			fmt.Fprintf(stdout, "%s %s: %s\n", check.Status, check.ID, check.Summary)
		}
		if exit != ExitSuccess && len(out.Diagnostics) > 0 {
			fmt.Fprintf(stderr, "%s %s: %s %s: %s\n", binary, command, out.Diagnostics[0].Path, out.Diagnostics[0].Code, out.Diagnostics[0].Message)
		}
		return exit
	case "json", "json-pretty":
		if err := writeJSON(stdout, format, out); err != nil {
			fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
			return ExitInternalError
		}
		return exit
	default:
		return writePkgError(binary, command, format, "", errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func packageOperationProfileHook(ctx context.Context, name string, provenance schema.PackageProvenance) []packages.ConformanceCheck {
	profile, ok := packageOperationProfile(name)
	if !ok {
		return []packages.ConformanceCheck{{
			ID:      "operation_profile." + name,
			Status:  packages.ConformanceFailed,
			Summary: "operation profile export is not available",
			Diagnostics: []spec.Diagnostic{{
				Path:     "$.exports.operation_profiles",
				Code:     "OPERATION_PROFILE_NOT_FOUND",
				Severity: spec.SeverityError,
				Message:  fmt.Sprintf("operation profile %q is not available", name),
			}},
		}}
	}
	explanation, err := opsstate.ExplainProfile(profile)
	if err != nil {
		return []packages.ConformanceCheck{{
			ID:      "operation_profile." + name + ".explain",
			Status:  packages.ConformanceFailed,
			Summary: "operation profile explain failed",
			Diagnostics: []spec.Diagnostic{{
				Path:     "$.exports.operation_profiles",
				Code:     "OPERATION_PROFILE_EXPLAIN_FAILED",
				Severity: spec.SeverityError,
				Message:  err.Error(),
			}},
		}}
	}
	explainDetails, _ := json.Marshal(explanation)
	rendered, err := opsstate.RenderProfile(opsstate.ProfileRenderRequest{
		Profile:   profile,
		SagaID:    "saga_conformance",
		Target:    schema.Target{Kind: "StatefulGroup", Name: "package-conformance"},
		Actor:     schema.Actor{ID: "skiff-package-conformance", Type: "agent"},
		TraceID:   "tr_package_conformance",
		Params:    sampleOperationProfileParams(profile),
		Package:   provenance,
		CreatedAt: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return []packages.ConformanceCheck{
			{ID: "operation_profile." + name + ".explain", Status: packages.ConformancePassed, Summary: "operation profile explain output is valid", Details: explainDetails},
			{
				ID:      "operation_profile." + name + ".render",
				Status:  packages.ConformanceFailed,
				Summary: "operation profile render failed",
				Diagnostics: []spec.Diagnostic{{
					Path:     "$.exports.operation_profiles",
					Code:     "OPERATION_PROFILE_RENDER_FAILED",
					Severity: spec.SeverityError,
					Message:  err.Error(),
				}},
			},
		}
	}
	renderDetails, _ := json.Marshal(map[string]any{
		"saga_id": rendered.Intent.SagaID,
		"steps":   len(rendered.Graph.Nodes),
	})
	return []packages.ConformanceCheck{
		{ID: "operation_profile." + name + ".explain", Status: packages.ConformancePassed, Summary: "operation profile explain output is valid", Details: explainDetails},
		{ID: "operation_profile." + name + ".render", Status: packages.ConformancePassed, Summary: "operation profile renders a saga graph", Details: renderDetails},
	}
}

func packageOperationProfile(name string) (sagaapi.OperationProfile, bool) {
	for _, profile := range opsstate.BuiltInProfiles() {
		if profile.Name == name || string(profile.Kind) == name {
			return profile, true
		}
	}
	return sagaapi.OperationProfile{}, false
}

func sampleOperationProfileParams(profile sagaapi.OperationProfile) map[string]json.RawMessage {
	params := make(map[string]json.RawMessage, len(profile.Params))
	for name, param := range profile.Params {
		if len(param.Default) > 0 {
			params[name] = append(json.RawMessage(nil), param.Default...)
			continue
		}
		params[name] = sampleParamValue(param.Type)
	}
	return params
}

func sampleParamValue(typ sagaapi.ParamType) json.RawMessage {
	switch typ {
	case sagaapi.ParamBoolean:
		return json.RawMessage(`false`)
	case sagaapi.ParamInteger, sagaapi.ParamNumber:
		return json.RawMessage(`1`)
	case sagaapi.ParamObject:
		return json.RawMessage(`{}`)
	case sagaapi.ParamArray:
		return json.RawMessage(`[]`)
	default:
		return json.RawMessage(`"fixture"`)
	}
}

func writePkgSuccess(stdout, stderr io.Writer, format, binary, command string, value any, human func()) int {
	switch format {
	case "human", "text":
		human()
		return ExitSuccess
	case "json", "json-pretty":
		return encodePkgJSON(stdout, stderr, binary, command, format, value)
	default:
		return writePkgError(binary, command, format, "", errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func encodePkgJSON(stdout, stderr io.Writer, binary, command, format string, value any) int {
	if err := writeJSON(stdout, format, value); err != nil {
		fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
		return ExitInternalError
	}
	return ExitSuccess
}

func writePkgDiagnostics(binary, command, format, traceID string, diagnostics []spec.Diagnostic, stdout, stderr io.Writer) int {
	if isJSONFormat(format) {
		_ = writeJSON(stdout, format, pkgVerifyOutput{OK: false, TraceID: traceID, Diagnostics: diagnostics})
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s %s: %s %s: %s\n", binary, command, diagnostics[0].Path, diagnostics[0].Code, diagnostics[0].Message)
	return ExitUserError
}

func writePkgError(binary, command, format, traceID string, err error, stdout, stderr io.Writer) int {
	if isJSONFormat(format) {
		code := string(skifferrors.ValidationFailed)
		var pkgErr *packages.Error
		if errors.As(err, &pkgErr) && pkgErr.Code != "" {
			code = pkgErr.Code
		}
		_ = writeJSON(stdout, format, commandErrorOutput{
			OK:      false,
			Code:    code,
			Summary: err.Error(),
			TraceID: traceID,
			RecommendedActions: []recommendedAction{
				{ID: "show_pkg_help", Command: binary + " pkg --help", Mutating: false},
			},
		})
		return ExitUserError
	}
	fmt.Fprintf(stderr, "%s %s: %v\n", binary, command, err)
	return ExitUserError
}

func packageSummaryForEntry(entry packages.LockEntry) packages.PackageSummary {
	return packages.PackageSummary{Name: entry.Name, Version: entry.Version, Ref: entry.Ref, Digest: entry.Digest}
}

func printPkgUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s pkg <command> [flags]\n\n", binary)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  add <ref>              Add and lock a package")
	fmt.Fprintln(w, "  update [name]          Refresh locked package digests")
	fmt.Fprintln(w, "  list                   List packages in skiff.lock.json")
	fmt.Fprintln(w, "  explain <name|ref>     Explain package exports and plugin capabilities")
	fmt.Fprintln(w, "  verify <name|ref>      Verify package manifest, digest, and signature reference")
	fmt.Fprintln(w, "  bundle <dir>           Store a local package in the content-addressed cache")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --lockfile skiff.lock.json --cache .skiff/packages/cache --registry-dir <dir>")
	fmt.Fprintln(w, "  --conformance --allow-unsigned-local --signature-ref <ref> --digest sha256:<hex>")
	fmt.Fprintln(w, "  --format human|json|json-pretty --no-color --yes --trace-id <id>")
}
