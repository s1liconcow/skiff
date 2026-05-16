package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
)

type Store struct {
	root string
}

func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("file object store root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve file object store root: %w", err)
	}
	return &Store{root: abs}, nil
}

func NewFromURI(value string) (*Store, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("parse file object store URI: %w", err)
	}
	if parsed.Scheme != "file" {
		return nil, fmt.Errorf("file object store URI must use file:// scheme")
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return nil, fmt.Errorf("file object store URI host %q is not supported", parsed.Host)
	}
	return New(parsed.Path)
}

func (s *Store) Get(ctx context.Context, key string) (*objstore.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return nil, objstore.WrapError("get", key, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, objstore.WrapError("get", key, objstore.ErrNotFound)
		}
		return nil, objstore.WrapError("get", key, err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		return nil, objstore.WrapError("get", key, err)
	}
	meta := metaForFile(key, body, stat)
	return &objstore.Object{
		Key:         meta.Key,
		Body:        body,
		ETag:        meta.ETag,
		VersionID:   meta.VersionID,
		Size:        meta.Size,
		ContentType: meta.ContentType,
		Metadata:    meta.Metadata,
		CreatedAt:   meta.CreatedAt,
		UpdatedAt:   meta.UpdatedAt,
	}, nil
}

func (s *Store) Head(ctx context.Context, key string) (*objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return nil, objstore.WrapError("head", key, err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, objstore.WrapError("head", key, objstore.ErrNotFound)
		}
		return nil, objstore.WrapError("head", key, err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		return nil, objstore.WrapError("head", key, err)
	}
	meta := metaForFile(key, body, stat)
	return &meta, nil
}

func (s *Store) Create(ctx context.Context, key string, body []byte, opts objstore.PutOptions) (*objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return nil, objstore.WrapError("create", key, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, objstore.WrapError("create", key, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, objstore.WrapError("create", key, objstore.ErrAlreadyExists)
		}
		return nil, objstore.WrapError("create", key, err)
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return nil, objstore.WrapError("create", key, err)
	}
	if err := file.Close(); err != nil {
		return nil, objstore.WrapError("create", key, err)
	}
	meta, err := s.Head(ctx, key)
	if err != nil {
		return nil, err
	}
	meta.ContentType = opts.ContentType
	meta.Metadata = cloneMetadata(opts.Metadata)
	return meta, nil
}

func (s *Store) CompareAndSwap(ctx context.Context, key string, previousETag string, body []byte, opts objstore.PutOptions) (*objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := s.Head(ctx, key)
	if err != nil {
		return nil, err
	}
	if current.ETag != previousETag {
		return nil, objstore.WrapError("compare-and-swap", key, objstore.ErrPreconditionFailed)
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return nil, objstore.WrapError("compare-and-swap", key, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return nil, objstore.WrapError("compare-and-swap", key, err)
	}
	meta, err := s.Head(ctx, key)
	if err != nil {
		return nil, err
	}
	meta.ContentType = opts.ContentType
	meta.Metadata = cloneMetadata(opts.Metadata)
	return meta, nil
}

func (s *Store) List(ctx context.Context, prefix string, opts objstore.ListOptions) ([]objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var metas []objstore.ObjectMeta
	err := filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		key, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		key = filepath.ToSlash(key)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		metas = append(metas, metaForFile(key, body, info))
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, objstore.WrapError("list", prefix, err)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Key < metas[j].Key })
	if opts.Limit > 0 && int(opts.Limit) < len(metas) {
		metas = metas[:opts.Limit]
	}
	return metas, nil
}

func (s *Store) pathForKey(key string) (string, error) {
	if key == "" {
		return "", errors.New("object key is required")
	}
	clean := filepath.Clean(filepath.FromSlash(key))
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	path := filepath.Join(s.root, clean)
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("object key %q escapes file object store root", key)
	}
	return path, nil
}

func metaForFile(key string, body []byte, info fs.FileInfo) objstore.ObjectMeta {
	updated := info.ModTime().UTC()
	sum := sha256.Sum256(body)
	etag := hex.EncodeToString(sum[:])
	return objstore.ObjectMeta{
		Key:         key,
		ETag:        etag,
		VersionID:   fmt.Sprintf("%d", updated.UnixNano()),
		Size:        info.Size(),
		ContentType: "application/json",
		CreatedAt:   time.Time{},
		UpdatedAt:   updated,
	}
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
