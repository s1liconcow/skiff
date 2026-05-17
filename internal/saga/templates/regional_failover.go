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
	RegionalFailoverKind          = "regional.failover"
	DefaultFailoverMaxReplicaLag  = "30s"
	DefaultFailoverTrafficCanary  = 10
	DefaultFailoverTrafficCutover = 100
)

type RegionalFailoverRequest struct {
	SagaID        string       `json:"saga_id,omitempty"`
	OperationID   string       `json:"operation_id,omitempty"`
	Stack         string       `json:"stack"`
	Service       string       `json:"service,omitempty"`
	Database      string       `json:"database"`
	Env           string       `json:"env,omitempty"`
	FromRegion    string       `json:"from_region"`
	ToRegion      string       `json:"to_region"`
	TrafficHost   string       `json:"traffic_host,omitempty"`
	MaxReplicaLag string       `json:"max_replica_lag,omitempty"`
	FreezeWrites  bool         `json:"freeze_writes,omitempty"`
	TraceID       string       `json:"trace_id,omitempty"`
	Actor         schema.Actor `json:"actor"`
	CreatedAt     time.Time    `json:"created_at,omitempty"`
}

type RegionalFailoverFactory func(RegionalFailoverRequest) (saga.CreateRequest, error)

var regionalFailoverTemplates = map[string]RegionalFailoverFactory{
	RegionalFailoverKind: RegionalFailover,
}

func LookupRegionalFailover(kind string) (RegionalFailoverFactory, bool) {
	factory, ok := regionalFailoverTemplates[kind]
	return factory, ok
}

