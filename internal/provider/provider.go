package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/ir"
)

type Provider interface {
	Name() string
	Plan(ctx context.Context, graph *ir.Graph) (*Plan, error)
	Apply(ctx context.Context, plan *Plan) (*ApplyResult, error)
	InspectService(ctx context.Context, ref ServiceRef) (*ServiceInspection, error)
	InspectResource(ctx context.Context, ref ResourceRef) (*ResourceInspection, error)
	Logs(ctx context.Context, req LogsRequest) (*LogsResult, error)
	Metrics(ctx context.Context, req MetricsRequest) (*MetricsResult, error)
	Debug(ctx context.Context, req DebugRequest) (*DebugSession, error)
	StartRollout(ctx context.Context, req RolloutRequest) (*Rollout, error)
	WatchRollout(ctx context.Context, req WatchRolloutRequest) (*RolloutStatus, error)
	Rollback(ctx context.Context, req RollbackRequest) (*Rollout, error)
}

type Plan struct {
	Provider  string          `json:"provider"`
	Service   string          `json:"service"`
	Env       string          `json:"env"`
	Resources []PlannedChange `json:"resources,omitempty"`
}

type PlannedChange struct {
	Action      string            `json:"action"`
	Kind        string            `json:"kind"`
	LogicalID   string            `json:"logical_id"`
	Name        string            `json:"name"`
	ProviderID  string            `json:"provider_id,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	Desired     json.RawMessage   `json:"desired,omitempty"`
}

const (
	ActionCreate             = "create"
	ActionUpdate             = "update"
	ActionNoop               = "no-op"
	ActionDeleteNotSupported = "delete-not-supported"
)

type ApplyResult struct {
	Provider    string               `json:"provider"`
	Service     string               `json:"service,omitempty"`
	Env         string               `json:"env,omitempty"`
	AppliedAt   time.Time            `json:"applied_at"`
	ResourceIDs []string             `json:"resource_ids,omitempty"`
	Resources   []ResourceInspection `json:"resources,omitempty"`
}

type ServiceRef struct {
	Service string `json:"service"`
	Env     string `json:"env"`
}

type ResourceRef struct {
	Service    string `json:"service,omitempty"`
	Env        string `json:"env,omitempty"`
	Kind       string `json:"kind,omitempty"`
	LogicalID  string `json:"logical_id,omitempty"`
	Name       string `json:"name,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
}

type ServiceInspection struct {
	Ref       ServiceRef           `json:"ref"`
	Provider  string               `json:"provider"`
	FreshAt   time.Time            `json:"fresh_at"`
	Resources []ResourceInspection `json:"resources,omitempty"`
}

type ResourceInspection struct {
	Kind       string            `json:"kind"`
	LogicalID  string            `json:"logical_id,omitempty"`
	Name       string            `json:"name,omitempty"`
	ProviderID string            `json:"provider_id,omitempty"`
	ARN        string            `json:"arn,omitempty"`
	Status     string            `json:"status,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type LogsRequest struct {
	Service    string    `json:"service"`
	Env        string    `json:"env"`
	ResourceID string    `json:"resource_id,omitempty"`
	Since      time.Time `json:"since,omitempty"`
	Limit      int       `json:"limit,omitempty"`
}

type LogsResult struct {
	Entries []LogEntry `json:"entries,omitempty"`
}

type LogEntry struct {
	Timestamp time.Time         `json:"timestamp"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
}

type MetricsRequest struct {
	Service string    `json:"service"`
	Env     string    `json:"env"`
	From    time.Time `json:"from,omitempty"`
	To      time.Time `json:"to,omitempty"`
}

type MetricsResult struct {
	Series []MetricSeries `json:"series,omitempty"`
}

type MetricSeries struct {
	Name   string        `json:"name"`
	Unit   string        `json:"unit,omitempty"`
	Points []MetricPoint `json:"points,omitempty"`
}

type MetricPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

type DebugRequest struct {
	Service string `json:"service"`
	Env     string `json:"env"`
	Reason  string `json:"reason,omitempty"`
}

type DebugSession struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	StartedAt time.Time `json:"started_at"`
}

type RolloutRequest struct {
	Service   string `json:"service"`
	Env       string `json:"env"`
	ReleaseID string `json:"release_id"`
}

type WatchRolloutRequest struct {
	Service   string `json:"service"`
	Env       string `json:"env"`
	RolloutID string `json:"rollout_id"`
}

type RollbackRequest struct {
	Service   string `json:"service"`
	Env       string `json:"env"`
	ReleaseID string `json:"release_id"`
}

type Rollout struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	Service    string    `json:"service"`
	Env        string    `json:"env"`
	ProviderID string    `json:"provider_id,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

type RolloutStatus struct {
	RolloutID  string    `json:"rollout_id"`
	Status     string    `json:"status"`
	ProviderID string    `json:"provider_id,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type ErrorCode string

const (
	CodeAccessDenied  ErrorCode = "ACCESS_DENIED"
	CodeInvalidConfig ErrorCode = "INVALID_CONFIG"
	CodeNotFound      ErrorCode = "NOT_FOUND"
	CodeProvider      ErrorCode = "PROVIDER_ERROR"
	CodeThrottled     ErrorCode = "THROTTLED"
	CodeUnsupported   ErrorCode = "UNSUPPORTED"
	CodeValidation    ErrorCode = "VALIDATION_ERROR"
)

type Error struct {
	Code     ErrorCode `json:"code"`
	Provider string    `json:"provider,omitempty"`
	Op       string    `json:"op,omitempty"`
	Resource string    `json:"resource,omitempty"`
	Summary  string    `json:"summary"`
	Cause    error     `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := string(e.Code)
	if e.Provider != "" {
		prefix = e.Provider + ": " + prefix
	}
	if e.Op != "" {
		prefix += " " + e.Op
	}
	if e.Resource != "" {
		prefix += " " + e.Resource
	}
	if e.Summary == "" {
		return prefix
	}
	return fmt.Sprintf("%s: %s", prefix, e.Summary)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func Unsupported(providerName, op string) *Error {
	return &Error{
		Code:     CodeUnsupported,
		Provider: providerName,
		Op:       op,
		Summary:  "provider operation is not implemented yet",
	}
}
