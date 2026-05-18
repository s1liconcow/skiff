package stateful

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
	stateruntime "github.com/s1liconcow/skiff/internal/stateful"
)

const (
	KindOrderedUpdatePlan     = "stateful.ordered_update.plan"
	KindOrderedMemberUpdate   = "stateful.member.ordered_update"
	KindOrderedUpdateComplete = "stateful.ordered_update.complete"
)

type OrderedUpdateParams struct {
	Group              string       `json:"group"`
	Env                string       `json:"env,omitempty"`
	OperationID        string       `json:"operation_id,omitempty"`
	ReleaseID          string       `json:"release_id"`
	ReleaseManifestKey string       `json:"release_manifest_key,omitempty"`
	RuntimeManifestKey string       `json:"runtime_manifest_key,omitempty"`
	Members            []int        `json:"members,omitempty"`
	Member             int          `json:"member,omitempty"`
	MaxUnavailable     int          `json:"max_unavailable,omitempty"`
	Recipe             string       `json:"recipe,omitempty"`
	Actor              schema.Actor `json:"actor,omitempty"`
}

type PlanOrderedUpdate struct {
	Store    objstore.ObjectStore
	Clock    func() time.Time
	LeaseTTL time.Duration
}

type OrderedMemberUpdate struct {
	Store    objstore.ObjectStore
	Recipe   stateruntime.Recipe
	Clock    func() time.Time
	LeaseTTL time.Duration
}

type CompleteOrderedUpdate struct {
	Store objstore.ObjectStore
	Clock func() time.Time
}

func New(store objstore.ObjectStore, recipe stateruntime.Recipe) []steps.Step {
	return []steps.Step{
		PlanOrderedUpdate{Store: store},
		OrderedMemberUpdate{Store: store, Recipe: recipe},
		CompleteOrderedUpdate{Store: store},
	}
}

func (PlanOrderedUpdate) Kind() string { return KindOrderedUpdatePlan }

func (s PlanOrderedUpdate) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeOrderedParams(params, false)
	return err
}

func (s PlanOrderedUpdate) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "acquire the StatefulGroup update lease and publish ordered update intent", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s PlanOrderedUpdate) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeOrderedParams(req.Node.Params, false)
	if err != nil {
		return nil, err
	}
	if s.Store == nil {
		return succeeded("planned ordered stateful update", params)
	}
	client := state.NewClient(s.Store, state.WithClock(stateClock(s.Clock)))
	current, err := client.GetStatefulGroupControl(ctx, params.Group)
	if err != nil {
		return nil, err
	}
	now := clockFunc(s.Clock)()
	owner := groupLeaseOwner(req.SagaID)
	if leaseActiveForOther(current.Control.Lease, owner, now) {
		return failed("STATEFUL_GROUP_LEASE_HELD", fmt.Sprintf("stateful group %s already has an active lease", params.Group)), nil
	}
	next := current.Control
	next.Lease = &schema.Lease{Owner: owner, Token: "lease_" + events.NewID(now, req.SagaID+params.OperationID), Generation: next.Version + 1, ExpiresAt: canonical.Time(now.Add(leaseTTL(s.LeaseTTL)))}
	next.Operation = &schema.ActiveOperation{ID: params.OperationID, Kind: "stateful.ordered_update", State: string(schema.OperationRunning), Step: "plan-ordered-members"}
	next.UpdatedBy = actorOrDefault(params.Actor, req.Intent.Actor)
	next.TraceID = req.TraceID
	if _, err := client.UpdateStatefulGroupControlCAS(ctx, current, next); err != nil {
		return nil, err
	}
	return succeeded("planned ordered stateful update", params)
}

func (s PlanOrderedUpdate) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s PlanOrderedUpdate) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, err := decodeOrderedParams(req.Node.Params, false)
	if err != nil {
		return nil, err
	}
	if s.Store != nil {
		client := state.NewClient(s.Store, state.WithClock(stateClock(s.Clock)))
		current, err := client.GetStatefulGroupControl(ctx, params.Group)
		if err != nil {
			return nil, err
		}
		next := current.Control
		if next.Lease != nil && next.Lease.Owner == groupLeaseOwner(req.SagaID) {
			next.Lease = nil
		}
		if next.Operation != nil && next.Operation.ID == params.OperationID {
			next.Operation = &schema.ActiveOperation{ID: params.OperationID, Kind: "stateful.ordered_update", State: string(schema.OperationFailed), Step: "compensate-plan"}
		}
		next.UpdatedBy = actorOrDefault(params.Actor, req.Intent.Actor)
		next.TraceID = req.TraceID
		if _, err := client.UpdateStatefulGroupControlCAS(ctx, current, next); err != nil {
			return nil, err
		}
	}
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "released ordered update group lease", Result: rawJSON(map[string]string{"summary": "released group lease; member generations are not automatically restored"})}, nil
}

