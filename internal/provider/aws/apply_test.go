package aws_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/s1liconcow/skiff/internal/compiler"
	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/objstore/memory"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	"github.com/s1liconcow/skiff/internal/spec"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

func TestPlanUsesResourceManagerDiffActions(t *testing.T) {
	manager := &fakeServiceResourceManager{
		plans: map[string]aws.ResourcePlan{
			aws.ResourceKindLaunchTemplate: {Action: provider.ActionUpdate, ProviderID: "lt-123", Summary: "update launch template user-data"},
			aws.ResourceKindTargetGroup:    {Action: provider.ActionNoop, ProviderID: "tg-123", Summary: "target group is current"},
		},
	}
	p := newApplyTestProvider(t, manager, nil)
	plan, err := p.Plan(context.Background(), compileApplyTestGraph(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	actions := map[string]string{}
	for _, resource := range plan.Resources {
		actions[resource.Kind] = resource.Action
		if len(resource.Desired) == 0 || resource.Fingerprint == "" {
			t.Fatalf("resource missing desired payload or fingerprint: %+v", resource)
		}
	}
	if actions[aws.ResourceKindLaunchTemplate] != provider.ActionUpdate {
		t.Fatalf("launch template action = %q, want update", actions[aws.ResourceKindLaunchTemplate])
	}
	if actions[aws.ResourceKindTargetGroup] != provider.ActionNoop {
		t.Fatalf("target group action = %q, want no-op", actions[aws.ResourceKindTargetGroup])
	}
	if actions[aws.ResourceKindAutoScalingGroup] != provider.ActionCreate {
		t.Fatalf("asg action = %q, want create", actions[aws.ResourceKindAutoScalingGroup])
	}
}

func TestApplyCreatesUpdatesAndWritesResourceRecords(t *testing.T) {
	store := memory.New()
	manager := &fakeServiceResourceManager{}
	p := newApplyTestProvider(t, manager, store)
	plan, err := p.Plan(context.Background(), compileApplyTestGraph(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	result, err := p.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(result.ResourceIDs) != len(plan.Resources) {
		t.Fatalf("applied resource IDs = %d, want %d", len(result.ResourceIDs), len(plan.Resources))
	}
	if manager.applyCalls != len(plan.Resources) {
		t.Fatalf("apply calls = %d, want %d", manager.applyCalls, len(plan.Resources))
	}
	if _, err := p.Apply(context.Background(), plan); err != nil {
		t.Fatalf("second apply should upsert resource records idempotently: %v", err)
	}

	asgName := "skiff-prod-payments-api-asg"
	logicalKey, err := paths.LogicalResource(aws.ResourceKindAutoScalingGroup, "autoscaling-group-payments-api")
	if err != nil {
		t.Fatal(err)
	}
	providerKey, err := paths.ProviderResource(aws.Name, aws.ResourceKindAutoScalingGroup, aws.ResourceKindAutoScalingGroup+"/"+asgName)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{logicalKey, providerKey} {
		obj, err := store.Get(context.Background(), key)
		if err != nil {
			t.Fatalf("resource record %s was not written: %v", key, err)
		}
		var record schema.ResourceRecord
		if err := canonical.UnmarshalStrict(obj.Body, &record); err != nil {
			t.Fatalf("resource record %s is invalid: %v", key, err)
		}
		if record.Provider.Provider != aws.Name || record.Provider.Kind != aws.ResourceKindAutoScalingGroup || record.Provider.ID != aws.ResourceKindAutoScalingGroup+"/"+asgName {
			t.Fatalf("unexpected provider ref in %s: %+v", key, record.Provider)
		}
		if record.Service != "payments-api" || record.Env != "prod" || record.Tags[ir.TagService] != "payments-api" {
			t.Fatalf("unexpected resource record identity: %+v", record)
		}
	}
}

func TestApplySkipsDeleteNotSupported(t *testing.T) {
	manager := &fakeServiceResourceManager{}
	p := newApplyTestProvider(t, manager, memory.New())
	result, err := p.Apply(context.Background(), &provider.Plan{
		Provider: aws.Name,
		Service:  "payments-api",
		Env:      "prod",
		Resources: []provider.PlannedChange{{
			Action:    provider.ActionDeleteNotSupported,
			Kind:      aws.ResourceKindAutoScalingGroup,
			LogicalID: "autoscaling-group:payments-api",
			Name:      "skiff-prod-payments-api-asg",
		}},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if manager.applyCalls != 0 || len(result.ResourceIDs) != 0 {
		t.Fatalf("delete-not-supported should not call apply: calls=%d result=%+v", manager.applyCalls, result)
	}
}

func TestPlanRetriesThrottledResourceManager(t *testing.T) {
	manager := &fakeServiceResourceManager{throttleOnce: true}
	p := newApplyTestProvider(t, manager, nil)
	if _, err := p.Plan(context.Background(), compileApplyTestGraph(t)); err != nil {
		t.Fatalf("plan should retry throttling error: %v", err)
	}
	if manager.planCalls < 2 {
		t.Fatalf("plan calls = %d, want retry", manager.planCalls)
	}
}

func newApplyTestProvider(t *testing.T, manager *fakeServiceResourceManager, store *memory.Store) *aws.Provider {
	t.Helper()
	opts := []aws.Option{aws.WithClients(aws.Clients{ServiceResources: manager})}
	if store != nil {
		opts = append(opts, aws.WithStateStore(store))
	}
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2", StateBucket: "s3://skiff-state-prod"}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func compileApplyTestGraph(t *testing.T) *ir.Graph {
	t.Helper()
	doc, result, err := spec.Parse([]byte(applyTestSpec), spec.DecodeOptions{})
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

type fakeServiceResourceManager struct {
	plans        map[string]aws.ResourcePlan
	throttleOnce bool
	planCalls    int
	applyCalls   int
}

func (m *fakeServiceResourceManager) PlanResource(ctx context.Context, desired aws.DesiredServiceResource) (*aws.ResourcePlan, error) {
	m.planCalls++
	if m.throttleOnce {
		m.throttleOnce = false
		return nil, errors.New("Throttling: rate exceeded")
	}
	if plan, ok := m.plans[desired.Kind]; ok {
		plan.Fingerprint = firstNonEmptyTest(plan.Fingerprint, desired.Fingerprint)
		return &plan, nil
	}
	return &aws.ResourcePlan{
		Action:      provider.ActionCreate,
		Summary:     "create " + desired.Summary,
		Fingerprint: desired.Fingerprint,
	}, nil
}

func (m *fakeServiceResourceManager) ApplyResource(ctx context.Context, desired aws.DesiredServiceResource) (*aws.AppliedResource, error) {
	m.applyCalls++
	return &aws.AppliedResource{
		Kind:        desired.Kind,
		LogicalID:   desired.LogicalID,
		Name:        desired.Name,
		ProviderID:  desired.Kind + "/" + desired.Name,
		ARN:         "arn:aws:skiff-test:::" + strings.ReplaceAll(desired.Kind+"/"+desired.Name, "/", ":"),
		Status:      "applied",
		Tags:        desired.Tags,
		Fingerprint: desired.Fingerprint,
	}, nil
}

func firstNonEmptyTest(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

const applyTestSpec = `
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: payments-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/payments-api@sha256:abc123
runtime:
  port: 8080
  health:
    path: /healthz
machine:
  size: small
scale:
  min: 2
  max: 4
network:
  ingress:
    type: public-http
    host: payments.example.com
    tls:
      enabled: true
      certRef: aws-acm://us-west-2/certificate/payments-api
`
