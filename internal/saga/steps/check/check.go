package check

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/saga/steps"
	"github.com/s1liconcow/skiff/internal/state"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	KindPreflight     = "check.preflight"
	KindServiceHealth = "check.service_healthy"
	KindTargetHealth  = "check.target_health"
	KindMetricsGate   = "check.metrics_gate"
)

type ProviderIdentity interface {
	Name() string
}

type ServiceInspector interface {
	InspectService(ctx context.Context, ref provider.ServiceRef) (*provider.ServiceInspection, error)
}

type ResourceInspector interface {
	InspectResource(ctx context.Context, ref provider.ResourceRef) (*provider.ResourceInspection, error)
}

type MetricsClient interface {
	Metrics(ctx context.Context, req provider.MetricsRequest) (*provider.MetricsResult, error)
}

type Preflight struct {
	Store    objstore.ObjectStore
	Provider ProviderIdentity
}

type ServiceHealthy struct {
	Provider ServiceInspector
}

type TargetHealth struct {
	Provider ResourceInspector
}

type MetricsGate struct {
	Client MetricsClient
}

type preflightParams struct {
	Service               string   `json:"service,omitempty"`
	Env                   string   `json:"env,omitempty"`
	RequireServiceControl *bool    `json:"require_service_control,omitempty"`
	RequireProvider       *bool    `json:"require_provider,omitempty"`
	RequiredFacts         []string `json:"required_facts,omitempty"`
}

type serviceHealthParams struct {
	Service         string   `json:"service,omitempty"`
	Env             string   `json:"env,omitempty"`
	AllowedStatuses []string `json:"allowed_statuses,omitempty"`
}

