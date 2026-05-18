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
	KindReplaceMember         = "stateful.member.replace"
	KindBackupSnapshotMember  = "stateful.backup.snapshot_member"
	KindBackupVerify          = "stateful.backup.verify"
	KindRestoreVerifyBackup   = "stateful.restore.verify_backup"
	KindRestoreApply          = "stateful.restore.apply"
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

type ReplaceMemberParams struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Member      int          `json:"member"`
	Reason      string       `json:"reason,omitempty"`
	Actor       schema.Actor `json:"actor,omitempty"`
}

type BackupParams struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id"`
	BackupID    string       `json:"backup_id"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Members     []int        `json:"members,omitempty"`
	Member      int          `json:"member,omitempty"`
	Reason      string       `json:"reason,omitempty"`
	Retention   string       `json:"retention,omitempty"`
	Actor       schema.Actor `json:"actor,omitempty"`
}

type RestoreParams struct {
	SagaID      string       `json:"saga_id,omitempty"`
	OperationID string       `json:"operation_id"`
	RestoreID   string       `json:"restore_id"`
	BackupID    string       `json:"backup_id"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Member      int          `json:"member"`
	Mode        string       `json:"mode,omitempty"`
	Reason      string       `json:"reason,omitempty"`
	ApprovalID  string       `json:"approval_id,omitempty"`
	Actor       schema.Actor `json:"actor,omitempty"`
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

type ReplaceMember struct {
	Store    objstore.ObjectStore
	Provider provider.StatefulOperations
	Recipe   stateruntime.Recipe
	Clock    func() time.Time
	LeaseTTL time.Duration
}

type BackupSnapshotMember struct {
	Store    objstore.ObjectStore
	Provider provider.StatefulOperations
	Recipe   stateruntime.Recipe
	Clock    func() time.Time
}

type BackupVerify struct {
	Store objstore.ObjectStore
	Clock func() time.Time
}

type RestoreVerifyBackup struct {
	Store objstore.ObjectStore
	Clock func() time.Time
}

type RestoreApply struct {
	Store  objstore.ObjectStore
	Recipe stateruntime.Recipe
	Clock  func() time.Time
}

func New(store objstore.ObjectStore, recipe stateruntime.Recipe) []steps.Step {
	return NewWithProvider(store, nil, recipe)
}

func NewWithProvider(store objstore.ObjectStore, statefulProvider provider.StatefulOperations, recipe stateruntime.Recipe) []steps.Step {
	return []steps.Step{
		PlanOrderedUpdate{Store: store},
		OrderedMemberUpdate{Store: store, Recipe: recipe},
		CompleteOrderedUpdate{Store: store},
		ReplaceMember{Store: store, Provider: statefulProvider, Recipe: recipe},
		BackupSnapshotMember{Store: store, Provider: statefulProvider, Recipe: recipe},
		BackupVerify{Store: store},
		RestoreVerifyBackup{Store: store},
		RestoreApply{Store: store, Recipe: recipe},
	}
}

func (ReplaceMember) Kind() string { return KindReplaceMember }

func (s ReplaceMember) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeReplaceParams(params)
	return err
}

func (s ReplaceMember) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "replace one stateful member after explicit provider fencing", Risk: schema.RiskHigh, Reversibility: schema.Compensatable}, nil
}

func (s ReplaceMember) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeReplaceParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	params.SagaID = firstNonEmpty(params.SagaID, req.SagaID)
	actor := actorOrDefault(params.Actor, req.Intent.Actor)
	if s.Store == nil {
		return nil, errors.New("stateful replacement step requires object store")
	}
	if s.Provider == nil {
		return replacementFailed(params, "STATEFUL_PROVIDER_UNSUPPORTED", "stateful provider does not implement replacement lifecycle operations", nil), nil
	}
	log, err := events.NewLog(events.Options{Store: s.Store, Clock: s.Clock})
	if err != nil {
		return nil, err
	}
	result, err := (stateruntime.ReplacementRunner{
		Store:    state.NewClient(s.Store, state.WithClock(stateClock(s.Clock))),
		Provider: s.Provider,
		Recipe:   s.Recipe,
		Audit:    log,
		EventLog: log,
		Owner:    "saga:" + req.SagaID + ":stateful-replace-member",
		LeaseTTL: leaseTTL(s.LeaseTTL),
	}).Replace(ctx, stateruntime.ReplaceMemberRequest{
		Group:       params.Group,
		Env:         params.Env,
		Member:      params.Member,
		OperationID: params.OperationID,
		SagaID:      params.SagaID,
		TraceID:     req.TraceID,
		Actor:       actor,
		Reason:      params.Reason,
	})
	if err != nil {
		code, summary := classifyReplaceError(err)
		return replacementFailed(params, code, summary, err), nil
	}
	payload := replacementResultPayload(params, result, nil)
	return &steps.StepResult{
		Status:             steps.StatusSucceeded,
		Summary:            fmt.Sprintf("replaced stateful member %s/%d", result.Group, result.Member),
		Result:             rawJSON(payload),
		ProviderOperations: append([]schema.ProviderOperationRef(nil), result.ProviderOperations...),
	}, nil
}

