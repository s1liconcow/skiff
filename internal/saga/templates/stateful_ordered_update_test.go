package templates_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/saga/templates"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestStatefulOrderedUpdateBuildsSequentialMemberGraph(t *testing.T) {
	req, err := templates.StatefulOrderedUpdate(templates.StatefulOrderedUpdateRequest{
		SagaID:             "saga_stateful_update",
		OperationID:        "op_stateful_update",
		Group:              "orders-stream",
		Env:                "prod",
		ReleaseID:          "rel_new",
		ReleaseManifestKey: "services/orders-stream/releases/rel_new/release.json",
		RuntimeManifestKey: "services/orders-stream/releases/rel_new/runtime-manifest.json",
		Members:            []int{2, 0, 1},
		MaxUnavailable:     1,
		Recipe:             "nats-jetstream",
		TraceID:            "tr_stateful_update",
		Actor:              schema.Actor{ID: "agent-one", Type: "agent"},
		CreatedAt:          time.Date(2026, 5, 18, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StatefulOrderedUpdate returned error: %v", err)
	}
	if req.Intent.Kind != templates.StatefulOrderedUpdateKind || req.Intent.Risk != schema.RiskMedium || req.Intent.Reversibility != schema.Compensatable {
		t.Fatalf("unexpected intent: %+v", req.Intent)
	}
	wantOrder := []string{"plan-ordered-members", "update-member-0", "update-member-1", "update-member-2", "mark-group-complete"}
	if got := nodeIDs(req.Graph.Nodes); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("node order = %v, want %v", got, wantOrder)
	}
	if req.Graph.Nodes[1].Compensate == nil || req.Graph.Nodes[1].Compensate.Kind != "stateful.member.ordered_update.compensate" {
		t.Fatalf("member update missing compensation: %+v", req.Graph.Nodes[1].Compensate)
	}
	var params struct {
		Member             int    `json:"member"`
		ReleaseID          string `json:"release_id"`
		ReleaseManifestKey string `json:"release_manifest_key"`
		MaxUnavailable     int    `json:"max_unavailable"`
	}
	if err := json.Unmarshal(req.Graph.Nodes[1].Params, &params); err != nil {
		t.Fatalf("decode member params: %v", err)
	}
	if params.Member != 0 || params.ReleaseID != "rel_new" || params.ReleaseManifestKey == "" || params.MaxUnavailable != 1 {
		t.Fatalf("unexpected member params: %+v", params)
	}
	if _, err := sagastate.TopologicalOrder(req.Graph); err != nil {
		t.Fatalf("graph is not topological: %v", err)
	}
}

func TestStatefulOrderedUpdateRequiresMembersFromControl(t *testing.T) {
	_, err := templates.StatefulOrderedUpdate(templates.StatefulOrderedUpdateRequest{
		Group:     "orders-stream",
		ReleaseID: "rel_new",
		Actor:     schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:   "tr_stateful_update",
	})
	if err == nil || !strings.Contains(err.Error(), "members are required") {
		t.Fatalf("error = %v, want member requirement", err)
	}
}

func TestStatefulReplaceMemberBuildsExecutableHighRiskGraph(t *testing.T) {
	req, err := templates.StatefulReplaceMember(templates.StatefulReplaceMemberRequest{
		SagaID:      "saga_replace",
		OperationID: "op_replace",
		Group:       "orders-stream",
		Env:         "prod",
		Member:      0,
		Reason:      "instance failed health checks",
		TraceID:     "tr_replace",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
		CreatedAt:   time.Date(2026, 5, 18, 3, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StatefulReplaceMember returned error: %v", err)
	}
	if req.Intent.Kind != templates.StatefulReplaceMemberKind || req.Intent.Risk != schema.RiskHigh || req.Intent.Reversibility != schema.Compensatable {
		t.Fatalf("unexpected intent: %+v", req.Intent)
	}
	if len(req.Graph.Nodes) != 1 {
		t.Fatalf("node count = %d, want 1", len(req.Graph.Nodes))
	}
	node := req.Graph.Nodes[0]
	if node.ID != "replace-member" || node.Kind != "stateful.member.replace" || node.Risk != schema.RiskHigh || node.Reversibility != schema.Compensatable {
		t.Fatalf("unexpected replacement node: %+v", node)
	}
	if node.Compensate == nil || node.Compensate.Kind != "stateful.member.replace.compensate" {
		t.Fatalf("replacement node missing compensation: %+v", node.Compensate)
	}
	if _, err := sagastate.TopologicalOrder(req.Graph); err != nil {
		t.Fatalf("graph is not topological: %v", err)
	}
}

func nodeIDs(nodes []schema.SagaNode) []string {
	out := make([]string, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node.ID)
	}
	return out
}
