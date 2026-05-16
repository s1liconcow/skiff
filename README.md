# Skiff

**Skiff is a clusterless deployment and operations platform for cloud-native VM workloads.**

Skiff’s core model is intentionally simple:

```text
VM = pod
cloud autoscaling group = deployment
cloud load balancer target group = service
cloud IAM role = workload identity
object storage = durable desired state and audit history
skiffd = normal API/TUI/agent facade with rebuildable in-memory views
skiff CLI direct mode = recovery path
```

Skiff is not trying to be a smaller Kubernetes. It is a different operating model: compile simple service specs into cloud primitives, publish signed release state to object storage, and let VM-local runners converge one workload replica per VM.

## Why Skiff exists

Most production workloads do not need the full Kubernetes abstraction stack. They need to:

- deploy services safely
- roll back quickly
- tail logs and inspect metrics
- rotate secrets and keys
- restore databases
- canary releases
- perform regional failover
- debug unhealthy instances
- understand what changed and why

Terraform is excellent for stable infrastructure shape, but not for operational journeys. Kubernetes operators can model operations, but they often become opaque always-running controllers with hidden behavior.

Skiff fills the gap with explicit, auditable, resumable operations called **sagas**.

```text
Terraform answers: what should exist?
Skiff releases answer: what should run?
Skiff sagas answer: what operational journey should happen now?
```

## Core principles

1. **Object storage is durable truth.**  
   Service control docs, release manifests, saga graphs, events, audit records, and resource summaries live in object storage.

2. **`skiffd` is a facade, not the database.**  
   `skiffd` provides API, TUI, auth, plugin execution, event streaming, and hot in-memory views. If it dies, it can rebuild from object storage.

3. **The CLI has direct recovery mode.**  
   Operators and agents can bypass `skiffd` and use object storage plus cloud APIs directly for break-glass recovery.

4. **One VM runs one workload replica by default.**  
   This keeps identity, metrics, logs, health, debugging, and blast radius easy to understand.

5. **Cloud primitives remain visible.**  
   Skiff does not hide ASGs, target groups, launch templates, IAM roles, logs, metrics, or secret references. It makes them easier to operate.

6. **Operations are explicit sagas.**  
   Deploys, canaries, rollbacks, restores, rotations, failovers, and cutovers are graph-shaped workflows with typed steps, risk, reversibility, approvals, and append-only events.

7. **Secure by default.**  
   Signed releases, digest-pinned artifacts, least-privilege IAM, no SSH-first debugging, encrypted state, and audited mutations are the defaults.

## Architecture

```text
Humans / agents / CI
        |
        v
      skiff CLI  <----------------------+
        |                               |
        | API mode                      | direct recovery mode
        v                               |
      skiffd                            |
  API / TUI / auth                      |
  plugins / sagas                      |
  in-memory views                       |
        |                               |
        v                               |
object storage state bucket <-----------+
        |
        v
cloud provider primitives
        |
        v
VM workload replicas
  skiff-runner
  app process
  log/metric collector
```

### Required components

- `skiff` - CLI/TUI entrypoint for humans, CI, and agents.
- `skiff-runner` - VM-local runner that fetches signed manifests and starts the workload.
- Object storage state bucket - durable state, release ledger, operation events, and audit history.
- Cloud provider primitives - AWS first.

### Normal product components

- `skiffd` - stateless facade with in-memory indexes, event streaming, auth, plugin execution, and API/TUI support.

### Optional components

- `skiff-worker` - optional operation/saga resumer for large installations or long-running operational flows.
- Plugins - typed extensions for capabilities such as mTLS, managed database operations, diagnostics, or traffic controls.

## Repository layout

The intended Go repository structure:

```text
skiff/
  cmd/
    skiff/                  # CLI/TUI
    skiffd/                 # stateless API facade
    skiff-runner/           # VM-local runner
    skiff-worker/           # optional operation/saga resumer

  internal/
    artifact/               # artifact preparation: OCI, tarball, binary
    auth/                   # actors and authn helpers
    authz/                  # authorization and approval policy
    bootstrap/              # cloud bootstrap for state bucket, KMS, roles
    cicd/                   # CI/CD templates and release candidate flows
    cli/                    # CLI output, errors, progress rendering
    client/                 # API and direct clients
    compiler/               # spec -> typed IR
    config/                 # config loading and validation
    cost/                   # shape/cost advisor
    debug/                  # diagnostic bundles and debug sessions
    deploy/                 # release publishing and deploy orchestration
    doctor/                 # service and saga diagnostics
    drift/                  # cloud drift detection
    events/                 # append-only event model and streaming
    gc/                     # safe cleanup planning and apply
    importers/              # Kubernetes and other importers
    index/                  # skiffd rebuildable in-memory views
    ir/                     # typed provider-neutral resource graph
    objstore/               # object storage interface and backends
    observability/          # logs, metrics, traces envelope
    ops/                    # resumable operation controls
    plugins/                # plugin host and registry
    provider/               # provider interface and AWS implementation
    release/                # release manifests, signing, verification
    runner/                 # runner FSM and local lifecycle
    saga/                   # operational graph executor and templates
    security/               # signing, policy generation, redaction
    spec/                   # public Skiff spec parsing/defaulting/validation
    state/                  # object-state schemas, paths, CAS controls
    stateful/               # StatefulGroup mechanics and recipe hooks
    status/                 # service and stack status views
    terraform/              # Terraform generate/adopt bridge
    tui/                    # terminal UI

  pkg/
    pluginapi/              # stable plugin API
    sagaapi/                # public saga step API, if needed later
    sdk/                    # client SDK for agents/tools, if needed later

  docs/
    adoption/
    operations/
    recipes/
    support/
    dev/

  examples/
    service/
    stacks/
    plugins/

  tests/
    conformance/
    e2e/
    fixtures/
    golden/
    integration/
```

## Object-state model

Skiff state is plain, inspectable, durable object storage.

Representative layout:

```text
root/
  trust-root.json

env/
  control.json

services/
  payments-api/
    control.json
    specs/
      sha256-....json
    candidates/
      rc_01J.../
        candidate.json
        plan.json
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
      op_01J.../
        intent.json
        control.json
        events/
          01J...-created.json
          01J...-planned.json
          01J...-rollout-started.json
          01J...-succeeded.json
        result.json

sagas/
  saga_01J.../
    intent.json
    graph.json
    control.json
    events/
      01J...-started.json
      01J...-approval-required.json
      01J...-completed.json
    artifacts/
      plan.md
      result.json

resources/
  by-logical/
    services/payments-api/asg.json
    services/payments-api/target-group.json
  by-provider/
    aws/asg/skiff-prod-payments-api.json

indexes/
  services.json
  active-sagas.json
  recent-events.json

audit/
  2026-05-16/
    01J....json
```

There are three state categories:

| Category | Examples | Mutation rule |
|---|---|---|
| Immutable history | release manifests, operation intents, events, audit records | create-only |
| Control documents | service control, operation control, saga control, stateful member control | compare-and-swap |
| Rebuildable views | service index, recent events, skiffd memory index | derived from truth |

## CAS control documents

Mutable control documents are updated through compare-and-swap. They are also the lease object. Do not create separate lock files.

Example service control document:

```json
{
  "schema": "skiff.service-control/v1",
  "service": "payments-api",
  "env": "prod",
  "desired_release": "2026.05.16.1",
  "stable_release": "2026.05.15.3",
  "operation": {
    "id": "op_01J...",
    "kind": "deploy",
    "state": "rolling_out",
    "step": "canary"
  },
  "lease": {
    "owner": "skiffd/i-abc123",
    "token": "lease_01J...",
    "generation": 42,
    "expires_at": "2026-05-16T20:05:00Z"
  },
  "version": 18,
  "updated_at": "2026-05-16T20:04:30Z"
}
```

Rule:

```text
Write object storage first.
Then update skiffd memory views.
Never the reverse.
```

## Sagas

A Skiff saga is a typed operational graph.

Examples:

- canary deployment
- rollback
- database backup
- database restore
- secret rotation
- key rotation
- certificate rotation
- traffic cutover
- regional failover
- stateful member replacement
- debug bundle collection

A saga contains:

```text
intent.json  - immutable request
graph.json   - immutable planned graph
control.json - CAS-updated execution state and lease
events/*     - append-only timeline
artifacts/*  - plans, reports, results
```

Example saga graph:

```text
preflight
  -> create snapshot
  -> restore new database
  -> smoke test
  -> approval before cutover
  -> update secret pointer
  -> roll service
  -> verify
```

Sagas are explicit and bounded. They are not hidden always-running controllers.

## CLI overview

Core commands:

```bash
skiff bootstrap aws
skiff validate skiff.yaml
skiff compile skiff.yaml
skiff plan skiff.yaml
skiff deploy skiff.yaml
skiff deploy skiff.yaml --canary
skiff status payments-api
skiff logs payments-api --follow
skiff metrics payments-api
skiff doctor payments-api
skiff solve payments-api --goal restore-health --format json
skiff rollback payments-api --to previous-stable
skiff saga list
skiff saga inspect saga_01J...
skiff saga watch saga_01J...
skiff saga approve saga_01J... --step approval_before_cutover
skiff restore database payments-db --to 2026-05-15T10:00:00Z
skiff rotate secret prod/payments/db-password
skiff drift payments-api
skiff gc plan
skiff tui
```

