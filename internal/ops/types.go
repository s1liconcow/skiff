package ops

import (
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type IntentDocument struct {
	Key    string                 `json:"key"`
	ETag   string                 `json:"etag"`
	Meta   objstore.ObjectMeta    `json:"meta"`
	Intent schema.OperationIntent `json:"intent"`
}

type ControlDocument struct {
	Key     string                  `json:"key"`
	ETag    string                  `json:"etag"`
	Meta    objstore.ObjectMeta     `json:"meta"`
	Control schema.OperationControl `json:"control"`
}

type Summary struct {
	OperationID        string                        `json:"operation_id"`
	Service            string                        `json:"service"`
	Env                string                        `json:"env,omitempty"`
	Kind               string                        `json:"kind,omitempty"`
	Status             schema.OperationStatus        `json:"status"`
	Lease              *schema.Lease                 `json:"lease,omitempty"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations,omitempty"`
	UpdatedAt          string                        `json:"updated_at,omitempty"`
	TraceID            string                        `json:"trace_id,omitempty"`
	Resumable          bool                          `json:"resumable"`
	ControlKey         string                        `json:"control_key"`
	IntentKey          string                        `json:"intent_key,omitempty"`
}

type InspectResult struct {
	OperationID        string                        `json:"operation_id"`
	Service            string                        `json:"service"`
	Env                string                        `json:"env,omitempty"`
	Kind               string                        `json:"kind,omitempty"`
	Target             schema.Target                 `json:"target"`
	Status             schema.OperationStatus        `json:"status"`
	Lease              *schema.Lease                 `json:"lease,omitempty"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations,omitempty"`
	StepResults        []schema.StepResultRef        `json:"step_results,omitempty"`
	Risk               schema.Risk                   `json:"risk,omitempty"`
	Reversibility      schema.Reversibility          `json:"reversibility,omitempty"`
	UpdatedAt          string                        `json:"updated_at,omitempty"`
	TraceID            string                        `json:"trace_id,omitempty"`
	Resumable          bool                          `json:"resumable"`
	Intent             *schema.OperationIntent       `json:"intent,omitempty"`
	Control            schema.OperationControl       `json:"control"`
	Paths              map[string]string             `json:"paths,omitempty"`
}

type ListOptions struct {
	Service         string
	IncludeTerminal bool
	Limit           int
}

type LeaseOptions struct {
	Owner    string
	Duration time.Duration
}

type LeaseHandle struct {
	Service     string    `json:"service"`
	OperationID string    `json:"operation_id"`
	Owner       string    `json:"owner"`
	Token       string    `json:"token"`
	Generation  int64     `json:"generation"`
	ExpiresAt   time.Time `json:"expires_at"`
	ETag        string    `json:"etag"`
}

type LeaseAcquireResult struct {
	Handle        *LeaseHandle     `json:"handle"`
	Control       *ControlDocument `json:"control"`
	PreviousLease *schema.Lease    `json:"previous_lease,omitempty"`
	TookOver      bool             `json:"took_over"`
}

type ResumeRequest struct {
	Service       string
	OperationID   string
	Actor         schema.Actor
	TraceID       string
	Owner         string
	LeaseDuration time.Duration
	Takeover      bool
}

type ResumeResult struct {
	OK                 bool                          `json:"ok"`
	OperationID        string                        `json:"operation_id"`
	Service            string                        `json:"service"`
	Env                string                        `json:"env,omitempty"`
	Kind               string                        `json:"kind,omitempty"`
	TraceID            string                        `json:"trace_id,omitempty"`
	PreviousStatus     schema.OperationStatus        `json:"previous_status,omitempty"`
	Status             schema.OperationStatus        `json:"status"`
	Resumed            bool                          `json:"resumed"`
	TookOver           bool                          `json:"took_over,omitempty"`
	RolloutStatus      *provider.RolloutStatus       `json:"rollout_status,omitempty"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations,omitempty"`
	NextCommands       []string                      `json:"next_commands,omitempty"`
}
