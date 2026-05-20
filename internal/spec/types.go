package spec

import "encoding/json"

const APIVersion = "skiff.dev/v1alpha1"

type Kind string

const (
	KindService          Kind = "Service"
	KindWorker           Kind = "Worker"
	KindJob              Kind = "Job"
	KindManagedDatabase  Kind = "ManagedDatabase"
	KindStatefulGroup    Kind = "StatefulGroup"
	KindStack            Kind = "Stack"
	KindMultiRegionStack Kind = "MultiRegionStack"
)

type Document struct {
	APIVersion      string                     `json:"apiVersion"`
	Kind            Kind                       `json:"kind"`
	Metadata        Metadata                   `json:"metadata"`
	Artifact        *Artifact                  `json:"artifact,omitempty"`
	Runtime         Runtime                    `json:"runtime,omitempty"`
	Machine         Machine                    `json:"machine,omitempty"`
	Scale           Scale                      `json:"scale,omitempty"`
	Network         Network                    `json:"network,omitempty"`
	Rollout         Rollout                    `json:"rollout,omitempty"`
	Secrets         []SecretRef                `json:"secrets,omitempty"`
	Addons          []Addon                    `json:"addons,omitempty"`
	ManagedDatabase *ManagedDatabase           `json:"database,omitempty"`
	StatefulGroup   *StatefulGroup             `json:"stateful,omitempty"`
	Stack           *Stack                     `json:"stack,omitempty"`
	MultiRegion     *MultiRegionStack          `json:"multiRegion,omitempty"`
	Provider        map[string]json.RawMessage `json:"provider,omitempty"`
}

type Metadata struct {
	Name   string            `json:"name"`
	Env    string            `json:"env"`
	Labels map[string]string `json:"labels,omitempty"`
}

type Service struct {
	Metadata Metadata    `json:"metadata"`
	Artifact *Artifact   `json:"artifact,omitempty"`
	Runtime  Runtime     `json:"runtime,omitempty"`
	Machine  Machine     `json:"machine,omitempty"`
	Scale    Scale       `json:"scale,omitempty"`
	Network  Network     `json:"network,omitempty"`
	Rollout  Rollout     `json:"rollout,omitempty"`
	Secrets  []SecretRef `json:"secrets,omitempty"`
}

type Worker struct {
	Metadata Metadata    `json:"metadata"`
	Artifact *Artifact   `json:"artifact,omitempty"`
	Runtime  Runtime     `json:"runtime,omitempty"`
	Machine  Machine     `json:"machine,omitempty"`
	Scale    Scale       `json:"scale,omitempty"`
	Rollout  Rollout     `json:"rollout,omitempty"`
	Secrets  []SecretRef `json:"secrets,omitempty"`
}

type Job struct {
	Metadata Metadata    `json:"metadata"`
	Artifact *Artifact   `json:"artifact,omitempty"`
	Runtime  Runtime     `json:"runtime,omitempty"`
	Machine  Machine     `json:"machine,omitempty"`
	Secrets  []SecretRef `json:"secrets,omitempty"`
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
	Health        Health            `json:"health,omitempty"`
	ShutdownGrace string            `json:"shutdownGrace,omitempty"`
	Logs          Logs              `json:"logs,omitempty"`
	Metrics       Metrics           `json:"metrics,omitempty"`
}

type Health struct {
	Type     string   `json:"type,omitempty"`
	Path     string   `json:"path,omitempty"`
	Port     int      `json:"port,omitempty"`
	Command  []string `json:"command,omitempty"`
	Interval string   `json:"interval,omitempty"`
	Timeout  string   `json:"timeout,omitempty"`
}

type Logs struct {
	Enabled bool   `json:"enabled,omitempty"`
	Format  string `json:"format,omitempty"`
}

type Metrics struct {
	Enabled bool   `json:"enabled,omitempty"`
	Path    string `json:"path,omitempty"`
}

type Machine struct {
	Size string `json:"size,omitempty"`
	Arch string `json:"arch,omitempty"`
}

type Scale struct {
	Min int `json:"min,omitempty"`
	Max int `json:"max,omitempty"`
}

type Network struct {
	Ingress *Ingress `json:"ingress,omitempty"`
}

type Ingress struct {
	Type    string `json:"type"`
	Host    string `json:"host,omitempty"`
	TLS     *TLS   `json:"tls,omitempty"`
	CertRef string `json:"certRef,omitempty"`
}

type TLS struct {
	Enabled bool   `json:"enabled,omitempty"`
	CertRef string `json:"certRef,omitempty"`
}

type Rollout struct {
	Strategy          string `json:"strategy,omitempty"`
	BatchSize         int    `json:"batchSize,omitempty"`
	HealthGracePeriod string `json:"healthGracePeriod,omitempty"`
}

type SecretRef struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}

