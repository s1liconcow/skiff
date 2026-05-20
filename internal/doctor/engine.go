package doctor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/internal/state/schema"
	servicestatus "github.com/s1liconcow/skiff/internal/status"
)

type Engine struct {
	hooks []PluginHook
}

type Option func(*Engine)

func WithPluginHook(hook PluginHook) Option {
	return func(e *Engine) {
		if hook != nil {
			e.hooks = append(e.hooks, hook)
		}
	}
}

func New(opts ...Option) *Engine {
	engine := &Engine{}
	for _, opt := range opts {
		opt(engine)
	}
	return engine
}

func Diagnose(ctx context.Context, status servicestatus.Result, opts Options) (Result, error) {
	return New().Diagnose(ctx, status, opts)
}

func (e *Engine) Diagnose(ctx context.Context, status servicestatus.Result, opts Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	builder := resultBuilder{
		result: Result{
			TraceID:   opts.TraceID,
			Service:   opts.Service,
			Env:       status.Env,
			Provider:  status.Provider,
			Region:    status.Region,
			Source:    status.Source,
			Freshness: status.Freshness,
			Health:    "nominal",
		},
		binary: firstNonEmpty(opts.Binary, "skiff"),
	}

	services := servicesForRequest(status.Services, opts.Service)
	statefulGroups := statefulGroupsForRequest(status.StatefulGroups, opts.Service)
	if opts.Service != "" && len(services) == 0 && len(statefulGroups) == 0 {
		builder.addFinding(Finding{
			ID:         findingID(opts.Service, "SERVICE_NOT_FOUND"),
			Code:       "SERVICE_NOT_FOUND",
			Severity:   SeverityHigh,
			Service:    opts.Service,
			Summary:    fmt.Sprintf("service %q was not found in object state or the skiffd index", opts.Service),
			Confidence: 0.95,
		})
		builder.addHypothesis(Hypothesis{
			ID:         hypothesisID(opts.Service, "service_not_found"),
			FindingID:  findingID(opts.Service, "SERVICE_NOT_FOUND"),
			Service:    opts.Service,
			Message:    "the service has not been deployed, the wrong environment is selected, or the cached index is stale",
			Confidence: 0.7,
		})
		builder.addAction(inspectStatusAction(builder.binary, opts.Service))
		builder.finish()
		return builder.result, nil
	}

	for _, service := range services {
		builder.addServiceFacts(service)
		builder.checkDependencies(service)
		builder.checkReleaseConvergence(service)
		builder.checkRollout(service)
		builder.checkRecentEvents(service)
		for _, hook := range e.hooks {
			findings, err := hook.Check(ctx, PluginRequest{Status: status, Service: service, TraceID: opts.TraceID})
			if err != nil {
				builder.addFinding(Finding{
					ID:         findingID(service.Service, "DOCTOR_PLUGIN_FAILED"),
					Code:       "DOCTOR_PLUGIN_FAILED",
					Severity:   SeverityLow,
					Service:    service.Service,
					Summary:    "doctor plugin hook failed: " + err.Error(),
					Confidence: 1,
				})
				continue
			}
			for _, finding := range findings {
				if finding.ID == "" {
					finding.ID = findingID(finding.Service, finding.Code)
				}
				builder.addFinding(finding)
			}
		}
	}
	for _, group := range statefulGroups {
		builder.addStatefulFacts(group)
		builder.checkStatefulGroup(group)
	}
	builder.finish()
	return builder.result, nil
}

type resultBuilder struct {
	result     Result
	binary     string
	findingIDs map[string]struct{}
	actionIDs  map[string]struct{}
}

func (b *resultBuilder) addServiceFacts(service servicestatus.Service) {
	b.addFact(Evidence{Type: "service_health", Service: service.Service, Source: "status", Message: fmt.Sprintf("%s health is %s", service.Service, firstNonEmpty(service.Health, "unknown"))})
	if service.DesiredRelease != "" {
		b.addFact(Evidence{Type: "desired_release", Service: service.Service, Source: "status", Message: "desired release is " + service.DesiredRelease})
	}
	if service.StableRelease != "" {
		b.addFact(Evidence{Type: "stable_release", Service: service.Service, Source: "status", Message: "stable release is " + service.StableRelease})
	}
	if service.Operation != nil {
		b.addFact(Evidence{Type: "operation", Service: service.Service, Source: "status", Message: fmt.Sprintf("%s operation %s is %s", service.Operation.Kind, service.Operation.ID, firstNonEmpty(service.Operation.State, "unknown")), ObservedAt: service.Operation.UpdatedAt})
	}
	addDependencyFact := func(kind string, dep servicestatus.DependencyStatus) {
		message := fmt.Sprintf("%s status is %s", kind, firstNonEmpty(dep.Status, "unknown"))
		if dep.Summary != "" {
			message += ": " + dep.Summary
		}
		b.addFact(Evidence{Type: kind, Service: service.Service, Source: firstNonEmpty(dep.Source, "status"), Message: message, ProviderID: dep.ProviderID, ObservedAt: dep.FreshAt})
	}
	addDependencyFact("capacity", service.Capacity)
	addDependencyFact("target_health", service.TargetHealth)
	if service.Database.Status != "" {
		addDependencyFact("database", service.Database)
	}
	addDependencyFact("logs", service.Logs)
	addDependencyFact("metrics", service.Metrics)
	for _, resource := range service.Resources {
		if resource.ProviderID == "" {
			continue
		}
		b.addFact(Evidence{
			Type:       "cloud_resource",
			Service:    service.Service,
			Source:     "object_state",
			Message:    fmt.Sprintf("%s provider id is %s", firstNonEmpty(resource.Kind, resource.LogicalKind), resource.ProviderID),
			ProviderID: resource.ProviderID,
			ObservedAt: resource.ObservedAt,
		})
	}
	for _, event := range service.RecentEvents {
		if event.Summary != "" {
			b.addFact(Evidence{Type: firstNonEmpty(event.Type, "event"), Service: service.Service, Source: "event", EventID: event.ID, ObservedAt: event.Time, Message: event.Summary})
		}
		for _, fact := range event.Facts {
			b.addFact(Evidence{Type: fact.Type, Service: service.Service, Source: "event", EventID: event.ID, ObservedAt: event.Time, Message: fact.Message})
		}
	}
}

