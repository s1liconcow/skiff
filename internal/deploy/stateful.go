package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/authz"
	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type StatefulApplier struct {
	Store      objstore.ObjectStore
	Provider   provider.Provider
	Clock      func() time.Time
	Authorizer authz.Authorizer
}

type statefulProviderApplier interface {
	ApplyStatefulGroup(ctx context.Context, graph *ir.Graph, plan *provider.Plan) (*provider.ApplyResult, error)
}

type StatefulRequest struct {
	Actor       schema.Actor `json:"actor"`
	TraceID     string       `json:"trace_id,omitempty"`
	OperationID string       `json:"operation_id,omitempty"`
	ApprovalID  string       `json:"approval_id,omitempty"`
	DryRun      bool         `json:"dry_run,omitempty"`
	PlanOnly    bool         `json:"plan_only,omitempty"`
}

type StatefulResult struct {
	OK                  bool                           `json:"ok"`
	DryRun              bool                           `json:"dry_run,omitempty"`
	PlanOnly            bool                           `json:"plan_only,omitempty"`
	Group               string                         `json:"group"`
	Env                 string                         `json:"env"`
	OperationID         string                         `json:"operation_id"`
	TraceID             string                         `json:"trace_id,omitempty"`
	Target              schema.Target                  `json:"target"`
	Risk                schema.Risk                    `json:"risk"`
	Reversibility       schema.Reversibility           `json:"reversibility"`
	Plan                *provider.Plan                 `json:"plan,omitempty"`
	GroupControl        *schema.StatefulGroupControl   `json:"group_control,omitempty"`
	MemberControls      []schema.StatefulMemberControl `json:"member_controls,omitempty"`
	ProviderResources   []StatefulProviderResource     `json:"provider_resources,omitempty"`
	Events              []events.Event                 `json:"events,omitempty"`
	RecommendedActions  []StatefulRecommendedAction    `json:"recommended_actions,omitempty"`
	MutableObjectWrites []string                       `json:"mutable_object_writes,omitempty"`
	ImmutableWrites     []string                       `json:"immutable_writes,omitempty"`
}

type StatefulInspectResult struct {
	Group              string                         `json:"group"`
	Env                string                         `json:"env"`
	OperationID        string                         `json:"operation_id,omitempty"`
	Status             schema.OperationStatus         `json:"status,omitempty"`
	TraceID            string                         `json:"trace_id,omitempty"`
	Risk               schema.Risk                    `json:"risk,omitempty"`
	Reversibility      schema.Reversibility           `json:"reversibility,omitempty"`
	GroupControl       *schema.StatefulGroupControl   `json:"group_control,omitempty"`
	MemberControls     []schema.StatefulMemberControl `json:"member_controls,omitempty"`
	OperationIntent    *schema.OperationIntent        `json:"operation_intent,omitempty"`
	OperationControl   *schema.OperationControl       `json:"operation_control,omitempty"`
	Events             []events.Event                 `json:"events,omitempty"`
	ProviderResources  []StatefulProviderResource     `json:"provider_resources,omitempty"`
	RecommendedActions []StatefulRecommendedAction    `json:"recommended_actions,omitempty"`
}

