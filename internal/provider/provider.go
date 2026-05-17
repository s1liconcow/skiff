package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/ir"
)

type Provider interface {
	Name() string
	Plan(ctx context.Context, graph *ir.Graph) (*Plan, error)
	Apply(ctx context.Context, plan *Plan) (*ApplyResult, error)
	InspectService(ctx context.Context, ref ServiceRef) (*ServiceInspection, error)
	InspectResource(ctx context.Context, ref ResourceRef) (*ResourceInspection, error)
	Logs(ctx context.Context, req LogsRequest) (*LogsResult, error)
	Metrics(ctx context.Context, req MetricsRequest) (*MetricsResult, error)
	Debug(ctx context.Context, req DebugRequest) (*DebugSession, error)
	StartRollout(ctx context.Context, req RolloutRequest) (*Rollout, error)
	WatchRollout(ctx context.Context, req WatchRolloutRequest) (*RolloutStatus, error)
	Rollback(ctx context.Context, req RollbackRequest) (*Rollout, error)
}

type DatabaseOperations interface {
	SnapshotDatabase(ctx context.Context, req DatabaseSnapshotRequest) (*DatabaseSnapshot, error)
	VerifyRestorePoint(ctx context.Context, req RestorePointRequest) (*RestorePoint, error)
	RestoreDatabase(ctx context.Context, req DatabaseRestoreRequest) (*DatabaseRestore, error)
	InspectDatabase(ctx context.Context, ref DatabaseRef) (*DatabaseInspection, error)
	RunDatabaseSmokeQuery(ctx context.Context, req DatabaseSmokeQueryRequest) (*DatabaseSmokeQueryResult, error)
	RunShadowServiceTest(ctx context.Context, req ShadowServiceTestRequest) (*ShadowServiceTestResult, error)
	UpdateSecretPointer(ctx context.Context, req SecretPointerRequest) (*SecretPointerUpdate, error)
	RestartService(ctx context.Context, req ServiceRestartRequest) (*Rollout, error)
	RetireDatabase(ctx context.Context, req DatabaseRetireRequest) (*DatabaseRetireResult, error)
}

type SecretOperations interface {
	CreateSecretVersion(ctx context.Context, req SecretVersionRequest) (*SecretVersion, error)
	ValidateSecretVersion(ctx context.Context, req SecretValidationRequest) (*SecretValidationResult, error)
	UpdateSecretVersionPointer(ctx context.Context, req SecretUpdateRequest) (*SecretPointer, error)
	RestoreSecretVersion(ctx context.Context, req SecretRestoreRequest) (*SecretPointer, error)
	CanaryServiceWithSecret(ctx context.Context, req SecretCanaryRequest) (*SecretCanaryResult, error)
	RollConsumersWithSecret(ctx context.Context, req SecretRollConsumersRequest) (*SecretRollConsumersResult, error)
	DisableOldCredential(ctx context.Context, req CredentialDisableRequest) (*CredentialDisableResult, error)
}

type TrafficOperations interface {
	ShiftTraffic(ctx context.Context, req TrafficShiftRequest) (*TrafficShiftResult, error)
}

type Plan struct {
	Provider  string          `json:"provider"`
	Service   string          `json:"service"`
	Env       string          `json:"env"`
	Resources []PlannedChange `json:"resources,omitempty"`
}

type PlannedChange struct {
	Action      string            `json:"action"`
	Kind        string            `json:"kind"`
	LogicalID   string            `json:"logical_id"`
	Name        string            `json:"name"`
	ProviderID  string            `json:"provider_id,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	Desired     json.RawMessage   `json:"desired,omitempty"`
}

const (
	ActionCreate             = "create"
	ActionUpdate             = "update"
	ActionNoop               = "no-op"
	ActionDeleteNotSupported = "delete-not-supported"
)

type ApplyResult struct {
	Provider    string               `json:"provider"`
	Service     string               `json:"service,omitempty"`
	Env         string               `json:"env,omitempty"`
	AppliedAt   time.Time            `json:"applied_at"`
	ResourceIDs []string             `json:"resource_ids,omitempty"`
	Resources   []ResourceInspection `json:"resources,omitempty"`
}

type ServiceRef struct {
	Service string `json:"service"`
	Env     string `json:"env"`
}

type ResourceRef struct {
	Service    string `json:"service,omitempty"`
	Env        string `json:"env,omitempty"`
	Kind       string `json:"kind,omitempty"`
	LogicalID  string `json:"logical_id,omitempty"`
	Name       string `json:"name,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
}

type ServiceInspection struct {
	Ref       ServiceRef           `json:"ref"`
	Provider  string               `json:"provider"`
	FreshAt   time.Time            `json:"fresh_at"`
	Resources []ResourceInspection `json:"resources,omitempty"`
}

