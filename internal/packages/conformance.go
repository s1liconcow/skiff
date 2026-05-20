package packages

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/s1liconcow/skiff/internal/plugins"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

type ConformanceStatus string

const (
	ConformancePassed ConformanceStatus = "passed"
	ConformanceFailed ConformanceStatus = "failed"
)

type ConformanceOptions struct {
	AllowUnsignedLocal   bool
	OperationProfileHook OperationProfileConformanceHook
}

type OperationProfileConformanceHook func(context.Context, string, schema.PackageProvenance) []ConformanceCheck

type ConformanceResult struct {
	OK          bool                     `json:"ok"`
	Package     PackageSummary           `json:"package"`
	Entry       LockEntry                `json:"entry"`
	Provenance  schema.PackageProvenance `json:"provenance"`
	Checks      []ConformanceCheck       `json:"checks"`
	Diagnostics []spec.Diagnostic        `json:"diagnostics,omitempty"`
}

type ConformanceCheck struct {
	ID          string            `json:"id"`
	Status      ConformanceStatus `json:"status"`
	Summary     string            `json:"summary"`
	Diagnostics []spec.Diagnostic `json:"diagnostics,omitempty"`
	Details     json.RawMessage   `json:"details,omitempty"`
}

func RunConformance(ctx context.Context, resolved ResolvedPackage, opts ConformanceOptions) ConformanceResult {
	result := ConformanceResult{
		Package: PackageSummary{
			Name:    resolved.Manifest.Name,
			Version: resolved.Manifest.Version,
			Ref:     resolved.Entry.Ref,
			Digest:  resolved.Entry.Digest,
		},
		Entry:      resolved.Entry,
		Provenance: conformanceProvenance(resolved.Entry),
	}
	result.addCheck(validateConformancePackageManifest(resolved.Manifest))
	result.addCheck(validateConformanceLock(resolved.Entry, opts.AllowUnsignedLocal))
	result.addChecks(validateConformancePlugin(resolved))
	result.addChecks(validateConformanceOperationProfiles(ctx, resolved.Manifest.Exports.OperationProfiles, result.Provenance, opts.OperationProfileHook))
	result.addChecks(validateConformanceCLIExamples(resolved.Directory))
	result.OK = len(result.Diagnostics) == 0
	return result
}

func (r *ConformanceResult) addCheck(check ConformanceCheck) {
	r.Checks = append(r.Checks, check)
	if check.Status == ConformanceFailed {
		r.Diagnostics = append(r.Diagnostics, check.Diagnostics...)
	}
}

func (r *ConformanceResult) addChecks(checks []ConformanceCheck) {
	for _, check := range checks {
		r.addCheck(check)
	}
}

func validateConformancePackageManifest(manifest Manifest) ConformanceCheck {
	diagnostics := ValidateManifest(manifest)
	if len(diagnostics) > 0 {
		return failedCheck("package_manifest", "package manifest schema is invalid", diagnostics...)
	}
	return passedCheck("package_manifest", "package manifest schema is valid", nil)
}

func validateConformanceLock(entry LockEntry, allowUnsignedLocal bool) ConformanceCheck {
	lock := LockFile{Schema: schema.PackageLockSchemaVersion, Packages: []LockEntry{entry}}
	diagnostics := ValidateLock(lock, ValidationOptions{AllowUnsignedLocal: allowUnsignedLocal})
	if len(diagnostics) > 0 {
		return failedCheck("lockfile_compatibility", "package lock entry is not production-compatible", diagnostics...)
	}
	return passedCheck("lockfile_compatibility", "package lock entry is compatible", nil)
}

