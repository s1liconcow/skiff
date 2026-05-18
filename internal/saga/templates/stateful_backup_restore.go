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
	StatefulBackupKind             = "stateful.backup"
	StatefulRestoreKind            = "stateful.restore"
	StatefulRestoreModeMember      = "member"
	DefaultStatefulBackupRetention = "168h"
)

type StatefulBackupRequest struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id,omitempty"`
	BackupID    string       `json:"backup_id,omitempty"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Members     []int        `json:"members,omitempty"`
	Member      int          `json:"member,omitempty"`
	Reason      string       `json:"reason,omitempty"`
	Retention   string       `json:"retention,omitempty"`
	TraceID     string       `json:"trace_id,omitempty"`
	Actor       schema.Actor `json:"actor"`
	CreatedAt   time.Time    `json:"created_at,omitempty"`
}

type StatefulRestoreRequest struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id,omitempty"`
	RestoreID   string       `json:"restore_id,omitempty"`
	BackupID    string       `json:"backup_id"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Member      int          `json:"member"`
	Mode        string       `json:"mode,omitempty"`
	Reason      string       `json:"reason,omitempty"`
	ApprovalID  string       `json:"approval_id,omitempty"`
	TraceID     string       `json:"trace_id,omitempty"`
	Actor       schema.Actor `json:"actor"`
	CreatedAt   time.Time    `json:"created_at,omitempty"`
}

func StatefulBackup(req StatefulBackupRequest) (saga.CreateRequest, error) {
	req = NormalizeStatefulBackupRequest(req)
	if err := validateStatefulBackupRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	nodes, edges := statefulBackupGraph(req)
	params := mustJSON(req)
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          StatefulBackupKind,
			Target:        schema.Target{Kind: "stateful-group", Name: req.Group},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
			Summary:       fmt.Sprintf("backup stateful group %s", req.Group),
			CreatedAt:     now,
			Params:        params,
		},
		Graph:   schema.SagaGraph{SchemaVersion: schema.Version, SagaID: req.SagaID, Nodes: nodes, Edges: edges, CreatedAt: now},
		Control: schema.SagaControl{SchemaVersion: schema.Version, SagaID: req.SagaID, Status: schema.SagaPending, UpdatedAt: now, TraceID: req.TraceID},
	}, nil
}

func StatefulRestore(req StatefulRestoreRequest) (saga.CreateRequest, error) {
	req = NormalizeStatefulRestoreRequest(req)
	if err := validateStatefulRestoreRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	params := mustJSON(req)
	nodes := []schema.SagaNode{
		{
			ID:            "verify-backup",
			Kind:          "stateful.restore.verify_backup",
			Params:        params,
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:       "approve-restore",
			Kind:     "approval.manual",
			Requires: []string{"verify-backup"},
			Params: mustJSON(map[string]any{
				"summary": fmt.Sprintf("approve restoring stateful member %s/%d from backup %s", req.Group, req.Member, req.BackupID),
				"risk":    schema.RiskHigh,
				"facts": []string{
					"group:" + req.Group,
					fmt.Sprintf("member:%d", req.Member),
					"backup:" + req.BackupID,
					"mode:" + req.Mode,
				},
			}),
			Risk:          schema.RiskHigh,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "apply-restore",
			Kind:          "stateful.restore.apply",
			Requires:      []string{"approve-restore"},
			Params:        params,
			Compensate:    &schema.CompensationSpec{Kind: "stateful.restore.apply.compensate", Params: params},
			Risk:          schema.RiskHigh,
			Reversibility: schema.PartiallyReversible,
		},
	}
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          StatefulRestoreKind,
			Target:        schema.Target{Kind: "stateful-member", Name: fmt.Sprintf("%s/%d", req.Group, req.Member)},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskHigh,
			Reversibility: schema.PartiallyReversible,
			Summary:       fmt.Sprintf("restore stateful member %s/%d from backup %s", req.Group, req.Member, req.BackupID),
			CreatedAt:     now,
			Params:        params,
		},
		Graph:   schema.SagaGraph{SchemaVersion: schema.Version, SagaID: req.SagaID, Nodes: nodes, Edges: sequentialEdges(nodes), CreatedAt: now},
		Control: schema.SagaControl{SchemaVersion: schema.Version, SagaID: req.SagaID, Status: schema.SagaPending, UpdatedAt: now, TraceID: req.TraceID},
	}, nil
}

func NormalizeStatefulBackupRequest(req StatefulBackupRequest) StatefulBackupRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.BackupID = strings.TrimSpace(req.BackupID)
	req.Group = strings.TrimSpace(req.Group)
	req.Env = strings.TrimSpace(req.Env)
	req.Reason = strings.TrimSpace(req.Reason)
	req.Retention = strings.TrimSpace(req.Retention)
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Group+"stateful-backup")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.BackupID == "" {
		req.BackupID = "backup_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	if req.Retention == "" {
		req.Retention = DefaultStatefulBackupRetention
	}
	req.Members = append([]int(nil), req.Members...)
	sort.Ints(req.Members)
	return req
}

func NormalizeStatefulRestoreRequest(req StatefulRestoreRequest) StatefulRestoreRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.RestoreID = strings.TrimSpace(req.RestoreID)
	req.BackupID = strings.TrimSpace(req.BackupID)
	req.Group = strings.TrimSpace(req.Group)
	req.Env = strings.TrimSpace(req.Env)
	req.Mode = strings.TrimSpace(req.Mode)
	req.Reason = strings.TrimSpace(req.Reason)
	req.ApprovalID = strings.TrimSpace(req.ApprovalID)
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.Mode == "" {
		req.Mode = StatefulRestoreModeMember
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Group+"stateful-restore")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.RestoreID == "" {
		req.RestoreID = "restore_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	return req
}

func validateStatefulBackupRequest(req StatefulBackupRequest) error {
	if req.Group == "" {
		return errors.New("stateful group is required")
	}
	if len(req.Members) == 0 {
		return errors.New("at least one member is required")
	}
	seen := map[int]bool{}
	for _, member := range req.Members {
		if member < 0 {
			return errors.New("member ordinal must be non-negative")
		}
		if seen[member] {
			return fmt.Errorf("duplicate member ordinal %d", member)
		}
		seen[member] = true
	}
	if _, err := time.ParseDuration(req.Retention); err != nil {
		return fmt.Errorf("retention must be a Go duration: %w", err)
	}
	return nil
}

func validateStatefulRestoreRequest(req StatefulRestoreRequest) error {
	if req.Group == "" {
		return errors.New("stateful group is required")
	}
	if req.BackupID == "" {
		return errors.New("backup id is required")
	}
	if req.Member < 0 {
		return errors.New("member ordinal must be non-negative")
	}
	if req.Mode != StatefulRestoreModeMember {
		return fmt.Errorf("unsupported restore mode %q", req.Mode)
	}
	return nil
}

func statefulBackupGraph(req StatefulBackupRequest) ([]schema.SagaNode, []schema.SagaEdge) {
	nodes := []schema.SagaNode{{
		ID:            "preflight",
		Kind:          "check.preflight",
		Params:        mustJSON(map[string]any{"service": req.Group, "env": req.Env, "require_service_control": false, "require_provider": true, "required_facts": []string{"stateful_group:" + req.Group, "backup:" + req.BackupID}}),
		Risk:          schema.RiskLow,
		Reversibility: schema.Reversible,
	}}
	edges := make([]schema.SagaEdge, 0, len(req.Members)+1)
	previous := "preflight"
	for _, member := range req.Members {
		params := req
		params.Member = member
		nodeID := fmt.Sprintf("snapshot-member-%d", member)
		nodes = append(nodes, schema.SagaNode{
			ID:            nodeID,
			Kind:          "stateful.backup.snapshot_member",
			Requires:      []string{previous},
			Params:        mustJSON(params),
			Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		})
		edges = append(edges, schema.SagaEdge{From: previous, To: nodeID})
		previous = nodeID
	}
	nodes = append(nodes, schema.SagaNode{
		ID:            "verify-backup",
		Kind:          "stateful.backup.verify",
		Requires:      []string{previous},
		Params:        mustJSON(req),
		Risk:          schema.RiskLow,
		Reversibility: schema.Reversible,
	})
	edges = append(edges, schema.SagaEdge{From: previous, To: "verify-backup"})
	return nodes, edges
}