func (b *resultBuilder) addStatefulFacts(group servicestatus.StatefulGroup) {
	b.addFact(Evidence{Type: "stateful_group_health", Service: group.Group, Source: "status", Message: fmt.Sprintf("%s health is %s", group.Group, firstNonEmpty(group.Health, "unknown"))})
	if group.Operation != nil {
		b.addFact(Evidence{Type: "stateful_operation", Service: group.Group, Source: "status", Message: fmt.Sprintf("%s operation %s is %s", group.Operation.Kind, group.Operation.ID, firstNonEmpty(group.Operation.State, "unknown")), ObservedAt: group.Operation.UpdatedAt})
		for _, op := range group.Operation.ProviderOperations {
			if op.ID != "" {
				b.addFact(Evidence{Type: "provider_operation", Service: group.Group, Source: "object_state", Message: fmt.Sprintf("%s %s", op.Kind, op.ID), ProviderID: op.ID, ObservedAt: op.ObservedAt})
			}
		}
	}
	if group.Lease != nil {
		b.addFact(Evidence{Type: "stateful_group_lease", Service: group.Group, Source: "object_state", Message: fmt.Sprintf("lease held by %s until %s", group.Lease.Owner, group.Lease.ExpiresAt), ObservedAt: group.UpdatedAt})
	}
	for _, member := range group.Members {
		message := fmt.Sprintf("member %d phase=%s generation=%d instance=%s volume=%s dns=%s", member.Member, firstNonEmpty(member.Phase, "unknown"), member.Generation, firstNonEmpty(member.InstanceID, "missing"), firstNonEmpty(member.VolumeID, "missing"), firstNonEmpty(member.DNSName, "missing"))
		b.addFact(Evidence{Type: "stateful_member", Service: group.Group, Source: "object_state", Message: message, ProviderID: firstNonEmpty(member.InstanceID, member.VolumeID), ObservedAt: member.UpdatedAt})
		for _, op := range member.ProviderOperations {
			if op.ID != "" {
				b.addFact(Evidence{Type: "provider_operation", Service: group.Group, Source: "object_state", Message: fmt.Sprintf("member %d %s %s", member.Member, op.Kind, op.ID), ProviderID: op.ID, ObservedAt: op.ObservedAt})
			}
		}
	}
	for _, backup := range group.Backups {
		message := fmt.Sprintf("backup %s member=%d status=%s snapshot=%s", backup.BackupID, backup.Member, firstNonEmpty(backup.Status, "unknown"), firstNonEmpty(backup.SnapshotID, "missing"))
		if backup.Stale {
			message += " stale=true"
		}
		b.addFact(Evidence{Type: "stateful_backup", Service: group.Group, Source: "object_state", Message: message, ProviderID: firstNonEmpty(backup.ProviderID, backup.SnapshotID), ObservedAt: backup.CreatedAt})
	}
	for _, event := range group.RecentEvents {
		if event.Summary != "" {
			b.addFact(Evidence{Type: firstNonEmpty(event.Type, "event"), Service: group.Group, Source: "event", EventID: event.ID, ObservedAt: event.Time, Message: event.Summary})
		}
		for _, fact := range event.Facts {
			b.addFact(Evidence{Type: fact.Type, Service: group.Group, Source: "event", EventID: event.ID, ObservedAt: event.Time, Message: fact.Message})
		}
	}
}

func (b *resultBuilder) checkStatefulGroup(group servicestatus.StatefulGroup) {
	for _, finding := range group.Findings {
		doctorFinding := Finding{
			ID:         findingID(group.Group, finding.Code),
			Code:       finding.Code,
			Severity:   severityForStatefulFinding(finding.Code),
			Service:    group.Group,
			Summary:    finding.Summary,
			Confidence: confidenceForStatefulFinding(finding.Code),
			Evidence:   statefulEvidence(group, finding.Code),
		}
		b.addFinding(doctorFinding)
		b.addStatefulHypothesis(group, doctorFinding)
	}
	b.addAction(statefulInspectAction(b.binary, group.Group))
	b.addAction(statefulLogsAction(b.binary, group.Group, -1))
	b.addAction(statefulMetricsAction(b.binary, group.Group, -1))
	if hasStatefulFinding(group, "STATEFUL_BACKUP_MISSING") || hasStatefulFinding(group, "STATEFUL_BACKUP_STALE") {
		b.addAction(statefulSnapshotAction(b.binary, group.Group, firstStatefulMember(group)))
	}
	if hasStatefulFinding(group, "STATEFUL_MEMBER_NOT_READY") || hasStatefulFinding(group, "STATEFUL_MEMBER_VOLUME_MISSING") || hasStatefulFinding(group, "STATEFUL_MEMBER_VOLUME_MISMATCH") || hasStatefulFinding(group, "STATEFUL_MEMBER_INSTANCE_MISSING") || hasStatefulFinding(group, "STATEFUL_RUNNER_STALE") || hasStatefulFinding(group, "STATEFUL_RECIPE_UNHEALTHY") || hasStatefulFinding(group, "STATEFUL_PROVIDER_DRIFT") {
		b.addAction(statefulReplaceAction(b.binary, group.Group, firstUnhealthyStatefulMember(group)))
	}
	if group.OperationID != "" && group.OperationState != string(schema.OperationSucceeded) {
		b.addAction(statefulResumeAction(b.binary, group.Group, group.OperationID))
	}
}

