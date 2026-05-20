package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const stateFile = "opsem-state.json"

type state struct {
	Mode       string            `json:"mode"`
	Member     int               `json:"member"`
	Members    int               `json:"members"`
	Generation int64             `json:"generation"`
	Role       string            `json:"role"`
	Term       int64             `json:"term"`
	Leader     int               `json:"leader"`
	Lag        int64             `json:"lag"`
	Quorum     quorumState       `json:"quorum"`
	Partitions []partitionState  `json:"partitions,omitempty"`
	Slots      slotState         `json:"slots,omitempty"`
	Shards     []shardState      `json:"shards,omitempty"`
	Relocation relocationState   `json:"relocation,omitempty"`
	Draining   bool              `json:"draining"`
	Failures   map[string]string `json:"failures,omitempty"`
	UpdatedAt  string            `json:"updated_at"`
}

type quorumState struct {
	Required  int  `json:"required"`
	Available int  `json:"available"`
	Healthy   bool `json:"healthy"`
}

type partitionState struct {
	Topic     string `json:"topic"`
	Partition int    `json:"partition"`
	Leader    int    `json:"leader"`
	Replicas  []int  `json:"replicas"`
	ISR       []int  `json:"isr"`
	MinISR    int    `json:"min_isr"`
}

type slotState struct {
	Owned      []slotRange `json:"owned"`
	Missing    []int       `json:"missing,omitempty"`
	CoverageOK bool        `json:"coverage_ok"`
}

type slotRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type shardState struct {
	Name     string `json:"name"`
	Primary  int    `json:"primary"`
	Replicas []int  `json:"replicas"`
	Health   string `json:"health"`
}

type relocationState struct {
	Active bool   `json:"active"`
	From   int    `json:"from,omitempty"`
	To     int    `json:"to,omitempty"`
	Status string `json:"status,omitempty"`
}

type app struct {
	mu       sync.Mutex
	path     string
	state    state
	now      func() time.Time
	onChange func(state)
}

type config struct {
	Addr       string
	StateDir   string
	Mode       string
	Member     int
	Members    int
	Generation int64
}

func main() {
	cfg := configFromFlags()
	app, err := newApp(cfg)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("skiff-opsem listening on %s mode=%s member=%d state=%s", cfg.Addr, app.state.Mode, app.state.Member, app.path)
	if err := http.ListenAndServe(cfg.Addr, app); err != nil {
		log.Fatal(err)
	}
}

func configFromFlags() config {
	var cfg config
	flag.StringVar(&cfg.Addr, "addr", envDefault("SKIFF_OPSEM_ADDR", ":8080"), "listen address")
	flag.StringVar(&cfg.StateDir, "state-dir", firstNonEmpty(os.Getenv("SKIFF_OPSEM_STATE_DIR"), os.Getenv("SKIFF_STATEFUL_VOLUME_MOUNT_PATH"), os.Getenv("SKIFF_STATEFUL_MOUNT_PATH"), "/data"), "durable state directory")
	flag.StringVar(&cfg.Mode, "mode", envDefault("SKIFF_OPSEM_MODE", "primary-replica"), "semantic mode")
	flag.IntVar(&cfg.Member, "member", envInt("SKIFF_STATEFUL_MEMBER", 0), "member ordinal")
	flag.IntVar(&cfg.Members, "members", envInt("SKIFF_OPSEM_MEMBERS", 3), "cluster member count")
	flag.Int64Var(&cfg.Generation, "generation", envInt64("SKIFF_STATEFUL_GENERATION", 1), "member generation")
	flag.Parse()
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
	a := &app{path: filepath.Join(cfg.StateDir, stateFile), now: func() time.Time { return time.Now().UTC() }}
	loaded, err := loadState(a.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		loaded = defaultState(cfg, a.now())
		if err := saveState(a.path, loaded); err != nil {
			return nil, err
		}
	}
	loaded.Generation = maxInt64(loaded.Generation, cfg.Generation)
	a.state = loaded
	return a, nil
}

func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		a.handleHealth(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/state":
		a.handleState(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/stepdown":
		a.mutate(w, "stepdown", stepdown)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/promote":
		a.mutate(w, "promote", promote)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/drain":
		a.mutate(w, "drain", drain)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/catch-up":
		a.mutate(w, "catch-up", catchUp)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/fail":
		a.handleFail(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/recover":
		a.handleRecover(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *app) handleHealth(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, failed := a.state.Failures["health"]; failed {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "state": a.state})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "member": a.state.Member, "generation": a.state.Generation})
}

func (a *app) handleState(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	writeJSON(w, http.StatusOK, a.state)
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
	a.mutate(w, "fail", func(s *state) error {
		injectFailure(s, failType)
		return nil
	})
}

func (a *app) handleRecover(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	cfg := config{Mode: a.state.Mode, Member: a.state.Member, Members: a.state.Members, Generation: a.state.Generation, StateDir: filepath.Dir(a.path)}
	next := defaultState(cfg, a.now())
	next.Term = maxInt64(a.state.Term, next.Term)
	a.state = next
	err := a.persistLocked()
	state := a.state
	a.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": "recover", "state": state})
}

func (a *app) mutate(w http.ResponseWriter, action string, fn func(*state) error) {
	a.mu.Lock()
	err := fn(&a.state)
	if err == nil {
		a.state.UpdatedAt = a.now().Format(time.RFC3339Nano)
		err = a.persistLocked()
	}
	state := a.state
	a.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": action, "state": state})
}

