package skiffd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/s1liconcow/skiff/internal/buildinfo"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	defaultShutdownTimeout = 5 * time.Second
	maxRecentEventsLimit   = 200
)

type Provider interface {
	Name() string
}

type Signer interface {
	KeyID() string
}

type Verifier interface {
	KeyIDs() []string
}

type EventBus interface {
	Publish(ctx context.Context, event schema.Event) error
}

type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (schema.Actor, error)
}

type AllowAllAuthenticator struct{}

func (AllowAllAuthenticator) Authenticate(_ context.Context, _ *http.Request) (schema.Actor, error) {
	return schema.Actor{ID: "local", Type: "dev"}, nil
}

type Options struct {
	Config        config.Config
	ObjectStore   objstore.ObjectStore
	Index         Index
	Provider      Provider
	Signer        Signer
	Verifier      Verifier
	EventBus      EventBus
	Authenticator Authenticator
	BuildInfo     buildinfo.Info
	Logger        *slog.Logger
	Clock         func() time.Time
}

type Server struct {
	cfg           config.Config
	store         objstore.ObjectStore
	index         Index
	provider      Provider
	signer        Signer
	verifier      Verifier
	eventBus      EventBus
	authenticator Authenticator
	info          buildinfo.Info
	logger        *slog.Logger
	clock         func() time.Time
	requestSeq    atomic.Uint64
}

func New(opts Options) (*Server, error) {
	if opts.ObjectStore == nil {
		return nil, errors.New("skiffd: object store dependency is required")
	}
	if opts.Index == nil {
		return nil, errors.New("skiffd: index dependency is required")
	}
	if opts.Authenticator == nil {
		opts.Authenticator = AllowAllAuthenticator{}
	}
	if opts.BuildInfo.Binary == "" {
		opts.BuildInfo = buildinfo.Current("skiffd")
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if opts.Clock == nil {
		opts.Clock = func() time.Time { return time.Now().UTC() }
	}

	return &Server{
		cfg:           opts.Config,
		store:         opts.ObjectStore,
		index:         opts.Index,
		provider:      opts.Provider,
		signer:        opts.Signer,
		verifier:      opts.Verifier,
		eventBus:      opts.EventBus,
		authenticator: opts.Authenticator,
		info:          opts.BuildInfo,
		logger:        opts.Logger,
		clock:         opts.Clock,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/version", s.handleVersion)
	mux.HandleFunc("/v1/env", s.handleEnv)
	mux.HandleFunc("/v1/services", s.handleServices)
	mux.HandleFunc("/v1/events/recent", s.handleRecentEvents)
	mux.HandleFunc("/", s.handleNotFound)
	return s.withMiddleware(mux)
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	httpServer := &http.Server{
		Handler: s.Handler(),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	errCh := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			errCh <- nil
			return
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	}
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := s.clock()
		requestID := s.requestID(r)
		traceID := traceIDForRequest(r, requestID)

		w.Header().Set("X-Request-Id", requestID)
		w.Header().Set("X-Skiff-Trace-Id", traceID)

		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		ctx = context.WithValue(ctx, traceIDKey{}, traceID)

		actor, err := s.authenticator.Authenticate(ctx, r)
		if err != nil {
			writeError(w, r.WithContext(ctx), http.StatusUnauthorized, "UNAUTHENTICATED", err.Error(), nil)
			return
		}
		ctx = context.WithValue(ctx, actorKey{}, actor)

		rw := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r.WithContext(ctx))

		s.logger.InfoContext(ctx, "skiffd request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"request_id", requestID,
			"trace_id", traceID,
			"actor_id", actor.ID,
			"duration_ms", s.clock().Sub(started).Milliseconds(),
		)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	jsonMode, ok := negotiateFormat(w, r, false)
	if !ok {
		return
	}
	if !jsonMode {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"service":    "skiffd",
		"status":     "healthy",
		"trace_id":   traceIDFromContext(r.Context()),
		"request_id": requestIDFromContext(r.Context()),
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	snapshot, err := s.index.Snapshot(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INDEX_SNAPSHOT_FAILED", err.Error(), nil)
		return
	}
	jsonMode, ok := negotiateFormat(w, r, false)
	if !ok {
		return
	}
	if !snapshot.Ready {
		if !jsonMode {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "not ready: index has not completed initial load\n")
			return
		}
		writeError(w, r, http.StatusServiceUnavailable, "INDEX_NOT_READY", "index has not completed initial load", []recommendedAction{
			{ID: "retry_readyz", Command: "curl -fsS http://<skiffd>/readyz?format=json", Mutating: false},
		})
		return
	}

	freshness := freshnessFromSnapshot(snapshot)
	if !jsonMode {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ready\n")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"ready":      true,
		"trace_id":   traceIDFromContext(r.Context()),
		"request_id": requestIDFromContext(r.Context()),
		"freshness":  freshness,
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	jsonMode, ok := negotiateFormat(w, r, false)
	if !ok {
		return
	}
	if !jsonMode {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "%s version %s\n", s.info.Binary, s.info.Version)
		fmt.Fprintf(w, "commit: %s\n", s.info.Commit)
		fmt.Fprintf(w, "build_date: %s\n", s.info.BuildDate)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"binary":     s.info.Binary,
		"version":    s.info.Version,
		"commit":     s.info.Commit,
		"build_date": s.info.BuildDate,
		"trace_id":   traceIDFromContext(r.Context()),
		"request_id": requestIDFromContext(r.Context()),
	})
}

