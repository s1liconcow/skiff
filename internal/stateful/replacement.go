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
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type ReplacementRunner struct {
	Store    *state.Client
	Provider provider.StatefulOperations
	Recipe   Recipe
	Audit    *events.Log
	EventLog *events.Log
	Owner    string
	LeaseTTL time.Duration
}

type ReplaceMemberRequest struct {
	Group       string       `json:"group"`
	Env         string       `json:"env"`
	Member      int          `json:"member"`
	OperationID string       `json:"operation_id"`
	SagaID      string       `json:"saga_id,omitempty"`
	TraceID     string       `json:"trace_id,omitempty"`
	Actor       schema.Actor `json:"actor"`
	Reason      string       `json:"reason,omitempty"`
}

type ReplaceMemberResult struct {
	Group              string                        `json:"group"`
	Env                string                        `json:"env"`
	Member             int                           `json:"member"`
	Generation         int64                         `json:"generation"`
	OldInstanceID      string                        `json:"old_instance_id,omitempty"`
	NewInstanceID      string                        `json:"new_instance_id,omitempty"`
	VolumeID           string                        `json:"volume_id,omitempty"`
	DNSName            string                        `json:"dns_name,omitempty"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations,omitempty"`
	Phase              string                        `json:"phase"`
}

func (r ReplacementRunner) Replace(ctx context.Context, req ReplaceMemberRequest) (*ReplaceMemberResult, error) {
	if r.Store == nil {
		return nil, errors.New("stateful replacement store is required")
	}
	if r.Provider == nil {
		return nil, errors.New("stateful provider is required")
	}
	if req.OperationID == "" {
		return nil, errors.New("operation id is required")
	}
	ttl := r.LeaseTTL
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	owner := r.Owner
	if owner == "" {
		owner = "skiff-stateful"
	}
	handle, doc, err := r.Store.AcquireStatefulMemberLease(ctx, req.Group, req.Member, state.StatefulMemberLeaseOptions{Owner: owner, Duration: ttl, Actor: req.Actor, TraceID: req.TraceID, Purpose: "replace-member"})
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = r.Store.ReleaseStatefulMemberLease(ctx, *handle)
	}()
	ensureReplacement := func(control *schema.StatefulMemberControl) {
		if control.Replacement == nil || control.Replacement.OperationID != req.OperationID {
			control.Replacement = &schema.StatefulReplacement{
				OperationID:   req.OperationID,
				SagaID:        req.SagaID,
				OldInstanceID: control.InstanceID,
				VolumeID:      control.VolumeID,
				Generation:    control.Generation + 1,
			}
		}
		control.Phase = state.StatefulMemberReplacing
		control.UpdatedBy = req.Actor
		control.TraceID = req.TraceID
	}
	handle, doc, err = r.Store.UpdateStatefulMemberWithLeaseCAS(ctx, *handle, func(control *schema.StatefulMemberControl) error {
		ensureReplacement(control)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := r.appendAudit(ctx, req, "started stateful member replacement"); err != nil {
		return nil, err
	}
	if err := r.appendProgressEvent(ctx, req, "stateful.replace_member.started", "started stateful member replacement", nil); err != nil {
		return nil, err
	}
	ref := provider.StatefulMemberRef{Group: req.Group, Env: firstNonEmpty(req.Env, doc.Control.Env), Member: req.Member}
	repl := doc.Control.Replacement
	oldInstance := repl.OldInstanceID
	volumeID := firstNonEmpty(repl.VolumeID, doc.Control.VolumeID)
	if repl.FencedAt == "" {
		result, err := r.Provider.FenceInstance(ctx, provider.FenceInstanceRequest{Ref: ref, InstanceID: oldInstance, Reason: firstNonEmpty(req.Reason, "replace failed stateful member")})
		if err != nil {
			return nil, err
		}
		handle, doc, err = r.recordProviderStep(ctx, *handle, result.ProviderOperation, func(control *schema.StatefulMemberControl) {
			ensureReplacement(control)
			control.Replacement.FencedAt = canonical.Time(firstTime(result.FencedAt))
		})
		if err != nil {
			return nil, err
		}
		if err := r.appendAudit(ctx, req, "fenced old stateful member instance"); err != nil {
			return nil, err
		}
		if err := r.appendProgressEvent(ctx, req, "stateful.replace_member.fenced", "fenced old stateful member instance", &result.ProviderOperation); err != nil {
			return nil, err
		}
		repl = doc.Control.Replacement
	}
	if repl.DetachedAt == "" {
		result, err := r.Provider.DetachVolume(ctx, provider.DetachVolumeRequest{Ref: ref, InstanceID: oldInstance, VolumeID: volumeID})
		if err != nil {
			return nil, err
		}
		handle, doc, err = r.recordProviderStep(ctx, *handle, result.ProviderOperation, func(control *schema.StatefulMemberControl) {
			ensureReplacement(control)
			control.Replacement.DetachedAt = canonical.Time(firstTime(result.CompletedAt))
		})
		if err != nil {
			return nil, err
		}
		if err := r.appendAudit(ctx, req, "detached stateful member volume"); err != nil {
			return nil, err
		}
		if err := r.appendProgressEvent(ctx, req, "stateful.replace_member.detached", "detached stateful member volume", &result.ProviderOperation); err != nil {
			return nil, err
		}
		repl = doc.Control.Replacement
	}
	if repl.NewInstanceID == "" {
		result, err := r.Provider.LaunchReplacement(ctx, provider.LaunchReplacementRequest{Ref: ref, Generation: repl.Generation, Zone: doc.Control.Zone, PreviousID: oldInstance, VolumeID: volumeID, IdentityHint: doc.Control.DNSName})
		if err != nil {
			return nil, err
		}
		handle, doc, err = r.recordProviderStep(ctx, *handle, result.ProviderOperation, func(control *schema.StatefulMemberControl) {
			ensureReplacement(control)
			control.Replacement.NewInstanceID = result.InstanceID
			control.Replacement.ReplacementLaunchedAt = canonical.Time(firstTime(result.LaunchedAt))
		})
		if err != nil {
			return nil, err
		}
		if err := r.appendAudit(ctx, req, "launched replacement stateful member instance"); err != nil {
			return nil, err
		}
		if err := r.appendProgressEvent(ctx, req, "stateful.replace_member.launched", "launched replacement stateful member instance", &result.ProviderOperation); err != nil {
			return nil, err
		}
		repl = doc.Control.Replacement
	}
	if repl.AttachedAt == "" {
		result, err := r.Provider.AttachVolume(ctx, provider.AttachVolumeRequest{Ref: ref, InstanceID: repl.NewInstanceID, VolumeID: volumeID})
		if err != nil {
			return nil, err
		}
		handle, doc, err = r.recordProviderStep(ctx, *handle, result.ProviderOperation, func(control *schema.StatefulMemberControl) {
			ensureReplacement(control)
			control.Replacement.AttachedAt = canonical.Time(firstTime(result.CompletedAt))
		})
		if err != nil {
			return nil, err
		}
		if err := r.appendAudit(ctx, req, "attached stateful member volume to replacement instance"); err != nil {
			return nil, err
		}
		if err := r.appendProgressEvent(ctx, req, "stateful.replace_member.attached", "attached stateful member volume to replacement instance", &result.ProviderOperation); err != nil {
			return nil, err
		}
		repl = doc.Control.Replacement
	}
	if repl.DNSUpdatedAt == "" && doc.Control.DNSName != "" {
		result, err := r.Provider.UpdateMemberDNS(ctx, provider.UpdateMemberDNSRequest{Ref: ref, DNSName: doc.Control.DNSName, InstanceID: repl.NewInstanceID})
		if err != nil {
			return nil, err
		}
		handle, doc, err = r.recordProviderStep(ctx, *handle, result.ProviderOperation, func(control *schema.StatefulMemberControl) {
			ensureReplacement(control)
			control.Replacement.DNSUpdatedAt = canonical.Time(firstTime(result.UpdatedAt))
		})
		if err != nil {
			return nil, err
		}
		if err := r.appendAudit(ctx, req, "updated stateful member DNS identity"); err != nil {
			return nil, err
		}
		if err := r.appendProgressEvent(ctx, req, "stateful.replace_member.dns_updated", "updated stateful member DNS identity", &result.ProviderOperation); err != nil {
			return nil, err
		}
		repl = doc.Control.Replacement
	}
	if r.Recipe != nil && repl.RecipeRecoveredAt == "" {
		if _, err := r.Recipe.Restore(ctx, recipeRequest(req, doc.Control)); err != nil {
			return nil, err
		}
		handle, doc, err = r.Store.UpdateStatefulMemberWithLeaseCAS(ctx, *handle, func(control *schema.StatefulMemberControl) error {
			ensureReplacement(control)
			control.Replacement.RecipeRecoveredAt = canonical.Time(time.Now().UTC())
			return nil
		})
		if err != nil {
			return nil, err
		}
		if err := r.appendAudit(ctx, req, "ran stateful member recipe recovery"); err != nil {
			return nil, err
		}
		if err := r.appendProgressEvent(ctx, req, "stateful.replace_member.recipe_recovered", "ran stateful member recipe recovery", nil); err != nil {
			return nil, err
		}
		repl = doc.Control.Replacement
	}
	if r.Recipe != nil && repl.VerifiedAt == "" {
		if _, err := r.Recipe.Health(ctx, recipeRequest(req, doc.Control)); err != nil {
			return nil, err
		}
		handle, doc, err = r.Store.UpdateStatefulMemberWithLeaseCAS(ctx, *handle, func(control *schema.StatefulMemberControl) error {
			ensureReplacement(control)
			control.Replacement.VerifiedAt = canonical.Time(time.Now().UTC())
			return nil
		})
		if err != nil {
			return nil, err
		}
		if err := r.appendAudit(ctx, req, "verified replacement stateful member health"); err != nil {
			return nil, err
		}
		if err := r.appendProgressEvent(ctx, req, "stateful.replace_member.verified", "verified replacement stateful member health", nil); err != nil {
			return nil, err
		}
	}
	_, doc, err = r.Store.UpdateStatefulMemberWithLeaseCAS(ctx, *handle, func(control *schema.StatefulMemberControl) error {
		ensureReplacement(control)
		control.InstanceID = control.Replacement.NewInstanceID
		control.Generation = control.Replacement.Generation
		control.Phase = state.StatefulMemberReady
		control.Lease = nil
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := r.updateGroupSummary(ctx, req, doc.Control); err != nil {
		return nil, err
	}
	if err := r.appendAudit(ctx, req, "completed stateful member replacement"); err != nil {
		return nil, err
	}
	if err := r.appendProgressEvent(ctx, req, "stateful.replace_member.completed", "completed stateful member replacement", nil); err != nil {
		return nil, err
	}
	return &ReplaceMemberResult{
		Group:              doc.Control.Group,
		Env:                doc.Control.Env,
		Member:             doc.Control.Member,
		Generation:         doc.Control.Generation,
		OldInstanceID:      oldInstance,
		NewInstanceID:      doc.Control.InstanceID,
		VolumeID:           doc.Control.VolumeID,
		DNSName:            doc.Control.DNSName,
		ProviderOperations: append([]schema.ProviderOperationRef(nil), doc.Control.ProviderOperations...),
		Phase:              doc.Control.Phase,
	}, nil
}

func (r ReplacementRunner) appendAudit(ctx context.Context, req ReplaceMemberRequest, summary string) error {
	if r.Audit == nil {
		return nil
	}
	record := events.NewAuditRecord(req.Actor, schema.Target{Kind: "stateful-member", Name: fmt.Sprintf("%s/%d", req.Group, req.Member)}, "stateful.replace_member", summary, req.TraceID, time.Now().UTC(), req.OperationID+summary)
	record.Risk = schema.RiskHigh
	body, err := json.Marshal(map[string]any{"group": req.Group, "member": req.Member, "operation_id": req.OperationID, "saga_id": req.SagaID, "reason": req.Reason})
	if err != nil {
		return err
	}
	record.Data = body
	_, err = r.Audit.AppendAudit(ctx, record)
	return err
}

func (r ReplacementRunner) appendProgressEvent(ctx context.Context, req ReplaceMemberRequest, eventType, summary string, op *schema.ProviderOperationRef) error {
	log := r.EventLog
	if log == nil {
		log = r.Audit
	}
	if log == nil {
		return nil
	}
	body := map[string]any{
		"group":        req.Group,
		"member":       req.Member,
		"operation_id": req.OperationID,
		"saga_id":      req.SagaID,
	}
	if op != nil && op.ID != "" {
		body["provider_operation"] = op
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	facts := []schema.Fact{
		{Type: "stateful_member", Message: fmt.Sprintf("%s/%d", req.Group, req.Member)},
		{Type: "operation", Message: req.OperationID},
	}
	if op != nil && op.ID != "" {
		facts = append(facts, schema.Fact{Type: "provider_operation", Message: op.Kind + ":" + op.ID})
	}
	operationEvent := events.NewOperationEvent(req.Group, req.OperationID, eventType, summary, now, req.OperationID+eventType)
	operationEvent.TraceID = req.TraceID
	operationEvent.Actor = &req.Actor
	operationEvent.Facts = facts
	operationEvent.Data = data
	if _, err := log.Append(ctx, operationEvent); err != nil {
		return err
	}
	if req.SagaID == "" {
		return nil
	}
	sagaEvent := events.NewSagaEvent(req.SagaID, eventType, summary, now, req.SagaID+eventType)
	sagaEvent.TraceID = req.TraceID
	sagaEvent.Actor = &req.Actor
	sagaEvent.Facts = facts
	sagaEvent.Data = data
	_, err = log.Append(ctx, sagaEvent)
	return err
}

func (r ReplacementRunner) updateGroupSummary(ctx context.Context, req ReplaceMemberRequest, member schema.StatefulMemberControl) error {
	current, err := r.Store.GetStatefulGroupControl(ctx, req.Group)
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) {
			return nil
		}
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
	if next.Operation != nil && next.Operation.ID == req.OperationID {
		next.Operation = &schema.ActiveOperation{ID: req.OperationID, Kind: "stateful.replace_member", State: string(schema.OperationSucceeded), Step: "complete"}
	}
	next.UpdatedBy = req.Actor
	next.TraceID = req.TraceID
	_, err = r.Store.UpdateStatefulGroupControlCAS(ctx, current, next)
	return err
}

func (r ReplacementRunner) recordProviderStep(ctx context.Context, handle state.StatefulMemberLeaseHandle, op schema.ProviderOperationRef, mutate func(*schema.StatefulMemberControl)) (*state.StatefulMemberLeaseHandle, *state.StatefulMemberDocument, error) {
	return r.Store.UpdateStatefulMemberWithLeaseCAS(ctx, handle, func(control *schema.StatefulMemberControl) error {
		if op.ID != "" {
			control.ProviderOperations = append(control.ProviderOperations, schema.ProviderOperationRef(op))
		}
		mutate(control)
		return nil
	})
}

func recipeRequest(req ReplaceMemberRequest, control schema.StatefulMemberControl) RecipeRequest {
	return RecipeRequest{Group: control.Group, Env: control.Env, Member: control.Member, Generation: control.Generation, InstanceID: control.InstanceID, VolumeID: control.VolumeID, DNSName: control.DNSName, Control: control, OperationID: req.OperationID, TraceID: req.TraceID}
}

func firstTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
