package s3store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	skiffaws "github.com/s1liconcow/skiff/internal/aws"
	"github.com/s1liconcow/skiff/internal/objstore"
)

type Options struct {
	Bucket         string
	Region         string
	Endpoint       string
	ForcePathStyle bool
	Credentials    skiffaws.Credentials
	KMSKeyID       string
	Client         Client
	Logger         Logger
	Clock          func() time.Time
}

type Store struct {
	bucket        string
	defaultKMSKey string
	client        Client
	logger        Logger
	clock         func() time.Time
}

func New(opts Options) (*Store, error) {
	if strings.TrimSpace(opts.Bucket) == "" {
		return nil, errors.New("s3 object store bucket is required")
	}
	client := opts.Client
	if client == nil {
		httpClient, err := NewHTTPClient(HTTPClientOptions{
			Region:         opts.Region,
			Endpoint:       opts.Endpoint,
			ForcePathStyle: opts.ForcePathStyle,
			Credentials:    opts.Credentials,
			Logger:         opts.Logger,
			Clock:          opts.Clock,
		})
		if err != nil {
			return nil, err
		}
		client = httpClient
	}
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Store{
		bucket:        opts.Bucket,
		defaultKMSKey: opts.KMSKeyID,
		client:        client,
		logger:        opts.Logger,
		clock:         clock,
	}, nil
}

func NewFromEnv(bucket, defaultRegion string, opts Options) (*Store, error) {
	cfg, err := skiffaws.LoadConfigFromEnv(defaultRegion)
	if err != nil {
		return nil, err
	}
	opts.Bucket = bucket
	opts.Region = cfg.Region
	opts.Endpoint = cfg.Endpoint
	opts.ForcePathStyle = cfg.ForcePathStyle
	opts.Credentials = cfg.Credentials
	return New(opts)
}

func (s *Store) Get(ctx context.Context, key string) (*objstore.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := s.client.GetObject(ctx, GetObjectInput{
		Bucket: s.bucket,
		Key:    key,
	})
	if err != nil {
		return nil, classifyError("get", key, err)
	}

	meta := metaFromGet(key, out)
	return &objstore.Object{
		Key:         meta.Key,
		Body:        cloneBytes(out.Body),
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
	out, err := s.client.HeadObject(ctx, HeadObjectInput{
		Bucket: s.bucket,
		Key:    key,
	})
	if err != nil {
		return nil, classifyError("head", key, err)
	}

	meta := metaFromHead(key, out)
	return &meta, nil
}

func (s *Store) Create(ctx context.Context, key string, body []byte, opts objstore.PutOptions) (*objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := s.client.PutObject(ctx, s.putInput(key, body, opts, "*", ""))
	if err != nil {
		return nil, classifyError("create", key, err)
	}

	meta := metaFromPut(key, len(body), opts, out, s.clock())
	return &meta, nil
}

func (s *Store) CompareAndSwap(ctx context.Context, key string, previousETag string, body []byte, opts objstore.PutOptions) (*objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(previousETag) == "" {
		return nil, objstore.WrapError("compare-and-swap", key, objstore.ErrPreconditionFailed)
	}

	out, err := s.client.PutObject(ctx, s.putInput(key, body, opts, "", previousETag))
	if err != nil {
		return nil, classifyError("compare-and-swap", key, err)
	}

	meta := metaFromPut(key, len(body), opts, out, s.clock())
	return &meta, nil
}

func (s *Store) List(ctx context.Context, prefix string, opts objstore.ListOptions) ([]objstore.ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var metas []objstore.ObjectMeta
	limit := opts.Limit
	var token string
	for {
		maxKeys := int32(1000)
		if limit > 0 {
			remaining := limit - int32(len(metas))
			if remaining <= 0 {
				break
			}
			if remaining < maxKeys {
				maxKeys = remaining
			}
		}

		out, err := s.client.ListObjectsV2(ctx, ListObjectsV2Input{
			Bucket:            s.bucket,
			Prefix:            prefix,
			MaxKeys:           maxKeys,
			ContinuationToken: token,
		})
		if err != nil {
			return nil, classifyError("list", prefix, err)
		}
		for _, object := range out.Objects {
			metas = append(metas, metaFromInfo(object))
		}
		if !out.IsTruncated || out.NextContinuationToken == "" {
			break
		}
		token = out.NextContinuationToken
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].Key < metas[j].Key
	})
	if opts.Limit > 0 && int(opts.Limit) < len(metas) {
		metas = metas[:opts.Limit]
	}
	return metas, nil
}

