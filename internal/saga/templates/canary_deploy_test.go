package templates_test

import (
	"encoding/json"
	"testing"
	"time"

	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestDeploymentCanaryTemplateRegistersStagedGraph(t *testing.T) {
	factory, ok := templates.LookupCanary(templates.CanaryDeployCommand)
	if !ok {
		t.Fatalf("canary template command was not registered")
	}
	req, err := factory(templates.CanaryRequest{
		SagaID:       "saga_01JCANARY",
		OperationID:  "op_01JCANARY",
		Service:      "payments-api",
		Env:          "prod",
		ReleaseID:    "rel_canary",
		Stages:       []templates.CanaryStage{{Percent: 10}, {Percent: 50}, {Percent: 100}},
		BakeDuration: "30s",
		MetricGates:  []templates.MetricGate{{Metric: "aws.elb.http_5xx_count", Comparator: "<=", Threshold: 0, Window: "5m"}},
		TraceID:      "tr_canary",
		Actor:        schema.Actor{ID: "agent-one", Type: "agent"},
		CreatedAt:    time.Date(2026, 5, 17, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if req.Intent.Kind != templates.DeploymentCanaryKind || req.Intent.Risk != schema.RiskMedium || req.Intent.Reversibility != schema.Compensatable {
		t.Fatalf("unexpected intent: %+v", req.Intent)
	}
	if req.Control.Status != schema.SagaPending || req.Control.TraceID != "tr_canary" {
		t.Fatalf("unexpected control: %+v", req.Control)
	}
	wantOrder := []string{
		"preflight",
		"start-10",
		"bake-10",
		"target-health-10",
		"metrics-gate-10-1",
		"start-50",
		"bake-50",
		"target-health-50",
		"metrics-gate-50-1",
		"start-100",
		"bake-100",
		"target-health-100",
		"metrics-gate-100-1",
		"mark-stable",
	}
	if len(req.Graph.Nodes) != len(wantOrder) {
		t.Fatalf("node count = %d, want %d", len(req.Graph.Nodes), len(wantOrder))
	}
	for i, want := range wantOrder {
		if req.Graph.Nodes[i].ID != want {
			t.Fatalf("node %d = %q, want %q", i, req.Graph.Nodes[i].ID, want)
		}
	}
	start := req.Graph.Nodes[1]
	if start.Compensate == nil || start.Compensate.Kind != "service.canary.rollback" {
		t.Fatalf("canary stage missing rollback compensation: %+v", start.Compensate)
	}
	var params struct {
		StagePercent int    `json:"stage_percent"`
		ReleaseID    string `json:"release_id"`
		Mechanism    string `json:"mechanism"`
	}
	if err := json.Unmarshal(start.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.StagePercent != 10 || params.ReleaseID != "rel_canary" || params.Mechanism != "aws-asg-instance-refresh-min-healthy" {
		t.Fatalf("unexpected start params: %+v", params)
	}
	if _, err := sagastate.TopologicalOrder(req.Graph); err != nil {
		t.Fatalf("graph is not explainable/topological: %v", err)
	}
}

func TestDeploymentCanaryTemplateValidatesStages(t *testing.T) {
	_, err := templates.DeploymentCanary(templates.CanaryRequest{
		Service:   "payments-api",
		Env:       "prod",
		ReleaseID: "rel_canary",
		Stages:    []templates.CanaryStage{{Percent: 50}, {Percent: 25}, {Percent: 100}},
		Actor:     schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:   "tr_canary",
	})
	if err == nil {
		t.Fatalf("expected unordered stages to fail validation")
	}
}