type StatefulProviderResource struct {
	Provider   string `json:"provider"`
	Kind       string `json:"kind"`
	LogicalID  string `json:"logical_id,omitempty"`
	Name       string `json:"name,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	Action     string `json:"action,omitempty"`
	Member     *int   `json:"member,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

type StatefulRecommendedAction struct {
	ID            string               `json:"id"`
	Command       string               `json:"command"`
	Mutating      bool                 `json:"mutating"`
	Safety        string               `json:"safety,omitempty"`
	Reversibility schema.Reversibility `json:"reversibility,omitempty"`
	Risk          schema.Risk          `json:"risk,omitempty"`
}

func (a StatefulApplier) Apply(ctx context.Context, graph *ir.Graph, req StatefulRequest) (*StatefulResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, fmt.Errorf("graph is required")
	}
	if len(graph.Resources.StatefulGroups) == 0 {
		return nil, fmt.Errorf("compiled graph does not contain a StatefulGroup")
	}
	if a.Provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	if a.Store == nil && !req.DryRun && !req.PlanOnly {
		return nil, fmt.Errorf("object store is required")
	}
	now := a.now()
	req = normalizeStatefulRequest(req, graph, now)
	plan, err := a.Provider.Plan(ctx, graph)
	if err != nil {
		return nil, err
	}
	providerResources := statefulProviderResources(plan)
	result := &StatefulResult{
		OK:                 true,
		DryRun:             req.DryRun,
		PlanOnly:           req.PlanOnly,
		Group:              graph.Service,
		Env:                graph.Env,
		OperationID:        req.OperationID,
		TraceID:            req.TraceID,
		Target:             schema.Target{Kind: "stateful-group", Name: graph.Service},
		Risk:               schema.RiskMedium,
		Reversibility:      schema.Compensatable,
		Plan:               plan,
		ProviderResources:  providerResources,
		RecommendedActions: statefulRecommendedActions(graph.Service, req.OperationID, req.TraceID),
	}
	if req.DryRun || req.PlanOnly {
		return result, nil
	}
	if _, err := authz.MustAuthorize(ctx, a.Authorizer, authz.Request{
		Actor:      req.Actor,
		Action:     authz.ActionDeploy,
		Target:     result.Target,
		Env:        graph.Env,
		Service:    graph.Service,
		Risk:       result.Risk,
		ApprovalID: req.ApprovalID,
		TraceID:    req.TraceID,
	}); err != nil {
		result.OK = false
		return result, err
	}

	log, err := events.NewLog(events.Options{Store: a.Store, Clock: a.now})
	if err != nil {
		return nil, err
	}
	if err := a.createStatefulOperationIntent(ctx, graph, req, now); err != nil {
		return nil, err
	}
	intentKey, _ := paths.OperationIntent(graph.Service, req.OperationID)
	result.ImmutableWrites = append(result.ImmutableWrites, intentKey)
	providerOps := providerOperationRefs(plan, a.providerName(), now)
	if err := a.createStatefulOperationControl(ctx, graph, req, schema.OperationRunning, now, providerOps, nil); err != nil {
		return nil, err
	}
	controlKey, _ := paths.OperationControl(graph.Service, req.OperationID)
	result.MutableObjectWrites = append(result.MutableObjectWrites, controlKey)
	appendEvent := func(eventType, summary string, facts ...schema.Fact) {
		event := events.NewOperationEvent(graph.Service, req.OperationID, eventType, summary, a.now(), req.TraceID+eventType)
		event.TraceID = req.TraceID
		event.Actor = &req.Actor
		event.Facts = facts
		if _, err := log.Append(ctx, event); err == nil {
			result.Events = append(result.Events, event)
			eventKey, _ := paths.OperationEvent(graph.Service, req.OperationID, event.ID)
			result.ImmutableWrites = append(result.ImmutableWrites, eventKey)
		}
	}
	fail := func(err error, step string) (*StatefulResult, error) {
		result.OK = false
		appendEvent("stateful.apply.failed", err.Error(), schema.Fact{Type: "step", Message: step})
		_ = a.updateStatefulOperationControl(ctx, graph.Service, req.OperationID, schema.OperationFailed, providerOps, []schema.StepResultRef{{
			StepID: step,
			Kind:   "stateful.apply",
			Status: "failed",
			Failure: &schema.StepFailure{
				Code:    "STATEFUL_APPLY_FAILED",
				Summary: err.Error(),
			},
			CompletedAt: canonical.Time(a.now()),
		}})
		_ = a.markStatefulGroupOperation(ctx, graph.Service, req, schema.OperationFailed, step)
		return result, err
	}

	appendEvent("stateful.apply.started", "StatefulGroup object-state apply started", schema.Fact{Type: "replicas", Message: strconv.Itoa(graph.Resources.StatefulGroups[0].Replicas)})
	if err := a.appendStatefulAudit(ctx, log, req, graph.Service, "stateful.apply.start", "started StatefulGroup object-state apply", now, "start"); err != nil {
		return fail(err, "audit_start")
	}
	auditKey, _ := paths.AuditEventForTime(now, events.NewAuditRecord(req.Actor, result.Target, "stateful.apply.start", "", req.TraceID, now, req.OperationID+"start").ID)
	result.ImmutableWrites = append(result.ImmutableWrites, auditKey)

	stateClient := state.NewClient(a.Store, state.WithClock(clockFunc(a.now)))
	groupDoc, memberDocs, err := a.writeStatefulControls(ctx, stateClient, graph, plan, req, schema.OperationRunning)
	if err != nil {
		return fail(err, "write_stateful_controls")
	}
	result.GroupControl = &groupDoc.Control
	for _, member := range memberDocs {
		result.MemberControls = append(result.MemberControls, member.Control)
	}
	groupKey, _ := paths.StatefulGroupControl(graph.Service)
	result.MutableObjectWrites = append(result.MutableObjectWrites, groupKey)
	for _, member := range memberDocs {
		result.MutableObjectWrites = append(result.MutableObjectWrites, member.Key)
	}

	appendEvent("stateful.apply.object_state_written", "StatefulGroup group and member controls written before provider effects", schema.Fact{Type: "members", Message: strconv.Itoa(len(memberDocs))})
	stepResults := []schema.StepResultRef{{
		StepID:      "write_stateful_controls",
		Kind:        "stateful.apply",
		Status:      "succeeded",
		Result:      rawJSON(map[string]any{"group": graph.Service, "replicas": graph.Resources.StatefulGroups[0].Replicas}),
		CompletedAt: canonical.Time(a.now()),
	}}
	if applier, ok := a.Provider.(statefulProviderApplier); ok {
		applyResult, err := applier.ApplyStatefulGroup(ctx, graph, plan)
		if err != nil {
			return fail(err, "apply_stateful_provider")
		}
		var resourceIDs []string
		if applyResult != nil {
			resourceIDs = append(resourceIDs, applyResult.ResourceIDs...)
		}
		appendEvent("stateful.apply.provider_applied", "StatefulGroup provider effects applied after object state", schema.Fact{Type: "provider", Message: a.providerName()}, schema.Fact{Type: "resources", Message: strconv.Itoa(len(resourceIDs))})
		stepResults = append(stepResults, schema.StepResultRef{
			StepID:      "apply_stateful_provider",
			Kind:        "stateful.apply",
			Status:      "succeeded",
			Result:      rawJSON(map[string]any{"provider": a.providerName(), "resource_ids": resourceIDs}),
			CompletedAt: canonical.Time(a.now()),
		})
	}

	if err := a.updateStatefulOperationControl(ctx, graph.Service, req.OperationID, schema.OperationSucceeded, providerOps, stepResults); err != nil {
		return fail(err, "complete_operation")
	}
	if err := a.markStatefulGroupOperation(ctx, graph.Service, req, schema.OperationSucceeded, "object-state-written"); err != nil {
		return fail(err, "mark_stateful_controls_succeeded")
	}
	groupDoc, err = stateClient.GetStatefulGroupControl(ctx, graph.Service)
	if err != nil {
		return fail(err, "read_stateful_group")
	}
	memberDocs, err = a.getStatefulMemberDocuments(ctx, stateClient, graph)
	if err != nil {
		return fail(err, "read_stateful_members")
	}
	result.GroupControl = &groupDoc.Control
	result.MemberControls = result.MemberControls[:0]
	for _, member := range memberDocs {
		result.MemberControls = append(result.MemberControls, member.Control)
	}
	appendEvent("stateful.apply.succeeded", "StatefulGroup object-state apply completed")
	successNow := a.now()
	if err := a.appendStatefulAudit(ctx, log, req, graph.Service, "stateful.apply", "applied StatefulGroup object state", successNow, "success"); err != nil {
		return fail(err, "audit_success")
	}
	successAuditKey, _ := paths.AuditEventForTime(successNow, events.NewAuditRecord(req.Actor, result.Target, "stateful.apply", "", req.TraceID, successNow, req.OperationID+"success").ID)
	result.ImmutableWrites = append(result.ImmutableWrites, successAuditKey)
	return result, nil
}

