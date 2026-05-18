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
	SagaID             string       `json:"saga_id,omitempty"`
	OperationID        string       `json:"operation_id,omitempty"`
	Group              string       `json:"group"`
	Env                string       `json:"env,omitempty"`
	ReleaseID          string       `json:"release_id"`
	ReleaseManifestKey string       `json:"release_manifest_key,omitempty"`
	RuntimeManifestKey string       `json:"runtime_manifest_key,omitempty"`
	Members            []int        `json:"members,omitempty"`
	Member             int          `json:"member"`
	MaxUnavailable     int          `json:"max_unavailable,omitempty"`
	Recipe             string       `json:"recipe,omitempty"`
	TraceID            string       `json:"trace_id,omitempty"`
	Actor              schema.Actor `json:"actor"`
	CreatedAt          time.Time    `json:"created_at,omitempty"`
}

func StatefulOrderedUpdate(req StatefulOrderedUpdateRequest) (saga.CreateRequest, error) {
	req = NormalizeStatefulOrderedUpdateRequest(req)
	if err := validateStatefulOrderedUpdateRequest(req); err != nil {
		return saga.CreateRequest{}, err
	}
	now := canonical.Time(req.CreatedAt.UTC())
	nodes, edges := statefulOrderedUpdateGraph(req)
	params := mustJSON(req)
	return saga.CreateRequest{
		Intent: schema.SagaIntent{
			SchemaVersion: schema.Version,
			SagaID:        req.SagaID,
			Kind:          StatefulOrderedUpdateKind,
			Target:        schema.Target{Kind: "stateful-group", Name: req.Group},
			Actor:         req.Actor,
			TraceID:       req.TraceID,
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
			Summary:       fmt.Sprintf("ordered update stateful group %s to release %s", req.Group, req.ReleaseID),
			CreatedAt:     now,
			Params:        params,
		},
		Graph:   schema.SagaGraph{SchemaVersion: schema.Version, SagaID: req.SagaID, Nodes: nodes, Edges: edges, CreatedAt: now},
		Control: schema.SagaControl{SchemaVersion: schema.Version, SagaID: req.SagaID, Status: schema.SagaPending, UpdatedAt: now, TraceID: req.TraceID},
	}, nil
}

func NormalizeStatefulOrderedUpdateRequest(req StatefulOrderedUpdateRequest) StatefulOrderedUpdateRequest {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	req.SagaID = strings.TrimSpace(req.SagaID)
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.Group = strings.TrimSpace(req.Group)
	req.Env = strings.TrimSpace(req.Env)
	req.ReleaseID = strings.TrimSpace(req.ReleaseID)
	req.ReleaseManifestKey = strings.TrimSpace(req.ReleaseManifestKey)
	req.RuntimeManifestKey = strings.TrimSpace(req.RuntimeManifestKey)
	req.Recipe = strings.TrimSpace(req.Recipe)
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(req.CreatedAt, req.Group+"stateful-ordered")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(req.CreatedAt, req.TraceID)
	}
	if req.SagaID == "" {
		req.SagaID = "saga_" + events.NewID(req.CreatedAt, req.OperationID)
	}
	if req.MaxUnavailable <= 0 {
		req.MaxUnavailable = 1
	}
	req.Members = append([]int(nil), req.Members...)
	sort.Ints(req.Members)
	return req
}

func validateStatefulOrderedUpdateRequest(req StatefulOrderedUpdateRequest) error {
	if req.Group == "" {
		return errors.New("stateful group is required")
	}
	if req.ReleaseID == "" {
		return errors.New("release ID is required")
	}
	if len(req.Members) == 0 {
		return errors.New("ordered update members are required; build them from StatefulGroup control")
	}
	seen := map[int]bool{}
	for _, member := range req.Members {
		if member < 0 {
			return errors.New("member ordinal must be non-negative")
		}
		if seen[member] {
			return fmt.Errorf("duplicate stateful member ordinal %d", member)
		}
		seen[member] = true
	}
	if req.MaxUnavailable < 1 {
		return errors.New("max unavailable must be at least 1")
	}
	return nil
}

func statefulOrderedUpdateGraph(req StatefulOrderedUpdateRequest) ([]schema.SagaNode, []schema.SagaEdge) {
	nodes := []schema.SagaNode{{
		ID:            "plan-ordered-members",
		Kind:          "stateful.ordered_update.plan",
		Params:        mustJSON(req),
		Compensate:    &schema.CompensationSpec{Kind: "stateful.ordered_update.plan.compensate", Params: mustJSON(req)},
		Risk:          schema.RiskLow,
		Reversibility: schema.Reversible,
	}}
	edges := make([]schema.SagaEdge, 0, len(req.Members)+1)
	previous := "plan-ordered-members"
	for _, member := range req.Members {
		params := req
		params.Member = member
		nodeID := fmt.Sprintf("update-member-%d", member)
		nodes = append(nodes, schema.SagaNode{
			ID:            nodeID,
			Kind:          "stateful.member.ordered_update",
			Requires:      []string{previous},
			Params:        mustJSON(params),
			Compensate:    &schema.CompensationSpec{Kind: "stateful.member.ordered_update.compensate", Params: mustJSON(params)},
			Retry:         &schema.RetryPolicy{MaxAttempts: 2, Backoff: "2s"},
			Risk:          schema.RiskMedium,
			Reversibility: schema.Compensatable,
		})
		edges = append(edges, schema.SagaEdge{From: previous, To: nodeID})
		previous = nodeID
	}
	nodes = append(nodes, schema.SagaNode{
		ID:            "mark-group-complete",
		Kind:          "stateful.ordered_update.complete",
		Requires:      []string{previous},
		Params:        mustJSON(req),
		Risk:          schema.RiskLow,
		Reversibility: schema.Reversible,
	})
	edges = append(edges, schema.SagaEdge{From: previous, To: "mark-group-complete"})
	return nodes, edges
}