func (b *resultBuilder) addStatefulHypothesis(group servicestatus.StatefulGroup, finding Finding) {
	message := ""
	switch finding.Code {
	case "STATEFUL_MEMBER_NOT_READY":
		message = "the runner, recipe health check, or replacement saga may not have completed for this member"
	case "STATEFUL_MEMBER_VOLUME_MISSING", "STATEFUL_MEMBER_VOLUME_MISMATCH":
		message = "the member may be attached to the wrong durable volume or provider state may have drifted"
	case "STATEFUL_RUNNER_STALE":
		message = "the VM-local runner may still be serving an older generation than object state expects"
	case "STATEFUL_RECIPE_UNHEALTHY":
		message = "the recipe hook reported unhealthy state or failed to complete a member lifecycle step"
	case "STATEFUL_PROVIDER_DRIFT":
		message = "provider resources observed in object state no longer match the StatefulGroup control summary"
	case "STATEFUL_QUORUM_RISK":
		message = "too many members are degraded to safely preserve majority quorum"
	case "STATEFUL_MEMBER_DNS_MISSING":
		message = "stable member DNS may not have been created or Route53 state may have drifted"
	case "STATEFUL_BACKUP_MISSING", "STATEFUL_BACKUP_STALE":
		message = "restore safety is reduced until a fresh member snapshot is recorded"
	case "STATEFUL_GROUP_LEASE_HELD", "STATEFUL_MEMBER_LEASE_HELD":
		message = "another saga or interrupted operation may still own the control document lease"
	default:
		message = "stateful durable object state needs operator review"
	}
	b.addHypothesis(Hypothesis{
		ID:         hypothesisID(group.Group, strings.ToLower(finding.Code)),
		FindingID:  finding.ID,
		Service:    group.Group,
		Message:    message,
		Confidence: 0.7,
		Evidence:   finding.Evidence,
	})
}

func (b *resultBuilder) checkDependencies(service servicestatus.Service) {
	if strings.EqualFold(service.Capacity.Status, "unknown") {
		finding := Finding{
			ID:         findingID(service.Service, "CAPACITY_RESOURCE_UNKNOWN"),
			Code:       "CAPACITY_RESOURCE_UNKNOWN",
			Severity:   SeverityHigh,
			Service:    service.Service,
			Summary:    "Auto Scaling Group capacity resource has not been observed in object state",
			Confidence: 0.82,
			Evidence:   dependencyEvidence(service.Service, "capacity", service.Capacity),
		}
		b.addFinding(finding)
		b.addHypothesis(Hypothesis{
			ID:         hypothesisID(service.Service, "capacity_missing"),
			FindingID:  finding.ID,
			Service:    service.Service,
			Message:    "provider resource apply may not have completed or the service is pointed at the wrong environment",
			Confidence: 0.62,
			Evidence:   finding.Evidence,
		})
		b.addAction(inspectStatusAction(b.binary, service.Service))
		b.addAction(inspectEventsAction(b.binary, service.Service))
	}
	if strings.EqualFold(service.TargetHealth.Status, "unknown") {
		finding := Finding{
			ID:         findingID(service.Service, "TARGET_HEALTH_UNKNOWN"),
			Code:       "TARGET_HEALTH_UNKNOWN",
			Severity:   SeverityHigh,
			Service:    service.Service,
			Summary:    "target group health resource has not been observed in object state",
			Confidence: 0.78,
			Evidence:   dependencyEvidence(service.Service, "target_health", service.TargetHealth),
		}
		b.addFinding(finding)
		b.addHypothesis(Hypothesis{
			ID:         hypothesisID(service.Service, "target_group_missing"),
			FindingID:  finding.ID,
			Service:    service.Service,
			Message:    "load balancer target group wiring may be missing or misconfigured",
			Confidence: 0.66,
			Evidence:   finding.Evidence,
		})
		b.addAction(inspectStatusAction(b.binary, service.Service))
		b.addAction(inspectEventsAction(b.binary, service.Service))
	}
	if strings.EqualFold(service.Database.Status, "unknown") && serviceHasDatabaseEvidence(service) {
		finding := Finding{
			ID:         findingID(service.Service, "DATABASE_AVAILABILITY_UNKNOWN"),
			Code:       "DATABASE_AVAILABILITY_UNKNOWN",
			Severity:   SeverityHigh,
			Service:    service.Service,
			Summary:    "managed database availability has not been observed for this service",
			Confidence: 0.8,
			Evidence:   dependencyEvidence(service.Service, "database", service.Database),
		}
		b.addFinding(finding)
		b.addHypothesis(Hypothesis{
			ID:         hypothesisID(service.Service, "database_unavailable"),
			FindingID:  finding.ID,
			Service:    service.Service,
			Message:    "the managed database may not be provisioned, may be unavailable, or the service binding is missing",
			Confidence: 0.68,
			Evidence:   finding.Evidence,
		})
		b.addAction(inspectStatusAction(b.binary, service.Service))
		b.addAction(inspectEventsAction(b.binary, service.Service))
	}
	if strings.EqualFold(service.Logs.Status, "unknown") {
		finding := Finding{
			ID:         findingID(service.Service, "LOG_DELIVERY_UNAVAILABLE"),
			Code:       "LOG_DELIVERY_UNAVAILABLE",
			Severity:   SeverityMedium,
			Service:    service.Service,
			Summary:    "service logs have not been observed or configured",
			Confidence: 0.76,
			Evidence:   dependencyEvidence(service.Service, "logs", service.Logs),
		}
		b.addFinding(finding)
		b.addHypothesis(Hypothesis{
			ID:         hypothesisID(service.Service, "logs_missing"),
			FindingID:  finding.ID,
			Service:    service.Service,
			Message:    "CloudWatch log delivery or the runner log forwarder may be unavailable",
			Confidence: 0.58,
			Evidence:   finding.Evidence,
		})
		b.addAction(inspectLogsAction(b.binary, service.Service))
	}
	if strings.EqualFold(service.Metrics.Status, "unknown") {
		finding := Finding{
			ID:         findingID(service.Service, "METRIC_DELIVERY_UNAVAILABLE"),
			Code:       "METRIC_DELIVERY_UNAVAILABLE",
			Severity:   SeverityMedium,
			Service:    service.Service,
			Summary:    "service metrics have not been observed or configured",
			Confidence: 0.74,
			Evidence:   dependencyEvidence(service.Service, "metrics", service.Metrics),
		}
		b.addFinding(finding)
		b.addHypothesis(Hypothesis{
			ID:         hypothesisID(service.Service, "metrics_missing"),
			FindingID:  finding.ID,
			Service:    service.Service,
			Message:    "metric collection or CloudWatch delivery may be unavailable",
			Confidence: 0.56,
			Evidence:   finding.Evidence,
		})
		b.addAction(inspectMetricsAction(b.binary, service.Service))
	}
}