func validateConformancePlugin(resolved ResolvedPackage) []ConformanceCheck {
	if resolved.Manifest.Plugin == nil {
		if len(resolved.Manifest.Exports.PackageSteps) == 0 && len(resolved.Manifest.Exports.DoctorChecks) == 0 {
			return []ConformanceCheck{passedCheck("plugin_manifest", "package has no plugin exports", nil)}
		}
		return []ConformanceCheck{failedCheck("plugin_manifest", "package exports plugin capabilities but has no plugin manifest", spec.Diagnostic{
			Path:     "$.plugin",
			Code:     "PACKAGE_PLUGIN_REQUIRED",
			Severity: spec.SeverityError,
			Message:  "package_steps or doctor_checks exports require plugin.manifest",
		})}
	}
	path := filepath.Join(resolved.Directory, filepath.FromSlash(resolved.Manifest.Plugin.Manifest))
	manifest, err := plugins.LoadManifest(path)
	if err != nil {
		return []ConformanceCheck{failedCheck("plugin_manifest", "plugin manifest is invalid", spec.Diagnostic{
			Path:     "$.plugin.manifest",
			Code:     "PACKAGE_PLUGIN_INVALID",
			Severity: spec.SeverityError,
			Message:  err.Error(),
		})}
	}
	checks := []ConformanceCheck{passedCheck("plugin_manifest", "plugin manifest is valid", nil)}
	checks = append(checks, validateExportedPackageSteps(resolved.Manifest.Exports.PackageSteps, manifest))
	checks = append(checks, validateExportedDoctorChecks(resolved.Manifest.Exports.DoctorChecks, manifest))
	return checks
}

func validateExportedPackageSteps(exports []string, manifest pluginapi.Manifest) ConformanceCheck {
	if len(exports) == 0 {
		return passedCheck("package_steps", "package exports no package steps", nil)
	}
	declared := map[string]struct{}{}
	allowed := stringSet(manifest.Permissions.PackageStepKinds)
	for _, capability := range manifest.Capabilities {
		if capability.Kind != pluginapi.CapabilityPackageStep {
			continue
		}
		for _, step := range capability.PackageSteps {
			if _, ok := allowed[step.Kind]; ok {
				declared[step.Kind] = struct{}{}
			}
		}
	}
	var diagnostics []spec.Diagnostic
	for i, name := range exports {
		if _, ok := declared[name]; !ok {
			diagnostics = append(diagnostics, spec.Diagnostic{
				Path:     fmt.Sprintf("$.exports.package_steps[%d]", i),
				Code:     "PACKAGE_STEP_NOT_DECLARED",
				Severity: spec.SeverityError,
				Message:  fmt.Sprintf("package step %q is not declared by plugin package_step capabilities and permissions", name),
			})
		}
	}
	if len(diagnostics) > 0 {
		return failedCheck("package_steps", "package step exports are not backed by plugin capabilities", diagnostics...)
	}
	return passedCheck("package_steps", "package step exports are backed by plugin capabilities", nil)
}

func validateExportedDoctorChecks(exports []string, manifest pluginapi.Manifest) ConformanceCheck {
	if len(exports) == 0 {
		return passedCheck("doctor_checks", "package exports no doctor checks", nil)
	}
	declared := map[string]struct{}{}
	if manifest.Permissions.DoctorChecks {
		for _, capability := range manifest.Capabilities {
			if capability.Kind != pluginapi.CapabilityDoctorCheck {
				continue
			}
			for _, check := range capability.DoctorChecks {
				declared[check] = struct{}{}
			}
		}
	}
	var diagnostics []spec.Diagnostic
	for i, name := range exports {
		if _, ok := declared[name]; !ok {
			diagnostics = append(diagnostics, spec.Diagnostic{
				Path:     fmt.Sprintf("$.exports.doctor_checks[%d]", i),
				Code:     "DOCTOR_CHECK_NOT_DECLARED",
				Severity: spec.SeverityError,
				Message:  fmt.Sprintf("doctor check %q is not declared by plugin doctor_check capabilities and permissions", name),
			})
		}
	}
	if len(diagnostics) > 0 {
		return failedCheck("doctor_checks", "doctor check exports are not backed by plugin capabilities", diagnostics...)
	}
	return passedCheck("doctor_checks", "doctor check exports are backed by plugin capabilities", nil)
}

