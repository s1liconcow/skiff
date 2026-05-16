package schema

import "encoding/json"

const Version = "skiff.state/v1"

type Actor struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type Target struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type Lease struct {
	Holder    string `json:"holder"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type ProviderOperationRef struct {
	Provider    string `json:"provider"`
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	ObservedAt  string `json:"observed_at,omitempty"`
	Description string `json:"description,omitempty"`
}

type Risk string

const (
	RiskLow      Risk = "low"
	RiskMedium   Risk = "medium"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)

type Reversibility string

const (
	Reversible          Reversibility = "reversible"
	Compensatable       Reversibility = "compensatable"
	PartiallyReversible Reversibility = "partially_reversible"
	Irreversible        Reversibility = "irreversible"
)

type OperationStatus string

const (
	OperationPending   OperationStatus = "pending"
	OperationRunning   OperationStatus = "running"
	OperationSucceeded OperationStatus = "succeeded"
	OperationFailed    OperationStatus = "failed"
	OperationCanceled  OperationStatus = "canceled"
)

type SagaStatus string

const (
	SagaPending      SagaStatus = "pending"
	SagaRunning      SagaStatus = "running"
	SagaCompensating SagaStatus = "compensating"
	SagaSucceeded    SagaStatus = "succeeded"
	SagaFailed       SagaStatus = "failed"
	SagaCanceled     SagaStatus = "canceled"
)

type ServiceControl struct {
	SchemaVersion  string `json:"schema_version"`
	Service        string `json:"service"`
	Env            string `json:"env"`
	DesiredRelease string `json:"desired_release,omitempty"`
	StableRelease  string `json:"stable_release,omitempty"`
	Lease          *Lease `json:"lease,omitempty"`
	UpdatedAt      string `json:"updated_at"`
	UpdatedBy      Actor  `json:"updated_by"`
	TraceID        string `json:"trace_id,omitempty"`
}

type OperationIntent struct {
	SchemaVersion string          `json:"schema_version"`
	OperationID   string          `json:"operation_id"`
	Service       string          `json:"service"`
	Env           string          `json:"env"`
	Kind          string          `json:"kind"`
	Target        Target          `json:"target"`
	Actor         Actor           `json:"actor"`
	TraceID       string          `json:"trace_id"`
	Risk          Risk            `json:"risk"`
	Reversibility Reversibility   `json:"reversibility"`
	Summary       string          `json:"summary"`
	CreatedAt     string          `json:"created_at"`
	Params        json.RawMessage `json:"params,omitempty"`
}

type OperationControl struct {
	SchemaVersion      string                 `json:"schema_version"`
	OperationID        string                 `json:"operation_id"`
	Service            string                 `json:"service"`
	Env                string                 `json:"env"`
	Status             OperationStatus        `json:"status"`
	Lease              *Lease                 `json:"lease,omitempty"`
	ProviderOperations []ProviderOperationRef `json:"provider_operations,omitempty"`
	StepResults        []StepResultRef        `json:"step_results,omitempty"`
	UpdatedAt          string                 `json:"updated_at"`
	TraceID            string                 `json:"trace_id,omitempty"`
}

type StepResultRef struct {
	StepID      string          `json:"step_id"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	Result      json.RawMessage `json:"result,omitempty"`
	CompletedAt string          `json:"completed_at,omitempty"`
}

type ArtifactRef struct {
	Type   string `json:"type"`
	URI    string `json:"uri"`
	Digest string `json:"digest"`
}

type Signature struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
	SignedAt  string `json:"signed_at"`
	Signer    *Actor `json:"signer,omitempty"`
}

type ReleaseManifest struct {
	SchemaVersion         string      `json:"schema_version"`
	Service               string      `json:"service"`
	Env                   string      `json:"env"`
	ReleaseID             string      `json:"release_id"`
	Artifact              ArtifactRef `json:"artifact"`
	RuntimeManifestKey    string      `json:"runtime_manifest_key"`
	RuntimeManifestDigest string      `json:"runtime_manifest_digest,omitempty"`
	Digest                string      `json:"digest,omitempty"`
	CreatedAt             string      `json:"created_at"`
	ExpiresAt             string      `json:"expires_at,omitempty"`
	Signatures            []Signature `json:"signatures,omitempty"`
}

type RuntimeManifest struct {
	SchemaVersion string            `json:"schema_version"`
	Service       string            `json:"service"`
	Env           string            `json:"env"`
	ReleaseID     string            `json:"release_id"`
	Command       []string          `json:"command,omitempty"`
	EnvVars       map[string]string `json:"env_vars,omitempty"`
	HealthCheck   *HealthCheck      `json:"health_check,omitempty"`
	CreatedAt     string            `json:"created_at"`
}

