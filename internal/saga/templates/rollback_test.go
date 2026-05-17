package templates_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestDeploymentRollbackTemplateRegistersTypedGraph(t *testing.T) {
	factory, ok := templates.LookupRollback(templates.DeploymentRollbackKind)
	if !ok {
		t.Fatalf("rollback template kind was not registered")
	}
	req, err := factory(templates.RollbackRequest{
		SagaID:        "saga_01JROLLBACK",
		OperationID:   "op_01JROLLBACK",
		Service:       "payments-api",
		Env:           "prod",
		FromRelease:   "rel_bad",
		Target:        templates.PreviousStable,
		TargetRelease: "rel_good",
		TraceID:       "tr_rollback",
		Actor:         schema.Actor{ID: "agent-one", Type: "agent"},
		CreatedAt:     time.Date(2026, 5, 17, 0, 10, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	if req.Intent.Kind != templates.DeploymentRollbackKind || req.Intent.Risk != schema.RiskMedium || req.Intent.Reversibility != schema.Reversible {
		t.Fatalf("unexpected intent: %+v", req.Intent)
	}
	if req.Control.Status != schema.SagaPending || req.Control.TraceID != "tr_rollback" {
		t.Fatalf("unexpected control: %+v", req.Control)
	}
	wantOrder := []string{
		"resolve-target",
		"create-operation",
		"acquire-service-lease",
		"update-desired-release",
		"start-asg-instance-refresh",
		"watch-rollout-health",
		"mark-complete",
	}
	if len(req.Graph.Nodes) != len(wantOrder) {
		t.Fatalf("node count = %d, want %d", len(req.Graph.Nodes), len(wantOrder))
	}
	for i, want := range wantOrder {
		if req.Graph.Nodes[i].ID != want {
			t.Fatalf("node %d = %q, want %q", i, req.Graph.Nodes[i].ID, want)
		}
	}
	update := req.Graph.Nodes[3]
	if update.Compensate == nil || update.Compensate.Kind != "service.desired_release.update" {
		t.Fatalf("desired release update missing compensation: %+v", update.Compensate)
	}
	var params map[string]string
	if err := json.Unmarshal(update.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params["target_release"] != "rel_good" || params["from_release"] != "rel_bad" {
		t.Fatalf("unexpected params: %+v", params)
	}
}