func (b *resultBuilder) checkReleaseConvergence(service servicestatus.Service) {
	if service.DesiredRelease == "" || service.StableRelease == "" || service.DesiredRelease == service.StableRelease || service.OperationID != "" {
		return
	}
	finding := Finding{
		ID:         findingID(service.Service, "DESIRED_RELEASE_NOT_STABLE"),
		Code:       "DESIRED_RELEASE_NOT_STABLE",
		Severity:   SeverityHigh,
		Service:    service.Service,
		Summary:    fmt.Sprintf("desired release %s differs from stable release %s without an active operation", service.DesiredRelease, service.StableRelease),
		Confidence: 0.72,
		Evidence: []Evidence{
			{Type: "desired_release", Service: service.Service, Source: "status", Message: "desired release is " + service.DesiredRelease},
			{Type: "stable_release", Service: service.Service, Source: "status", Message: "stable release is " + service.StableRelease},
		},
	}
	b.addFinding(finding)
	b.addHypothesis(Hypothesis{
		ID:         hypothesisID(service.Service, "release_not_converged"),
		FindingID:  finding.ID,
		Service:    service.Service,
		Message:    "a deploy may have stalled after changing desired state but before marking a stable release",
		Confidence: 0.6,
		Evidence:   finding.Evidence,
	})
	b.addAction(inspectStatusAction(b.binary, service.Service))
	b.addAction(rollbackAction(b.binary, service.Service, service.StableRelease))
}

func (b *resultBuilder) checkRollout(service servicestatus.Service) {
	state := strings.ToLower(firstNonEmpty(service.OperationState, ""))
	if !strings.Contains(state, "failed") && !strings.EqualFold(service.Health, "degraded") {
		return
	}
	if service.OperationID == "" {
		return
	}
	finding := Finding{
		ID:         findingID(service.Service, "ROLLOUT_FAILED_OR_DEGRADED"),
		Code:       "ROLLOUT_FAILED_OR_DEGRADED",
		Severity:   SeverityHigh,
		Service:    service.Service,
		Summary:    fmt.Sprintf("operation %s is %s and service health is %s", service.OperationID, firstNonEmpty(service.OperationState, "unknown"), firstNonEmpty(service.Health, "unknown")),
		Confidence: 0.84,
		Evidence: []Evidence{
			{Type: "operation", Service: service.Service, Source: "status", Message: fmt.Sprintf("operation %s state is %s", service.OperationID, firstNonEmpty(service.OperationState, "unknown"))},
			{Type: "service_health", Service: service.Service, Source: "status", Message: "service health is " + firstNonEmpty(service.Health, "unknown")},
		},
	}
	b.addFinding(finding)
	b.addHypothesis(Hypothesis{
		ID:         hypothesisID(service.Service, "rollout_failed"),
		FindingID:  finding.ID,
		Service:    service.Service,
		Message:    "the current release or its cloud rollout is failing health checks",
		Confidence: 0.78,
		Evidence:   finding.Evidence,
	})
	b.addAction(rolloutWatchAction(b.binary, service.Service, service.OperationID, providerID(service)))
	b.addAction(inspectLogsAction(b.binary, service.Service))
	if service.StableRelease != "" {
		b.addAction(rollbackAction(b.binary, service.Service, service.StableRelease))
	}
}