func (a StatefulApplier) Inspect(ctx context.Context, group, operationID, traceID string) (*StatefulInspectResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(group) == "" {
		return nil, fmt.Errorf("stateful group is required")
	}
	if a.Store == nil {
		return nil, fmt.Errorf("object store is required")
	}
	stateClient := state.NewClient(a.Store, state.WithClock(clockFunc(a.now)))
	groupDoc, err := stateClient.GetStatefulGroupControl(ctx, group)
	if err != nil {
		return nil, err
	}
	if operationID == "" && groupDoc.Control.Operation != nil {
		operationID = groupDoc.Control.Operation.ID
	}
	result := &StatefulInspectResult{
		Group:        groupDoc.Control.Group,
		Env:          groupDoc.Control.Env,
		OperationID:  operationID,
		TraceID:      traceID,
		GroupControl: &groupDoc.Control,
	}
	for member := 0; member < groupDoc.Control.Replicas; member++ {
		doc, err := stateClient.GetStatefulMemberControl(ctx, group, member)
		if err != nil {
			if errors.Is(err, objstore.ErrNotFound) {
				continue
			}
			return nil, err
		}
		result.MemberControls = append(result.MemberControls, doc.Control)
	}
	if operationID != "" {
		intent, _ := a.readOperationIntent(ctx, group, operationID)
		control, _ := a.readOperationControl(ctx, group, operationID)
		result.OperationIntent = intent
		result.OperationControl = control
		if intent != nil {
			result.Risk = intent.Risk
			result.Reversibility = intent.Reversibility
			if result.TraceID == "" {
				result.TraceID = intent.TraceID
			}
		}
		if control != nil {
			result.Status = control.Status
			if result.TraceID == "" {
				result.TraceID = control.TraceID
			}
			result.ProviderResources = providerResourcesFromOperationRefs(control.ProviderOperations)
		}
		log, err := events.NewLog(events.Options{Store: a.Store, Clock: a.now})
		if err == nil {
			events, listErr := log.List(ctx, events.Scope{Kind: events.ScopeOperation, Service: group, Operation: operationID}, events.ListOptions{})
			if listErr == nil {
				result.Events = events
			}
		}
	}
	result.RecommendedActions = statefulRecommendedActions(group, operationID, result.TraceID)
	return result, nil
}