func (s ReplaceMember) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s ReplaceMember) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, _ := decodeReplaceParams(req.Node.Params)
	return &steps.StepResult{
		Status:  steps.StatusSucceeded,
		Summary: "stateful member replacement compensation requires explicit follow-up",
		Result: rawJSON(map[string]any{
			"group":        params.Group,
			"member":       params.Member,
			"operation_id": params.OperationID,
			"summary":      "replacement fencing and volume attachment are not automatically reversed",
		}),
	}, nil
}

func (s ReplaceMember) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (BackupSnapshotMember) Kind() string { return KindBackupSnapshotMember }

func (s BackupSnapshotMember) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeBackupParams(params, true)
	return err
}

func (s BackupSnapshotMember) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "snapshot one stateful member volume and persist provider snapshot ID", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
}

func (s BackupSnapshotMember) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeBackupParams(req.Node.Params, true)
	if err != nil {
		return nil, err
	}
	params.SagaID = firstNonEmpty(params.SagaID, req.SagaID)
	if s.Store == nil {
		return nil, errors.New("stateful backup step requires object store")
	}
	if s.Provider == nil {
		return nil, errors.New("stateful backup step requires provider lifecycle operations")
	}
	log, err := events.NewLog(events.Options{Store: s.Store, Clock: s.Clock})
	if err != nil {
		return nil, err
	}
	result, err := (stateruntime.BackupRunner{
		Objects:  s.Store,
		State:    state.NewClient(s.Store, state.WithClock(stateClock(s.Clock))),
		Provider: s.Provider,
		Recipe:   s.Recipe,
		Audit:    log,
		EventLog: log,
		Clock:    clockFunc(s.Clock),
	}).SnapshotMember(ctx, stateruntime.SnapshotMemberRequest{
		BackupID:    memberBackupID(params.BackupID, params.Member, len(params.Members)),
		Group:       params.Group,
		Env:         params.Env,
		Member:      params.Member,
		OperationID: params.OperationID,
		SagaID:      params.SagaID,
		TraceID:     req.TraceID,
		Actor:       actorOrDefault(params.Actor, req.Intent.Actor),
		Reason:      params.Reason,
		Retention:   params.Retention,
	})
	if err != nil {
		return nil, err
	}
	return &steps.StepResult{
		Status:             steps.StatusSucceeded,
		Summary:            fmt.Sprintf("snapshotted stateful member %s/%d", params.Group, params.Member),
		Result:             rawJSON(result),
		ProviderOperations: append([]schema.ProviderOperationRef(nil), result.ProviderOperations...),
	}, nil
}

func (s BackupSnapshotMember) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s BackupSnapshotMember) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, _ := decodeBackupParams(req.Node.Params, true)
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "stateful backups are retained for recovery", Result: rawJSON(map[string]string{"backup_id": params.BackupID, "summary": "snapshot is not deleted by compensation"})}, nil
}

func (s BackupSnapshotMember) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (BackupVerify) Kind() string { return KindBackupVerify }

func (s BackupVerify) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeBackupParams(params, false)
	return err
}

func (s BackupVerify) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "verify all requested stateful member backup records exist", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s BackupVerify) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeBackupParams(req.Node.Params, false)
	if err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, errors.New("stateful backup verify requires object store")
	}
	records := make([]stateruntime.BackupRecord, 0, len(params.Members))
	for _, member := range params.Members {
		backupID := memberBackupID(params.BackupID, member, len(params.Members))
		record, err := stateruntime.ReadBackupRecord(ctx, s.Store, params.Group, backupID)
		if err != nil {
			return failed("STATEFUL_BACKUP_MISSING", fmt.Sprintf("backup record %s for member %d is missing: %v", backupID, member, err)), nil
		}
		if stale, err := stateruntime.BackupRecordStale(record, clockFunc(s.Clock)()); err != nil {
			return failed("STATEFUL_BACKUP_STALE", err.Error()), nil
		} else if stale {
			return failed("STATEFUL_BACKUP_STALE", fmt.Sprintf("backup record %s for member %d expired at %s", backupID, member, record.ExpiresAt)), nil
		}
		records = append(records, record)
	}
	return succeeded("verified stateful backup records", map[string]any{"group": params.Group, "backup_id": params.BackupID, "records": records})
}

