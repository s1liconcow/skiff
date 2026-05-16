package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/observability"
	"github.com/s1liconcow/skiff/internal/provider"
)

const (
	MetricASGDesiredCapacity       = "aws.asg.desired_capacity"
	MetricASGInServiceInstances    = "aws.asg.in_service_instances"
	MetricTargetHealthyCount       = "aws.elb.target_healthy_count"
	MetricTargetUnhealthyCount     = "aws.elb.target_unhealthy_count"
	MetricALBRequestCount          = "aws.elb.request_count"
	MetricALBTargetResponseTimeP95 = "aws.elb.target_response_time_p95"
	MetricALBHTTP5XXCount          = "aws.elb.http_5xx_count"
	MetricInstanceCPUUtilization   = "aws.ec2.cpu_utilization"
)

type MetricQueryClient interface {
	QueryServiceMetrics(ctx context.Context, req QueryMetricsRequest) ([]observability.MetricSeries, error)
}

type QueryMetricsRequest struct {
	Service       string        `json:"service"`
	Env           string        `json:"env"`
	ReleaseID     string        `json:"release_id,omitempty"`
	InstanceID    string        `json:"instance_id,omitempty"`
	From          time.Time     `json:"from"`
	To            time.Time     `json:"to"`
	PeriodSeconds int           `json:"period_seconds"`
	Queries       []MetricQuery `json:"queries"`
}

type MetricQuery struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	MetricName string            `json:"metric_name"`
	Statistic  string            `json:"statistic"`
	Unit       string            `json:"unit,omitempty"`
	Dimensions map[string]string `json:"dimensions,omitempty"`
}

func (p *Provider) Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.clients.MetricQueries == nil {
		return nil, provider.Unsupported(Name, "metrics")
	}
	if strings.TrimSpace(req.Service) == "" || strings.TrimSpace(req.Env) == "" {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "metrics", Summary: "service and env are required"}
	}
	from, to := metricWindow(req.From, req.To)
	period := req.PeriodSeconds
	if period <= 0 {
		period = 60
	}
	queries, err := CoreMetricQueries(req.Service, req.Env, req.Names)
	if err != nil {
		return nil, err
	}
	series, err := p.clients.MetricQueries.QueryServiceMetrics(ctx, QueryMetricsRequest{
		Service:       req.Service,
		Env:           req.Env,
		ReleaseID:     req.ReleaseID,
		InstanceID:    req.InstanceID,
		From:          from,
		To:            to,
		PeriodSeconds: period,
		Queries:       queries,
	})
	if err != nil {
		return nil, ClassifyError("metrics", err)
	}
	series = filterMetricSeries(series, req)
	observability.SortMetricSeries(series)
	out := make([]provider.MetricSeries, 0, len(series))
	for _, item := range series {
		labels := cloneStringMap(item.Labels)
		if labels == nil {
			labels = map[string]string{}
		}
		labels["service"] = firstNonEmpty(item.Identity.Service, req.Service)
		labels["env"] = firstNonEmpty(item.Identity.Env, req.Env)
		if release := firstNonEmpty(item.Identity.Release, req.ReleaseID); release != "" {
			labels["release"] = release
		}
		if instance := firstNonEmpty(item.Identity.Instance, req.InstanceID); instance != "" {
			labels["instance"] = instance
		}
		if region := firstNonEmpty(item.Identity.Region, p.cfg.Region); region != "" {
			labels["region"] = region
		}
		if item.Identity.Zone != "" {
			labels["zone"] = item.Identity.Zone
		}
		points := make([]provider.MetricPoint, 0, len(item.Points))
		for _, point := range item.Points {
			if (!from.IsZero() && point.Timestamp.Before(from)) || (!to.IsZero() && point.Timestamp.After(to)) {
				continue
			}
			points = append(points, provider.MetricPoint{Timestamp: point.Timestamp.UTC(), Value: point.Value})
		}
		out = append(out, provider.MetricSeries{
			Name:     item.Name,
			Category: item.Category,
			Source:   item.Source,
			Unit:     item.Unit,
			Labels:   labels,
			Points:   points,
		})
	}
	return &provider.MetricsResult{Series: out}, nil
}

