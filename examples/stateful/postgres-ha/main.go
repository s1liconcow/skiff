package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

const stateFile = "postgres-ha-state.json"

type memberState struct {
	Mode       string            `json:"mode"`
	Member     int               `json:"member"`
	Members    int               `json:"members"`
	Generation int64             `json:"generation"`
	Role       string            `json:"role"`
	Term       int64             `json:"term"`
	Leader     int               `json:"leader"`
	Lag        int64             `json:"lag"`
	Failures   map[string]string `json:"failures,omitempty"`
	UpdatedAt  string            `json:"updated_at"`
}

type config struct {
	Addr       string
	StateDir   string
	PGData     string
	PGPort     int
	Mode       string
	Member     int
	Members    int
	Generation int64
}

type app struct {
	mu      sync.Mutex
	cfg     config
	state   memberState
	path    string
	db      *sql.DB
	pg      *exec.Cmd
	started time.Time
}

func main() {
	cfg := configFromFlags()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := newApp(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer app.close()

	if err := app.startPostgres(ctx); err != nil {
		log.Fatal(err)
	}
	go func() {
		<-ctx.Done()
		app.close()
	}()

	log.Printf("postgres-ha demo member listening on %s member=%d role=%s state=%s pgdata=%s", cfg.Addr, app.state.Member, app.state.Role, app.path, cfg.PGData)
	server := &http.Server{Addr: cfg.Addr, Handler: app}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func configFromFlags() config {
	var cfg config
	flag.StringVar(&cfg.Addr, "addr", envDefault("SKIFF_POSTGRES_HA_ADDR", ":8008"), "admin API listen address")
	flag.StringVar(&cfg.StateDir, "state-dir", firstNonEmpty(os.Getenv("SKIFF_POSTGRES_HA_STATE_DIR"), os.Getenv("SKIFF_STATEFUL_VOLUME_MOUNT_PATH"), os.Getenv("SKIFF_STATEFUL_MOUNT_PATH"), "/data"), "durable state directory")
	flag.StringVar(&cfg.Mode, "mode", envDefault("SKIFF_POSTGRES_HA_MODE", "primary-replica"), "operation semantics mode")
	flag.IntVar(&cfg.PGPort, "postgres-port", envInt("POSTGRES_PORT", 5432), "Postgres listen port")
	flag.IntVar(&cfg.Member, "member", envInt("SKIFF_STATEFUL_MEMBER", 0), "member ordinal")
	flag.IntVar(&cfg.Members, "members", envInt("SKIFF_POSTGRES_HA_MEMBERS", 3), "cluster member count")
	flag.Int64Var(&cfg.Generation, "generation", envInt64("SKIFF_STATEFUL_GENERATION", 1), "member generation")
	flag.Parse()
	cfg.PGData = firstNonEmpty(os.Getenv("SKIFF_POSTGRES_HA_PGDATA"), filepath.Join(cfg.StateDir, "pgdata"))
	return cfg
}

func newApp(cfg config) (*app, error) {
	if cfg.Members <= 0 {
		return nil, errors.New("members must be positive")
	}
	if cfg.Member < 0 || cfg.Member >= cfg.Members {
		return nil, fmt.Errorf("member %d outside cluster size %d", cfg.Member, cfg.Members)
	}
	if strings.TrimSpace(cfg.StateDir) == "" {
		return nil, errors.New("state directory is required")
	}
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(cfg.StateDir, stateFile)
	state, err := loadState(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		state = defaultState(cfg)
		if err := saveState(path, state); err != nil {
			return nil, err
		}
	}
	state.Generation = maxInt64(state.Generation, cfg.Generation)
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", cfg.PGPort))
	if err != nil {
		return nil, err
	}
	return &app{cfg: cfg, state: state, path: path, db: db, started: time.Now().UTC()}, nil
}

func (a *app) startPostgres(ctx context.Context) error {
	if err := os.MkdirAll(a.cfg.PGData, 0o700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "/usr/local/bin/docker-entrypoint.sh", "postgres", "-c", "listen_addresses=*", "-c", fmt.Sprintf("port=%d", a.cfg.PGPort))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"PGDATA="+a.cfg.PGData,
		"POSTGRES_USER=postgres",
		"POSTGRES_DB=postgres",
		"POSTGRES_HOST_AUTH_METHOD=trust",
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start postgres: %w", err)
	}
	a.pg = cmd
	go func() {
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			log.Printf("postgres exited: %v", err)
		}
	}()
	return nil
}

func (a *app) close() {
	if a.db != nil {
		_ = a.db.Close()
	}
	if a.pg != nil && a.pg.Process != nil {
		_ = a.pg.Process.Signal(syscall.SIGTERM)
	}
}