func (a *app) persistLocked() error {
	if err := saveState(a.path, a.state); err != nil {
		return err
	}
	if a.onChange != nil {
		a.onChange(a.state)
	}
	return nil
}

func stepdown(s *state) error {
	if s.Role != "primary" && s.Role != "leader" {
		return nil
	}
	nextLeader := (s.Member + 1) % s.Members
	s.Role = "replica"
	s.Leader = nextLeader
	s.Term++
	for i := range s.Partitions {
		s.Partitions[i].Leader = nextLeader
	}
	return nil
}

func promote(s *state) error {
	s.Role = roleForMode(s.Mode, true)
	s.Leader = s.Member
	s.Term++
	for i := range s.Partitions {
		s.Partitions[i].Leader = s.Member
	}
	return nil
}

func drain(s *state) error {
	s.Draining = true
	s.Relocation = relocationState{Active: true, From: s.Member, To: (s.Member + 1) % s.Members, Status: "draining"}
	return nil
}

func catchUp(s *state) error {
	if _, failed := s.Failures["catch-up-timeout"]; failed {
		return errors.New("catch-up timed out")
	}
	s.Lag = 0
	s.Quorum = quorum(s.Members)
	for i := range s.Partitions {
		s.Partitions[i].ISR = ordinalSlice(s.Members)
	}
	s.Slots.Missing = nil
	s.Slots.CoverageOK = true
	for i := range s.Shards {
		s.Shards[i].Health = "green"
	}
	delete(s.Failures, "replica-lag-too-high")
	delete(s.Failures, "isr-below-min")
	delete(s.Failures, "slot-coverage-missing")
	delete(s.Failures, "red-shard-health")
	return nil
}

func injectFailure(s *state, failType string) {
	if s.Failures == nil {
		s.Failures = map[string]string{}
	}
	s.Failures[failType] = "injected"
	switch failType {
	case "replica-lag-too-high":
		s.Lag = 9999
	case "quorum-would-be-lost":
		s.Quorum.Available = maxInt(1, s.Quorum.Required-1)
		s.Quorum.Healthy = false
	case "isr-below-min":
		for i := range s.Partitions {
			s.Partitions[i].ISR = []int{s.Partitions[i].Leader}
		}
	case "slot-coverage-missing":
		s.Slots.Missing = []int{42}
		s.Slots.CoverageOK = false
	case "red-shard-health":
		if len(s.Shards) > 0 {
			s.Shards[0].Health = "red"
		}
	case "catch-up-timeout":
	case "health":
	default:
		s.Failures[failType] = "unknown failure marker"
	}
}

func defaultState(cfg config, now time.Time) state {
	mode := firstNonEmpty(cfg.Mode, "primary-replica")
	leader := 0
	primary := cfg.Member == leader
	s := state{
		Mode:       mode,
		Member:     cfg.Member,
		Members:    cfg.Members,
		Generation: maxInt64(cfg.Generation, 1),
		Role:       roleForMode(mode, primary),
		Term:       1,
		Leader:     leader,
		Lag:        0,
		Quorum:     quorum(cfg.Members),
		Slots:      defaultSlots(cfg.Member),
		Shards:     defaultShards(cfg.Members),
		Failures:   map[string]string{},
		UpdatedAt:  now.Format(time.RFC3339Nano),
	}
	s.Partitions = defaultPartitions(cfg.Members, leader)
	if mode == "primary-replica" {
		s.Partitions = nil
		s.Slots = slotState{}
		s.Shards = nil
	}
	if mode == "raft-groups" {
		s.Slots = slotState{}
		s.Shards = nil
	}
	if mode == "partition-isr" {
		s.Slots = slotState{}
		s.Shards = nil
	}
	if mode == "slot-cluster" {
		s.Partitions = nil
		s.Shards = nil
	}
	if mode == "shard-cluster" {
		s.Partitions = nil
		s.Slots = slotState{}
	}
	return s
}

func roleForMode(mode string, primary bool) string {
	if primary {
		if mode == "raft-groups" || mode == "partition-isr" {
			return "leader"
		}
		return "primary"
	}
	if mode == "slot-cluster" || mode == "shard-cluster" {
		return "worker"
	}
	return "replica"
}

func quorum(members int) quorumState {
	required := members/2 + 1
	return quorumState{Required: required, Available: members, Healthy: members >= required}
}

func defaultPartitions(members, leader int) []partitionState {
	return []partitionState{{
		Topic:     "orders",
		Partition: 0,
		Leader:    leader,
		Replicas:  ordinalSlice(members),
		ISR:       ordinalSlice(members),
		MinISR:    minInt(2, members),
	}}
}

func defaultSlots(member int) slotState {
	start := member * 100
	return slotState{Owned: []slotRange{{Start: start, End: start + 99}}, CoverageOK: true}
}

func defaultShards(members int) []shardState {
	return []shardState{{
		Name:     "orders-000",
		Primary:  0,
		Replicas: ordinalSlice(members),
		Health:   "green",
	}}
}

func loadState(path string) (state, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return state{}, err
	}
	var s state
	if err := json.Unmarshal(body, &s); err != nil {
		return state{}, err
	}
	if s.Failures == nil {
		s.Failures = map[string]string{}
	}
	return s, nil
}

func saveState(path string, s state) error {
	tmp := path + ".tmp"
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func ordinalSlice(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
