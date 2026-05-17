package templates

import (
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
	DeploymentCanaryKind  = "deployment.canary"
	CanaryDeployCommand   = "canary-deploy"
	DefaultCanaryBake     = "5m"
	DefaultRollbackPolicy = PreviousStable
)

type CanaryRequest struct {
	SagaID         string        `json:"saga_id,omitempty"`
	OperationID    string        `json:"operation_id,omitempty"`
	Service        string        `json:"service"`
	Env            string        `json:"env"`
	ReleaseID      string        `json:"release_id"`
	Stages         []CanaryStage `json:"stages,omitempty"`
	BakeDuration   string        `json:"bake_duration,omitempty"`
	MetricGates    []MetricGate  `json:"metric_gates,omitempty"`
	RollbackPolicy string        `json:"rollback_policy,omitempty"`
	TraceID        string        `json:"trace_id,omitempty"`
	Actor          schema.Actor  `json:"actor"`
	CreatedAt      time.Time     `json:"created_at,omitempty"`
}

type CanaryStage struct {
	Percent              int `json:"percent"`
	MinHealthyPercentage int `json:"min_healthy_percentage,omitempty"`
	InstanceWarmup       int `json:"instance_warmup,omitempty"`
}

type MetricGate struct {
	Metric        string  `json:"metric"`
	Comparator    string  `json:"comparator,omitempty"`
	Threshold     float64 `json:"threshold"`
	Window        string  `json:"window,omitempty"`
	PeriodSeconds int     `json:"period_seconds,omitempty"`
}

type CanaryFactory func(CanaryRequest) (saga.CreateRequest, error)

var canaryTemplates = map[string]CanaryFactory{
	DeploymentCanaryKind: DeploymentCanary,
	CanaryDeployCommand:  DeploymentCanary,
}

func LookupCanary(kind string) (CanaryFactory, bool) {
	factory, ok := canaryTemplates[kind]
	return factory, ok
}

