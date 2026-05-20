// Package ir contains Skiff's provider-neutral implementation graph.
package ir

import "encoding/json"

const SchemaVersion = "skiff.ir/v1alpha1"

type Graph struct {
	SchemaVersion     string              `json:"schema_version"`
	Service           string              `json:"service"`
	Env               string              `json:"env"`
	PackageLockDigest string              `json:"package_lock_digest,omitempty"`
	Packages          []PackageProvenance `json:"packages,omitempty"`
	Resources         Resources           `json:"resources"`
}

type Resources struct {
	WorkloadIdentities  []WorkloadIdentity   `json:"workload_identities,omitempty"`
	IAMRoles            []IAMRole            `json:"iam_roles,omitempty"`
	SecurityGroups      []SecurityGroup      `json:"security_groups,omitempty"`
	LogConfigs          []LogConfig          `json:"log_configs,omitempty"`
	MetricConfigs       []MetricConfig       `json:"metric_configs,omitempty"`
	TargetGroups        []TargetGroup        `json:"target_groups,omitempty"`
	Listeners           []Listener           `json:"listeners,omitempty"`
	ManagedDatabases    []ManagedDatabase    `json:"managed_databases,omitempty"`
	DatabaseSecrets     []DatabaseSecret     `json:"database_secrets,omitempty"`
	DatabaseBindings    []DatabaseBinding    `json:"database_bindings,omitempty"`
	ObjectStores        []ObjectStore        `json:"object_stores,omitempty"`
	ObjectStoreBindings []ObjectStoreBinding `json:"object_store_bindings,omitempty"`
	GlobalTraffic       []GlobalTraffic      `json:"global_traffic,omitempty"`
	InstanceTemplates   []InstanceTemplate   `json:"instance_templates,omitempty"`
	AutoscalingGroups   []AutoscalingGroup   `json:"autoscaling_groups,omitempty"`
	RuntimeManifests    []RuntimeManifest    `json:"runtime_manifests,omitempty"`
	StatefulGroups      []StatefulGroup      `json:"stateful_groups,omitempty"`
	StatefulMembers     []StatefulMember     `json:"stateful_members,omitempty"`
	StatefulVolumes     []StatefulVolume     `json:"stateful_volumes,omitempty"`
	StatefulDNS         []StatefulDNS        `json:"stateful_dns,omitempty"`
	StatefulRecipes     []StatefulRecipe     `json:"stateful_recipes,omitempty"`
	SnapshotPolicies    []SnapshotPolicy     `json:"snapshot_policies,omitempty"`
	UpdatePolicies      []UpdatePolicy       `json:"update_policies,omitempty"`
	PackageOperations   []PackageOperation   `json:"package_operations,omitempty"`
}

type SourceRef struct {
	Path           string `json:"path,omitempty"`
	Package        string `json:"package,omitempty"`
	Version        string `json:"version,omitempty"`
	Digest         string `json:"digest,omitempty"`
	ManifestDigest string `json:"manifest_digest,omitempty"`
	LockfileDigest string `json:"lockfile_digest,omitempty"`
}

type PackageProvenance struct {
	Name           string `json:"name,omitempty"`
	Ref            string `json:"ref,omitempty"`
	Version        string `json:"version,omitempty"`
	Digest         string `json:"digest,omitempty"`
	ManifestDigest string `json:"manifest_digest,omitempty"`
	LockfileDigest string `json:"lockfile_digest,omitempty"`
}

type ResourceMeta struct {
	LogicalID string            `json:"logical_id"`
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Tags      map[string]string `json:"tags,omitempty"`
	Source    []SourceRef       `json:"source,omitempty"`
}

type WorkloadIdentity struct {
	Meta ResourceMeta `json:"meta"`
}

type IAMRole struct {
	Meta                ResourceMeta `json:"meta"`
	WorkloadIdentityRef string       `json:"workload_identity_ref"`
	SecretRefs          []SecretRef  `json:"secret_refs,omitempty"`
}

type SecurityGroup struct {
	Meta  ResourceMeta   `json:"meta"`
	Rules []SecurityRule `json:"rules,omitempty"`
}

