package aws_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/observability"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

func TestMetricsBuildsCoreCloudWatchQueriesAndNormalizesSeries(t *testing.T) {
	now := time.Date(2026, 5, 16, 23, 50, 0, 0, time.UTC)
	client := &fakeMetricQueryClient{series: []observability.MetricSeries{
		{
			Name:     aws.MetricALBRequestCount,
			Category: "cloud",
			Source:   "target-group/skiff-prod-payments-api-tg",
			Unit:     "Count",
			Identity: observability.MetricIdentity{Service: "payments-api", Env: "prod", Release: "rel_02"},
			Points:   []observability.MetricPoint{{Timestamp: now.Add(time.Minute), Value: 12}, {Timestamp: now, Value: 10}},
		},
		{
			Name:     aws.MetricInstanceCPUUtilization,
			Category: "node",
			Unit:     "Percent",
			Identity: observability.MetricIdentity{Service: "payments-api", Env: "prod", Release: "rel_01", Instance: "i-old"},
			Points:   []observability.MetricPoint{{Timestamp: now, Value: 90}},
		},
	}}
	p := newMetricsProvider(t, client)
	result, err := p.Metrics(context.Background(), provider.MetricsRequest{
		Service:   "payments-api",
		Env:       "prod",
		ReleaseID: "rel_02",
		From:      now.Add(-time.Minute),
		To:        now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if client.req.Service != "payments-api" || client.req.Env != "prod" || client.req.PeriodSeconds != 60 {
		t.Fatalf("unexpected metric request: %+v", client.req)
	}
	if len(client.req.Queries) != 8 {
		t.Fatalf("query count = %d, want 8: %+v", len(client.req.Queries), client.req.Queries)
	}
	assertMetricQuery(t, client.req.Queries, aws.MetricASGDesiredCapacity, "AWS/AutoScaling", "GroupDesiredCapacity", "AutoScalingGroupName")
	assertMetricQuery(t, client.req.Queries, aws.MetricALBRequestCount, "AWS/ApplicationELB", "RequestCount", "TargetGroup")
	assertMetricQuery(t, client.req.Queries, aws.MetricInstanceCPUUtilization, "AWS/EC2", "CPUUtilization", "")
	if len(result.Series) != 1 {
		t.Fatalf("series = %d, want 1: %+v", len(result.Series), result.Series)
	}
	if result.Series[0].Labels["service"] != "payments-api" || result.Series[0].Labels["release"] != "rel_02" || result.Series[0].Labels["region"] != "us-west-2" {
		t.Fatalf("series labels not enriched: %+v", result.Series[0].Labels)
	}
	if len(result.Series[0].Points) != 2 || result.Series[0].Points[0].Value != 10 || result.Series[0].Points[1].Value != 12 {
		t.Fatalf("metric points not sorted: %+v", result.Series[0].Points)
	}
}

func TestMetricsFiltersByRequestedNamesAndInstance(t *testing.T) {
	now := time.Date(2026, 5, 16, 23, 55, 0, 0, time.UTC)
	client := &fakeMetricQueryClient{series: []observability.MetricSeries{
		{Name: aws.MetricInstanceCPUUtilization, Identity: observability.MetricIdentity{Release: "rel_02", Instance: "i-keep"}, Points: []observability.MetricPoint{{Timestamp: now, Value: 40}}},
		{Name: aws.MetricInstanceCPUUtilization, Identity: observability.MetricIdentity{Release: "rel_02", Instance: "i-drop"}, Points: []observability.MetricPoint{{Timestamp: now, Value: 80}}},
	}}
	p := newMetricsProvider(t, client)
	result, err := p.Metrics(context.Background(), provider.MetricsRequest{
		Service:    "payments-api",
		Env:        "prod",
		ReleaseID:  "rel_02",
		InstanceID: "i-keep",
		Names:      []string{aws.MetricInstanceCPUUtilization},
	})
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if len(client.req.Queries) != 1 || client.req.Queries[0].Name != aws.MetricInstanceCPUUtilization {
		t.Fatalf("unexpected queries: %+v", client.req.Queries)
	}
	if len(result.Series) != 1 || result.Series[0].Labels["instance"] != "i-keep" {
		t.Fatalf("unexpected filtered series: %+v", result.Series)
	}
}

func TestMetricsClassifiesPermissionErrors(t *testing.T) {
	p := newMetricsProvider(t, &fakeMetricQueryClient{err: errors.New("AccessDeniedException: not authorized")})
	_, err := p.Metrics(context.Background(), provider.MetricsRequest{Service: "payments-api", Env: "prod"})
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("metrics err = %T, want provider.Error", err)
	}
	if providerErr.Code != provider.CodeAccessDenied {
		t.Fatalf("code = %s, want %s", providerErr.Code, provider.CodeAccessDenied)
	}
}

func newMetricsProvider(t *testing.T, client *fakeMetricQueryClient) *aws.Provider {
	t.Helper()
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2"}, aws.WithClients(aws.Clients{MetricQueries: client}))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func assertMetricQuery(t *testing.T, queries []aws.MetricQuery, name, namespace, metric, dimension string) {
	t.Helper()
	for _, query := range queries {
		if query.Name != name {
			continue
		}
		if query.Namespace != namespace || query.MetricName != metric {
			t.Fatalf("query %s = %+v", name, query)
		}
		if dimension != "" && query.Dimensions[dimension] == "" {
			t.Fatalf("query %s missing dimension %s: %+v", name, dimension, query)
		}
		return
	}
	t.Fatalf("missing query %s in %+v", name, queries)
}

type fakeMetricQueryClient struct {
	req    aws.QueryMetricsRequest
	series []observability.MetricSeries
	err    error
}

func (c *fakeMetricQueryClient) QueryServiceMetrics(ctx context.Context, req aws.QueryMetricsRequest) ([]observability.MetricSeries, error) {
	c.req = req
	return c.series, c.err
}
