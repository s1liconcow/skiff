package templates_test

import (
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestRegionalFailoverTemplateSurfacesIrreversibleBoundary(t *testing.T) {
	factory, ok := templates.LookupRegionalFailover(templates.RegionalFailoverKind)
	if !ok {
		t.Fatal("regional failover template not registered")
	}
	req, err := factory(templates.RegionalFailoverRequest{
		SagaID:        "saga_failover",
		OperationID:   "op_failover",
		Stack:         "orders",
		Service:       "orders",
		Database:      "orders-db",
		Env:           "prod",
		FromRegion:    "us-west-2",
		ToRegion:      "us-east-1",
		MaxReplicaLag: "30s",
		FreezeWrites:  true,
		Actor:         schema.Actor{ID: "operator", Type: "user"},
		TraceID:       "tr_failover",
		CreatedAt:     time.Date(2026, 5, 17, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RegionalFailover: %v", err)
	}
	if req.Intent.Kind != templates.RegionalFailoverKind || req.Intent.Risk != schema.RiskCritical || req.Intent.Reversibility != schema.PartiallyReversible {
		t.Fatalf("intent = %+v", req.Intent)
	}
	var sawApproval, sawBoundary bool
	for _, node := range req.Graph.Nodes {
		switch node.ID {
		case "approve-failover":
			sawApproval = node.Risk == schema.RiskCritical
		case "writes-after-promotion-boundary":
			sawBoundary = node.Risk == schema.RiskCritical && node.Reversibility == schema.Irreversible
		}
	}
	if !sawApproval || !sawBoundary {
		t.Fatalf("graph did not include critical approval and irreversible boundary: %+v", req.Graph.Nodes)
	}
}
