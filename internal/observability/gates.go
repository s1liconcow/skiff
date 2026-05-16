package observability

import (
	"context"
	"fmt"
	"math"

	"github.com/s1liconcow/skiff/internal/provider"
)

type MetricClient interface {
	Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error)
}

type MetricThresholdGate struct {
	Metric    string  `json:"metric"`
	Operator  string  `json:"operator"`
	Threshold float64 `json:"threshold"`
}

type MetricGateResult struct {
	OK        bool    `json:"ok"`
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Operator  string  `json:"operator"`
	Summary   string  `json:"summary"`
}

func EvaluateMetricThreshold(ctx context.Context, client MetricClient, req provider.MetricsRequest, gate MetricThresholdGate) (MetricGateResult, error) {
	if client == nil {
		return MetricGateResult{}, fmt.Errorf("metric client is required")
	}
	if gate.Metric == "" {
		return MetricGateResult{}, fmt.Errorf("metric name is required")
	}
	req.Names = []string{gate.Metric}
	result, err := client.Metrics(ctx, req)
	if err != nil {
		return MetricGateResult{}, err
	}
	value, ok := latestMetricValue(result.Series, gate.Metric)
	if !ok {
		return MetricGateResult{
			OK:        false,
			Metric:    gate.Metric,
			Threshold: gate.Threshold,
			Operator:  gate.Operator,
			Summary:   "metric has no data points",
		}, nil
	}
	passed, err := compareMetricValue(value, gate.Operator, gate.Threshold)
	if err != nil {
		return MetricGateResult{}, err
	}
	return MetricGateResult{
		OK:        passed,
		Metric:    gate.Metric,
		Value:     value,
		Threshold: gate.Threshold,
		Operator:  gate.Operator,
		Summary:   metricGateSummary(gate.Metric, value, gate.Operator, gate.Threshold, passed),
	}, nil
}

func latestMetricValue(series []provider.MetricSeries, name string) (float64, bool) {
	found := false
	value := math.NaN()
	var latest int64
	for _, item := range series {
		if item.Name != name {
			continue
		}
		for _, point := range item.Points {
			ts := point.Timestamp.UnixNano()
			if !found || ts >= latest {
				found = true
				latest = ts
				value = point.Value
			}
		}
	}
	return value, found
}

func compareMetricValue(value float64, op string, threshold float64) (bool, error) {
	switch op {
	case "<":
		return value < threshold, nil
	case "<=":
		return value <= threshold, nil
	case ">":
		return value > threshold, nil
	case ">=":
		return value >= threshold, nil
	default:
		return false, fmt.Errorf("unsupported metric threshold operator %q", op)
	}
}

func metricGateSummary(metric string, value float64, op string, threshold float64, passed bool) string {
	status := "failed"
	if passed {
		status = "passed"
	}
	return fmt.Sprintf("%s %s: %.6g %s %.6g", metric, status, value, op, threshold)
}
