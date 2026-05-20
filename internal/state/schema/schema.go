package schema

import "encoding/json"

const Version = "skiff.state/v1"

const EnvironmentRootSchemaVersion = "skiff.environment-root/v1"

type EnvironmentRoot struct {
	SchemaVersion string              `json:"schema_version"`
	Env           string              `json:"env"`
	Provider      string              `json:"provider"`
	Region        string              `json:"region"`
	StateBucket   string              `json:"state_bucket"`
	KMSAlias      string              `json:"kms_alias"`
	Roles         map[string]string   `json:"roles"`
	Network       *EnvironmentNetwork `json:"network,omitempty"`
	Ingress       *EnvironmentIngress `json:"ingress,omitempty"`
	Runner        *EnvironmentRunner  `json:"runner,omitempty"`
	ReleaseTrust  *ReleaseTrust       `json:"release_trust,omitempty"`
	CreatedAt     string              `json:"created_at"`
	UpdatedAt     string              `json:"updated_at"`
}

type EnvironmentNetwork struct {
	Mode             string   `json:"mode"`
	VPCID            string   `json:"vpc_id,omitempty"`
	PrivateSubnetIDs []string `json:"private_subnet_ids,omitempty"`
	PublicSubnetIDs  []string `json:"public_subnet_ids,omitempty"`
}

type EnvironmentIngress struct {
	Type                string                           `json:"type"`
	Host                string                           `json:"host,omitempty"`
	BaseDomain          string                           `json:"base_domain,omitempty"`
	DefaultHostTemplate string                           `json:"default_host_template,omitempty"`
	DomainName          string                           `json:"domain_name,omitempty"`
	Route53ZoneID       string                           `json:"route53_zone_id,omitempty"`
	LoadBalancer        *EnvironmentLoadBalancerDefaults `json:"load_balancer,omitempty"`
}

type EnvironmentLoadBalancerDefaults struct {
	ARN              string `json:"arn,omitempty"`
	DNSName          string `json:"dns_name,omitempty"`
	ProviderDNSName  string `json:"provider_dns_name,omitempty"`
	HostedZoneID     string `json:"hosted_zone_id,omitempty"`
	SecurityGroupID  string `json:"security_group_id,omitempty"`
	HTTPListenerARN  string `json:"http_listener_arn,omitempty"`
	HTTPSListenerARN string `json:"https_listener_arn,omitempty"`
	CertificateARN   string `json:"certificate_arn,omitempty"`
}

type EnvironmentRunner struct {
	AMIID               string `json:"ami_id,omitempty"`
	AMISSMParameter     string `json:"ami_ssm_parameter,omitempty"`
	InstallVersion      string `json:"install_version,omitempty"`
	InstallBaseURL      string `json:"install_base_url,omitempty"`
	InstallScriptURL    string `json:"install_script_url,omitempty"`
	InstallPublicKeyRef string `json:"install_public_key_ref,omitempty"`
}

type ReleaseTrust struct {
	ActiveKeyIDs []string          `json:"active_key_ids,omitempty"`
	Keys         []ReleaseTrustKey `json:"keys,omitempty"`
}

