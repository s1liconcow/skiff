package packages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Manifest struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	Exports    ManifestExports `json:"exports,omitempty"`
	Plugin     *PluginRef      `json:"plugin,omitempty"`
}

type ManifestExports struct {
	Dependencies          []string `json:"dependencies,omitempty"`
	OperationProfiles     []string `json:"operation_profiles,omitempty"`
	ManagedOperations     []string `json:"managed_operations,omitempty"`
	SelfManagedOperations []string `json:"self_managed_operations,omitempty"`
	PackageSteps          []string `json:"package_steps,omitempty"`
	DoctorChecks          []string `json:"doctor_checks,omitempty"`
}

type PluginRef struct {
	Manifest string `json:"manifest"`
}

type LockFile struct {
	Schema   string      `json:"schema"`
	Packages []LockEntry `json:"packages"`
}

type LockEntry struct {
	Name           string `json:"name"`
	Ref            string `json:"ref"`
	Version        string `json:"version"`
	Digest         string `json:"digest"`
	SignatureRef   string `json:"signature_ref,omitempty"`
	Source         string `json:"source"`
	ManifestDigest string `json:"manifest_digest"`
	ResolvedAt     string `json:"resolved_at"`
}

type ValidationOptions struct {
	AllowUnsignedLocal       bool
	RequirePackageLock       bool
	RequireSignedLockEntries bool
}

var skiffNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var exactSemverPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
var packageStepPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func DecodeManifest(body []byte) (*Manifest, error) {
	var manifest Manifest
	if err := decodeStrict(body, &manifest, "package manifest"); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func DecodeLock(body []byte) (*LockFile, error) {
	var lock LockFile
	if err := decodeStrict(body, &lock, "package lockfile"); err != nil {
		return nil, err
	}
	return &lock, nil
}

func CanonicalLockJSON(lock LockFile) ([]byte, error) {
	return canonical.Marshal(lock)
}

func ValidateManifest(manifest Manifest) []spec.Diagnostic {
	var diagnostics []spec.Diagnostic
	add := diagnosticAppender(&diagnostics)
	if manifest.APIVersion != schema.PackageManifestAPIVersion {
		add("$.apiVersion", "UNSUPPORTED_PACKAGE_API_VERSION", "package apiVersion must be skiff.dev/package/v1alpha1")
	}
	if manifest.Kind != schema.PackageManifestKind {
		add("$.kind", "UNSUPPORTED_PACKAGE_KIND", "package kind must be Package")
	}
	validateName(add, "$.name", manifest.Name, "package name must be a DNS-style Skiff package name")
	if !exactSemverPattern.MatchString(strings.TrimSpace(manifest.Version)) {
		add("$.version", "INVALID_PACKAGE_VERSION", "package version must be an exact semantic version such as 1.2.0")
	}
	validateExportList(add, "$.exports.dependencies", manifest.Exports.Dependencies, validateNameValue)
	validateExportList(add, "$.exports.operation_profiles", manifest.Exports.OperationProfiles, validateStepKindValue)
	validateExportList(add, "$.exports.managed_operations", manifest.Exports.ManagedOperations, validateStepKindValue)
	validateExportList(add, "$.exports.self_managed_operations", manifest.Exports.SelfManagedOperations, validateStepKindValue)
	validateExportList(add, "$.exports.package_steps", manifest.Exports.PackageSteps, validateStepKindValue)
	validateExportList(add, "$.exports.doctor_checks", manifest.Exports.DoctorChecks, validateStepKindValue)
	if manifest.Plugin != nil {
		value := strings.TrimSpace(manifest.Plugin.Manifest)
		if value == "" {
			add("$.plugin.manifest", "REQUIRED", "plugin manifest path is required when plugin is set")
		} else if strings.HasPrefix(value, "/") || strings.Contains(value, "://") {
			add("$.plugin.manifest", "INVALID_PLUGIN_MANIFEST", "plugin manifest must be a relative package path such as plugin.json")
		}
	}
	return diagnostics
}

func ValidateLock(lock LockFile, opts ValidationOptions) []spec.Diagnostic {
	var diagnostics []spec.Diagnostic
	add := diagnosticAppender(&diagnostics)
	if lock.Schema != schema.PackageLockSchemaVersion {
		add("$.schema", "UNSUPPORTED_PACKAGE_LOCK_SCHEMA", "package lock schema must be skiff.lock/v1alpha1")
	}
	seen := map[string]struct{}{}
	for i, entry := range lock.Packages {
		base := fmt.Sprintf("$.packages[%d]", i)
		validateName(add, base+".name", entry.Name, "locked package name must be a DNS-style Skiff name")
		if entry.Name != "" {
			if _, ok := seen[entry.Name]; ok {
				add(base+".name", "DUPLICATE_PACKAGE_LOCK_ENTRY", "package lock entries must have unique names")
			}
			seen[entry.Name] = struct{}{}
		}
		validatePackageRef(add, base+".ref", entry.Ref)
		if !exactSemverPattern.MatchString(strings.TrimSpace(entry.Version)) {
			add(base+".version", "INVALID_LOCKED_VERSION", "locked package version must be an exact semantic version")
		}
		validateSHA256(add, base+".digest", entry.Digest, "locked package digest must be sha256:<64 hex chars>")
		validateSource(add, base+".source", entry.Source)
		validateSHA256(add, base+".manifest_digest", entry.ManifestDigest, "locked package manifest digest must be sha256:<64 hex chars>")
		if strings.TrimSpace(entry.ResolvedAt) == "" {
			add(base+".resolved_at", "REQUIRED", "lock entry resolved_at timestamp is required")
		} else if _, err := time.Parse(time.RFC3339, entry.ResolvedAt); err != nil {
			add(base+".resolved_at", "INVALID_TIMESTAMP", "lock entry resolved_at must be an RFC3339 timestamp")
		}
		if strings.TrimSpace(entry.SignatureRef) == "" {
			if !(opts.AllowUnsignedLocal && isLocalPackageRef(entry.Ref) && isLocalPackageRef(entry.Source)) {
				add(base+".signature_ref", "PACKAGE_SIGNATURE_REQUIRED", "lock entry signature_ref is required unless unsigned local packages are explicitly allowed")
			}
		}
	}
	return diagnostics
}

func ValidateStackLock(doc spec.Document, lock *LockFile, opts ValidationOptions) []spec.Diagnostic {
	if doc.Stack == nil || len(doc.Stack.Dependencies) == 0 {
		return nil
	}
	if lock == nil {
		if !opts.RequirePackageLock {
			return nil
		}
		return []spec.Diagnostic{{
			Path:     "$.stack.dependencies",
			Code:     "PACKAGE_LOCK_REQUIRED",
			Severity: spec.SeverityError,
			Message:  "this environment class requires skiff.lock.json with digest-pinned signed entries",
		}}
	}
	validationOpts := opts
	if opts.RequireSignedLockEntries {
		validationOpts.AllowUnsignedLocal = false
	}
	diagnostics := ValidateLock(*lock, validationOpts)
	add := diagnosticAppender(&diagnostics)
	for i, dependency := range doc.Stack.Dependencies {
		base := fmt.Sprintf("$.stack.dependencies[%d]", i)
		entry, ok := FindLockEntryForDependency(*lock, dependency)
		if !ok {
			add(base+".name", "PACKAGE_LOCK_ENTRY_MISSING", "dependency has no matching skiff.lock.json entry")
			continue
		}
		if !lockEntryMatchesDependency(entry, dependency) {
			add(base+".uses", "PACKAGE_LOCK_REF_MISMATCH", "dependency uses does not match locked package ref")
		}
	}
	return diagnostics
}

func FindLockEntryForDependency(lock LockFile, dependency spec.StackDependency) (LockEntry, bool) {
	dependencyName := strings.TrimSpace(dependency.Name)
	for _, entry := range lock.Packages {
		if dependencyName != "" && strings.TrimSpace(entry.Name) == dependencyName {
			return entry, true
		}
	}
	for _, entry := range lock.Packages {
		if lockEntryMatchesDependency(entry, dependency) {
			return entry, true
		}
	}
	return LockEntry{}, false
}

func lockEntryMatchesDependency(entry LockEntry, dependency spec.StackDependency) bool {
	uses := strings.TrimSpace(dependency.Uses)
	if uses == "" {
		return false
	}
	return strings.TrimSpace(entry.Ref) == uses || strings.TrimSpace(entry.Source) == uses
}

func decodeStrict(body []byte, dst any, label string) error {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("decode %s: multiple JSON documents are not supported", label)
	}
	return nil
}

