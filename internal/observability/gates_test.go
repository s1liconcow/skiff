package observability

import (
	"context"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/provider"
)

func TestEvaluateMetricThresholdUsesMetricClient(t *testing.T) {
	client := &fakeMetricClient{result: &provider.MetricsResult{Series: []provider.MetricSeries{{
		Name: "aws.elb.http_5xx_count",
		Points: []provider.MetricPoint{
			{Timestamp: time.Date(2026, 5, 16, 23, 0, 0, 0, time.UTC), Value: 1},
			{Timestamp: time.Date(2026, 5, 16, 23, 1, 0, 0, time.UTC), Value: 3},
		},
	}}}}
	result, err := EvaluateMetricThreshold(context.Background(), client, provider.MetricsRequest{
		Service: "payments-api",
		Env:     "prod",
	}, MetricThresholdGate{Metric: "aws.elb.http_5xx_count", Operator: "<=", Threshold: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Value != 3 {
		t.Fatalf("unexpected gate result: %+v", result)
	}
	if len(client.req.Names) != 1 || client.req.Names[0] != "aws.elb.http_5xx_count" {
		t.Fatalf("gate did not constrain provider metric request: %+v", client.req)
	}
}

func TestEvaluateMetricThresholdMissingDataFailsClosed(t *testing.T) {
	client := &fakeMetricClient{result: &provider.MetricsResult{}}
	result, err := EvaluateMetricThreshold(context.Background(), client, provider.MetricsRequest{
		Service: "payments-api",
		Env:     "prod",
	}, MetricThresholdGate{Metric: "aws.elb.http_5xx_count", Operator: "<=", Threshold: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Summary != "metric has no data points" {
		t.Fatalf("unexpected missing data result: %+v", result)
	}
}

type fakeMetricClient struct {
	req    provider.MetricsRequest
	result *provider.MetricsResult
}

func (c *fakeMetricClient) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	c.req = req
	return c.result, nil
}
