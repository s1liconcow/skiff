package skiffd_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/skiffd"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestEventStreamReplaysObjectLogAfterID(t *testing.T) {
	store := memory.New()
	createJSON(t, store, "services/payments-api/events/01J0000001.json", schema.Event{
		SchemaVersion: schema.Version,
		ID:            "01J0000001",
		Time:          "2026-05-16T19:00:00Z",
		Subject:       schema.Target{Kind: "service", Name: "payments-api"},
		Type:          "deploy.started",
		Summary:       "old event",
	})
	createJSON(t, store, "services/payments-api/events/01J0000002.json", schema.Event{
		SchemaVersion: schema.Version,
		ID:            "01J0000002",
		Time:          "2026-05-16T19:01:00Z",
		Subject:       schema.Target{Kind: "service", Name: "payments-api"},
		Type:          "deploy.succeeded",
		Summary:       "new event",
	})
	snapshot, err := skiffd.SnapshotFromObjectStore(nilContext(), store, fixedTime())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	server := newTestServerWithStoreAndIndex(t, store, skiffd.NewStaticIndex(snapshot))

	rec := get(t, server.Handler(), "/v1/events/stream?scope=service&service=payments-api&after=01J0000001&once=true", "text/event-stream")
	if rec.Code != http.StatusOK {
		t.Fatalf("stream status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	if strings.Contains(body, "01J0000001") || !strings.Contains(body, "id: 01J0000002") || !strings.Contains(body, `"type":"deploy.succeeded"`) {
		t.Fatalf("unexpected SSE replay body:\n%s", body)
	}
}

func nilContext() context.Context {
	return context.Background()
}