type SecurityRule struct {
	Direction   string `json:"direction"`
	Protocol    string `json:"protocol"`
	FromPort    int    `json:"from_port,omitempty"`
	ToPort      int    `json:"to_port,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	Description string `json:"description,omitempty"`
}

type LogConfig struct {
	Meta    ResourceMeta `json:"meta"`
	Enabled bool         `json:"enabled"`
	Format  string       `json:"format"`
}

type MetricConfig struct {
	Meta    ResourceMeta `json:"meta"`
	Enabled bool         `json:"enabled"`
	Path    string       `json:"path,omitempty"`
}

type TargetGroup struct {
	Meta        ResourceMeta `json:"meta"`
	Protocol    string       `json:"protocol"`
	Port        int          `json:"port"`
	HealthCheck HealthCheck  `json:"health_check"`
}

type Listener struct {
	Meta           ResourceMeta `json:"meta"`
	Visibility     string       `json:"visibility"`
	Protocol       string       `json:"protocol"`
	Port           int          `json:"port"`
	Host           string       `json:"host,omitempty"`
	TLS            TLS          `json:"tls,omitempty"`
	TargetGroupRef string       `json:"target_group_ref"`
}

type ManagedDatabase struct {
	Meta                ResourceMeta    `json:"meta"`
	Engine              string          `json:"engine"`
	Version             string          `json:"version"`
	Size                string          `json:"size"`
	Port                int             `json:"port"`
	Region              string          `json:"region,omitempty"`
	Role                string          `json:"role,omitempty"`
	PrimaryRegion       string          `json:"primary_region,omitempty"`
	ReplicaOfRef        string          `json:"replica_of_ref,omitempty"`
	ReplicationMode     string          `json:"replication_mode,omitempty"`
	FailoverPolicy      FailoverPolicy  `json:"failover_policy,omitempty"`
	Storage             DatabaseStorage `json:"storage"`
	Backups             DatabaseBackups `json:"backups"`
	Network             DatabaseNetwork `json:"network"`
	SecurityGroupRefs   []string        `json:"security_group_refs,omitempty"`
	ConnectionSecretRef string          `json:"connection_secret_ref"`
}

type FailoverPolicy struct {
	MaxReplicaLag   string `json:"max_replica_lag,omitempty"`
	FreezeWrites    bool   `json:"freeze_writes,omitempty"`
	RequireApproval bool   `json:"require_approval,omitempty"`
}

type DatabaseStorage struct {
	SizeGB    int    `json:"size_gb"`
	Type      string `json:"type"`
	Encrypted bool   `json:"encrypted"`
}

type DatabaseBackups struct {
	Enabled       bool   `json:"enabled"`
	RetentionDays int    `json:"retention_days"`
	Window        string `json:"window,omitempty"`
}

type DatabaseNetwork struct {
	Private           bool     `json:"private"`
	SubnetGroupRef    string   `json:"subnet_group_ref,omitempty"`
	SecurityGroupRefs []string `json:"security_group_refs,omitempty"`
}

type DatabaseSecret struct {
	Meta        ResourceMeta `json:"meta"`
	DatabaseRef string       `json:"database_ref"`
	Name        string       `json:"name"`
	Ref         string       `json:"ref"`
	EnvName     string       `json:"env_name,omitempty"`
}

type DatabaseBinding struct {
	Meta        ResourceMeta `json:"meta"`
	FromService string       `json:"from_service"`
	DatabaseRef string       `json:"database_ref"`
	EnvName     string       `json:"env_name"`
	SecretRef   string       `json:"secret_ref"`
}

type ObjectStore struct {
	Meta      ResourceMeta `json:"meta"`
	URI       string       `json:"uri"`
	Bucket    string       `json:"bucket,omitempty"`
	Prefix    string       `json:"prefix,omitempty"`
	Purpose   string       `json:"purpose,omitempty"`
	Access    string       `json:"access"`
	Versioned bool         `json:"versioned"`
	Encrypted bool         `json:"encrypted"`
}

type ObjectStoreBinding struct {
	Meta           ResourceMeta `json:"meta"`
	FromService    string       `json:"from_service"`
	ObjectStoreRef string       `json:"object_store_ref"`
	EnvName        string       `json:"env_name"`
	URI            string       `json:"uri"`
	Access         string       `json:"access"`
}

type GlobalTraffic struct {
	Meta          ResourceMeta    `json:"meta"`
	Host          string          `json:"host,omitempty"`
	Mode          string          `json:"mode"`
	PrimaryRegion string          `json:"primary_region"`
	Regions       []TrafficRegion `json:"regions"`
}

type TrafficRegion struct {
	Region         string `json:"region"`
	ServiceRef     string `json:"service_ref"`
	TargetGroupRef string `json:"target_group_ref,omitempty"`
	Weight         int    `json:"weight"`
}

type InstanceTemplate struct {
	Meta                ResourceMeta `json:"meta"`
	Machine             Machine      `json:"machine"`
	Artifact            Artifact     `json:"artifact"`
	Runtime             Runtime      `json:"runtime"`
	WorkloadIdentityRef string       `json:"workload_identity_ref"`
	IAMRoleRef          string       `json:"iam_role_ref"`
	SecurityGroupRefs   []string     `json:"security_group_refs,omitempty"`
	LogConfigRef        string       `json:"log_config_ref,omitempty"`
	MetricConfigRef     string       `json:"metric_config_ref,omitempty"`
}

type AutoscalingGroup struct {
	Meta                ResourceMeta `json:"meta"`
	Min                 int          `json:"min"`
	Max                 int          `json:"max"`
	InstanceTemplateRef string       `json:"instance_template_ref"`
	TargetGroupRefs     []string     `json:"target_group_refs,omitempty"`
	Rollout             Rollout      `json:"rollout"`
}

type RuntimeManifest struct {
	Meta        ResourceMeta      `json:"meta"`
	Artifact    Artifact          `json:"artifact"`
	Command     []string          `json:"command,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	SecretRefs  []SecretRef       `json:"secret_refs,omitempty"`
	HealthCheck HealthCheck       `json:"health_check"`
	Metrics     AppMetrics        `json:"metrics,omitempty"`
}

