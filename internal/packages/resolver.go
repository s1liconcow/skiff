package packages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

const manifestFileName = "skiff-package.json"

type ResolveOptions struct {
	Cache              Cache
	Clock              func() time.Time
	ExpectedDigest     string
	SignatureRef       string
	AllowUnsignedLocal bool
	RegistryDir        string
	RefOverride        string
}

type ResolvedPackage struct {
	Manifest       Manifest   `json:"manifest"`
	Entry          LockEntry  `json:"entry"`
	Directory      string     `json:"directory,omitempty"`
	ManifestPath   string     `json:"manifest_path,omitempty"`
	Cache          CacheEntry `json:"cache,omitempty"`
	Local          bool       `json:"local,omitempty"`
	SignatureFound bool       `json:"signature_found,omitempty"`
}

func Resolve(ctx context.Context, ref string, opts ResolveOptions) (*ResolvedPackage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, packageError("PACKAGE_REF_REQUIRED", "package ref is required", nil)
	}
	if opts.Cache.Root == "" {
		opts.Cache.Root = DefaultCacheRoot()
	}
	switch {
	case strings.HasPrefix(ref, "file://"):
		return resolveLocal(ctx, ref, opts)
	case strings.HasPrefix(ref, "oci://"):
		return resolveOCI(ctx, ref, opts)
	case strings.HasPrefix(ref, "skiff.dev/"):
		if opts.RegistryDir == "" {
			return nil, packageError("PACKAGE_REGISTRY_REQUIRED", "skiff.dev package refs require --registry-dir until a remote registry is configured", nil)
		}
		name := strings.TrimPrefix(ref, "skiff.dev/")
		next := opts
		next.RefOverride = ref
		return resolveLocal(ctx, "file://"+filepath.Join(opts.RegistryDir, name), next)
	default:
		return nil, packageError("INVALID_PACKAGE_REF", "package refs must use file://, oci://, or skiff.dev/name", nil)
	}
}

func resolveLocal(ctx context.Context, ref string, opts ResolveOptions) (*ResolvedPackage, error) {
	dir, err := localRefPath(ref)
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(dir, manifestFileName)
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, packageError("PACKAGE_MANIFEST_NOT_FOUND", "package manifest skiff-package.json was not found", err)
		}
		return nil, err
	}
	manifest, err := DecodeManifest(body)
	if err != nil {
		return nil, err
	}
	if diagnostics := ValidateManifest(*manifest); len(diagnostics) > 0 {
		return nil, packageError("PACKAGE_MANIFEST_INVALID", diagnostics[0].Message, nil)
	}
	digest, err := directoryDigest(ctx, dir)
	if err != nil {
		return nil, err
	}
	manifestDigest := digestBytes(body)
	if err := verifyExpectedDigest(opts.ExpectedDigest, digest); err != nil {
		return nil, err
	}
	signatureRef, err := signatureRefForDirectory(dir, opts.SignatureRef)
	if err != nil {
		return nil, err
	}
	if signatureRef == "" && !opts.AllowUnsignedLocal {
		return nil, packageError("PACKAGE_SIGNATURE_REQUIRED", "package signature is required unless unsigned local packages are explicitly allowed", nil)
	}
	cacheEntry, err := opts.Cache.PutDirectory(ctx, digest, dir)
	if err != nil {
		return nil, err
	}
	sourceRef := ref
	if opts.RefOverride != "" {
		sourceRef = opts.RefOverride
	}
	return resolvedFromManifest(*manifest, LockEntry{
		Name:           manifest.Name,
		Ref:            sourceRef,
		Version:        manifest.Version,
		Digest:         digest,
		SignatureRef:   signatureRef,
		Source:         ref,
		ManifestDigest: manifestDigest,
		ResolvedAt:     resolvedAt(opts),
	}, dir, manifestPath, cacheEntry, true), nil
}

func resolveOCI(ctx context.Context, ref string, opts ResolveOptions) (*ResolvedPackage, error) {
	digest := digestFromOCIRef(ref)
	if digest == "" {
		return nil, packageError("PACKAGE_DIGEST_REQUIRED", "OCI package refs must be digest-pinned before they can be locked", nil)
	}
	if err := verifyExpectedDigest(opts.ExpectedDigest, digest); err != nil {
		return nil, err
	}
	cacheEntry, err := opts.Cache.Get(digest)
	if err != nil {
		return nil, err
	}
	actualDigest, err := directoryDigest(ctx, cacheEntry.Path)
	if err != nil {
		return nil, err
	}
	if err := verifyExpectedDigest(digest, actualDigest); err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(cacheEntry.Path, manifestFileName)
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	manifest, err := DecodeManifest(body)
	if err != nil {
		return nil, err
	}
	if diagnostics := ValidateManifest(*manifest); len(diagnostics) > 0 {
		return nil, packageError("PACKAGE_MANIFEST_INVALID", diagnostics[0].Message, nil)
	}
	signatureRef := opts.SignatureRef
	if signatureRef == "" {
		signatureRef, err = signatureRefForDirectory(cacheEntry.Path, "")
		if err != nil {
			return nil, err
		}
	}
	if signatureRef == "" {
		return nil, packageError("PACKAGE_SIGNATURE_REQUIRED", "OCI package lock entries require a signature reference", nil)
	}
	return resolvedFromManifest(*manifest, LockEntry{
		Name:           manifest.Name,
		Ref:            refWithoutDigest(ref),
		Version:        manifest.Version,
		Digest:         digest,
		SignatureRef:   signatureRef,
		Source:         ref,
		ManifestDigest: digestBytes(body),
		ResolvedAt:     resolvedAt(opts),
	}, cacheEntry.Path, manifestPath, cacheEntry, false), nil
}