func (s *Store) putInput(key string, body []byte, opts objstore.PutOptions, ifNoneMatch string, ifMatch string) PutObjectInput {
	kmsKeyID := opts.KMSKeyID
	if kmsKeyID == "" {
		kmsKeyID = s.defaultKMSKey
	}

	input := PutObjectInput{
		Bucket:      s.bucket,
		Key:         key,
		Body:        cloneBytes(body),
		ContentType: opts.ContentType,
		Metadata:    cloneMetadata(opts.Metadata),
		IfNoneMatch: ifNoneMatch,
		IfMatch:     quoteETag(ifMatch),
	}
	if kmsKeyID != "" {
		input.ServerSideEncryption = serverSideEncryptionAWSKMS
		input.SSEKMSKeyID = kmsKeyID
	}
	return input
}

func metaFromPut(key string, size int, opts objstore.PutOptions, out *PutObjectOutput, timestamp time.Time) objstore.ObjectMeta {
	meta := objstore.ObjectMeta{
		Key:         key,
		Size:        int64(size),
		ContentType: opts.ContentType,
		Metadata:    cloneMetadata(opts.Metadata),
		CreatedAt:   timestamp.UTC(),
		UpdatedAt:   timestamp.UTC(),
	}
	if out != nil {
		meta.ETag = normalizeETag(out.ETag)
		meta.VersionID = out.VersionID
	}
	return meta
}

func metaFromGet(key string, out *GetObjectOutput) objstore.ObjectMeta {
	if out == nil {
		return objstore.ObjectMeta{Key: key}
	}
	timestamp := out.LastModified.UTC()
	return objstore.ObjectMeta{
		Key:         key,
		ETag:        normalizeETag(out.ETag),
		VersionID:   out.VersionID,
		Size:        out.ContentLength,
		ContentType: out.ContentType,
		Metadata:    cloneMetadata(out.Metadata),
		CreatedAt:   timestamp,
		UpdatedAt:   timestamp,
	}
}

func metaFromHead(key string, out *HeadObjectOutput) objstore.ObjectMeta {
	if out == nil {
		return objstore.ObjectMeta{Key: key}
	}
	timestamp := out.LastModified.UTC()
	return objstore.ObjectMeta{
		Key:         key,
		ETag:        normalizeETag(out.ETag),
		VersionID:   out.VersionID,
		Size:        out.ContentLength,
		ContentType: out.ContentType,
		Metadata:    cloneMetadata(out.Metadata),
		CreatedAt:   timestamp,
		UpdatedAt:   timestamp,
	}
}

func metaFromInfo(info ObjectInfo) objstore.ObjectMeta {
	timestamp := info.LastModified.UTC()
	return objstore.ObjectMeta{
		Key:       info.Key,
		ETag:      normalizeETag(info.ETag),
		VersionID: info.VersionID,
		Size:      info.Size,
		CreatedAt: timestamp,
		UpdatedAt: timestamp,
	}
}

func normalizeETag(etag string) string {
	etag = strings.TrimSpace(etag)
	if strings.HasPrefix(etag, "W/") {
		etag = strings.TrimPrefix(etag, "W/")
	}
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		return etag[1 : len(etag)-1]
	}
	return etag
}

func quoteETag(etag string) string {
	etag = strings.TrimSpace(etag)
	if etag == "" || etag == "*" {
		return etag
	}
	if strings.HasPrefix(etag, "W/") {
		return etag
	}
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		return etag
	}
	return `"` + etag + `"`
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

func traceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	for _, key := range []any{"trace_id", "trace-id", "traceID"} {
		if value, ok := ctx.Value(key).(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func logRequest(logger Logger, ctx context.Context, method, bucket, key string) {
	if logger == nil {
		return
	}
	traceID := traceIDFromContext(ctx)
	if traceID == "" {
		logger.Printf("s3 %s bucket=%s key=%s", method, bucket, key)
		return
	}
	logger.Printf("s3 %s bucket=%s key=%s trace_id=%s", method, bucket, key, traceID)
}

func logListRequest(logger Logger, ctx context.Context, bucket, prefix string) {
	if logger == nil {
		return
	}
	traceID := traceIDFromContext(ctx)
	if traceID == "" {
		logger.Printf("s3 LIST bucket=%s prefix=%s", bucket, prefix)
		return
	}
	logger.Printf("s3 LIST bucket=%s prefix=%s trace_id=%s", bucket, prefix, traceID)
}

func validateBucketAndKey(bucket, key string) error {
	if strings.TrimSpace(bucket) == "" {
		return errors.New("s3 bucket is required")
	}
	if key == "" {
		return errors.New("s3 object key is required")
	}
	if strings.HasPrefix(key, "/") {
		return fmt.Errorf("s3 object key %q must not start with /", key)
	}
	return nil
}
