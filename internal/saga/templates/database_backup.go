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
	DatabaseBackupKind = "database.backup"
)

type DatabaseBackupRequest struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id,omitempty"`
	Database    string       `json:"database"`
	Env         string       `json:"env,omitempty"`
	Service     string       `json:"service,omitempty"`
	TraceID     string       `json:"trace_id,omitempty"`
	Actor       schema.Actor `json:"actor"`
	CreatedAt   time.Time    `json:"created_at,omitempty"`
}

type DatabaseBackupFactory func(DatabaseBackupRequest) (saga.CreateRequest, error)

var databaseBackupTemplates = map[string]DatabaseBackupFactory{
	DatabaseBackupKind: DatabaseBackup,
}

func LookupDatabaseBackup(kind string) (DatabaseBackupFactory, bool) {
	factory, ok := databaseBackupTemplates[kind]
	return factory, ok
}

func RegisteredDatabaseBackupKinds() []string {
	kinds := make([]string, 0, len(databaseBackupTemplates))
	for kind := range databaseBackupTemplates {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func DatabaseBackup(req DatabaseBackupRequest) (saga.CreateRequest, error) {
	req = NormalizeDatabaseBackupRequest(req)
	if err := validateDatabaseBackupRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	params := databaseParams{
		Database:    req.Database,
		Env:         req.Env,
		Service:     req.Service,
		OperationID: req.OperationID,
	}
	nodes := []schema.SagaNode{
		databasePreflightNode(req.Service, req.Env, req.Database, req.OperationID, "backup"),
		{
			ID:            "snapshot-current",
			Kind:          "database.snapshot",
			Requires:      []string{"preflight"},
			Params:        mustJSON(params),
			Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		},
		{
			ID:            "verify-snapshot",
			Kind:          "database.verify_restore_point",
			Requires:      []string{"snapshot-current"},
			Params:        mustJSON(params),
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
	}
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          DatabaseBackupKind,
			Target:        schema.Target{Kind: "database", Name: req.Database},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
			Summary:       fmt.Sprintf("backup database %s", req.Database),
			CreatedAt:     now,
			Params:        mustJSON(params),
		},
		Graph: schema.SagaGraph{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Nodes:         nodes,
			Edges: []schema.SagaEdge{
				{From: "preflight", To: "snapshot-current"},
				{From: "snapshot-current", To: "verify-snapshot"},
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

func NormalizeDatabaseBackupRequest(req DatabaseBackupRequest) DatabaseBackupRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.Database = strings.TrimSpace(req.Database)
	req.Env = strings.TrimSpace(req.Env)
	req.Service = strings.TrimSpace(req.Service)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.TraceID = strings.TrimSpace(req.TraceID)
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Database+"backup")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	return req
}

func validateDatabaseBackupRequest(req DatabaseBackupRequest) error {
	switch {
	case req.Database == "":
		return errors.New("database is required")
	case req.Actor.ID == "" || req.Actor.Type == "":
		return errors.New("actor id and type are required")
	case req.TraceID == "":
		return errors.New("trace id is required")
	}
	return nil
}

type databaseParams struct {
	Database         string `json:"database"`
	Env              string `json:"env,omitempty"`
	Service          string `json:"service,omitempty"`
	OperationID      string `json:"operation_id,omitempty"`
	RestorePoint     string `json:"restore_point,omitempty"`
	RestoreTime      string `json:"restore_time,omitempty"`
	RestoredDatabase string `json:"restored_database,omitempty"`
	SecretRef        string `json:"secret_ref,omitempty"`
	SmokeQuery       string `json:"smoke_query,omitempty"`
	ReleaseID        string `json:"release_id,omitempty"`
	Mode             string `json:"mode,omitempty"`
}

func databasePreflightNode(service, env, database, operationID, mode string) schema.SagaNode {
	requireServiceControl := strings.TrimSpace(service) != ""
	facts := []string{
		"database:" + database,
		"operation:" + operationID,
	}
	if strings.TrimSpace(mode) != "" {
		facts = append(facts, "mode:"+mode)
	}
	return schema.SagaNode{
		ID:   "preflight",
		Kind: "check.preflight",
		Params: mustJSON(map[string]any{
			"service":                 service,
			"env":                     env,
			"require_service_control": requireServiceControl,
			"require_provider":        true,
			"required_facts":          facts,
		}),
		Risk:          schema.RiskLow,
		Reversibility: schema.Reversible,
	}
}
