package stateful

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/events"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestReplaceMemberFencesBeforeReattachingVolume(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	client := state.NewClient(store, state.WithClock(newStatefulClock(time.Date(2026, 5, 17, 7, 0, 0, 0, time.UTC))), state.WithTokenGenerator(func() string { return "lease-token" }))
	seedMember(t, client)
	fake := &fakeStatefulProvider{now: time.Date(2026, 5, 17, 7, 1, 0, 0, time.UTC), replacementID: "i-new"}
	auditLog, err := events.NewLog(events.Options{Store: store})
	if err != nil {
		t.Fatalf("new audit log: %v", err)
	}

	result, err := (ReplacementRunner{Store: client, Provider: fake, Audit: auditLog, Owner: "test", LeaseTTL: time.Minute}).Replace(ctx, ReplaceMemberRequest{
		Group:       "postgres",
		Env:         "prod",
		Member:      0,
		OperationID: "op_replace",
		TraceID:     "tr_replace",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
	})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if !reflect.DeepEqual(fake.calls, []string{"fence", "detach", "launch", "attach", "dns"}) {
		t.Fatalf("provider call order = %+v", fake.calls)
	}
	if result.OldInstanceID != "i-old" || result.NewInstanceID != "i-new" || result.Generation != 2 || result.Phase != state.StatefulMemberReady {
		t.Fatalf("unexpected result: %+v", result)
	}
	doc, err := client.GetStatefulMemberControl(ctx, "postgres", 0)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if doc.Control.Lease != nil || doc.Control.InstanceID != "i-new" || doc.Control.Generation != 2 {
		t.Fatalf("member control not finalized safely: %+v", doc.Control)
	}
	if len(doc.Control.ProviderOperations) != 5 {
		t.Fatalf("provider operations not recorded: %+v", doc.Control.ProviderOperations)
	}
	metas, err := store.List(ctx, "audit/", objstore.ListOptions{})
	if err != nil {
		t.Fatalf("list audit records: %v", err)
	}
	if len(metas) != 7 {
		t.Fatalf("audit record count = %d, want 7", len(metas))
	}
	for _, meta := range metas {
		object, err := store.Get(ctx, meta.Key)
		if err != nil {
			t.Fatalf("get audit record: %v", err)
		}
		var record events.AuditRecord
		if err := canonical.UnmarshalStrict(object.Body, &record); err != nil {
			t.Fatalf("decode audit record: %v", err)
		}
		if record.Action != "stateful.replace_member" || record.Target.Kind != "stateful-member" || record.Target.Name != "postgres/0" || record.Risk != schema.RiskHigh || record.TraceID != "tr_replace" {
			t.Fatalf("unexpected audit record: %+v", record)
		}
	}
	eventMetas, err := store.List(ctx, "services/postgres/operations/op_replace/events/", objstore.ListOptions{})
	if err != nil {
		t.Fatalf("list replacement events: %v", err)
	}
	if len(eventMetas) != 7 {
		t.Fatalf("replacement event count = %d, want 7", len(eventMetas))
	}
}

func TestReplaceMemberPersistsProgressBeforeAttachFailure(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	client := state.NewClient(store, state.WithClock(newStatefulClock(time.Date(2026, 5, 17, 7, 0, 0, 0, time.UTC))), state.WithTokenGenerator(func() string { return "lease-token" }))
	seedMember(t, client)
	fake := &fakeStatefulProvider{now: time.Date(2026, 5, 17, 7, 1, 0, 0, time.UTC), replacementID: "i-new", failAttach: true}

	_, err := (ReplacementRunner{Store: client, Provider: fake, Owner: "test", LeaseTTL: time.Minute}).Replace(ctx, ReplaceMemberRequest{
		Group:       "postgres",
		Env:         "prod",
		Member:      0,
		OperationID: "op_replace",
		TraceID:     "tr_replace",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
	})
	if err == nil {
		t.Fatal("expected attach failure")
	}
	doc, err := client.GetStatefulMemberControl(ctx, "postgres", 0)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if doc.Control.Replacement == nil || doc.Control.Replacement.FencedAt == "" || doc.Control.Replacement.DetachedAt == "" || doc.Control.Replacement.NewInstanceID != "i-new" {
		t.Fatalf("replacement progress was not durable before attach failure: %+v", doc.Control.Replacement)
	}
	if doc.Control.Phase != state.StatefulMemberReplacing || doc.Control.InstanceID != "i-old" {
		t.Fatalf("failed replacement should not publish new writer: %+v", doc.Control)
	}
}

func TestReplaceMemberResumesAfterReplacementLaunched(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	client := state.NewClient(store, state.WithClock(newStatefulClock(time.Date(2026, 5, 17, 7, 0, 0, 0, time.UTC))), state.WithTokenGenerator(func() string { return "lease-token" }))
	seedMemberWithReplacement(t, client)
	fake := &fakeStatefulProvider{now: time.Date(2026, 5, 17, 7, 1, 0, 0, time.UTC), replacementID: "i-new"}

	result, err := (ReplacementRunner{Store: client, Provider: fake, Owner: "test", LeaseTTL: time.Minute}).Replace(ctx, ReplaceMemberRequest{
		Group:       "postgres",
		Env:         "prod",
		Member:      0,
		OperationID: "op_replace",
		TraceID:     "tr_replace",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
	})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if !reflect.DeepEqual(fake.calls, []string{"attach", "dns"}) {
		t.Fatalf("resume should skip completed provider steps, got %+v", fake.calls)
	}
	if result.NewInstanceID != "i-new" || result.Generation != 2 {
		t.Fatalf("unexpected resume result: %+v", result)
	}
}

