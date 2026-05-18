package aws_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
	"github.com/s1liconcow/skiff/internal/stateful"
)

func TestApplyStatefulResourcesWritesAWSResourceRecords(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	statefulResources := &fakeStatefulResourceManager{}
	p, err := aws.New(aws.Config{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"}, aws.WithStateStore(store), aws.WithClients(aws.Clients{
		ServiceResources:  &fakeServiceResourceManager{},
		StatefulResources: statefulResources,
		Route53:           &fakeRoute53{},
	}))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(ctx, compileStatefulLifecycleGraph(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	result, err := p.Apply(ctx, plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	wantKinds := map[string]bool{
		aws.ResourceKindEC2Instance:    false,
		aws.ResourceKindEBSVolume:      false,
		aws.ResourceKindEBSAttachment:  false,
		aws.ResourceKindRoute53Record:  false,
		aws.ResourceKindSnapshotPolicy: false,
		aws.ResourceKindFencingPolicy:  false,
	}
	for _, resource := range result.Resources {
		if _, ok := wantKinds[resource.Kind]; !ok {
			continue
		}
		wantKinds[resource.Kind] = true
		key, err := paths.ProviderResource(aws.Name, resource.Kind, resource.ProviderID)
		if err != nil {
			t.Fatalf("provider resource key for %+v: %v", resource, err)
		}
		if _, err := store.Get(ctx, key); err != nil {
			t.Fatalf("stateful resource record %s was not written: %v", key, err)
		}
	}
	for kind, found := range wantKinds {
		if !found {
			t.Fatalf("stateful apply did not surface %s: %+v", kind, result.Resources)
		}
	}
	if len(statefulResources.applied) == 0 {
		t.Fatalf("stateful resource manager was not called")
	}
}

func TestAWSProviderStatefulReplacementPersistsOperationIDsBeforeAttachFailure(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	client := state.NewClient(store, state.WithClock(fixedClock{now: time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC)}), state.WithTokenGenerator(func() string { return "lease-token" }))
	_, err := client.CreateStatefulMemberControl(ctx, schema.StatefulMemberControl{
		Group:      "orders-stream",
		Env:        "prod",
		Member:     0,
		Zone:       "us-west-2a",
		InstanceID: "i-old",
		VolumeID:   "vol-123",
		DNSName:    "orders-stream-0.internal.example.com",
		Generation: 1,
		Phase:      state.StatefulMemberReady,
		UpdatedBy:  schema.Actor{ID: "seed", Type: "test"},
	})
	if err != nil {
		t.Fatalf("seed member: %v", err)
	}
	ops := &fakeAWSStatefulOps{now: time.Date(2026, 5, 18, 9, 1, 0, 0, time.UTC), failAttach: true}
	p, err := aws.New(aws.Config{Region: "us-west-2"}, aws.WithClients(aws.Clients{StatefulOperations: ops}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = (stateful.ReplacementRunner{Store: client, Provider: p, Owner: "test", LeaseTTL: time.Minute}).Replace(ctx, stateful.ReplaceMemberRequest{
		Group:       "orders-stream",
		Env:         "prod",
		Member:      0,
		OperationID: "op_replace",
		TraceID:     "tr_replace",
		Actor:       schema.Actor{ID: "operator", Type: "user"},
	})
	if err == nil {
		t.Fatal("expected attach failure")
	}
	doc, err := client.GetStatefulMemberControl(ctx, "orders-stream", 0)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if doc.Control.Replacement == nil || doc.Control.Replacement.NewInstanceID != "i-new" || doc.Control.Replacement.AttachedAt != "" {
		t.Fatalf("replacement progress should stop before attach publish: %+v", doc.Control.Replacement)
	}
	gotKinds := make([]string, 0, len(doc.Control.ProviderOperations))
	for _, op := range doc.Control.ProviderOperations {
		if op.Provider != aws.Name || op.ID == "" {
			t.Fatalf("provider operation missing AWS identity: %+v", op)
		}
		gotKinds = append(gotKinds, op.Kind)
	}
	want := []string{aws.StatefulOperationFenceInstance, aws.StatefulOperationDetachVolume, aws.StatefulOperationLaunchReplacement}
	if strings.Join(gotKinds, ",") != strings.Join(want, ",") {
		t.Fatalf("provider operations persisted before failure = %+v, want %+v", gotKinds, want)
	}
}

type fakeStatefulResourceManager struct {
	applied []string
}

func (m *fakeStatefulResourceManager) ApplyStatefulResource(ctx context.Context, desired aws.DesiredServiceResource) (*aws.AppliedResource, error) {
	m.applied = append(m.applied, desired.Kind)
	return &aws.AppliedResource{
		Kind:        desired.Kind,
		LogicalID:   desired.LogicalID,
		Name:        desired.Name,
		ProviderID:  desired.Kind + "/" + strings.NewReplacer(":", "-", "/", "-").Replace(firstNonEmptyTest(desired.Name, desired.LogicalID)),
		Status:      "applied",
		Tags:        desired.Tags,
		Fingerprint: desired.Fingerprint,
	}, ctx.Err()
}

func (m *fakeStatefulResourceManager) FindStatefulResources(ctx context.Context, filters []aws.TagFilter) ([]aws.DiscoveredResource, error) {
	return nil, ctx.Err()
}

type fakeRoute53 struct{}

func (f *fakeRoute53) UpsertARecord(ctx context.Context, req aws.Route53RecordUpdate) (*aws.Route53RecordChange, error) {
	return &aws.Route53RecordChange{ChangeID: "route53-change/" + strings.TrimSuffix(req.DNSName, "."), DNSName: req.DNSName, HostedZoneID: req.HostedZoneRef, Status: "INSYNC", UpdatedAt: time.Date(2026, 5, 18, 9, 2, 0, 0, time.UTC)}, ctx.Err()
}

type fakeAWSStatefulOps struct {
	now        time.Time
	failAttach bool
}

func (f *fakeAWSStatefulOps) FenceInstance(ctx context.Context, req provider.FenceInstanceRequest) (*provider.FenceInstanceResult, error) {
	return &provider.FenceInstanceResult{ProviderOperation: f.op(aws.StatefulOperationFenceInstance, "terminate/"+req.InstanceID), FencedAt: f.now}, ctx.Err()
}

func (f *fakeAWSStatefulOps) DetachVolume(ctx context.Context, req provider.DetachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	return &provider.VolumeAttachmentResult{ProviderOperation: f.op(aws.StatefulOperationDetachVolume, "detach/"+req.VolumeID), VolumeID: req.VolumeID, InstanceID: req.InstanceID, CompletedAt: f.now}, ctx.Err()
}

func (f *fakeAWSStatefulOps) LaunchReplacement(ctx context.Context, req provider.LaunchReplacementRequest) (*provider.ReplacementInstance, error) {
	return &provider.ReplacementInstance{ProviderOperation: f.op(aws.StatefulOperationLaunchReplacement, "run-instances/i-new"), InstanceID: "i-new", Zone: req.Zone, LaunchedAt: f.now}, ctx.Err()
}

func (f *fakeAWSStatefulOps) AttachVolume(ctx context.Context, req provider.AttachVolumeRequest) (*provider.VolumeAttachmentResult, error) {
	if f.failAttach {
		return nil, errors.New("InvalidVolume.InUse: volume is still attached")
	}
	return &provider.VolumeAttachmentResult{ProviderOperation: f.op(aws.StatefulOperationAttachVolume, "attach/"+req.VolumeID), VolumeID: req.VolumeID, InstanceID: req.InstanceID, CompletedAt: f.now}, ctx.Err()
}

func (f *fakeAWSStatefulOps) UpdateMemberDNS(ctx context.Context, req provider.UpdateMemberDNSRequest) (*provider.DNSUpdateResult, error) {
	return &provider.DNSUpdateResult{ProviderOperation: f.op(aws.StatefulOperationUpdateDNS, "change/"+req.DNSName), DNSName: req.DNSName, UpdatedAt: f.now}, ctx.Err()
}

func (f *fakeAWSStatefulOps) SnapshotVolume(ctx context.Context, req provider.SnapshotVolumeRequest) (*provider.VolumeSnapshot, error) {
	return &provider.VolumeSnapshot{ProviderOperation: f.op(aws.StatefulOperationSnapshotVolume, "snapshot/"+req.VolumeID), SnapshotID: "snap-123", VolumeID: req.VolumeID, CreatedAt: f.now}, ctx.Err()
}

func (f *fakeAWSStatefulOps) op(kind, id string) schema.ProviderOperationRef {
	return schema.ProviderOperationRef{Provider: aws.Name, Kind: kind, ID: id, ObservedAt: canonical.Time(f.now), Description: kind}
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

func compileStatefulLifecycleGraph(t *testing.T) *ir.Graph {
	t.Helper()
	doc, result, err := spec.Parse([]byte(statefulLifecycleSpec), spec.DecodeOptions{})
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	if !result.OK {
		t.Fatalf("spec invalid: %+v", result.Diagnostics)
	}
	graph, err := compiler.Compile(context.Background(), *doc, compiler.Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return graph
}

const statefulLifecycleSpec = `
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: orders-stream
  env: prod
stateful:
  replicas: 1
  members:
    - ordinal: 0
      zone: us-west-2a
      dnsName: orders-stream-0.internal.example.com
  volume:
    size: 20Gi
    type: gp3
    mountPath: /var/lib/jetstream
    encrypted: true
  identity:
    dnsZoneRef: Z123456789
  recipe:
    name: nats-jetstream
    config:
      runtime:
        ports:
          client: 4222
          monitoring: 8222
        health:
          path: /healthz
          port: 8222
      snapshots:
        enabled: true
        interval: 6h
        retention: 168h
`
