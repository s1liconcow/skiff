package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

const (
	defaultManifestFile = "skiff-plugin.json"
	legacyManifestFile  = "plugin.json"
)

type ManifestError struct {
	Path        string                 `json:"path,omitempty"`
	Diagnostics []pluginapi.Diagnostic `json:"diagnostics"`
}

func (e ManifestError) Error() string {
	if len(e.Diagnostics) == 0 {
		if e.Path != "" {
			return "plugin manifest " + e.Path + " is invalid"
		}
		return "plugin manifest is invalid"
	}
	var parts []string
	for _, diagnostic := range e.Diagnostics {
		if diagnostic.Field != "" {
			parts = append(parts, diagnostic.Field+": "+diagnostic.Summary)
			continue
		}
		parts = append(parts, diagnostic.Summary)
	}
	prefix := "plugin manifest"
	if e.Path != "" {
		prefix += " " + e.Path
	}
	return prefix + " is invalid: " + strings.Join(parts, "; ")
}

func LoadManifest(path string) (pluginapi.Manifest, error) {
	resolved, err := resolveManifestPath(path)
	if err != nil {
		return pluginapi.Manifest{}, err
	}
	body, err := os.ReadFile(resolved)
	if err != nil {
		return pluginapi.Manifest{}, fmt.Errorf("read plugin manifest %q: %w", resolved, err)
	}
	manifest, err := DecodeManifest(body)
	if err != nil {
		return pluginapi.Manifest{}, fmt.Errorf("%s: %w", resolved, err)
	}
	if diagnostics := ValidateManifest(manifest); len(diagnostics) > 0 {
		return pluginapi.Manifest{}, ManifestError{Path: resolved, Diagnostics: diagnostics}
	}
	return manifest, nil
}

func DecodeManifest(body []byte) (pluginapi.Manifest, error) {
	var manifest pluginapi.Manifest
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return pluginapi.Manifest{}, fmt.Errorf("decode JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return pluginapi.Manifest{}, fmt.Errorf("multiple JSON documents are not supported")
	}
	return manifest, nil
}

