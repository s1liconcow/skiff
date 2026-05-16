package index

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Reader interface {
	Snapshot(ctx context.Context) (Snapshot, error)
}

type Snapshot struct {
	Ready          bool
	Generation     int64
	RefreshedAt    time.Time
	LastFullScanAt time.Time
	Services       []ServiceSummary
	Sagas          []SagaSummary
	Operations     []OperationSummary
	Resources      []ResourceSummary
	RecentEvents   []schema.Event
	Findings       []Finding
}

type ServiceSummary struct {
	Service        string `json:"service"`
	Env            string `json:"env,omitempty"`
	DesiredRelease string `json:"desired_release,omitempty"`
	StableRelease  string `json:"stable_release,omitempty"`
	OperationID    string `json:"operation_id,omitempty"`
	OperationKind  string `json:"operation_kind,omitempty"`
	OperationState string `json:"operation_state,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type SagaSummary struct {
	SagaID       string            `json:"saga_id"`
	Status       schema.SagaStatus `json:"status"`
	CurrentSteps []string          `json:"current_steps,omitempty"`
	UpdatedAt    string            `json:"updated_at,omitempty"`
	TraceID      string            `json:"trace_id,omitempty"`
}

type OperationSummary struct {
	OperationID        string                        `json:"operation_id"`
	Service            string                        `json:"service"`
	Env                string                        `json:"env,omitempty"`
	Status             schema.OperationStatus        `json:"status"`
	UpdatedAt          string                        `json:"updated_at,omitempty"`
	TraceID            string                        `json:"trace_id,omitempty"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations,omitempty"`
}

type ResourceSummary struct {
	Provider    string `json:"provider"`
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Service     string `json:"service,omitempty"`
	Env         string `json:"env,omitempty"`
	LogicalKind string `json:"logical_kind,omitempty"`
	LogicalName string `json:"logical_name,omitempty"`
	ObservedAt  string `json:"observed_at,omitempty"`
}

type Finding struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
	Key     string `json:"key,omitempty"`
}

type Freshness struct {
	Source           string    `json:"source"`
	Ready            bool      `json:"ready"`
	Generation       int64     `json:"generation"`
	RefreshedAt      time.Time `json:"refreshed_at,omitempty"`
	LastFullScanAt   time.Time `json:"last_full_scan_at,omitempty"`
	FreshnessSeconds int64     `json:"freshness_seconds"`
	Findings         []Finding `json:"findings,omitempty"`
}

func FreshnessFromSnapshot(snapshot Snapshot, now time.Time, source string) Freshness {
	if source == "" {
		source = "memory"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var age int64
	if !snapshot.RefreshedAt.IsZero() {
		age = int64(now.UTC().Sub(snapshot.RefreshedAt.UTC()).Seconds())
		if age < 0 {
			age = 0
		}
	}
	return Freshness{
		Source:           source,
		Ready:            snapshot.Ready,
		Generation:       snapshot.Generation,
		RefreshedAt:      snapshot.RefreshedAt,
		LastFullScanAt:   snapshot.LastFullScanAt,
		FreshnessSeconds: age,
		Findings:         append([]Finding(nil), snapshot.Findings...),
	}
}

type Static struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

func NewStatic(snapshot Snapshot) *Static {
	return &Static{snapshot: CloneSnapshot(snapshot)}
}

func (i *Static) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return CloneSnapshot(i.snapshot), nil
}

func (i *Static) Set(snapshot Snapshot) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.snapshot = CloneSnapshot(snapshot)
}

type AtomicSnapshot struct {
	current atomic.Value // *Snapshot
}

func (s *AtomicSnapshot) Load() Snapshot {
	current, _ := s.current.Load().(*Snapshot)
	if current == nil {
		return Snapshot{}
	}
	return CloneSnapshot(*current)
}

func (s *AtomicSnapshot) Store(snapshot Snapshot) {
	clone := CloneSnapshot(snapshot)
	s.current.Store(&clone)
}

func CloneSnapshot(snapshot Snapshot) Snapshot {
	out := snapshot
	out.Services = append([]ServiceSummary(nil), snapshot.Services...)
	out.Sagas = append([]SagaSummary(nil), snapshot.Sagas...)
	out.Operations = make([]OperationSummary, 0, len(snapshot.Operations))
	for _, operation := range snapshot.Operations {
		operation.ProviderOperations = append([]schema.ProviderOperationRef(nil), operation.ProviderOperations...)
		out.Operations = append(out.Operations, operation)
	}
	out.Resources = append([]ResourceSummary(nil), snapshot.Resources...)
	out.Findings = append([]Finding(nil), snapshot.Findings...)
	out.RecentEvents = make([]schema.Event, 0, len(snapshot.RecentEvents))
	for _, event := range snapshot.RecentEvents {
		out.RecentEvents = append(out.RecentEvents, CloneEvent(event))
	}
	return out
}

func CloneEvent(event schema.Event) schema.Event {
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
