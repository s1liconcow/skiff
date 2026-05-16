package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
)

type Store struct {
	mu      sync.RWMutex
	objects map[string]*entry
	tick    int64
}

type entry struct {
	meta    objstore.ObjectMeta
	body    []byte
	version int64
}

func New() *Store {
	return &Store{
		objects: make(map[string]*entry),
	}
}

func (s *Store) Get(ctx context.Context, key string) (*objstore.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	item, ok := s.objects[key]
	if !ok {
		return nil, objstore.WrapError("get", key, objstore.ErrNotFound)
	}

	meta := cloneMeta(item.meta)
	return &objstore.Object{
		Key:         meta.Key,
		Body:        cloneBytes(item.body),
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

	s.mu.RLock()
	defer s.mu.RUnlock()

	item, ok := s.objects[key]
	if !ok {
		return nil, objstore.WrapError("head", key, objstore.ErrNotFound)
	}

	meta := cloneMeta(item.meta)
	return &meta, nil
}

func (s *Store) Create(ctx context.Context, key string, body []byte, opts objstore.PutOptions) (*objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.objects[key]; ok {
		return nil, objstore.WrapError("create", key, objstore.ErrAlreadyExists)
	}

	now := s.nextTimeLocked()
	item := &entry{
		body:    cloneBytes(body),
		version: 1,
	}
	item.meta = objstore.ObjectMeta{
		Key:         key,
		ETag:        etagForVersion(item.version),
		VersionID:   versionIDForVersion(item.version),
		Size:        int64(len(body)),
		ContentType: opts.ContentType,
		Metadata:    cloneMetadata(opts.Metadata),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.objects[key] = item

	meta := cloneMeta(item.meta)
	return &meta, nil
}

func (s *Store) CompareAndSwap(ctx context.Context, key string, previousETag string, body []byte, opts objstore.PutOptions) (*objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.objects[key]
	if !ok {
		return nil, objstore.WrapError("compare-and-swap", key, objstore.ErrNotFound)
	}
	if item.meta.ETag != previousETag {
		return nil, objstore.WrapError("compare-and-swap", key, objstore.ErrPreconditionFailed)
	}

	item.version++
	item.body = cloneBytes(body)
	item.meta.ETag = etagForVersion(item.version)
	item.meta.VersionID = versionIDForVersion(item.version)
	item.meta.Size = int64(len(body))
	item.meta.ContentType = opts.ContentType
	item.meta.Metadata = cloneMetadata(opts.Metadata)
	item.meta.UpdatedAt = s.nextTimeLocked()

	meta := cloneMeta(item.meta)
	return &meta, nil
}

func (s *Store) List(ctx context.Context, prefix string, opts objstore.ListOptions) ([]objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	if opts.Limit > 0 && int(opts.Limit) < len(keys) {
		keys = keys[:opts.Limit]
	}

	metas := make([]objstore.ObjectMeta, 0, len(keys))
	for _, key := range keys {
		metas = append(metas, cloneMeta(s.objects[key].meta))
	}
	return metas, nil
}

func (s *Store) nextTimeLocked() time.Time {
	s.tick++
	return time.Unix(0, s.tick).UTC()
}

func etagForVersion(version int64) string {
	return fmt.Sprintf("etag-%020d", version)
}

func versionIDForVersion(version int64) string {
	return fmt.Sprintf("version-%020d", version)
}

func cloneMeta(meta objstore.ObjectMeta) objstore.ObjectMeta {
	meta.Metadata = cloneMetadata(meta.Metadata)
	return meta
}

func cloneBytes(body []byte) []byte {
	if body == nil {
		return nil
	}
	out := make([]byte, len(body))
	copy(out, body)
	return out
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