func (a StatefulApplier) writeStatefulControls(ctx context.Context, stateClient *state.Client, graph *ir.Graph, plan *provider.Plan, req StatefulRequest, status schema.OperationStatus) (*state.StatefulGroupDocument, []state.StatefulMemberDocument, error) {
	group := graph.Resources.StatefulGroups[0]
	groupDoc, err := a.ensureStatefulGroupControl(ctx, stateClient, graph, group, req, status)
	if err != nil {
		return nil, nil, err
	}
	members := append([]ir.StatefulMember(nil), graph.Resources.StatefulMembers...)
	sort.Slice(members, func(i, j int) bool { return members[i].Ordinal < members[j].Ordinal })
	memberDocs := make([]state.StatefulMemberDocument, 0, len(members))
	for _, member := range members {
		doc, err := a.ensureStatefulMemberControl(ctx, stateClient, graph, plan, member, req)
		if err != nil {
			return nil, nil, err
		}
		memberDocs = append(memberDocs, *doc)
	}
	next := groupDoc.Control
	next.Replicas = group.Replicas
	next.Members = memberSummaries(memberDocs)
	next.Operation = &schema.ActiveOperation{ID: req.OperationID, Kind: "stateful.apply", State: string(status), Step: "object-state-written"}
	next.UpdatedBy = req.Actor
	next.TraceID = req.TraceID
	updated, err := stateClient.UpdateStatefulGroupControlCAS(ctx, groupDoc, next)
	if err != nil {
		return nil, nil, err
	}
	return updated, memberDocs, nil
}

