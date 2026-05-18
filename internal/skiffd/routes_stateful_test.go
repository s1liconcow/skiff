package skiffd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider/fake"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestStatefulReplaceMemberRouteCreatesAndRunsSaga(t *testing.T) {
	store := memory.New()
	seedSkiffdStatefulMember(t, store)
	server, err := New(Options{
		Config:      config.Config{Env: "dev", Provider: "fake"},
		ObjectStore: store,
		Index:       NewStaticIndex(Snapshot{Ready: true, Generation: 1, RefreshedAt: time.Now().UTC()}),
		Provider:    fake.New(),
		Clock:       func() time.Time { return time.Date(2026, 5, 18, 3, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	body := bytes.NewBufferString(`{"group":"orders-stream","member":0,"reason":"health failed"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/stateful/replace-member?format=json", body)
	req.Header.Set("X-Skiff-Trace-Id", "tr_skiffd_replace")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		OK     bool                  `json:"ok"`
		Result statefulSagaAPIResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	if !got.OK || got.Result.Status != schema.SagaSucceeded || got.Result.NextAction != "complete" {
		t.Fatalf("unexpected response: %+v", got)
	}
	member, err := state.NewClient(store).GetStatefulMemberControl(context.Background(), "orders-stream", 0)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if member.Control.InstanceID != "fake-orders-stream-0-gen-2" || member.Control.Generation != 2 {
		t.Fatalf("member was not replaced by skiffd route: %+v", member.Control)
	}
}

func TestStatefulReplaceMemberRouteRequiresProdApproval(t *testing.T) {
	store := memory.New()
	server, err := New(Options{
		Config:      config.Config{Env: "prod", Provider: "fake"},
		ObjectStore: store,
		Index:       NewStaticIndex(Snapshot{Ready: true, Generation: 1, RefreshedAt: time.Now().UTC()}),
		Provider:    fake.New(),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/stateful/replace-member?format=json", bytes.NewBufferString(`{"group":"orders-stream","member":0}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	if got.Code != "APPROVAL_REQUIRED" || len(got.RecommendedActions) != 1 || !got.RecommendedActions[0].Mutating {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func seedSkiffdStatefulMember(t *testing.T, store *memory.Store) {
	t.Helper()
	client := state.NewClient(store)
	ctx := context.Background()
	if _, err := client.CreateStatefulGroupControl(ctx, schema.StatefulGroupControl{
		Group: "orders-stream",
		Env:   "dev",
		Members: []schema.StatefulMemberSummary{
			{Member: 0, Generation: 1, InstanceID: "i-old", VolumeID: "vol-0", DNSName: "orders-stream-0.internal", Phase: state.StatefulMemberReady},
		},
		Replicas:  1,
		UpdatedBy: schema.Actor{ID: "seed", Type: "test"},
	}); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if _, err := client.CreateStatefulMemberControl(ctx, schema.StatefulMemberControl{
		Group:      "orders-stream",
		Env:        "dev",
		Member:     0,
		Zone:       "us-west-2a",
		InstanceID: "i-old",
		VolumeID:   "vol-0",
		DNSName:    "orders-stream-0.internal",
		Generation: 1,
		Phase:      state.StatefulMemberReady,
		UpdatedBy:  schema.Actor{ID: "seed", Type: "test"},
	}); err != nil {
		t.Fatalf("create member: %v", err)
	}
}
