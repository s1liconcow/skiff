package agent

import (
	"github.com/s1liconcow/skiff/internal/doctor"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const GoalRestoreHealth = "restore-health"

type GraphStatus string

const (
	StatusNoAction         GraphStatus = "no_action"
	StatusPlanReady        GraphStatus = "plan_ready"
	StatusApprovalRequired GraphStatus = "approval_required"
)

type ActionGraph struct {
	TraceID    string              `json:"trace_id,omitempty"`
	Goal       string              `json:"goal"`
	Status     GraphStatus         `json:"status"`
	Confidence float64             `json:"confidence"`
	Service    string              `json:"service,omitempty"`
	Health     string              `json:"health,omitempty"`
	Source     string              `json:"source,omitempty"`
	Facts      []doctor.Evidence   `json:"facts,omitempty"`
	Findings   []doctor.Finding    `json:"findings,omitempty"`
	Hypotheses []doctor.Hypothesis `json:"hypotheses,omitempty"`
	Steps      []ActionStep        `json:"steps"`
}

type ActionStep struct {
	ID                 string               `json:"id"`
	Kind               string               `json:"kind"`
	Service            string               `json:"service,omitempty"`
	Summary            string               `json:"summary,omitempty"`
	Command            string               `json:"command"`
	APIOperation       *APIOperation        `json:"api_operation,omitempty"`
	Mutating           bool                 `json:"mutating"`
	Safety             string               `json:"safety"`
	Risk               schema.Risk          `json:"risk"`
	Reversibility      schema.Reversibility `json:"reversibility"`
	Reversible         bool                 `json:"reversible"`
	RequiresApproval   bool                 `json:"requires_approval"`
	Requires           []string             `json:"requires"`
	ExpectedValidation []ExpectedValidation `json:"expected_validation,omitempty"`
	SourceActionID     string               `json:"source_action_id,omitempty"`
}

type APIOperation struct {
	Operation string            `json:"operation"`
	Target    schema.Target     `json:"target"`
	Params    map[string]string `json:"params,omitempty"`
	Mutating  bool              `json:"mutating"`
}

type ExpectedValidation struct {
	ID               string `json:"id"`
	Command          string `json:"command"`
	Summary          string `json:"summary"`
	SuccessCondition string `json:"success_condition"`
}