func resolvedFromManifest(manifest Manifest, entry LockEntry, dir, manifestPath string, cacheEntry CacheEntry, local bool) *ResolvedPackage {
	return &ResolvedPackage{
		Manifest:       manifest,
		Entry:          entry,
		Directory:      dir,
		ManifestPath:   manifestPath,
		Cache:          cacheEntry,
		Local:          local,
		SignatureFound: entry.SignatureRef != "",
	}
}

func AddLockEntry(lock LockFile, entry LockEntry) (LockFile, error) {
	lock = normalizeLock(lock)
	for _, existing := range lock.Packages {
		if existing.Name == entry.Name {
			return LockFile{}, packageError("DUPLICATE_PACKAGE_LOCK_ENTRY", fmt.Sprintf("package %q is already locked; use pkg update to change it", entry.Name), nil)
		}
	}
	lock.Packages = append(lock.Packages, entry)
	sortLockEntries(lock.Packages)
	return lock, nil
}

func UpdateLockEntry(lock LockFile, entry LockEntry, name string) (LockFile, error) {
	lock = normalizeLock(lock)
	if name == "" {
		name = entry.Name
	}
	for i, existing := range lock.Packages {
		if existing.Name == name {
			entry.Name = existing.Name
			lock.Packages[i] = entry
			sortLockEntries(lock.Packages)
			return lock, nil
		}
	}
	return LockFile{}, packageError("PACKAGE_LOCK_ENTRY_MISSING", fmt.Sprintf("package %q is not locked", name), nil)
}

func ReadLockFile(path string) (LockFile, bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizeLock(LockFile{}), false, nil
		}
		return LockFile{}, false, err
	}
	lock, err := DecodeLock(body)
	if err != nil {
		return LockFile{}, true, err
	}
	return normalizeLock(*lock), true, nil
}

func ReadLockFileWithDigest(path string) (LockFile, string, bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return normalizeLock(LockFile{}), "", false, nil
		}
		return LockFile{}, "", false, err
	}
	lock, err := DecodeLock(body)
	if err != nil {
		return LockFile{}, "", true, err
	}
	return normalizeLock(*lock), DigestBytes(body), true, nil
}

func WriteLockFile(path string, lock LockFile) error {
	body, err := CanonicalLockJSON(normalizeLock(lock))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

func normalizeLock(lock LockFile) LockFile {
	if lock.Schema == "" {
		lock.Schema = schema.PackageLockSchemaVersion
	}
	if lock.Packages == nil {
		lock.Packages = []LockEntry{}
	}
	sortLockEntries(lock.Packages)
	return lock
}

func sortLockEntries(entries []LockEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Name == entries[j].Name {
			return entries[i].Ref < entries[j].Ref
		}
		return entries[i].Name < entries[j].Name
	})
}

func localRefPath(ref string) (string, error) {
	raw := strings.TrimPrefix(ref, "file://")
	if raw == "" {
		return "", packageError("INVALID_PACKAGE_REF", "file package refs must include a path", nil)
	}
	if strings.HasPrefix(raw, "localhost/") {
		raw = strings.TrimPrefix(raw, "localhost")
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	if !filepath.IsAbs(raw) {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return "", err
		}
		raw = abs
	}
	info, err := os.Stat(raw)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", packageError("PACKAGE_PATH_INVALID", "file package refs must point at a package directory", nil)
	}
	return raw, nil
}

func directoryDigest(ctx context.Context, dir string) (string, error) {
	var files []string
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".skiff" {
				return filepath.SkipDir
			}
			return nil
		}
		if isPackageSignatureFile(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, rel := range files {
		hash.Write([]byte(rel))
		hash.Write([]byte{0})
		file, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func DigestBytes(body []byte) string {
	return digestBytes(body)
}

func verifyExpectedDigest(expected, actual string) error {
	if strings.TrimSpace(expected) == "" {
		return nil
	}
	if expected != actual {
		return packageError("PACKAGE_DIGEST_MISMATCH", fmt.Sprintf("package digest mismatch: got %s, want %s", actual, expected), nil)
	}
	return nil
}

func digestFromOCIRef(ref string) string {
	if _, after, ok := strings.Cut(ref, "@"); ok && strings.HasPrefix(after, "sha256:") {
		return after
	}
	return ""
}

func refWithoutDigest(ref string) string {
	before, _, ok := strings.Cut(ref, "@")
	if ok {
		return before
	}
	return ref
}

func resolvedAt(opts ResolveOptions) string {
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return clock().UTC().Round(0).Format(time.RFC3339)
}
