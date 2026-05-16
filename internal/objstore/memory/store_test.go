package memory_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
)

func TestCreateGetHeadAndCopies(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	body := []byte("release")
	metadata := map[string]string{"service": "payments-api"}

	meta, err := store.Create(ctx, "services/payments-api/releases/r1/release.json", body, objstore.PutOptions{
		ContentType: "application/json",
		Metadata:    metadata,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if meta.ETag != "etag-00000000000000000001" {
		t.Fatalf("ETag = %q, want deterministic first version", meta.ETag)
	}
	if meta.VersionID != "version-00000000000000000001" {
		t.Fatalf("VersionID = %q, want deterministic first version", meta.VersionID)
	}
	if meta.ContentType != "application/json" {
		t.Fatalf("ContentType = %q", meta.ContentType)
	}
	if meta.Size != int64(len("release")) {
		t.Fatalf("Size = %d", meta.Size)
	}

	body[0] = 'R'
	metadata["service"] = "mutated"
	meta.Metadata["service"] = "mutated-again"

	got, err := store.Get(ctx, "services/payments-api/releases/r1/release.json")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if string(got.Body) != "release" {
		t.Fatalf("Body = %q, want create-time copy", got.Body)
	}
	if got.Metadata["service"] != "payments-api" {
		t.Fatalf("Metadata service = %q, want create-time copy", got.Metadata["service"])
	}
	got.Body[0] = 'X'
	got.Metadata["service"] = "changed"

	head, err := store.Head(ctx, "services/payments-api/releases/r1/release.json")
	if err != nil {
		t.Fatalf("Head returned error: %v", err)
	}
	if head.Metadata["service"] != "payments-api" {
		t.Fatalf("Head metadata was mutated through Get result")
	}
	if !head.CreatedAt.Equal(head.UpdatedAt) {
		t.Fatalf("CreatedAt = %s UpdatedAt = %s, want equal on create", head.CreatedAt, head.UpdatedAt)
	}

	gotAgain, err := store.Get(ctx, "services/payments-api/releases/r1/release.json")
	if err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}
	if string(gotAgain.Body) != "release" {
		t.Fatalf("stored body was mutated through Get result")
	}
}

func TestCreateFailsIfKeyExists(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	if _, err := store.Create(ctx, "services/api/control.json", []byte("{}"), objstore.PutOptions{}); err != nil {
		t.Fatalf("initial Create returned error: %v", err)
	}

	_, err := store.Create(ctx, "services/api/control.json", []byte("{}"), objstore.PutOptions{})
	if !errors.Is(err, objstore.ErrAlreadyExists) {
		t.Fatalf("Create existing error = %v, want ErrAlreadyExists", err)
	}
}

func TestCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	first, err := store.Create(ctx, "services/api/control.json", []byte(`{"version":1}`), objstore.PutOptions{
		ContentType: "application/json",
		Metadata:    map[string]string{"owner": "deploy"},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	second, err := store.CompareAndSwap(ctx, "services/api/control.json", first.ETag, []byte(`{"version":2}`), objstore.PutOptions{
		ContentType: "application/json",
		Metadata:    map[string]string{"owner": "rollout"},
	})
	if err != nil {
		t.Fatalf("CompareAndSwap returned error: %v", err)
	}
	if second.ETag != "etag-00000000000000000002" {
		t.Fatalf("ETag = %q, want second deterministic version", second.ETag)
	}
	if second.VersionID != "version-00000000000000000002" {
		t.Fatalf("VersionID = %q, want second deterministic version", second.VersionID)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("CreatedAt changed across CAS")
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("UpdatedAt did not advance across CAS")
	}

	got, err := store.Get(ctx, "services/api/control.json")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if string(got.Body) != `{"version":2}` {
		t.Fatalf("Body = %q", got.Body)
	}
	if got.Metadata["owner"] != "rollout" {
		t.Fatalf("Metadata owner = %q", got.Metadata["owner"])
	}
}

func TestCompareAndSwapPreconditions(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	if _, err := store.CompareAndSwap(ctx, "missing", "etag", []byte("{}"), objstore.PutOptions{}); !errors.Is(err, objstore.ErrNotFound) {
		t.Fatalf("CAS missing error = %v, want ErrNotFound", err)
	}

	first, err := store.Create(ctx, "services/api/control.json", []byte("{}"), objstore.PutOptions{})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if _, err := store.CompareAndSwap(ctx, "services/api/control.json", first.ETag+"-stale", []byte("{}"), objstore.PutOptions{}); !errors.Is(err, objstore.ErrPreconditionFailed) {
		t.Fatalf("CAS stale error = %v, want ErrPreconditionFailed", err)
	}
}

func TestListPrefixOrderingAndLimit(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	for _, key := range []string{
		"services/payments/events/003.json",
		"services/auth/control.json",
		"services/payments/events/001.json",
		"services/payments/events/002.json",
		"sagas/deploy/events/001.json",
	} {
		if _, err := store.Create(ctx, key, []byte(key), objstore.PutOptions{}); err != nil {
			t.Fatalf("Create %q returned error: %v", key, err)
		}
	}

	metas, err := store.List(ctx, "services/payments/events/", objstore.ListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	got := keysOf(metas)
	want := []string{
		"services/payments/events/001.json",
		"services/payments/events/002.json",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("List keys = %v, want %v", got, want)
	}
}

func TestConcurrentCASOnlyOneWinnerForStaleETag(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	first, err := store.Create(ctx, "services/api/control.json", []byte("initial"), objstore.PutOptions{})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	var wins atomic.Int64
	var preconditions atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.CompareAndSwap(ctx, "services/api/control.json", first.ETag, []byte(fmt.Sprintf("writer-%d", i)), objstore.PutOptions{})
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, objstore.ErrPreconditionFailed):
				preconditions.Add(1)
			default:
				t.Errorf("CompareAndSwap returned unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if wins.Load() != 1 {
		t.Fatalf("CAS wins = %d, want 1", wins.Load())
	}
	if preconditions.Load() != 99 {
		t.Fatalf("CAS preconditions = %d, want 99", preconditions.Load())
	}
}

func TestConcurrentCASIncrements(t *testing.T) {
	ctx := context.Background()
	store := memory.New()

	if _, err := store.Create(ctx, "services/api/control.json", []byte("0"), objstore.PutOptions{}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	const goroutines = 16
	const incrementsPerGoroutine = 50
	var successes atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				for {
					current, err := store.Get(ctx, "services/api/control.json")
					if err != nil {
						t.Errorf("Get returned error: %v", err)
						return
					}
					value, err := strconv.Atoi(string(current.Body))
					if err != nil {
						t.Errorf("stored value is not an integer: %v", err)
						return
					}
					_, err = store.CompareAndSwap(ctx, "services/api/control.json", current.ETag, []byte(strconv.Itoa(value+1)), objstore.PutOptions{})
					if err == nil {
						successes.Add(1)
						break
					}
					if !errors.Is(err, objstore.ErrPreconditionFailed) {
						t.Errorf("CompareAndSwap returned unexpected error: %v", err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()

	current, err := store.Get(ctx, "services/api/control.json")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	value, err := strconv.Atoi(string(current.Body))
	if err != nil {
		t.Fatalf("stored value is not an integer: %v", err)
	}
	if value != int(successes.Load()) {
		t.Fatalf("stored value = %d, successful CAS writes = %d", value, successes.Load())
	}

	wantVersion := fmt.Sprintf("version-%020d", successes.Load()+1)
	if current.VersionID != wantVersion {
		t.Fatalf("VersionID = %q, want %q", current.VersionID, wantVersion)
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := memory.New()

	if _, err := store.Create(ctx, "services/api/control.json", []byte("{}"), objstore.PutOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create canceled error = %v, want context.Canceled", err)
	}
}

func keysOf(metas []objstore.ObjectMeta) []string {
	keys := make([]string, 0, len(metas))
	for _, meta := range metas {
		keys = append(keys, meta.Key)
	}
	return keys
}
