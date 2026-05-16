package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

func TestMetricsJSONUsesProviderFilters(t *testing.T) {
	clearSkiffEnv(t)
	now := time.Date(2026, 5, 16, 23, 58, 0, 0, time.UTC)
	fake := &fakeMetricsProvider{
		result: &provider.MetricsResult{Series: []provider.MetricSeries{{
			Name:   aws.MetricALBRequestCount,
			Source: "target-group/skiff-prod-payments-api-tg",
			Unit:   "Count",
			Labels: map[string]string{"service": "payments-api", "release": "rel_02"},
			Points: []provider.MetricPoint{{Timestamp: now, Value: 42}},
		}}},
	}
	restoreMetricsTestHooks(t, fake)

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"metrics", "payments-api",
		"--direct",
		"--state", "file://" + t.TempDir(),
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--release", "rel_02",
		"--instance", "i-123",
		"--metric", aws.MetricALBRequestCount,
		"--from", "2026-05-16T23:50:00Z",
		"--to", "2026-05-17T00:00:00Z",
		"--period", "120",
		"--format", "json",
		"--trace-id", "tr_metrics",
	}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if fake.req.Service != "payments-api" || fake.req.Env != "prod" || fake.req.ReleaseID != "rel_02" || fake.req.InstanceID != "i-123" {
		t.Fatalf("unexpected provider request: %+v", fake.req)
	}
	if len(fake.req.Names) != 1 || fake.req.Names[0] != aws.MetricALBRequestCount || fake.req.PeriodSeconds != 120 {
		t.Fatalf("unexpected metric request filters: %+v", fake.req)
	}

	var got metricsOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("metrics output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !got.OK || got.TraceID != "tr_metrics" || len(got.Series) != 1 || got.Series[0].Points[0].Value != 42 {
		t.Fatalf("unexpected metrics output: %+v", got)
	}
}

func TestMetricsProviderErrorJSONIsActionable(t *testing.T) {
	clearSkiffEnv(t)
	fake := &fakeMetricsProvider{err: &provider.Error{Code: provider.CodeAccessDenied, Provider: "aws", Op: "metrics", Summary: "cloudwatch permission denied"}}
	restoreMetricsTestHooks(t, fake)

	var stdout, stderr bytes.Buffer
	code := Run("skiff", []string{
		"metrics", "payments-api",
		"--direct",
		"--state", "file://" + t.TempDir(),
		"--env", "prod",
		"--provider", "aws",
		"--region", "us-west-2",
		"--format", "json",
		"--trace-id", "tr_metrics_error",
	}, &stdout, &stderr)
	if code != ExitProviderError {
		t.Fatalf("exit code = %d, want %d; stderr = %s stdout = %s", code, ExitProviderError, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var got commandErrorOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("metrics error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OK || got.Code != string(provider.CodeAccessDenied) || got.TraceID != "tr_metrics_error" || len(got.RecommendedActions) == 0 {
		t.Fatalf("unexpected metrics error output: %+v", got)
	}
}

func restoreMetricsTestHooks(t *testing.T, fake *fakeMetricsProvider) {
	t.Helper()
	oldNewMetricsProvider := newMetricsProviderForCLI
	newMetricsProviderForCLI = func(cfg config.Config) (metricsProvider, error) {
		if cfg.Env != "prod" || cfg.Provider != "aws" || cfg.Region != "us-west-2" {
			return nil, errors.New("unexpected config")
		}
		return fake, nil
	}
	t.Cleanup(func() {
		newMetricsProviderForCLI = oldNewMetricsProvider
	})
}

type fakeMetricsProvider struct {
	req    provider.MetricsRequest
	result *provider.MetricsResult
	err    error
}

func (p *fakeMetricsProvider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	p.req = req
	if p.err != nil {
		return nil, p.err
	}
	if p.result == nil {
		return &provider.MetricsResult{}, nil
	}
	return p.result, nil
}