func defaultState(cfg config) memberState {
	role := "replica"
	leader := 0
	if cfg.Member == 0 {
		role = "primary"
	}
	return memberState{
		Mode:       firstNonEmpty(cfg.Mode, "primary-replica"),
		Member:     cfg.Member,
		Members:    cfg.Members,
		Generation: maxInt64(cfg.Generation, 1),
		Role:       role,
		Term:       1,
		Leader:     leader,
		Lag:        0,
		Failures:   map[string]string{},
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		a.handleHealth(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/state":
		a.handleState(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/promote":
		a.mutate(w, "promote", promote)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/stepdown":
		a.mutate(w, "stepdown", stepdown)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/catch-up":
		a.mutate(w, "catch-up", catchUp)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/fail":
		a.handleFail(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/recover":
		a.handleRecover(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/demo/write":
		a.handleDemoWrite(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/demo/read":
		a.handleDemoRead(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *app) handleHealth(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	failed := a.state.Failures["health"]
	a.mu.Unlock()
	if failed != "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "failure": failed})
		return
	}
	if err := a.postgresReady(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *app) handleState(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	state := a.state
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, state)
}

func (a *app) handleFail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type string `json:"type"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	failType := firstNonEmpty(body.Type, r.URL.Query().Get("type"))
	if failType == "" {
		http.Error(w, "failure type is required", http.StatusBadRequest)
		return
	}
	a.mutate(w, "fail", func(s *memberState) error {
		if s.Failures == nil {
			s.Failures = map[string]string{}
		}
		switch failType {
		case "replica-lag-too-high":
			s.Lag = 1 << 30
			s.Failures["replica_lag"] = failType
		default:
			s.Failures[failType] = failType
		}
		return nil
	})
}

func (a *app) handleRecover(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.state.Failures = map[string]string{}
	a.state.Lag = 0
	a.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	state := a.state
	err := saveState(a.path, state)
	a.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": "recover", "state": state})
}

func (a *app) handleDemoWrite(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	role := a.state.Role
	member := a.state.Member
	a.mu.Unlock()
	if role != "primary" && role != "leader" {
		http.Error(w, "writes are accepted only by the current primary", http.StatusConflict)
		return
	}
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body.Key = firstNonEmpty(strings.TrimSpace(body.Key), "demo")
	body.Value = firstNonEmpty(body.Value, time.Now().UTC().Format(time.RFC3339Nano))
	if err := a.ensureDemoTable(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO skiff_demo_kv (key, value, member) VALUES ($1, $2, $3)`, body.Key, body.Value, member)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "member": member, "role": role, "key": body.Key, "value": body.Value})
}

func (a *app) handleDemoRead(w http.ResponseWriter, r *http.Request) {
	key := firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("key")), "demo")
	if err := a.ensureDemoTable(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT key, value, member, created_at FROM skiff_demo_kv WHERE key = $1 ORDER BY id DESC LIMIT 20`, key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var values []map[string]any
	for rows.Next() {
		var k, v string
		var member int
		var created time.Time
		if err := rows.Scan(&k, &v, &member, &created); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		values = append(values, map[string]any{"key": k, "value": v, "member": member, "created_at": created})
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key, "values": values})
}

func (a *app) ensureDemoTable(ctx context.Context) error {
	if err := a.postgresReady(ctx); err != nil {
		return err
	}
	_, err := a.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS skiff_demo_kv (
	id BIGSERIAL PRIMARY KEY,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	member INTEGER NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`)
	return err
}

func (a *app) postgresReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return a.db.PingContext(ctx)
}

func (a *app) mutate(w http.ResponseWriter, action string, fn func(*memberState) error) {
	a.mu.Lock()
	err := fn(&a.state)
	if err == nil {
		a.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		err = saveState(a.path, a.state)
	}
	state := a.state
	a.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": action, "state": state})
}

func promote(s *memberState) error {
	if failure := firstFailure(s.Failures); failure != "" {
		return fmt.Errorf("member has active failure: %s", failure)
	}
	if s.Lag > 0 {
		return fmt.Errorf("replica lag is %d bytes", s.Lag)
	}
	s.Role = "primary"
	s.Leader = s.Member
	s.Term++
	return nil
}

func stepdown(s *memberState) error {
	if s.Role != "primary" && s.Role != "leader" {
		return nil
	}
	s.Role = "replica"
	if s.Members > 1 {
		s.Leader = (s.Member + 1) % s.Members
	}
	s.Term++
	return nil
}

func catchUp(s *memberState) error {
	delete(s.Failures, "replica_lag")
	s.Lag = 0
	return nil
}

func firstFailure(failures map[string]string) string {
	for key, value := range failures {
		if value != "" {
			return key + "=" + value
		}
	}
	return ""
}

func loadState(path string) (memberState, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return memberState{}, err
	}
	var state memberState
	if err := json.Unmarshal(body, &state); err != nil {
		return memberState{}, err
	}
	if state.Failures == nil {
		state.Failures = map[string]string{}
	}
	return state, nil
}

func saveState(path string, state memberState) error {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envDefault(name, fallback string) string {
	return firstNonEmpty(os.Getenv(name), fallback)
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
