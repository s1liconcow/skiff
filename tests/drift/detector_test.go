package drift_test

import (
	"context"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/drift"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestDetectClassifiesMissingChangedOrphanedAndUnsafe(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	createRecord(t, store, "resources/by-provider/aws/asg/asg-123.json", schema.ResourceRecord{
		SchemaVersion: schema.Version,
		Logical:       schema.ResourceLogicalRef{Kind: "asg", Name: "asg:payments-api"},
		Provider:      schema.ResourceProviderRef{Provider: "aws", Kind: "asg", ID: "asg-123"},
		Service:       "payments-api",
		Env:           "prod",
		Tags:          map[string]string{ir.TagService: "payments-api", ir.TagEnv: "prod"},
		ObservedAt:    "2026-05-16T20:00:00Z",
	})
	createRecord(t, store, "resources/by-provider/aws/target-group/tg-123.json", schema.ResourceRecord{
		SchemaVersion: schema.Version,
		Logical:       schema.ResourceLogicalRef{Kind: "target-group", Name: "tg:payments-api"},
		Provider:      schema.ResourceProviderRef{Provider: "aws", Kind: "target-group", ID: "tg-123"},
		Service:       "payments-api",
		Env:           "prod",
		Tags:          map[string]string{ir.TagService: "payments-api"},
		ObservedAt:    "2026-05-16T20:00:00Z",
	})
	createRecord(t, store, "resources/by-provider/aws/route53-record/dns-123.json", schema.ResourceRecord{
		SchemaVersion: schema.Version,
		Logical:       schema.ResourceLogicalRef{Kind: "route53-record", Name: "stateful-dns:payments-api:0"},
		Provider:      schema.ResourceProviderRef{Provider: "aws", Kind: "route53-record", ID: "dns-123"},
		Service:       "payments-api",
		Env:           "prod",
		Tags:          map[string]string{ir.TagService: "payments-api", ir.TagEnv: "prod", ir.TagStatefulGroup: "payments-api"},
		ObservedAt:    "2026-05-16T20:00:00Z",
	})
	cloud := fakeProvider{inspection: provider.ServiceInspection{
		Ref:      provider.ServiceRef{Service: "payments-api", Env: "prod"},
		Provider: "aws",
		FreshAt:  time.Date(2026, 5, 17, 3, 0, 0, 0, time.UTC),
		Resources: []provider.ResourceInspection{
			{Kind: "asg", LogicalID: "asg:payments-api", ProviderID: "asg-123", Tags: map[string]string{ir.TagService: "orders-api", ir.TagEnv: "prod"}},
			{Kind: "route53-record", LogicalID: "stateful-dns:payments-api:0", ProviderID: "dns-123", Status: "changed outside Skiff", Tags: map[string]string{ir.TagService: "payments-api", ir.TagEnv: "prod", ir.TagStatefulGroup: "payments-api"}},
			{Kind: "launch-template", LogicalID: "lt:orphan", ProviderID: "lt-orphan", Tags: map[string]string{ir.TagService: "payments-api"}},
			{Kind: "rds-db-instance", LogicalID: "db:orphan", ProviderID: "db-orphan", Tags: map[string]string{ir.TagService: "payments-api"}},
			{Kind: "ec2-instance", LogicalID: "stateful-member:payments-api:0", ProviderID: "i-orphan", Tags: map[string]string{ir.TagService: "payments-api", ir.TagStatefulGroup: "payments-api"}},
		},
	}}

	result, err := drift.Detector{Store: store, Provider: cloud}.Detect(ctx, drift.Request{Service: "payments-api", Env: "prod"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	got := map[string]bool{}
	for _, finding := range result.Findings {
		got[finding.Code] = true
	}
	for _, code := range []string{"RESOURCE_TAG_DRIFT", "RESOURCE_MISSING", "RESOURCE_ORPHANED", "STATEFUL_ORPHAN_PROTECTED"} {
		if !got[code] {
			t.Fatalf("missing %s in findings: %+v", code, result.Findings)
		}
	}
	if !got["STATEFUL_RESOURCE_DRIFT"] {
		t.Fatalf("missing stateful drift finding: %+v", result.Findings)
	}
	if !hasProvider(result.Findings, "dns-123") {
		t.Fatalf("stateful drift did not include provider ID: %+v", result.Findings)
	}
}

func hasProvider(findings []drift.Finding, providerID string) bool {
	for _, finding := range findings {
		if finding.ProviderID == providerID {
			return true
		}
	}
	return false
}

func createRecord(t *testing.T, store objstore.ObjectStore, key string, value any) {
	t.Helper()
	body, err := canonical.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), key, body, objstore.PutOptions{ContentType: canonical.ContentType}); err != nil {
		t.Fatal(err)
	}
}

type fakeProvider struct {
	inspection provider.ServiceInspection
}

func (p fakeProvider) Name() string { return "aws" }
func (p fakeProvider) Plan(context.Context, *ir.Graph) (*provider.Plan, error) {
	return nil, provider.Unsupported("aws", "plan")
}
func (p fakeProvider) Apply(context.Context, *provider.Plan) (*provider.ApplyResult, error) {
	return nil, provider.Unsupported("aws", "apply")
}
func (p fakeProvider) InspectService(context.Context, provider.ServiceRef) (*provider.ServiceInspection, error) {
	return &p.inspection, nil
}
func (p fakeProvider) InspectResource(context.Context, provider.ResourceRef) (*provider.ResourceInspection, error) {
	return nil, provider.Unsupported("aws", "inspect-resource")
}
func (p fakeProvider) Logs(context.Context, provider.LogsRequest) (*provider.LogsResult, error) {
	return nil, provider.Unsupported("aws", "logs")
}
func (p fakeProvider) Metrics(context.Context, provider.MetricsRequest) (*provider.MetricsResult, error) {
	return nil, provider.Unsupported("aws", "metrics")
}
func (p fakeProvider) Debug(context.Context, provider.DebugRequest) (*provider.DebugSession, error) {
	return nil, provider.Unsupported("aws", "debug")
}
func (p fakeProvider) StartRollout(context.Context, provider.RolloutRequest) (*provider.Rollout, error) {
	return nil, provider.Unsupported("aws", "start-rollout")
}
func (p fakeProvider) WatchRollout(context.Context, provider.WatchRolloutRequest) (*provider.RolloutStatus, error) {
	return nil, provider.Unsupported("aws", "watch-rollout")
}
func (p fakeProvider) Rollback(context.Context, provider.RollbackRequest) (*provider.Rollout, error) {
	return nil, provider.Unsupported("aws", "rollback")
}
