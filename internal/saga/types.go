package saga

import (
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type Intent = schema.SagaIntent
type Graph = schema.SagaGraph
type Node = schema.SagaNode
type Control = schema.SagaControl
type Event = schema.SagaEvent
type StepResult = schema.StepResult
type StepFailure = schema.StepFailure

type IntentDocument struct {
	Key    string              `json:"key"`
	ETag   string              `json:"etag"`
	Meta   objstore.ObjectMeta `json:"meta"`
	Intent schema.SagaIntent   `json:"intent"`
}

type GraphDocument struct {
	Key   string              `json:"key"`
	ETag  string              `json:"etag"`
	Meta  objstore.ObjectMeta `json:"meta"`
	Graph schema.SagaGraph    `json:"graph"`
}

type ControlDocument struct {
	Key     string              `json:"key"`
	ETag    string              `json:"etag"`
	Meta    objstore.ObjectMeta `json:"meta"`
	Control schema.SagaControl  `json:"control"`
}

type EventDocument struct {
	Key   string              `json:"key"`
	ETag  string              `json:"etag"`
	Meta  objstore.ObjectMeta `json:"meta"`
	Event schema.Event        `json:"event"`
}

type StepResultDocument struct {
	Key    string              `json:"key"`
	ETag   string              `json:"etag"`
	Meta   objstore.ObjectMeta `json:"meta"`
	Result schema.StepResult   `json:"result"`
}

type Documents struct {
	Intent  *IntentDocument  `json:"intent"`
	Graph   *GraphDocument   `json:"graph"`
	Control *ControlDocument `json:"control"`
}

type CreateRequest struct {
	Intent  schema.SagaIntent  `json:"intent"`
	Graph   schema.SagaGraph   `json:"graph"`
	Control schema.SagaControl `json:"control"`
}

type InspectResult struct {
	SagaID        string               `json:"saga_id"`
	Kind          string               `json:"kind,omitempty"`
	Target        schema.Target        `json:"target"`
	Status        schema.SagaStatus    `json:"status"`
	CurrentSteps  []string             `json:"current_steps,omitempty"`
	Risk          schema.Risk          `json:"risk,omitempty"`
	Reversibility schema.Reversibility `json:"reversibility,omitempty"`
	UpdatedAt     string               `json:"updated_at,omitempty"`
	TraceID       string               `json:"trace_id,omitempty"`
	Intent        schema.SagaIntent    `json:"intent"`
	Graph         schema.SagaGraph     `json:"graph"`
	Control       schema.SagaControl   `json:"control"`
	Nodes         []NodeSummary        `json:"nodes,omitempty"`
	Paths         map[string]string    `json:"paths,omitempty"`
}

type NodeSummary struct {
	ID            string               `json:"id"`
	Kind          string               `json:"kind"`
	Requires      []string             `json:"requires,omitempty"`
	Risk          schema.Risk          `json:"risk,omitempty"`
	Reversibility schema.Reversibility `json:"reversibility,omitempty"`
}
