package runner

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/s1liconcow/skiff/internal/state/canonical"
)

const RunnerEventSchemaVersion = "skiff.runner-event/v1"

type State string

const (
	StateBooting           State = "Booting"
	StateFetchingManifest  State = "FetchingManifest"
	StateVerifyingRelease  State = "VerifyingRelease"
	StatePreparingArtifact State = "PreparingArtifact"
	StateRenderingConfig   State = "RenderingConfig"
	StateStartingWorkload  State = "StartingWorkload"
	StateWaitingForHealth  State = "WaitingForHealth"
	StateServing           State = "Serving"
	StateDraining          State = "Draining"
	StateStopping          State = "Stopping"
	StateStopped           State = "Stopped"
	StateFailed            State = "Failed"
)

type StateEvent struct {
	SchemaVersion string       `json:"schema_version"`
	Time          string       `json:"time"`
	State         State        `json:"state"`
	Service       string       `json:"service"`
	Env           string       `json:"env"`
	ReleaseID     string       `json:"release_id,omitempty"`
	TraceID       string       `json:"trace_id,omitempty"`
	Identity      *Identity    `json:"identity,omitempty"`
	Health        HealthStatus `json:"health,omitempty"`
	UnitName      string       `json:"unit_name,omitempty"`
	Summary       string       `json:"summary"`
	Error         string       `json:"error,omitempty"`
}

type EventSink interface {
	EmitRunnerEvent(ctx context.Context, event StateEvent) error
}

type FileEventSink struct {
	Path string
	mu   sync.Mutex
}

func (s *FileEventSink) EmitRunnerEvent(ctx context.Context, event StateEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := s.Path
	if path == "" {
		path = DefaultEventsPath
	}

	body, err := canonical.Marshal(event)
	if err != nil {
		return err
	}
	body = append(body, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(body)
	return err
}
