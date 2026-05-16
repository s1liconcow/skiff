package s3store

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
)

func TestCreateUsesIfNoneMatchKMSAndNormalizesETag(t *testing.T) {
	client := &fakeClient{
		put: func(_ context.Context, input PutObjectInput) (*PutObjectOutput, error) {
			if input.IfNoneMatch != "*" {
				t.Fatalf("IfNoneMatch = %q, want *", input.IfNoneMatch)
			}
			if input.IfMatch != "" {
				t.Fatalf("IfMatch = %q, want empty", input.IfMatch)
			}
			if input.ServerSideEncryption != serverSideEncryptionAWSKMS {
				t.Fatalf("ServerSideEncryption = %q", input.ServerSideEncryption)
			}
			if input.SSEKMSKeyID != "alias/skiff-prod" {
				t.Fatalf("SSEKMSKeyID = %q", input.SSEKMSKeyID)
			}
			if string(input.Body) != "release" {
				t.Fatalf("Body = %q", input.Body)
			}
			return &PutObjectOutput{ETag: `"abc123"`, VersionID: "v1"}, nil
		},
	}
	store := mustStore(t, Options{
		Bucket:   "state-bucket",
		KMSKeyID: "alias/skiff-prod",
		Client:   client,
		Clock:    fixedClock,
	})

	meta, err := store.Create(context.Background(), "services/api/control.json", []byte("release"), objstore.PutOptions{
		ContentType: "application/json",
		Metadata:    map[string]string{"service": "api"},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if meta.ETag != "abc123" {
		t.Fatalf("ETag = %q, want normalized value", meta.ETag)
	}
	if meta.VersionID != "v1" {
		t.Fatalf("VersionID = %q", meta.VersionID)
	}
	if meta.CreatedAt != fixedTime || meta.UpdatedAt != fixedTime {
		t.Fatalf("timestamps = %s/%s, want fixed clock", meta.CreatedAt, meta.UpdatedAt)
	}
}

func TestCompareAndSwapUsesIfMatchAndPutOptionKMSOverride(t *testing.T) {
	client := &fakeClient{
		put: func(_ context.Context, input PutObjectInput) (*PutObjectOutput, error) {
			if input.IfNoneMatch != "" {
				t.Fatalf("IfNoneMatch = %q, want empty", input.IfNoneMatch)
			}
			if input.IfMatch != `"etag-old"` {
				t.Fatalf("IfMatch = %q, want previous etag", input.IfMatch)
			}
			if input.SSEKMSKeyID != "alias/override" {
				t.Fatalf("SSEKMSKeyID = %q, want put option override", input.SSEKMSKeyID)
			}
			return &PutObjectOutput{ETag: `"etag-new"`, VersionID: "v2"}, nil
		},
	}
	store := mustStore(t, Options{
		Bucket:   "state-bucket",
		KMSKeyID: "alias/default",
		Client:   client,
		Clock:    fixedClock,
	})

	meta, err := store.CompareAndSwap(context.Background(), "services/api/control.json", "etag-old", []byte("next"), objstore.PutOptions{
		KMSKeyID: "alias/override",
	})
	if err != nil {
		t.Fatalf("CompareAndSwap returned error: %v", err)
	}
	if meta.ETag != "etag-new" {
		t.Fatalf("ETag = %q", meta.ETag)
	}
}

func TestCompareAndSwapRejectsEmptyETag(t *testing.T) {
	store := mustStore(t, Options{Bucket: "state-bucket", Client: &fakeClient{}})
	_, err := store.CompareAndSwap(context.Background(), "services/api/control.json", "", []byte("next"), objstore.PutOptions{})
	if !errors.Is(err, objstore.ErrPreconditionFailed) {
		t.Fatalf("CompareAndSwap empty ETag error = %v, want ErrPreconditionFailed", err)
	}
}

func TestQuoteAndNormalizeETag(t *testing.T) {
	tests := []struct {
		input      string
		normalized string
		quoted     string
	}{
		{input: `"abc123"`, normalized: "abc123", quoted: `"abc123"`},
		{input: "abc123", normalized: "abc123", quoted: `"abc123"`},
		{input: ` W/"abc123" `, normalized: "abc123", quoted: `W/"abc123"`},
		{input: "*", normalized: "*", quoted: "*"},
	}
	for _, tt := range tests {
		if got := normalizeETag(tt.input); got != tt.normalized {
			t.Fatalf("normalizeETag(%q) = %q, want %q", tt.input, got, tt.normalized)
		}
		if got := quoteETag(tt.input); got != tt.quoted {
			t.Fatalf("quoteETag(%q) = %q, want %q", tt.input, got, tt.quoted)
		}
	}
}

func TestGetHeadAndListMapMetadata(t *testing.T) {
	lastModified := time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC)
	client := &fakeClient{
		get: func(_ context.Context, input GetObjectInput) (*GetObjectOutput, error) {
			return &GetObjectOutput{
				Body:          []byte("release"),
				ETag:          `"etag-get"`,
				VersionID:     "v1",
				ContentType:   "application/json",
				Metadata:      map[string]string{"service": "api"},
				LastModified:  lastModified,
				ContentLength: 7,
			}, nil
		},
		head: func(_ context.Context, input HeadObjectInput) (*HeadObjectOutput, error) {
			return &HeadObjectOutput{
				ETag:          `"etag-head"`,
				VersionID:     "v2",
				ContentType:   "application/json",
				Metadata:      map[string]string{"service": "api"},
				LastModified:  lastModified,
				ContentLength: 0,
			}, nil
		},
		list: func(_ context.Context, input ListObjectsV2Input) (*ListObjectsV2Output, error) {
			return &ListObjectsV2Output{Objects: []ObjectInfo{
				{Key: "services/api/events/002.json", ETag: `"b"`, LastModified: lastModified, Size: 2},
				{Key: "services/api/events/001.json", ETag: `"a"`, LastModified: lastModified, Size: 1},
			}}, nil
		},
	}
	store := mustStore(t, Options{Bucket: "state-bucket", Client: client})

	got, err := store.Get(context.Background(), "services/api/control.json")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if string(got.Body) != "release" || got.ETag != "etag-get" || got.Metadata["service"] != "api" {
		t.Fatalf("Get object = %+v", got)
	}
	got.Body[0] = 'R'
	got.Metadata["service"] = "mutated"

	head, err := store.Head(context.Background(), "services/api/control.json")
	if err != nil {
		t.Fatalf("Head returned error: %v", err)
	}
	if head.ETag != "etag-head" || head.Metadata["service"] != "api" {
		t.Fatalf("Head meta = %+v", head)
	}

	metas, err := store.List(context.Background(), "services/api/events/", objstore.ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if gotKeys := metaKeys(metas); !reflect.DeepEqual(gotKeys, []string{"services/api/events/001.json"}) {
		t.Fatalf("List keys = %v", gotKeys)
	}
}

func TestListPaginatesUntilLimit(t *testing.T) {
	client := &fakeClient{
		list: func(_ context.Context, input ListObjectsV2Input) (*ListObjectsV2Output, error) {
			switch input.ContinuationToken {
			case "":
				if input.MaxKeys != 3 {
					t.Fatalf("first MaxKeys = %d, want 3", input.MaxKeys)
				}
				return &ListObjectsV2Output{
					Objects: []ObjectInfo{
						{Key: "services/api/events/001.json"},
						{Key: "services/api/events/002.json"},
					},
					IsTruncated:           true,
					NextContinuationToken: "next",
				}, nil
			case "next":
				if input.MaxKeys != 1 {
					t.Fatalf("second MaxKeys = %d, want remaining limit 1", input.MaxKeys)
				}
				return &ListObjectsV2Output{
					Objects: []ObjectInfo{{Key: "services/api/events/003.json"}},
				}, nil
			default:
				t.Fatalf("unexpected continuation token %q", input.ContinuationToken)
				return nil, nil
			}
		},
	}
	store := mustStore(t, Options{Bucket: "state-bucket", Client: client})

	metas, err := store.List(context.Background(), "services/api/events/", objstore.ListOptions{Limit: 3})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if got := metaKeys(metas); !reflect.DeepEqual(got, []string{
		"services/api/events/001.json",
		"services/api/events/002.json",
		"services/api/events/003.json",
	}) {
		t.Fatalf("List keys = %v", got)
	}
}

func TestErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		op   string
		err  error
		want error
	}{
		{
			name: "create precondition is already exists",
			op:   "create",
			err:  &fakeSmithyError{status: http.StatusPreconditionFailed, code: "PreconditionFailed"},
			want: objstore.ErrAlreadyExists,
		},
		{
			name: "cas precondition is precondition failed",
			op:   "compare-and-swap",
			err:  &fakeSmithyError{status: http.StatusPreconditionFailed, code: "PreconditionFailed"},
			want: objstore.ErrPreconditionFailed,
		},
		{
			name: "conditional conflict",
			op:   "compare-and-swap",
			err:  &fakeSmithyError{status: http.StatusConflict, code: "ConditionalRequestConflict"},
			want: objstore.ErrConflict,
		},
		{
			name: "missing",
			op:   "get",
			err:  &fakeSmithyError{status: http.StatusNotFound, code: "NoSuchKey"},
			want: objstore.ErrNotFound,
		},
		{
			name: "permission",
			op:   "get",
			err:  &fakeSmithyError{status: http.StatusForbidden, code: "AccessDenied"},
			want: objstore.ErrPermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.op, "key", tt.err)
			if !errors.Is(got, tt.want) {
				t.Fatalf("classifyError = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStoreWrapsClientErrors(t *testing.T) {
	client := &fakeClient{
		put: func(_ context.Context, input PutObjectInput) (*PutObjectOutput, error) {
			return nil, &APIError{StatusCode: http.StatusPreconditionFailed, Code: "PreconditionFailed"}
		},
	}
	store := mustStore(t, Options{Bucket: "state-bucket", Client: client})

	_, err := store.Create(context.Background(), "services/api/control.json", []byte("{}"), objstore.PutOptions{})
	if !errors.Is(err, objstore.ErrAlreadyExists) {
		t.Fatalf("Create error = %v, want ErrAlreadyExists", err)
	}
}

type fakeClient struct {
	get  func(context.Context, GetObjectInput) (*GetObjectOutput, error)
	head func(context.Context, HeadObjectInput) (*HeadObjectOutput, error)
	put  func(context.Context, PutObjectInput) (*PutObjectOutput, error)
	list func(context.Context, ListObjectsV2Input) (*ListObjectsV2Output, error)
}

func (c *fakeClient) GetObject(ctx context.Context, input GetObjectInput) (*GetObjectOutput, error) {
	if c.get == nil {
		return nil, fmt.Errorf("unexpected GetObject call")
	}
	return c.get(ctx, input)
}

func (c *fakeClient) HeadObject(ctx context.Context, input HeadObjectInput) (*HeadObjectOutput, error) {
	if c.head == nil {
		return nil, fmt.Errorf("unexpected HeadObject call")
	}
	return c.head(ctx, input)
}

func (c *fakeClient) PutObject(ctx context.Context, input PutObjectInput) (*PutObjectOutput, error) {
	if c.put == nil {
		return nil, fmt.Errorf("unexpected PutObject call")
	}
	return c.put(ctx, input)
}

func (c *fakeClient) ListObjectsV2(ctx context.Context, input ListObjectsV2Input) (*ListObjectsV2Output, error) {
	if c.list == nil {
		return nil, fmt.Errorf("unexpected ListObjectsV2 call")
	}
	return c.list(ctx, input)
}

type fakeSmithyError struct {
	status  int
	code    string
	message string
}

func (e *fakeSmithyError) Error() string {
	return e.code
}

func (e *fakeSmithyError) HTTPStatusCode() int {
	return e.status
}

func (e *fakeSmithyError) ErrorCode() string {
	return e.code
}

func (e *fakeSmithyError) ErrorMessage() string {
	return e.message
}

func mustStore(t *testing.T, opts Options) *Store {
	t.Helper()
	store, err := New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return store
}

func metaKeys(metas []objstore.ObjectMeta) []string {
	keys := make([]string, 0, len(metas))
	for _, meta := range metas {
		keys = append(keys, meta.Key)
	}
	return keys
}

var fixedTime = time.Date(2026, 5, 16, 18, 0, 0, 0, time.UTC)

func fixedClock() time.Time {
	return fixedTime
}