func (s PlanOrderedUpdate) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (OrderedMemberUpdate) Kind() string { return KindOrderedMemberUpdate }

func (s OrderedMemberUpdate) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeOrderedParams(params, true)
	return err
}

func (s OrderedMemberUpdate) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "update one stateful member release after durable generation fencing and health checks", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
}

func (s OrderedMemberUpdate) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeOrderedParams(req.Node.Params, true)
	if err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, errors.New("stateful ordered update step requires object store")
	}
	client := state.NewClient(s.Store, state.WithClock(stateClock(s.Clock)))
	if err := enforceMaxUnavailable(ctx, client, params); err != nil {
		return failed("STATEFUL_QUORUM_RISK", err.Error()), nil
	}
	actor := actorOrDefault(params.Actor, req.Intent.Actor)
	ttl := leaseTTL(s.LeaseTTL)
	handle, doc, err := client.AcquireStatefulMemberLease(ctx, params.Group, params.Member, state.StatefulMemberLeaseOptions{
		Owner:    fmt.Sprintf("%s:member:%d", groupLeaseOwner(req.SagaID), params.Member),
		Duration: ttl,
		Actor:    actor,
		TraceID:  req.TraceID,
		Purpose:  "ordered-update",
	})
	if err != nil {
		return nil, err
	}
	releaseLease := true
	defer func() {
		if releaseLease {
			_, _ = client.ReleaseStatefulMemberLease(ctx, *handle)
		}
	}()
	if doc.Control.ReleaseID == params.ReleaseID && doc.Control.Phase == state.StatefulMemberReady {
		if err := updateGroupMemberSummary(ctx, client, params.Group, doc.Control, params.OperationID, actor, req.TraceID); err != nil {
			return nil, err
		}
		return succeeded("stateful member already updated", orderedMemberResult(params, doc.Control, nil, "already-updated"))
	}
	previous := doc.Control
	handle, doc, err = client.UpdateStatefulMemberWithLeaseCAS(ctx, *handle, func(control *schema.StatefulMemberControl) error {
		control.Phase = state.StatefulMemberUpdating
		control.Generation++
		control.ReleaseID = params.ReleaseID
		control.ReleaseManifestKey = params.ReleaseManifestKey
		control.RuntimeManifestKey = params.RuntimeManifestKey
		control.UpdatedBy = actor
		control.TraceID = req.TraceID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := updateGroupMemberSummary(ctx, client, params.Group, doc.Control, params.OperationID, actor, req.TraceID); err != nil {
		return nil, err
	}
	hooks, err := s.runRecipeHooks(ctx, req, params, doc.Control)
	if err != nil {
		_, _, _ = client.UpdateStatefulMemberWithLeaseCAS(ctx, *handle, func(control *schema.StatefulMemberControl) error {
			control.Phase = state.StatefulMemberFailed
			control.UpdatedBy = actor
			control.TraceID = req.TraceID
			return nil
		})
		return failed("STATEFUL_RECIPE_HOOK_FAILED", err.Error()), nil
	}
	_, doc, err = client.UpdateStatefulMemberWithLeaseCAS(ctx, *handle, func(control *schema.StatefulMemberControl) error {
		control.Phase = state.StatefulMemberReady
		control.ReleaseID = params.ReleaseID
		control.ReleaseManifestKey = params.ReleaseManifestKey
		control.RuntimeManifestKey = params.RuntimeManifestKey
		control.UpdatedBy = actor
		control.TraceID = req.TraceID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := updateGroupMemberSummary(ctx, client, params.Group, doc.Control, params.OperationID, actor, req.TraceID); err != nil {
		return nil, err
	}
	return succeeded("updated stateful member", orderedMemberResult(params, doc.Control, hooks, fmt.Sprintf("advanced generation from %d to %d", previous.Generation, doc.Control.Generation)))
}

func (s OrderedMemberUpdate) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s OrderedMemberUpdate) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, _ := decodeOrderedParams(req.Node.Params, true)
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "stateful member update compensation requires explicit follow-up", Result: rawJSON(map[string]any{"group": params.Group, "member": params.Member, "release_id": params.ReleaseID, "summary": "previous durable generation is not automatically restored"})}, nil
}

