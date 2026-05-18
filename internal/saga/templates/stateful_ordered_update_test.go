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

func TestStatefulBackupBuildsSnapshotAndVerifyGraph(t *testing.T) {
	req, err := templates.StatefulBackup(templates.StatefulBackupRequest{
		SagaID:      "saga_backup",
		OperationID: "op_backup",
		BackupID:    "backup_01JABC",
		Group:       "orders-stream",
		Env:         "prod",
		Members:     []int{1, 0},
		TraceID:     "tr_backup",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
		CreatedAt:   time.Date(2026, 5, 18, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StatefulBackup returned error: %v", err)
	}
	if req.Intent.Kind != templates.StatefulBackupKind || req.Intent.Risk != schema.RiskMedium || req.Intent.Reversibility != schema.Compensatable {
		t.Fatalf("unexpected backup intent: %+v", req.Intent)
	}
	wantOrder := []string{"preflight", "snapshot-member-0", "snapshot-member-1", "verify-backup"}
	if got := nodeIDs(req.Graph.Nodes); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("node order = %v, want %v", got, wantOrder)
	}
	if req.Graph.Nodes[1].Kind != "stateful.backup.snapshot_member" || req.Graph.Nodes[3].Kind != "stateful.backup.verify" {
		t.Fatalf("unexpected backup graph nodes: %+v", req.Graph.Nodes)
	}
	var params struct {
		Retention string `json:"retention"`
		Member    int    `json:"member"`
	}
	if err := json.Unmarshal(req.Graph.Nodes[1].Params, &params); err != nil {
		t.Fatalf("decode backup params: %v", err)
	}
	if params.Retention != templates.DefaultStatefulBackupRetention || params.Member != 0 {
		t.Fatalf("unexpected backup params: %+v", params)
	}
	if _, err := sagastate.TopologicalOrder(req.Graph); err != nil {
		t.Fatalf("graph is not topological: %v", err)
	}
}

func TestStatefulBackupRequiresExplicitMembers(t *testing.T) {
	_, err := templates.StatefulBackup(templates.StatefulBackupRequest{
		Group:   "orders-stream",
		Actor:   schema.Actor{ID: "operator", Type: "user"},
		TraceID: "tr_backup",
	})
	if err == nil || !strings.Contains(err.Error(), "at least one member") {
		t.Fatalf("error = %v, want member requirement", err)
	}
}

func TestStatefulRestoreBuildsApprovalGatedGraph(t *testing.T) {
	req, err := templates.StatefulRestore(templates.StatefulRestoreRequest{
		SagaID:      "saga_restore",
		OperationID: "op_restore",
		RestoreID:   "restore_01JABC",
		BackupID:    "backup_01JABC",
		Group:       "orders-stream",
		Env:         "prod",
		Member:      0,
		TraceID:     "tr_restore",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
		CreatedAt:   time.Date(2026, 5, 18, 4, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("StatefulRestore returned error: %v", err)
	}
	if req.Intent.Kind != templates.StatefulRestoreKind || req.Intent.Risk != schema.RiskHigh || req.Intent.Reversibility != schema.PartiallyReversible {
		t.Fatalf("unexpected restore intent: %+v", req.Intent)
	}
	wantOrder := []string{"verify-backup", "approve-restore", "apply-restore"}
	if got := nodeIDs(req.Graph.Nodes); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("node order = %v, want %v", got, wantOrder)
	}
	if req.Graph.Nodes[1].Kind != "approval.manual" || req.Graph.Nodes[1].Risk != schema.RiskHigh {
		t.Fatalf("restore approval node missing high risk gate: %+v", req.Graph.Nodes[1])
	}
	if req.Graph.Nodes[2].Kind != "stateful.restore.apply" || req.Graph.Nodes[2].Reversibility != schema.PartiallyReversible {
		t.Fatalf("restore apply node is not marked partial reversible: %+v", req.Graph.Nodes[2])
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
