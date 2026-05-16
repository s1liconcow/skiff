package bootstrap

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestPlanAWSComplete(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:         "prod",
		Region:      "us-west-2",
		StateBucket: "skiff-state-prod",
		Now:         time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Provider != ProviderAWS || plan.Env != "prod" || plan.Region != "us-west-2" {
		t.Fatalf("unexpected plan identity: %#v", plan)
	}
	if plan.StateBucketURI != "s3://skiff-state-prod" {
		t.Fatalf("state bucket URI = %q", plan.StateBucketURI)
	}
	if plan.KMSAlias != "alias/skiff-prod-state" {
		t.Fatalf("kms alias = %q", plan.KMSAlias)
	}
	if plan.RootObjectKey != "envs/prod/root.json" {
		t.Fatalf("root object key = %q", plan.RootObjectKey)
	}
	if len(plan.Resources) != 7 {
		t.Fatalf("resource count = %d, want 7", len(plan.Resources))
	}
	wantPolicies := []string{"deployer", "runner", "skiffd"}
	if got := sortedPolicyNames(plan.IAMPolicies); !reflect.DeepEqual(got, wantPolicies) {
		t.Fatalf("policy names = %#v, want %#v", got, wantPolicies)
	}
	if len(plan.BucketPolicy.Statement) == 0 {
		t.Fatal("missing bucket policy statements")
	}
}

func TestApplyAWSIdempotentWithFakeClient(t *testing.T) {
	plan, err := PlanAWS(AWSOptions{
		Env:         "prod",
		Region:      "us-west-2",
		StateBucket: "skiff-state-prod",
		Now:         time.Date(2026, 5, 16, 21, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := newFakeAWSBootstrapClient()

	first, err := ApplyAWS(context.Background(), client, plan)
	if err != nil {
		t.Fatalf("first ApplyAWS() error = %v", err)
	}
	if len(first.Actions) != 7 {
		t.Fatalf("first action count = %d, want 7", len(first.Actions))
	}
	for _, action := range first.Actions {
		if action.Action != "created" && action.Action != "put" {
			t.Fatalf("first apply action = %#v, want created/put", action)
		}
	}

	second, err := ApplyAWS(context.Background(), client, plan)
	if err != nil {
		t.Fatalf("second ApplyAWS() error = %v", err)
	}
	for _, action := range second.Actions {
		if action.Action != "unchanged" {
			t.Fatalf("second apply action = %#v, want unchanged", action)
		}
	}
}

type fakeAWSBootstrapClient struct {
	seen map[string]bool
}

func newFakeAWSBootstrapClient() *fakeAWSBootstrapClient {
	return &fakeAWSBootstrapClient{seen: make(map[string]bool)}
}

func (f *fakeAWSBootstrapClient) EnsureStateBucket(ctx context.Context, spec StateBucketSpec) (ApplyAction, error) {
	return f.ensure("s3-bucket", spec.Name), nil
}

func (f *fakeAWSBootstrapClient) EnsureKMSKey(ctx context.Context, spec KMSKeySpec) (ApplyAction, error) {
	return f.ensure("kms-key", spec.Alias), nil
}

func (f *fakeAWSBootstrapClient) EnsureIAMRole(ctx context.Context, spec IAMRoleSpec) (ApplyAction, error) {
	return f.ensure("iam-role", spec.Name), nil
}

func (f *fakeAWSBootstrapClient) PutBucketPolicy(ctx context.Context, spec BucketPolicySpec) (ApplyAction, error) {
	return f.put("s3-bucket-policy", spec.Bucket), nil
}

func (f *fakeAWSBootstrapClient) PutEnvironmentRoot(ctx context.Context, spec EnvironmentRootSpec) (ApplyAction, error) {
	return f.ensure("environment-root", spec.Key), nil
}

func (f *fakeAWSBootstrapClient) ensure(kind, name string) ApplyAction {
	key := kind + "/" + name
	if f.seen[key] {
		return ApplyAction{Kind: kind, Name: name, Action: "unchanged"}
	}
	f.seen[key] = true
	return ApplyAction{Kind: kind, Name: name, Action: "created", ProviderID: key}
}

func (f *fakeAWSBootstrapClient) put(kind, name string) ApplyAction {
	key := kind + "/" + name
	if f.seen[key] {
		return ApplyAction{Kind: kind, Name: name, Action: "unchanged"}
	}
	f.seen[key] = true
	return ApplyAction{Kind: kind, Name: name, Action: "put", ProviderID: key}
}
