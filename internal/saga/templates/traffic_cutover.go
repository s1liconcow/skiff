package templates

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const TrafficCutoverKind = "traffic.cutover"

type TrafficCutoverRequest struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id,omitempty"`
	Service     string       `json:"service"`
	Env         string       `json:"env"`
	From        string       `json:"from"`
	To          string       `json:"to"`
	Percent     int          `json:"percent"`
	TraceID     string       `json:"trace_id,omitempty"`
	Actor       schema.Actor `json:"actor"`
	CreatedAt   time.Time    `json:"created_at,omitempty"`
}

type trafficCutoverParams struct {
	Service     string `json:"service"`
	Env         string `json:"env"`
	OperationID string `json:"operation_id,omitempty"`
	From        string `json:"from"`
	To          string `json:"to"`
	Percent     int    `json:"percent"`
}

func TrafficCutover(req TrafficCutoverRequest) (saga.CreateRequest, error) {
	req = NormalizeTrafficCutoverRequest(req)
	if err := validateTrafficCutoverRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	params := trafficCutoverParams{
		Service:     req.Service,
		Env:         req.Env,
		OperationID: req.OperationID,
		From:        req.From,
		To:          req.To,
		Percent:     req.Percent,
	}
	risk := schema.RiskMedium
	if req.Percent == 100 {
		risk = schema.RiskHigh
	}
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          TrafficCutoverKind,
			Target:        schema.Target{Kind: "service", Name: req.Service},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          risk,
			Reversibility: schema.Compensatable,
			Summary:       fmt.Sprintf("shift %d%% traffic from %s to %s for %s", req.Percent, req.From, req.To, req.Service),
			CreatedAt:     now,
			Params:        mustJSON(params),
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Nodes: []schema.SagaNode{
				{
					ID:            "preflight",
					Kind:          "check.preflight",
					Params:        mustJSON(map[string]any{"service": req.Service, "env": req.Env, "require_service_control": false, "require_provider": true, "required_facts": []string{"from:" + req.From, "to:" + req.To}}),
					Risk:          schema.RiskLow,
					Reversibility: schema.Reversible,
				},
				{
					ID:            "approve-cutover",
					Kind:          "approval.manual",
					Requires:      []string{"preflight"},
					Params:        mustJSON(map[string]any{"summary": fmt.Sprintf("approve shifting %d%% traffic to %s", req.Percent, req.To), "risk": risk, "facts": []string{"source:" + req.From, "target:" + req.To}}),
					Risk:          risk,
					Reversibility: schema.Reversible,
				},
				{
					ID:            "shift-traffic",
					Kind:          "service.traffic.shift",
					Requires:      []string{"approve-cutover"},
					Params:        mustJSON(params),
					Risk:          risk,
					Reversibility: schema.Compensatable,
				},
				{
					ID:            "verify-target-health",
					Kind:          "check.service_healthy",
					Requires:      []string{"shift-traffic"},
					Params:        mustJSON(map[string]any{"service": req.Service, "env": req.Env, "allowed_statuses": []string{"healthy", "ok", "active", "configured", "running", "applied", "unchanged"}}),
					Risk:          schema.RiskLow,
					Reversibility: schema.Reversible,
				},
			},
			Edges: []schema.SagaEdge{
				{From: "preflight", To: "approve-cutover"},
				{From: "approve-cutover", To: "shift-traffic"},
				{From: "shift-traffic", To: "verify-target-health"},
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

func NormalizeTrafficCutoverRequest(req TrafficCutoverRequest) TrafficCutoverRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.Service = strings.TrimSpace(req.Service)
	req.Env = strings.TrimSpace(req.Env)
	req.From = strings.TrimSpace(req.From)
	req.To = strings.TrimSpace(req.To)
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.TraceID = strings.TrimSpace(req.TraceID)
	if req.From == "" {
		req.From = "kube"
	}
	if req.To == "" {
		req.To = "skiff"
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Service+"cutover")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	return req
}

func validateTrafficCutoverRequest(req TrafficCutoverRequest) error {
	switch {
	case req.Service == "":
		return errors.New("service is required")
	case req.Env == "":
		return errors.New("env is required")
	case req.From == "":
		return errors.New("source target is required")
	case req.To == "":
		return errors.New("destination target is required")
	case req.From == req.To:
		return errors.New("source and destination targets must differ")
	case req.Percent < 0 || req.Percent > 100:
		return errors.New("percent must be between 0 and 100")
	case req.Actor.ID == "" || req.Actor.Type == "":
		return errors.New("actor id and type are required")
	case req.TraceID == "":
		return errors.New("trace id is required")
	default:
		return nil
	}
}