func CoreMetricQueries(service, env string, names []string) ([]MetricQuery, error) {
	asgName, err := rolloutASGName(service, env)
	if err != nil {
		return nil, err
	}
	targetGroupName, err := serviceTargetGroupName(service, env)
	if err != nil {
		return nil, err
	}
	all := []MetricQuery{
		{Name: MetricASGDesiredCapacity, Namespace: "AWS/AutoScaling", MetricName: "GroupDesiredCapacity", Statistic: "Average", Unit: "Count", Dimensions: map[string]string{"AutoScalingGroupName": asgName}},
		{Name: MetricASGInServiceInstances, Namespace: "AWS/AutoScaling", MetricName: "GroupInServiceInstances", Statistic: "Average", Unit: "Count", Dimensions: map[string]string{"AutoScalingGroupName": asgName}},
		{Name: MetricTargetHealthyCount, Namespace: "AWS/ApplicationELB", MetricName: "HealthyHostCount", Statistic: "Average", Unit: "Count", Dimensions: map[string]string{"TargetGroup": targetGroupName}},
		{Name: MetricTargetUnhealthyCount, Namespace: "AWS/ApplicationELB", MetricName: "UnHealthyHostCount", Statistic: "Average", Unit: "Count", Dimensions: map[string]string{"TargetGroup": targetGroupName}},
		{Name: MetricALBRequestCount, Namespace: "AWS/ApplicationELB", MetricName: "RequestCount", Statistic: "Sum", Unit: "Count", Dimensions: map[string]string{"TargetGroup": targetGroupName}},
		{Name: MetricALBTargetResponseTimeP95, Namespace: "AWS/ApplicationELB", MetricName: "TargetResponseTime", Statistic: "p95", Unit: "Seconds", Dimensions: map[string]string{"TargetGroup": targetGroupName}},
		{Name: MetricALBHTTP5XXCount, Namespace: "AWS/ApplicationELB", MetricName: "HTTPCode_Target_5XX_Count", Statistic: "Sum", Unit: "Count", Dimensions: map[string]string{"TargetGroup": targetGroupName}},
		{Name: MetricInstanceCPUUtilization, Namespace: "AWS/EC2", MetricName: "CPUUtilization", Statistic: "Average", Unit: "Percent"},
	}
	if len(names) == 0 {
		return all, nil
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[strings.TrimSpace(name)] = struct{}{}
	}
	var out []MetricQuery
	for _, query := range all {
		if _, ok := wanted[query.Name]; ok {
			out = append(out, query)
		}
	}
	if len(out) == 0 {
		return nil, &provider.Error{Code: provider.CodeValidation, Provider: Name, Op: "metrics", Summary: fmt.Sprintf("no supported metric names matched %q", strings.Join(names, ","))}
	}
	return out, nil
}

func metricWindow(from, to time.Time) (time.Time, time.Time) {
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.Add(-15 * time.Minute)
	}
	return from.UTC(), to.UTC()
}

func serviceTargetGroupName(service, env string) (string, error) {
	return ResourceName(NameInput{
		Service: service,
		Env:     env,
		Kind:    ResourceKindTargetGroup,
		Base:    fmt.Sprintf("skiff-%s-%s-tg", env, service),
	})
}

func filterMetricSeries(series []observability.MetricSeries, req provider.MetricsRequest) []observability.MetricSeries {
	out := series[:0]
	for _, item := range series {
		if req.ReleaseID != "" && metricSeriesValue(item, item.Identity.Release, "release", "release_id") != req.ReleaseID {
			continue
		}
		if req.InstanceID != "" && metricSeriesValue(item, item.Identity.Instance, "instance", "instance_id") != req.InstanceID {
			continue
		}
		out = append(out, item)
	}
	return out
}

func metricSeriesValue(series observability.MetricSeries, primary string, labels ...string) string {
	if primary != "" {
		return primary
	}
	for _, label := range labels {
		if value := series.Labels[label]; value != "" {
			return value
		}
	}
	return ""
}
