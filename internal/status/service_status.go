package status

import (
	"strings"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	stateindex "github.com/s1liconcow/skiff/internal/index"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const RecentEventsPerService = 5

type Options struct {
	Mode        config.Mode
	Env         string
	Provider    string
	Region      string
	StateBucket string
	APIURL      string
	Service     string
	Source      string
	Freshness   Freshness
}

type Result struct {
	Mode        config.Mode `json:"mode"`
	Env         string      `json:"env,omitempty"`
	Provider    string      `json:"provider,omitempty"`
	Region      string      `json:"region,omitempty"`
	StateBucket string      `json:"state_bucket,omitempty"`
	APIURL      string      `json:"api_url,omitempty"`
	Services    []Service   `json:"services"`
	Freshness   Freshness   `json:"freshness"`
	Findings    []Finding   `json:"findings,omitempty"`
	Source      string      `json:"source"`
}

type Service struct {
	Service        string            `json:"service"`
	Env            string            `json:"env,omitempty"`
	DesiredRelease string            `json:"desired_release,omitempty"`
	StableRelease  string            `json:"stable_release,omitempty"`
	OperationID    string            `json:"operation_id,omitempty"`
	OperationKind  string            `json:"operation_kind,omitempty"`
	OperationState string            `json:"operation_state,omitempty"`
	UpdatedAt      string            `json:"updated_at,omitempty"`
	Health         string            `json:"health"`
	Operation      *Operation        `json:"operation,omitempty"`
	Rollout        *Rollout          `json:"rollout,omitempty"`
	Capacity       DependencyStatus  `json:"capacity"`
	TargetHealth   DependencyStatus  `json:"target_health"`
	Logs           DependencyStatus  `json:"logs"`
	Metrics        DependencyStatus  `json:"metrics"`
	RecentEvents   []schema.Event    `json:"recent_events,omitempty"`
	Findings       []Finding         `json:"findings,omitempty"`
	Resources      []ResourceSummary `json:"resources,omitempty"`
}

type Operation struct {
	ID                 string                        `json:"id"`
	Kind               string                        `json:"kind,omitempty"`
	State              string                        `json:"state,omitempty"`
	UpdatedAt          string                        `json:"updated_at,omitempty"`
	TraceID            string                        `json:"trace_id,omitempty"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations,omitempty"`
}

type Rollout struct {
	Status     string `json:"status"`
	ProviderID string `json:"provider_id,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

type DependencyStatus struct {
	Status     string `json:"status"`
	Source     string `json:"source,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	FreshAt    string `json:"fresh_at,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

type ResourceSummary struct {
	Kind        string `json:"kind"`
	ProviderID  string `json:"provider_id,omitempty"`
	LogicalKind string `json:"logical_kind,omitempty"`
	LogicalName string `json:"logical_name,omitempty"`
	ObservedAt  string `json:"observed_at,omitempty"`
}

type Freshness struct {
	Source           string    `json:"source"`
	Ready            bool      `json:"ready"`
	Generation       int64     `json:"generation"`
	RefreshedAt      time.Time `json:"refreshed_at,omitempty"`
	LastFullScanAt   time.Time `json:"last_full_scan_at,omitempty"`
	FreshnessSeconds int64     `json:"freshness_seconds,omitempty"`
	Findings         []Finding `json:"findings,omitempty"`
}

type Finding struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
	Key     string `json:"key,omitempty"`
}

func FromSnapshot(snapshot stateindex.Snapshot, opts Options) Result {
	freshness := opts.Freshness
	if freshness.Source == "" {
		freshness = FreshnessFromIndex(stateindex.FreshnessFromSnapshot(snapshot, time.Now().UTC(), "memory"))
	}
	services := make([]Service, 0, len(snapshot.Services))
	for _, summary := range snapshot.Services {
		if opts.Service != "" && summary.Service != opts.Service {
			continue
		}
		service := serviceFromSummary(summary)
		service.Operation = operationForService(snapshot.Operations, service)
		if service.Operation != nil {
			service.Rollout = rolloutFromOperation(service.Operation)
		}
		service.Resources = resourcesForService(snapshot.Resources, service)
		service.Capacity = dependencyForKind(service.Resources, "autoscaling-group", "capacity", "Auto Scaling Group resource is known; live capacity has not been refreshed")
		service.TargetHealth = dependencyForKind(service.Resources, "target-group", "target_health", "Target group resource is known; live target health has not been refreshed")
		service.Logs = dependencyForKind(service.Resources, "log-group", "logs", "CloudWatch log group resource is known")
		service.Metrics = dependencyForKind(service.Resources, "metric-config", "metrics", "Metric config resource is known")
		service.RecentEvents = eventsForService(snapshot.RecentEvents, service.Service, RecentEventsPerService)
		service.Findings = serviceFindings(service)
		service.Health = deriveHealth(service)
		services = append(services, service)
	}
	findings := append([]Finding(nil), freshness.Findings...)
	for _, finding := range snapshot.Findings {
		findings = append(findings, Finding{Code: finding.Code, Summary: finding.Summary, Key: finding.Key})
	}
	return Result{
		Mode:        opts.Mode,
		Env:         opts.Env,
		Provider:    opts.Provider,
		Region:      opts.Region,
		StateBucket: opts.StateBucket,
		APIURL:      opts.APIURL,
		Services:    services,
		Freshness:   freshness,
		Findings:    findings,
		Source:      opts.Source,
	}
}

func FreshnessFromIndex(in stateindex.Freshness) Freshness {
	findings := make([]Finding, 0, len(in.Findings))
	for _, finding := range in.Findings {
		findings = append(findings, Finding{Code: finding.Code, Summary: finding.Summary, Key: finding.Key})
	}
	return Freshness{
		Source:           in.Source,
		Ready:            in.Ready,
		Generation:       in.Generation,
		RefreshedAt:      in.RefreshedAt,
		LastFullScanAt:   in.LastFullScanAt,
		FreshnessSeconds: in.FreshnessSeconds,
		Findings:         findings,
	}
}

func serviceFromSummary(summary stateindex.ServiceSummary) Service {
	service := Service{
		Service:        summary.Service,
		Env:            summary.Env,
		DesiredRelease: summary.DesiredRelease,
		StableRelease:  summary.StableRelease,
		OperationID:    summary.OperationID,
		OperationKind:  summary.OperationKind,
		OperationState: summary.OperationState,
		UpdatedAt:      summary.UpdatedAt,
	}
	if summary.OperationID != "" {
		service.Operation = &Operation{ID: summary.OperationID, Kind: summary.OperationKind, State: summary.OperationState}
	}
	return service
}

func operationForService(operations []stateindex.OperationSummary, service Service) *Operation {
	if service.OperationID == "" {
		return service.Operation
	}
	for _, op := range operations {
		if op.OperationID != service.OperationID || op.Service != service.Service {
			continue
		}
		return &Operation{
			ID:                 op.OperationID,
			Kind:               service.OperationKind,
			State:              string(op.Status),
			UpdatedAt:          op.UpdatedAt,
			TraceID:            op.TraceID,
			ProviderOperations: append([]schema.ProviderOperationRef(nil), op.ProviderOperations...),
		}
	}
	return service.Operation
}

func rolloutFromOperation(operation *Operation) *Rollout {
	if operation == nil {
		return nil
	}
	rollout := &Rollout{Status: firstNonEmpty(operation.State, "unknown"), UpdatedAt: operation.UpdatedAt}
	if len(operation.ProviderOperations) > 0 {
		rollout.ProviderID = operation.ProviderOperations[0].ID
		rollout.Summary = operation.ProviderOperations[0].Description
	}
	return rollout
}

func resourcesForService(resources []stateindex.ResourceSummary, service Service) []ResourceSummary {
	out := make([]ResourceSummary, 0)
	for _, resource := range resources {
		if resource.Service != service.Service {
			continue
		}
		if service.Env != "" && resource.Env != "" && resource.Env != service.Env {
			continue
		}
		out = append(out, ResourceSummary{
			Kind:        resource.Kind,
			ProviderID:  resource.ID,
			LogicalKind: resource.LogicalKind,
			LogicalName: resource.LogicalName,
			ObservedAt:  resource.ObservedAt,
		})
	}
	return out
}

func dependencyForKind(resources []ResourceSummary, kind, source, configuredSummary string) DependencyStatus {
	for _, resource := range resources {
		if resource.Kind != kind && resource.LogicalKind != kind {
			continue
		}
		return DependencyStatus{
			Status:     "configured",
			Source:     source,
			ProviderID: resource.ProviderID,
			FreshAt:    resource.ObservedAt,
			Summary:    configuredSummary,
		}
	}
	return DependencyStatus{Status: "unknown", Source: source, Summary: "resource has not been observed in object state"}
}

func eventsForService(events []schema.Event, service string, limit int) []schema.Event {
	out := make([]schema.Event, 0, limit)
	for i := len(events) - 1; i >= 0 && len(out) < limit; i-- {
		if events[i].Subject.Kind == "service" && events[i].Subject.Name == service {
			out = append(out, stateindex.CloneEvent(events[i]))
		}
	}
	return out
}

func serviceFindings(service Service) []Finding {
	var findings []Finding
	if service.Logs.Status == "unknown" {
		findings = append(findings, Finding{Code: "LOGS_STATUS_UNKNOWN", Summary: "logs have not been observed for service"})
	}
	if service.Metrics.Status == "unknown" {
		findings = append(findings, Finding{Code: "METRICS_STATUS_UNKNOWN", Summary: "metrics have not been observed for service"})
	}
	return findings
}

func deriveHealth(service Service) string {
	state := strings.ToLower(firstNonEmpty(service.OperationState, ""))
	switch {
	case strings.Contains(state, "failed"):
		return "degraded"
	case service.OperationID != "" && state != "succeeded" && state != "canceled" && state != "cancelled":
		return "updating"
	case service.DesiredRelease == "":
		return "unknown"
	case service.Logs.Status == "unknown" || service.Metrics.Status == "unknown":
		return "degraded"
	default:
		return "nominal"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