func (s BackupVerify) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s BackupVerify) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "backup verification has no compensation"}, nil
}

func (s BackupVerify) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	params, err := decodeBackupParams(req.Node.Params, false)
	if err != nil {
		return nil, err
	}
	if s.Store == nil {
		return []steps.Finding{{Code: "STATEFUL_BACKUP_STORE_MISSING", Severity: "high", Summary: "object store is required to verify stateful backups"}}, nil
	}
	var findings []steps.Finding
	for _, member := range params.Members {
		backupID := memberBackupID(params.BackupID, member, len(params.Members))
		record, err := stateruntime.ReadBackupRecord(ctx, s.Store, params.Group, backupID)
		if err != nil {
			findings = append(findings, steps.Finding{Code: "STATEFUL_BACKUP_MISSING", Severity: "high", Summary: fmt.Sprintf("backup record %s for member %d is missing", backupID, member)})
			continue
		}
		if stale, err := stateruntime.BackupRecordStale(record, clockFunc(s.Clock)()); err != nil {
			findings = append(findings, steps.Finding{Code: "STATEFUL_BACKUP_STALE", Severity: "high", Summary: err.Error()})
		} else if stale {
			findings = append(findings, steps.Finding{Code: "STATEFUL_BACKUP_STALE", Severity: "high", Summary: fmt.Sprintf("backup record %s for member %d expired at %s", backupID, member, record.ExpiresAt)})
		}
	}
	return findings, nil
}

func (RestoreVerifyBackup) Kind() string { return KindRestoreVerifyBackup }

func (s RestoreVerifyBackup) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeRestoreParams(params)
	return err
}

func (s RestoreVerifyBackup) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "verify the requested stateful backup exists before restore approval", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s RestoreVerifyBackup) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeRestoreParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, errors.New("stateful restore verify requires object store")
	}
	record, err := stateruntime.ReadBackupRecord(ctx, s.Store, params.Group, params.BackupID)
	if err != nil {
		return failed("STATEFUL_BACKUP_MISSING", err.Error()), nil
	}
	if stale, err := stateruntime.BackupRecordStale(record, clockFunc(s.Clock)()); err != nil {
		return failed("STATEFUL_BACKUP_STALE", err.Error()), nil
	} else if stale {
		return failed("STATEFUL_BACKUP_STALE", fmt.Sprintf("backup %s expired at %s", params.BackupID, record.ExpiresAt)), nil
	}
	return succeeded("verified stateful restore backup", map[string]any{"group": params.Group, "member": params.Member, "backup_id": params.BackupID, "snapshot_id": record.SnapshotID, "provider_id": record.ProviderID})
}

func (s RestoreVerifyBackup) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s RestoreVerifyBackup) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "restore verification has no compensation"}, nil
}

func (s RestoreVerifyBackup) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	params, err := decodeRestoreParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if s.Store == nil {
		return []steps.Finding{{Code: "STATEFUL_BACKUP_STORE_MISSING", Severity: "high", Summary: "object store is required to verify stateful restore backups"}}, nil
	}
	record, err := stateruntime.ReadBackupRecord(ctx, s.Store, params.Group, params.BackupID)
	if err != nil {
		return []steps.Finding{{Code: "STATEFUL_BACKUP_MISSING", Severity: "high", Summary: fmt.Sprintf("backup record %s is missing", params.BackupID)}}, nil
	}
	if stale, err := stateruntime.BackupRecordStale(record, clockFunc(s.Clock)()); err != nil {
		return []steps.Finding{{Code: "STATEFUL_BACKUP_STALE", Severity: "high", Summary: err.Error()}}, nil
	} else if stale {
		return []steps.Finding{{Code: "STATEFUL_BACKUP_STALE", Severity: "high", Summary: fmt.Sprintf("backup %s expired at %s", params.BackupID, record.ExpiresAt)}}, nil
	}
	return nil, nil
}

