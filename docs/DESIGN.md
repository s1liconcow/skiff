# Skiff Implementation Design

**Status:** Draft design 2026-05-16  
**Primary implementation language:** Go  
**Initial cloud:** AWS  
**Core constraint:** object storage is the durable coordination substrate  

---

## 0. Executive summary

Skiff is a clusterless deployment platform where the cloud VM is the workload replica:

```text
VM = pod
ASG/MIG/VMSS = deployment/replica set
Cloud load balancer target group = service
Cloud IAM role = workload identity
Cloud logs/metrics = observability backend
Signed object-storage manifest = runtime desired state
```

Skiff is intentionally not a smaller Kubernetes. It compiles simple workload specs into native cloud primitives, publishes signed desired state to object storage, and uses a tiny runner on each VM to converge that VM into the desired workload state.

The revised design removes the required Postgres control database. Skiff’s durable state lives in object storage:

```text
Immutable objects     - releases, plans, operation intents, events, audit records
CAS control documents - service state, operation state, leases, stateful member state
Derived indexes       - service lists, recent events, summaries, optional SlateDB indexes
Observations          - runner snapshots, cloud resource snapshots, diagnostics
```

For AWS v1, this means S3 + KMS + IAM + EC2 Auto Scaling + ALB/NLB + CloudWatch + SSM Session Manager. S3’s strong read-after-write and list consistency, plus conditional writes using `If-None-Match` and `If-Match`, are the primitives Skiff uses for safe object creation and compare-and-swap updates.[^s3-consistency][^s3-conditional]

Skiff should be usable in two operating modes:

```text
Direct mode:
  skiff CLI/TUI/agent -> state bucket + cloud APIs

API mode:
  skiff CLI/TUI/agent -> stateless skiffd -> state bucket + cloud APIs
```

`skiffd` improves auth, policy, plugin execution, and UX, but it is not the durable control plane. If `skiffd` is down, the CLI can still inspect state, roll back, diagnose, and recover using the state bucket and cloud APIs.

The product should feel like:

```bash
skiff deploy
skiff status payments-api
skiff logs payments-api --follow
skiff doctor payments-api
skiff rollback payments-api --to previous-stable
```

For agents, every command must support deterministic JSON output, idempotency, machine-readable recommended actions, and explicit safety classification.

---

## 1. Design goals

### 1.1 Primary goals

1. **Make the VM the pod.** One workload replica per VM by default.
2. **Require only object storage and cloud IAM for Skiff state.** No required Postgres, Redis, queue, etcd, or scheduler database.
3. **Use native cloud primitives.** ASGs, load balancers, IAM, KMS, CloudWatch, SSM, managed databases.
4. **Stay secure by default.** Signed releases, digest-pinned artifacts, least-privilege IAM, no SSH ingress, KMS encryption, conditional writes, Object Lock optional for immutable history.
5. **Make happy paths extremely simple.** API server, worker, job, managed Postgres, multi-region service, Kubernetes migration.
6. **Expose complexity rather than hiding it.** `skiff explain`, `skiff doctor`, `skiff plan`, and object-state files should make the platform legible.
7. **Treat agents as first-class operators.** JSON output, action graphs, failure taxonomy, deterministic recovery commands.
8. **Support incremental adoption.** Kubernetes importer, Terraform generator/adopter, shadow deployments, weighted cutovers.
9. **Be composable without becoming Kubernetes.** Plugins may add capabilities like mTLS, WAF, egress policy, or stateful recipes, but plugins patch typed IR and runtime manifests rather than running arbitrary controllers.

### 1.2 Non-goals

Skiff should explicitly avoid:

```text
- generic bin-packing scheduler
- arbitrary Kubernetes YAML as the native API
- Helm compatibility as a core feature
- third-party operator/CRD ecosystem
- in-house secret store
- in-house metrics/logs backend as the default
- service mesh by default
- multi-service VM packing as the prod default
- SSH-first debugging
- mutable in-place patching as the normal deploy mechanism
- object storage as a hot metrics/log-search backend
```

---

## 2. Conceptual model

### 2.1 Workload classes

Skiff supports these workload classes:

| Class | Description | Cloud mapping |
|---|---|---|
| `Service` | Long-running HTTP/TCP service | ASG + target group + listener |
| `Worker` | Long-running queue or stream consumer | ASG + autoscaling policy, no ingress |
| `Job` | Ephemeral command execution | one-shot VM launch, logs/artifacts, terminate |
| `ManagedPostgres` | Cloud-managed Postgres/Aurora dependency | RDS/Aurora, secrets, network binding |
| `StatefulGroup` | Named VM members with durable storage | EC2 + EBS + DNS + recipes + fencing |

The default recommendation is still managed state. Skiff should make running a managed database with an API trivial; self-managed stateful groups are for deliberate cases.

### 2.2 Core mapping

| Skiff | AWS v1 |
|---|---|
| Workload replica | EC2 instance |
| Workload identity | IAM instance profile |
| Deployment | EC2 Auto Scaling Group |
| Rollout | ASG Instance Refresh |
| Rollback | ASG Instance Refresh rollback or previous release pointer |
| Ingress | ALB/NLB listener + target group |
| Health | ALB/NLB target health + runner health |
| Release ledger | S3 signed object graph |
| Coordination | S3 control documents updated by CAS |
| Secrets | Secrets Manager + KMS |
| Hot logs | CloudWatch Logs |
| Log archive | S3 |
| Metrics | CloudWatch and/or OTLP backend |
| Debug | SSM Session Manager |

ASG Instance Refresh is a good rollout primitive because AWS explicitly supports rolling replacement when AMIs, user data, launch templates, or other instance settings change.[^asg-instance-refresh] AWS also supports rollback of an in-progress instance refresh and auto rollback on failures or CloudWatch alarms.[^asg-rollback]

---

## 3. High-level architecture

```text
                           ┌────────────────────────────┐
                           │ skiff CLI / TUI / agents   │
                           └──────────────┬─────────────┘
                                          │ direct mode or API mode
                                          v
                       ┌────────────────────────────────────┐
                       │ optional stateless skiffd          │
                       │ auth, policy, plugins, UX, API     │
                       └──────────────┬─────────────────────┘
                                      │
                                      v
                       ┌────────────────────────────────────┐
                       │ object storage state bucket         │
                       │ releases, control docs, events      │
                       │ derived indexes, optional SlateDB   │
                       └──────────────┬─────────────────────┘
                                      │
                                      v
                       ┌────────────────────────────────────┐
                       │ provider layer                      │
                       │ AWS v1, GCP/Azure later             │
                       └──────────────┬─────────────────────┘
                                      │
                                      v
       ┌────────────────────────────────────────────────────────────────┐
       │ AWS: S3, KMS, IAM, EC2, ASG, ALB/NLB, CloudWatch, SSM, RDS      │
       └──────────────────────────────┬─────────────────────────────────┘
                                      │
                                      v
                       ┌────────────────────────────────────┐
                       │ EC2 VM = Skiff workload replica     │
                       │ skiff-runner                        │
                       │ app process                         │
                       │ local log/metric collector          │
                       └────────────────────────────────────┘
```

### 3.1 Component overview

| Binary | Required? | Responsibility |
|---|---:|---|
| `skiff` | Yes | CLI/TUI, direct-mode deploys, recovery, agent interface |
| `skiffd` | Optional | Stateless API server, auth broker, policy runner, plugin host, TUI backend |
| `skiff-runner` | Yes on VMs | Fetch signed release, verify, prepare artifact, start app, report status |
| `skiff-worker` | Optional | Resume operations, rebuild indexes, run GC, scheduled diagnostics |
| `skiff-plugin` | Dev tool | Validate/test plugins locally |
| `skiff-indexer` | Optional | Single-writer indexer for raw indexes or SlateDB indexes |

### 3.2 Direct mode vs API mode

Direct mode:

```text
skiff CLI -> S3 state bucket + AWS APIs
```

API mode:

```text
skiff CLI -> skiffd -> S3 state bucket + AWS APIs
```

Direct mode is the recovery path. API mode is the ergonomic enterprise path.

---

## 4. Repository layout