type Addon struct {
	Name   string          `json:"name"`
	Mode   string          `json:"mode,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

type ManagedDatabase struct {
	Engine  string          `json:"engine,omitempty"`
	Version string          `json:"version,omitempty"`
	Size    string          `json:"size,omitempty"`
	Storage DatabaseStorage `json:"storage,omitempty"`
	Backups DatabaseBackups `json:"backups,omitempty"`
	Region  string          `json:"region,omitempty"`
	Network DatabaseNetwork `json:"network,omitempty"`
}

type DatabaseStorage struct {
	SizeGB    int    `json:"sizeGB,omitempty"`
	Type      string `json:"type,omitempty"`
	Encrypted bool   `json:"encrypted,omitempty"`
}

type DatabaseBackups struct {
	Enabled       bool   `json:"enabled,omitempty"`
	RetentionDays int    `json:"retentionDays,omitempty"`
	Window        string `json:"window,omitempty"`
}

type DatabaseNetwork struct {
	Private           bool     `json:"private,omitempty"`
	SubnetGroupRef    string   `json:"subnetGroupRef,omitempty"`
	SecurityGroupRefs []string `json:"securityGroupRefs,omitempty"`
}

type StatefulGroup struct {
	Replicas int                  `json:"replicas,omitempty"`
	Members  []StatefulMemberSpec `json:"members,omitempty"`
	Volume   Volume               `json:"volume,omitempty"`
	Identity StatefulIdentity     `json:"identity,omitempty"`
	Recipe   StatefulRecipe       `json:"recipe,omitempty"`
	Update   StatefulUpdate       `json:"update,omitempty"`
}

type StatefulMemberSpec struct {
	Ordinal int    `json:"ordinal"`
	Zone    string `json:"zone,omitempty"`
	DNSName string `json:"dnsName,omitempty"`
}

type Volume struct {
	Size      string `json:"size,omitempty"`
	Type      string `json:"type,omitempty"`
	MountPath string `json:"mountPath,omitempty"`
	Encrypted bool   `json:"encrypted,omitempty"`
}

type StatefulIdentity struct {
	DNSZoneRef     string `json:"dnsZoneRef,omitempty"`
	HostnamePrefix string `json:"hostnamePrefix,omitempty"`
}

type StatefulRecipe struct {
	Name   string          `json:"name,omitempty"`
	Ref    string          `json:"ref,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

type StatefulUpdate struct {
	Strategy string `json:"strategy,omitempty"`
}

type Stack struct {
	Services     []StackService     `json:"services,omitempty"`
	Databases    []StackDatabase    `json:"databases,omitempty"`
	ObjectStores []StackObjectStore `json:"objectStores,omitempty"`
	Dependencies []StackDependency  `json:"dependencies,omitempty"`
	Bindings     []StackBinding     `json:"bindings,omitempty"`
}

type StackService struct {
	Name     string      `json:"name"`
	Ref      string      `json:"ref,omitempty"`
	Artifact *Artifact   `json:"artifact,omitempty"`
	Runtime  Runtime     `json:"runtime,omitempty"`
	Machine  Machine     `json:"machine,omitempty"`
	Scale    Scale       `json:"scale,omitempty"`
	Network  Network     `json:"network,omitempty"`
	Rollout  Rollout     `json:"rollout,omitempty"`
	Secrets  []SecretRef `json:"secrets,omitempty"`
}

type StackDatabase struct {
	Name string `json:"name"`
	Ref  string `json:"ref,omitempty"`
	ManagedDatabase
}

type StackObjectStore struct {
	Name      string `json:"name"`
	Ref       string `json:"ref,omitempty"`
	URI       string `json:"uri,omitempty"`
	Purpose   string `json:"purpose,omitempty"`
	Access    string `json:"access,omitempty"`
	Versioned bool   `json:"versioned,omitempty"`
	Encrypted bool   `json:"encrypted,omitempty"`
}

type StackDependency struct {
	Name    string          `json:"name"`
	Uses    string          `json:"uses"`
	Version string          `json:"version,omitempty"`
	Config  json.RawMessage `json:"config,omitempty"`
}

type StackBinding struct {
	From string `json:"from"`
	To   string `json:"to"`
	As   string `json:"as"`
}

type MultiRegionStack struct {
	PrimaryRegion       string                    `json:"primaryRegion"`
	SecondaryRegions    []string                  `json:"secondaryRegions,omitempty"`
	Service             StackService              `json:"service"`
	Database            StackDatabase             `json:"database"`
	Binding             StackBinding              `json:"binding"`
	TrafficPolicy       MultiRegionTrafficPolicy  `json:"trafficPolicy,omitempty"`
	DatabaseReplication DatabaseReplication       `json:"databaseReplication,omitempty"`
	FailoverPolicy      MultiRegionFailoverPolicy `json:"failoverPolicy,omitempty"`
}

type MultiRegionTrafficPolicy struct {
	Mode    string          `json:"mode,omitempty"`
	Host    string          `json:"host,omitempty"`
	Weights []TrafficWeight `json:"weights,omitempty"`
}

type TrafficWeight struct {
	Region string `json:"region"`
	Weight int    `json:"weight"`
}

type DatabaseReplication struct {
	Mode          string `json:"mode,omitempty"`
	MaxReplicaLag string `json:"maxReplicaLag,omitempty"`
}

type MultiRegionFailoverPolicy struct {
	RequireApproval bool   `json:"requireApproval,omitempty"`
	FreezeWrites    bool   `json:"freezeWrites,omitempty"`
	MaxReplicaLag   string `json:"maxReplicaLag,omitempty"`
	Failback        string `json:"failback,omitempty"`
}
