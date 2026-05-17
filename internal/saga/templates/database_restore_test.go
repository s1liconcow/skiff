package templates

import (
	"testing"
	"time"

	sagastate "github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestDatabaseRestoreTemplateRequiresApprovalBeforeCutover(t *testing.T) {
	req, err := DatabaseRestore(DatabaseRestoreRequest{
		SagaID:           "saga_db_restore",
		OperationID:      "op_db_restore",
		Database:         "orders-db",
		Env:              "prod",
		Service:          "orders-api",
		RestoreTime:      "2026-05-17T02:00:00Z",
		RestoredDatabase: "orders-db-restore",
		SecretRef:        "secret://managed-database/orders-db/connection-url",
		Actor:            schema.Actor{ID: "agent-one", Type: "agent"},
		TraceID:          "tr_restore",
		CreatedAt:        time.Date(2026, 5, 17, 3, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("DatabaseRestore() error = %v", err)
	}
	if req.Intent.Risk != schema.RiskHigh || req.Intent.Reversibility != schema.PartiallyReversible {
		t.Fatalf("restore risk/reversibility = %s/%s", req.Intent.Risk, req.Intent.Reversibility)
	}
	if _, err := sagastate.TopologicalOrder(req.Graph); err != nil {
		t.Fatalf("restore graph is not valid DAG: %v", err)
	}
	positions := map[string]int{}
	for i, node := range req.Graph.Nodes {
		positions[node.ID] = i
		if node.Kind == "database.in_place_restore" {
			t.Fatalf("restore graph must not use destructive in-place restore")
		}
		if node.ID == "restore-new-db" {
			if node.Compensate == nil || node.Compensate.Kind != "database.retire_restored_instance" {
				t.Fatalf("restore-new-db compensation = %+v", node.Compensate)
			}
		}
	}
	if positions["approve-cutover"] == 0 || positions["update-secret-pointer"] == 0 {
		t.Fatalf("approval or secret update node missing: %+v", positions)
	}
	if positions["approve-cutover"] > positions["update-secret-pointer"] {
		t.Fatalf("approval must come before secret cutover: %+v", positions)
	}
	if positions["shadow-service"] == 0 || positions["shadow-service"] > positions["approve-cutover"] {
		t.Fatalf("shadow service test should happen before approval: %+v", positions)
	}
}

func TestDatabaseBackupTemplateCreatesSnapshotThenVerification(t *testing.T) {
	req, err := DatabaseBackup(DatabaseBackupRequest{
		SagaID:      "saga_db_backup",
		OperationID: "op_db_backup",
		Database:    "orders-db",
		Env:         "prod",
		Actor:       schema.Actor{ID: "skiff-cli", Type: "user"},
		TraceID:     "tr_backup",
		CreatedAt:   time.Date(2026, 5, 17, 3, 5, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("DatabaseBackup() error = %v", err)
	}
	if len(req.Graph.Nodes) != 3 {
		t.Fatalf("backup node count = %d", len(req.Graph.Nodes))
	}
	if req.Graph.Nodes[1].Kind != "database.snapshot" || req.Graph.Nodes[2].Kind != "database.verify_restore_point" {
		t.Fatalf("backup graph nodes = %+v", req.Graph.Nodes)
	}
}