func validateConformanceOperationProfiles(ctx context.Context, exports []string, provenance schema.PackageProvenance, hook OperationProfileConformanceHook) []ConformanceCheck {
	if len(exports) == 0 {
		return []ConformanceCheck{passedCheck("operation_profiles", "package exports no operation profiles", nil)}
	}
	if hook == nil {
		return []ConformanceCheck{failedCheck("operation_profiles", "operation profile conformance hook is not configured", spec.Diagnostic{
			Path:     "$.exports.operation_profiles",
			Code:     "OPERATION_PROFILE_HOOK_REQUIRED",
			Severity: spec.SeverityError,
			Message:  "operation profile exports require an explain/render conformance hook",
		})}
	}
	var checks []ConformanceCheck
	for _, name := range exports {
		checks = append(checks, hook(ctx, name, provenance)...)
	}
	return checks
}

func validateConformanceCLIExamples(dir string) []ConformanceCheck {
	if dir == "" {
		return []ConformanceCheck{passedCheck("cli_examples", "no package directory available for CLI example checks", nil)}
	}
	examples, diagnostics := packageCLIExamples(dir)
	if len(diagnostics) > 0 {
		return []ConformanceCheck{failedCheck("cli_examples", "package docs contain invalid CLI examples", diagnostics...)}
	}
	details, _ := json.Marshal(map[string]any{"commands": examples})
	return []ConformanceCheck{passedCheck("cli_examples", fmt.Sprintf("parsed %d package CLI example(s)", len(examples)), details)}
}

func packageCLIExamples(dir string) ([]string, []spec.Diagnostic) {
	var examples []string
	var diagnostics []spec.Diagnostic
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			diagnostics = append(diagnostics, spec.Diagnostic{Path: path, Code: "PACKAGE_DOC_READ_FAILED", Severity: spec.SeverityError, Message: err.Error()})
			return nil
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "$"))
			if !strings.HasPrefix(line, "skiff ") {
				continue
			}
			examples = append(examples, line)
			if err := validateCLIExample(line); err != nil {
				diagnostics = append(diagnostics, spec.Diagnostic{
					Path:     fmt.Sprintf("%s:%d", path, lineNo),
					Code:     "PACKAGE_CLI_EXAMPLE_INVALID",
					Severity: spec.SeverityError,
					Message:  err.Error(),
				})
			}
		}
		if err := scanner.Err(); err != nil {
			diagnostics = append(diagnostics, spec.Diagnostic{Path: path, Code: "PACKAGE_DOC_READ_FAILED", Severity: spec.SeverityError, Message: err.Error()})
		}
		return nil
	})
	return examples, diagnostics
}

func validateCLIExample(command string) error {
	fields := strings.Fields(command)
	if len(fields) < 3 {
		return errors.New("Skiff CLI examples must include a command group and subcommand")
	}
	if fields[0] != "skiff" {
		return errors.New("command must start with skiff")
	}
	switch fields[1] {
	case "pkg", "ops", "deploy", "doctor", "status", "saga", "stateful":
		return nil
	default:
		return fmt.Errorf("unsupported Skiff command group %q in package docs", fields[1])
	}
}

func conformanceProvenance(entry LockEntry) schema.PackageProvenance {
	lock := LockFile{Schema: schema.PackageLockSchemaVersion, Packages: []LockEntry{entry}}
	body, _ := CanonicalLockJSON(lock)
	sum := sha256.Sum256(body)
	return schema.PackageProvenance{
		Name:           entry.Name,
		Ref:            entry.Ref,
		Version:        entry.Version,
		Digest:         entry.Digest,
		ManifestDigest: entry.ManifestDigest,
		LockfileDigest: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

func passedCheck(id, summary string, details json.RawMessage) ConformanceCheck {
	return ConformanceCheck{ID: id, Status: ConformancePassed, Summary: summary, Details: details}
}

func failedCheck(id, summary string, diagnostics ...spec.Diagnostic) ConformanceCheck {
	return ConformanceCheck{ID: id, Status: ConformanceFailed, Summary: summary, Diagnostics: diagnostics}
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
