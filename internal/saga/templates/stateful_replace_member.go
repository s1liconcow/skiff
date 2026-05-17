package templates

import (
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/saga"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	StatefulReplaceMemberKind = "stateful.replace_member"
	StatefulOrderedUpdateKind = "stateful.ordered_update"
)

type StatefulReplaceMemberRequest struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id,omitempty"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Member      int          `json:"member"`
	Reason      string       `json:"reason,omitempty"`
	TraceID     string       `json:"trace_id,omitempty"`
	Actor       schema.Actor `json:"actor"`
	CreatedAt   time.Time    `json:"created_at,omitempty"`
}

func StatefulReplaceMember(req StatefulReplaceMemberRequest) (saga.CreateRequest, error) {
	req = NormalizeStatefulReplaceMemberRequest(req)
	if req.Group == "" {
		return saga.CreateRequest{}, errors.New("stateful group is required")
	}
	if req.Member < 0 {
		return saga.CreateRequest{}, errors.New("member ordinal must be non-negative")
	}
	now := canonical.Time(req.CreatedAt.UTC())
	params := mustJSON(req)
	nodes := []schema.SagaNode{
		{ID: "acquire-member-lease", Kind: "stateful.member.acquire_lease", Params: params, Risk: schema.RiskLow, Reversibility: schema.Reversible},
		{ID: "fence-old-instance", Kind: "stateful.member.fence_instance", Requires: []string{"acquire-member-lease"}, Params: params, Risk: schema.RiskHigh, Reversibility: schema.Compensatable},
		{ID: "detach-volume", Kind: "stateful.volume.detach", Requires: []string{"fence-old-instance"}, Params: params, Risk: schema.RiskHigh, Reversibility: schema.Compensatable},
		{ID: "launch-replacement", Kind: "stateful.member.launch_replacement", Requires: []string{"detach-volume"}, Params: params, Risk: schema.RiskMedium, Reversibility: schema.Compensatable},
		{ID: "attach-volume", Kind: "stateful.volume.attach", Requires: []string{"launch-replacement"}, Params: params, Risk: schema.RiskHigh, Reversibility: schema.Compensatable},
		{ID: "boot-same-identity", Kind: "stateful.member.boot_identity", Requires: []string{"attach-volume"}, Params: params, Risk: schema.RiskMedium, Reversibility: schema.Compensatable},
		{ID: "run-recipe-recovery", Kind: "stateful.recipe.recover", Requires: []string{"boot-same-identity"}, Params: params, Risk: schema.RiskMedium, Reversibility: schema.Compensatable},
		{ID: "verify-member", Kind: "stateful.recipe.health", Requires: []string{"run-recipe-recovery"}, Params: params, Risk: schema.RiskLow, Reversibility: schema.Reversible},
		{ID: "publish-member-control", Kind: "stateful.member.publish_control", Requires: []string{"verify-member"}, Params: params, Risk: schema.RiskMedium, Reversibility: schema.Compensatable},
	}
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          StatefulReplaceMemberKind,
			Target:        schema.Target{Kind: "stateful-member", Name: fmt.Sprintf("%s/%d", req.Group, req.Member)},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskHigh,
			Reversibility: schema.Compensatable,
			Summary:       fmt.Sprintf("replace stateful member %s/%d with explicit fencing", req.Group, req.Member),
			CreatedAt:     now,
			Params:        params,
		},
		Graph:   schema.SagaGraph{SchemaVersion: schema.Version, SagaID: req.SagaID, Nodes: nodes, Edges: sequentialEdges(nodes), CreatedAt: now},
		Control: schema.SagaControl{SchemaVersion: schema.Version, SagaID: req.SagaID, Status: schema.SagaPending, UpdatedAt: now, TraceID: req.TraceID},
	}, nil
}

func NormalizeStatefulReplaceMemberRequest(req StatefulReplaceMemberRequest) StatefulReplaceMemberRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Group+"stateful-replace")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	return req
}

type StatefulOrderedUpdateRequest struct {
	SagaID    string       `json:"saga_id,omitempty"`
	Group     string       `json:"group"`
	Env       string       `json:"env,omitempty"`
	Members   []int        `json:"members,omitempty"`
	TraceID   string       `json:"trace_id,omitempty"`
	Actor     schema.Actor `json:"actor"`
	CreatedAt time.Time    `json:"created_at,omitempty"`
}

func StatefulOrderedUpdate(req StatefulOrderedUpdateRequest) (saga.CreateRequest, error) {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Group+"stateful-ordered")
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.Group == "" {
		return saga.CreateRequest{}, errors.New("stateful group is required")
	}
	now := canonical.Time(req.CreatedAt.UTC())
	nodes := []schema.SagaNode{{ID: "plan-ordered-members", Kind: "stateful.ordered_update.plan", Params: mustJSON(req), Risk: schema.RiskLow, Reversibility: schema.Reversible}}
	return saga.CreateRequest{
		Intent:  schema.SagaIntent{SchemaVersion: schema.Version, SagaID: req.SagaID, Kind: StatefulOrderedUpdateKind, Target: schema.Target{Kind: "stateful-group", Name: req.Group}, Actor: req.Actor, TraceID: req.TraceID, Risk: schema.RiskMedium, Reversibility: schema.Compensatable, Summary: "plan ordered stateful member update for " + req.Group, CreatedAt: now, Params: mustJSON(req)},
		Graph:   schema.SagaGraph{SchemaVersion: schema.Version, SagaID: req.SagaID, Nodes: nodes, Edges: nil, CreatedAt: now},
		Control: schema.SagaControl{SchemaVersion: schema.Version, SagaID: req.SagaID, Status: schema.SagaPending, UpdatedAt: now, TraceID: req.TraceID},
	}, nil
}
