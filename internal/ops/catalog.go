package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/internal/packages"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

type CatalogOptions struct {
	Lockfile string
	Cache    packages.Cache
}

type Catalog struct {
	Lockfile       string           `json:"lockfile,omitempty"`
	LockfileDigest string           `json:"lockfile_digest,omitempty"`
	Profiles       []CatalogProfile `json:"profiles"`
}

type CatalogProfile struct {
	Profile sagaapi.OperationProfile  `json:"profile"`
	Package *schema.PackageProvenance `json:"package,omitempty"`
}

func LoadCatalog(ctx context.Context, opts CatalogOptions) (*Catalog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Lockfile) == "" {
		opts.Lockfile = "skiff.lock.json"
	}
	if opts.Cache.Root == "" {
		opts.Cache.Root = packages.DefaultCacheRoot()
	}
	byName := make(map[string]CatalogProfile)
	for _, profile := range BuiltInProfiles() {
		byName[profile.Name] = CatalogProfile{Profile: profile}
	}

	lock, lockDigest, exists, err := readCatalogLock(opts.Lockfile)
	if err != nil {
		return nil, err
	}
	if exists {
		for _, entry := range lock.Packages {
			manifest, err := readCachedPackageManifest(opts.Cache, entry)
			if err != nil {
				return nil, err
			}
			provenance := schema.PackageProvenance{
				Name:           entry.Name,
				Ref:            entry.Ref,
				Version:        entry.Version,
				Digest:         entry.Digest,
				ManifestDigest: entry.ManifestDigest,
				LockfileDigest: lockDigest,
			}
			for _, name := range manifest.Exports.OperationProfiles {
				current, ok := byName[name]
				if !ok {
					continue
				}
				current.Package = &provenance
				byName[name] = current
			}
		}
	}

	out := &Catalog{Lockfile: opts.Lockfile, LockfileDigest: lockDigest, Profiles: make([]CatalogProfile, 0, len(byName))}
	for _, item := range byName {
		out.Profiles = append(out.Profiles, item)
	}
	sort.Slice(out.Profiles, func(i, j int) bool { return out.Profiles[i].Profile.Name < out.Profiles[j].Profile.Name })
	return out, nil
}

func (c *Catalog) List(targetKind string) []CatalogProfile {
	if strings.TrimSpace(targetKind) == "" {
		targetKind = "StatefulGroup"
	}
	out := make([]CatalogProfile, 0, len(c.Profiles))
	for _, item := range c.Profiles {
		if profileTargetsKind(item.Profile.TargetKinds, targetKind) {
			out = append(out, item)
		}
	}
	return out
}

func (c *Catalog) Resolve(name string) (CatalogProfile, error) {
	for _, item := range c.Profiles {
		if item.Profile.Name == name || string(item.Profile.Kind) == name {
			return item, nil
		}
	}
	return CatalogProfile{}, fmt.Errorf("operation profile %q is not available", name)
}

func readCatalogLock(path string) (packages.LockFile, string, bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return packages.LockFile{}, "", false, nil
		}
		return packages.LockFile{}, "", false, err
	}
	lock, err := packages.DecodeLock(body)
	if err != nil {
		return packages.LockFile{}, "", true, err
	}
	if diagnostics := packages.ValidateLock(*lock, packages.ValidationOptions{AllowUnsignedLocal: true}); len(diagnostics) > 0 {
		return packages.LockFile{}, "", true, fmt.Errorf("package lock validation failed: %s %s", diagnostics[0].Path, diagnostics[0].Message)
	}
	sum := sha256.Sum256(body)
	return *lock, "sha256:" + hex.EncodeToString(sum[:]), true, nil
}

func readCachedPackageManifest(cache packages.Cache, entry packages.LockEntry) (*packages.Manifest, error) {
	cached, err := cache.Get(entry.Digest)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(filepath.Join(cached.Path, "skiff-package.json"))
	if err != nil {
		return nil, err
	}
	manifest, err := packages.DecodeManifest(body)
	if err != nil {
		return nil, err
	}
	if diagnostics := packages.ValidateManifest(*manifest); len(diagnostics) > 0 {
		return nil, fmt.Errorf("package manifest validation failed: %s %s", diagnostics[0].Path, diagnostics[0].Message)
	}
	return manifest, nil
}