func (s *Server) handleEnv(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	snapshot, err := s.index.Snapshot(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INDEX_SNAPSHOT_FAILED", err.Error(), nil)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"trace_id":   traceIDFromContext(r.Context()),
		"request_id": requestIDFromContext(r.Context()),
		"env": map[string]any{
			"name":         s.cfg.Env,
			"provider":     s.cfg.Provider,
			"region":       s.cfg.Region,
			"state_bucket": redactURI(s.cfg.StateBucket),
			"auth_mode":    s.cfg.AuthMode,
			"log_level":    s.cfg.LogLevel,
		},
		"dependencies": map[string]any{
			"object_store": s.store != nil,
			"index":        s.index != nil,
			"provider":     s.provider != nil,
			"signer":       s.signer != nil,
			"verifier":     s.verifier != nil,
			"event_bus":    s.eventBus != nil,
		},
		"freshness": freshnessFromSnapshot(snapshot),
	})
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	snapshot, err := s.index.Snapshot(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INDEX_SNAPSHOT_FAILED", err.Error(), nil)
		return
	}
	if !snapshot.Ready {
		writeError(w, r, http.StatusServiceUnavailable, "INDEX_NOT_READY", "index has not completed initial load", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"trace_id":   traceIDFromContext(r.Context()),
		"request_id": requestIDFromContext(r.Context()),
		"freshness":  freshnessFromSnapshot(snapshot),
		"services":   snapshot.Services,
	})
}

func (s *Server) handleRecentEvents(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if _, ok := negotiateFormat(w, r, true); !ok {
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "INVALID_LIMIT", err.Error(), nil)
		return
	}
	snapshot, err := s.index.Snapshot(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "INDEX_SNAPSHOT_FAILED", err.Error(), nil)
		return
	}
	if !snapshot.Ready {
		writeError(w, r, http.StatusServiceUnavailable, "INDEX_NOT_READY", "index has not completed initial load", nil)
		return
	}
	events := latestEvents(snapshot.RecentEvents, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"trace_id":   traceIDFromContext(r.Context()),
		"request_id": requestIDFromContext(r.Context()),
		"freshness":  freshnessFromSnapshot(snapshot),
		"events":     events,
	})
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "NOT_FOUND", "no skiffd route matches "+r.URL.Path, []recommendedAction{
		{ID: "check_health", Command: "curl -fsS http://<skiffd>/healthz?format=json", Mutating: false},
	})
}

func (s *Server) requestID(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get("X-Request-Id")); id != "" {
		return id
	}
	return fmt.Sprintf("req_%012d", s.requestSeq.Add(1))
}

type Index interface {
	Snapshot(ctx context.Context) (Snapshot, error)
}

type Snapshot struct {
	Ready        bool
	Generation   int64
	RefreshedAt  time.Time
	Services     []ServiceSummary
	RecentEvents []schema.Event
	Findings     []Finding
}