type targetHealthParams struct {
	Service         string   `json:"service,omitempty"`
	Env             string   `json:"env,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	LogicalID       string   `json:"logical_id,omitempty"`
	Name            string   `json:"name,omitempty"`
	ProviderID      string   `json:"provider_id,omitempty"`
	AllowedStatuses []string `json:"allowed_statuses,omitempty"`
}

type metricsGateParams struct {
	Service       string   `json:"service,omitempty"`
	Env           string   `json:"env,omitempty"`
	Metric        string   `json:"metric"`
	Comparator    string   `json:"comparator"`
	Threshold     float64  `json:"threshold"`
	Names         []string `json:"names,omitempty"`
	Window        string   `json:"window,omitempty"`
	PeriodSeconds int      `json:"period_seconds,omitempty"`
}

func (s Preflight) Kind() string { return KindPreflight }

func (s Preflight) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeParams[preflightParams](params)
	return err
}

func (s Preflight) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "verify object state, provider, identity, and service control before continuing", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s Preflight) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	params, err := decodeParams[preflightParams](req.Node.Params)
	if err != nil {
		return nil, err
	}
	service := firstNonEmpty(params.Service, req.Intent.Target.Name)
	env := params.Env
	facts := []schema.Fact{
		{Type: "saga", Message: req.SagaID},
		{Type: "trace_id", Message: req.TraceID},
	}
	if strings.TrimSpace(req.Intent.Actor.ID) == "" || strings.TrimSpace(req.Intent.Actor.Type) == "" {
		return failed("PREFLIGHT_IDENTITY_MISSING", "saga intent actor is incomplete", facts), nil
	}
	facts = append(facts, schema.Fact{Type: "actor", Message: req.Intent.Actor.Type + ":" + req.Intent.Actor.ID})
	if boolDefault(params.RequireProvider, true) {
		if s.Provider == nil || strings.TrimSpace(s.Provider.Name()) == "" {
			return failed("PREFLIGHT_PROVIDER_MISSING", "provider is required for preflight", facts), nil
		}
		facts = append(facts, schema.Fact{Type: "provider", Message: s.Provider.Name()})
	}
	if s.Store == nil {
		return failed("PREFLIGHT_OBJECT_STATE_MISSING", "object store is required for preflight", facts), nil
	}
	facts = append(facts, schema.Fact{Type: "object_state", Message: "available"})
	if boolDefault(params.RequireServiceControl, true) {
		if service == "" {
			return failed("PREFLIGHT_SERVICE_MISSING", "service is required for service control preflight", facts), nil
		}
		control, err := state.NewClient(s.Store).GetServiceControl(ctx, service)
		if err != nil {
			if errors.Is(err, objstore.ErrNotFound) {
				return failed("PREFLIGHT_SERVICE_CONTROL_MISSING", "service control does not exist", facts), nil
			}
			return nil, err
		}
		if env != "" && control.Control.Env != env {
			return failed("PREFLIGHT_ENV_MISMATCH", fmt.Sprintf("service control env is %s, want %s", control.Control.Env, env), facts), nil
		}
		facts = append(facts,
			schema.Fact{Type: "service", Message: service},
			schema.Fact{Type: "service_control", Message: control.Key},
			schema.Fact{Type: "desired_release", Message: control.Control.DesiredRelease},
			schema.Fact{Type: "stable_release", Message: control.Control.StableRelease},
		)
	}
	for _, fact := range params.RequiredFacts {
		fact = strings.TrimSpace(fact)
		if fact != "" {
			facts = append(facts, schema.Fact{Type: "required_fact", Message: fact})
		}
	}
	return succeeded("preflight passed", facts, map[string]any{"ok": true, "facts": facts}), nil
}

func (s Preflight) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s Preflight) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "preflight has no compensation"})}, nil
}

func (s Preflight) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s ServiceHealthy) Kind() string { return KindServiceHealth }

func (s ServiceHealthy) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeParams[serviceHealthParams](params)
	return err
}

func (s ServiceHealthy) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "inspect provider service resources for unhealthy status", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s ServiceHealthy) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	if s.Provider == nil {
		return nil, errors.New("service health check provider is required")
	}
	params, err := decodeParams[serviceHealthParams](req.Node.Params)
	if err != nil {
		return nil, err
	}
	service := firstNonEmpty(params.Service, req.Intent.Target.Name)
	if service == "" {
		return nil, errors.New("service health check service is required")
	}
	inspection, err := s.Provider.InspectService(ctx, provider.ServiceRef{Service: service, Env: params.Env})
	if err != nil {
		return nil, err
	}
	if inspection == nil {
		return nil, errors.New("service health provider returned no inspection")
	}
	facts := []schema.Fact{{Type: "service", Message: service}, {Type: "provider", Message: inspection.Provider}}
	allowed := statusSet(params.AllowedStatuses, []string{"healthy", "ok", "active", "configured", "running", "applied", "unchanged"})
	for _, resource := range inspection.Resources {
		status := normalizeStatus(resource.Status)
		facts = append(facts, schema.Fact{Type: "resource", Message: fmt.Sprintf("%s %s %s", resource.Kind, resource.ProviderID, firstNonEmpty(resource.Status, "unknown"))})
		if status == "" {
			continue
		}
		if !allowed[status] {
			return failed("SERVICE_UNHEALTHY", fmt.Sprintf("%s %s status is %s", resource.Kind, firstNonEmpty(resource.ProviderID, resource.Name), resource.Status), facts), nil
		}
	}
	return succeeded("service health check passed", facts, map[string]any{"resource_count": len(inspection.Resources), "facts": facts}), nil
}

func (s ServiceHealthy) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s ServiceHealthy) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "service health check has no compensation"})}, nil
}

func (s ServiceHealthy) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s TargetHealth) Kind() string { return KindTargetHealth }

func (s TargetHealth) ValidateParams(ctx context.Context, params json.RawMessage) error {
	_, err := decodeParams[targetHealthParams](params)
	return err
}

func (s TargetHealth) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "inspect provider target group health", Risk: schema.RiskLow, Reversibility: schema.Reversible}, nil
}

func (s TargetHealth) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	if s.Provider == nil {
		return nil, errors.New("target health check provider is required")
	}
	params, err := decodeParams[targetHealthParams](req.Node.Params)
	if err != nil {
		return nil, err
	}
	service := firstNonEmpty(params.Service, req.Intent.Target.Name)
	kind := firstNonEmpty(params.Kind, "target-group")
	inspection, err := s.Provider.InspectResource(ctx, provider.ResourceRef{
		Service:    service,
		Env:        params.Env,
		Kind:       kind,
		LogicalID:  params.LogicalID,
		Name:       params.Name,
		ProviderID: params.ProviderID,
	})
	if err != nil {
		return nil, err
	}
	if inspection == nil {
		return nil, errors.New("target health provider returned no inspection")
	}
	status := normalizeStatus(inspection.Status)
	allowed := statusSet(params.AllowedStatuses, []string{"healthy", "ok", "active", "configured", "in-service", "applied", "unchanged"})
	facts := []schema.Fact{
		{Type: "resource_kind", Message: inspection.Kind},
		{Type: "provider_id", Message: inspection.ProviderID},
		{Type: "status", Message: firstNonEmpty(inspection.Status, "unknown")},
	}
	if status != "" && !allowed[status] {
		return failed("TARGET_HEALTH_FAILED", fmt.Sprintf("target %s status is %s", firstNonEmpty(inspection.ProviderID, inspection.Name), inspection.Status), facts), nil
	}
	return succeeded("target health check passed", facts, map[string]any{"status": inspection.Status, "provider_id": inspection.ProviderID}), nil
}

func (s TargetHealth) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s TargetHealth) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "target health check has no compensation"})}, nil
}

func (s TargetHealth) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func (s MetricsGate) Kind() string { return KindMetricsGate }

func (s MetricsGate) ValidateParams(ctx context.Context, params json.RawMessage) error {
	decoded, err := decodeParams[metricsGateParams](params)
	if err != nil {
		return err
	}
	if strings.TrimSpace(decoded.Metric) == "" && len(decoded.Names) == 0 {
		return errors.New("metrics gate metric or names is required")
	}
	if decoded.Comparator == "" {
		return nil
	}
	if _, err := compare(0, decoded.Comparator, 0); err != nil {
		return err
	}
	return nil
}

func (s MetricsGate) Plan(ctx context.Context, req steps.StepRequest) (*steps.StepPlan, error) {
	return &steps.StepPlan{Summary: "evaluate observed metrics against configured gate threshold", Risk: schema.RiskMedium, Reversibility: schema.Reversible}, nil
}

func (s MetricsGate) Run(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	if s.Client == nil {
		return nil, errors.New("metrics gate client is required")
	}
	params, err := decodeParams[metricsGateParams](req.Node.Params)
	if err != nil {
		return nil, err
	}
	service := firstNonEmpty(params.Service, req.Intent.Target.Name)
	if service == "" {
		return nil, errors.New("metrics gate service is required")
	}
	names := append([]string(nil), params.Names...)
	if len(names) == 0 && params.Metric != "" {
		names = []string{params.Metric}
	}
	window := parseWindow(params.Window)
	result, err := s.Client.Metrics(ctx, provider.MetricsRequest{
		Service:       service,
		Env:           params.Env,
		Names:         names,
		From:          time.Now().UTC().Add(-window),
		To:            time.Now().UTC(),
		PeriodSeconds: params.PeriodSeconds,
	})
	if err != nil {
		return nil, err
	}
	metricName := firstNonEmpty(params.Metric, firstString(names))
	value, ok := latestMetricValue(result, metricName)
	facts := []schema.Fact{
		{Type: "metric", Message: metricName},
		{Type: "comparator", Message: firstNonEmpty(params.Comparator, "<=")},
		{Type: "threshold", Message: fmt.Sprintf("%g", params.Threshold)},
	}
	if !ok || math.IsNaN(value) {
		return failed("METRIC_MISSING", "metrics gate did not find an observed value for "+metricName, facts), nil
	}
	facts = append(facts, schema.Fact{Type: "observed", Message: fmt.Sprintf("%g", value)})
	passed, err := compare(value, firstNonEmpty(params.Comparator, "<="), params.Threshold)
	if err != nil {
		return nil, err
	}
	if !passed {
		return failed("METRIC_GATE_FAILED", fmt.Sprintf("metric %s observed %g does not satisfy %s %g", metricName, value, firstNonEmpty(params.Comparator, "<="), params.Threshold), facts), nil
	}
	return succeeded("metrics gate passed", facts, map[string]any{"metric": metricName, "observed": value, "comparator": firstNonEmpty(params.Comparator, "<="), "threshold": params.Threshold}), nil
}

func (s MetricsGate) Resume(ctx context.Context, req steps.StepRequest) (*steps.StepResult, error) {
	return s.Run(ctx, req)
}

func (s MetricsGate) Compensate(ctx context.Context, req steps.StepRequest, result schema.StepResult) (*steps.StepResult, error) {
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: rawJSON(map[string]string{"summary": "metrics gate has no compensation"})}, nil
}

func (s MetricsGate) Doctor(ctx context.Context, req steps.StepRequest) ([]steps.Finding, error) {
	return nil, nil
}

func succeeded(summary string, facts []schema.Fact, result any) *steps.StepResult {
	body := rawJSON(result)
	return &steps.StepResult{Status: steps.StatusSucceeded, Result: body, Summary: summary}
}

func failed(code, summary string, facts []schema.Fact) *steps.StepResult {
	return &steps.StepResult{
		Status: steps.StatusFailed,
		Result: rawJSON(map[string]any{
			"ok":      false,
			"code":    code,
			"summary": summary,
			"facts":   facts,
		}),
		Failure: &schema.StepFailure{Code: code, Summary: summary},
		Summary: summary,
	}
}

func decodeParams[T any](body json.RawMessage) (T, error) {
	var out T
	if len(bytes.TrimSpace(body)) == 0 {
		return out, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func statusSet(values, defaults []string) map[string]bool {
	if len(values) == 0 {
		values = defaults
	}
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[normalizeStatus(value)] = true
	}
	return out
}

func normalizeStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func latestMetricValue(result *provider.MetricsResult, name string) (float64, bool) {
	if result == nil {
		return 0, false
	}
	for _, series := range result.Series {
		if name != "" && series.Name != name {
			continue
		}
		if len(series.Points) == 0 {
			continue
		}
		return series.Points[len(series.Points)-1].Value, true
	}
	return 0, false
}

func compare(observed float64, comparator string, threshold float64) (bool, error) {
	switch strings.TrimSpace(comparator) {
	case "", "<=":
		return observed <= threshold, nil
	case "<":
		return observed < threshold, nil
	case ">=":
		return observed >= threshold, nil
	case ">":
		return observed > threshold, nil
	case "==", "=":
		return observed == threshold, nil
	default:
		return false, fmt.Errorf("unsupported metrics comparator %q", comparator)
	}
}

func parseWindow(value string) time.Duration {
	if value == "" {
		return 5 * time.Minute
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 5 * time.Minute
	}
	return parsed
}

func rawJSON(value any) json.RawMessage {
	body, _ := json.Marshal(value)
	return body
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