type ResourceInspection struct {
	Kind       string            `json:"kind"`
	LogicalID  string            `json:"logical_id,omitempty"`
	Name       string            `json:"name,omitempty"`
	ProviderID string            `json:"provider_id,omitempty"`
	ARN        string            `json:"arn,omitempty"`
	Status     string            `json:"status,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type LogsRequest struct {
	Service    string    `json:"service"`
	Env        string    `json:"env"`
	ResourceID string    `json:"resource_id,omitempty"`
	ReleaseID  string    `json:"release_id,omitempty"`
	InstanceID string    `json:"instance_id,omitempty"`
	Since      time.Time `json:"since,omitempty"`
	Limit      int       `json:"limit,omitempty"`
}

type LogsResult struct {
	Entries []LogEntry `json:"entries,omitempty"`
}

type LogEntry struct {
	Timestamp time.Time         `json:"timestamp"`
	Message   string            `json:"message"`
	Source    string            `json:"source,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

type MetricsRequest struct {
	Service       string    `json:"service"`
	Env           string    `json:"env"`
	ReleaseID     string    `json:"release_id,omitempty"`
	InstanceID    string    `json:"instance_id,omitempty"`
	Names         []string  `json:"names,omitempty"`
	From          time.Time `json:"from,omitempty"`
	To            time.Time `json:"to,omitempty"`
	PeriodSeconds int       `json:"period_seconds,omitempty"`
}

type MetricsResult struct {
	Series []MetricSeries `json:"series,omitempty"`
}

type MetricSeries struct {
	Name     string            `json:"name"`
	Category string            `json:"category,omitempty"`
	Source   string            `json:"source,omitempty"`
	Unit     string            `json:"unit,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Points   []MetricPoint     `json:"points,omitempty"`
}

type MetricPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

type DebugRequest struct {
	Service string `json:"service"`
	Env     string `json:"env"`
	Reason  string `json:"reason,omitempty"`
}

type DebugSession struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	StartedAt time.Time `json:"started_at"`
}

type RolloutRequest struct {
	Service              string `json:"service"`
	Env                  string `json:"env"`
	ReleaseID            string `json:"release_id"`
	OperationID          string `json:"operation_id,omitempty"`
	MinHealthyPercentage int    `json:"min_healthy_percentage,omitempty"`
	InstanceWarmup       int    `json:"instance_warmup,omitempty"`
}

type WatchRolloutRequest struct {
	Service    string `json:"service"`
	Env        string `json:"env"`
	RolloutID  string `json:"rollout_id"`
	ProviderID string `json:"provider_id,omitempty"`
}

type RollbackRequest struct {
	Service   string `json:"service"`
	Env       string `json:"env"`
	ReleaseID string `json:"release_id"`
}

type Rollout struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	Service    string    `json:"service"`
	Env        string    `json:"env"`
	ProviderID string    `json:"provider_id,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

type RolloutStatus struct {
	RolloutID  string    `json:"rollout_id"`
	Status     string    `json:"status"`
	ProviderID string    `json:"provider_id,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type TrafficShiftRequest struct {
	Service     string `json:"service"`
	Env         string `json:"env"`
	From        string `json:"from"`
	To          string `json:"to"`
	Percent     int    `json:"percent"`
	OperationID string `json:"operation_id,omitempty"`
	SagaID      string `json:"saga_id,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
}

type TrafficShiftResult struct {
	Provider   string            `json:"provider"`
	Service    string            `json:"service"`
	Env        string            `json:"env"`
	From       string            `json:"from"`
	To         string            `json:"to"`
	Percent    int               `json:"percent"`
	ProviderID string            `json:"provider_id,omitempty"`
	Status     string            `json:"status"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Facts      map[string]string `json:"facts,omitempty"`
}

type DatabaseRef struct {
	Service    string `json:"service,omitempty"`
	Env        string `json:"env,omitempty"`
	Database   string `json:"database"`
	ProviderID string `json:"provider_id,omitempty"`
	ARN        string `json:"arn,omitempty"`
}

type DatabaseSnapshotRequest struct {
	Ref         DatabaseRef `json:"ref"`
	SnapshotID  string      `json:"snapshot_id,omitempty"`
	OperationID string      `json:"operation_id,omitempty"`
	SagaID      string      `json:"saga_id,omitempty"`
	TraceID     string      `json:"trace_id,omitempty"`
}

type DatabaseSnapshot struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	ProviderID string    `json:"provider_id,omitempty"`
	Database   string    `json:"database"`
	Status     string    `json:"status,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

type RestorePointRequest struct {
	Ref          DatabaseRef `json:"ref"`
	RestorePoint string      `json:"restore_point,omitempty"`
	RestoreTime  time.Time   `json:"restore_time,omitempty"`
	SnapshotID   string      `json:"snapshot_id,omitempty"`
}

type RestorePoint struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	ProviderID string    `json:"provider_id,omitempty"`
	Database   string    `json:"database"`
	Status     string    `json:"status,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
}

type DatabaseRestoreRequest struct {
	Source         DatabaseRef `json:"source"`
	RestorePointID string      `json:"restore_point_id,omitempty"`
	RestoreTime    time.Time   `json:"restore_time,omitempty"`
	TargetDatabase string      `json:"target_database"`
	OperationID    string      `json:"operation_id,omitempty"`
	SagaID         string      `json:"saga_id,omitempty"`
	TraceID        string      `json:"trace_id,omitempty"`
}

type DatabaseRestore struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	ProviderID string    `json:"provider_id,omitempty"`
	Database   string    `json:"database"`
	Status     string    `json:"status,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

type DatabaseInspection struct {
	Ref        DatabaseRef `json:"ref"`
	Provider   string      `json:"provider"`
	Status     string      `json:"status,omitempty"`
	Endpoint   string      `json:"endpoint,omitempty"`
	ProviderID string      `json:"provider_id,omitempty"`
	FreshAt    time.Time   `json:"fresh_at"`
}

type DatabaseSmokeQueryRequest struct {
	Ref   DatabaseRef `json:"ref"`
	Query string      `json:"query,omitempty"`
}

type DatabaseSmokeQueryResult struct {
	OK        bool   `json:"ok"`
	Summary   string `json:"summary,omitempty"`
	Rows      int    `json:"rows,omitempty"`
	LatencyMS int    `json:"latency_ms,omitempty"`
}

type ShadowServiceTestRequest struct {
	Service  string      `json:"service"`
	Env      string      `json:"env,omitempty"`
	Database DatabaseRef `json:"database"`
	Query    string      `json:"query,omitempty"`
}

type ShadowServiceTestResult struct {
	OK      bool   `json:"ok"`
	Summary string `json:"summary,omitempty"`
}

type SecretPointerRequest struct {
	Service          string `json:"service,omitempty"`
	Env              string `json:"env,omitempty"`
	Database         string `json:"database,omitempty"`
	SecretRef        string `json:"secret_ref"`
	TargetDatabase   string `json:"target_database,omitempty"`
	TargetProviderID string `json:"target_provider_id,omitempty"`
	PreviousVersion  string `json:"previous_version,omitempty"`
	OperationID      string `json:"operation_id,omitempty"`
	SagaID           string `json:"saga_id,omitempty"`
	TraceID          string `json:"trace_id,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

type SecretPointerUpdate struct {
	SecretRef        string    `json:"secret_ref"`
	PreviousVersion  string    `json:"previous_version,omitempty"`
	NewVersion       string    `json:"new_version,omitempty"`
	PreviousDatabase string    `json:"previous_database,omitempty"`
	NewDatabase      string    `json:"new_database,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ServiceRestartRequest struct {
	Service     string `json:"service"`
	Env         string `json:"env,omitempty"`
	ReleaseID   string `json:"release_id,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	SagaID      string `json:"saga_id,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type DatabaseRetireRequest struct {
	Ref         DatabaseRef `json:"ref"`
	OperationID string      `json:"operation_id,omitempty"`
	SagaID      string      `json:"saga_id,omitempty"`
	TraceID     string      `json:"trace_id,omitempty"`
	Reason      string      `json:"reason,omitempty"`
}

type DatabaseRetireResult struct {
	Database   string    `json:"database"`
	Provider   string    `json:"provider"`
	ProviderID string    `json:"provider_id,omitempty"`
	Status     string    `json:"status"`
	RetiredAt  time.Time `json:"retired_at"`
}

type SecretVersionRequest struct {
	SecretRef   string   `json:"secret_ref"`
	Env         string   `json:"env,omitempty"`
	Consumers   []string `json:"consumers,omitempty"`
	Database    string   `json:"database,omitempty"`
	OperationID string   `json:"operation_id,omitempty"`
	SagaID      string   `json:"saga_id,omitempty"`
	TraceID     string   `json:"trace_id,omitempty"`
}

type SecretVersion struct {
	SecretRef       string    `json:"secret_ref"`
	Provider        string    `json:"provider"`
	VersionID       string    `json:"version_id"`
	PreviousVersion string    `json:"previous_version,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type SecretValidationRequest struct {
	SecretRef string `json:"secret_ref"`
	VersionID string `json:"version_id"`
	Env       string `json:"env,omitempty"`
	Database  string `json:"database,omitempty"`
}

type SecretValidationResult struct {
	OK        bool   `json:"ok"`
	SecretRef string `json:"secret_ref"`
	VersionID string `json:"version_id"`
	Summary   string `json:"summary,omitempty"`
}

type SecretUpdateRequest struct {
	SecretRef       string   `json:"secret_ref"`
	VersionID       string   `json:"version_id"`
	PreviousVersion string   `json:"previous_version,omitempty"`
	Env             string   `json:"env,omitempty"`
	Database        string   `json:"database,omitempty"`
	Consumers       []string `json:"consumers,omitempty"`
	Scope           string   `json:"scope,omitempty"`
	OperationID     string   `json:"operation_id,omitempty"`
	SagaID          string   `json:"saga_id,omitempty"`
	TraceID         string   `json:"trace_id,omitempty"`
}

type SecretPointer struct {
	SecretRef       string    `json:"secret_ref"`
	Provider        string    `json:"provider"`
	VersionID       string    `json:"version_id"`
	PreviousVersion string    `json:"previous_version,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type SecretRestoreRequest struct {
	SecretRef       string   `json:"secret_ref"`
	PreviousVersion string   `json:"previous_version"`
	Env             string   `json:"env,omitempty"`
	Database        string   `json:"database,omitempty"`
	Consumers       []string `json:"consumers,omitempty"`
	Scope           string   `json:"scope,omitempty"`
	OperationID     string   `json:"operation_id,omitempty"`
	SagaID          string   `json:"saga_id,omitempty"`
	TraceID         string   `json:"trace_id,omitempty"`
}

type SecretCanaryRequest struct {
	SecretRef   string `json:"secret_ref"`
	VersionID   string `json:"version_id"`
	Env         string `json:"env,omitempty"`
	Database    string `json:"database,omitempty"`
	Consumer    string `json:"consumer"`
	OperationID string `json:"operation_id,omitempty"`
	SagaID      string `json:"saga_id,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
}

type SecretCanaryResult struct {
	OK       bool   `json:"ok"`
	Consumer string `json:"consumer"`
	Summary  string `json:"summary,omitempty"`
}

type SecretRollConsumersRequest struct {
	SecretRef   string   `json:"secret_ref"`
	VersionID   string   `json:"version_id"`
	Env         string   `json:"env,omitempty"`
	Database    string   `json:"database,omitempty"`
	Consumers   []string `json:"consumers"`
	OperationID string   `json:"operation_id,omitempty"`
	SagaID      string   `json:"saga_id,omitempty"`
	TraceID     string   `json:"trace_id,omitempty"`
}

type SecretRollConsumersResult struct {
	OK        bool     `json:"ok"`
	Consumers []string `json:"consumers"`
	Summary   string   `json:"summary,omitempty"`
}

type CredentialDisableRequest struct {
	SecretRef       string   `json:"secret_ref"`
	PreviousVersion string   `json:"previous_version"`
	Env             string   `json:"env,omitempty"`
	Database        string   `json:"database,omitempty"`
	Consumers       []string `json:"consumers,omitempty"`
	DisableAfter    string   `json:"disable_after,omitempty"`
	OperationID     string   `json:"operation_id,omitempty"`
	SagaID          string   `json:"saga_id,omitempty"`
	TraceID         string   `json:"trace_id,omitempty"`
}

type CredentialDisableResult struct {
	SecretRef       string    `json:"secret_ref"`
	PreviousVersion string    `json:"previous_version"`
	ScheduledFor    string    `json:"scheduled_for,omitempty"`
	Provider        string    `json:"provider"`
	Status          string    `json:"status"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ErrorCode string

const (
	CodeAccessDenied  ErrorCode = "ACCESS_DENIED"
	CodeInvalidConfig ErrorCode = "INVALID_CONFIG"
	CodeNotFound      ErrorCode = "NOT_FOUND"
	CodeProvider      ErrorCode = "PROVIDER_ERROR"
	CodeThrottled     ErrorCode = "THROTTLED"
	CodeUnsupported   ErrorCode = "UNSUPPORTED"
	CodeValidation    ErrorCode = "VALIDATION_ERROR"
)

type Error struct {
	Code     ErrorCode `json:"code"`
	Provider string    `json:"provider,omitempty"`
	Op       string    `json:"op,omitempty"`
	Resource string    `json:"resource,omitempty"`
	Summary  string    `json:"summary"`
	Cause    error     `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := string(e.Code)
	if e.Provider != "" {
		prefix = e.Provider + ": " + prefix
	}
	if e.Op != "" {
		prefix += " " + e.Op
	}
	if e.Resource != "" {
		prefix += " " + e.Resource
	}
	if e.Summary == "" {
		return prefix
	}
	return fmt.Sprintf("%s: %s", prefix, e.Summary)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func Unsupported(providerName, op string) *Error {
	return &Error{
		Code:     CodeUnsupported,
		Provider: providerName,
		Op:       op,
		Summary:  "provider operation is not implemented yet",
	}
}
