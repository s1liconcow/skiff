package stateful

import (
	"context"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestBackupRunnerCreatesImmutableSnapshotRecord(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	client := state.NewClient(store, state.WithClock(newStatefulClock(time.Date(2026, 5, 18, 4, 0, 0, 0, time.UTC))))
	seedMember(t, client)
	log, err := events.NewLog(events.Options{Store: store})
	if err != nil {
		t.Fatalf("new log: %v", err)
	}
	fake := &fakeStatefulProvider{now: time.Date(2026, 5, 18, 4, 1, 0, 0, time.UTC)}

	result, err := (BackupRunner{Objects: store, State: client, Provider: fake, Audit: log, EventLog: log, Clock: func() time.Time {
		return time.Date(2026, 5, 18, 4, 0, 0, 0, time.UTC)
	}}).SnapshotMember(ctx, SnapshotMemberRequest{
		BackupID:    "backup_01JABC",
		Group:       "postgres",
		Env:         "prod",
		Member:      0,
		OperationID: "op_backup",
		SagaID:      "saga_backup",
		TraceID:     "tr_backup",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
		Retention:   "24h",
	})
	if err != nil {
		t.Fatalf("SnapshotMember: %v", err)
	}
	if result.SnapshotID != "snap-123" || result.ProviderOperation.ID != "snap-vol-123" || result.ExpiresAt == "" {
		t.Fatalf("unexpected snapshot result: %+v", result)
	}
	record, err := ReadBackupRecord(ctx, store, "postgres", "backup_01JABC")
	if err != nil {
		t.Fatalf("read backup record: %v", err)
	}
	if record.VolumeID != "vol-123" || record.Status != BackupStatusAvailable || record.ProviderOperation.ID != "snap-vol-123" {
		t.Fatalf("unexpected backup record: %+v", record)
	}
	if _, err := store.Get(ctx, "stateful/postgres/backups/backup_01JABC/intent.json"); err != nil {
		t.Fatalf("intent was not written before snapshot: %v", err)
	}
	events, err := store.List(ctx, "services/postgres/operations/op_backup/events/", objstore.ListOptions{})
	if err != nil {
		t.Fatalf("list operation events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("operation event count = %d, want 1", len(events))
	}
	audit, err := store.List(ctx, "audit/", objstore.ListOptions{})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(audit) != 2 {
		t.Fatalf("audit count = %d, want 2", len(audit))
	}
}

func TestBackupRunnerRestoreRecordsApprovedIntent(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	client := state.NewClient(store)
	seedMember(t, client)
	fake := &fakeStatefulProvider{now: time.Date(2026, 5, 18, 4, 1, 0, 0, time.UTC)}
	if _, err := (BackupRunner{Objects: store, State: client, Provider: fake}).SnapshotMember(ctx, SnapshotMemberRequest{
		BackupID:    "backup_01JABC",
		Group:       "postgres",
		Env:         "prod",
		Member:      0,
		OperationID: "op_backup",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
	}); err != nil {
		t.Fatalf("SnapshotMember: %v", err)
	}
	log, err := events.NewLog(events.Options{Store: store})
	if err != nil {
		t.Fatalf("new log: %v", err)
	}
	result, err := (BackupRunner{Objects: store, Audit: log, EventLog: log}).RestoreMember(ctx, RestoreMemberRequest{
		RestoreID:   "restore_01JABC",
		BackupID:    "backup_01JABC",
		Group:       "postgres",
		Env:         "prod",
		Member:      0,
		OperationID: "op_restore",
		SagaID:      "saga_restore",
		TraceID:     "tr_restore",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
		ApprovalID:  "approval-123",
	})
	if err != nil {
		t.Fatalf("RestoreMember: %v", err)
	}
	if result.Status != RestoreStatusPlanned || result.SnapshotID != "snap-123" {
		t.Fatalf("unexpected restore result: %+v", result)
	}
	obj, err := store.Get(ctx, "stateful/postgres/restores/restore_01JABC/intent.json")
	if err != nil {
		t.Fatalf("restore intent missing: %v", err)
	}
	var intent RestoreIntent
	if err := canonical.UnmarshalStrict(obj.Body, &intent); err != nil {
		t.Fatalf("decode restore intent: %v", err)
	}
	if intent.ApprovalID != "approval-123" || intent.ProviderID != "snap-123" {
		t.Fatalf("restore intent lost approval/provider data: %+v", intent)
	}
}

func TestBackupRunnerRestoreRejectsExpiredBackup(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	client := state.NewClient(store)
	seedMember(t, client)
	fake := &fakeStatefulProvider{now: time.Date(2026, 5, 18, 4, 1, 0, 0, time.UTC)}
	if _, err := (BackupRunner{Objects: store, State: client, Provider: fake, Clock: func() time.Time {
		return time.Date(2026, 5, 18, 4, 0, 0, 0, time.UTC)
	}}).SnapshotMember(ctx, SnapshotMemberRequest{
		BackupID:    "backup_expired",
		Group:       "postgres",
		Env:         "prod",
		Member:      0,
		OperationID: "op_backup",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
		Retention:   "1h",
	}); err != nil {
		t.Fatalf("SnapshotMember: %v", err)
	}
	_, err := (BackupRunner{Objects: store, Clock: func() time.Time {
		return time.Date(2026, 5, 18, 6, 0, 0, 0, time.UTC)
	}}).RestoreMember(ctx, RestoreMemberRequest{
		RestoreID:   "restore_expired",
		BackupID:    "backup_expired",
		Group:       "postgres",
		Env:         "prod",
		Member:      0,
		OperationID: "op_restore",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
	})
	if err == nil {
		t.Fatalf("RestoreMember succeeded with expired backup")
	}
}
