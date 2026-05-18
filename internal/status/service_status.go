package status

import (
	"strconv"
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
	Now         time.Time
}

type Result struct {
	Mode           config.Mode     `json:"mode"`
	Env            string          `json:"env,omitempty"`
	Provider       string          `json:"provider,omitempty"`
	Region         string          `json:"region,omitempty"`
	StateBucket    string          `json:"state_bucket,omitempty"`
	APIURL         string          `json:"api_url,omitempty"`
	Services       []Service       `json:"services"`
	StatefulGroups []StatefulGroup `json:"stateful_groups,omitempty"`
	Freshness      Freshness       `json:"freshness"`
	Findings       []Finding       `json:"findings,omitempty"`
	Source         string          `json:"source"`
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
	Database       DependencyStatus  `json:"database,omitempty"`
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

type StatefulGroup struct {
	Group          string           `json:"group"`
	Env            string           `json:"env,omitempty"`
	Replicas       int              `json:"replicas"`
	OperationID    string           `json:"operation_id,omitempty"`
	OperationKind  string           `json:"operation_kind,omitempty"`
	OperationState string           `json:"operation_state,omitempty"`
	UpdatedAt      string           `json:"updated_at,omitempty"`
	Health         string           `json:"health"`
	Operation      *Operation       `json:"operation,omitempty"`
	Lease          *schema.Lease    `json:"lease,omitempty"`
	Members        []StatefulMember `json:"members,omitempty"`
	Backups        []StatefulBackup `json:"backups,omitempty"`
	RecentEvents   []schema.Event   `json:"recent_events,omitempty"`
	Findings       []Finding        `json:"findings,omitempty"`
}

type StatefulMember struct {
	Member             int                           `json:"member"`
	Env                string                        `json:"env,omitempty"`
	Zone               string                        `json:"zone,omitempty"`
	Generation         int64                         `json:"generation"`
	ExpectedGeneration int64                         `json:"expected_generation,omitempty"`
	ReleaseID          string                        `json:"release_id,omitempty"`
	ReleaseManifestKey string                        `json:"release_manifest_key,omitempty"`
	RuntimeManifestKey string                        `json:"runtime_manifest_key,omitempty"`
	InstanceID         string                        `json:"instance_id,omitempty"`
	ExpectedInstanceID string                        `json:"expected_instance_id,omitempty"`
	VolumeID           string                        `json:"volume_id,omitempty"`
	ExpectedVolumeID   string                        `json:"expected_volume_id,omitempty"`
	DNSName            string                        `json:"dns_name,omitempty"`
	ExpectedDNSName    string                        `json:"expected_dns_name,omitempty"`
	Phase              string                        `json:"phase,omitempty"`
	ExpectedPhase      string                        `json:"expected_phase,omitempty"`
	Role               string                        `json:"role,omitempty"`
	RecipeStatus       string                        `json:"recipe_status,omitempty"`
	RecipeSummary      string                        `json:"recipe_summary,omitempty"`
	Health             string                        `json:"health"`
	Lease              *schema.Lease                 `json:"lease,omitempty"`
	ProviderOperations []schema.ProviderOperationRef `json:"provider_operations,omitempty"`
	UpdatedAt          string                        `json:"updated_at,omitempty"`
	Findings           []Finding                     `json:"findings,omitempty"`
}

type StatefulBackup struct {
	BackupID          string                      `json:"backup_id"`
	Member            int                         `json:"member"`
	VolumeID          string                      `json:"volume_id,omitempty"`
	SnapshotID        string                      `json:"snapshot_id,omitempty"`
	Provider          string                      `json:"provider,omitempty"`
	ProviderID        string                      `json:"provider_id,omitempty"`
	ProviderOperation schema.ProviderOperationRef `json:"provider_operation,omitempty"`
	Status            string                      `json:"status,omitempty"`
	RecipeStatus      string                      `json:"recipe_status,omitempty"`
	RecipeSummary     string                      `json:"recipe_summary,omitempty"`
	CreatedAt         string                      `json:"created_at,omitempty"`
	ExpiresAt         string                      `json:"expires_at,omitempty"`
	Stale             bool                        `json:"stale,omitempty"`
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
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	freshness := opts.Freshness
	if freshness.Source == "" {
		freshness = FreshnessFromIndex(stateindex.FreshnessFromSnapshot(snapshot, now, "memory"))
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
		service.Database = databaseDependency(service.Resources)
		service.Logs = dependencyForKind(service.Resources, "log-group", "logs", "CloudWatch log group resource is known")
		service.Metrics = dependencyForKind(service.Resources, "metric-config", "metrics", "Metric config resource is known")
		service.RecentEvents = eventsForService(snapshot.RecentEvents, service.Service, RecentEventsPerService)
		service.Findings = serviceFindings(service)
		service.Health = deriveHealth(service)
		services = append(services, service)
	}
	statefulGroups := make([]StatefulGroup, 0, len(snapshot.StatefulGroups))
	for _, summary := range snapshot.StatefulGroups {
		if opts.Service != "" && summary.Group != opts.Service {
			continue
		}
		group := statefulGroupFromSummary(summary, snapshot.Operations, snapshot.RecentEvents, now)
		statefulGroups = append(statefulGroups, group)
	}
	findings := append([]Finding(nil), freshness.Findings...)
	for _, finding := range snapshot.Findings {
		findings = append(findings, Finding{Code: finding.Code, Summary: finding.Summary, Key: finding.Key})
	}
	return Result{
		Mode:           opts.Mode,
		Env:            opts.Env,
		Provider:       opts.Provider,
		Region:         opts.Region,
		StateBucket:    opts.StateBucket,
		APIURL:         opts.APIURL,
		Services:       services,
		StatefulGroups: statefulGroups,
		Freshness:      freshness,
		Findings:       findings,
		Source:         opts.Source,
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

func statefulGroupFromSummary(summary stateindex.StatefulGroupSummary, operations []stateindex.OperationSummary, events []schema.Event, now time.Time) StatefulGroup {
	group := StatefulGroup{
		Group:          summary.Group,
		Env:            summary.Env,
		Replicas:       summary.Replicas,
		OperationID:    summary.OperationID,
		OperationKind:  summary.OperationKind,
		OperationState: summary.OperationState,
		UpdatedAt:      summary.UpdatedAt,
		Lease:          cloneLease(summary.Lease),
		RecentEvents:   eventsForService(events, summary.Group, RecentEventsPerService),
	}
	if summary.OperationID != "" {
		group.Operation = operationForStatefulGroup(operations, summary)
	}
	group.Members = make([]StatefulMember, 0, len(summary.Members))
	for _, member := range summary.Members {
		out := StatefulMember{
			Member:             member.Member,
			Env:                firstNonEmpty(member.Env, summary.Env),
			Zone:               member.Zone,
			Generation:         member.Generation,
			ExpectedGeneration: member.ExpectedGeneration,
			ReleaseID:          member.ReleaseID,
			ReleaseManifestKey: member.ReleaseManifestKey,
			RuntimeManifestKey: member.RuntimeManifestKey,
			InstanceID:         member.InstanceID,
			ExpectedInstanceID: member.ExpectedInstanceID,
			VolumeID:           member.VolumeID,
			ExpectedVolumeID:   member.ExpectedVolumeID,
			DNSName:            member.DNSName,
			ExpectedDNSName:    member.ExpectedDNSName,
			Phase:              member.Phase,
			ExpectedPhase:      member.ExpectedPhase,
			Role:               member.Role,
			RecipeStatus:       member.RecipeStatus,
			RecipeSummary:      member.RecipeSummary,
			Lease:              cloneLease(member.Lease),
			ProviderOperations: append([]schema.ProviderOperationRef(nil), member.ProviderOperations...),
			UpdatedAt:          member.UpdatedAt,
		}
		out.Findings = statefulMemberFindings(summary, member)
		out.Health = deriveStatefulMemberHealth(out)
		group.Members = append(group.Members, out)
	}
	group.Backups = make([]StatefulBackup, 0, len(summary.Backups))
	backupsByMember := map[int][]StatefulBackup{}
	for _, backup := range summary.Backups {
		out := StatefulBackup{
			BackupID:          backup.BackupID,
			Member:            backup.Member,
			VolumeID:          backup.VolumeID,
			SnapshotID:        backup.SnapshotID,
			Provider:          backup.Provider,
			ProviderID:        backup.ProviderID,
			ProviderOperation: backup.ProviderOperation,
			Status:            backup.Status,
			RecipeStatus:      backup.RecipeStatus,
			RecipeSummary:     backup.RecipeSummary,
			CreatedAt:         backup.CreatedAt,
			ExpiresAt:         backup.ExpiresAt,
			Stale:             backupStale(backup.ExpiresAt, now),
		}
		group.Backups = append(group.Backups, out)
		backupsByMember[out.Member] = append(backupsByMember[out.Member], out)
	}
	group.Findings = statefulGroupFindings(group, backupsByMember)
	group.Health = deriveStatefulGroupHealth(group)
	return group
}

func operationForStatefulGroup(operations []stateindex.OperationSummary, group stateindex.StatefulGroupSummary) *Operation {
	if group.OperationID == "" {
		return nil
	}
	for _, op := range operations {
		if op.OperationID != group.OperationID || op.Service != group.Group {
			continue
		}
		return &Operation{
			ID:                 op.OperationID,
			Kind:               group.OperationKind,
			State:              string(op.Status),
			UpdatedAt:          op.UpdatedAt,
			TraceID:            op.TraceID,
			ProviderOperations: append([]schema.ProviderOperationRef(nil), op.ProviderOperations...),
		}
	}
	return &Operation{ID: group.OperationID, Kind: group.OperationKind, State: group.OperationState}
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

func databaseDependency(resources []ResourceSummary) DependencyStatus {
	for _, resource := range resources {
		if resource.Kind == "rds-db-instance" || resource.LogicalKind == "rds-db-instance" {
			return DependencyStatus{
				Status:     "configured",
				Source:     "database",
				ProviderID: resource.ProviderID,
				FreshAt:    resource.ObservedAt,
				Summary:    "RDS managed database resource is known; live availability has not been refreshed",
			}
		}
	}
	for _, resource := range resources {
		if resource.Kind == "secretsmanager-secret" || resource.LogicalKind == "secretsmanager-secret" {
			return DependencyStatus{
				Status:     "unknown",
				Source:     "database",
				ProviderID: resource.ProviderID,
				FreshAt:    resource.ObservedAt,
				Summary:    "database connection secret is known but database resource has not been observed in object state",
			}
		}
	}
	return DependencyStatus{}
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
	if hasDatabaseBinding(service) && service.Database.Status == "unknown" {
		findings = append(findings, Finding{Code: "DATABASE_STATUS_UNKNOWN", Summary: "managed database has not been observed for service"})
	}
	return findings
}

func statefulMemberFindings(group stateindex.StatefulGroupSummary, member stateindex.StatefulMemberSummary) []Finding {
	var findings []Finding
	name := group.Group
	if name == "" {
		name = "stateful group"
	}
	if member.ExpectedGeneration > 0 && member.Generation > 0 && member.Generation < member.ExpectedGeneration {
		findings = append(findings, Finding{Code: "STATEFUL_RUNNER_STALE", Summary: memberSummary(name, member.Member, "runner generation "+strconv.FormatInt(member.Generation, 10)+" is behind expected generation "+strconv.FormatInt(member.ExpectedGeneration, 10))})
	}
	if member.Phase == "" {
		findings = append(findings, Finding{Code: "STATEFUL_MEMBER_PHASE_UNKNOWN", Summary: memberSummary(name, member.Member, "phase has not been observed")})
	} else if !strings.EqualFold(member.Phase, "Ready") {
		findings = append(findings, Finding{Code: "STATEFUL_MEMBER_NOT_READY", Summary: memberSummary(name, member.Member, "phase is "+member.Phase)})
	}
	if member.InstanceID == "" {
		findings = append(findings, Finding{Code: "STATEFUL_MEMBER_INSTANCE_MISSING", Summary: memberSummary(name, member.Member, "instance provider ID is missing")})
	}
	if member.VolumeID == "" {
		findings = append(findings, Finding{Code: "STATEFUL_MEMBER_VOLUME_MISSING", Summary: memberSummary(name, member.Member, "volume provider ID is missing")})
	}
	if member.DNSName == "" {
		findings = append(findings, Finding{Code: "STATEFUL_MEMBER_DNS_MISSING", Summary: memberSummary(name, member.Member, "stable DNS name is missing")})
	}
	if member.ExpectedVolumeID != "" && member.VolumeID != "" && member.ExpectedVolumeID != member.VolumeID {
		findings = append(findings, Finding{Code: "STATEFUL_MEMBER_VOLUME_MISMATCH", Summary: memberSummary(name, member.Member, "member volume "+member.VolumeID+" differs from expected volume "+member.ExpectedVolumeID)})
	}
	if member.ExpectedInstanceID != "" && member.InstanceID != "" && member.ExpectedInstanceID != member.InstanceID {
		findings = append(findings, Finding{Code: "STATEFUL_PROVIDER_DRIFT", Summary: memberSummary(name, member.Member, "instance provider ID "+member.InstanceID+" differs from expected instance "+member.ExpectedInstanceID)})
	}
	if member.ExpectedDNSName != "" && member.DNSName != "" && member.ExpectedDNSName != member.DNSName {
		findings = append(findings, Finding{Code: "STATEFUL_PROVIDER_DRIFT", Summary: memberSummary(name, member.Member, "DNS name "+member.DNSName+" differs from expected DNS "+member.ExpectedDNSName)})
	}
	if member.RecipeStatus != "" && !strings.EqualFold(member.RecipeStatus, "ok") {
		summary := "recipe health is " + member.RecipeStatus
		if member.RecipeSummary != "" {
			summary += ": " + member.RecipeSummary
		}
		findings = append(findings, Finding{Code: "STATEFUL_RECIPE_UNHEALTHY", Summary: memberSummary(name, member.Member, summary)})
	}
	if member.Lease != nil {
		findings = append(findings, Finding{Code: "STATEFUL_MEMBER_LEASE_HELD", Summary: memberSummary(name, member.Member, "lease is held by "+member.Lease.Owner)})
	}
	return findings
}

func statefulGroupFindings(group StatefulGroup, backupsByMember map[int][]StatefulBackup) []Finding {
	var findings []Finding
	if group.Lease != nil {
		findings = append(findings, Finding{Code: "STATEFUL_GROUP_LEASE_HELD", Summary: "StatefulGroup " + group.Group + " has an active lease held by " + group.Lease.Owner})
	}
	for _, member := range group.Members {
		findings = append(findings, member.Findings...)
		backups := backupsByMember[member.Member]
		if len(backups) == 0 {
			findings = append(findings, Finding{Code: "STATEFUL_BACKUP_MISSING", Summary: memberSummary(group.Group, member.Member, "no backup record has been observed")})
			continue
		}
		hasFresh := false
		for _, backup := range backups {
			if strings.EqualFold(backup.Status, "available") && !backup.Stale {
				hasFresh = true
				break
			}
		}
		if !hasFresh {
			findings = append(findings, Finding{Code: "STATEFUL_BACKUP_STALE", Summary: memberSummary(group.Group, member.Member, "no available fresh backup was observed")})
		}
	}
	for _, backup := range group.Backups {
		if backup.Stale {
			findings = append(findings, Finding{Code: "STATEFUL_BACKUP_STALE", Summary: "backup " + backup.BackupID + " for " + group.Group + " has expired"})
		}
		if backup.Status != "" && !strings.EqualFold(backup.Status, "available") {
			findings = append(findings, Finding{Code: "STATEFUL_BACKUP_UNAVAILABLE", Summary: "backup " + backup.BackupID + " status is " + backup.Status})
		}
		if backup.RecipeStatus != "" && !strings.EqualFold(backup.RecipeStatus, "ok") {
			summary := "backup " + backup.BackupID + " recipe status is " + backup.RecipeStatus
			if backup.RecipeSummary != "" {
				summary += ": " + backup.RecipeSummary
			}
			findings = append(findings, Finding{Code: "STATEFUL_RECIPE_UNHEALTHY", Summary: summary})
		}
	}
	if statefulQuorumRisk(group) {
		findings = append(findings, Finding{Code: "STATEFUL_QUORUM_RISK", Summary: "StatefulGroup " + group.Group + " has too few nominal members to preserve quorum"})
	}
	return dedupeFindings(findings)
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
	case service.Logs.Status == "unknown" || service.Metrics.Status == "unknown" || (hasDatabaseBinding(service) && service.Database.Status == "unknown"):
		return "degraded"
	default:
		return "nominal"
	}
}

func deriveStatefulMemberHealth(member StatefulMember) string {
	for _, finding := range member.Findings {
		switch finding.Code {
		case "STATEFUL_MEMBER_NOT_READY", "STATEFUL_MEMBER_VOLUME_MISSING", "STATEFUL_MEMBER_VOLUME_MISMATCH", "STATEFUL_MEMBER_INSTANCE_MISSING", "STATEFUL_RUNNER_STALE", "STATEFUL_RECIPE_UNHEALTHY", "STATEFUL_PROVIDER_DRIFT":
			return "degraded"
		}
	}
	if member.Phase == "" {
		return "unknown"
	}
	if !strings.EqualFold(member.Phase, "Ready") {
		return "degraded"
	}
	return "nominal"
}

func deriveStatefulGroupHealth(group StatefulGroup) string {
	state := strings.ToLower(firstNonEmpty(group.OperationState, ""))
	switch {
	case strings.Contains(state, "failed"):
		return "degraded"
	case group.OperationID != "" && state != "succeeded" && state != "canceled" && state != "cancelled":
		return "updating"
	}
	for _, finding := range group.Findings {
		switch finding.Code {
		case "STATEFUL_MEMBER_NOT_READY", "STATEFUL_MEMBER_VOLUME_MISSING", "STATEFUL_MEMBER_VOLUME_MISMATCH", "STATEFUL_MEMBER_INSTANCE_MISSING", "STATEFUL_BACKUP_STALE", "STATEFUL_RUNNER_STALE", "STATEFUL_RECIPE_UNHEALTHY", "STATEFUL_QUORUM_RISK", "STATEFUL_PROVIDER_DRIFT":
			return "degraded"
		}
	}
	if len(group.Members) == 0 {
		return "unknown"
	}
	for _, member := range group.Members {
		if member.Health == "degraded" {
			return "degraded"
		}
		if member.Health == "unknown" {
			return "unknown"
		}
	}
	return "nominal"
}

func statefulQuorumRisk(group StatefulGroup) bool {
	replicas := group.Replicas
	if replicas <= 0 {
		replicas = len(group.Members)
	}
	if replicas <= 1 || len(group.Members) == 0 {
		return false
	}
	nominal := 0
	for _, member := range group.Members {
		if member.Health == "nominal" {
			nominal++
		}
	}
	return nominal <= replicas/2
}

func backupStale(expiresAt string, now time.Time) bool {
	if expiresAt == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return true
	}
	return now.UTC().After(parsed)
}

func memberSummary(group string, member int, summary string) string {
	return group + " member " + strconv.Itoa(member) + " " + summary
}

func dedupeFindings(findings []Finding) []Finding {
	seen := map[string]struct{}{}
	out := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		key := finding.Code + "\x00" + finding.Summary
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, finding)
	}
	return out
}

func cloneLease(lease *schema.Lease) *schema.Lease {
	if lease == nil {
		return nil
	}
	out := *lease
	return &out
}

func hasDatabaseBinding(service Service) bool {
	if service.Database.Status != "" && service.Database.Status != "unknown" {
		return true
	}
	for _, resource := range service.Resources {
		if resource.Kind == "rds-db-instance" || resource.LogicalKind == "rds-db-instance" {
			return true
		}
		if resource.Kind == "secretsmanager-secret" || resource.LogicalKind == "secretsmanager-secret" {
			return true
		}
	}
	for _, event := range service.RecentEvents {
		for _, fact := range event.Facts {
			if fact.Type == "database" || fact.Type == "database_connectivity" {
				return true
			}
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