func (s OrderedMemberUpdate) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s OrderedMemberUpdate) runRecipeHooks(ctx context.Context, req steps.StepRequest, params OrderedUpdateParams, control schema.StatefulMemberControl) ([]string, error) {
	if s.Recipe == nil {
		return nil, nil
	}
	if params.Recipe != "" && s.Recipe.Name() != "" && s.Recipe.Name() != params.Recipe {
		return nil, fmt.Errorf("stateful recipe %q is configured but runner has recipe %q", params.Recipe, s.Recipe.Name())
	}
	recipeReq := stateruntime.RecipeRequest{
		Group:       control.Group,
		Env:         control.Env,
		Member:      control.Member,
		Generation:  control.Generation,
		InstanceID:  control.InstanceID,
		VolumeID:    control.VolumeID,
		DNSName:     control.DNSName,
		Control:     control,
		OperationID: params.OperationID,
		TraceID:     req.TraceID,
	}
	var hooks []string
	run := func(name string, call func(context.Context, stateruntime.RecipeRequest) (*stateruntime.RecipeResult, error)) error {
		result, err := call(ctx, recipeReq)
		if err != nil {
			return err
		}
		if result == nil {
			return fmt.Errorf("recipe %s hook returned no result", name)
		}
		if !result.OK {
			summary := result.Summary
			if summary == "" {
				summary = fmt.Sprintf("recipe %s hook reported not ok", name)
			}
			return errors.New(summary)
		}
		hooks = append(hooks, name)
		return nil
	}
	if err := run("stop", s.Recipe.Stop); err != nil {
		return hooks, err
	}
	if err := run("start", s.Recipe.Start); err != nil {
		return hooks, err
	}
	if err := run("recover", s.Recipe.Restore); err != nil {
		return hooks, err
	}
	if err := run("health", s.Recipe.Health); err != nil {
		return hooks, err
	}
	role, err := s.Recipe.DetectRole(ctx, recipeReq)
	if err != nil {
		return hooks, err
	}
	if role == nil || role.Role == "" {
		return hooks, errors.New("recipe role detection returned no role")
	}
	hooks = append(hooks, "detect_role")
	return hooks, nil
}

func (CompleteOrderedUpdate) Kind() string { return KindOrderedUpdateComplete }

func (s CompleteOrderedUpdate) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeOrderedParams(params, false)
	return err
}

func (s CompleteOrderedUpdate) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "mark the StatefulGroup ordered update complete and release the group lease", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s CompleteOrderedUpdate) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeOrderedParams(req.Node.Params, false)
	if err != nil {
		return nil, err
	}
	if s.Store == nil {
		return succeeded("completed ordered stateful update", params)
	}
	client := state.NewClient(s.Store, state.WithClock(stateClock(s.Clock)))
	current, err := client.GetStatefulGroupControl(ctx, params.Group)
	if err != nil {
		return nil, err
	}
	next := current.Control
	if next.Operation != nil && next.Operation.ID == params.OperationID {
		next.Operation = &schema.ActiveOperation{ID: params.OperationID, Kind: "stateful.ordered_update", State: string(schema.OperationSucceeded), Step: "complete"}
	}
	if next.Lease != nil && next.Lease.Owner == groupLeaseOwner(req.SagaID) {
		next.Lease = nil
	}
	next.UpdatedBy = actorOrDefault(params.Actor, req.Intent.Actor)
	next.TraceID = req.TraceID
	if _, err := client.UpdateStatefulGroupControlCAS(ctx, current, next); err != nil {
		return nil, err
	}
	return succeeded("completed ordered stateful update", params)
}

func (s CompleteOrderedUpdate) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s CompleteOrderedUpdate) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "complete step has no compensation", Result: rawJSON(map[string]string{"summary": "completed state is immutable history"})}, nil
}