func ValidateManifest(manifest pluginapi.Manifest) []pluginapi.Diagnostic {
	var diagnostics []pluginapi.Diagnostic
	add := func(code, field, summary string) {
		diagnostics = append(diagnostics, pluginapi.Diagnostic{
			Code:     code,
			Severity: "error",
			Field:    field,
			Summary:  summary,
		})
	}
	if manifest.APIVersion != pluginapi.APIVersion {
		add("PLUGIN_API_VERSION_UNSUPPORTED", "apiVersion", fmt.Sprintf("apiVersion must be %q", pluginapi.APIVersion))
	}
	if manifest.Kind != pluginapi.KindPlugin {
		add("PLUGIN_KIND_UNSUPPORTED", "kind", fmt.Sprintf("kind must be %q", pluginapi.KindPlugin))
	}
	if strings.TrimSpace(manifest.Name) == "" {
		add("PLUGIN_NAME_REQUIRED", "name", "name is required")
	} else if !validName(manifest.Name) {
		add("PLUGIN_NAME_INVALID", "name", "name must contain only lowercase letters, digits, and hyphens")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		add("PLUGIN_VERSION_REQUIRED", "version", "version is required")
	}
	if len(manifest.Hooks) == 0 {
		add("PLUGIN_HOOKS_REQUIRED", "hooks", "at least one hook is required")
	}
	seenHooks := map[pluginapi.Hook]struct{}{}
	for i, hook := range manifest.Hooks {
		field := fmt.Sprintf("hooks[%d]", i)
		if !knownHook(hook) {
			add("PLUGIN_HOOK_UNKNOWN", field, fmt.Sprintf("unknown hook %q", hook))
			continue
		}
		if _, ok := seenHooks[hook]; ok {
			add("PLUGIN_HOOK_DUPLICATE", field, fmt.Sprintf("duplicate hook %q", hook))
		}
		seenHooks[hook] = struct{}{}
	}
	if manifest.Runtime.Kind == "" {
		manifest.Runtime.Kind = pluginapi.RuntimeManifestOnly
	}
	switch manifest.Runtime.Kind {
	case pluginapi.RuntimeManifestOnly:
	case pluginapi.RuntimeCommand:
		if len(manifest.Runtime.Command) == 0 {
			add("PLUGIN_RUNTIME_COMMAND_REQUIRED", "runtime.command", "command runtime requires command argv")
		}
	case pluginapi.RuntimeGRPC:
		if manifest.Runtime.Endpoint == "" && len(manifest.Runtime.Command) == 0 {
			add("PLUGIN_RUNTIME_GRPC_TARGET_REQUIRED", "runtime", "grpc runtime requires endpoint or command")
		}
	default:
		add("PLUGIN_RUNTIME_KIND_UNSUPPORTED", "runtime.kind", "runtime kind must be manifest, command, or grpc")
	}
	if manifest.Package != nil {
		if manifest.Package.Ref == "" {
			add("PLUGIN_PACKAGE_REF_REQUIRED", "package.ref", "package ref is required when package is present")
		}
		if manifest.Package.Digest == "" {
			add("PLUGIN_PACKAGE_DIGEST_REQUIRED", "package.digest", "signed package references must include a digest")
		}
		if manifest.Package.SignatureRef == "" {
			add("PLUGIN_PACKAGE_SIGNATURE_REQUIRED", "package.signature_ref", "signed package references must include a signature reference")
		}
	}
	seenCapabilities := map[string]struct{}{}
	for i, capability := range manifest.Capabilities {
		field := fmt.Sprintf("capabilities[%d]", i)
		if capability.Name == "" {
			add("PLUGIN_CAPABILITY_NAME_REQUIRED", field+".name", "capability name is required")
		} else {
			key := string(capability.Kind) + "/" + capability.Name
			if _, ok := seenCapabilities[key]; ok {
				add("PLUGIN_CAPABILITY_DUPLICATE", field+".name", "capability names must be unique per kind")
			}
			seenCapabilities[key] = struct{}{}
		}
		switch capability.Kind {
		case pluginapi.CapabilityIRPatch:
			if len(capability.PatchKinds) == 0 {
				add("PLUGIN_CAPABILITY_PATCH_KINDS_REQUIRED", field+".patch_kinds", "IR patch capabilities must list patch kinds")
			}
		case pluginapi.CapabilityRuntimeAddon:
			if len(capability.RuntimeAddons) == 0 {
				add("PLUGIN_CAPABILITY_RUNTIME_ADDONS_REQUIRED", field+".runtime_addons", "runtime addon capabilities must list addon kinds")
			}
		case pluginapi.CapabilityDoctorCheck:
			if len(capability.DoctorChecks) == 0 {
				add("PLUGIN_CAPABILITY_DOCTOR_CHECKS_REQUIRED", field+".doctor_checks", "doctor check capabilities must list check kinds")
			}
		case pluginapi.CapabilitySagaStep:
			if len(capability.SagaStepKinds) == 0 {
				add("PLUGIN_CAPABILITY_SAGA_STEPS_REQUIRED", field+".saga_step_kinds", "saga step capabilities must list step kinds")
			}
		case pluginapi.CapabilityPackageStep:
			if len(capability.PackageSteps) == 0 {
				add("PLUGIN_CAPABILITY_PACKAGE_STEPS_REQUIRED", field+".package_steps", "package step capabilities must list typed package steps")
			}
			for j, step := range capability.PackageSteps {
				stepField := fmt.Sprintf("%s.package_steps[%d]", field, j)
				if !validCapabilityName(step.Kind) {
					add("PLUGIN_CAPABILITY_PACKAGE_STEP_KIND_INVALID", stepField+".kind", "package step kind is required and must use lowercase letters, digits, dots, underscores, or hyphens")
				}
				if step.Risk != "" && !validRisk(string(step.Risk)) {
					add("PLUGIN_CAPABILITY_PACKAGE_STEP_RISK_INVALID", stepField+".risk", "package step risk must be low, medium, high, or critical")
				}
				if step.Reversibility != "" && !validReversibility(string(step.Reversibility)) {
					add("PLUGIN_CAPABILITY_PACKAGE_STEP_REVERSIBILITY_INVALID", stepField+".reversibility", "package step reversibility must be reversible, compensatable, partially_reversible, or irreversible")
				}
			}
		default:
			add("PLUGIN_CAPABILITY_KIND_UNKNOWN", field+".kind", "capability kind must be ir_patch, runtime_addon, doctor_check, saga_step, or package_step")
		}
	}
	if hookSet(manifest.Hooks)[pluginapi.HookRuntimeAddons] && !manifest.Permissions.RuntimeAddons {
		add("PLUGIN_PERMISSION_RUNTIME_ADDONS_REQUIRED", "permissions.runtime_addons", "runtime_addons hook requires runtime_addons permission")
	}
	if hookSet(manifest.Hooks)[pluginapi.HookDoctorChecks] && !manifest.Permissions.DoctorChecks {
		add("PLUGIN_PERMISSION_DOCTOR_CHECKS_REQUIRED", "permissions.doctor_checks", "doctor_checks hook requires doctor_checks permission")
	}
	if hookSet(manifest.Hooks)[pluginapi.HookSagaStep] && len(manifest.Permissions.SagaStepKinds) == 0 {
		add("PLUGIN_PERMISSION_SAGA_STEPS_REQUIRED", "permissions.saga_step_kinds", "saga_step hook requires allowed saga step kinds")
	}
	if hookSet(manifest.Hooks)[pluginapi.HookPackageStep] && len(manifest.Permissions.PackageStepKinds) == 0 {
		add("PLUGIN_PERMISSION_PACKAGE_STEPS_REQUIRED", "permissions.package_step_kinds", "package_step hook requires allowed package step kinds")
	}
	return diagnostics
}

func resolveManifestPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("plugin path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return path, nil
	}
	candidates := []string{
		filepath.Join(path, defaultManifestFile),
		filepath.Join(path, legacyManifestFile),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("plugin directory %q does not contain %s or %s", path, defaultManifestFile, legacyManifestFile)
}

func knownHook(hook pluginapi.Hook) bool {
	switch hook {
	case pluginapi.HookValidate, pluginapi.HookMutateIR, pluginapi.HookRuntimeAddons, pluginapi.HookDoctorChecks, pluginapi.HookSagaStep, pluginapi.HookPackageStep:
		return true
	default:
		return false
	}
}

func validCapabilityName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validRisk(value string) bool {
	switch value {
	case "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

func validReversibility(value string) bool {
	switch value {
	case "reversible", "compensatable", "partially_reversible", "irreversible":
		return true
	default:
		return false
	}
}

func hookSet(hooks []pluginapi.Hook) map[pluginapi.Hook]bool {
	out := make(map[pluginapi.Hook]bool, len(hooks))
	for _, hook := range hooks {
		out[hook] = true
	}
	return out
}

func hasHook(manifest pluginapi.Manifest, hook pluginapi.Hook) bool {
	for _, item := range manifest.Hooks {
		if item == hook {
			return true
		}
	}
	return false
}

func validName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return false
	}
	return !strings.HasPrefix(value, "-") && !strings.HasSuffix(value, "-")
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
