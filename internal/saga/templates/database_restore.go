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

const (
	DatabaseRestoreKind        = "database.restore"
	DatabaseRestoreModeNewDB   = "new-db-cutover"
	DefaultDatabaseSmokeQuery  = "select 1"
	defaultRestoreDatabaseSuff = "restore"
)

type DatabaseRestoreRequest struct {
	SagaID           string       `json:"saga_id,omitempty"`
	OperationID      string       `json:"operation_id,omitempty"`
	Database         string       `json:"database"`
	Env              string       `json:"env,omitempty"`
	Service          string       `json:"service,omitempty"`
	ReleaseID        string       `json:"release_id,omitempty"`
	RestorePoint     string       `json:"restore_point,omitempty"`
	RestoreTime      string       `json:"restore_time,omitempty"`
	Mode             string       `json:"mode,omitempty"`
	RestoredDatabase string       `json:"restored_database,omitempty"`
	SecretRef        string       `json:"secret_ref,omitempty"`
	SmokeQuery       string       `json:"smoke_query,omitempty"`
	TraceID          string       `json:"trace_id,omitempty"`
	Actor            schema.Actor `json:"actor"`
	CreatedAt        time.Time    `json:"created_at,omitempty"`
}

type DatabaseRestoreFactory func(DatabaseRestoreRequest) (saga.CreateRequest, error)

var databaseRestoreTemplates = map[string]DatabaseRestoreFactory{
	DatabaseRestoreKind: DatabaseRestore,
}

func LookupDatabaseRestore(kind string) (DatabaseRestoreFactory, bool) {
	factory, ok := databaseRestoreTemplates[kind]
	return factory, ok
}

func DatabaseRestore(req DatabaseRestoreRequest) (saga.CreateRequest, error) {
	req = NormalizeDatabaseRestoreRequest(req)
	if err := validateDatabaseRestoreRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	params := databaseParams{
		Database:         req.Database,
		Env:              req.Env,
		Service:          req.Service,
		OperationID:      req.OperationID,
		RestorePoint:     req.RestorePoint,
		RestoreTime:      req.RestoreTime,
		RestoredDatabase: req.RestoredDatabase,
		SecretRef:        req.SecretRef,
		SmokeQuery:       req.SmokeQuery,
		ReleaseID:        req.ReleaseID,
		Mode:             req.Mode,
	}
	nodes, edges := databaseRestoreGraph(params)
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          DatabaseRestoreKind,
			Target:        schema.Target{Kind: "database", Name: req.Database},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskHigh,
			Reversibility: schema.PartiallyReversible,
			Summary:       fmt.Sprintf("restore database %s to new instance %s", req.Database, req.RestoredDatabase),
			CreatedAt:     now,
			Params:        mustJSON(params),
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

func NormalizeDatabaseRestoreRequest(req DatabaseRestoreRequest) DatabaseRestoreRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.Database = strings.TrimSpace(req.Database)
	req.Env = strings.TrimSpace(req.Env)
	req.Service = strings.TrimSpace(req.Service)
	req.ReleaseID = strings.TrimSpace(req.ReleaseID)
	req.RestorePoint = strings.TrimSpace(req.RestorePoint)
	req.RestoreTime = strings.TrimSpace(req.RestoreTime)
	req.Mode = strings.TrimSpace(req.Mode)
	req.RestoredDatabase = strings.TrimSpace(req.RestoredDatabase)
	req.SecretRef = strings.TrimSpace(req.SecretRef)
	req.SmokeQuery = strings.TrimSpace(req.SmokeQuery)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.TraceID = strings.TrimSpace(req.TraceID)
	if req.Mode == "" {
		req.Mode = DatabaseRestoreModeNewDB
	}
	if req.SmokeQuery == "" {
		req.SmokeQuery = DefaultDatabaseSmokeQuery
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Database+"restore")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	if req.RestoredDatabase == "" {
		req.RestoredDatabase = req.Database + "-" + defaultRestoreDatabaseSuff + "-" + strings.ToLower(events.NewID(req.CreatedAt, req.SagaID))[:10]
	}
	return req
}

func validateDatabaseRestoreRequest(req DatabaseRestoreRequest) error {
	switch {
	case req.Database == "":
		return errors.New("database is required")
	case req.RestorePoint == "" && req.RestoreTime == "":
		return errors.New("restore point or restore time is required")
	case req.Mode != DatabaseRestoreModeNewDB:
		return fmt.Errorf("unsupported restore mode %q; use %s", req.Mode, DatabaseRestoreModeNewDB)
	case req.RestoredDatabase == "":
		return errors.New("restored database name is required")
	case req.SecretRef == "":
		return errors.New("secret ref is required")
	case req.Actor.ID == "" || req.Actor.Type == "":
		return errors.New("actor id and type are required")
	case req.TraceID == "":
		return errors.New("trace id is required")
	}
	if req.RestoreTime != "" {
		if _, err := time.Parse(time.RFC3339, req.RestoreTime); err != nil {
			return fmt.Errorf("restore time must be RFC3339: %w", err)
		}
	}
	return nil
}