func RegisteredCanaryKinds() []string {
	kinds := make([]string, 0, len(canaryTemplates))
	for kind := range canaryTemplates {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func DeploymentCanary(req CanaryRequest) (saga.CreateRequest, error) {
	req = NormalizeCanaryRequest(req)
	if err := validateCanaryRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	common := canaryParams{
		Service:        req.Service,
		Env:            req.Env,
		OperationID:    req.OperationID,
		ReleaseID:      req.ReleaseID,
		BakeDuration:   req.BakeDuration,
		RollbackPolicy: req.RollbackPolicy,
		Stages:         req.Stages,
		MetricGates:    req.MetricGates,
	}
	nodes, edges := canaryGraph(req)
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          DeploymentCanaryKind,
			Target:        schema.Target{Kind: "service", Name: req.Service},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
			Summary:       fmt.Sprintf("canary deploy %s release %s", req.Service, req.ReleaseID),
			CreatedAt:     now,
			Params:        mustJSON(common),
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Nodes:         nodes,
			Edges:         edges,
			CreatedAt:     now,
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

func NormalizeCanaryRequest(req CanaryRequest) CanaryRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.Service = strings.TrimSpace(req.Service)
	req.Env = strings.TrimSpace(req.Env)
	req.ReleaseID = strings.TrimSpace(req.ReleaseID)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.TraceID = strings.TrimSpace(req.TraceID)
	req.RollbackPolicy = strings.TrimSpace(req.RollbackPolicy)
	req.BakeDuration = strings.TrimSpace(req.BakeDuration)
	if req.BakeDuration == "" {
		req.BakeDuration = DefaultCanaryBake
	}
	if req.RollbackPolicy == "" {
		req.RollbackPolicy = DefaultRollbackPolicy
	}
	if len(req.Stages) == 0 {
		req.Stages = []CanaryStage{{Percent: 5}, {Percent: 25}, {Percent: 100}}
	}
	if len(req.MetricGates) == 0 {
		req.MetricGates = []MetricGate{{Metric: "aws.elb.http_5xx_count", Comparator: "<=", Threshold: 0, Window: "5m"}}
	}
	for i := range req.MetricGates {
		req.MetricGates[i].Metric = strings.TrimSpace(req.MetricGates[i].Metric)
		req.MetricGates[i].Comparator = strings.TrimSpace(req.MetricGates[i].Comparator)
		req.MetricGates[i].Window = strings.TrimSpace(req.MetricGates[i].Window)
		if req.MetricGates[i].Comparator == "" {
			req.MetricGates[i].Comparator = "<="
		}
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Service+"canary")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	return req
}

func validateCanaryRequest(req CanaryRequest) error {
	switch {
	case req.Service == "":
		return errors.New("service is required")
	case req.Env == "":
		return errors.New("env is required")
	case req.ReleaseID == "":
		return errors.New("release ID is required")
	case req.Actor.ID == "" || req.Actor.Type == "":
		return errors.New("actor id and type are required")
	case req.TraceID == "":
		return errors.New("trace id is required")
	}
	if _, err := time.ParseDuration(req.BakeDuration); err != nil {
		return fmt.Errorf("bake duration is invalid: %w", err)
	}
	seen := map[int]bool{}
	last := 0
	for _, stage := range req.Stages {
		if stage.Percent <= 0 || stage.Percent > 100 {
			return fmt.Errorf("canary stage percent %d must be between 1 and 100", stage.Percent)
		}
		if seen[stage.Percent] {
			return fmt.Errorf("duplicate canary stage percent %d", stage.Percent)
		}
		if stage.Percent < last {
			return errors.New("canary stages must be ordered by increasing percent")
		}
		seen[stage.Percent] = true
		last = stage.Percent
	}
	if len(req.Stages) == 0 || req.Stages[len(req.Stages)-1].Percent != 100 {
		return errors.New("canary stages must end at 100 percent")
	}
	for _, gate := range req.MetricGates {
		if strings.TrimSpace(gate.Metric) == "" {
			return errors.New("metric gate metric is required")
		}
		if gate.Window != "" {
			if _, err := time.ParseDuration(gate.Window); err != nil {
				return fmt.Errorf("metric gate window is invalid: %w", err)
			}
		}
	}
	return nil
}

type canaryParams struct {
	Service        string        `json:"service"`
	Env            string        `json:"env"`
	OperationID    string        `json:"operation_id,omitempty"`
	ReleaseID      string        `json:"release_id"`
	BakeDuration   string        `json:"bake_duration,omitempty"`
	RollbackPolicy string        `json:"rollback_policy,omitempty"`
	Stages         []CanaryStage `json:"stages,omitempty"`
	MetricGates    []MetricGate  `json:"metric_gates,omitempty"`
}

type canaryStageParams struct {
	Service              string `json:"service"`
	Env                  string `json:"env"`
	OperationID          string `json:"operation_id,omitempty"`
	ReleaseID            string `json:"release_id"`
	StagePercent         int    `json:"stage_percent"`
	RollbackPolicy       string `json:"rollback_policy,omitempty"`
	MinHealthyPercentage int    `json:"min_healthy_percentage,omitempty"`
	InstanceWarmup       int    `json:"instance_warmup,omitempty"`
	Mechanism            string `json:"mechanism"`
}

type canaryBakeParams struct {
	Service      string `json:"service"`
	Env          string `json:"env"`
	StagePercent int    `json:"stage_percent"`
	Duration     string `json:"duration"`
}

func canaryGraph(req CanaryRequest) ([]schema.SagaNode, []schema.SagaEdge) {
	nodes := []schema.SagaNode{{
		ID:   "preflight",
		Kind: "check.preflight",
		Params: mustJSON(map[string]any{
			"service":                 req.Service,
			"env":                     req.Env,
			"require_service_control": true,
			"require_provider":        true,
			"required_facts":          []string{"release:" + req.ReleaseID, "rollback_policy:" + req.RollbackPolicy},
		}),
		Risk:          schema.RiskLow,
		Reversibility: schema.Reversible,
	}}
	edges := make([]schema.SagaEdge, 0)
	previous := "preflight"
	for _, stage := range req.Stages {
		stageParams := canaryStageParams{
			Service:              req.Service,
			Env:                  req.Env,
			OperationID:          req.OperationID,
			ReleaseID:            req.ReleaseID,
			StagePercent:         stage.Percent,
			RollbackPolicy:       req.RollbackPolicy,
			MinHealthyPercentage: stage.MinHealthyPercentage,
			InstanceWarmup:       stage.InstanceWarmup,
			Mechanism:            "aws-asg-instance-refresh-min-healthy",
		}
		stageID := fmt.Sprintf("start-%d", stage.Percent)
		nodes = append(nodes, schema.SagaNode{
			ID:            stageID,
			Kind:          "service.canary.stage",
			Requires:      []string{previous},
			Params:        mustJSON(stageParams),
			Compensate:    &schema.CompensationSpec{Kind: "service.canary.rollback", Params: mustJSON(stageParams)},
			Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		})
		edges = append(edges, schema.SagaEdge{From: previous, To: stageID})
		previous = stageID

		bakeID := fmt.Sprintf("bake-%d", stage.Percent)
		nodes = append(nodes, schema.SagaNode{
			ID:            bakeID,
			Kind:          "time.sleep",
			Requires:      []string{previous},
			Params:        mustJSON(canaryBakeParams{Service: req.Service, Env: req.Env, StagePercent: stage.Percent, Duration: req.BakeDuration}),
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		})
		edges = append(edges, schema.SagaEdge{From: previous, To: bakeID})
		previous = bakeID

		targetID := fmt.Sprintf("target-health-%d", stage.Percent)
		nodes = append(nodes, schema.SagaNode{
			ID:       targetID,
			Kind:     "check.target_health",
			Requires: []string{previous},
			Params: mustJSON(map[string]any{
				"service":          req.Service,
				"env":              req.Env,
				"kind":             "target-group",
				"logical_id":       "target-group:" + req.Service,
				"allowed_statuses": []string{"healthy", "ok", "active", "configured", "in-service", "applied", "unchanged"},
			}),
			Risk:          schema.RiskMedium,
			Reversibility: schema.Reversible,
		})
		edges = append(edges, schema.SagaEdge{From: previous, To: targetID})
		previous = targetID

		for i, gate := range req.MetricGates {
			gateID := fmt.Sprintf("metrics-gate-%d-%d", stage.Percent, i+1)
			nodes = append(nodes, schema.SagaNode{
				ID:       gateID,
				Kind:     "check.metrics_gate",
				Requires: []string{previous},
				Params: mustJSON(map[string]any{
					"service":        req.Service,
					"env":            req.Env,
					"metric":         gate.Metric,
					"comparator":     gate.Comparator,
					"threshold":      gate.Threshold,
					"window":         gate.Window,
					"period_seconds": gate.PeriodSeconds,
				}),
				Risk:          schema.RiskMedium,
				Reversibility: schema.Reversible,
			})
			edges = append(edges, schema.SagaEdge{From: previous, To: gateID})
			previous = gateID
		}
	}
	markStable := canaryStageParams{
		Service:        req.Service,
		Env:            req.Env,
		OperationID:    req.OperationID,
		ReleaseID:      req.ReleaseID,
		StagePercent:   100,
		RollbackPolicy: req.RollbackPolicy,
		Mechanism:      "service-control-stable-release-cas",
	}
	nodes = append(nodes, schema.SagaNode{
		ID:            "mark-stable",
		Kind:          "service.canary.mark_stable",
		Requires:      []string{previous},
		Params:        mustJSON(markStable),
		Compensate:    &schema.CompensationSpec{Kind: "service.canary.rollback", Params: mustJSON(markStable)},
		Risk:          schema.RiskMedium,
		Reversibility: schema.Compensatable,
	})
	edges = append(edges, schema.SagaEdge{From: previous, To: "mark-stable"})
	return nodes, edges
}