func (RestoreApply) Kind() string { return KindRestoreApply }

func (s RestoreApply) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeRestoreParams(params)
	return err
}

func (s RestoreApply) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "record an approved stateful restore intent; recipe restore may prepare member state", Risk: schema.RiskHigh, Reversibility: schema.PartiallyReversible}, nil
}

func (s RestoreApply) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeRestoreParams(req.Node.Params)
	if err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, errors.New("stateful restore apply requires object store")
	}
	log, err := events.NewLog(events.Options{Store: s.Store, Clock: s.Clock})
	if err != nil {
		return nil, err
	}
	result, err := (stateruntime.BackupRunner{
		Objects:  s.Store,
		Recipe:   s.Recipe,
		Audit:    log,
		EventLog: log,
		Clock:    clockFunc(s.Clock),
	}).RestoreMember(ctx, stateruntime.RestoreMemberRequest{
		RestoreID:   params.RestoreID,
		BackupID:    params.BackupID,
		Group:       params.Group,
		Env:         params.Env,
		Member:      params.Member,
		Mode:        params.Mode,
		OperationID: params.OperationID,
		SagaID:      firstNonEmpty(params.SagaID, req.SagaID),
		TraceID:     req.TraceID,
		Actor:       actorOrDefault(params.Actor, req.Intent.Actor),
		ApprovalID:  params.ApprovalID,
		Reason:      params.Reason,
	})
	if err != nil {
		return nil, err
	}
	return succeeded("planned stateful restore", result)
}

func (s RestoreApply) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s RestoreApply) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, _ := decodeRestoreParams(req.Node.Params)
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "stateful restore compensation requires explicit follow-up", Result: rawJSON(map[string]string{"restore_id": params.RestoreID, "summary": "restore intent remains immutable; no volume is deleted automatically"})}, nil
}

func (s RestoreApply) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
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
	return &steps.StepPlan{Summary: "update one stateful member release in place; keep the same VM, volume, DNS identity, and member generation", Risk: schema.RiskMedium, Reversibility: schema.Compensatable}, nil
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
	return succeeded("updated stateful member release in place", orderedMemberResult(params, doc.Control, hooks, fmt.Sprintf("kept generation %d on the same member VM and durable volume", previous.Generation)))
}

func (s OrderedMemberUpdate) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s OrderedMemberUpdate) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	params, _ := decodeOrderedParams(req.Node.Params, true)
	return &steps.StepResult{Status: steps.StatusSucceeded, Summary: "stateful member release update compensation requires explicit follow-up", Result: rawJSON(map[string]any{"group": params.Group, "member": params.Member, "release_id": params.ReleaseID, "summary": "member generation was not changed; restore the previous release with another explicit release update if needed"})}, nil
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

func decodeReplaceParams(raw json.RawMessage) (ReplaceMemberParams, error) {
	var params ReplaceMemberParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, err
	}
	if params.Group == "" {
		return params, errors.New("stateful group is required")
	}
	if params.Member < 0 {
		return params, errors.New("member ordinal must be non-negative")
	}
	if params.OperationID == "" {
		return params, errors.New("operation ID is required")
	}
	return params, nil
}

func decodeBackupParams(raw json.RawMessage, requireMember bool) (BackupParams, error) {
	var params BackupParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, err
	}
	if params.Group == "" {
		return params, errors.New("stateful group is required")
	}
	if params.BackupID == "" {
		return params, errors.New("backup ID is required")
	}
	if params.OperationID == "" {
		return params, errors.New("operation ID is required")
	}
	if requireMember && params.Member < 0 {
		return params, errors.New("member ordinal must be non-negative")
	}
	if len(params.Members) == 0 && params.Member >= 0 {
		params.Members = []int{params.Member}
	}
	sort.Ints(params.Members)
	return params, nil
}

func decodeRestoreParams(raw json.RawMessage) (RestoreParams, error) {
	var params RestoreParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, err
	}
	if params.Group == "" {
		return params, errors.New("stateful group is required")
	}
	if params.BackupID == "" {
		return params, errors.New("backup ID is required")
	}
	if params.RestoreID == "" {
		return params, errors.New("restore ID is required")
	}
	if params.OperationID == "" {
		return params, errors.New("operation ID is required")
	}
	if params.Member < 0 {
		return params, errors.New("member ordinal must be non-negative")
	}
	if params.Mode == "" {
		params.Mode = stateruntime.RestoreModeMember
	}
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
		"replaces_vm":          false,
		"moves_volume":         false,
		"changes_generation":   false,
		"release_id":           control.ReleaseID,
		"release_manifest_key": control.ReleaseManifestKey,
		"runtime_manifest_key": control.RuntimeManifestKey,
		"phase":                control.Phase,
		"operation_id":         params.OperationID,
		"hooks":                hooks,
		"summary":              summary,
	}
}

