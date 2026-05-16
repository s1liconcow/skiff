package aws_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

func TestInspectServiceDiscoversTaggedResources(t *testing.T) {
	autoScaling := &mockAutoScaling{
		resources: []aws.DiscoveredResource{{
			Name:       "skiff-prod-payments-api-asg",
			ProviderID: "asg/prod-payments",
			ARN:        "arn:aws:autoscaling:us-west-2:123:autoScalingGroup:abc:autoScalingGroupName/skiff-prod-payments-api-asg",
			Status:     "InService",
			Tags: map[string]string{
				ir.TagService: "payments-api",
				ir.TagEnv:     "prod",
			},
		}},
	}
	ec2 := &mockEC2{resources: []aws.DiscoveredResource{{Name: "skiff-prod-payments-api-lt", ProviderID: "lt-123"}}}
	elb := &mockELBV2{resources: []aws.DiscoveredResource{{Name: "skiff-prod-payments-api-tg", ProviderID: "tg-123"}}}
	iam := &mockIAM{resources: []aws.DiscoveredResource{{Name: "skiff-prod-payments-api-role", ProviderID: "role/payments-api"}}}
	logs := &mockLogs{resources: []aws.DiscoveredResource{{Name: "/skiff/prod/payments-api", ProviderID: "log-group:/skiff/prod/payments-api"}}}

	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2"}, aws.WithClients(aws.Clients{
		AutoScaling: autoScaling,
		EC2:         ec2,
		ELBV2:       elb,
		IAM:         iam,
		Logs:        logs,
	}))
	if err != nil {
		t.Fatal(err)
	}

	inspection, err := p.InspectService(context.Background(), provider.ServiceRef{Service: "payments-api", Env: "prod"})
	if err != nil {
		t.Fatalf("InspectService() error = %v", err)
	}
	if len(inspection.Resources) != 5 {
		t.Fatalf("resource count = %d, want 5: %#v", len(inspection.Resources), inspection.Resources)
	}
	wantKinds := []string{
		aws.ResourceKindAutoScalingGroup,
		aws.ResourceKindLaunchTemplate,
		aws.ResourceKindTargetGroup,
		aws.ResourceKindIAMRole,
		aws.ResourceKindLogGroup,
	}
	for i, kind := range wantKinds {
		if inspection.Resources[i].Kind != kind {
			t.Fatalf("resource[%d].Kind = %q, want %q", i, inspection.Resources[i].Kind, kind)
		}
	}

	wantFilters := aws.SkiffTagFilters("payments-api", "prod")
	for name, got := range map[string][]aws.TagFilter{
		"autoscaling": autoScaling.filters,
		"ec2":         ec2.filters,
		"elbv2":       elb.filters,
		"iam":         iam.filters,
		"logs":        logs.filters,
	} {
		if !reflect.DeepEqual(got, wantFilters) {
			t.Fatalf("%s filters mismatch\nwant: %#v\n got: %#v", name, wantFilters, got)
		}
	}
}

func TestInspectResourceFindsDiscoveredResource(t *testing.T) {
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2"}, aws.WithClients(aws.Clients{
		AutoScaling: &mockAutoScaling{resources: []aws.DiscoveredResource{{Name: "skiff-prod-payments-api-asg", ProviderID: "asg-123"}}},
	}))
	if err != nil {
		t.Fatal(err)
	}

	resource, err := p.InspectResource(context.Background(), provider.ResourceRef{
		Service:    "payments-api",
		Env:        "prod",
		Kind:       aws.ResourceKindAutoScalingGroup,
		ProviderID: "asg-123",
	})
	if err != nil {
		t.Fatalf("InspectResource() error = %v", err)
	}
	if resource.Name != "skiff-prod-payments-api-asg" {
		t.Fatalf("name = %q", resource.Name)
	}
}

func TestDiscoverClassifiesClientErrors(t *testing.T) {
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2"}, aws.WithClients(aws.Clients{
		AutoScaling: &mockAutoScaling{err: errors.New("AccessDenied: not authorized")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.InspectService(context.Background(), provider.ServiceRef{Service: "payments-api", Env: "prod"})
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("InspectService() error = %T, want provider.Error", err)
	}
	if providerErr.Code != provider.CodeAccessDenied {
		t.Fatalf("code = %s, want %s", providerErr.Code, provider.CodeAccessDenied)
	}
}

type mockAutoScaling struct {
	filters   []aws.TagFilter
	resources []aws.DiscoveredResource
	err       error
}

func (m *mockAutoScaling) FindAutoScalingGroups(ctx context.Context, filters []aws.TagFilter) ([]aws.DiscoveredResource, error) {
	m.filters = append([]aws.TagFilter(nil), filters...)
	return m.resources, m.err
}

type mockEC2 struct {
	filters   []aws.TagFilter
	resources []aws.DiscoveredResource
	err       error
}

func (m *mockEC2) FindLaunchTemplates(ctx context.Context, filters []aws.TagFilter) ([]aws.DiscoveredResource, error) {
	m.filters = append([]aws.TagFilter(nil), filters...)
	return m.resources, m.err
}

type mockELBV2 struct {
	filters   []aws.TagFilter
	resources []aws.DiscoveredResource
	err       error
}

func (m *mockELBV2) FindTargetGroups(ctx context.Context, filters []aws.TagFilter) ([]aws.DiscoveredResource, error) {
	m.filters = append([]aws.TagFilter(nil), filters...)
	return m.resources, m.err
}

type mockIAM struct {
	filters   []aws.TagFilter
	resources []aws.DiscoveredResource
	err       error
}

func (m *mockIAM) FindRoles(ctx context.Context, filters []aws.TagFilter) ([]aws.DiscoveredResource, error) {
	m.filters = append([]aws.TagFilter(nil), filters...)
	return m.resources, m.err
}

type mockLogs struct {
	filters   []aws.TagFilter
	resources []aws.DiscoveredResource
	err       error
}

func (m *mockLogs) FindLogGroups(ctx context.Context, filters []aws.TagFilter) ([]aws.DiscoveredResource, error) {
	m.filters = append([]aws.TagFilter(nil), filters...)
	return m.resources, m.err
}