func (b *resultBuilder) checkRecentEvents(service servicestatus.Service) {
	for _, event := range service.RecentEvents {
		eventEvidence := evidenceFromEvent(service.Service, event)
		text := eventText(event)
		lower := strings.ToLower(text)
		if containsAny(lower, "accessdenied", "access denied", "permission denied", "not authorized", "unauthorized") && containsAny(lower, "iam", "secret", "kms", "s3", "object", "manifest") {
			finding := Finding{
				ID:         findingID(service.Service, "IAM_OR_SECRET_ACCESS_DENIED"),
				Code:       "IAM_OR_SECRET_ACCESS_DENIED",
				Severity:   SeverityHigh,
				Service:    service.Service,
				Summary:    "recent events show IAM, object-state, KMS, or secret access denied symptoms",
				Confidence: 0.86,
				Evidence:   eventEvidence,
			}
			b.addFinding(finding)
			b.addHypothesis(Hypothesis{
				ID:         hypothesisID(service.Service, "access_denied"),
				FindingID:  finding.ID,
				Service:    service.Service,
				Message:    "runner or skiffd credentials may lack required least-privilege access",
				Confidence: 0.78,
				Evidence:   eventEvidence,
			})
			b.addAction(inspectEventsAction(b.binary, service.Service))
		}
		if containsAny(lower, "runner") && containsAny(lower, "failed", "waitingforhealth", "waiting for health", "stopped", "crash", "oom") {
			finding := Finding{
				ID:         findingID(service.Service, "RUNNER_NOT_SERVING"),
				Code:       "RUNNER_NOT_SERVING",
				Severity:   SeverityHigh,
				Service:    service.Service,
				Summary:    "recent events show runner state is not Serving",
				Confidence: 0.84,
				Evidence:   eventEvidence,
			}
			b.addFinding(finding)
			b.addHypothesis(Hypothesis{
				ID:         hypothesisID(service.Service, "runner_waiting"),
				FindingID:  finding.ID,
				Service:    service.Service,
				Message:    "the workload may start but fail local health checks or crash before becoming healthy",
				Confidence: 0.76,
				Evidence:   eventEvidence,
			})
			b.addAction(inspectLogsAction(b.binary, service.Service))
		}
		if containsAny(lower, "target", "target group", "health check") && containsAny(lower, "unhealthy", "misconfigured", "mismatch", "timeout", "500", "failed") {
			finding := Finding{
				ID:         findingID(service.Service, "TARGET_HEALTH_UNHEALTHY"),
				Code:       "TARGET_HEALTH_UNHEALTHY",
				Severity:   SeverityHigh,
				Service:    service.Service,
				Summary:    "recent events show unhealthy or misconfigured target health",
				Confidence: 0.83,
				Evidence:   eventEvidence,
			}
			b.addFinding(finding)
			b.addHypothesis(Hypothesis{
				ID:         hypothesisID(service.Service, "target_health_bad"),
				FindingID:  finding.ID,
				Service:    service.Service,
				Message:    "load balancer health checks may not match the workload listener or health endpoint",
				Confidence: 0.72,
				Evidence:   eventEvidence,
			})
			b.addAction(rolloutWatchAction(b.binary, service.Service, service.OperationID, providerID(service)))
			b.addAction(inspectLogsAction(b.binary, service.Service))
		}
		if containsAny(lower, "metric", "gate", "slo") && containsAny(lower, "failed", "breach", "above", "below", "threshold") {
			finding := Finding{
				ID:         findingID(service.Service, "METRIC_GATE_FAILED"),
				Code:       "METRIC_GATE_FAILED",
				Severity:   SeverityHigh,
				Service:    service.Service,
				Summary:    "recent events show a metric gate failure",
				Confidence: 0.8,
				Evidence:   eventEvidence,
			}
			b.addFinding(finding)
			b.addHypothesis(Hypothesis{
				ID:         hypothesisID(service.Service, "metric_gate_failed"),
				FindingID:  finding.ID,
				Service:    service.Service,
				Message:    "the current release may be violating an operational metric threshold",
				Confidence: 0.72,
				Evidence:   eventEvidence,
			})
			b.addAction(inspectMetricsAction(b.binary, service.Service))
		}
		if containsAny(lower, "log", "stderr", "application") && containsAny(lower, "panic", "exception", "error", "5xx", "oom", "crash") {
			finding := Finding{
				ID:         findingID(service.Service, "RECENT_BAD_LOGS"),
				Code:       "RECENT_BAD_LOGS",
				Severity:   SeverityHigh,
				Service:    service.Service,
				Summary:    "recent events mention severe application log symptoms",
				Confidence: 0.74,
				Evidence:   eventEvidence,
			}
			b.addFinding(finding)
			b.addHypothesis(Hypothesis{
				ID:         hypothesisID(service.Service, "bad_release_logs"),
				FindingID:  finding.ID,
				Service:    service.Service,
				Message:    "the release may be starting but failing at runtime",
				Confidence: 0.68,
				Evidence:   eventEvidence,
			})
			b.addAction(inspectLogsAction(b.binary, service.Service))
		}
		if containsAny(lower, "capacity", "autoscaling", "asg") && containsAny(lower, "mismatch", "zero", "0 in-service", "insufficient", "failed") {
			finding := Finding{
				ID:         findingID(service.Service, "CAPACITY_MISMATCH"),
				Code:       "CAPACITY_MISMATCH",
				Severity:   SeverityHigh,
				Service:    service.Service,
				Summary:    "recent events show desired capacity does not match serving capacity",
				Confidence: 0.8,
				Evidence:   eventEvidence,
			}
			b.addFinding(finding)
			b.addHypothesis(Hypothesis{
				ID:         hypothesisID(service.Service, "capacity_mismatch"),
				FindingID:  finding.ID,
				Service:    service.Service,
				Message:    "instances may not be launching, joining the target group, or passing health checks",
				Confidence: 0.68,
				Evidence:   eventEvidence,
			})
			b.addAction(inspectStatusAction(b.binary, service.Service))
		}
		if containsAny(lower, "secret", "credential") && containsAny(lower, "stale", "old version", "previous version", "version mismatch", "not rolled", "still using") {
			finding := Finding{
				ID:         findingID(service.Service, "SECRET_CONSUMER_STALE"),
				Code:       "SECRET_CONSUMER_STALE",
				Severity:   SeverityHigh,
				Service:    service.Service,
				Summary:    "recent events show a consumer may still be using an old secret or credential version",
				Confidence: 0.82,
				Evidence:   eventEvidence,
			}
			b.addFinding(finding)
			b.addHypothesis(Hypothesis{
				ID:         hypothesisID(service.Service, "secret_consumer_stale"),
				FindingID:  finding.ID,
				Service:    service.Service,
				Message:    "the secret pointer may have promoted before all consumers rolled, or a consumer may cache credentials too long",
				Confidence: 0.72,
				Evidence:   eventEvidence,
			})
			b.addAction(inspectEventsAction(b.binary, service.Service))
			b.addAction(inspectLogsAction(b.binary, service.Service))
		}
		if containsAny(lower, "secret", "credential", "rotation") && containsAny(lower, "canary failed", "rotation failed", "restore previous", "restored previous", "disable failed") {
			finding := Finding{
				ID:         findingID(service.Service, "SECRET_ROTATION_FAILED"),
				Code:       "SECRET_ROTATION_FAILED",
				Severity:   SeverityHigh,
				Service:    service.Service,
				Summary:    "recent events show a secret rotation failed or restored the previous version",
				Confidence: 0.84,
				Evidence:   eventEvidence,
			}
			b.addFinding(finding)
			b.addHypothesis(Hypothesis{
				ID:         hypothesisID(service.Service, "secret_rotation_failed"),
				FindingID:  finding.ID,
				Service:    service.Service,
				Message:    "the canary consumer may have rejected the new version, or credential disable scheduling may have failed",
				Confidence: 0.74,
				Evidence:   eventEvidence,
			})
			b.addAction(inspectEventsAction(b.binary, service.Service))
			b.addAction(inspectLogsAction(b.binary, service.Service))
		}
		if containsAny(lower, "certificate", "cert", "tls", "mtls") && containsAny(lower, "expired", "expires", "expiring", "near expiry", "notafter") {
			severity := SeverityMedium
			code := "CERTIFICATE_EXPIRING"
			summary := "recent events show a certificate is nearing expiry"
			if containsAny(lower, "expired", "not valid") {
				severity = SeverityCritical
				code = "CERTIFICATE_EXPIRED"
				summary = "recent events show a certificate is expired"
			}
			finding := Finding{
				ID:         findingID(service.Service, code),
				Code:       code,
				Severity:   severity,
				Service:    service.Service,
				Summary:    summary,
				Confidence: 0.86,
				Evidence:   eventEvidence,
			}
			b.addFinding(finding)
			b.addHypothesis(Hypothesis{
				ID:         hypothesisID(service.Service, "certificate_expiry"),
				FindingID:  finding.ID,
				Service:    service.Service,
				Message:    "certificate rotation may be needed before consumers reject the workload certificate",
				Confidence: 0.76,
				Evidence:   eventEvidence,
			})
			b.addAction(inspectEventsAction(b.binary, service.Service))
		}
		if containsAny(lower, "key", "kms", "policy") && containsAny(lower, "policy mismatch", "least privilege", "overbroad", "wrong principal", "missing grant", "access denied") {
			finding := Finding{
				ID:         findingID(service.Service, "KEY_POLICY_MISMATCH"),
				Code:       "KEY_POLICY_MISMATCH",
				Severity:   SeverityHigh,
				Service:    service.Service,
				Summary:    "recent events show a key policy or grant mismatch",
				Confidence: 0.82,
				Evidence:   eventEvidence,
			}
			b.addFinding(finding)
			b.addHypothesis(Hypothesis{
				ID:         hypothesisID(service.Service, "key_policy_mismatch"),
				FindingID:  finding.ID,
				Service:    service.Service,
				Message:    "the runner, skiffd, or a workload role may not have the least-privilege key permissions needed after rotation",
				Confidence: 0.72,
				Evidence:   eventEvidence,
			})
			b.addAction(inspectEventsAction(b.binary, service.Service))
		}
	}
}

