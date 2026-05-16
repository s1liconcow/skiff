package skiffd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/buildinfo"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/skiffd"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestHealthAndVersionSupportHumanAndJSON(t *testing.T) {
	server := newTestServer(t, skiffd.Snapshot{
		Ready:       true,
		Generation:  1,
		RefreshedAt: fixedTime(),
	})
	handler := server.Handler()

	health := get(t, handler, "/healthz", "")
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", health.Code, health.Body.String())
	}
	if health.Body.String() != "ok\n" {
		t.Fatalf("health human body = %q, want ok", health.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz?format=json", nil)
	req.Header.Set("X-Skiff-Trace-Id", "tr_test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health JSON status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Skiff-Trace-Id") != "tr_test" {
		t.Fatalf("trace header = %q, want tr_test", rec.Header().Get("X-Skiff-Trace-Id"))
	}
	var healthJSON struct {
		OK        bool   `json:"ok"`
		Service   string `json:"service"`
		Status    string `json:"status"`
		TraceID   string `json:"trace_id"`
		RequestID string `json:"request_id"`
	}
	decodeJSON(t, rec, &healthJSON)
	if !healthJSON.OK || healthJSON.Service != "skiffd" || healthJSON.Status != "healthy" || healthJSON.TraceID != "tr_test" || healthJSON.RequestID == "" {
		t.Fatalf("unexpected health JSON: %+v", healthJSON)
	}

	version := get(t, handler, "/version", "")
	if version.Code != http.StatusOK {
		t.Fatalf("version status = %d, body = %s", version.Code, version.Body.String())
	}
	if want := "skiffd version test\ncommit: abc123\nbuild_date: 2026-05-16T19:00:00Z\n"; version.Body.String() != want {
		t.Fatalf("version human body = %q, want %q", version.Body.String(), want)
	}

	versionJSON := get(t, handler, "/version?format=json", "")
	if versionJSON.Code != http.StatusOK {
		t.Fatalf("version JSON status = %d, body = %s", versionJSON.Code, versionJSON.Body.String())
	}
	var gotVersion struct {
		OK      bool   `json:"ok"`
		Binary  string `json:"binary"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	decodeJSON(t, versionJSON, &gotVersion)
	if !gotVersion.OK || gotVersion.Binary != "skiffd" || gotVersion.Version != "test" || gotVersion.Commit != "abc123" {
		t.Fatalf("unexpected version JSON: %+v", gotVersion)
	}
}

func TestReadinessReflectsIndexInitialization(t *testing.T) {
	index := skiffd.NewStaticIndex(skiffd.Snapshot{Ready: false})
	server := newTestServerWithIndex(t, index)
	handler := server.Handler()

	notReady := get(t, handler, "/readyz?format=json", "")
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d, body = %s", notReady.Code, notReady.Body.String())
	}
	var errorBody struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	decodeJSON(t, notReady, &errorBody)
	if errorBody.OK || errorBody.Code != "INDEX_NOT_READY" {
		t.Fatalf("unexpected not-ready body: %+v", errorBody)
	}

	index.Set(skiffd.Snapshot{Ready: true, Generation: 2, RefreshedAt: fixedTime()})
	ready := get(t, handler, "/readyz?format=json", "")
	if ready.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body = %s", ready.Code, ready.Body.String())
	}
	var readyBody struct {
		OK        bool `json:"ok"`
		Ready     bool `json:"ready"`
		Freshness struct {
			Ready      bool  `json:"ready"`
			Generation int64 `json:"generation"`
		} `json:"freshness"`
	}
	decodeJSON(t, ready, &readyBody)
	if !readyBody.OK || !readyBody.Ready || !readyBody.Freshness.Ready || readyBody.Freshness.Generation != 2 {
		t.Fatalf("unexpected ready body: %+v", readyBody)
	}
}

func TestSnapshotBackedEnvServicesAndRecentEvents(t *testing.T) {
	store := memory.New()
	createJSON(t, store, "services/payments-api/control.json", schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        "payments-api",
		Env:            "prod",
		DesiredRelease: "rel_02",
		StableRelease:  "rel_01",
		Version:        1,
		UpdatedAt:      "2026-05-16T19:00:00Z",
		UpdatedBy:      schema.Actor{ID: "alpha-one", Type: "agent"},
	})
	createJSON(t, store, "services/payments-api/operations/op_01/events/01JSTARTED.json", schema.Event{
		SchemaVersion: schema.Version,
		ID:            "01JSTARTED",
		Time:          "2026-05-16T19:01:00Z",
		TraceID:       "tr_event",
		Subject:       schema.Target{Kind: "service", Name: "payments-api"},
		Type:          "operation.started",
		Severity:      "info",
		Summary:       "deploy started",
	})
	createJSON(t, store, "sagas/saga_01JABC/control.json", schema.SagaControl{
		SchemaVersion: schema.Version,
		SagaID:        "saga_01JABC",
		Status:        schema.SagaRunning,
		CurrentSteps:  []string{"shift-traffic"},
		UpdatedAt:     "2026-05-16T19:02:00Z",
		TraceID:       "tr_saga",
	})
	snapshot, err := skiffd.SnapshotFromObjectStore(context.Background(), store, fixedTime())
	if err != nil {
		t.Fatalf("snapshot from object store: %v", err)
	}
	server := newTestServerWithStoreAndIndex(t, store, skiffd.NewStaticIndex(snapshot))
	handler := server.Handler()

	env := get(t, handler, "/v1/env", "application/json")
	if env.Code != http.StatusOK {
		t.Fatalf("env status = %d, body = %s", env.Code, env.Body.String())
	}
	var envBody struct {
		OK  bool `json:"ok"`
		Env struct {
			Name        string `json:"name"`
			Provider    string `json:"provider"`
			Region      string `json:"region"`
			StateBucket string `json:"state_bucket"`
		} `json:"env"`
		Dependencies map[string]bool `json:"dependencies"`
	}
	decodeJSON(t, env, &envBody)
	if !envBody.OK || envBody.Env.Name != "prod" || envBody.Env.StateBucket != "memory://skiffd-test" {
		t.Fatalf("unexpected env body: %+v", envBody)
	}
	if !envBody.Dependencies["object_store"] || !envBody.Dependencies["index"] {
		t.Fatalf("missing dependency flags: %+v", envBody.Dependencies)
	}

	services := get(t, handler, "/v1/services", "application/json")
	if services.Code != http.StatusOK {
		t.Fatalf("services status = %d, body = %s", services.Code, services.Body.String())
	}
	var servicesBody struct {
		OK       bool                    `json:"ok"`
		Services []skiffd.ServiceSummary `json:"services"`
		Index    struct {
			Source           string `json:"source"`
			Generation       int64  `json:"generation"`
			FreshnessSeconds int64  `json:"freshness_seconds"`
		} `json:"index"`
	}
	decodeJSON(t, services, &servicesBody)
	if !servicesBody.OK || len(servicesBody.Services) != 1 {
		t.Fatalf("unexpected services body: %+v", servicesBody)
	}
	if servicesBody.Services[0].Service != "payments-api" || servicesBody.Services[0].DesiredRelease != "rel_02" {
		t.Fatalf("unexpected service summary: %+v", servicesBody.Services[0])
	}
	if servicesBody.Index.Source != "memory" || servicesBody.Index.Generation != 1 {
		t.Fatalf("unexpected index freshness: %+v", servicesBody.Index)
	}

	status := get(t, handler, "/v1/status?service=payments-api", "application/json")
	if status.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d, body = %s", status.Code, status.Body.String())
	}
	var statusBody struct {
		OK     bool `json:"ok"`
		Status struct {
			Source   string `json:"source"`
			Services []struct {
				Service      string `json:"service"`
				Health       string `json:"health"`
				RecentEvents []struct {
					ID string `json:"id"`
				} `json:"recent_events"`
				Logs struct {
					Status string `json:"status"`
				} `json:"logs"`
				Metrics struct {
					Status string `json:"status"`
				} `json:"metrics"`
			} `json:"services"`
		} `json:"status"`
	}
	decodeJSON(t, status, &statusBody)
	if !statusBody.OK || statusBody.Status.Source != "api" || len(statusBody.Status.Services) != 1 {
		t.Fatalf("unexpected status body: %+v", statusBody)
	}
	if statusBody.Status.Services[0].Service != "payments-api" || statusBody.Status.Services[0].Health == "" || len(statusBody.Status.Services[0].RecentEvents) != 1 {
		t.Fatalf("unexpected service status: %+v", statusBody.Status.Services[0])
	}
	if statusBody.Status.Services[0].Logs.Status == "" || statusBody.Status.Services[0].Metrics.Status == "" {
		t.Fatalf("missing dependency status: %+v", statusBody.Status.Services[0])
	}

	doctor := get(t, handler, "/v1/doctor?service=payments-api", "application/json")
	if doctor.Code != http.StatusOK {
		t.Fatalf("doctor endpoint = %d, body = %s", doctor.Code, doctor.Body.String())
	}
	var doctorBody struct {
		OK      bool   `json:"ok"`
		TraceID string `json:"trace_id"`
		Doctor  struct {
			Service  string `json:"service"`
			Health   string `json:"health"`
			Findings []struct {
				Code     string `json:"code"`
				Severity string `json:"severity"`
			} `json:"findings"`
			RecommendedActions []struct {
				ID       string `json:"id"`
				Mutating bool   `json:"mutating"`
				Command  string `json:"command"`
			} `json:"recommended_actions"`
		} `json:"doctor"`
	}
	decodeJSON(t, doctor, &doctorBody)
	if !doctorBody.OK || doctorBody.Doctor.Service != "payments-api" || doctorBody.Doctor.Health == "" {
		t.Fatalf("unexpected doctor body: %+v", doctorBody)
	}
	if len(doctorBody.Doctor.Findings) == 0 || len(doctorBody.Doctor.RecommendedActions) == 0 {
		t.Fatalf("doctor missing findings/actions: %+v", doctorBody.Doctor)
	}

	sagas := get(t, handler, "/v1/sagas", "application/json")
	if sagas.Code != http.StatusOK {
		t.Fatalf("sagas status = %d, body = %s", sagas.Code, sagas.Body.String())
	}
	var sagasBody struct {
		OK    bool                 `json:"ok"`
		Sagas []skiffd.SagaSummary `json:"sagas"`
	}
	decodeJSON(t, sagas, &sagasBody)
	if !sagasBody.OK || len(sagasBody.Sagas) != 1 || sagasBody.Sagas[0].SagaID != "saga_01JABC" {
		t.Fatalf("unexpected sagas body: %+v", sagasBody)
	}

	events := get(t, handler, "/v1/events/recent?limit=1", "application/json")
	if events.Code != http.StatusOK {
		t.Fatalf("events status = %d, body = %s", events.Code, events.Body.String())
	}
	var eventsBody struct {
		OK     bool           `json:"ok"`
		Events []schema.Event `json:"events"`
	}
	decodeJSON(t, events, &eventsBody)
	if !eventsBody.OK || len(eventsBody.Events) != 1 || eventsBody.Events[0].ID != "01JSTARTED" {
		t.Fatalf("unexpected events body: %+v", eventsBody)
	}

	admin := get(t, handler, "/v1/admin/index", "application/json")
	if admin.Code != http.StatusOK {
		t.Fatalf("admin index status = %d, body = %s", admin.Code, admin.Body.String())
	}
	var adminBody struct {
		OK    bool           `json:"ok"`
		Index map[string]any `json:"index"`
		Stats struct {
			Services     int `json:"services"`
			Sagas        int `json:"sagas"`
			RecentEvents int `json:"recent_events"`
		} `json:"stats"`
	}
	decodeJSON(t, admin, &adminBody)
	if !adminBody.OK || adminBody.Stats.Services != 1 || adminBody.Stats.Sagas != 1 || adminBody.Stats.RecentEvents != 1 {
		t.Fatalf("unexpected admin index body: %+v", adminBody)
	}
}

func TestFreshServicesBypassesStaleMemoryIndex(t *testing.T) {
	store := memory.New()
	createJSON(t, store, "services/payments-api/control.json", schema.ServiceControl{
		SchemaVersion:  schema.Version,
		Service:        "payments-api",
		Env:            "prod",
		DesiredRelease: "rel_fresh",
		Version:        1,
		UpdatedAt:      "2026-05-16T19:00:00Z",
		UpdatedBy:      schema.Actor{ID: "alpha-one", Type: "agent"},
	})
	server := newTestServerWithStoreAndIndex(t, store, skiffd.NewStaticIndex(skiffd.Snapshot{
		Ready:      true,
		Generation: 99,
		Services: []skiffd.ServiceSummary{
			{Service: "payments-api", Env: "prod", DesiredRelease: "rel_stale"},
		},
	}))
	handler := server.Handler()

	stale := get(t, handler, "/v1/services", "application/json")
	if stale.Code != http.StatusOK {
		t.Fatalf("stale services status = %d, body = %s", stale.Code, stale.Body.String())
	}
	var staleBody struct {
		Services []skiffd.ServiceSummary `json:"services"`
		Index    struct {
			Source string `json:"source"`
		} `json:"index"`
	}
	decodeJSON(t, stale, &staleBody)
	if len(staleBody.Services) != 1 || staleBody.Services[0].DesiredRelease != "rel_stale" || staleBody.Index.Source != "memory" {
		t.Fatalf("unexpected stale body: %+v", staleBody)
	}

	fresh := get(t, handler, "/v1/services?fresh=true", "application/json")
	if fresh.Code != http.StatusOK {
		t.Fatalf("fresh services status = %d, body = %s", fresh.Code, fresh.Body.String())
	}
	var freshBody struct {
		Services []skiffd.ServiceSummary `json:"services"`
		Index    struct {
			Source string `json:"source"`
		} `json:"index"`
	}
	decodeJSON(t, fresh, &freshBody)
	if len(freshBody.Services) != 1 || freshBody.Services[0].DesiredRelease != "rel_fresh" {
		t.Fatalf("fresh=true did not read object state: %+v", freshBody.Services)
	}
	if freshBody.Index.Source != "direct_object_store" {
		t.Fatalf("fresh index source = %q", freshBody.Index.Source)
	}
}

func TestStructuredNotFoundError(t *testing.T) {
	server := newTestServer(t, skiffd.Snapshot{Ready: true, Generation: 1, RefreshedAt: fixedTime()})
	rec := get(t, server.Handler(), "/missing", "application/json")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	decodeJSON(t, rec, &body)
	if body.OK || body.Code != "NOT_FOUND" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func TestTraceIDIsPropagatedToStructuredLogs(t *testing.T) {
	var logs bytes.Buffer
	server, err := skiffd.New(skiffd.Options{
		Config: config.Config{
			Env:         "prod",
			Provider:    "aws",
			Region:      "us-west-2",
			StateBucket: "memory://skiffd-test",
			AuthMode:    "none",
			LogLevel:    "info",
			Mode:        config.ModeSkiffd,
		},
		ObjectStore: memory.New(),
		Index: skiffd.NewStaticIndex(skiffd.Snapshot{
			Ready:       true,
			Generation:  1,
			RefreshedAt: fixedTime(),
		}),
		BuildInfo: buildinfo.Info{Binary: "skiffd", Version: "test"},
		Logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
		Clock:     fixedTime,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz?format=json", nil)
	req.Header.Set("X-Skiff-Trace-Id", "tr_log")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), `"trace_id":"tr_log"`) {
		t.Fatalf("log output missing trace id: %s", logs.String())
	}
}

func TestServeStopsGracefullyWhenContextIsCanceled(t *testing.T) {
	server := newTestServer(t, skiffd.Snapshot{Ready: true, Generation: 1, RefreshedAt: fixedTime()})
	listener := newBlockingListener()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx, listener)
	}()

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

type blockingListener struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingListener() *blockingListener {
	return &blockingListener{closed: make(chan struct{})}
}

func (l *blockingListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *blockingListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *blockingListener) Addr() net.Addr {
	return fakeAddr("memory")
}

type fakeAddr string

func (a fakeAddr) Network() string { return string(a) }
func (a fakeAddr) String() string  { return string(a) }

func newTestServer(t *testing.T, snapshot skiffd.Snapshot) *skiffd.Server {
	t.Helper()
	return newTestServerWithIndex(t, skiffd.NewStaticIndex(snapshot))
}

func newTestServerWithIndex(t *testing.T, index skiffd.Index) *skiffd.Server {
	t.Helper()
	return newTestServerWithStoreAndIndex(t, memory.New(), index)
}

func newTestServerWithStoreAndIndex(t *testing.T, store objstore.ObjectStore, index skiffd.Index) *skiffd.Server {
	t.Helper()
	server, err := skiffd.New(skiffd.Options{
		Config: config.Config{
			Env:         "prod",
			Provider:    "aws",
			Region:      "us-west-2",
			StateBucket: "memory://skiffd-test",
			AuthMode:    "none",
			LogLevel:    "info",
			Mode:        config.ModeSkiffd,
		},
		ObjectStore: store,
		Index:       index,
		BuildInfo: buildinfo.Info{
			Binary:    "skiffd",
			Version:   "test",
			Commit:    "abc123",
			BuildDate: "2026-05-16T19:00:00Z",
		},
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Clock:  fixedTime,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

func get(t *testing.T, handler http.Handler, target string, accept string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func createJSON(t *testing.T, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: "application/json"}); err != nil {
		t.Fatalf("create %s: %v", key, err)
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, rec.Body.String())
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 5, 16, 19, 30, 0, 0, time.UTC)
}