func diagnosticAppender(diagnostics *[]spec.Diagnostic) func(path, code, message string) {
	return func(path, code, message string) {
		*diagnostics = append(*diagnostics, spec.Diagnostic{
			Path:     path,
			Code:     code,
			Severity: spec.SeverityError,
			Message:  message,
		})
	}
}

func validateExportList(add func(path, code, message string), path string, values []string, validate func(string) bool) {
	seen := map[string]struct{}{}
	for i, value := range values {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		value = strings.TrimSpace(value)
		if !validate(value) {
			add(itemPath, "INVALID_PACKAGE_EXPORT", "package exports must use DNS-style names or step kinds")
			continue
		}
		if _, ok := seen[value]; ok {
			add(itemPath, "DUPLICATE_PACKAGE_EXPORT", "package exports must be unique")
		}
		seen[value] = struct{}{}
	}
}

func validateName(add func(path, code, message string), path, value, message string) {
	value = strings.TrimSpace(value)
	if value == "" {
		add(path, "REQUIRED", message)
		return
	}
	if len(value) > 63 || !skiffNamePattern.MatchString(value) {
		add(path, "INVALID_NAME", message)
	}
}

func validateNameValue(value string) bool {
	return value != "" && len(value) <= 63 && skiffNamePattern.MatchString(value)
}

func validateStepKindValue(value string) bool {
	return value != "" && len(value) <= 128 && packageStepPattern.MatchString(value)
}

func validatePackageRef(add func(path, code, message string), path, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		add(path, "REQUIRED", "package ref is required")
		return
	}
	switch {
	case strings.HasPrefix(value, "skiff.dev/"):
		if !validPackagePath(strings.TrimPrefix(value, "skiff.dev/")) {
			add(path, "INVALID_PACKAGE_REF", "skiff.dev package refs must look like skiff.dev/name")
		}
	case strings.HasPrefix(value, "oci://"):
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
			add(path, "INVALID_PACKAGE_REF", "OCI package refs must include registry and repository path")
		}
	case strings.HasPrefix(value, "file://"):
		if strings.TrimSpace(strings.TrimPrefix(value, "file://")) == "" {
			add(path, "INVALID_PACKAGE_REF", "file package refs must include a local package path")
		}
	default:
		add(path, "INVALID_PACKAGE_REF", "package refs must use skiff.dev/name, oci://registry/repo/name:version, or file://../local-package")
	}
}

func validateSource(add func(path, code, message string), path, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		add(path, "REQUIRED", "package source is required")
		return
	}
	if strings.HasPrefix(value, "oci://") || strings.HasPrefix(value, "file://") {
		validatePackageRef(add, path, value)
		return
	}
	add(path, "INVALID_PACKAGE_SOURCE", "package source must use oci:// or file://")
}

func validateSHA256(add func(path, code, message string), path, value, message string) {
	value = strings.TrimSpace(value)
	if value == "" {
		add(path, "REQUIRED", message)
		return
	}
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		add(path, "INVALID_DIGEST", message)
		return
	}
	for _, r := range value[len("sha256:"):] {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' {
			continue
		}
		add(path, "INVALID_DIGEST", message)
		return
	}
}

func validPackagePath(value string) bool {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) == 0 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if !validateNameValue(part) {
			return false
		}
	}
	return true
}

func isLocalPackageRef(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "file://")
}
