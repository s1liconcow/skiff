package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/s1liconcow/skiff/internal/release"
	"github.com/s1liconcow/skiff/internal/state/canonical"
)

const (
	RunnerStateSchemaVersion = "skiff.runner-state/v1"
	DefaultStatePath         = "/var/lib/skiff/runner/state.json"
	DefaultEventsPath        = "/var/lib/skiff/runner/events.jsonl"
)

var ErrStateNotFound = errors.New("runner local state not found")

type LocalState struct {
	SchemaVersion              string                     `json:"schema_version"`
	Service                    string                     `json:"service"`
	Env                        string                     `json:"env"`
	CurrentState               State                      `json:"current_state"`
	Health                     HealthStatus               `json:"health,omitempty"`
	LastAcceptedRelease        string                     `json:"last_accepted_release"`
	LastAcceptedReleaseCreated string                     `json:"last_accepted_release_created_at,omitempty"`
	ReleaseDigest              string                     `json:"release_digest,omitempty"`
	RuntimeManifestDigest      string                     `json:"runtime_manifest_digest,omitempty"`
	ControlKey                 string                     `json:"control_key"`
	ReleaseKey                 string                     `json:"release_key"`
	RuntimeManifestKey         string                     `json:"runtime_manifest_key"`
	WorkloadUnit               string                     `json:"workload_unit,omitempty"`
	TraceID                    string                     `json:"trace_id,omitempty"`
	UpdatedAt                  string                     `json:"updated_at"`
	Identity                   *Identity                  `json:"identity,omitempty"`
	Verification               release.VerificationResult `json:"verification"`
}

type StateStore interface {
	LoadState(ctx context.Context) (*LocalState, error)
	SaveState(ctx context.Context, state LocalState) error
}

type FileStateStore struct {
	Path string
}

func (s FileStateStore) LoadState(ctx context.Context) (*LocalState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := s.Path
	if path == "" {
		path = DefaultStatePath
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrStateNotFound
		}
		return nil, err
	}
	var state LocalState
	if err := canonical.UnmarshalStrict(body, &state); err != nil {
		return nil, fmt.Errorf("read runner local state %q: %w", path, err)
	}
	if state.SchemaVersion != RunnerStateSchemaVersion {
		return nil, fmt.Errorf("read runner local state %q: unsupported schema version %q", path, state.SchemaVersion)
	}
	return &state, nil
}

func (s FileStateStore) SaveState(ctx context.Context, state LocalState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := s.Path
	if path == "" {
		path = DefaultStatePath
	}
	if state.SchemaVersion == "" {
		state.SchemaVersion = RunnerStateSchemaVersion
	}
	body, err := canonical.Marshal(state)
	if err != nil {
		return err
	}
	body = append(body, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
