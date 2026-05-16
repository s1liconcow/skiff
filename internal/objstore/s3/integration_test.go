package s3store

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	skiffaws "github.com/s1liconcow/skiff/internal/aws"
	"github.com/s1liconcow/skiff/internal/objstore"
)

func TestIntegrationS3ConditionalWrites(t *testing.T) {
	bucket := os.Getenv("SKIFF_TEST_S3_BUCKET")
	if bucket == "" {
		t.Skip("set SKIFF_TEST_S3_BUCKET to run real S3 integration test")
	}
	region := firstNonEmptyTest(os.Getenv("SKIFF_TEST_S3_REGION"), os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"))
	if region == "" {
		t.Fatal("SKIFF_TEST_S3_REGION or AWS_REGION is required")
	}
	pathStyle, _ := strconv.ParseBool(os.Getenv("SKIFF_TEST_S3_PATH_STYLE"))
	client, err := NewHTTPClient(HTTPClientOptions{
		Region:         region,
		Endpoint:       os.Getenv("SKIFF_TEST_S3_ENDPOINT"),
		ForcePathStyle: pathStyle,
		Credentials:    skiffaws.LoadCredentialsFromEnv(),
	})
	if err != nil {
		t.Fatalf("NewHTTPClient returned error: %v", err)
	}
	store, err := New(Options{
		Bucket:   bucket,
		Client:   client,
		KMSKeyID: os.Getenv("SKIFF_TEST_S3_KMS_KEY"),
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ctx := context.Background()
	prefix := "tests/skiff-s3/" + time.Now().UTC().Format("20060102T150405.000000000") + "/"
	key := prefix + "control.json"
	defer func() {
		_ = client.DeleteObject(context.Background(), bucket, key)
		_ = client.DeleteObject(context.Background(), bucket, prefix+"event.json")
	}()

	first, err := store.Create(ctx, key, []byte(`{"version":1}`), objstore.PutOptions{ContentType: "application/json"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.Create(ctx, key, []byte(`{"version":1}`), objstore.PutOptions{ContentType: "application/json"}); !errors.Is(err, objstore.ErrAlreadyExists) {
		t.Fatalf("duplicate Create error = %v, want ErrAlreadyExists", err)
	}
	if _, err := store.CompareAndSwap(ctx, key, first.ETag+"-stale", []byte(`{"version":2}`), objstore.PutOptions{ContentType: "application/json"}); !errors.Is(err, objstore.ErrPreconditionFailed) && !errors.Is(err, objstore.ErrConflict) {
		t.Fatalf("stale CAS error = %v, want ErrPreconditionFailed or ErrConflict", err)
	}
	second, err := store.CompareAndSwap(ctx, key, first.ETag, []byte(`{"version":2}`), objstore.PutOptions{ContentType: "application/json"})
	if err != nil {
		t.Fatalf("CompareAndSwap returned error: %v", err)
	}
	if second.ETag == "" || second.ETag == first.ETag {
		t.Fatalf("second ETag = %q first = %q", second.ETag, first.ETag)
	}

	if _, err := store.Create(ctx, prefix+"event.json", []byte(`{"ok":true}`), objstore.PutOptions{ContentType: "application/json"}); err != nil {
		t.Fatalf("Create event returned error: %v", err)
	}
	metas, err := store.List(ctx, prefix, objstore.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(metas) < 2 {
		t.Fatalf("List returned %d object(s), want at least 2", len(metas))
	}
}

func firstNonEmptyTest(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