type ServiceSummary struct {
	Service        string `json:"service"`
	Env            string `json:"env,omitempty"`
	DesiredRelease string `json:"desired_release,omitempty"`
	StableRelease  string `json:"stable_release,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type Finding struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
	Key     string `json:"key,omitempty"`
}

type StaticIndex struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

func NewStaticIndex(snapshot Snapshot) *StaticIndex {
	return &StaticIndex{snapshot: cloneSnapshot(snapshot)}
}

func (i *StaticIndex) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return cloneSnapshot(i.snapshot), nil
}

func (i *StaticIndex) Set(snapshot Snapshot) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.snapshot = cloneSnapshot(snapshot)
}

func SnapshotFromObjectStore(ctx context.Context, store objstore.ObjectStore, now time.Time) (Snapshot, error) {
	if store == nil {
		return Snapshot{}, errors.New("object store is required")
	}
	snapshot := Snapshot{
		Ready:       true,
		Generation:  1,
		RefreshedAt: now.UTC(),
	}

	serviceMetas, err := store.List(ctx, "services/", objstore.ListOptions{})
	if err != nil {
		return Snapshot{}, err
	}
	for _, meta := range serviceMetas {
		if !strings.HasSuffix(meta.Key, "/control.json") {
			continue
		}
		service, ok := serviceFromControlKey(meta.Key)
		if !ok {
			snapshot.Findings = append(snapshot.Findings, Finding{
				Code:    "MALFORMED_SERVICE_CONTROL_KEY",
				Summary: "service control key does not match services/<service>/control.json",
				Key:     meta.Key,
			})
			continue
		}
		obj, err := store.Get(ctx, meta.Key)
		if err != nil {
			return Snapshot{}, err
		}
		var control schema.ServiceControl
		if err := json.Unmarshal(obj.Body, &control); err != nil {
			snapshot.Findings = append(snapshot.Findings, Finding{
				Code:    "MALFORMED_SERVICE_CONTROL",
				Summary: err.Error(),
				Key:     meta.Key,
			})
			continue
		}
		if control.Service == "" {
			control.Service = service
		}
		snapshot.Services = append(snapshot.Services, ServiceSummary{
			Service:        control.Service,
			Env:            control.Env,
			DesiredRelease: control.DesiredRelease,
			StableRelease:  control.StableRelease,
			UpdatedAt:      control.UpdatedAt,
		})
	}
	sort.Slice(snapshot.Services, func(i, j int) bool {
		if snapshot.Services[i].Service == snapshot.Services[j].Service {
			return snapshot.Services[i].Env < snapshot.Services[j].Env
		}
		return snapshot.Services[i].Service < snapshot.Services[j].Service
	})

	events, findings, err := eventsFromObjectStore(ctx, store)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.RecentEvents = events
	snapshot.Findings = append(snapshot.Findings, findings...)
	return snapshot, nil
}

func eventsFromObjectStore(ctx context.Context, store objstore.ObjectStore) ([]schema.Event, []Finding, error) {
	var metas []objstore.ObjectMeta
	for _, prefix := range []string{"services/", "sagas/"} {
		listed, err := store.List(ctx, prefix, objstore.ListOptions{})
		if err != nil {
			return nil, nil, err
		}
		metas = append(metas, listed...)
	}

	events := make([]schema.Event, 0)
	findings := make([]Finding, 0)
	for _, meta := range metas {
		if !isEventKey(meta.Key) {
			continue
		}
		obj, err := store.Get(ctx, meta.Key)
		if err != nil {
			return nil, nil, err
		}
		var event schema.Event
		if err := json.Unmarshal(obj.Body, &event); err != nil {
			findings = append(findings, Finding{
				Code:    "MALFORMED_EVENT",
				Summary: err.Error(),
				Key:     meta.Key,
			})
			continue
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Time == events[j].Time {
			return events[i].ID < events[j].ID
		}
		return events[i].Time < events[j].Time
	})
	return events, findings, nil
}

func serviceFromControlKey(key string) (string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "services" || parts[2] != "control.json" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func isEventKey(key string) bool {
	return strings.Contains(key, "/events/") && strings.HasSuffix(key, ".json")
}

type freshness struct {
	Source      string    `json:"source"`
	Ready       bool      `json:"ready"`
	Generation  int64     `json:"generation"`
	RefreshedAt time.Time `json:"refreshed_at,omitempty"`
	Findings    []Finding `json:"findings,omitempty"`
}

func freshnessFromSnapshot(snapshot Snapshot) freshness {
	return freshness{
		Source:      "memory_index",
		Ready:       snapshot.Ready,
		Generation:  snapshot.Generation,
		RefreshedAt: snapshot.RefreshedAt,
		Findings:    snapshot.Findings,
	}
}

func latestEvents(events []schema.Event, limit int) []schema.Event {
	if limit <= 0 || limit > len(events) {
		limit = len(events)
	}
	start := len(events) - limit
	out := make([]schema.Event, 0, limit)
	for i := len(events) - 1; i >= start; i-- {
		out = append(out, cloneEvent(events[i]))
	}
	return out
}

func parseLimit(r *http.Request) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return 50, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > maxRecentEventsLimit {
		return 0, fmt.Errorf("limit must be an integer between 1 and %d", maxRecentEventsLimit)
	}
	return limit, nil
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	out := snapshot
	out.Services = append([]ServiceSummary(nil), snapshot.Services...)
	out.Findings = append([]Finding(nil), snapshot.Findings...)
	out.RecentEvents = make([]schema.Event, 0, len(snapshot.RecentEvents))
	for _, event := range snapshot.RecentEvents {
		out.RecentEvents = append(out.RecentEvents, cloneEvent(event))
	}
	return out
}

func cloneEvent(event schema.Event) schema.Event {
	out := event
	if event.Actor != nil {
		actor := *event.Actor
		out.Actor = &actor
	}
	out.Facts = append([]schema.Fact(nil), event.Facts...)
	if event.Data != nil {
		out.Data = append([]byte(nil), event.Data...)
	}
	return out
}

type requestIDKey struct{}
type traceIDKey struct{}
type actorKey struct{}

func requestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

func traceIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(traceIDKey{}).(string)
	return value
}

func traceIDForRequest(r *http.Request, requestID string) string {
	for _, candidate := range []string{
		r.Header.Get("X-Skiff-Trace-Id"),
		r.Header.Get("Traceparent"),
		r.URL.Query().Get("trace_id"),
	} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return "tr_" + strings.TrimPrefix(requestID, "req_")
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method must be "+method, nil)
	return false
}

func negotiateFormat(w http.ResponseWriter, r *http.Request, forceJSON bool) (bool, bool) {
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	if format == "" {
		if forceJSON || strings.Contains(r.Header.Get("Accept"), "application/json") {
			return true, true
		}
		return false, true
	}
	switch format {
	case "json":
		return true, true
	case "human", "text":
		if forceJSON {
			writeError(w, r, http.StatusBadRequest, "UNSUPPORTED_FORMAT", `format must be "json" for this endpoint`, nil)
			return false, false
		}
		return false, true
	default:
		writeError(w, r, http.StatusBadRequest, "UNSUPPORTED_FORMAT", `format must be "human" or "json"`, nil)
		return false, false
	}
}

type errorEnvelope struct {
	OK                 bool                `json:"ok"`
	Code               string              `json:"code"`
	Summary            string              `json:"summary"`
	TraceID            string              `json:"trace_id,omitempty"`
	RequestID          string              `json:"request_id,omitempty"`
	RecommendedActions []recommendedAction `json:"recommended_actions,omitempty"`
}

type recommendedAction struct {
	ID       string `json:"id"`
	Command  string `json:"command"`
	Mutating bool   `json:"mutating"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, summary string, actions []recommendedAction) {
	writeJSON(w, status, errorEnvelope{
		OK:                 false,
		Code:               code,
		Summary:            summary,
		TraceID:            traceIDFromContext(r.Context()),
		RequestID:          requestIDFromContext(r.Context()),
		RecommendedActions: actions,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(value)
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func redactURI(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	if parsed.User != nil {
		parsed.User = url.User("redacted")
	}
	return parsed.String()
}