```text
skiff/
  cmd/
    skiff/                         # CLI and TUI entrypoint
    skiffd/                        # optional stateless API server
    skiff-runner/                  # VM-local runner
    skiff-worker/                  # optional operation/index/GC worker
    skiff-indexer/                 # optional single-writer indexer
    skiff-plugin/                  # plugin dev/test runner

  api/
    proto/                         # gRPC/Connect API definitions
    openapi/                       # generated HTTP API docs
    gen/                           # generated clients/servers

  internal/
    app/                           # application wiring
    auth/                          # authn/authz, OIDC, IAM identity
    audit/                         # audit record generation
    compiler/                      # spec -> IR pipeline
    config/                        # CLI/server/runner config loading
    doctor/                        # diagnostics engine
    events/                        # event types and event scanning
    ir/                            # typed provider-neutral graph
    policy/                        # built-in policy checks
    release/                       # signing, verification, manifest publication
    solve/                         # agent action graph generation
    state/                         # logical state API over object storage
      objstore/                    # S3/GCS/Azure/memory object store implementations
      control/                     # CAS and lease helpers
      eventlog/                    # append-only event writing/scanning
      indexes/                     # raw index and SlateDB-backed index stores
      paths/                       # canonical key builder
    runner/                        # runner FSM, artifact runtime, systemd renderer
    observability/                 # logs/metrics/traces configs and adapters
    tui/                           # Bubble Tea TUI models/views
    terraform/                     # renderer/adopter/generator
    adoption/                      # Kubernetes import and resource adoption
    provider/
      provider.go                  # provider interface
      conformance/                 # provider conformance tests
      aws/
        aws.go
        asg.go
        ec2.go
        elb.go
        iam.go
        kms.go
        logs.go
        metrics.go
        rds.go
        route53.go
        s3.go
        ssm.go
    plugins/
      host/                        # gRPC plugin host
      wasm/                        # WASM validator/mutator host
      registry/                    # plugin registry and signature checks

  pkg/
    spec/                          # public Skiff spec Go types
    sdk/                           # client SDK for humans/agents/tools
    pluginapi/                     # stable plugin API
    runnerapi/                     # runner addon API

  examples/
    service-basic/
    api-postgres/
    api-postgres-multiregion/
    worker-sqs/
    job-report/
    stateful-postgres-single/
    mtls-service-to-service/
    kubernetes-import/
    terraform-owned-infra/

  testdata/
    specs/
    golden-ir/
    golden-terraform/
    golden-events/

  docs/
    architecture.md
    object-state.md
    security.md
    plugin-authoring.md
    aws-provider.md
    migration-kubernetes.md
    cicd.md
    agent-guide.md
```

---

## 5. Public spec examples

### 5.1 Basic HTTP service

```yaml
apiVersion: skiff.dev/v1alpha1
kind: Service

metadata:
  name: payments-api
  env: prod

artifact:
  type: oci
  ref: registry.example.com/payments-api@sha256:abc123
  command: ["/app/payments-api"]

runtime:
  port: 8080
  health:
    path: /healthz
    interval: 10s
    timeout: 2s
    unhealthyThreshold: 3
  shutdown:
    grace: 30s

machine:
  size: small
  arch: arm64

scale:
  min: 6
  max: 40
  policies:
    - type: requestCountPerTarget
      target: 1000
    - type: cpu
      target: 65

network:
  ingress:
    type: public-http
    host: payments.example.com
    tls: managed
  egress:
    default: deny
    allow:
      - type: dns
        name: api.stripe.com
      - type: service
        name: orders-api

identity:
  secrets:
    read:
      - prod/payments/*
  kms:
    decrypt:
      - alias/prod-payments

logs:
  format: ndjson
  hot: cloudwatch
  archive:
    type: s3
    retention: 365d

metrics:
  prometheus:
    port: 9090
    path: /metrics
  defaults: true
```

### 5.2 API server + managed Postgres

```yaml
apiVersion: skiff.dev/v1alpha1
kind: Stack

metadata:
  name: payments
  env: prod

components:
  - kind: Service
    name: api
    artifact:
      type: oci
      ref: registry.example.com/payments-api@sha256:abc123
    runtime:
      port: 8080
      health:
        path: /healthz
    scale:
      min: 3
      max: 30
    identity:
      secrets:
        read:
          - prod/payments/*

  - kind: ManagedPostgres
    name: db
    engine: aurora-postgresql
    version: "16"
    topology:
      type: multi-az
    size: small
    storage:
      gb: 100
    backups:
      retention: 7d

bindings:
  - from: api
    to: db
    as: DATABASE_URL
```

### 5.3 API server + multi-region Postgres

For AWS v1, Skiff should implement this as a managed Aurora Global Database recipe, not self-managed replication. Aurora Global Database spans multiple AWS Regions, has one primary DB cluster and up to 10 secondary clusters, and automatically synchronizes changes from the primary to secondary clusters.[^aurora-global]

```yaml
apiVersion: skiff.dev/v1alpha1
kind: Stack

metadata:
  name: ledger
  env: prod

regions:
  primary: us-west-2
  secondaries:
    - us-east-1

components:
  - kind: Service
    name: api
    deploy:
      regions:
        - us-west-2
        - us-east-1
    artifact:
      type: oci
      ref: registry.example.com/ledger-api@sha256:abc123
    runtime:
      port: 8080
      health:
        path: /healthz
    scale:
      perRegion:
        us-west-2:
          min: 4
          max: 40
        us-east-1:
          min: 2
          max: 20

  - kind: ManagedPostgres
    name: db
    engine: aurora-postgresql
    version: "16"
    topology:
      type: global
      primaryRegion: us-west-2
      secondaryRegions:
        - us-east-1
    failover:
      mode: manual-approved
      runbook: aurora-global-managed-failover

bindings:
  - from: api
    to: db
    as: DATABASE_URL
    mode: writer
  - from: api
    to: db
    as: DATABASE_READ_URL
    mode: nearest-reader
```

Skiff’s job is to provision, bind, observe, fail over, and update app configuration. Skiff should not implement database replication.

---

## 6. Public Go spec types

```go
package spec

type TypeMeta struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind" yaml:"kind"`
}

