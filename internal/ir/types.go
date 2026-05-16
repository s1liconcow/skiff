// Package ir contains Skiff's provider-neutral implementation graph.
package ir

const SchemaVersion = "skiff.ir/v1alpha1"

type Graph struct {
	SchemaVersion string    `json:"schema_version"`
	Service       string    `json:"service"`
	Env           string    `json:"env"`
	Resources     Resources `json:"resources"`
}

type Resources struct {
	WorkloadIdentities []WorkloadIdentity `json:"workload_identities,omitempty"`
	IAMRoles           []IAMRole          `json:"iam_roles,omitempty"`
	SecurityGroups     []SecurityGroup    `json:"security_groups,omitempty"`
	LogConfigs         []LogConfig        `json:"log_configs,omitempty"`
	MetricConfigs      []MetricConfig     `json:"metric_configs,omitempty"`
	TargetGroups       []TargetGroup      `json:"target_groups,omitempty"`
	Listeners          []Listener         `json:"listeners,omitempty"`
	InstanceTemplates  []InstanceTemplate `json:"instance_templates,omitempty"`
	AutoscalingGroups  []AutoscalingGroup `json:"autoscaling_groups,omitempty"`
	RuntimeManifests   []RuntimeManifest  `json:"runtime_manifests,omitempty"`
}

type SourceRef struct {
	Path string `json:"path"`
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
	Enabled bool   `json:"enabled"`
	CertRef string `json:"cert_ref,omitempty"`
}

type SecretRef struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}
