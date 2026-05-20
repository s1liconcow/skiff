package packages

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Cache struct {
	Root string
}

type CacheEntry struct {
	Digest string `json:"digest"`
	Path   string `json:"path"`
	Reused bool   `json:"reused,omitempty"`
}

func DefaultCacheRoot() string {
	if value := strings.TrimSpace(os.Getenv("SKIFF_PACKAGE_CACHE")); value != "" {
		return value
	}
	return filepath.Join(".skiff", "packages", "cache")
}

func (c Cache) PackageDir(digest string) (string, error) {
	if digest == "" {
		return "", packageError("PACKAGE_DIGEST_REQUIRED", "package digest is required", nil)
	}
	if c.Root == "" {
		c.Root = DefaultCacheRoot()
	}
	escaped := strings.ReplaceAll(digest, ":", "-")
	return filepath.Join(c.Root, escaped), nil
}

func (c Cache) PutDirectory(ctx context.Context, digest, sourceDir string) (CacheEntry, error) {
	if err := ctx.Err(); err != nil {
		return CacheEntry{}, err
	}
	dst, err := c.PackageDir(digest)
	if err != nil {
		return CacheEntry{}, err
	}
	if _, err := os.Stat(filepath.Join(dst, manifestFileName)); err == nil {
		return CacheEntry{Digest: digest, Path: dst, Reused: true}, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return CacheEntry{}, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return CacheEntry{}, err
	}
	if err := copyDirectory(ctx, sourceDir, dst); err != nil {
		return CacheEntry{}, err
	}
	return CacheEntry{Digest: digest, Path: dst}, nil
}

func (c Cache) Get(digest string) (CacheEntry, error) {
	dir, err := c.PackageDir(digest)
	if err != nil {
		return CacheEntry{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, manifestFileName)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CacheEntry{}, packageError("PACKAGE_CACHE_MISS", "package digest is not present in local cache", err)
		}
		return CacheEntry{}, err
	}
	return CacheEntry{Digest: digest, Path: dir, Reused: true}, nil
}

func (c Cache) ReadManifest(entry LockEntry) (*Manifest, CacheEntry, error) {
	cached, err := c.Get(entry.Digest)
	if err != nil {
		return nil, CacheEntry{}, err
	}
	body, err := os.ReadFile(filepath.Join(cached.Path, manifestFileName))
	if err != nil {
		return nil, CacheEntry{}, err
	}
	if entry.ManifestDigest != "" && DigestBytes(body) != entry.ManifestDigest {
		return nil, CacheEntry{}, packageError("PACKAGE_MANIFEST_DIGEST_MISMATCH", "cached package manifest digest does not match skiff.lock.json", nil)
	}
	manifest, err := DecodeManifest(body)
	if err != nil {
		return nil, CacheEntry{}, err
	}
	if diagnostics := ValidateManifest(*manifest); len(diagnostics) > 0 {
		return nil, CacheEntry{}, packageError("PACKAGE_MANIFEST_INVALID", diagnostics[0].Message, nil)
	}
	return manifest, cached, nil
}

func copyDirectory(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".skiff" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(path, filepath.Join(dst, rel), info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
