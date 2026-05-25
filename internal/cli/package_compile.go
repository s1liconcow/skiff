package cli

import (
	"flag"
	"fmt"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/packages"
	"github.com/s1liconcow/skiff/internal/spec"
)

type packageCompileFlags struct {
	lockfile           *string
	cacheRoot          *string
	environmentClass   *string
	allowUnsignedLocal *bool
}

func addPackageCompileFlags(fs *flag.FlagSet) packageCompileFlags {
	return packageCompileFlags{
		lockfile:           fs.String("lockfile", "skiff.lock.json", "package lockfile used when stack.dependencies is present"),
		cacheRoot:          fs.String("cache", packages.DefaultCacheRoot(), "content-addressed package cache used when stack.dependencies is present"),
		environmentClass:   fs.String("environment-class", "", "environment class for validation policy: production, staging, development, or sandbox"),
		allowUnsignedLocal: fs.Bool("allow-unsigned-local", false, "allow unsigned file:// packages for local development"),
	}
}

func compilerOptionsForDocument(doc spec.Document, flags packageCompileFlags, requireLock bool) (compiler.Options, error) {
	return compilerOptionsForDocumentWithConfig(doc, flags, requireLock, config.Config{})
}

func compilerOptionsForDocumentWithConfig(doc spec.Document, flags packageCompileFlags, requireLock bool, cfg config.Config) (compiler.Options, error) {
	opts := compiler.Options{}
	if flags.environmentClass != nil && *flags.environmentClass != "" {
		cfg.EnvironmentClass = *flags.environmentClass
		cfg.ReleasePolicy = nil
	}
	releasePolicy, err := config.EffectiveReleasePolicy(cfg)
	if err != nil {
		return opts, err
	}
	opts.ReleasePolicy = releasePolicy
	if flags.allowUnsignedLocal != nil {
		opts.AllowUnsignedLocalPackages = *flags.allowUnsignedLocal
	}
	if doc.Kind != spec.KindStack || doc.Stack == nil || len(doc.Stack.Dependencies) == 0 {
		return opts, nil
	}
	lockfile := "skiff.lock.json"
	if flags.lockfile != nil && *flags.lockfile != "" {
		lockfile = *flags.lockfile
	}
	cacheRoot := packages.DefaultCacheRoot()
	if flags.cacheRoot != nil && *flags.cacheRoot != "" {
		cacheRoot = *flags.cacheRoot
	}
	lock, lockDigest, exists, err := packages.ReadLockFileWithDigest(lockfile)
	if err != nil {
		return opts, fmt.Errorf("read package lockfile %s: %w", lockfile, err)
	}
	validationOpts := packages.ValidationOptions{
		AllowUnsignedLocal:       opts.AllowUnsignedLocalPackages || releasePolicy.AllowUnsignedCode,
		RequirePackageLock:       releasePolicy.RequireSignedReleases,
		RequireSignedLockEntries: releasePolicy.RequireSignedReleases,
	}
	if !exists {
		if diagnostics := packages.ValidateStackLock(doc, nil, validationOpts); len(diagnostics) > 0 {
			return opts, spec.ValidationError{Diagnostics: diagnostics}
		}
		if requireLock {
			return opts, fmt.Errorf("stack dependencies require %s and cached package manifests before compile, plan, explain, or deploy", lockfile)
		}
		return opts, nil
	}
	if diagnostics := packages.ValidateStackLock(doc, &lock, validationOpts); len(diagnostics) > 0 {
		return opts, spec.ValidationError{Diagnostics: diagnostics}
	}
	cache := packages.Cache{Root: cacheRoot}
	manifests := map[string]packages.Manifest{}
	for _, dependency := range doc.Stack.Dependencies {
		entry, ok := packages.FindLockEntryForDependency(lock, dependency)
		if !ok {
			return opts, fmt.Errorf("dependency %q has no matching package lock entry", dependency.Name)
		}
		manifest, _, err := cache.ReadManifest(entry)
		if err != nil {
			return opts, fmt.Errorf("read cached package manifest for dependency %q: %w", dependency.Name, err)
		}
		for _, key := range []string{dependency.Name, entry.Name, entry.Ref, entry.Source, entry.Digest} {
			if key != "" {
				manifests[key] = *manifest
			}
		}
	}
	opts.PackageLock = &lock
	opts.PackageLockDigest = lockDigest
	opts.PackageManifests = manifests
	return opts, nil
}