func (b *resultBuilder) addFact(evidence Evidence) {
	if evidence.Message == "" {
		return
	}
	b.result.Facts = append(b.result.Facts, evidence)
}

func (b *resultBuilder) addFinding(finding Finding) {
	if finding.Code == "" {
		return
	}
	if finding.ID == "" {
		finding.ID = findingID(finding.Service, finding.Code)
	}
	if b.findingIDs == nil {
		b.findingIDs = make(map[string]struct{})
	}
	if _, exists := b.findingIDs[finding.ID]; exists {
		return
	}
	b.findingIDs[finding.ID] = struct{}{}
	if finding.Confidence == 0 {
		finding.Confidence = 0.5
	}
	b.result.Findings = append(b.result.Findings, finding)
}

func (b *resultBuilder) addHypothesis(hypothesis Hypothesis) {
	if hypothesis.Message == "" {
		return
	}
	if hypothesis.ID == "" {
		hypothesis.ID = hypothesisID(hypothesis.Service, hypothesis.Message)
	}
	if hypothesis.Confidence == 0 {
		hypothesis.Confidence = 0.5
	}
	b.result.Hypotheses = append(b.result.Hypotheses, hypothesis)
}

func (b *resultBuilder) addAction(action RecommendedAction) {
	if action.ID == "" {
		return
	}
	if b.actionIDs == nil {
		b.actionIDs = make(map[string]struct{})
	}
	if _, exists := b.actionIDs[action.ID]; exists {
		return
	}
	b.actionIDs[action.ID] = struct{}{}
	b.result.RecommendedActions = append(b.result.RecommendedActions, action)
}

func (b *resultBuilder) finish() {
	sort.SliceStable(b.result.Facts, func(i, j int) bool {
		return evidenceLess(b.result.Facts[i], b.result.Facts[j])
	})
	sort.SliceStable(b.result.Findings, func(i, j int) bool {
		left, right := b.result.Findings[i], b.result.Findings[j]
		if severityRank(left.Severity) != severityRank(right.Severity) {
			return severityRank(left.Severity) > severityRank(right.Severity)
		}
		if left.Confidence != right.Confidence {
			return left.Confidence > right.Confidence
		}
		if left.Service != right.Service {
			return left.Service < right.Service
		}
		return left.Code < right.Code
	})
	sort.SliceStable(b.result.Hypotheses, func(i, j int) bool {
		left, right := b.result.Hypotheses[i], b.result.Hypotheses[j]
		if left.Confidence != right.Confidence {
			return left.Confidence > right.Confidence
		}
		if left.Service != right.Service {
			return left.Service < right.Service
		}
		return left.ID < right.ID
	})
	sort.SliceStable(b.result.RecommendedActions, func(i, j int) bool {
		left, right := b.result.RecommendedActions[i], b.result.RecommendedActions[j]
		if left.Mutating != right.Mutating {
			return !left.Mutating
		}
		if left.Service != right.Service {
			return left.Service < right.Service
		}
		return left.ID < right.ID
	})
	b.result.Health = doctorHealth(b.result.Findings)
}

func servicesForRequest(services []servicestatus.Service, service string) []servicestatus.Service {
	out := make([]servicestatus.Service, 0, len(services))
	for _, item := range services {
		if service == "" || item.Service == service {
			out = append(out, item)
		}
	}
	return out
}

func statefulGroupsForRequest(groups []servicestatus.StatefulGroup, service string) []servicestatus.StatefulGroup {
	out := make([]servicestatus.StatefulGroup, 0, len(groups))
	for _, item := range groups {
		if service == "" || item.Group == service {
			out = append(out, item)
		}
	}
	return out
}

func dependencyEvidence(service, kind string, dep servicestatus.DependencyStatus) []Evidence {
	message := fmt.Sprintf("%s status is %s", kind, firstNonEmpty(dep.Status, "unknown"))
	if dep.Summary != "" {
		message += ": " + dep.Summary
	}
	return []Evidence{{
		Type:       kind,
		Service:    service,
		Source:     firstNonEmpty(dep.Source, "status"),
		Message:    message,
		ProviderID: dep.ProviderID,
		ObservedAt: dep.FreshAt,
	}}
}

func severityForStatefulFinding(code string) Severity {
	switch code {
	case "STATEFUL_MEMBER_NOT_READY", "STATEFUL_MEMBER_VOLUME_MISSING", "STATEFUL_MEMBER_VOLUME_MISMATCH", "STATEFUL_MEMBER_INSTANCE_MISSING", "STATEFUL_BACKUP_STALE", "STATEFUL_RUNNER_STALE", "STATEFUL_RECIPE_UNHEALTHY", "STATEFUL_QUORUM_RISK", "STATEFUL_PROVIDER_DRIFT":
		return SeverityHigh
	case "STATEFUL_BACKUP_MISSING", "STATEFUL_MEMBER_DNS_MISSING", "STATEFUL_GROUP_LEASE_HELD", "STATEFUL_MEMBER_LEASE_HELD":
		return SeverityMedium
	default:
		return SeverityLow
	}
}

func confidenceForStatefulFinding(code string) float64 {
	switch code {
	case "STATEFUL_MEMBER_VOLUME_MISSING", "STATEFUL_MEMBER_VOLUME_MISMATCH", "STATEFUL_BACKUP_STALE", "STATEFUL_RUNNER_STALE", "STATEFUL_PROVIDER_DRIFT":
		return 0.9
	case "STATEFUL_MEMBER_NOT_READY", "STATEFUL_MEMBER_INSTANCE_MISSING", "STATEFUL_BACKUP_MISSING", "STATEFUL_RECIPE_UNHEALTHY", "STATEFUL_QUORUM_RISK":
		return 0.84
	default:
		return 0.72
	}
}