type ReleaseTrustKey struct {
	KeyID     string `json:"key_id"`
	Backend   string `json:"backend"`
	Algorithm string `json:"algorithm,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	KeyRef    string `json:"key_ref,omitempty"`
	PublicKey string `json:"public_key"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type Actor struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type Target struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type Lease struct {
	Owner      string `json:"owner"`
	Token      string `json:"token"`
	Generation int64  `json:"generation"`
	ExpiresAt  string `json:"expires_at"`
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
	SchemaVersion  string           `json:"schema_version"`
	Service        string           `json:"service"`
	Env            string           `json:"env"`
	DesiredRelease string           `json:"desired_release,omitempty"`
	StableRelease  string           `json:"stable_release,omitempty"`
	Operation      *ActiveOperation `json:"operation,omitempty"`
	Lease          *Lease           `json:"lease,omitempty"`
	Version        int64            `json:"version"`
	UpdatedAt      string           `json:"updated_at"`
	UpdatedBy      Actor            `json:"updated_by"`
	TraceID        string           `json:"trace_id,omitempty"`
}

type StatefulGroupControl struct {
	SchemaVersion string                  `json:"schema_version"`
	Group         string                  `json:"group"`
	Env           string                  `json:"env"`
	Replicas      int                     `json:"replicas"`
	Members       []StatefulMemberSummary `json:"members,omitempty"`
	Operation     *ActiveOperation        `json:"operation,omitempty"`
	Lease         *Lease                  `json:"lease,omitempty"`
	Version       int64                   `json:"version"`
	UpdatedAt     string                  `json:"updated_at"`
	UpdatedBy     Actor                   `json:"updated_by"`
	TraceID       string                  `json:"trace_id,omitempty"`
}

type StatefulMemberSummary struct {
	Member             int    `json:"member"`
	Generation         int64  `json:"generation"`
	ReleaseID          string `json:"release_id,omitempty"`
	ReleaseManifestKey string `json:"release_manifest_key,omitempty"`
	RuntimeManifestKey string `json:"runtime_manifest_key,omitempty"`
	InstanceID         string `json:"instance_id,omitempty"`
	VolumeID           string `json:"volume_id,omitempty"`
	DNSName            string `json:"dns_name,omitempty"`
	Phase              string `json:"phase,omitempty"`
}

type StatefulMemberControl struct {
	SchemaVersion      string                 `json:"schema_version"`
	Group              string                 `json:"group"`
	Env                string                 `json:"env"`
	Member             int                    `json:"member"`
	Zone               string                 `json:"zone,omitempty"`
	InstanceID         string                 `json:"instance_id,omitempty"`
	VolumeID           string                 `json:"volume_id,omitempty"`
	DNSName            string                 `json:"dns_name,omitempty"`
	Generation         int64                  `json:"generation"`
	ReleaseID          string                 `json:"release_id,omitempty"`
	ReleaseManifestKey string                 `json:"release_manifest_key,omitempty"`
	RuntimeManifestKey string                 `json:"runtime_manifest_key,omitempty"`
	Phase              string                 `json:"phase"`
	Lease              *Lease                 `json:"lease,omitempty"`
	ProviderOperations []ProviderOperationRef `json:"provider_operations,omitempty"`
	Replacement        *StatefulReplacement   `json:"replacement,omitempty"`
	Version            int64                  `json:"version"`
	UpdatedAt          string                 `json:"updated_at"`
	UpdatedBy          Actor                  `json:"updated_by"`
	TraceID            string                 `json:"trace_id,omitempty"`
}

type StatefulReplacement struct {
	OperationID           string `json:"operation_id"`
	SagaID                string `json:"saga_id,omitempty"`
	OldInstanceID         string `json:"old_instance_id,omitempty"`
	NewInstanceID         string `json:"new_instance_id,omitempty"`
	VolumeID              string `json:"volume_id,omitempty"`
	Generation            int64  `json:"generation"`
	FencedAt              string `json:"fenced_at,omitempty"`
	DetachedAt            string `json:"detached_at,omitempty"`
	ReplacementLaunchedAt string `json:"replacement_launched_at,omitempty"`
	AttachedAt            string `json:"attached_at,omitempty"`
	DNSUpdatedAt          string `json:"dns_updated_at,omitempty"`
	RecipeRecoveredAt     string `json:"recipe_recovered_at,omitempty"`
	VerifiedAt            string `json:"verified_at,omitempty"`
}

type ActiveOperation struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Step  string `json:"step,omitempty"`
}

type OperationIntent struct {
	SchemaVersion     string          `json:"schema_version"`
	OperationID       string          `json:"operation_id"`
	Service           string          `json:"service"`
	Env               string          `json:"env"`
	Kind              string          `json:"kind"`
	Target            Target          `json:"target"`
	Actor             Actor           `json:"actor"`
	TraceID           string          `json:"trace_id"`
	Risk              Risk            `json:"risk"`
	Reversibility     Reversibility   `json:"reversibility"`
	PackageLockDigest string          `json:"package_lock_digest,omitempty"`
	Summary           string          `json:"summary"`
	CreatedAt         string          `json:"created_at"`
	Params            json.RawMessage `json:"params,omitempty"`
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
	StepID             string                 `json:"step_id"`
	Kind               string                 `json:"kind"`
	Status             string                 `json:"status"`
	Result             json.RawMessage        `json:"result,omitempty"`
	ResultRef          string                 `json:"result_ref,omitempty"`
	Failure            *StepFailure           `json:"failure,omitempty"`
	ProviderOperations []ProviderOperationRef `json:"provider_operations,omitempty"`
	CompletedAt        string                 `json:"completed_at,omitempty"`
}

type StepResult struct {
	SchemaVersion      string                 `json:"schema_version"`
	SagaID             string                 `json:"saga_id,omitempty"`
	OperationID        string                 `json:"operation_id,omitempty"`
	StepID             string                 `json:"step_id"`
	Kind               string                 `json:"kind"`
	Status             string                 `json:"status"`
	Result             json.RawMessage        `json:"result,omitempty"`
	Failure            *StepFailure           `json:"failure,omitempty"`
	ProviderOperations []ProviderOperationRef `json:"provider_operations,omitempty"`
	StartedAt          string                 `json:"started_at,omitempty"`
	CompletedAt        string                 `json:"completed_at,omitempty"`
}

type StepFailure struct {
	Code       string `json:"code"`
	Summary    string `json:"summary"`
	Cause      string `json:"cause,omitempty"`
	Retriable  bool   `json:"retriable,omitempty"`
	RetryAfter string `json:"retry_after,omitempty"`
}