func databaseRestoreGraph(params databaseParams) ([]schema.SagaNode, []schema.SagaEdge) {
	nodes := []schema.SagaNode{
		databasePreflightNode(params.Service, params.Env, params.Database, params.OperationID, params.Mode),
		{
			ID:            "verify-restore-point",
			Kind:          "database.verify_restore_point",
			Requires:      []string{"preflight"},
			Params:        mustJSON(params),
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "snapshot-current",
			Kind:          "database.snapshot",
			Requires:      []string{"verify-restore-point"},
			Params:        mustJSON(params),
			Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "restore-new-db",
			Kind:          "database.restore_to_new_instance",
			Requires:      []string{"snapshot-current"},
			Params:        mustJSON(params),
			Compensate:    &schema.CompensationSpec{Kind: "database.retire_restored_instance", Params: mustJSON(params)},
			Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "wait-restored-db",
			Kind:          "database.wait_available",
			Requires:      []string{"restore-new-db"},
			Params:        mustJSON(params),
			Risk:          schema.RiskMedium,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "smoke-query",
			Kind:          "database.run_smoke_query",
			Requires:      []string{"wait-restored-db"},
			Params:        mustJSON(params),
			Risk:          schema.RiskMedium,
			Reversibility: schema.Reversible,
		},
	}
	edges := sequentialEdges(nodes)
	previous := "smoke-query"
	if params.Service != "" {
		nodes = append(nodes, schema.SagaNode{
			ID:            "shadow-service",
			Kind:          "database.shadow_service_test",
			Requires:      []string{previous},
			Params:        mustJSON(params),
			Risk:          schema.RiskMedium,
			Reversibility: schema.Reversible,
		})
		edges = append(edges, schema.SagaEdge{From: previous, To: "shadow-service"})
		previous = "shadow-service"
	}
	nodes = append(nodes,
		schema.SagaNode{
			ID:       "approve-cutover",
			Kind:     "approval.manual",
			Requires: []string{previous},
			Params: mustJSON(map[string]any{
				"summary": "approve database cutover after restored database checks passed",
				"risk":    schema.RiskHigh,
				"facts": []string{
					"source_database:" + params.Database,
					"restored_database:" + params.RestoredDatabase,
					"secret_ref:" + params.SecretRef,
					"mode:" + params.Mode,
				},
			}),
			Risk:          schema.RiskHigh,
			Reversibility: schema.Reversible,
		},
		schema.SagaNode{
			ID:            "update-secret-pointer",
			Kind:          "database.secret_update_pointer",
			Requires:      []string{"approve-cutover"},
			Params:        mustJSON(params),
			Compensate:    &schema.CompensationSpec{Kind: "database.secret_update_pointer", Params: mustJSON(params)},
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
		},
	)
	edges = append(edges,
		schema.SagaEdge{From: previous, To: "approve-cutover"},
		schema.SagaEdge{From: "approve-cutover", To: "update-secret-pointer"},
	)
	previous = "update-secret-pointer"
	if params.Service != "" {
		nodes = append(nodes,
			schema.SagaNode{
				ID:            "roll-service",
				Kind:          "service.rollout_restart",
				Requires:      []string{previous},
				Params:        mustJSON(params),
				Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
				Risk:          schema.RiskHigh,
				Reversibility: schema.PartiallyReversible,
			},
			schema.SagaNode{
				ID:       "verify-service",
				Kind:     "check.service_healthy",
				Requires: []string{"roll-service"},
				Params: mustJSON(map[string]any{
					"service":          params.Service,
					"env":              params.Env,
					"allowed_statuses": []string{"healthy", "ok", "active", "configured", "running", "applied", "unchanged"},
				}),
				Risk:          schema.RiskMedium,
				Reversibility: schema.Reversible,
			},
		)
		edges = append(edges,
			schema.SagaEdge{From: previous, To: "roll-service"},
			schema.SagaEdge{From: "roll-service", To: "verify-service"},
		)
	}
	return nodes, edges
}

func sequentialEdges(nodes []schema.SagaNode) []schema.SagaEdge {
	edges := make([]schema.SagaEdge, 0, len(nodes)-1)
	for i := 1; i < len(nodes); i++ {
		edges = append(edges, schema.SagaEdge{From: nodes[i-1].ID, To: nodes[i].ID})
	}
	return edges
}