type StatefulGroup struct {
	Meta              ResourceMeta `json:"meta"`
	Replicas          int          `json:"replicas"`
	MemberRefs        []string     `json:"member_refs"`
	VolumeRefs        []string     `json:"volume_refs"`
	DNSIdentityRefs   []string     `json:"dns_identity_refs,omitempty"`
	RecipeRuntimeRef  string       `json:"recipe_runtime_ref"`
	SnapshotPolicyRef string       `json:"snapshot_policy_ref,omitempty"`
	UpdatePolicyRef   string       `json:"update_policy_ref"`
}

type StatefulMember struct {
	Meta             ResourceMeta `json:"meta"`
	Ordinal          int          `json:"ordinal"`
	Zone             string       `json:"zone,omitempty"`
	DNSName          string       `json:"dns_name,omitempty"`
	VolumeRef        string       `json:"volume_ref"`
	DNSIdentityRef   string       `json:"dns_identity_ref,omitempty"`
	RecipeRuntimeRef string       `json:"recipe_runtime_ref"`
	UpdatePolicyRef  string       `json:"update_policy_ref"`
}

type StatefulVolume struct {
	Meta          ResourceMeta `json:"meta"`
	MemberOrdinal int          `json:"member_ordinal"`
	Size          string       `json:"size"`
	Type          string       `json:"type"`
	MountPath     string       `json:"mount_path"`
	Encrypted     bool         `json:"encrypted"`
}

type StatefulDNS struct {
	Meta           ResourceMeta `json:"meta"`
	MemberOrdinal  int          `json:"member_ordinal"`
	DNSZoneRef     string       `json:"dns_zone_ref,omitempty"`
	HostnamePrefix string       `json:"hostname_prefix"`
	DNSName        string       `json:"dns_name,omitempty"`
}

type StatefulRecipe struct {
	Meta        ResourceMeta      `json:"meta"`
	Name        string            `json:"name,omitempty"`
	Ref         string            `json:"ref,omitempty"`
	Artifact    Artifact          `json:"artifact,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Ports       map[string]int    `json:"ports,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	HealthCheck HealthCheck       `json:"health_check,omitempty"`
	Metrics     AppMetrics        `json:"metrics,omitempty"`
	Config      json.RawMessage   `json:"config,omitempty"`
}

type SnapshotPolicy struct {
	Meta      ResourceMeta `json:"meta"`
	Enabled   bool         `json:"enabled"`
	Interval  string       `json:"interval,omitempty"`
	Retention string       `json:"retention,omitempty"`
}

type UpdatePolicy struct {
	Meta     ResourceMeta `json:"meta"`
	Strategy string       `json:"strategy"`
}

type PackageOperation struct {
	Meta                  ResourceMeta      `json:"meta"`
	Dependency            string            `json:"dependency"`
	Mode                  string            `json:"mode,omitempty"`
	Package               PackageProvenance `json:"package"`
	OperationProfiles     []string          `json:"operation_profiles,omitempty"`
	ManagedOperations     []string          `json:"managed_operations,omitempty"`
	SelfManagedOperations []string          `json:"self_managed_operations,omitempty"`
	PackageSteps          []string          `json:"package_steps,omitempty"`
	Config                json.RawMessage   `json:"config,omitempty"`
}

type Artifact struct {
	Type   string `json:"type"`
	Ref    string `json:"ref"`
	Digest string `json:"digest,omitempty"`
}

type Runtime struct {
	Port          int               `json:"port,omitempty"`
	Command       []string          `json:"command,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	HealthCheck   HealthCheck       `json:"health_check"`
	ShutdownGrace string            `json:"shutdown_grace,omitempty"`
}

type AppMetrics struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"`
	Port    int    `json:"port,omitempty"`
}

type HealthCheck struct {
	Type     string   `json:"type,omitempty"`
	Path     string   `json:"path,omitempty"`
	Port     int      `json:"port,omitempty"`
	Command  []string `json:"command,omitempty"`
	Interval string   `json:"interval,omitempty"`
	Timeout  string   `json:"timeout,omitempty"`
}

type Machine struct {
	Size string `json:"size"`
	Arch string `json:"arch"`
}

type Rollout struct {
	Strategy          string `json:"strategy"`
	BatchSize         int    `json:"batch_size"`
	HealthGracePeriod string `json:"health_grace_period,omitempty"`
}

type TLS struct {
	Enabled           bool               `json:"enabled"`
	CertRef           string             `json:"cert_ref,omitempty"`
	ClientCertificate *ClientCertificate `json:"client_certificate,omitempty"`
}

type ClientCertificate struct {
	Mode          string `json:"mode"`
	TrustStoreRef string `json:"trust_store_ref"`
}

type SecretRef struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}
