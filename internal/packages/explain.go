package packages

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/s1liconcow/skiff/pkg/pluginapi"
)

type Explanation struct {
	Package          PackageSummary     `json:"package"`
	Exports          ManifestExports    `json:"exports,omitempty"`
	Plugin           *PluginExplanation `json:"plugin,omitempty"`
	Cache            CacheEntry         `json:"cache,omitempty"`
	Provenance       PackageProvenance  `json:"provenance"`
	RecommendedUsage []string           `json:"recommended_usage,omitempty"`
}

type PackageSummary struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Ref     string `json:"ref,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

type PackageProvenance struct {
	Source         string `json:"source,omitempty"`
	Digest         string `json:"digest,omitempty"`
	ManifestDigest string `json:"manifest_digest,omitempty"`
	SignatureRef   string `json:"signature_ref,omitempty"`
	ResolvedAt     string `json:"resolved_at,omitempty"`
}

type PluginExplanation struct {
	ManifestPath string                 `json:"manifest_path,omitempty"`
	Runtime      pluginapi.RuntimeSpec  `json:"runtime,omitempty"`
	Hooks        []pluginapi.Hook       `json:"hooks,omitempty"`
	Permissions  pluginapi.Permissions  `json:"permissions,omitempty"`
	Capabilities []pluginapi.Capability `json:"capabilities,omitempty"`
	Package      *pluginapi.PackageRef  `json:"package,omitempty"`
}

func ExplainResolved(resolved ResolvedPackage) (Explanation, error) {
	out := Explanation{
		Package: PackageSummary{
			Name:    resolved.Manifest.Name,
			Version: resolved.Manifest.Version,
			Ref:     resolved.Entry.Ref,
			Digest:  resolved.Entry.Digest,
		},
		Exports: resolved.Manifest.Exports,
		Cache:   resolved.Cache,
		Provenance: PackageProvenance{
			Source:         resolved.Entry.Source,
			Digest:         resolved.Entry.Digest,
			ManifestDigest: resolved.Entry.ManifestDigest,
			SignatureRef:   resolved.Entry.SignatureRef,
			ResolvedAt:     resolved.Entry.ResolvedAt,
		},
		RecommendedUsage: []string{"skiff pkg add " + resolved.Entry.Ref, "skiff pkg verify " + resolved.Manifest.Name},
	}
	if resolved.Manifest.Plugin != nil {
		plugin, err := explainPackagePlugin(resolved.Directory, resolved.Manifest.Plugin.Manifest)
		if err != nil {
			return Explanation{}, err
		}
		out.Plugin = plugin
	}
	return out, nil
}

func explainPackagePlugin(dir, manifestRel string) (*PluginExplanation, error) {
	if manifestRel == "" {
		return nil, nil
	}
	path := filepath.Join(dir, filepath.FromSlash(manifestRel))
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, packageError("PACKAGE_PLUGIN_MANIFEST_NOT_FOUND", "package plugin manifest was not found", err)
		}
		return nil, err
	}
	var manifest pluginapi.Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, packageError("PACKAGE_PLUGIN_MANIFEST_INVALID", "package plugin manifest is invalid JSON", err)
	}
	return &PluginExplanation{
		ManifestPath: path,
		Runtime:      manifest.Runtime,
		Hooks:        append([]pluginapi.Hook(nil), manifest.Hooks...),
		Permissions:  manifest.Permissions,
		Capabilities: append([]pluginapi.Capability(nil), manifest.Capabilities...),
		Package:      manifest.Package,
	}, nil
}