func (a StatefulApplier) getStatefulMemberDocuments(ctx context.Context, stateClient *state.Client, graph *ir.Graph) ([]state.StatefulMemberDocument, error) {
	members := append([]ir.StatefulMember(nil), graph.Resources.StatefulMembers...)
	sort.Slice(members, func(i, j int) bool { return members[i].Ordinal < members[j].Ordinal })
	out := make([]state.StatefulMemberDocument, 0, len(members))
	for _, member := range members {
		doc, err := stateClient.GetStatefulMemberControl(ctx, graph.Service, member.Ordinal)
		if err != nil {
			return nil, err
		}
		out = append(out, *doc)
	}
	return out, nil
}

func (a StatefulApplier) ensureStatefulGroupControl(ctx context.Context, stateClient *state.Client, graph *ir.Graph, group ir.StatefulGroup, req StatefulRequest, status schema.OperationStatus) (*state.StatefulGroupDocument, error) {
	current, err := stateClient.GetStatefulGroupControl(ctx, graph.Service)
	if err == nil {
		next := current.Control
		next.Replicas = group.Replicas
		next.Operation = &schema.ActiveOperation{ID: req.OperationID, Kind: "stateful.apply", State: string(status), Step: "object-state-started"}
		next.UpdatedBy = req.Actor
		next.TraceID = req.TraceID
		return stateClient.UpdateStatefulGroupControlCAS(ctx, current, next)
	}
	if !errors.Is(err, objstore.ErrNotFound) {
		return nil, err
	}
	created, err := stateClient.CreateStatefulGroupControl(ctx, schema.StatefulGroupControl{
		Group:     graph.Service,
		Env:       graph.Env,
		Replicas:  group.Replicas,
		Operation: &schema.ActiveOperation{ID: req.OperationID, Kind: "stateful.apply", State: string(status), Step: "object-state-started"},
		UpdatedBy: req.Actor,
		TraceID:   req.TraceID,
	})
	if errors.Is(err, objstore.ErrAlreadyExists) {
		return stateClient.GetStatefulGroupControl(ctx, graph.Service)
	}
	return created, err
}

func (a StatefulApplier) ensureStatefulMemberControl(ctx context.Context, stateClient *state.Client, graph *ir.Graph, plan *provider.Plan, member ir.StatefulMember, req StatefulRequest) (*state.StatefulMemberDocument, error) {
	current, err := stateClient.GetStatefulMemberControl(ctx, graph.Service, member.Ordinal)
	if err == nil {
		next := desiredStatefulMemberControl(graph, plan, member, req, current.Control)
		return stateClient.UpdateStatefulMemberControlCAS(ctx, current, next)
	}
	if !errors.Is(err, objstore.ErrNotFound) {
		return nil, err
	}
	return stateClient.CreateStatefulMemberControl(ctx, desiredStatefulMemberControl(graph, plan, member, req, schema.StatefulMemberControl{}))
}

func desiredStatefulMemberControl(graph *ir.Graph, plan *provider.Plan, member ir.StatefulMember, req StatefulRequest, current schema.StatefulMemberControl) schema.StatefulMemberControl {
	control := current
	control.Group = graph.Service
	control.Env = graph.Env
	control.Member = member.Ordinal
	control.Zone = member.Zone
	control.DNSName = firstNonEmpty(member.DNSName, dnsNameForMember(graph, member.Ordinal), current.DNSName)
	if control.Generation <= 0 {
		control.Generation = 1
	}
	if control.Phase == "" {
		control.Phase = state.StatefulMemberReady
	}
	if id := adoptedProviderIDForMember(plan, member.Ordinal, "ec2-instance", ir.ResourceKindStatefulMember); id != "" {
		control.InstanceID = id
	}
	if id := adoptedProviderIDForMember(plan, member.Ordinal, "ebs-volume", ir.ResourceKindStatefulVolume); id != "" {
		control.VolumeID = id
	}
	control.ProviderOperations = providerOperationRefsForMember(plan, "", member.Ordinal)
	control.UpdatedBy = req.Actor
	control.TraceID = req.TraceID
	return control
}

