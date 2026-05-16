package doctor

import (
	"context"

	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

type Options struct {
	Service string
	TraceID string
	Binary  string
}

type Result struct {
	TraceID            string                  `json:"trace_id,omitempty"`
	Service            string                  `json:"service,omitempty"`
	Env                string                  `json:"env,omitempty"`
	Provider           string                  `json:"provider,omitempty"`
	Region             string                  `json:"region,omitempty"`
	Source             string                  `json:"source,omitempty"`
	Health             string                  `json:"health"`
	Freshness          servicestatus.Freshness `json:"freshness"`
	Facts              []Evidence              `json:"facts"`
	Findings           []Finding               `json:"findings"`
	Hypotheses         []Hypothesis            `json:"hypotheses"`
	RecommendedActions []RecommendedAction     `json:"recommended_actions"`
}

type Evidence struct {
	Type       string `json:"type"`
	Message    string `json:"message"`
	Service    string `json:"service,omitempty"`
	Source     string `json:"source,omitempty"`
	EventID    string `json:"event_id,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
}

type Finding struct {
	ID         string     `json:"id"`
	Code       string     `json:"code"`
	Severity   Severity   `json:"severity"`
	Service    string     `json:"service,omitempty"`
	Summary    string     `json:"summary"`
	Confidence float64    `json:"confidence"`
	Evidence   []Evidence `json:"evidence,omitempty"`
}

type Hypothesis struct {
	ID         string     `json:"id"`
	FindingID  string     `json:"finding_id,omitempty"`
	Service    string     `json:"service,omitempty"`
	Message    string     `json:"message"`
	Confidence float64    `json:"confidence"`
	Evidence   []Evidence `json:"evidence,omitempty"`
}

type RecommendedAction struct {
	ID               string               `json:"id"`
	Kind             string               `json:"kind"`
	Service          string               `json:"service,omitempty"`
	Summary          string               `json:"summary,omitempty"`
	Command          string               `json:"command,omitempty"`
	Mutating         bool                 `json:"mutating"`
	Safety           string               `json:"safety,omitempty"`
	Reversibility    schema.Reversibility `json:"reversibility,omitempty"`
	Risk             schema.Risk          `json:"risk,omitempty"`
	RequiresApproval bool                 `json:"requires_approval,omitempty"`
}

type PluginHook interface {
	Check(ctx context.Context, req PluginRequest) ([]Finding, error)
}

type PluginRequest struct {
	Status  servicestatus.Result
	Service servicestatus.Service
	TraceID string
}
