package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	DeploymentRollbackKind = "deployment.rollback"
	PreviousStable         = "previous-stable"
)

type RollbackRequest struct {
	SagaID        string       `json:"saga_id,omitempty"`
	OperationID   string       `json:"operation_id,omitempty"`
	Service       string       `json:"service"`
	Env           string       `json:"env"`
	FromRelease   string       `json:"from_release,omitempty"`
	Target        string       `json:"target,omitempty"`
	TargetRelease string       `json:"target_release,omitempty"`
	TraceID       string       `json:"trace_id,omitempty"`
	Actor         schema.Actor `json:"actor"`
	CreatedAt     time.Time    `json:"created_at,omitempty"`
}

type RollbackFactory func(RollbackRequest) (saga.CreateRequest, error)

var rollbackTemplates = map[string]RollbackFactory{
	DeploymentRollbackKind: DeploymentRollback,
}

func LookupRollback(kind string) (RollbackFactory, bool) {
	factory, ok := rollbackTemplates[kind]
	return factory, ok
}

func RegisteredRollbackKinds() []string {
	kinds := make([]string, 0, len(rollbackTemplates))
	for kind := range rollbackTemplates {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func DeploymentRollback(req RollbackRequest) (saga.CreateRequest, error) {
	req = normalizeRollbackRequest(req)
	if err := validateRollbackRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	common := rollbackParams{
		Service:       req.Service,
		Env:           req.Env,
		OperationID:   req.OperationID,
		FromRelease:   req.FromRelease,
		Target:        req.Target,
		TargetRelease: req.TargetRelease,
	}
	intentParams := mustJSON(common)
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          DeploymentRollbackKind,
			Target:        schema.Target{Kind: "service", Name: req.Service},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Reversible,
			Summary:       fmt.Sprintf("rollback %s to %s", req.Service, req.TargetRelease),
			CreatedAt:     now,
			Params:        intentParams,
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Nodes: []schema.SagaNode{
				{
					ID:            "resolve-target",
					Kind:          "deployment.rollback.resolve_target",
					Params:        mustJSON(common),
					Risk:          schema.RiskLow,
					Reversibility: schema.Reversible,
				},
				{
					ID:            "create-operation",
					Kind:          "operation.intent.create",
					Requires:      []string{"resolve-target"},
					Params:        mustJSON(common),
					Risk:          schema.RiskLow,
					Reversibility: schema.Reversible,
				},
				{
					ID:            "acquire-service-lease",
					Kind:          "service.lease.acquire",
					Requires:      []string{"create-operation"},
					Params:        mustJSON(common),
					Risk:          schema.RiskLow,
					Reversibility: schema.Reversible,
				},
				{
					ID:            "update-desired-release",
					Kind:          "service.desired_release.update",
					Requires:      []string{"acquire-service-lease"},
					Params:        mustJSON(common),
					Compensate:    &schema.CompensationSpec{Kind: "service.desired_release.update", Params: mustJSON(rollbackParams{Service: req.Service, Env: req.Env, OperationID: req.OperationID, TargetRelease: req.FromRelease})},
					Risk:          schema.RiskMedium,
					Reversibility: schema.Reversible,
				},
				{
					ID:            "start-asg-instance-refresh",
					Kind:          "provider.aws.asg_instance_refresh.start",
					Requires:      []string{"update-desired-release"},
					Params:        mustJSON(common),
					Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
					Risk:          schema.RiskMedium,
					Reversibility: schema.Compensatable,
				},
				{
					ID:            "watch-rollout-health",
					Kind:          "provider.aws.asg_instance_refresh.watch",
					Requires:      []string{"start-asg-instance-refresh"},
					Params:        mustJSON(common),
					Risk:          schema.RiskMedium,
					Reversibility: schema.Compensatable,
				},
				{
					ID:            "mark-complete",
					Kind:          "operation.rollback.complete",
					Requires:      []string{"watch-rollout-health"},
					Params:        mustJSON(common),
					Risk:          schema.RiskLow,
					Reversibility: schema.Reversible,
				},
			},
			Edges: []schema.SagaEdge{
				{From: "resolve-target", To: "create-operation"},
				{From: "create-operation", To: "acquire-service-lease"},
				{From: "acquire-service-lease", To: "update-desired-release"},
				{From: "update-desired-release", To: "start-asg-instance-refresh"},
				{From: "start-asg-instance-refresh", To: "watch-rollout-health"},
				{From: "watch-rollout-health", To: "mark-complete"},
			},
			CreatedAt: now,
		},
		Control: schema.SagaControl{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Status:        schema.SagaPending,
			UpdatedAt:     now,
			TraceID:       req.TraceID,
		},
	}, nil
}

type rollbackParams struct {
	Service       string `json:"service"`
	Env           string `json:"env"`
	OperationID   string `json:"operation_id,omitempty"`
	FromRelease   string `json:"from_release,omitempty"`
	Target        string `json:"target,omitempty"`
	TargetRelease string `json:"target_release,omitempty"`
}

func normalizeRollbackRequest(req RollbackRequest) RollbackRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.Service = strings.TrimSpace(req.Service)
	req.Env = strings.TrimSpace(req.Env)
	req.Target = strings.TrimSpace(req.Target)
	req.TargetRelease = strings.TrimSpace(req.TargetRelease)
	req.FromRelease = strings.TrimSpace(req.FromRelease)
	if req.Target == "" {
		req.Target = PreviousStable
	}
	if req.TargetRelease == "" {
		req.TargetRelease = req.Target
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Service+"rollback")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	return req
}

func validateRollbackRequest(req RollbackRequest) error {
	switch {
	case req.Service == "":
		return errors.New("service is required")
	case req.Env == "":
		return errors.New("env is required")
	case req.TargetRelease == "":
		return errors.New("target release is required")
	case req.Actor.ID == "" || req.Actor.Type == "":
		return errors.New("actor id and type are required")
	case req.TraceID == "":
		return errors.New("trace id is required")
	}
	return nil
}

func mustJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}
