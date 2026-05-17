package tui

import (
	"context"
	"time"

	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type SagasClient interface {
	Sagas(ctx context.Context, opts client.SagaOptions) (*client.SagaList, error)
}

type Options struct {
	Client   client.Interface
	Sagas    SagasClient
	Service  string
	TraceID  string
	Fresh    bool
	ReadOnly bool
	NoColor  bool
	Width    int
	Height   int
	Now      func() time.Time
}

type Dashboard struct {
	Status    client.Status        `json:"status"`
	Sagas     []client.SagaSummary `json:"sagas,omitempty"`
	Events    []schema.Event       `json:"events,omitempty"`
	Freshness client.Freshness     `json:"freshness"`
	Source    string               `json:"source,omitempty"`
}

type Action struct {
	ID            string               `json:"id"`
	Label         string               `json:"label"`
	Key           string               `json:"key"`
	Command       string               `json:"command"`
	Mutating      bool                 `json:"mutating"`
	Risk          schema.Risk          `json:"risk,omitempty"`
	Reversibility schema.Reversibility `json:"reversibility,omitempty"`
	Allowed       bool                 `json:"allowed"`
	Summary       string               `json:"summary,omitempty"`
}