func (s CompleteOrderedUpdate) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func decodeOrderedParams(raw json.RawMessage, requireMember bool) (OrderedUpdateParams, error) {
	var params OrderedUpdateParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, err
	}
	if params.Group == "" {
		return params, errors.New("stateful group is required")
	}
	if params.ReleaseID == "" {
		return params, errors.New("release ID is required")
	}
	if requireMember && params.Member < 0 {
		return params, errors.New("member ordinal must be non-negative")
	}
	if params.MaxUnavailable <= 0 {
		params.MaxUnavailable = 1
	}
	sort.Ints(params.Members)
	return params, nil
}

func enforceMaxUnavailable(ctx context.Context, client *state.Client, params OrderedUpdateParams) error {
	group, err := client.GetStatefulGroupControl(ctx, params.Group)
	if err != nil {
		return err
	}
	unavailable := 0
	for _, member := range group.Control.Members {
		if member.Member == params.Member {
			continue
		}
		if member.Phase != "" && member.Phase != state.StatefulMemberReady {
			unavailable++
		}
	}
	if unavailable >= params.MaxUnavailable {
		return fmt.Errorf("stateful group %s already has %d unavailable member(s), max unavailable is %d", params.Group, unavailable, params.MaxUnavailable)
	}
	return nil
}

func updateGroupMemberSummary(ctx context.Context, client *state.Client, group string, member schema.StatefulMemberControl, operationID string, actor schema.Actor, traceID string) error {
	current, err := client.GetStatefulGroupControl(ctx, group)
	if err != nil {
		return err
	}
	next := current.Control
	summary := schema.StatefulMemberSummary{
		Member:             member.Member,
		Generation:         member.Generation,
		ReleaseID:          member.ReleaseID,
		ReleaseManifestKey: member.ReleaseManifestKey,
		RuntimeManifestKey: member.RuntimeManifestKey,
		InstanceID:         member.InstanceID,
		VolumeID:           member.VolumeID,
		DNSName:            member.DNSName,
		Phase:              member.Phase,
	}
	found := false
	for i := range next.Members {
		if next.Members[i].Member == member.Member {
			next.Members[i] = summary
			found = true
			break
		}
	}
	if !found {
		next.Members = append(next.Members, summary)
	}
	sort.Slice(next.Members, func(i, j int) bool { return next.Members[i].Member < next.Members[j].Member })
	if next.Operation != nil && next.Operation.ID == operationID {
		next.Operation.Step = fmt.Sprintf("member-%d", member.Member)
	}
	next.UpdatedBy = actor
	next.TraceID = traceID
	_, err = client.UpdateStatefulGroupControlCAS(ctx, current, next)
	return err
}

func leaseActiveForOther(lease *schema.Lease, owner string, now time.Time) bool {
	if lease == nil || lease.Owner == owner {
		return false
	}
	expires, err := time.Parse(time.RFC3339Nano, lease.ExpiresAt)
	if err != nil {
		return true
	}
	return now.Before(expires)
}

func orderedMemberResult(params OrderedUpdateParams, control schema.StatefulMemberControl, hooks []string, summary string) map[string]any {
	return map[string]any{
		"group":                control.Group,
		"env":                  control.Env,
		"member":               control.Member,
		"generation":           control.Generation,
		"release_id":           control.ReleaseID,
		"release_manifest_key": control.ReleaseManifestKey,
		"runtime_manifest_key": control.RuntimeManifestKey,
		"phase":                control.Phase,
		"operation_id":         params.OperationID,
		"hooks":                hooks,
		"summary":              summary,
	}
}

func actorOrDefault(actor, fallback schema.Actor) schema.Actor {
	if actor.ID == "" {
		actor = fallback
	}
	if actor.ID == "" {
		actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if actor.Type == "" {
		actor.Type = "user"
	}
	return actor
}

func groupLeaseOwner(sagaID string) string {
	return "saga:" + sagaID + ":stateful-ordered-update"
}

func leaseTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 2 * time.Minute
	}
	return ttl
}

func clockFunc(clock func() time.Time) func() time.Time {
	if clock != nil {
		return clock
	}
	return func() time.Time { return time.Now().UTC() }
}

type stepClock func() time.Time

func (c stepClock) Now() time.Time {
	return c()
}

func stateClock(clock func() time.Time) state.Clock {
	return stepClock(clockFunc(clock))
}

func succeeded(summary string, result any) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: summary, Result: rawJSON(result)}, nil
}

func failed(code, summary string) *steps.StepResult {
	return &steps.StepResult{Status: steps.StatusFailed, Summary: summary, Failure: &schema.StepFailure{Code: code, Summary: summary}}
}

func rawJSON(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}