func memberSummaries(docs []state.StatefulMemberDocument) []schema.StatefulMemberSummary {
	out := make([]schema.StatefulMemberSummary, 0, len(docs))
	sort.Slice(docs, func(i, j int) bool { return docs[i].Control.Member < docs[j].Control.Member })
	for _, doc := range docs {
		control := doc.Control
		out = append(out, schema.StatefulMemberSummary{
			Member:             control.Member,
			Generation:         control.Generation,
			ReleaseID:          control.ReleaseID,
			ReleaseManifestKey: control.ReleaseManifestKey,
			RuntimeManifestKey: control.RuntimeManifestKey,
			InstanceID:         control.InstanceID,
			VolumeID:           control.VolumeID,
			DNSName:            control.DNSName,
			Phase:              control.Phase,
		})
	}
	return out
}

func dnsNameForMember(graph *ir.Graph, ordinal int) string {
	for _, item := range graph.Resources.StatefulDNS {
		if item.MemberOrdinal == ordinal {
			return item.DNSName
		}
	}
	return ""
}

func (a StatefulApplier) markStatefulGroupOperation(ctx context.Context, group string, req StatefulRequest, status schema.OperationStatus, step string) error {
	stateClient := state.NewClient(a.Store, state.WithClock(clockFunc(a.now)))
	current, err := stateClient.GetStatefulGroupControl(ctx, group)
	if err != nil {
		return err
	}
	next := current.Control
	next.Operation = &schema.ActiveOperation{ID: req.OperationID, Kind: "stateful.apply", State: string(status), Step: step}
	next.UpdatedBy = req.Actor
	next.TraceID = req.TraceID
	_, err = stateClient.UpdateStatefulGroupControlCAS(ctx, current, next)
	return err
}