type PackageProvenance struct {
	Name           string `json:"name,omitempty"`
	Ref            string `json:"ref,omitempty"`
	Version        string `json:"version,omitempty"`
	Digest         string `json:"digest,omitempty"`
	ManifestDigest string `json:"manifest_digest,omitempty"`
	LockfileDigest string `json:"lockfile_digest,omitempty"`
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
	PackageLockDigest     string      `json:"package_lock_digest,omitempty"`
	MinRunnerVersion      string      `json:"min_runner_version,omitempty"`
	Digest                string      `json:"digest,omitempty"`
	CreatedAt             string      `json:"created_at"`
	ExpiresAt             string      `json:"expires_at,omitempty"`
	Signatures            []Signature `json:"signatures,omitempty"`
}

type ReleaseCandidate struct {
	SchemaVersion string            `json:"schema_version"`
	CandidateID   string            `json:"candidate_id"`
	Service       string            `json:"service"`
	Env           string            `json:"env"`
	ReleaseID     string            `json:"release_id,omitempty"`
	Artifact      ArtifactRef       `json:"artifact"`
	Git           GitMetadata       `json:"git,omitempty"`
	CI            CIMetadata        `json:"ci,omitempty"`
	Checks        []EvidenceCheck   `json:"checks,omitempty"`
	SBOM          []EvidenceRef     `json:"sbom,omitempty"`
	Provenance    []EvidenceRef     `json:"provenance,omitempty"`
	CreatedAt     string            `json:"created_at"`
	CreatedBy     Actor             `json:"created_by"`
	TraceID       string            `json:"trace_id,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

type GitMetadata struct {
	Repo   string `json:"repo,omitempty"`
	SHA    string `json:"sha,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Branch string `json:"branch,omitempty"`
}

type CIMetadata struct {
	Provider string `json:"provider,omitempty"`
	RunID    string `json:"run_id,omitempty"`
	RunURL   string `json:"run_url,omitempty"`
	JobURL   string `json:"job_url,omitempty"`
}

type EvidenceCheck struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	URL         string `json:"url,omitempty"`
	Summary     string `json:"summary,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type EvidenceRef struct {
	Name   string `json:"name,omitempty"`
	URI    string `json:"uri"`
	Digest string `json:"digest,omitempty"`
}

type RuntimeManifest struct {
	SchemaVersion     string            `json:"schema_version"`
	Service           string            `json:"service"`
	Env               string            `json:"env"`
	ReleaseID         string            `json:"release_id"`
	PackageLockDigest string            `json:"package_lock_digest,omitempty"`
	Command           []string          `json:"command,omitempty"`
	EnvVars           map[string]string `json:"env_vars,omitempty"`
	HealthCheck       *HealthCheck      `json:"health_check,omitempty"`
	Metrics           *MetricsConfig    `json:"metrics,omitempty"`
	CreatedAt         string            `json:"created_at"`
}

type MetricsConfig struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"`
	Port    int    `json:"port,omitempty"`
}

type HealthCheck struct {
	Type     string   `json:"type"`
	Path     string   `json:"path,omitempty"`
	Port     int      `json:"port,omitempty"`
	Command  []string `json:"command,omitempty"`
	Interval string   `json:"interval,omitempty"`
	Timeout  string   `json:"timeout,omitempty"`
}

type SagaIntent struct {
	SchemaVersion     string             `json:"schema_version"`
	SagaID            string             `json:"saga_id"`
	Kind              string             `json:"kind"`
	Target            Target             `json:"target"`
	Actor             Actor              `json:"actor"`
	TraceID           string             `json:"trace_id"`
	Risk              Risk               `json:"risk"`
	Reversibility     Reversibility      `json:"reversibility"`
	PackageLockDigest string             `json:"package_lock_digest,omitempty"`
	Summary           string             `json:"summary"`
	CreatedAt         string             `json:"created_at"`
	Params            json.RawMessage    `json:"params,omitempty"`
	Package           *PackageProvenance `json:"package,omitempty"`
}

type SagaGraph struct {
	SchemaVersion string             `json:"schema_version"`
	SagaID        string             `json:"saga_id"`
	Nodes         []SagaNode         `json:"nodes"`
	Edges         []SagaEdge         `json:"edges,omitempty"`
	CreatedAt     string             `json:"created_at"`
	Package       *PackageProvenance `json:"package,omitempty"`
}

type SagaNode struct {
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	Requires      []string          `json:"requires,omitempty"`
	Params        json.RawMessage   `json:"params,omitempty"`
	Retry         *RetryPolicy      `json:"retry,omitempty"`
	Compensate    *CompensationSpec `json:"compensate,omitempty"`
	Risk          Risk              `json:"risk,omitempty"`
	Reversibility Reversibility     `json:"reversibility,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts int    `json:"max_attempts,omitempty"`
	Backoff     string `json:"backoff,omitempty"`
}

type CompensationSpec struct {
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
	Ownership     *ResourceOwnership  `json:"ownership,omitempty"`
	Tags          map[string]string   `json:"tags,omitempty"`
	ObservedAt    string              `json:"observed_at"`
}

type ResourceOwnership struct {
	Mode      string `json:"mode"`
	Source    string `json:"source,omitempty"`
	ManagedBy string `json:"managed_by,omitempty"`
	AdoptedAt string `json:"adopted_at,omitempty"`
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

type SagaEvent = Event

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
		Version:       1,
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
