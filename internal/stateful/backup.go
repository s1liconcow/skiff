package stateful

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	BackupStatusAvailable = "available"
	RestoreStatusPlanned  = "planned"
	RestoreModeMember     = "member"
)

type BackupRunner struct {
	Objects  objstore.ObjectStore
	State    *state.Client
	Provider provider.StatefulOperations
	Recipe   Recipe
	Audit    *events.Log
	EventLog *events.Log
	Clock    func() time.Time
}

type SnapshotMemberRequest struct {
	BackupID    string       `json:"backup_id"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Member      int          `json:"member"`
	OperationID string       `json:"operation_id"`
	SagaID      string       `json:"saga_id,omitempty"`
	TraceID     string       `json:"trace_id,omitempty"`
	Actor       schema.Actor `json:"actor"`
	Reason      string       `json:"reason,omitempty"`
	Retention   string       `json:"retention,omitempty"`
}

type SnapshotMemberResult struct {
	BackupID           string                        `json:"backup_id"`
	Group              string                        `json:"group"`
	Env                string                        `json:"env,omitempty"`
	Member             int                           `json:"member"`
	VolumeID           string                        `json:"volume_id"`
	SnapshotID         string                        `json:"snapshot_id"`
	Provider           string                        `json:"provider,omitempty"`
	ProviderID         string                        `json:"provider_id,omitempty"`
	ProviderOperation  schema.ProviderOperationRef   `json:"provider_operation"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations,omitempty"`
	Status             string                        `json:"status"`
	CreatedAt          string                        `json:"created_at"`
	ExpiresAt          string                        `json:"expires_at,omitempty"`
}

type RestoreMemberRequest struct {
	RestoreID   string       `json:"restore_id"`
	BackupID    string       `json:"backup_id"`
	Group       string       `json:"group"`
	Env         string       `json:"env,omitempty"`
	Member      int          `json:"member"`
	Mode        string       `json:"mode,omitempty"`
	OperationID string       `json:"operation_id"`
	SagaID      string       `json:"saga_id,omitempty"`
	TraceID     string       `json:"trace_id,omitempty"`
	Actor       schema.Actor `json:"actor"`
	ApprovalID  string       `json:"approval_id,omitempty"`
	Reason      string       `json:"reason,omitempty"`
}