func seedMember(t *testing.T, client *state.Client) {
	t.Helper()
	_, err := client.CreateStatefulMemberControl(context.Background(), schema.StatefulMemberControl{
		Group:      "postgres",
		Env:        "prod",
		Member:     0,
		Zone:       "us-west-2a",
		InstanceID: "i-old",
		VolumeID:   "vol-123",
		DNSName:    "postgres-0.internal.example.com",
		Generation: 1,
		Phase:      state.StatefulMemberReady,
		UpdatedAt:  canonical.Time(time.Date(2026, 5, 17, 6, 0, 0, 0, time.UTC)),
		UpdatedBy:  schema.Actor{ID: "seed", Type: "test"},
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
}

func seedMemberWithReplacement(t *testing.T, client *state.Client) {
	t.Helper()
	_, err := client.CreateStatefulMemberControl(context.Background(), schema.StatefulMemberControl{
		Group:      "postgres",
		Env:        "prod",
		Member:     0,
		Zone:       "us-west-2a",
		InstanceID: "i-old",
		VolumeID:   "vol-123",
		DNSName:    "postgres-0.internal.example.com",
		Generation: 1,
		Phase:      state.StatefulMemberReplacing,
		Replacement: &schema.StatefulReplacement{
			OperationID:           "op_replace",
			OldInstanceID:         "i-old",
			NewInstanceID:         "i-new",
			VolumeID:              "vol-123",
			Generation:            2,
			FencedAt:              "2026-05-17T07:00:00Z",
			DetachedAt:            "2026-05-17T07:00:01Z",
			ReplacementLaunchedAt: "2026-05-17T07:00:02Z",
		},
		UpdatedAt: canonical.Time(time.Date(2026, 5, 17, 6, 0, 0, 0, time.UTC)),
		UpdatedBy: schema.Actor{ID: "seed", Type: "test"},
	})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
}

type fakeStatefulProvider struct {
	now           time.Time
	replacementID string
	failAttach    bool
	calls         []string
}

func (p *fakeStatefulProvider) FenceInstance(ctx context.Context, req provider.FenceInstanceRequest) (*provider.FenceInstanceResult, error) {
	p.calls = append(p.calls, "fence")
	return &provider.FenceInstanceResult{ProviderOperation: p.op("fence", "fence-"+req.InstanceID), FencedAt: p.now}, ctx.Err()
}

func (p *fakeStatefulProvider) DetachVolume(ctx context.Context, req provider.DetachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	p.calls = append(p.calls, "detach")
	return &provider.VolumeAttachmentResult{ProviderOperation: p.op("detach-volume", "detach-"+req.VolumeID), VolumeID: req.VolumeID, InstanceID: req.InstanceID, CompletedAt: p.now}, ctx.Err()
}

func (p *fakeStatefulProvider) LaunchReplacement(ctx context.Context, req provider.LaunchReplacementRequest) (*provider.ReplacementInstance, error) {
	p.calls = append(p.calls, "launch")
	return &provider.ReplacementInstance{ProviderOperation: p.op("launch-replacement", "launch-"+p.replacementID), InstanceID: p.replacementID, Zone: req.Zone, LaunchedAt: p.now}, ctx.Err()
}

func (p *fakeStatefulProvider) AttachVolume(ctx context.Context, req provider.AttachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	p.calls = append(p.calls, "attach")
	if p.failAttach {
		return nil, errors.New("attach failed")
	}
	return &provider.VolumeAttachmentResult{ProviderOperation: p.op("attach-volume", "attach-"+req.VolumeID), VolumeID: req.VolumeID, InstanceID: req.InstanceID, CompletedAt: p.now}, ctx.Err()
}

func (p *fakeStatefulProvider) UpdateMemberDNS(ctx context.Context, req provider.UpdateMemberDNSRequest) (*provider.DNSUpdateResult, error) {
	p.calls = append(p.calls, "dns")
	return &provider.DNSUpdateResult{ProviderOperation: p.op("update-dns", "dns-"+req.InstanceID), DNSName: req.DNSName, UpdatedAt: p.now}, ctx.Err()
}

func (p *fakeStatefulProvider) SnapshotVolume(ctx context.Context, req provider.SnapshotVolumeRequest) (*provider.VolumeSnapshot, error) {
	p.calls = append(p.calls, "snapshot")
	return &provider.VolumeSnapshot{ProviderOperation: p.op("snapshot-volume", "snap-"+req.VolumeID), SnapshotID: "snap-123", VolumeID: req.VolumeID, CreatedAt: p.now}, ctx.Err()
}

func (p *fakeStatefulProvider) op(kind, id string) schema.ProviderOperationRef {
	return schema.ProviderOperationRef{Provider: "fake", Kind: kind, ID: id, ObservedAt: canonical.Time(p.now)}
}

type statefulClock struct {
	now time.Time
}

func newStatefulClock(now time.Time) statefulClock {
	return statefulClock{now: now}
}

func (c statefulClock) Now() time.Time {
	return c.now
}