type ObjectMeta struct {
	Name string            `json:"name" yaml:"name"`
	Env  string            `json:"env" yaml:"env"`
	Tags map[string]string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

type Service struct {
	TypeMeta `json:",inline" yaml:",inline"`
	Metadata ObjectMeta `json:"metadata" yaml:"metadata"`

	Artifact ArtifactSpec `json:"artifact" yaml:"artifact"`
	Runtime  RuntimeSpec  `json:"runtime" yaml:"runtime"`
	Machine  MachineSpec  `json:"machine" yaml:"machine"`
	Scale    ScaleSpec    `json:"scale" yaml:"scale"`
	Network  NetworkSpec  `json:"network,omitempty" yaml:"network,omitempty"`
	Identity IdentitySpec `json:"identity,omitempty" yaml:"identity,omitempty"`
	Logs     LogsSpec     `json:"logs,omitempty" yaml:"logs,omitempty"`
	Metrics  MetricsSpec  `json:"metrics,omitempty" yaml:"metrics,omitempty"`
	Addons   []AddonSpec  `json:"addons,omitempty" yaml:"addons,omitempty"`
}

type ArtifactSpec struct {
	Type    string   `json:"type" yaml:"type"` // oci, tar, binary, ami
	Ref     string   `json:"ref" yaml:"ref"`
	Command []string `json:"command,omitempty" yaml:"command,omitempty"`
	Digest  string   `json:"digest,omitempty" yaml:"digest,omitempty"`
}

type RuntimeSpec struct {
	Port     int               `json:"port" yaml:"port"`
	Health   HealthSpec        `json:"health" yaml:"health"`
	Shutdown ShutdownSpec      `json:"shutdown,omitempty" yaml:"shutdown,omitempty"`
	Env      map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
}

type HealthSpec struct {
	Path               string `json:"path,omitempty" yaml:"path,omitempty"`
	Command            string `json:"command,omitempty" yaml:"command,omitempty"`
	Interval           string `json:"interval,omitempty" yaml:"interval,omitempty"`
	Timeout            string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	UnhealthyThreshold int    `json:"unhealthyThreshold,omitempty" yaml:"unhealthyThreshold,omitempty"`
}

type MachineSpec struct {
	Size string `json:"size" yaml:"size"` // nano, small, medium, large
	Arch string `json:"arch,omitempty" yaml:"arch,omitempty"`
}

type ScaleSpec struct {
	Min      int            `json:"min" yaml:"min"`
	Max      int            `json:"max" yaml:"max"`
	Policies []ScalePolicy  `json:"policies,omitempty" yaml:"policies,omitempty"`
	WarmPool *WarmPoolSpec  `json:"warmPool,omitempty" yaml:"warmPool,omitempty"`
}

type AddonSpec struct {
	Name   string                 `json:"name" yaml:"name"`
	Mode   string                 `json:"mode,omitempty" yaml:"mode,omitempty"`
	Config map[string]interface{} `json:"config,omitempty" yaml:"config,omitempty"`
}
```

---

## 7. Compiler and typed IR

Skiff compiles public specs into a provider-neutral IR. The IR is cloud-shaped, not Kubernetes-shaped.

### 7.1 Compiler pipeline

```text
parse YAML/JSON
  -> apply environment defaults
  -> validate public spec
  -> normalize names/tags
  -> compile base IR
  -> run capability plugins
  -> run policy checks
  -> lower to provider plan
  -> publish signed release
  -> apply or emit Terraform
```

### 7.2 IR sketch

```go
package ir

type Graph struct {
	ID        string
	Service   ServiceIdentity
	Release   ReleaseIdentity
	Resources []Resource
	Edges     []Dependency
	Policies  []Policy
	Runtime   RuntimeManifest
}

type Resource interface {
	ResourceID() ResourceID
	Kind() ResourceKind
	Tags() map[string]string
}

type AutoScaleGroup struct {
	ID                 ResourceID
	Min                int
	Max                int
	Desired            *int
	TemplateRef        string
	TargetGroups       []string
	HealthGraceSeconds int
	TagsMap            map[string]string
}

type InstanceTemplate struct {
	ID              ResourceID
	BaseImage       string
	MachineType     string
	Arch            string
	InstanceProfile string
	UserData        map[string]string
	SecurityGroups  []string
	TagsMap         map[string]string
}

type TargetGroup struct {
	ID      ResourceID
	Protocol string
	Port     int
	Health   HealthCheck
	TagsMap  map[string]string
}

type IAMRole struct {
	ID       ResourceID
	AssumeBy string
	Policies []IAMPolicy
	TagsMap  map[string]string
}

type RuntimeManifest struct {
	Service string
	Env     string
	Release string
	Artifact Artifact
	Command []string
	Port    int
	Health  HealthCheck
	Logs    LogsConfig
	Metrics MetricsConfig
	Addons  []RuntimeAddon
}
```

### 7.3 Compiler entrypoint

```go
package compiler

type Compiler struct {
	Defaults DefaultsEngine
	Plugins  []CapabilityPlugin
	Policy   PolicyEngine
}

func (c *Compiler) Compile(ctx context.Context, svc spec.Service, target Target) (*ir.Graph, []Diagnostic, error) {
	var diags []Diagnostic

	svc = c.Defaults.ApplyService(svc, target)

	if d := ValidateService(svc); len(d) > 0 {
		return nil, d, ErrValidation
	}

	graph, d, err := BuildBaseGraph(svc, target)
	diags = append(diags, d...)
	if err != nil {
		return nil, diags, err
	}

	for _, p := range c.Plugins {
		patch, d, err := p.MutateIR(ctx, PluginRequest{
			Service: svc,
			Graph:   graph,
			Target:  target,
		})
		diags = append(diags, d...)
		if err != nil {
			return nil, diags, err
		}
		if err := graph.ApplyPatch(patch); err != nil {
			return nil, diags, err
		}
	}

	if d := c.Policy.Check(graph); len(d) > 0 {
		diags = append(diags, d...)
		if HasBlocking(d) {
			return nil, diags, ErrPolicyDenied
		}
	}

	return graph, diags, nil
}
```

---

## 8. Object-storage state model

### 8.1 Principle

Skiff has no required database. The source of truth is object storage.

```text
Immutable history is truth.
Control documents are coordination state.
Indexes are rebuildable acceleration.
Observations are best-effort diagnostics.
```

S3 is viable for this because it provides strong read-after-write/list consistency and conditional writes. `If-None-Match` prevents overwriting existing keys; `If-Match` compares the object’s current ETag and fails the write if it changed.[^s3-consistency][^s3-conditional]

### 8.2 Object store interface

```go
package objstore

type Object struct {
	Key         string
	Body        []byte
	ETag        string
	VersionID   string
	ContentType string
	Metadata    map[string]string
	UpdatedAt   time.Time
}

type ObjectMeta struct {
	Key       string
	ETag      string
	VersionID string
	Size      int64
	UpdatedAt time.Time
	Metadata  map[string]string
}

type PutOptions struct {
	ContentType string
	Metadata    map[string]string
	KMSKeyID    string
}

type ListOptions struct {
	Limit int32
}

type ObjectStore interface {
	Get(ctx context.Context, key string) (*Object, error)
	Head(ctx context.Context, key string) (*ObjectMeta, error)

	Create(ctx context.Context, key string, body []byte, opts PutOptions) (*ObjectMeta, error)
	CompareAndSwap(ctx context.Context, key string, previousETag string, body []byte, opts PutOptions) (*ObjectMeta, error)

	List(ctx context.Context, prefix string, opts ListOptions) ([]ObjectMeta, error)
}
```

### 8.3 State bucket layout

Prefer one state bucket per environment/account boundary:

```text
s3://skiff-state-prod/
  root/
    trust-root.json
    trust-root.bundle

  env/
    control.json

  services/
    payments-api/
      control.json

      specs/
        sha256-aaa.json

      candidates/
        rc_01HY.../
          candidate.json
          plan.json
          sbom.spdx.json
          provenance.intoto.jsonl

      releases/
        2026.05.16.1/
          release.json
          release.bundle
          runtime-manifest.json
          runtime-manifest.bundle
          ir.json
          plan.json
          sbom.spdx.json
          provenance.intoto.jsonl

      operations/
        op_01HY.../
          intent.json
          intent.bundle
          control.json
          events/
            01HY...-created.json
            01HY...-planned.json
            01HY...-rollout-started.json
            01HY...-checkpoint-10.json
            01HY...-succeeded.json
          result.json

      observations/
        instances/
          i-abc123.json
          i-def456.json

  stateful/
    orders-postgres/
      control.json
      members/
        0/control.json
        1/control.json
        2/control.json

  resources/
    by-logical/
      services/payments-api/asg.json
      services/payments-api/target-group.json
      services/payments-api/iam-role.json
    by-provider/
      aws/ec2/i-abc123.json
      aws/asg/payments-api-prod.json

  audit/
    2026/05/16/01HY...json

  indexes/
    services.json
    env-summary.json
    recent-events/2026-05-16.json

  slate/
    indexes/
      ... optional SlateDB files ...
```

### 8.4 Object classes

| Class | Examples | Write model | Correctness role |
|---|---|---|---|
| Immutable | releases, events, audit, plans | `Create` with `If-None-Match` | source of historical truth |
| Control | `services/*/control.json` | CAS with `If-Match` | coordination and current desired state |
| Derived index | `indexes/services.json` | CAS, rebuildable | fast list/TUI UX |
| Observation | runner/cloud snapshots | best-effort overwrite or CAS | diagnostics only |

### 8.5 Critical coordination rule

Do **not** use separate lock objects for important mutations.

Bad:

```text
locks/payments-api.deploy.lock
services/payments-api/control.json
```

Good:

```text
services/payments-api/control.json
```

The lease lives inside the same control document that contains the desired state. This matters because object storage gives Skiff CAS on one object, not atomic multi-object transactions.

---

## 9. Control documents and leases

### 9.1 Service control document

```go
type ServiceControl struct {
	Schema string `json:"schema"`

	Service string `json:"service"`
	Env     string `json:"env"`

	DesiredRelease string `json:"desired_release,omitempty"`
	StableRelease  string `json:"stable_release,omitempty"`

	Operation *OperationState `json:"operation,omitempty"`
	Lease     *Lease          `json:"lease,omitempty"`

	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OperationState struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	State     string    `json:"state"`
	Step      string    `json:"step,omitempty"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Lease struct {
	Owner       string    `json:"owner"`
	Token       string    `json:"token"`
	Generation  int64     `json:"generation"`
	ExpiresAt   time.Time `json:"expires_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	Purpose     string    `json:"purpose"`
}
```

### 9.2 CAS helper

```go
type Versioned[T any] struct {
	Value     T
	Key       string
	ETag      string
	VersionID string
	LoadedAt  time.Time
}

func UpdateCAS[T any](
	ctx context.Context,
	store objstore.ObjectStore,
	key string,
	mutate func(current T) (T, error),
) (*Versioned[T], error) {
	for attempt := 0; attempt < 10; attempt++ {
		obj, err := store.Get(ctx, key)
		if err != nil {
			return nil, err
		}

		var current T
		if err := json.Unmarshal(obj.Body, &current); err != nil {
			return nil, err
		}

		next, err := mutate(current)
		if err != nil {
			return nil, err
		}

		meta, err := store.CompareAndSwap(ctx, key, obj.ETag, mustJSON(next), jsonPutOptions())
		if err == nil {
			return &Versioned[T]{
				Value:     next,
				Key:       key,
				ETag:      meta.ETag,
				VersionID: meta.VersionID,
				LoadedAt:  time.Now().UTC(),
			}, nil
		}

		if errors.Is(err, objstore.ErrPreconditionFailed) || errors.Is(err, objstore.ErrConflict) {
			time.Sleep(backoff(attempt))
			continue
		}

		return nil, err
	}

	return nil, ErrCASRetriesExhausted
}
```

### 9.3 Acquire service lease

```go
func AcquireServiceLease(
	ctx context.Context,
	store objstore.ObjectStore,
	key string,
	owner string,
	purpose string,
	ttl time.Duration,
) (*Versioned[ServiceControl], error) {
	return UpdateCAS[ServiceControl](ctx, store, key, func(current ServiceControl) (ServiceControl, error) {
		now := time.Now().UTC()

		if current.Lease != nil && current.Lease.ExpiresAt.After(now) {
			return current, ErrLeaseHeld
		}

		next := current
		next.Lease = &Lease{
			Owner:       owner,
			Token:       newToken(),
			Generation:  nextLeaseGeneration(current.Lease),
			ExpiresAt:   now.Add(ttl),
			HeartbeatAt: now,
			Purpose:     purpose,
		}
		next.Version++
		next.UpdatedAt = now
		return next, nil
	})
}
```

### 9.4 Fencing model

A mutator is valid only while it holds the latest lease generation and CAS handle.

```text
lease token      - proves who believes they own the operation
lease generation - fencing epoch
ETag             - compare-and-swap handle
```

If an old deploy process wakes up after its lease expired, it cannot update the service state because another owner’s lease acquisition has changed the control object ETag.

---

## 10. Immutable operation/event model

### 10.1 Operation intent

```json
{
  "schema": "skiff.operation-intent/v1",
  "operation_id": "op_01HY...",
  "kind": "deploy",
  "service": "payments-api",
  "env": "prod",
  "actor": "ci:github-actions:payments-api",
  "requested_at": "2026-05-16T20:10:00Z",
  "candidate": "rc_01HY...",
  "release": "2026.05.16.1",
  "artifact": "registry.example.com/payments-api@sha256:abc123",
  "strategy": "canary_then_rolling"
}
```

### 10.2 Operation control document

```json
{
  "schema": "skiff.operation-control/v1",
  "operation_id": "op_01HY...",
  "kind": "deploy",
  "state": "rolling_out",
  "step": "instance_refresh_started",
  "provider": {
    "aws_instance_refresh_id": "abc123"
  },
  "lease": {
    "owner": "skiffd/i-abc123",
    "token": "lease_01HY...",
    "generation": 7,
    "expires_at": "2026-05-16T20:05:00Z",
    "purpose": "deploy"
  },
  "updated_at": "2026-05-16T20:04:00Z"
}
```

### 10.3 Append-only event

```json
{
  "schema": "skiff.event/v1",
  "operation_id": "op_01HY...",
  "event_id": "01HY...",
  "type": "rollout.checkpoint_passed",
  "message": "Canary checkpoint 10% passed",
  "created_at": "2026-05-16T20:03:10Z",
  "data": {
    "healthy_targets": 6,
    "unhealthy_targets": 0
  }
}
```

Events are written using `Create`, never overwritten. Event keys should be sortable ULIDs.

---

## 11. Release ledger

### 11.1 Release object layout

```text
services/payments-api/releases/2026.05.16.1/
  release.json
  release.bundle
  runtime-manifest.json
  runtime-manifest.bundle
  ir.json
  plan.json
  sbom.spdx.json
  provenance.intoto.jsonl
```

### 11.2 Release manifest

```json
{
  "schema": "skiff.release/v1alpha1",
  "service": "payments-api",
  "env": "prod",
  "release": "2026.05.16.1",
  "created_at": "2026-05-16T19:40:00Z",
  "expires_at": "2026-06-16T19:40:00Z",
  "artifact": {
    "type": "oci",
    "ref": "registry.example.com/payments-api@sha256:abc123",
    "digest": "sha256:abc123"
  },
  "runtime_manifest_digest": "sha256:def456",
  "ir_digest": "sha256:999aaa",
  "previous_release": "2026.05.15.3"
}
```

### 11.3 Verification rules

Runner verification must check:

```text
- release signature is valid
- release service/env matches VM identity
- metadata has not expired
- artifact is digest-pinned in prod
- runtime manifest digest matches
- rollback policy is obeyed
- optional SBOM/provenance policy is satisfied
```

The release pointer is not trusted. Only signed release metadata is trusted.

---

## 12. Optional SlateDB role

SlateDB is useful but not required. SlateDB describes itself as an embedded key-value database built on object storage, with Go bindings and features such as range scans, transactions, TTL, checkpoints, snapshots, and separate compaction.[^slatedb-home][^slatedb-intro]

Recommended use:

```text
Raw object-state graph = source of truth
SlateDB = optional materialized index/query accelerator
```

Use cases:

```text
- fast TUI list/search
- recent event queries
- service summary indexes
- resource reverse lookup
- historical operation queries
```

Do not use SlateDB for:

```text
- runner boot correctness
- release verification
- service control CAS
- stateful fencing correctness
```

SlateDB indexer model:

```text
skiff-indexer acquires env/control.json indexer lease
  -> scans raw events/control docs
  -> writes SlateDB indexes
  -> readers use SlateDB if present
  -> fallback to raw object scans if unavailable
```

---

## 13. AWS provider design

### 13.1 Provider interface

```go
package provider

type Provider interface {
	Name() string

	Plan(ctx context.Context, graph *ir.Graph) (*Plan, error)
	Apply(ctx context.Context, plan *Plan) (*ApplyResult, error)
	Destroy(ctx context.Context, graph *ir.Graph) (*Plan, error)

	InspectService(ctx context.Context, ref ServiceRef) (*ServiceInspection, error)
	InspectInstance(ctx context.Context, instanceID string) (*InstanceInspection, error)

	StartRollout(ctx context.Context, req RolloutRequest) (*Rollout, error)
	WatchRollout(ctx context.Context, rolloutID string) (<-chan RolloutEvent, error)
	Rollback(ctx context.Context, req RollbackRequest) (*Rollout, error)

	Logs(ctx context.Context, req LogsRequest) (LogStream, error)
	Metrics(ctx context.Context, req MetricsRequest) (*MetricResult, error)

	StartDebugSession(ctx context.Context, req DebugSessionRequest) (*DebugSession, error)

	EstimateCost(ctx context.Context, graph *ir.Graph) (*CostEstimate, error)
	ValidateIdentityPolicy(ctx context.Context, policy IdentityPolicy) ([]PolicyFinding, error)
}
```

### 13.2 AWS provider fields

```go
type AWSProvider struct {
	Region string

	EC2            *ec2.Client
	ASG            *autoscaling.Client
	ELB            *elasticloadbalancingv2.Client
	IAM            *iam.Client
	S3             *s3.Client
	KMS            *kms.Client
	CWLogs         *cloudwatchlogs.Client
	CW             *cloudwatch.Client
	SSM            *ssm.Client
	RDS            *rds.Client
	Route53        *route53.Client
	AccessAnalyzer *accessanalyzer.Client
}
```

### 13.3 Tags

Every Skiff-managed AWS resource must include:

```text
skiff.dev/managed = true
skiff.dev/service = payments-api
skiff.dev/env = prod
skiff.dev/region = us-west-2
skiff.dev/graph = sha256:...
skiff.dev/owner = skiff
```

Additional per-release tags on instances:

```text
skiff.dev/release = 2026.05.16.1
skiff.dev/operation = op_01HY...
```

Tags power drift detection, cost reporting, `doctor`, and adoption.

---

## 14. Deploy flow

```text
skiff deploy service.yaml
  1. Parse and validate spec.
  2. Compile typed IR.
  3. Run plugins.
  4. Run policy checks.
  5. Produce cloud plan.
  6. Create immutable operation intent.
  7. Acquire service lease by CAS on services/<svc>/control.json.
  8. Create immutable release objects.
  9. Update service control desired_release by CAS.
 10. Apply cloud plan.
 11. Start ASG instance refresh.
 12. Append rollout events.
 13. Update operation state by CAS.
 14. Mark stable_release on success by CAS.
 15. Release lease by CAS.
```

### 14.1 Deploy service implementation sketch

```go
func (s *DeployService) Deploy(ctx context.Context, req DeployRequest) (*DeployResult, error) {
	specDoc, err := s.SpecLoader.Load(req.SpecRef)
	if err != nil {
		return nil, err
	}

	graph, diags, err := s.Compiler.Compile(ctx, specDoc.Service, req.Target)
	if err != nil {
		return nil, err
	}

	plan, err := s.Provider.Plan(ctx, graph)
	if err != nil {
		return nil, err
	}

	if req.DryRun {
		return &DeployResult{Plan: plan, Diagnostics: diags}, nil
	}

	op := NewOperationIntent(req, graph)
	if err := s.State.CreateOperation(ctx, op); err != nil {
		return nil, err
	}

	lease, err := s.State.AcquireServiceLease(ctx, req.ServiceRef, req.Actor, "deploy", 5*time.Minute)
	if err != nil {
		return nil, err
	}
	defer s.State.TryReleaseServiceLease(ctx, req.ServiceRef, lease)

	release, err := s.ReleasePublisher.Publish(ctx, graph, req.Artifact)
	if err != nil {
		return nil, err
	}

	if err := s.State.SetDesiredRelease(ctx, req.ServiceRef, lease, release.Name); err != nil {
		return nil, err
	}

	apply, err := s.Provider.Apply(ctx, plan)
	if err != nil {
		return nil, err
	}

	rollout, err := s.Provider.StartRollout(ctx, provider.RolloutRequest{
		Service: req.ServiceRef,
		Release: release.Name,
		Strategy: req.Strategy,
	})
	if err != nil {
		return nil, err
	}

	return s.WatchAndFinalize(ctx, op.Ref(), rollout, release, apply)
}
```

---

## 15. Runner design

### 15.1 Runner responsibilities

`skiff-runner` runs on every workload VM.

```text
- discover VM identity from user data, tags, and IMDSv2
- fetch service control and signed release from object storage
- verify release and runtime manifest
- pull and prepare artifact
- render config and systemd unit
- start workload
- wait for health
- expose local runner status
- ship logs/metrics/traces through collector
- handle drain/shutdown
```

### 15.2 Runner FSM

```go
type RunnerState string

const (
	StateBooting           RunnerState = "Booting"
	StateFetchingManifest  RunnerState = "FetchingManifest"
	StateVerifyingRelease  RunnerState = "VerifyingRelease"
	StatePreparingArtifact RunnerState = "PreparingArtifact"
	StateRenderingConfig   RunnerState = "RenderingConfig"
	StateStartingWorkload  RunnerState = "StartingWorkload"
	StateWaitingForHealth  RunnerState = "WaitingForHealth"
	StateServing           RunnerState = "Serving"
	StateDraining          RunnerState = "Draining"
	StateStopping          RunnerState = "Stopping"
	StateStopped           RunnerState = "Stopped"
	StateFailed            RunnerState = "Failed"
)
```

### 15.3 Runner main loop

```go
func (r *Runner) Run(ctx context.Context) error {
	if err := r.transition(StateBooting, nil); err != nil {
		return err
	}

	identity, err := r.Identity.Discover(ctx)
	if err != nil {
		return r.fail("discover identity", err)
	}

	if err := r.transition(StateFetchingManifest, nil); err != nil {
		return err
	}

	release, bundle, err := r.Manifest.Fetch(ctx, identity)
	if err != nil {
		return r.fail("fetch release manifest", err)
	}

	if err := r.transition(StateVerifyingRelease, nil); err != nil {
		return err
	}

	if err := r.Verifier.Verify(ctx, release, bundle, identity); err != nil {
		return r.fail("verify release", err)
	}

	if err := r.transition(StatePreparingArtifact, nil); err != nil {
		return err
	}

	prepared, err := r.Artifacts.Prepare(ctx, release.Artifact)
	if err != nil {
		return r.fail("prepare artifact", err)
	}

	if err := r.transition(StateRenderingConfig, nil); err != nil {
		return err
	}

	unit, err := r.Renderer.RenderSystemdUnit(release.RuntimeManifest, prepared)
	if err != nil {
		return r.fail("render config", err)
	}

	if err := r.transition(StateStartingWorkload, nil); err != nil {
		return err
	}

	if err := r.Systemd.InstallAndStart(ctx, unit); err != nil {
		return r.fail("start workload", err)
	}

	if err := r.transition(StateWaitingForHealth, nil); err != nil {
		return err
	}

	if err := r.Health.WaitHealthy(ctx, release.RuntimeManifest.Health); err != nil {
		return r.fail("health check", err)
	}

	return r.transition(StateServing, nil)
}
```

### 15.4 Systemd hardening defaults

```ini
[Unit]
Description=Skiff workload {{ .Service }}
After=network-online.target skiff-collector.service
Wants=network-online.target

[Service]
Type=simple
User=skiff-app
Group=skiff-app
WorkingDirectory=/opt/skiff/workloads/{{ .Service }}/current
ExecStart={{ .Command }}
Restart=always
RestartSec=2
TimeoutStopSec={{ .ShutdownGraceSeconds }}

Environment=SKIFF_SERVICE={{ .Service }}
Environment=SKIFF_ENV={{ .Env }}
Environment=SKIFF_RELEASE={{ .Release }}
Environment=SKIFF_INSTANCE_ID={{ .InstanceID }}

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/skiff /var/log/skiff
CapabilityBoundingSet=
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
```

---

## 16. Artifact runtime

Supported v1 artifacts:

```text
oci     - OCI image as packaging format, not security boundary
binary  - signed/digest-pinned binary artifact
tar     - signed tarball unpacked to workload dir
ami     - immutable VM image mode, later/advanced
```

Production rule:

```go
func ValidateArtifactRef(ref string, env string) error {
	if env == "prod" && !strings.Contains(ref, "@sha256:") {
		return fmt.Errorf("production artifact must be pinned by digest")
	}
	return nil
}
```

OCI implementation can run either:

```text
- unpacked rootfs/process under systemd
- rootless container runtime inside dedicated VM
```

The VM remains the isolation boundary.

---

## 17. Observability

### 17.1 Default pipeline

```text
app stdout/stderr
  -> journald
  -> local collector
  -> CloudWatch Logs hot path
  -> S3 archive

app /metrics + host metrics
  -> local collector
  -> CloudWatch Metrics or OTLP backend

traces
  -> OpenTelemetry collector
  -> configured backend
```

CloudWatch Logs supports exporting log data to S3 for analysis or loading into other systems.[^cwlogs-s3]

### 17.2 Unified envelope

Every log, metric, trace, event, and health check should include:

```json
{
  "service": "payments-api",
  "env": "prod",
  "region": "us-west-2",
  "zone": "us-west-2a",
  "pool": "payments-api-prod",
  "instance_id": "i-abc123",
  "release": "2026.05.16.1",
  "runner_version": "0.1.0"
}
```

### 17.3 Metrics categories

```text
cloud metrics  - ASG, target group, LB, RDS, SQS, EBS
node metrics   - CPU, memory, disk, network, systemd, OOMs
app metrics    - HTTP, business counters, queue lag, DB latency
runner metrics - state, manifest age, log/metric delivery lag
```

### 17.4 Hot/cold split

```text
Hot logs/metrics:
  CloudWatch, Loki, Mimir, ClickHouse, Datadog, etc.

Cold archive:
  object storage partitioned by env/service/date/release
```

Object storage is not the live log search backend.

---

## 18. CLI and TUI UX

### 18.1 CLI principles

Every command must support:

```bash
--format json
--no-color
--yes
--dry-run
--explain
--trace-id
--state s3://bucket/prefix
--api https://skiff.example.com
```

Exit codes:

```text
0  success
1  user/spec error
2  policy denied
3  cloud/provider error
4  rollout failed
5  partial success
6  authentication/authorization error
7  timeout
8  unknown/internal error
```

### 18.2 Command surface

```text
skiff init
skiff bootstrap aws
skiff validate
skiff plan
skiff apply
skiff deploy
skiff release promote
skiff rollback
skiff status
skiff doctor
skiff solve
skiff logs
skiff metrics
skiff top
skiff debug
skiff import kube
skiff adopt aws
skiff adopt terraform
skiff terraform generate
skiff drift
skiff gc
skiff cost
skiff stateful
skiff plugin
skiff tui
```

### 18.3 `skiff doctor`

```bash
skiff doctor payments-api --env prod
```

Human output:

```text
payments-api prod

Status:
  desired release: 2026.05.16.1
  ASG desired/running/in-service: 6/6/5
  target health: 5 healthy, 1 unhealthy
  rollout: paused at 10% canary

Problem:
  i-abc123 is failing readiness.

Facts:
  - ALB target health: unhealthy
  - runner state: WaitingForHealth
  - app process: running
  - /healthz: HTTP 500
  - first bad log: "database migration missing"
  - release changed 14m ago

Likely cause:
  New release starts but fails application readiness.

Recommended:
  skiff logs payments-api --instance i-abc123 --since 20m
  skiff rollback payments-api --to previous-stable
```

Agent JSON output:

```json
{
  "ok": false,
  "code": "CANARY_FAILED",
  "service": "payments-api",
  "env": "prod",
  "release": "2026.05.16.1",
  "summary": "Canary failed readiness on 1 of 1 new instances",
  "facts": [
    "rollout is paused at 10% canary",
    "new instance returns 500 on /healthz",
    "previous release was healthy"
  ],
  "hypotheses": [
    {
      "confidence": 0.86,
      "message": "new release starts but fails application readiness"
    }
  ],
  "recommended_actions": [
    {
      "id": "inspect_logs",
      "command": "skiff logs payments-api --instance i-abc123 --since 20m --format json",
      "mutating": false,
      "safety": "read_only"
    },
    {
      "id": "rollback",
      "command": "skiff rollback payments-api --to previous-stable --yes --format json",
      "mutating": true,
      "safety": "reversible"
    }
  ]
}
```

### 18.4 TUI layout

```text
┌ Services ─────────────┐ ┌ payments-api ──────────────────────────┐
│ payments-api  green   │ │ release: 2026.05.16.1                 │
│ orders-api    green   │ │ healthy: 6/6                           │
│ invoice-worker yellow │ │ p95: 92ms  err: 0.2%  cpu: 48%         │
└───────────────────────┘ └────────────────────────────────────────┘

┌ Events ──────────────────────────────────────────────────────────┐
│ 20:11 rollout completed                                          │
│ 20:09 instance i-abc became healthy                              │
│ 20:06 instance refresh checkpoint 50% passed                     │
└──────────────────────────────────────────────────────────────────┘

Keys: d doctor | l logs | m metrics | r rollback | e explain | ? help
```

---

## 19. Agent amenities

Agents should be able to operate Skiff without scraping prose.

### 19.1 Action graph

```bash
skiff solve payments-api --goal restore-health --format json
```

```json
{
  "goal": "restore service health",
  "status": "plan_ready",
  "confidence": 0.91,
  "root_cause": {
    "summary": "release 2026.05.16.1 failed readiness",
    "evidence": [
      "rollout failed at 10% canary",
      "only new release instances are unhealthy",
      "health endpoint returns 500",
      "previous release was healthy"
    ]
  },
  "steps": [
    {
      "id": "rollback",
      "description": "Rollback to previous stable release",
      "command": "skiff rollback payments-api --to previous-stable --yes --format json",
      "mutating": true,
      "reversible": true
    },
    {
      "id": "verify",
      "description": "Watch rollout until all targets healthy",
      "command": "skiff status payments-api --watch --format json",
      "mutating": false
    }
  ]
}
```

### 19.2 Idempotency

Every mutating command accepts or generates an idempotency key:

```bash
skiff deploy --idempotency-key ci-run-12345
```

The key is written into the operation intent object. Re-running the same command returns the existing operation status.

### 19.3 Failure taxonomy

```text
VALIDATION_FAILED
POLICY_DENIED
CONTRACT_FAILED
ARTIFACT_UNTRUSTED
PLAN_FAILED
LEASE_HELD
CLOUD_APPLY_FAILED
ROLLOUT_FAILED
CANARY_FAILED
ROLLBACK_FAILED
OBSERVABILITY_UNAVAILABLE
```

---

## 20. Security design

### 20.1 Secure defaults

```text
- production artifacts must be digest-pinned
- release manifests must be signed
- release bucket/state bucket encrypted with KMS
- state bucket versioning enabled
- public access blocked
- conditional writes enforced by bucket policy on state prefixes
- no DeleteObject permissions for normal roles
- no inbound SSH security group
- SSM Session Manager for debug
- IMDSv2 required
- least-privilege IAM generated from spec
- Secrets Manager references, not secret values in object manifests
- egress default deny for production services unless relaxed
- systemd hardening defaults
- plugin artifacts must be signed
```

AWS Systems Manager Session Manager is a good default debug path because it provides node access without opening inbound ports, managing bastion hosts, or managing SSH keys.[^ssm-session]

### 20.2 State bucket IAM model

Roles:

```text
runner role:
  read root/*
  read services/<service>/control.json
  read services/<service>/releases/*
  no writes

ci/deployer role:
  create candidates/releases/operations
  CAS services/<service>/control.json
  call allowed provider rollout APIs
  no deletes

skiffd role:
  same as deployer, scoped by authz

indexer role:
  read state graph
  CAS indexes/*
  optional write slate/*

gc role:
  lifecycle cleanup only
  never delete stateful volumes without explicit policy
```

### 20.3 Bucket policy: require conditional writes

Bucket policies can enforce conditional writes using `s3:if-match` and `s3:if-none-match` condition keys.[^s3-enforce-conditional]

```json
{
  "Sid": "DenyUnconditionalStateWrites",
  "Effect": "Deny",
  "Principal": "*",
  "Action": "s3:PutObject",
  "Resource": [
    "arn:aws:s3:::skiff-state-prod/services/*",
    "arn:aws:s3:::skiff-state-prod/indexes/*",
    "arn:aws:s3:::skiff-state-prod/resources/*"
  ],
  "Condition": {
    "Null": {
      "s3:if-match": "true",
      "s3:if-none-match": "true"
    }
  }
}
```

Tune by prefix:

```text
immutable prefixes -> require If-None-Match
control prefixes   -> require If-Match except initial create
index prefixes     -> require If-Match or If-None-Match
```

### 20.4 IAM policy compiler

The compiler derives workload IAM from the spec:

```yaml
identity:
  secrets:
    read:
      - prod/payments/*
  kms:
    decrypt:
      - alias/prod-payments
```

Generated policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ReadPaymentSecrets",
      "Effect": "Allow",
      "Action": ["secretsmanager:GetSecretValue"],
      "Resource": [
        "arn:aws:secretsmanager:us-west-2:123456789012:secret:prod/payments/*"
      ]
    },
    {
      "Sid": "DecryptPaymentSecretsOnlyViaSecretsManager",
      "Effect": "Allow",
      "Action": ["kms:Decrypt"],
      "Resource": [
        "arn:aws:kms:us-west-2:123456789012:key/abcd-1234"
      ],
      "Condition": {
        "StringEquals": {
          "kms:ViaService": "secretsmanager.us-west-2.amazonaws.com"
        }
      }
    }
  ]
}
```

### 20.5 mTLS plugin

ALB supports mutual TLS, including client certificate authentication with CA certificates from a third-party CA or AWS Private CA.[^alb-mtls]

Ingress mTLS example:

```yaml
network:
  ingress:
    type: public-http
    host: api.example.com
    mtls:
      mode: verify
      caBundle: s3://company-trust/ca.pem
```

Service-to-service mTLS plugin:

```yaml
addons:
  - name: mtls.spiffe
    mode: strict
    config:
      trustDomain: prod.skiff.local
      outbound:
        - service: orders-api
          port: 8443
```

Skiff should not make a service mesh default. mTLS is a capability plugin.

---

## 21. Plugin system

### 21.1 Plugin types

```text
provider plugin    - AWS/GCP/Azure implementation
capability plugin  - mTLS, WAF, private DNS, egress policy, backup policy
runtime plugin     - runner-side systemd units/files/env
recipe plugin      - stateful workload recipes
scanner plugin     - SBOM/vulnerability/provenance checks
diagnostic plugin  - doctor checks and solve actions
```

### 21.2 Plugin principle

Plugins cannot run arbitrary hidden controllers. They operate at explicit hooks:

```text
Validate
MutateIR
Runtime
Doctor
Solve
```

They return typed patches and diagnostics.

### 21.3 Plugin API

```go
package pluginapi

type Capability interface {
	Name() string
	Version() string

	Validate(ctx context.Context, req ValidateRequest) (*ValidateResponse, error)
	MutateIR(ctx context.Context, req MutateIRRequest) (*MutateIRResponse, error)
	Runtime(ctx context.Context, req RuntimeRequest) (*RuntimeResponse, error)
	Doctor(ctx context.Context, req DoctorRequest) (*DoctorResponse, error)
}

type MutateIRRequest struct {
	Service spec.Service `json:"service"`
	Graph   ir.Graph    `json:"graph"`
	Target  Target      `json:"target"`
}

type MutateIRResponse struct {
	Patches     []IRPatch     `json:"patches"`
	Diagnostics []Diagnostic  `json:"diagnostics"`
}

type RuntimeResponse struct {
	Addons []RunnerAddon `json:"addons"`
}

type RunnerAddon struct {
	Name  string            `json:"name"`
	Units []SystemdUnitSpec `json:"units"`
	Files []RenderedFile    `json:"files"`
	Env   map[string]string `json:"env"`
}
```

### 21.4 Plugin runtimes

Use:

```text
gRPC plugin process - trusted, powerful, provider/capability plugins
WASM plugin         - validation/mutation plugins with strong sandboxing
```

Avoid Go’s native `plugin` package for portability/versioning reasons.

---

## 22. Terraform interaction

Terraform is optional. It is an apply backend and adoption bridge, not Skiff’s source of truth.

### 22.1 Modes

| Mode | Description | Recommended use |
|---|---|---|
| Direct | Skiff calls AWS SDK | default happy path |
| Generate | Skiff emits Terraform/OpenTofu | Terraform-heavy orgs |
| Managed Terraform | Skiff runs Terraform | later, if demanded |
| Adopt | Skiff records externally managed resources | migration |

### 22.2 Boundary

```text
Terraform may own infrastructure shape.
Skiff owns releases, rollout, rollback, operation events, diagnostics.
```

Best hybrid:

```text
Terraform:
  ASG exists
  target group exists
  IAM role exists
  launch template exists

Skiff:
  desired release changed
  signed release published
  ASG instance refresh started
  health watched
  rollback performed
```

### 22.3 Commands

```bash
skiff terraform generate skiff.yaml --out infra/generated/payments-api
terraform -chdir=infra/generated/payments-api plan
terraform -chdir=infra/generated/payments-api apply
skiff adopt terraform infra/generated/payments-api
skiff deploy skiff.yaml --artifact registry.example.com/payments-api@sha256:abc123
```

---

## 23. CI/CD interaction

CI/CD is the release authoring and approval layer. Skiff is the runtime deployment and rollout layer.

```text
CI:
  test -> build -> scan -> sign -> SBOM/provenance -> contract test -> plan

CD:
  skiff deploy/promote/rollback

Skiff:
  signed release -> object state -> cloud rollout -> health/metrics gates -> events
```

### 23.1 GitHub Actions skeleton

```yaml
name: payments-api

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read
  id-token: write

jobs:
  test-build-plan:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Install Skiff
        run: curl -fsSL https://get.skiff.dev | bash
      - name: Test
        run: go test ./...
      - name: Build image
        run: |
          docker build -t registry.example.com/payments-api:${{ github.sha }} .
          docker push registry.example.com/payments-api:${{ github.sha }}
      - name: Resolve digest
        id: image
        run: |
          DIGEST="$(skiff artifact digest registry.example.com/payments-api:${{ github.sha }})"
          echo "ref=registry.example.com/payments-api@$DIGEST" >> "$GITHUB_OUTPUT"
      - name: Contract test
        run: skiff contract test skiff.yaml --artifact "${{ steps.image.outputs.ref }}"
      - name: Plan
        run: skiff plan skiff.yaml --env staging --artifact "${{ steps.image.outputs.ref }}" --out skiff-plan.json
      - name: Create candidate
        run: |
          skiff release candidate create \
            --env staging \
            --service payments-api \
            --artifact "${{ steps.image.outputs.ref }}" \
            --plan skiff-plan.json \
            --git-sha "${{ github.sha }}" \
            --ci-url "${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}"

  deploy-staging:
    needs: [test-build-plan]
    if: github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: curl -fsSL https://get.skiff.dev | bash
      - run: skiff deploy skiff.yaml --env staging --candidate latest --yes --format json

  promote-prod:
    needs: [deploy-staging]
    environment: production
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: curl -fsSL https://get.skiff.dev | bash
      - run: skiff release promote payments-api --from staging --to prod --candidate latest-stable --strategy canary --yes --format json
```

---

## 24. Stateful workloads

### 24.1 Stateful philosophy

Stateful support is explicit and conservative.

```text
Managed cloud state first.
Self-managed state only through recipes.
No generic database operator framework.
No automatic stateful scale-down by default.
```

### 24.2 ManagedPostgres recipes

| Recipe | Backing service | Use case |
|---|---|---|
| `postgres-rds-multiaz` | RDS PostgreSQL Multi-AZ | ordinary regional Postgres |
| `aurora-postgres-multiaz` | Aurora regional cluster | HA regional app/database |
| `aurora-postgres-global` | Aurora Global Database | multi-region read locality and DR |

RDS Multi-AZ clusters use reader instances as failover targets and RDS manages failover to a reader with the most recent changes during writer outage.[^rds-multiaz-cluster]

### 24.3 StatefulGroup

```yaml
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup

metadata:
  name: orders-postgres
  env: prod

recipe: postgres-single
members: 1

machine:
  size: medium

storage:
  type: block
  sizeGB: 500
  class: gp3
  mount: /var/lib/postgresql/data

network:
  endpoint: orders-postgres.prod.internal
  port: 5432

backup:
  snapshots:
    interval: 15m
    retention: 7d
  logical:
    interval: 24h
    retention: 30d

recovery:
  autoReplaceVM: true
  requireFencing: true
```

### 24.4 Stateful member control

```json
{
  "schema": "skiff.stateful-member-control/v1",
  "group": "orders-postgres",
  "member": 0,
  "zone": "us-west-2a",
  "volume_id": "vol-abc123",
  "instance_id": "i-def456",
  "generation": 12,
  "role": "primary",
  "health": "healthy",
  "lease": {
    "owner": "state-controller/i-xyz",
    "token": "lease_...",
    "generation": 12,
    "expires_at": "2026-05-16T20:05:00Z",
    "purpose": "replace-member"
  },
  "fenced_instance_ids": ["i-old123"],
  "updated_at": "2026-05-16T20:00:00Z"
}
```

### 24.5 Replacement flow

```text
1. CAS member control to acquire replacement lease.
2. Fence old VM using cloud provider.
3. Confirm old VM stopped/terminated.
4. Detach volume.
5. Launch replacement VM in same AZ.
6. Attach volume.
7. Boot with same member identity.
8. Run recipe recovery hook.
9. CAS member control with new instance id.
10. Append recovery events.
```

Object-storage leases coordinate Skiff actors. They do not replace storage-level or database-level fencing.

---

## 25. Customer journeys and recipes

### 25.1 Journey: first API service

```bash
skiff init service payments-api
# edit skiff.yaml
skiff dev run
skiff contract test
skiff deploy --env staging
skiff logs payments-api --follow
skiff release promote payments-api --from staging --to prod
```

Generated defaults:

```text
- private subnets
- ALB listener
- ASG min=2 in prod
- digest-pinned artifact
- health check
- CloudWatch logs
- basic metrics
- IAM role
- no SSH ingress
```

### 25.2 Journey: API server + regional Postgres

```bash
skiff init stack api-postgres payments
skiff deploy --env staging
skiff release promote payments --to prod
```

Skiff creates or configures:

```text
- Service api
- ManagedPostgres db
- secret binding DATABASE_URL
- security group api -> db
- backup policy
- CloudWatch/RDS metrics dashboard
- database connection alarms
```

### 25.3 Journey: API server + multi-region Postgres

```bash
skiff init stack api-postgres-multiregion ledger
skiff deploy --env prod --regions us-west-2,us-east-1
```

Skiff creates or configures:

```text
- Service api in primary and secondary regions
- Aurora Global Database with primary and secondary clusters
- region-local read endpoints
- writer endpoint abstraction
- runbook for managed failover
- dashboards for replica lag, writer region, and app health per region
```

Failover:

```bash
skiff recipe aurora-global failover ledger-db --to us-east-1 --dry-run
skiff recipe aurora-global failover ledger-db --to us-east-1 --yes
```

Skiff should:

```text
1. Check app health in target region.
2. Check database replication/lag/eligibility.
3. Acquire DB control lease.
4. Run Aurora managed failover or required AWS operation.
5. Update Skiff control docs.
6. Update DNS/secret writer endpoint if needed.
7. Roll or restart app VMs if connection config changed.
8. Verify writes in new region.
9. Append immutable failover event log.
```

### 25.4 Journey: SQS worker

```yaml
apiVersion: skiff.dev/v1alpha1
kind: Worker
metadata:
  name: invoice-worker
  env: prod
artifact:
  type: oci
  ref: registry.example.com/invoice-worker@sha256:abc123
queue:
  type: sqs
  name: invoices-prod
scale:
  min: 2
  max: 100
  policies:
    - type: queueAge
      target: 30s
```

### 25.5 Journey: Kubernetes migration

```bash
skiff import kube \
  --namespace payments \
  --deployment payments-api \
  --service payments-api \
  --ingress payments-api \
  --out skiff.yaml

skiff deploy skiff.yaml --env staging --shadow
skiff cutover payments-api --from kube --to skiff --percent 10
skiff cutover payments-api --from kube --to skiff --percent 100
```

Importer behavior:

```text
Deployment -> Service
Service -> runtime port/target port
Ingress -> host/TLS
HPA -> scale policy where possible
ConfigMap -> runtime config
Secret -> cloud secret reference stubs
```

Reject or warn:

```text
DaemonSet
privileged containers
hostPath
unclear sidecars
StatefulSet without recipe
CSI volumes
CRDs/operators
service mesh annotations
```

### 25.6 Journey: Terraform-heavy organization

```bash
skiff terraform generate skiff.yaml --out infra/generated/payments-api
terraform -chdir=infra/generated/payments-api plan
terraform -chdir=infra/generated/payments-api apply
skiff adopt terraform infra/generated/payments-api
skiff deploy skiff.yaml --artifact registry.example.com/payments-api@sha256:abc123
```

### 25.7 Journey: agent solves outage

```bash
skiff doctor payments-api --format json > doctor.json
skiff solve payments-api --goal restore-health --format json > solve.json
```

Agent sees:

```json
{
  "status": "plan_ready",
  "confidence": 0.91,
  "steps": [
    {
      "id": "rollback",
      "command": "skiff rollback payments-api --to previous-stable --yes --format json",
      "mutating": true,
      "reversible": true
    }
  ]
}
```

---

## 26. Adoption modes

```text
render-only      - generate skiff.yaml from kube/Terraform/cloud, no deploy
shadow           - deploy Skiff service without traffic
parallel         - separate DNS/internal endpoint
weighted-cutover - shift traffic gradually
adopt-cloud      - record existing AWS resources in Skiff object state
infra-owned      - Terraform owns infra shape, Skiff owns release/rollout
full-skiff       - Skiff owns infra shape, release, rollout
```

Skiff should make migration honest. It should not pretend that every Kubernetes feature maps cleanly.

---

## 27. Testing strategy

### 27.1 Unit tests

Test pure components:

```text
spec parser
validator
defaulting
compiler IR generation
policy checks
path builder
CAS helper
lease logic
event serialization
release verification
runner FSM transitions
Terraform renderer
CLI JSON output
```

### 27.2 Golden tests

Golden files for:

```text
spec -> IR
IR -> AWS plan
IR -> Terraform
release manifest canonicalization
doctor reports
tui model snapshots
```

### 27.3 Object-store concurrency tests

Use the in-memory object store and S3 integration tests to prove:

```text
- Create fails if key exists
- CompareAndSwap fails on stale ETag
- only one deploy lease wins under concurrent attempts
- expired leases can be acquired
- stale owner cannot heartbeat
- operation resume handles expired leases
- index rebuild is deterministic
```

Example property:

```go
func TestOnlyOneLeaseWinner(t *testing.T) {
	store := objstoremem.New()
	key := "services/payments-api/control.json"
	mustCreateInitialControl(store, key)

	var wins atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := AcquireServiceLease(context.Background(), store, key, fmt.Sprintf("worker-%d", i), "deploy", time.Minute)
			if err == nil {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()

	require.Equal(t, int64(1), wins.Load())
}
```

### 27.4 Runner tests

```text
fake object store
fake systemd
fake artifact registry
fake health endpoint
invalid signatures
expired metadata
network interruption
secret permission failure
SIGTERM behavior
local status endpoint
```

### 27.5 Provider conformance tests

Every provider must pass:

```text
stateless HTTP service deploy
rollout success
rollback
logs
metrics
debug session
drift detection
release fetch from object storage
identity/secret equivalent
```

```go
func TestProviderConformance(t *testing.T, p provider.Provider) {
	ctx := context.Background()
	graph := fixtures.StatelessHTTPService()

	plan, err := p.Plan(ctx, graph)
	require.NoError(t, err)

	_, err = p.Apply(ctx, plan)
	require.NoError(t, err)

	rollout, err := p.StartRollout(ctx, provider.RolloutRequest{
		Service: provider.ServiceRef{Name: "hello", Env: "test"},
		Release: "test-release",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		inspection, _ := p.InspectService(ctx, provider.ServiceRef{Name: "hello", Env: "test"})
		return inspection.Healthy >= 1
	}, 10*time.Minute, 10*time.Second)

	_ = rollout
}
```

### 27.6 Integration tests

Run in a disposable AWS account:

```text
bootstrap minimal state bucket
create service
roll out good release
roll out bad release
auto rollback
logs visible
doctor identifies readiness failure
SSM debug command works
state bucket policy blocks unconditional writes
```

### 27.7 Chaos and resilience tests

```text
terminate instance
break artifact fetch
break secret permission
block log egress
fail target health
kill skiffd mid-deploy
start two deploys concurrently
expire lease mid-operation
simulate stale index
```

Expected property: operations are resumable from object state.

---

## 28. Bootstrapping

Minimal AWS bootstrap:

```bash
skiff bootstrap aws --env prod --region us-west-2 --minimal
```

Creates:

```text
S3 state bucket
KMS key
bucket policy
IAM deployer role
IAM runner role template
CloudTrail/data-event recommendation
```

Full bootstrap:

```bash
skiff bootstrap aws --env prod --region us-west-2 --domain skiff.example.com
```

Adds:

```text
optional skiffd service
optional indexer
optional worker
CloudWatch log groups
SSM permissions
base security groups
Route53 records
```

Skiff should be able to manage itself after bootstrap:

```bash
skiff deploy examples/skiffd/skiff.yaml
```

---

## 29. Roadmap

### Phase 0 - object-state core

```text
objstore interface
S3 implementation
memory implementation
path conventions
control docs
CAS leases
append-only events
raw indexes
```

### Phase 1 - stateless AWS service

```text
Service spec
compiler IR
AWS direct provider
release ledger
runner
ASG/ALB/IAM/KMS/S3/CloudWatch/SSM
skiff deploy/status/logs/rollback
```

### Phase 2 - safety and diagnostics

```text
release signing
IAM policy compiler
doctor
solve
contract tests
bucket policy enforcement
SSM debug
rollback gates
```

### Phase 3 - adoption

```text
Terraform generate/adopt
Kubernetes importer
shadow deploys
weighted cutover
CI/CD templates
```

### Phase 4 - managed dependencies

```text
ManagedPostgres regional
API + Postgres recipe
RDS/Aurora dashboards
backup defaults
secret bindings
```

### Phase 5 - plugins

```text
plugin registry
mTLS plugin
diagnostic plugin API
runtime addon API
scanner plugin API
```

### Phase 6 - multi-region and stateful

```text
multi-region service deploys
Aurora Global Database recipe
StatefulGroup single-member durable VM
EBS fencing/replacement
Postgres-single recipe
```

### Phase 7 - optional indexes and multi-cloud

```text
SlateDB indexer
GCP provider
Azure provider
provider conformance suite
```

---

## 30. Sharp implementation opinions

1. **Do not add a required database.** Object storage is the durable control plane.
2. **Do not make SlateDB required.** It is an index accelerator, not correctness substrate.
3. **Do not put leases in separate lock files.** Lease and mutable desired state must live in the same CAS control document.
4. **Do not make Terraform the deploy engine.** Terraform can own infrastructure shape; Skiff owns release and rollout.
5. **Do not build a service mesh by default.** mTLS is a plugin.
6. **Do not accept arbitrary Kubernetes YAML as native API.** Import it, explain gaps, and produce Skiff specs.
7. **Do not make logs object-storage-only.** CloudWatch/Loki/ClickHouse/etc. are the hot path; object storage is archive.
8. **Do not use SSH as the default debug path.** Use SSM/IAP equivalents.
9. **Do not support multi-service VM packing in v1.** The VM is the pod.
10. **Do not hide complexity.** Show plans, state keys, cloud resources, rollout IDs, evidence, and recommended actions.

---

## 31. References

[^s3-consistency]: AWS, “Amazon S3 Strong Consistency.” https://aws.amazon.com/s3/consistency/
[^s3-conditional]: AWS S3 User Guide, “How to prevent object overwrites with conditional writes.” https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html
[^s3-enforce-conditional]: AWS S3 User Guide, “Enforce conditional writes on Amazon S3 buckets.” https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes-enforce.html
[^asg-instance-refresh]: AWS EC2 Auto Scaling User Guide, “Use an instance refresh to update instances in an Auto Scaling group.” https://docs.aws.amazon.com/autoscaling/ec2/userguide/asg-instance-refresh.html
[^asg-rollback]: AWS EC2 Auto Scaling User Guide, “Undo changes with a manual or auto rollback.” https://docs.aws.amazon.com/autoscaling/ec2/userguide/instance-refresh-rollback.html
[^ssm-session]: AWS Systems Manager User Guide, “AWS Systems Manager Session Manager.” https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager.html
[^alb-mtls]: AWS Elastic Load Balancing User Guide, “Mutual authentication with TLS in Application Load Balancer.” https://docs.aws.amazon.com/elasticloadbalancing/latest/application/mutual-authentication.html
[^cwlogs-s3]: AWS CloudWatch Logs User Guide, “Exporting log data to Amazon S3.” https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/S3Export.html
[^slatedb-home]: SlateDB, “Embedded DB Built on Object Storage.” https://slatedb.io/
[^slatedb-intro]: SlateDB Docs, “Introduction.” https://slatedb.io/docs/get-started/introduction/
[^aurora-global]: AWS Aurora User Guide, “Using Amazon Aurora Global Database.” https://docs.aws.amazon.com/AmazonRDS/latest/AuroraUserGuide/aurora-global-database.html
[^rds-multiaz-cluster]: AWS RDS User Guide, “Multi-AZ DB cluster deployments for Amazon RDS.” https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/multi-az-db-clusters-concepts.html