type HealthCheck struct {
	Type     string `json:"type"`
	Path     string `json:"path,omitempty"`
	Port     int    `json:"port,omitempty"`
	Interval string `json:"interval,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
}

type SagaIntent struct {
	SchemaVersion string          `json:"schema_version"`
	SagaID        string          `json:"saga_id"`
	Kind          string          `json:"kind"`
	Target        Target          `json:"target"`
	Actor         Actor           `json:"actor"`
	TraceID       string          `json:"trace_id"`
	Risk          Risk            `json:"risk"`
	Reversibility Reversibility   `json:"reversibility"`
	Summary       string          `json:"summary"`
	CreatedAt     string          `json:"created_at"`
	Params        json.RawMessage `json:"params,omitempty"`
}

type SagaGraph struct {
	SchemaVersion string     `json:"schema_version"`
	SagaID        string     `json:"saga_id"`
	Nodes         []SagaNode `json:"nodes"`
	Edges         []SagaEdge `json:"edges,omitempty"`
	CreatedAt     string     `json:"created_at"`
}

type SagaNode struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Params json.RawMessage `json:"params,omitempty"`
}

type SagaEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type SagaControl struct {
	SchemaVersion string          `json:"schema_version"`
	SagaID        string          `json:"saga_id"`
	Status        SagaStatus      `json:"status"`
	Lease         *Lease          `json:"lease,omitempty"`
	CurrentSteps  []string        `json:"current_steps,omitempty"`
	StepResults   []StepResultRef `json:"step_results,omitempty"`
	UpdatedAt     string          `json:"updated_at"`
	TraceID       string          `json:"trace_id,omitempty"`
}

type ResourceRecord struct {
	SchemaVersion string              `json:"schema_version"`
	Logical       ResourceLogicalRef  `json:"logical"`
	Provider      ResourceProviderRef `json:"provider"`
	Service       string              `json:"service,omitempty"`
	Env           string              `json:"env,omitempty"`
	Tags          map[string]string   `json:"tags,omitempty"`
	ObservedAt    string              `json:"observed_at"`
}

type ResourceLogicalRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type ResourceProviderRef struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
}

type Event struct {
	SchemaVersion string          `json:"schema_version"`
	ID            string          `json:"id"`
	Time          string          `json:"time"`
	TraceID       string          `json:"trace_id,omitempty"`
	Subject       Target          `json:"subject"`
	Type          string          `json:"type"`
	Severity      string          `json:"severity,omitempty"`
	Actor         *Actor          `json:"actor,omitempty"`
	Summary       string          `json:"summary"`
	Facts         []Fact          `json:"facts,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
}

type Fact struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type AuditRecord struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Time          string `json:"time"`
	Actor         Actor  `json:"actor"`
	TraceID       string `json:"trace_id"`
	Target        Target `json:"target"`
	OperationID   string `json:"operation_id,omitempty"`
	SagaID        string `json:"saga_id,omitempty"`
	Risk          Risk   `json:"risk"`
	Summary       string `json:"summary"`
}

type ServicesIndex struct {
	SchemaVersion string         `json:"schema_version"`
	GeneratedAt   string         `json:"generated_at"`
	Services      []ServiceIndex `json:"services"`
}

type ServiceIndex struct {
	Service    string `json:"service"`
	Env        string `json:"env"`
	ControlKey string `json:"control_key"`
	Release    string `json:"release,omitempty"`
}

type ActiveSagasIndex struct {
	SchemaVersion string            `json:"schema_version"`
	GeneratedAt   string            `json:"generated_at"`
	Sagas         []ActiveSagaIndex `json:"sagas"`
}

type ActiveSagaIndex struct {
	SagaID    string     `json:"saga_id"`
	Kind      string     `json:"kind"`
	Status    SagaStatus `json:"status"`
	Target    Target     `json:"target"`
	UpdatedAt string     `json:"updated_at"`
}

type RecentEventsIndex struct {
	SchemaVersion string       `json:"schema_version"`
	GeneratedAt   string       `json:"generated_at"`
	Events        []EventIndex `json:"events"`
}

type EventIndex struct {
	ID      string `json:"id"`
	Time    string `json:"time"`
	Type    string `json:"type"`
	Subject Target `json:"subject"`
	Key     string `json:"key"`
}

type ServiceObservation struct {
	SchemaVersion string          `json:"schema_version"`
	ID            string          `json:"id"`
	Service       string          `json:"service"`
	Env           string          `json:"env,omitempty"`
	ObservedAt    string          `json:"observed_at"`
	Kind          string          `json:"kind"`
	Data          json.RawMessage `json:"data,omitempty"`
}

func NewServiceControl(service, env, updatedAt string, actor Actor) ServiceControl {
	return ServiceControl{
		SchemaVersion: Version,
		Service:       service,
		Env:           env,
		UpdatedAt:     updatedAt,
		UpdatedBy:     actor,
	}
}

func NewOperationIntent(operationID, service, env, kind string, target Target, actor Actor, traceID, createdAt string) OperationIntent {
	return OperationIntent{
		SchemaVersion: Version,
		OperationID:   operationID,
		Service:       service,
		Env:           env,
		Kind:          kind,
		Target:        target,
		Actor:         actor,
		TraceID:       traceID,
		CreatedAt:     createdAt,
	}
}

func NewSagaControl(sagaID string, status SagaStatus, updatedAt string) SagaControl {
	return SagaControl{
		SchemaVersion: Version,
		SagaID:        sagaID,
		Status:        status,
		UpdatedAt:     updatedAt,
	}
}