func (a StatefulApplier) createStatefulOperationIntent(ctx context.Context, graph *ir.Graph, req StatefulRequest, now time.Time) error {
	intent := schema.NewOperationIntent(req.OperationID, graph.Service, graph.Env, "stateful.apply", schema.Target{Kind: "stateful-group", Name: graph.Service}, req.Actor, req.TraceID, canonical.Time(now))
	intent.Risk = schema.RiskMedium
	intent.Reversibility = schema.Compensatable
	intent.PackageLockDigest = graph.PackageLockDigest
	intent.Summary = "apply StatefulGroup " + graph.Service + " object state"
	params := map[string]any{"group": graph.Service, "replicas": graph.Resources.StatefulGroups[0].Replicas}
	if graph.PackageLockDigest != "" {
		params["package_lock_digest"] = graph.PackageLockDigest
	}
	intent.Params = rawJSON(params)
	body, err := canonical.Marshal(intent)
	if err != nil {
		return err
	}
	key, err := paths.OperationIntent(graph.Service, req.OperationID)
	if err != nil {
		return err
	}
	_, err = a.Store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func (a StatefulApplier) createStatefulOperationControl(ctx context.Context, graph *ir.Graph, req StatefulRequest, status schema.OperationStatus, now time.Time, providerOps []schema.ProviderOperationRef, results []schema.StepResultRef) error {
	control := schema.OperationControl{
		SchemaVersion:      schema.Version,
		OperationID:        req.OperationID,
		Service:            graph.Service,
		Env:                graph.Env,
		Status:             status,
		ProviderOperations: providerOps,
		StepResults:        results,
		UpdatedAt:          canonical.Time(now),
		TraceID:            req.TraceID,
	}
	body, err := canonical.Marshal(control)
	if err != nil {
		return err
	}
	key, err := paths.OperationControl(graph.Service, req.OperationID)
	if err != nil {
		return err
	}
	_, err = a.Store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func (a StatefulApplier) updateStatefulOperationControl(ctx context.Context, service, operationID string, status schema.OperationStatus, providerOps []schema.ProviderOperationRef, results []schema.StepResultRef) error {
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		return err
	}
	obj, err := a.Store.Get(ctx, key)
	if err != nil {
		return err
	}
	var control schema.OperationControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		return err
	}
	control.Status = status
	control.ProviderOperations = providerOps
	control.StepResults = results
	control.UpdatedAt = canonical.Time(a.now())
	body, err := canonical.Marshal(control)
	if err != nil {
		return err
	}
	_, err = a.Store.CompareAndSwap(ctx, key, obj.ETag, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func (a StatefulApplier) appendStatefulAudit(ctx context.Context, log *events.Log, req StatefulRequest, group, action, summary string, now time.Time, seed string) error {
	record := events.NewAuditRecord(req.Actor, schema.Target{Kind: "stateful-group", Name: group}, action, summary, req.TraceID, now, req.OperationID+seed)
	record.Risk = schema.RiskMedium
	record.ApprovalID = req.ApprovalID
	record.Data = rawJSON(map[string]any{"operation_id": req.OperationID, "group": group})
	_, err := log.AppendAudit(ctx, record)
	return err
}

func (a StatefulApplier) readOperationIntent(ctx context.Context, service, operationID string) (*schema.OperationIntent, error) {
	key, err := paths.OperationIntent(service, operationID)
	if err != nil {
		return nil, err
	}
	obj, err := a.Store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var intent schema.OperationIntent
	if err := canonical.UnmarshalStrict(obj.Body, &intent); err != nil {
		return nil, err
	}
	return &intent, nil
}

func (a StatefulApplier) readOperationControl(ctx context.Context, service, operationID string) (*schema.OperationControl, error) {
	key, err := paths.OperationControl(service, operationID)
	if err != nil {
		return nil, err
	}
	obj, err := a.Store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	var control schema.OperationControl
	if err := canonical.UnmarshalStrict(obj.Body, &control); err != nil {
		return nil, err
	}
	return &control, nil
}

func (a StatefulApplier) now() time.Time {
	if a.Clock != nil {
		return a.Clock().UTC()
	}
	return time.Now().UTC()
}

func (a StatefulApplier) providerName() string {
	if a.Provider == nil {
		return ""
	}
	return a.Provider.Name()
}

func normalizeStatefulRequest(req StatefulRequest, graph *ir.Graph, now time.Time) StatefulRequest {
	if req.Actor.ID == "" {
		req.Actor = schema.Actor{ID: "skiff", Type: "user"}
	}
	if req.Actor.Type == "" {
		req.Actor.Type = "user"
	}
	if req.TraceID == "" {
		req.TraceID = "tr_" + events.NewID(now, graph.Service+"stateful-apply")
	}
	if req.OperationID == "" {
		req.OperationID = "op_" + events.NewID(now, req.TraceID)
	}
	return req
}

func statefulProviderResources(plan *provider.Plan) []StatefulProviderResource {
	if plan == nil {
		return nil
	}
	out := make([]StatefulProviderResource, 0, len(plan.Resources))
	for _, change := range plan.Resources {
		out = append(out, StatefulProviderResource{
			Provider:   plan.Provider,
			Kind:       change.Kind,
			LogicalID:  change.LogicalID,
			Name:       change.Name,
			ProviderID: change.ProviderID,
			Action:     change.Action,
			Member:     memberOrdinalFromChange(change),
			Summary:    change.Summary,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].LogicalID < out[j].LogicalID
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func providerOperationRefs(plan *provider.Plan, providerName string, now time.Time) []schema.ProviderOperationRef {
	if plan == nil {
		return nil
	}
	refs := make([]schema.ProviderOperationRef, 0)
	for _, change := range plan.Resources {
		if strings.TrimSpace(change.ProviderID) == "" {
			continue
		}
		refs = append(refs, schema.ProviderOperationRef{
			Provider:    firstNonEmpty(providerName, plan.Provider),
			Kind:        change.Kind,
			ID:          change.ProviderID,
			ObservedAt:  canonical.Time(now),
			Description: firstNonEmpty(change.Summary, change.LogicalID, change.Name),
		})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind == refs[j].Kind {
			return refs[i].ID < refs[j].ID
		}
		return refs[i].Kind < refs[j].Kind
	})
	return refs
}

func providerOperationRefsForMember(plan *provider.Plan, providerName string, member int) []schema.ProviderOperationRef {
	if plan == nil {
		return nil
	}
	refs := make([]schema.ProviderOperationRef, 0)
	for _, change := range plan.Resources {
		if change.ProviderID == "" {
			continue
		}
		ordinal := memberOrdinalFromChange(change)
		if ordinal == nil || *ordinal != member {
			continue
		}
		if change.Action == provider.ActionCreate {
			continue
		}
		refs = append(refs, schema.ProviderOperationRef{
			Provider:    firstNonEmpty(providerName, plan.Provider),
			Kind:        change.Kind,
			ID:          change.ProviderID,
			Description: firstNonEmpty(change.Summary, change.LogicalID, change.Name),
		})
	}
	return refs
}

func providerResourcesFromOperationRefs(refs []schema.ProviderOperationRef) []StatefulProviderResource {
	out := make([]StatefulProviderResource, 0, len(refs))
	for _, ref := range refs {
		out = append(out, StatefulProviderResource{Provider: ref.Provider, Kind: ref.Kind, ProviderID: ref.ID, Summary: ref.Description})
	}
	return out
}

func adoptedProviderIDForMember(plan *provider.Plan, member int, kinds ...string) string {
	if plan == nil {
		return ""
	}
	for _, change := range plan.Resources {
		if change.ProviderID == "" {
			continue
		}
		if !stringIn(change.Kind, kinds) {
			continue
		}
		ordinal := memberOrdinalFromChange(change)
		if ordinal != nil && *ordinal == member {
			return change.ProviderID
		}
	}
	return ""
}

func memberOrdinalFromChange(change provider.PlannedChange) *int {
	var desired struct {
		MemberOrdinal *int `json:"member_ordinal"`
		Ordinal       *int `json:"ordinal"`
	}
	if len(change.Desired) > 0 {
		_ = json.Unmarshal(change.Desired, &desired)
	}
	if desired.MemberOrdinal != nil {
		return desired.MemberOrdinal
	}
	if desired.Ordinal != nil {
		return desired.Ordinal
	}
	if value := change.Tags[ir.TagMemberOrdinal]; value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return &parsed
		}
	}
	parts := strings.Split(change.LogicalID, ":")
	if len(parts) > 0 {
		if parsed, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			return &parsed
		}
	}
	return nil
}

func statefulRecommendedActions(group, operationID, traceID string) []StatefulRecommendedAction {
	return []StatefulRecommendedAction{
		{ID: "inspect_stateful", Command: commandWithTrace(fmt.Sprintf("skiff stateful inspect %s --format json", group), traceID), Mutating: false},
		{ID: "inspect_operation", Command: commandWithTrace(fmt.Sprintf("skiff ops inspect --service %s --operation %s --format json", group, operationID), traceID), Mutating: false},
		{ID: "recover_member", Command: commandWithTrace(fmt.Sprintf("skiff ops run %s replace-member --member <ordinal> --format json", group), traceID), Mutating: true, Safety: "requires explicit member fencing before volume attach", Reversibility: schema.Compensatable, Risk: schema.RiskHigh},
	}
}

func commandWithTrace(command, traceID string) string {
	if traceID == "" {
		return command
	}
	return command + " --trace-id " + traceID
}

func stringIn(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