func RegisteredRegionalFailoverKinds() []string {
	kinds := make([]string, 0, len(regionalFailoverTemplates))
	for kind := range regionalFailoverTemplates {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func RegionalFailover(req RegionalFailoverRequest) (saga.CreateRequest, error) {
	req = NormalizeRegionalFailoverRequest(req)
	if err := validateRegionalFailoverRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	params := regionalFailoverParams{
		Stack:         req.Stack,
		Service:       req.Service,
		Database:      req.Database,
		Env:           req.Env,
		OperationID:   req.OperationID,
		FromRegion:    req.FromRegion,
		ToRegion:      req.ToRegion,
		TrafficHost:   req.TrafficHost,
		MaxReplicaLag: req.MaxReplicaLag,
		FreezeWrites:  req.FreezeWrites,
	}
	nodes, edges := regionalFailoverGraph(params)
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          RegionalFailoverKind,
			Target:        schema.Target{Kind: "multi-region-stack", Name: req.Stack},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskCritical,
			Reversibility: schema.PartiallyReversible,
			Summary:       fmt.Sprintf("fail over %s from %s to %s", req.Stack, req.FromRegion, req.ToRegion),
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

func NormalizeRegionalFailoverRequest(req RegionalFailoverRequest) RegionalFailoverRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.Stack = strings.TrimSpace(req.Stack)
	req.Service = strings.TrimSpace(req.Service)
	req.Database = strings.TrimSpace(req.Database)
	req.Env = strings.TrimSpace(req.Env)
	req.FromRegion = strings.TrimSpace(req.FromRegion)
	req.ToRegion = strings.TrimSpace(req.ToRegion)
	req.TrafficHost = strings.TrimSpace(req.TrafficHost)
	req.MaxReplicaLag = strings.TrimSpace(req.MaxReplicaLag)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.TraceID = strings.TrimSpace(req.TraceID)
	if req.Service == "" {
		req.Service = req.Stack
	}
	if req.MaxReplicaLag == "" {
		req.MaxReplicaLag = DefaultFailoverMaxReplicaLag
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Stack+"failover")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	return req
}

func validateRegionalFailoverRequest(req RegionalFailoverRequest) error {
	switch {
	case req.Stack == "":
		return errors.New("stack is required")
	case req.Database == "":
		return errors.New("database is required")
	case req.FromRegion == "":
		return errors.New("from region is required")
	case req.ToRegion == "":
		return errors.New("to region is required")
	case req.FromRegion == req.ToRegion:
		return errors.New("from region and to region must differ")
	case req.Actor.ID == "" || req.Actor.Type == "":
		return errors.New("actor id and type are required")
	case req.TraceID == "":
		return errors.New("trace id is required")
	}
	if _, err := time.ParseDuration(req.MaxReplicaLag); err != nil {
		return fmt.Errorf("max replica lag is invalid: %w", err)
	}
	return nil
}

type regionalFailoverParams struct {
	Stack          string `json:"stack"`
	Service        string `json:"service,omitempty"`
	Database       string `json:"database"`
	Env            string `json:"env,omitempty"`
	OperationID    string `json:"operation_id,omitempty"`
	FromRegion     string `json:"from_region"`
	ToRegion       string `json:"to_region"`
	TrafficHost    string `json:"traffic_host,omitempty"`
	MaxReplicaLag  string `json:"max_replica_lag"`
	FreezeWrites   bool   `json:"freeze_writes,omitempty"`
	TrafficPercent int    `json:"traffic_percent,omitempty"`
}

func regionalFailoverGraph(params regionalFailoverParams) ([]schema.SagaNode, []schema.SagaEdge) {
	common := mustJSON(params)
	nodes := []schema.SagaNode{
		{
			ID:   "preflight",
			Kind: "check.preflight",
			Params: mustJSON(map[string]any{
				"service":                 params.Stack,
				"env":                     params.Env,
				"require_service_control": false,
				"require_provider":        true,
				"required_facts": []string{
					"from_region:" + params.FromRegion,
					"to_region:" + params.ToRegion,
					"database:" + params.Database,
					"max_replica_lag:" + params.MaxReplicaLag,
				},
			}),
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "verify-secondary-capacity",
			Kind:          "multiregion.verify_secondary_capacity",
			Requires:      []string{"preflight"},
			Params:        common,
			Risk:          schema.RiskLow,
			Reversibility: schema.Reversible,
		},
		{
			ID:            "verify-replica-lag",
			Kind:          "multiregion.verify_replica_lag",
			Requires:      []string{"verify-secondary-capacity"},
			Params:        common,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Reversible,
		},
		{
			ID:       "approve-failover",
			Kind:     "approval.manual",
			Requires: []string{"verify-replica-lag"},
			Params: mustJSON(map[string]any{
				"summary": "approve regional database failover before writes can move to the secondary region",
				"risk":    schema.RiskCritical,
				"facts": []string{
					"stack:" + params.Stack,
					"database:" + params.Database,
					"from_region:" + params.FromRegion,
					"to_region:" + params.ToRegion,
					"partially_irreversible_after:promote-secondary-database",
				},
			}),
			Risk:          schema.RiskCritical,
			Reversibility: schema.Reversible,
		},
	}
	previous := "approve-failover"
	if params.FreezeWrites {
		nodes = append(nodes, schema.SagaNode{
			ID:            "freeze-writes",
			Kind:          "multiregion.freeze_writes",
			Requires:      []string{previous},
			Params:        common,
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
		})
		previous = "freeze-writes"
	}
	nodes = append(nodes,
		schema.SagaNode{
			ID:            "promote-secondary-database",
			Kind:          "multiregion.promote_database",
			Requires:      []string{previous},
			Params:        common,
			Risk:          schema.RiskCritical,
			Reversibility: schema.PartiallyReversible,
		},
		schema.SagaNode{
			ID:            "update-writer-secret",
			Kind:          "multiregion.update_writer_secret",
			Requires:      []string{"promote-secondary-database"},
			Params:        common,
			Risk:          schema.RiskCritical,
			Reversibility: schema.PartiallyReversible,
		},
		schema.SagaNode{
			ID:            "writes-after-promotion-boundary",
			Kind:          "multiregion.irreversible_boundary",
			Requires:      []string{"update-writer-secret"},
			Params:        common,
			Risk:          schema.RiskCritical,
			Reversibility: schema.Irreversible,
		},
		trafficNode(params, "shift-traffic-10", "writes-after-promotion-boundary", DefaultFailoverTrafficCanary),
		schema.SagaNode{
			ID:       "metrics-gate",
			Kind:     "check.metrics_gate",
			Requires: []string{"shift-traffic-10"},
			Params: mustJSON(map[string]any{
				"service":    params.Stack,
				"env":        params.Env,
				"metric":     "multiregion.failover_5xx",
				"comparator": "<=",
				"threshold":  1,
				"window":     "5m",
			}),
			Risk:          schema.RiskMedium,
			Reversibility: schema.Reversible,
		},
		trafficNode(params, "shift-traffic-100", "metrics-gate", DefaultFailoverTrafficCutover),
		schema.SagaNode{
			ID:            "mark-secondary-primary",
			Kind:          "multiregion.mark_primary",
			Requires:      []string{"shift-traffic-100"},
			Params:        common,
			Risk:          schema.RiskHigh,
			Reversibility: schema.PartiallyReversible,
		},
	)
	return nodes, sequentialEdges(nodes)
}

func trafficNode(params regionalFailoverParams, id, requires string, percent int) schema.SagaNode {
	params.TrafficPercent = percent
	return schema.SagaNode{
		ID:            id,
		Kind:          "multiregion.shift_traffic",
		Requires:      []string{requires},
		Params:        mustJSON(params),
		Risk:          schema.RiskHigh,
		Reversibility: schema.Compensatable,
	}
}
