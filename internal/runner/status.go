package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
)

const RunnerStatusSchemaVersion = "skiff.runner-status/v1"

type RunnerStatus struct {
	OK            bool         `json:"ok"`
	SchemaVersion string       `json:"schema_version"`
	Service       string       `json:"service"`
	Env           string       `json:"env"`
	ReleaseID     string       `json:"release"`
	State         State        `json:"state"`
	Health        HealthStatus `json:"health"`
	UnitName      string       `json:"unit_name,omitempty"`
	TraceID       string       `json:"trace_id,omitempty"`
	UpdatedAt     string       `json:"updated_at"`
	Identity      *Identity    `json:"identity,omitempty"`
}

func StatusFromState(state LocalState) RunnerStatus {
	health := state.Health
	if health == "" {
		health = HealthUnknown
	}
	return RunnerStatus{
		OK:            state.CurrentState != StateFailed,
		SchemaVersion: RunnerStatusSchemaVersion,
		Service:       state.Service,
		Env:           state.Env,
		ReleaseID:     state.LastAcceptedRelease,
		State:         state.CurrentState,
		Health:        health,
		UnitName:      state.WorkloadUnit,
		TraceID:       state.TraceID,
		UpdatedAt:     state.UpdatedAt,
		Identity:      state.Identity,
	}
}

func NewStatusHandler(store StateStore) http.Handler {
	if store == nil {
		store = FileStateStore{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		state, err := store.LoadState(r.Context())
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, ErrStateNotFound) {
				status = http.StatusNotFound
			}
			writeStatusError(w, status, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(StatusFromState(*state))
	})
}

func ListenAndServeLocalStatus(ctx context.Context, addr string, store StateStore) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if addr == "" {
		addr = "127.0.0.1:6060"
	}
	if err := validateLocalListenAddress(addr); err != nil {
		return err
	}
	server := &http.Server{Handler: NewStatusHandler(store)}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func validateLocalListenAddress(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("runner status address %q is invalid: %w", addr, err)
	}
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		return fmt.Errorf("runner status address %q must bind to localhost or loopback", addr)
	}
}

func writeStatusError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Summary string `json:"summary"`
	}{
		OK:      false,
		Code:    "RUNNER_STATUS_UNAVAILABLE",
		Summary: err.Error(),
	})
}