type RestoreMemberResult struct {
	RestoreID  string `json:"restore_id"`
	BackupID   string `json:"backup_id"`
	Group      string `json:"group"`
	Env        string `json:"env,omitempty"`
	Member     int    `json:"member"`
	Mode       string `json:"mode"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
}

type BackupIntent struct {
	SchemaVersion string       `json:"schema_version"`
	BackupID      string       `json:"backup_id"`
	Group         string       `json:"group"`
	Env           string       `json:"env,omitempty"`
	Member        int          `json:"member"`
	VolumeID      string       `json:"volume_id,omitempty"`
	OperationID   string       `json:"operation_id"`
	SagaID        string       `json:"saga_id,omitempty"`
	TraceID       string       `json:"trace_id,omitempty"`
	Actor         schema.Actor `json:"actor"`
	Reason        string       `json:"reason,omitempty"`
	Retention     string       `json:"retention,omitempty"`
	CreatedAt     string       `json:"created_at"`
}

type BackupRecord struct {
	SchemaVersion     string                      `json:"schema_version"`
	BackupID          string                      `json:"backup_id"`
	Group             string                      `json:"group"`
	Env               string                      `json:"env,omitempty"`
	Member            int                         `json:"member"`
	VolumeID          string                      `json:"volume_id"`
	SnapshotID        string                      `json:"snapshot_id"`
	Provider          string                      `json:"provider,omitempty"`
	ProviderID        string                      `json:"provider_id,omitempty"`
	ProviderOperation schema.ProviderOperationRef `json:"provider_operation"`
	RecipeBackup      *RecipeResult               `json:"recipe_backup,omitempty"`
	Status            string                      `json:"status"`
	CreatedAt         string                      `json:"created_at"`
	ExpiresAt         string                      `json:"expires_at,omitempty"`
}

type RestoreIntent struct {
	SchemaVersion string       `json:"schema_version"`
	RestoreID     string       `json:"restore_id"`
	BackupID      string       `json:"backup_id"`
	Group         string       `json:"group"`
	Env           string       `json:"env,omitempty"`
	Member        int          `json:"member"`
	Mode          string       `json:"mode"`
	SnapshotID    string       `json:"snapshot_id,omitempty"`
	ProviderID    string       `json:"provider_id,omitempty"`
	OperationID   string       `json:"operation_id"`
	SagaID        string       `json:"saga_id,omitempty"`
	TraceID       string       `json:"trace_id,omitempty"`
	Actor         schema.Actor `json:"actor"`
	ApprovalID    string       `json:"approval_id,omitempty"`
	Reason        string       `json:"reason,omitempty"`
	CreatedAt     string       `json:"created_at"`
}

type RestoreRecord struct {
	SchemaVersion string `json:"schema_version"`
	RestoreID     string `json:"restore_id"`
	BackupID      string `json:"backup_id"`
	Group         string `json:"group"`
	Env           string `json:"env,omitempty"`
	Member        int    `json:"member"`
	Mode          string `json:"mode"`
	SnapshotID    string `json:"snapshot_id,omitempty"`
	ProviderID    string `json:"provider_id,omitempty"`
	Status        string `json:"status"`
	CreatedAt     string `json:"created_at"`
}

func (r BackupRunner) SnapshotMember(ctx context.Context, req SnapshotMemberRequest) (*SnapshotMemberResult, error) {
	if r.Objects == nil {
		return nil, errors.New("stateful backup object store is required")
	}
	if r.Provider == nil {
		return nil, errors.New("stateful provider is required")
	}
	if req.BackupID == "" || req.Group == "" || req.Member < 0 || req.OperationID == "" {
		return nil, errors.New("backup id, group, member, and operation id are required")
	}
	client := r.State
	if client == nil {
		client = state.NewClient(r.Objects)
	}
	member, err := client.GetStatefulMemberControl(ctx, req.Group, req.Member)
	if err != nil {
		return nil, err
	}
	if member.Control.VolumeID == "" {
		return nil, errors.New("stateful member has no durable volume ID")
	}
	record, err := r.getBackupRecord(ctx, req.Group, req.BackupID)
	if err == nil {
		return snapshotResultFromRecord(record), nil
	}
	if err != nil && !errors.Is(err, objstore.ErrNotFound) {
		return nil, err
	}
	now := r.now()
	intent := BackupIntent{
		SchemaVersion: schema.Version,
		BackupID:      req.BackupID,
		Group:         req.Group,
		Env:           firstNonEmpty(req.Env, member.Control.Env),
		Member:        req.Member,
		VolumeID:      member.Control.VolumeID,
		OperationID:   req.OperationID,
		SagaID:        req.SagaID,
		TraceID:       req.TraceID,
		Actor:         req.Actor,
		Reason:        req.Reason,
		Retention:     req.Retention,
		CreatedAt:     canonical.Time(now),
	}
	if err := createCanonical(ctx, r.Objects, mustStatefulBackupIntentPath(req.Group, req.BackupID), intent); err != nil && !errors.Is(err, objstore.ErrAlreadyExists) {
		return nil, err
	}
	if err := r.appendAudit(ctx, req.Actor, schema.Target{Kind: "stateful-backup", Name: req.Group + "/" + req.BackupID}, "stateful.backup.snapshot", req.OperationID, req.SagaID, req.TraceID, schema.RiskMedium, "started stateful member snapshot"); err != nil {
		return nil, err
	}
	providerResult, err := r.Provider.SnapshotVolume(ctx, provider.SnapshotVolumeRequest{
		Ref:      provider.StatefulMemberRef{Group: req.Group, Env: intent.Env, Member: req.Member},
		VolumeID: member.Control.VolumeID,
		Reason:   firstNonEmpty(req.Reason, "stateful backup"),
	})
	if err != nil {
		return nil, err
	}
	if providerResult == nil {
		return nil, errors.New("stateful provider returned no snapshot result")
	}
	var recipeResult *RecipeResult
	if r.Recipe != nil {
		recipeResult, err = r.Recipe.Backup(ctx, RecipeRequest{
			Group:       member.Control.Group,
			Env:         member.Control.Env,
			Member:      member.Control.Member,
			Generation:  member.Control.Generation,
			InstanceID:  member.Control.InstanceID,
			VolumeID:    member.Control.VolumeID,
			DNSName:     member.Control.DNSName,
			Control:     member.Control,
			OperationID: req.OperationID,
			TraceID:     req.TraceID,
		})
		if err != nil {
			return nil, err
		}
	}
	expiresAt := ""
	if ttl, err := time.ParseDuration(req.Retention); err == nil && ttl > 0 {
		expiresAt = canonical.Time(now.Add(ttl))
	}
	record = BackupRecord{
		SchemaVersion:     schema.Version,
		BackupID:          req.BackupID,
		Group:             req.Group,
		Env:               intent.Env,
		Member:            req.Member,
		VolumeID:          member.Control.VolumeID,
		SnapshotID:        providerResult.SnapshotID,
		Provider:          providerResult.ProviderOperation.Provider,
		ProviderID:        providerResult.SnapshotID,
		ProviderOperation: providerResult.ProviderOperation,
		RecipeBackup:      recipeResult,
		Status:            BackupStatusAvailable,
		CreatedAt:         canonical.Time(firstTime(providerResult.CreatedAt)),
		ExpiresAt:         expiresAt,
	}
	if err := createCanonical(ctx, r.Objects, mustStatefulBackupRecordPath(req.Group, req.BackupID), record); err != nil {
		if errors.Is(err, objstore.ErrAlreadyExists) {
			record, err = r.getBackupRecord(ctx, req.Group, req.BackupID)
			if err != nil {
				return nil, err
			}
			return snapshotResultFromRecord(record), nil
		}
		return nil, err
	}
	if err := r.appendProgressEvent(ctx, req.Group, req.OperationID, req.SagaID, req.TraceID, req.Actor, "stateful.backup.snapshot_created", "created stateful member volume snapshot", record.ProviderOperation); err != nil {
		return nil, err
	}
	if err := r.appendAudit(ctx, req.Actor, schema.Target{Kind: "stateful-backup", Name: req.Group + "/" + req.BackupID}, "stateful.backup.snapshot", req.OperationID, req.SagaID, req.TraceID, schema.RiskMedium, "completed stateful member snapshot"); err != nil {
		return nil, err
	}
	return snapshotResultFromRecord(record), nil
}

func (r BackupRunner) RestoreMember(ctx context.Context, req RestoreMemberRequest) (*RestoreMemberResult, error) {
	if r.Objects == nil {
		return nil, errors.New("stateful restore object store is required")
	}
	if req.RestoreID == "" || req.BackupID == "" || req.Group == "" || req.Member < 0 || req.OperationID == "" {
		return nil, errors.New("restore id, backup id, group, member, and operation id are required")
	}
	mode := firstNonEmpty(req.Mode, RestoreModeMember)
	record, err := r.getBackupRecord(ctx, req.Group, req.BackupID)
	if err != nil {
		return nil, err
	}
	if record.Status != BackupStatusAvailable {
		return nil, fmt.Errorf("backup %s status is %s", req.BackupID, record.Status)
	}
	if stale, err := BackupRecordStale(record, r.now()); err != nil {
		return nil, err
	} else if stale {
		return nil, fmt.Errorf("backup %s expired at %s", req.BackupID, record.ExpiresAt)
	}
	existing, err := r.getRestoreRecord(ctx, req.Group, req.RestoreID)
	if err == nil {
		return restoreResultFromRecord(existing), nil
	}
	if err != nil && !errors.Is(err, objstore.ErrNotFound) {
		return nil, err
	}
	now := r.now()
	intent := RestoreIntent{
		SchemaVersion: schema.Version,
		RestoreID:     req.RestoreID,
		BackupID:      req.BackupID,
		Group:         req.Group,
		Env:           firstNonEmpty(req.Env, record.Env),
		Member:        req.Member,
		Mode:          mode,
		SnapshotID:    record.SnapshotID,
		ProviderID:    record.ProviderID,
		OperationID:   req.OperationID,
		SagaID:        req.SagaID,
		TraceID:       req.TraceID,
		Actor:         req.Actor,
		ApprovalID:    req.ApprovalID,
		Reason:        req.Reason,
		CreatedAt:     canonical.Time(now),
	}
	if err := createCanonical(ctx, r.Objects, mustStatefulRestoreIntentPath(req.Group, req.RestoreID), intent); err != nil && !errors.Is(err, objstore.ErrAlreadyExists) {
		return nil, err
	}
	if r.Recipe != nil {
		if _, err := r.Recipe.Restore(ctx, RecipeRequest{
			Group:       req.Group,
			Env:         intent.Env,
			Member:      req.Member,
			VolumeID:    record.VolumeID,
			OperationID: req.OperationID,
			TraceID:     req.TraceID,
		}); err != nil {
			return nil, err
		}
	}
	restore := RestoreRecord{
		SchemaVersion: schema.Version,
		RestoreID:     req.RestoreID,
		BackupID:      req.BackupID,
		Group:         req.Group,
		Env:           intent.Env,
		Member:        req.Member,
		Mode:          mode,
		SnapshotID:    record.SnapshotID,
		ProviderID:    record.ProviderID,
		Status:        RestoreStatusPlanned,
		CreatedAt:     canonical.Time(now),
	}
	if err := createCanonical(ctx, r.Objects, mustStatefulRestoreRecordPath(req.Group, req.RestoreID), restore); err != nil {
		if errors.Is(err, objstore.ErrAlreadyExists) {
			restore, err = r.getRestoreRecord(ctx, req.Group, req.RestoreID)
			if err != nil {
				return nil, err
			}
			return restoreResultFromRecord(restore), nil
		}
		return nil, err
	}
	if err := r.appendAudit(ctx, req.Actor, schema.Target{Kind: "stateful-restore", Name: req.Group + "/" + req.RestoreID}, "stateful.restore.apply", req.OperationID, req.SagaID, req.TraceID, schema.RiskHigh, "planned stateful member restore from backup"); err != nil {
		return nil, err
	}
	if err := r.appendProgressEvent(ctx, req.Group, req.OperationID, req.SagaID, req.TraceID, req.Actor, "stateful.restore.planned", "planned stateful member restore from backup", record.ProviderOperation); err != nil {
		return nil, err
	}
	return restoreResultFromRecord(restore), nil
}

func (r BackupRunner) getBackupRecord(ctx context.Context, group, backup string) (BackupRecord, error) {
	return ReadBackupRecord(ctx, r.Objects, group, backup)
}

func ReadBackupRecord(ctx context.Context, store objstore.ObjectStore, group, backup string) (BackupRecord, error) {
	key, err := paths.StatefulBackupRecord(group, backup)
	if err != nil {
		return BackupRecord{}, err
	}
	var record BackupRecord
	if err := getCanonical(ctx, store, key, &record); err != nil {
		return BackupRecord{}, err
	}
	return record, nil
}

func BackupRecordStale(record BackupRecord, now time.Time) (bool, error) {
	if record.ExpiresAt == "" {
		return false, nil
	}
	expires, err := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	if err != nil {
		return true, fmt.Errorf("backup %s has invalid expiry %q: %w", record.BackupID, record.ExpiresAt, err)
	}
	return now.UTC().After(expires), nil
}

func (r BackupRunner) getRestoreRecord(ctx context.Context, group, restore string) (RestoreRecord, error) {
	key, err := paths.StatefulRestoreRecord(group, restore)
	if err != nil {
		return RestoreRecord{}, err
	}
	var record RestoreRecord
	if err := getCanonical(ctx, r.Objects, key, &record); err != nil {
		return RestoreRecord{}, err
	}
	return record, nil
}

func (r BackupRunner) appendAudit(ctx context.Context, actor schema.Actor, target schema.Target, action, operationID, sagaID, traceID string, risk schema.Risk, summary string) error {
	if r.Audit == nil {
		return nil
	}
	record := events.NewAuditRecord(actor, target, action, summary, traceID, r.now(), operationID+action+summary)
	record.Risk = risk
	body, err := json.Marshal(map[string]any{"operation_id": operationID, "saga_id": sagaID})
	if err != nil {
		return err
	}
	record.Data = body
	_, err = r.Audit.AppendAudit(ctx, record)
	return err
}

func (r BackupRunner) appendProgressEvent(ctx context.Context, group, operationID, sagaID, traceID string, actor schema.Actor, eventType, summary string, op schema.ProviderOperationRef) error {
	log := r.EventLog
	if log == nil {
		log = r.Audit
	}
	if log == nil {
		return nil
	}
	now := r.now()
	data, err := json.Marshal(map[string]any{"group": group, "operation_id": operationID, "saga_id": sagaID, "provider_operation": op})
	if err != nil {
		return err
	}
	facts := []schema.Fact{{Type: "stateful_group", Message: group}}
	if op.ID != "" {
		facts = append(facts, schema.Fact{Type: "provider_operation", Message: op.Kind + ":" + op.ID})
	}
	event := events.NewOperationEvent(group, operationID, eventType, summary, now, operationID+eventType)
	event.TraceID = traceID
	event.Actor = &actor
	event.Facts = facts
	event.Data = data
	if _, err := log.Append(ctx, event); err != nil && !errors.Is(err, events.ErrDuplicate) {
		return err
	}
	if sagaID == "" {
		return nil
	}
	sagaEvent := events.NewSagaEvent(sagaID, eventType, summary, now, sagaID+eventType)
	sagaEvent.TraceID = traceID
	sagaEvent.Actor = &actor
	sagaEvent.Facts = facts
	sagaEvent.Data = data
	_, err = log.Append(ctx, sagaEvent)
	if errors.Is(err, events.ErrDuplicate) {
		return nil
	}
	return err
}

func (r BackupRunner) now() time.Time {
	if r.Clock != nil {
		return r.Clock().UTC()
	}
	return time.Now().UTC()
}

func snapshotResultFromRecord(record BackupRecord) *SnapshotMemberResult {
	return &SnapshotMemberResult{
		BackupID:           record.BackupID,
		Group:              record.Group,
		Env:                record.Env,
		Member:             record.Member,
		VolumeID:           record.VolumeID,
		SnapshotID:         record.SnapshotID,
		Provider:           record.Provider,
		ProviderID:         record.ProviderID,
		ProviderOperation:  record.ProviderOperation,
		ProviderOperations: []schema.ProviderOperationRef{record.ProviderOperation},
		Status:             record.Status,
		CreatedAt:          record.CreatedAt,
		ExpiresAt:          record.ExpiresAt,
	}
}

func restoreResultFromRecord(record RestoreRecord) *RestoreMemberResult {
	return &RestoreMemberResult{
		RestoreID:  record.RestoreID,
		BackupID:   record.BackupID,
		Group:      record.Group,
		Env:        record.Env,
		Member:     record.Member,
		Mode:       record.Mode,
		SnapshotID: record.SnapshotID,
		ProviderID: record.ProviderID,
		Status:     record.Status,
		CreatedAt:  record.CreatedAt,
	}
}

func createCanonical(ctx context.Context, store objstore.ObjectStore, key string, value any) error {
	body, err := canonical.Marshal(value)
	if err != nil {
		return err
	}
	_, err = store.Create(ctx, key, body, objstore.PutOptions{ContentType: canonical.ContentType})
	return err
}

func getCanonical(ctx context.Context, store objstore.ObjectStore, key string, out any) error {
	object, err := store.Get(ctx, key)
	if err != nil {
		return err
	}
	return canonical.UnmarshalStrict(object.Body, out)
}

func mustStatefulBackupIntentPath(group, backup string) string {
	key, err := paths.StatefulBackupIntent(group, backup)
	if err != nil {
		panic(err)
	}
	return key
}

func mustStatefulBackupRecordPath(group, backup string) string {
	key, err := paths.StatefulBackupRecord(group, backup)
	if err != nil {
		panic(err)
	}
	return key
}

func mustStatefulRestoreIntentPath(group, restore string) string {
	key, err := paths.StatefulRestoreIntent(group, restore)
	if err != nil {
		panic(err)
	}
	return key
}

func mustStatefulRestoreRecordPath(group, restore string) string {
	key, err := paths.StatefulRestoreRecord(group, restore)
	if err != nil {
		panic(err)
	}
	return key
}