func statefulEvidence(group servicestatus.StatefulGroup, code string) []Evidence {
	var evidence []Evidence
	evidence = append(evidence, Evidence{Type: "stateful_group", Service: group.Group, Source: "status", Message: fmt.Sprintf("%s health is %s", group.Group, firstNonEmpty(group.Health, "unknown"))})
	for _, member := range group.Members {
		switch code {
		case "STATEFUL_MEMBER_NOT_READY", "STATEFUL_MEMBER_VOLUME_MISSING", "STATEFUL_MEMBER_VOLUME_MISMATCH", "STATEFUL_MEMBER_INSTANCE_MISSING", "STATEFUL_MEMBER_DNS_MISSING", "STATEFUL_MEMBER_LEASE_HELD", "STATEFUL_RUNNER_STALE", "STATEFUL_RECIPE_UNHEALTHY", "STATEFUL_PROVIDER_DRIFT", "STATEFUL_QUORUM_RISK":
			evidence = append(evidence, Evidence{Type: "stateful_member", Service: group.Group, Source: "object_state", Message: fmt.Sprintf("member %d phase=%s generation=%d expected_generation=%d instance=%s expected_instance=%s volume=%s expected_volume=%s dns=%s expected_dns=%s", member.Member, firstNonEmpty(member.Phase, "unknown"), member.Generation, member.ExpectedGeneration, firstNonEmpty(member.InstanceID, "missing"), firstNonEmpty(member.ExpectedInstanceID, "unknown"), firstNonEmpty(member.VolumeID, "missing"), firstNonEmpty(member.ExpectedVolumeID, "unknown"), firstNonEmpty(member.DNSName, "missing"), firstNonEmpty(member.ExpectedDNSName, "unknown")), ProviderID: firstNonEmpty(member.InstanceID, member.VolumeID), ObservedAt: member.UpdatedAt})
		}
	}
	for _, backup := range group.Backups {
		if strings.HasPrefix(code, "STATEFUL_BACKUP") || code == "STATEFUL_RECIPE_UNHEALTHY" {
			evidence = append(evidence, Evidence{Type: "stateful_backup", Service: group.Group, Source: "object_state", Message: fmt.Sprintf("backup %s member=%d status=%s stale=%t", backup.BackupID, backup.Member, firstNonEmpty(backup.Status, "unknown"), backup.Stale), ProviderID: firstNonEmpty(backup.ProviderID, backup.SnapshotID), ObservedAt: backup.CreatedAt})
		}
	}
	return evidence
}

