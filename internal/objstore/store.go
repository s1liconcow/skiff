package objstore

import (
	"context"
	"time"
)

type Object struct {
	Key         string
	Body        []byte
	ETag        string
	VersionID   string
	Size        int64
	ContentType string
	Metadata    map[string]string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ObjectMeta struct {
	Key         string
	ETag        string
	VersionID   string
	Size        int64
	ContentType string
	Metadata    map[string]string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type PutOptions struct {
	ContentType string
	Metadata    map[string]string
	KMSKeyID    string
}

type ListOptions struct {
	Limit int32
}

type ObjectStore interface {
	Get(ctx context.Context, key string) (*Object, error)
	Head(ctx context.Context, key string) (*ObjectMeta, error)
	Create(ctx context.Context, key string, body []byte, opts PutOptions) (*ObjectMeta, error)
	CompareAndSwap(ctx context.Context, key string, previousETag string, body []byte, opts PutOptions) (*ObjectMeta, error)
	List(ctx context.Context, prefix string, opts ListOptions) ([]ObjectMeta, error)
}