Every operational command should support:

```bash
--format json
--no-color
--yes
--trace-id
```

Direct recovery mode:

```bash
skiff --direct --state s3://skiff-state-prod status payments-api
skiff --direct --state s3://skiff-state-prod rollback payments-api --to previous-stable
```

## Agent amenities

Skiff is designed for agents as first-class users.

Agent-facing behaviors:

- deterministic JSON output
- explicit exit codes
- facts separated from hypotheses
- recommended actions with safety metadata
- reversible vs irreversible classification
- operation and saga IDs for resumption
- direct recovery commands
- event streams plus object-log resume
- trace IDs everywhere

Example doctor output shape:

```json
{
  "ok": false,
  "code": "CANARY_FAILED",
  "summary": "Canary failed metrics gate",
  "facts": [
    {
      "type": "target_health",
      "message": "new release targets are healthy"
    },
    {
      "type": "metrics_gate",
      "message": "new release error rate is above threshold"
    }
  ],
  "hypotheses": [
    {
      "confidence": 0.86,
      "message": "new release is application-broken rather than infrastructure-broken"
    }
  ],
  "recommended_actions": [
    {
      "id": "rollback",
      "command": "skiff rollback payments-api --to previous-stable --yes --format json",
      "mutating": true,
      "safety": "reversible"
    }
  ]
}
```

## Security defaults

Skiff should default to:

- signed release manifests
- digest-pinned production artifacts
- encrypted object-state bucket
- versioned object-state bucket
- least-privilege runner, deployer, and skiffd roles
- no SSH-first debugging
- managed debug sessions through cloud-native access mechanisms
- public ingress through load balancers only
- workload identity through cloud IAM
- secret references, not plaintext secret values in object state
- audit events for every mutation
- approval gates for high-risk sagas
- explicit risk and reversibility classification

## Adoption paths

### From scratch

```bash
skiff bootstrap aws --env prod --region us-west-2
skiff init service payments-api
skiff deploy skiff.yaml
```

### API plus managed database

```bash
skiff init stack api-database payments
skiff deploy
skiff status payments-api
```

### From Kubernetes

```bash
skiff import kube ./k8s --out skiff.yaml
skiff deploy skiff.yaml --shadow
skiff saga start traffic-cutover --from kube/payments-api --to skiff/payments-api
```

### From Terraform

```bash
skiff terraform generate skiff.yaml --out infra/skiff/payments-api
terraform -chdir=infra/skiff/payments-api apply
skiff adopt terraform infra/skiff/payments-api
skiff deploy skiff.yaml
```

Terraform can own stable infrastructure shape. Skiff owns releases, rollouts, rollback, diagnosis, sagas, and operation state.

### CI/CD

CI builds and proves artifacts. Skiff publishes and rolls them out.

```bash
skiff validate skiff.yaml
skiff contract test skiff.yaml --artifact "$IMAGE_REF"
skiff plan skiff.yaml --artifact "$IMAGE_REF"
skiff release candidate create --service payments-api --artifact "$IMAGE_REF"
skiff deploy skiff.yaml --env staging --candidate latest --yes
skiff promote payments-api --from staging --to prod --candidate latest-stable --strategy canary --yes
```

## Testing strategy

Major test layers:

1. **Unit tests** - schemas, CAS logic, compiler, provider lowering, signing, policy generation.
2. **State workflow tests** - object-state flows using the in-memory object store.
3. **Fake provider e2e tests** - full deploy/rollback/saga flows without cloud credentials.
4. **Provider conformance tests** - support bar for AWS and future providers.
5. **Optional cloud integration tests** - gated, isolated, tagged, and not required for normal PRs.
6. **Production readiness tests** - interrupted deploys, failed rollouts, stale skiffd cache, direct recovery, lease contention, observability outages.

## Milestone documents

Implementation planning is organized into three bead documents:

- `skiff_beads_m1_foundation.md` - core object-state foundation and runner MVP.
- `skiff_beads_m2_operations.md` - stateless deploys, observability, doctor, and sagas.
- `skiff_beads_m3_adoption_production.md` - adoption, plugins, recipes, production hardening, and GA.

## Non-goals

Skiff should not become:

- a Kubernetes YAML emulator
- a generic bin-packing scheduler
- a service mesh by default
- an always-running operator framework
- an in-house secret store
- a hidden abstraction over cloud primitives
- a platform that requires its API server to recover itself
- a system where state is only understandable through a proprietary database

## Slogan

```text
Terraform for shape. Skiff for journeys.
```

Or:

```text
The VM is the pod. The cloud is the cluster. Object storage is the ledger.
```