func replacementResultPayload(params ReplaceMemberParams, result *stateruntime.ReplaceMemberResult, failure *schema.StepFailure) map[string]any {
	payload := map[string]any{
		"group":        params.Group,
		"env":          params.Env,
		"member":       params.Member,
		"operation_id": params.OperationID,
		"saga_id":      params.SagaID,
		"facts": []schema.Fact{
			{Type: "stateful_member", Message: fmt.Sprintf("%s/%d", params.Group, params.Member)},
			{Type: "operation", Message: params.OperationID},
			{Type: "vm", Message: "new VM is launched only after the old writer is fenced"},
			{Type: "volume", Message: "same durable volume is detached from the old VM and attached to the replacement"},
			{Type: "generation", Message: "member generation changes for replacement fencing"},
		},
		"recommended_actions": replacementRecommendedActions(params, failure),
	}
	if result != nil {
		payload["env"] = result.Env
		payload["generation"] = result.Generation
		payload["old_instance_id"] = result.OldInstanceID
		payload["new_instance_id"] = result.NewInstanceID
		payload["volume_id"] = result.VolumeID
		payload["dns_name"] = result.DNSName
		payload["provider_operations"] = append([]schema.ProviderOperationRef(nil), result.ProviderOperations...)
		payload["phase"] = result.Phase
	}
	if failure != nil {
		payload["failure"] = failure
		payload["hypotheses"] = []schema.Fact{{Type: "replacement_incomplete", Message: "member control records the last durable provider step; resume the saga after the underlying issue is corrected"}}
	}
	return payload
}

func replacementRecommendedActions(params ReplaceMemberParams, failure *schema.StepFailure) []map[string]any {
	sagaID := params.SagaID
	if sagaID == "" {
		sagaID = "<saga-id>"
	}
	actions := []map[string]any{
		{"id": "inspect_stateful_member", "command": fmt.Sprintf("skiff stateful inspect %s --format json", params.Group), "mutating": false},
		{"id": "inspect_saga", "command": fmt.Sprintf("skiff ops inspect %s --format json", sagaID), "mutating": false},
	}
	if failure != nil {
		actions = append(actions, map[string]any{
			"id":            "resume_replacement",
			"command":       fmt.Sprintf("skiff stateful resume %s --format json", sagaID),
			"mutating":      true,
			"risk":          schema.RiskHigh,
			"reversibility": schema.Compensatable,
			"safety":        "resumes from durable member replacement progress",
		})
	}
	return actions
}

func memberBackupID(backupID string, member int, count int) string {
	if count <= 1 {
		return backupID
	}
	return fmt.Sprintf("%s-m%d", backupID, member)
}

func replacementFailed(params ReplaceMemberParams, code, summary string, cause error) *steps.StepResult {
	failure := &schema.StepFailure{Code: code, Summary: summary, Cause: causeString(cause), Retriable: replacementFailureRetriable(code)}
	return &steps.StepResult{
		Status:  steps.StatusFailed,
		Summary: summary,
		Failure: failure,
		Result:  rawJSON(replacementResultPayload(params, nil, failure)),
	}
}

func classifyReplaceError(err error) (string, string) {
	switch {
	case errors.Is(err, state.ErrLeaseHeld):
		return string(state.CodeLeaseHeld), err.Error()
	case errors.Is(err, state.ErrLeaseLost):
		return string(state.CodeLeaseLost), err.Error()
	case errors.Is(err, state.ErrPreconditionFailed):
		return string(state.CodePreconditionFailed), err.Error()
	}
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		if providerErr.Code == provider.CodeUnsupported {
			return "STATEFUL_PROVIDER_UNSUPPORTED", err.Error()
		}
		return "STATEFUL_PROVIDER_ERROR", err.Error()
	}
	return "STATEFUL_REPLACE_FAILED", err.Error()
}

func replacementFailureRetriable(code string) bool {
	switch code {
	case string(state.CodeLeaseHeld), string(state.CodePreconditionFailed), "STATEFUL_PROVIDER_ERROR":
		return true
	default:
		return false
	}
}

func causeString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
