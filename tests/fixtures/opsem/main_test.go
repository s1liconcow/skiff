package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultModesExposeExpectedSemantics(t *testing.T) {
	modes := []string{"primary-replica", "raft-groups", "partition-isr", "slot-cluster", "shard-cluster"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t, config{Mode: mode, Member: 1, Members: 3, Generation: 4, StateDir: t.TempDir()})
			got := getState(t, a)
			if got.Mode != mode || got.Member != 1 || got.Generation != 4 || got.Quorum.Required != 2 || !got.Quorum.Healthy {
				t.Fatalf("unexpected state: %+v", got)
			}
			switch mode {
			case "partition-isr":
				if len(got.Partitions) != 1 || len(got.Partitions[0].ISR) != 3 || got.Partitions[0].MinISR != 2 {
					t.Fatalf("partition-isr state missing partition semantics: %+v", got)
				}
			case "slot-cluster":
				if len(got.Slots.Owned) != 1 || !got.Slots.CoverageOK {
					t.Fatalf("slot state missing coverage: %+v", got)
				}
			case "shard-cluster":
				if len(got.Shards) != 1 || got.Shards[0].Health != "green" {
					t.Fatalf("shard state missing health: %+v", got)
				}
			}
		})
	}
}

func TestStatePersistsAndReloadsFromMountedVolume(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, config{Mode: "primary-replica", Member: 0, Members: 3, Generation: 1, StateDir: dir})
	post(t, a, "/admin/drain", nil, http.StatusOK)
	reloaded := newTestApp(t, config{Mode: "primary-replica", Member: 0, Members: 3, Generation: 2, StateDir: dir})
	got := getState(t, reloaded)
	if !got.Draining || !got.Relocation.Active || got.Generation != 2 {
		t.Fatalf("state did not persist/reload with generation bump: %+v", got)
	}
	if _, err := loadState(filepath.Join(dir, stateFile)); err != nil {
		t.Fatalf("load state file: %v", err)
	}
}

func TestFailureInjectionAndRecovery(t *testing.T) {
	a := newTestApp(t, config{Mode: "partition-isr", Member: 1, Members: 3, Generation: 1, StateDir: t.TempDir()})
	post(t, a, "/admin/fail", map[string]string{"type": "replica-lag-too-high"}, http.StatusOK)
	post(t, a, "/admin/fail", map[string]string{"type": "isr-below-min"}, http.StatusOK)
	post(t, a, "/admin/fail", map[string]string{"type": "quorum-would-be-lost"}, http.StatusOK)
	got := getState(t, a)
	if got.Lag < 9999 || got.Quorum.Healthy || len(got.Partitions[0].ISR) >= got.Partitions[0].MinISR {
		t.Fatalf("unsafe injected state not reflected: %+v", got)
	}
	post(t, a, "/admin/catch-up", nil, http.StatusOK)
	post(t, a, "/admin/recover", nil, http.StatusOK)
	got = getState(t, a)
	if got.Lag != 0 || !got.Quorum.Healthy || len(got.Partitions[0].ISR) != 3 || len(got.Failures) != 0 {
		t.Fatalf("recover did not restore safe state: %+v", got)
	}
}

func TestCatchUpTimeoutReturnsConflict(t *testing.T) {
	a := newTestApp(t, config{Mode: "raft-groups", Member: 2, Members: 3, Generation: 1, StateDir: t.TempDir()})
	post(t, a, "/admin/fail", map[string]string{"type": "catch-up-timeout"}, http.StatusOK)
	post(t, a, "/admin/catch-up", nil, http.StatusConflict)
}

func TestLeadershipAndRelocationAPIs(t *testing.T) {
	a := newTestApp(t, config{Mode: "raft-groups", Member: 0, Members: 3, Generation: 1, StateDir: t.TempDir()})
	post(t, a, "/admin/stepdown", nil, http.StatusOK)
	got := getState(t, a)
	if got.Role != "replica" || got.Leader != 1 || got.Term != 2 {
		t.Fatalf("stepdown did not move leadership: %+v", got)
	}
	post(t, a, "/admin/promote", nil, http.StatusOK)
	got = getState(t, a)
	if got.Role != "leader" || got.Leader != 0 || got.Term != 3 {
		t.Fatalf("promote did not restore leadership: %+v", got)
	}
	post(t, a, "/admin/drain", nil, http.StatusOK)
	got = getState(t, a)
	if !got.Draining || !got.Relocation.Active || got.Relocation.To != 1 {
		t.Fatalf("drain did not start relocation: %+v", got)
	}
}

func newTestApp(t *testing.T, cfg config) *app {
	t.Helper()
	a, err := newApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Date(2026, 5, 20, 4, 10, 0, 0, time.UTC) }
	return a
}

func getState(t *testing.T, a *app) state {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/state", nil)
	a.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/state status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got state
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func post(t *testing.T, a *app, path string, body any, wantStatus int) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	rr := httptest.NewRecorder()
	a.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("POST %s status=%d want=%d body=%s", path, rr.Code, wantStatus, rr.Body.String())
	}
}
