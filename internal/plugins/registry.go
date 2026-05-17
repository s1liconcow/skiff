package plugins

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

type SourceKind string

const (
	SourcePath       SourceKind = "path"
	SourcePackageRef SourceKind = "package_ref"
)

type Source struct {
	Kind         SourceKind `json:"kind"`
	Path         string     `json:"path,omitempty"`
	PackageRef   string     `json:"package_ref,omitempty"`
	Digest       string     `json:"digest,omitempty"`
	SignatureRef string     `json:"signature_ref,omitempty"`
}

type Plugin struct {
	Manifest    pluginapi.Manifest     `json:"manifest"`
	Source      Source                 `json:"source"`
	Diagnostics []pluginapi.Diagnostic `json:"diagnostics,omitempty"`
}

type Registry struct {
	Plugins []Plugin `json:"plugins"`
}

type RegistryOptions struct {
	Paths       []string
	PackageRefs []PackageSource
}

type PackageSource struct {
	Ref          string
	Digest       string
	SignatureRef string
}

func LoadRegistry(ctx context.Context, opts RegistryOptions) (*Registry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	registry := &Registry{}
	for _, path := range opts.Paths {
		manifest, err := LoadManifest(path)
		if err != nil {
			return nil, err
		}
		resolved, err := resolveManifestPath(path)
		if err != nil {
			return nil, err
		}
		registry.Plugins = append(registry.Plugins, Plugin{
			Manifest: manifest,
			Source: Source{
				Kind: SourcePath,
				Path: resolved,
			},
		})
	}
	for _, source := range opts.PackageRefs {
		if diagnostics := validatePackageSource(source); len(diagnostics) > 0 {
			return nil, ManifestError{Path: source.Ref, Diagnostics: diagnostics}
		}
		manifest := pluginapi.Manifest{
			APIVersion: pluginapi.APIVersion,
			Kind:       pluginapi.KindPlugin,
			Name:       packageName(source.Ref),
			Version:    "unresolved",
			Runtime:    pluginapi.RuntimeSpec{Kind: pluginapi.RuntimeManifestOnly},
			Package: &pluginapi.PackageRef{
				Ref:          source.Ref,
				Digest:       source.Digest,
				SignatureRef: source.SignatureRef,
			},
		}
		registry.Plugins = append(registry.Plugins, Plugin{
			Manifest: manifest,
			Source: Source{
				Kind:         SourcePackageRef,
				PackageRef:   source.Ref,
				Digest:       source.Digest,
				SignatureRef: source.SignatureRef,
			},
		})
	}
	if err := registry.validateUniqueNames(); err != nil {
		return nil, err
	}
	sort.Slice(registry.Plugins, func(i, j int) bool {
		left, right := registry.Plugins[i], registry.Plugins[j]
		if left.Manifest.Name == right.Manifest.Name {
			return left.Manifest.Version < right.Manifest.Version
		}
		return left.Manifest.Name < right.Manifest.Name
	})
	return registry, nil
}

func validatePackageSource(source PackageSource) []pluginapi.Diagnostic {
	var diagnostics []pluginapi.Diagnostic
	add := func(code, field, summary string) {
		diagnostics = append(diagnostics, pluginapi.Diagnostic{Code: code, Severity: "error", Field: field, Summary: summary})
	}
	if strings.TrimSpace(source.Ref) == "" {
		add("PLUGIN_PACKAGE_REF_REQUIRED", "package.ref", "package ref is required")
	}
	if strings.TrimSpace(source.Digest) == "" {
		add("PLUGIN_PACKAGE_DIGEST_REQUIRED", "package.digest", "signed package references must include a digest")
	}
	if strings.TrimSpace(source.SignatureRef) == "" {
		add("PLUGIN_PACKAGE_SIGNATURE_REQUIRED", "package.signature_ref", "signed package references must include a signature reference")
	}
	if packageName(source.Ref) == "" {
		add("PLUGIN_PACKAGE_NAME_INVALID", "package.ref", "package ref must contain a usable plugin name")
	}
	return diagnostics
}

func (r *Registry) validateUniqueNames() error {
	seen := make(map[string]string)
	for _, plugin := range r.Plugins {
		key := plugin.Manifest.Name + "@" + plugin.Manifest.Version
		if previous := seen[key]; previous != "" {
			return fmt.Errorf("plugin %s loaded more than once from %s and %s", key, previous, plugin.Source.label())
		}
		seen[key] = plugin.Source.label()
	}
	return nil
}

func (s Source) label() string {
	switch s.Kind {
	case SourcePackageRef:
		return s.PackageRef
	default:
		return s.Path
	}
}

func (r *Registry) Hooks(hook pluginapi.Hook) []Plugin {
	if r == nil {
		return nil
	}
	var out []Plugin
	for _, plugin := range r.Plugins {
		if hasHook(plugin.Manifest, hook) {
			out = append(out, plugin)
		}
	}
	return out
}

func (p Plugin) CapabilityNames(kind pluginapi.CapabilityKind) []string {
	var out []string
	for _, capability := range p.Manifest.Capabilities {
		if capability.Kind == kind {
			out = append(out, capability.Name)
		}
	}
	return sortedStrings(out)
}

func packageName(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimSuffix(ref, "/")
	if ref == "" {
		return ""
	}
	if idx := strings.Index(ref, "://"); idx >= 0 {
		ref = ref[idx+3:]
	}
	if before, _, ok := strings.Cut(ref, "@"); ok {
		ref = before
	}
	if idx := strings.LastIndex(ref, "/"); idx >= 0 && idx+1 < len(ref) {
		ref = ref[idx+1:]
	}
	if before, _, ok := strings.Cut(ref, ":"); ok {
		ref = before
	}
	ref = strings.ToLower(ref)
	ref = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, ref)
	ref = strings.Trim(ref, "-")
	return ref
}