func hasStatefulFinding(group servicestatus.StatefulGroup, code string) bool {
	for _, finding := range group.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func firstStatefulMember(group servicestatus.StatefulGroup) int {
	if len(group.Members) == 0 {
		return 0
	}
	return group.Members[0].Member
}

func firstUnhealthyStatefulMember(group servicestatus.StatefulGroup) int {
	for _, member := range group.Members {
		if member.Health != "" && member.Health != "nominal" {
			return member.Member
		}
	}
	return firstStatefulMember(group)
}

func evidenceFromEvent(service string, event schema.Event) []Evidence {
	out := make([]Evidence, 0, 1+len(event.Facts))
	if event.Summary != "" {
		out = append(out, Evidence{Type: firstNonEmpty(event.Type, "event"), Service: service, Source: "event", EventID: event.ID, ObservedAt: event.Time, Message: event.Summary})
	}
	for _, fact := range event.Facts {
		out = append(out, Evidence{Type: fact.Type, Service: service, Source: "event", EventID: event.ID, ObservedAt: event.Time, Message: fact.Message})
	}
	return out
}

func eventText(event schema.Event) string {
	var parts []string
	parts = append(parts, event.Type, event.Severity, event.Summary)
	for _, fact := range event.Facts {
		parts = append(parts, fact.Type, fact.Message)
	}
	return strings.Join(parts, " ")
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func providerID(service servicestatus.Service) string {
	if service.Rollout != nil && service.Rollout.ProviderID != "" {
		return service.Rollout.ProviderID
	}
	if service.Operation != nil {
		for _, op := range service.Operation.ProviderOperations {
			if op.ID != "" {
				return op.ID
			}
		}
	}
	return ""
}

func serviceHasDatabaseEvidence(service servicestatus.Service) bool {
	for _, resource := range service.Resources {
		if resource.Kind == "rds-db-instance" || resource.LogicalKind == "rds-db-instance" {
			return true
		}
		if resource.Kind == "secretsmanager-secret" || resource.LogicalKind == "secretsmanager-secret" {
			return true
		}
	}
	for _, event := range service.RecentEvents {
		text := strings.ToLower(eventText(event))
		if containsAny(text, "database", "postgres", "mysql", "rds") {
			return true
		}
	}
	return false
}

func inspectStatusAction(binary, service string) RecommendedAction {
	return RecommendedAction{
		ID:       actionID(service, "inspect_status"),
		Kind:     "command",
		Service:  service,
		Summary:  "refresh service status from object state",
		Command:  fmt.Sprintf("%s status %s --fresh --format json", binary, service),
		Mutating: false,
	}
}

func inspectEventsAction(binary, service string) RecommendedAction {
	return RecommendedAction{
		ID:       actionID(service, "inspect_events"),
		Kind:     "command",
		Service:  service,
		Summary:  "inspect recent service events",
		Command:  fmt.Sprintf("%s ops events %s --limit 20 --fresh --format json", binary, service),
		Mutating: false,
	}
}

func inspectLogsAction(binary, service string) RecommendedAction {
	return RecommendedAction{
		ID:       actionID(service, "inspect_logs"),
		Kind:     "command",
		Service:  service,
		Summary:  "inspect recent service logs",
		Command:  fmt.Sprintf("%s logs %s --since 20m --format json", binary, service),
		Mutating: false,
	}
}

func inspectMetricsAction(binary, service string) RecommendedAction {
	return RecommendedAction{
		ID:       actionID(service, "inspect_metrics"),
		Kind:     "command",
		Service:  service,
		Summary:  "inspect recent service metrics",
		Command:  fmt.Sprintf("%s metrics %s --since 20m --format json", binary, service),
		Mutating: false,
	}
}

func statefulInspectAction(binary, group string) RecommendedAction {
	return RecommendedAction{
		ID:       actionID(group, "stateful_status"),
		Kind:     "command",
		Service:  group,
		Summary:  "refresh StatefulGroup status from object state",
		Command:  fmt.Sprintf("%s stateful status %s --fresh --format json", binary, group),
		Mutating: false,
	}
}

func statefulLogsAction(binary, group string, member int) RecommendedAction {
	command := fmt.Sprintf("%s stateful logs %s --since 20m --format json", binary, group)
	id := "stateful_logs"
	if member >= 0 {
		command = fmt.Sprintf("%s stateful logs %s --member %d --since 20m --format json", binary, group, member)
		id = fmt.Sprintf("stateful_logs_member_%d", member)
	}
	return RecommendedAction{
		ID:       actionID(group, id),
		Kind:     "command",
		Service:  group,
		Summary:  "inspect recent StatefulGroup member logs",
		Command:  command,
		Mutating: false,
	}
}

func statefulMetricsAction(binary, group string, member int) RecommendedAction {
	command := fmt.Sprintf("%s stateful metrics %s --since 20m --format json", binary, group)
	id := "stateful_metrics"
	if member >= 0 {
		command = fmt.Sprintf("%s stateful metrics %s --member %d --since 20m --format json", binary, group, member)
		id = fmt.Sprintf("stateful_metrics_member_%d", member)
	}
	return RecommendedAction{
		ID:       actionID(group, id),
		Kind:     "command",
		Service:  group,
		Summary:  "inspect recent StatefulGroup member metrics",
		Command:  command,
		Mutating: false,
	}
}

func statefulSnapshotAction(binary, group string, member int) RecommendedAction {
	return RecommendedAction{
		ID:            actionID(group, "stateful_snapshot_member"),
		Kind:          "command",
		Service:       group,
		Summary:       "create a fresh stateful member snapshot before restore or replacement work",
		Command:       fmt.Sprintf("%s stateful snapshot %s --member %d --format json", binary, group, member),
		Mutating:      true,
		Safety:        "create-only backup intent and snapshot record; does not delete or overwrite volumes",
		Reversibility: schema.Compensatable,
		Risk:          schema.RiskMedium,
	}
}

func statefulReplaceAction(binary, group string, member int) RecommendedAction {
	return RecommendedAction{
		ID:               actionID(group, "stateful_replace_member"),
		Kind:             "command",
		Service:          group,
		Summary:          "replace one unhealthy StatefulGroup member through the explicit saga",
		Command:          fmt.Sprintf("%s ops run %s replace-member --member %d --yes --format json", binary, group, member),
		Mutating:         true,
		Safety:           "requires explicit member fencing before volume attach",
		Reversibility:    schema.Compensatable,
		Risk:             schema.RiskHigh,
		RequiresApproval: true,
	}
}

func statefulResumeAction(binary, group, operationID string) RecommendedAction {
	if operationID == "" {
		return RecommendedAction{}
	}
	return RecommendedAction{
		ID:            actionID(group, "stateful_resume"),
		Kind:          "command",
		Service:       group,
		Summary:       "resume the active StatefulGroup saga or inspect it if the operation ID is not a saga ID",
		Command:       fmt.Sprintf("%s stateful resume %s --format json", binary, operationID),
		Mutating:      true,
		Safety:        "resumes from durable saga/control state; does not start a new operation",
		Reversibility: schema.Compensatable,
		Risk:          schema.RiskMedium,
	}
}

func rolloutWatchAction(binary, service, operationID, rolloutProviderID string) RecommendedAction {
	if operationID == "" {
		return RecommendedAction{}
	}
	command := fmt.Sprintf("%s ops watch %s --operation %s --format json", binary, service, operationID)
	return RecommendedAction{
		ID:       actionID(service, "watch_rollout"),
		Kind:     "command",
		Service:  service,
		Summary:  "watch the provider rollout and persist updated operation state",
		Command:  command,
		Mutating: true,
		Safety:   "low risk; writes operation status/events after reading provider rollout state",
		Risk:     schema.RiskLow,
	}
}

func rollbackAction(binary, service, stableRelease string) RecommendedAction {
	if stableRelease == "" {
		return RecommendedAction{}
	}
	return RecommendedAction{
		ID:               actionID(service, "rollback_to_stable"),
		Kind:             "command",
		Service:          service,
		Summary:          "return desired state to the last stable release",
		Command:          fmt.Sprintf("%s rollback %s --to %s --yes --format json", binary, service, stableRelease),
		Mutating:         true,
		Safety:           "prefer this before destructive repair when a stable release exists",
		Reversibility:    schema.Reversible,
		Risk:             schema.RiskMedium,
		RequiresApproval: false,
	}
}

func findingID(service, code string) string {
	return compactID(service, strings.ToLower(code))
}

func hypothesisID(service, value string) string {
	return compactID(service, "hypothesis_"+strings.ToLower(value))
}

func actionID(service, value string) string {
	return compactID(service, value)
}

func compactID(service, value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "_", "-", "_", ":", "_", "/", "_", ".", "_", "__", "_")
	value = replacer.Replace(value)
	if service == "" {
		return value
	}
	return strings.Trim(strings.ToLower(service)+"_"+value, "_")
}

func evidenceLess(left, right Evidence) bool {
	for _, cmp := range []int{
		strings.Compare(left.Service, right.Service),
		strings.Compare(left.Type, right.Type),
		strings.Compare(left.Source, right.Source),
		strings.Compare(left.EventID, right.EventID),
		strings.Compare(left.Message, right.Message),
	} {
		if cmp != 0 {
			return cmp < 0
		}
	}
	return left.ObservedAt < right.ObservedAt
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

func doctorHealth(findings []Finding) string {
	health := "nominal"
	for _, finding := range findings {
		switch finding.Severity {
		case SeverityCritical:
			return "critical"
		case SeverityHigh:
			health = "degraded"
		case SeverityMedium:
			if health == "nominal" {
				health = "warning"
			}
		}
	}
	return health
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
