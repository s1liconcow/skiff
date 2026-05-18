---
# Skiff Beads - Milestone 4: Packages, Stateful Operations, and Validation

**Milestone:** M4 - Packages and Stateful Operations  
**Audience:** Junior and mid-level engineers implementing Skiff under senior staff guidance.  
**Source of truth:** This document is self-contained. Do not rely on earlier design notes to understand the work.  
**Core product rule:** Object storage remains durable truth. Packages provide typed declarations and typed operational steps; they do not become hidden controllers.

## Objective

Make the simple dependency case easy and the complex stateful operations case possible.

A user running an API server should be able to depend on a highly available Postgres, MySQL, Kafka, NATS JetStream, Redis, or Elastic/OpenSearch package without hand-writing all internals. Operators should still be able to run explicit, auditable, resumable sagas for application-specific stateful operations such as primary switchover, quorum-safe rolling update, slot-aware failover, and shard-allocation rolling update.

## Deliverable outcome

At the end of this milestone:

- `stack.dependencies[]` can reference signed, digest-locked packages.
- `skiff pkg` resolves, verifies, explains, and locks packages.
- Package dependencies compile into typed IR with provenance.
- Package operation profiles render immutable saga graphs.
- `skiff ops` is the normal operation entrypoint.
- Low-level `skiff saga start` is deprecated for normal creation.
- First-party packages exist for HA Postgres, HA MySQL, Kafka, NATS JetStream, Redis, and Elastic/OpenSearch.
- Every non-managed package is validated by deploying a real StatefulGroup through the Apple Silicon provider and running its declared operations.
- A lightweight realistic stateful test app validates operation semantics without requiring heavyweight upstream systems in the normal test gate.

## Validated design inputs

Use these primary docs as the basis for package defaults and operation gates:

- AWS RDS Multi-AZ and failover behavior:
  - https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/multi-az-db-clusters-concepts.html
  - https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/multi-az-db-clusters-concepts-failover.html
- PostgreSQL standby/failover and Patroni switchover:
  - https://www.postgresql.org/docs/current/warm-standby.html
  - https://www.postgresql.org/docs/current/warm-standby-failover.html
  - https://patroni.readthedocs.io/en/latest/rest_api.html
- MySQL InnoDB Cluster, Group Replication, and primary changes:
  - https://dev.mysql.com/doc/mysql-shell/8.1/en/mysql-innodb-cluster.html
  - https://dev.mysql.com/doc/refman/8.2/en/group-replication-change-primary.html
  - https://dev.mysql.com/doc/refman/en/group-replication-replication-group-members.html
- Kafka ISR and durability semantics:
  - https://kafka.apache.org/41/design/design/
  - https://kafka.apache.org/41/operations/eligible-leader-replicas/
- NATS JetStream RAFT/quorum and JSON admin APIs:
  - https://docs.nats.io/running-a-nats-service/configuration/clustering/jetstream_clustering
  - https://docs.nats.io/running-a-nats-service/nats_admin/jetstream_admin/streams
  - https://docs.nats.io/reference/reference-protocols/nats_api_reference
- Redis Sentinel and Redis Cluster failover:
  - https://redis.io/docs/latest/operate/oss_and_stack/management/sentinel/
  - https://redis.io/docs/latest/commands/cluster-failover/
  - https://redis.io/docs/latest/commands/cluster-nodes/
- Elasticsearch/OpenSearch rolling operations:
  - https://www.elastic.co/docs/deploy-manage/upgrade/deployment-or-cluster/elasticsearch
  - https://docs.opensearch.org/3.1/migrate-or-upgrade/rolling-upgrade/
  - https://docs.opensearch.org/latest/api-reference/cluster-api/cluster-health/

## Dependency overlay

```text
Package foundation:
skiff-m4-001 ── skiff-m4-002 ── skiff-m4-003
        │              │              │
        └──────────────┴──── skiff-m4-004 ── skiff-m4-005 ── skiff-m4-006
                                      │
                                      └── skiff-m4-007 ── skiff-m4-008

Real validation:
skiff-m4-009 ── skiff-m4-010 ── skiff-m4-011
        │              │              │
        └──────────────┴──────────────┴── package beads

Packages:
skiff-m4-012 postgres
skiff-m4-013 mysql
skiff-m4-014 kafka
skiff-m4-015 nats jetstream
skiff-m4-016 redis
skiff-m4-017 elastic/opensearch

Release/docs gate:
all package beads ── skiff-m4-018 ── skiff-m4-019
```

## Shared implementation conventions

- Packages return typed declarations, typed patches, typed step results, or typed doctor findings. They do not directly mutate cloud resources behind Skiff.
- Production deploys must use package lock entries with digest and signature references.
- Package-expanded resources must preserve package provenance in explain output, IR, release/runtime manifests, operation intents, and saga intents.
- Every package operation must be represented as an explicit saga graph with immutable intent/graph, CAS control, and append-only events.
- Every long-running package step must store provider operation IDs and step results before waiting.
- Managed database packages and self-managed StatefulGroup packages may share UX but must be explicit in JSON output.
- `VerifyQuorum` is not a universal hook. The generic concept is an operation safety gate. Concrete packages expose specific steps such as `nats.jetstream.verify_quorum`, `kafka.verify_isr`, and `search.verify_cluster_health`.
- Apple Silicon validation is required for non-managed packages because it exercises real StatefulGroup lifecycle, real volumes, object state, direct mode, and operation sagas without requiring AWS credentials.

---

## Define package dependency schemas and lockfile

### ID
skiff-m4-001

### Priority
P0

### Type
feature

### Labels
packages, spec, lockfile, ux

### Dependencies
skiff-m3-007

skiff-m3-013

### Description
Add the public package vocabulary that lets a Stack depend on package-provided infrastructure and operations.

#### Subtasks
- Add `stack.dependencies[]` to the public spec.
- Define `StackDependency` fields:
  - `name`: local dependency name used by bindings and ops targets.
  - `uses`: package ref such as `skiff.dev/postgres-ha`.
  - `version`: semver range or exact version.
  - `config`: package-specific JSON/YAML object.
- Add `skiff.package.json` schema.
- Add `skiff.lock.json` schema.
- Validate unknown fields strictly.
- Reject production deploys with unresolved, unsigned, or floating package refs.
- Add source references for package-derived validation errors.
- Document package ref forms:
  - `skiff.dev/name`
  - `oci://registry/repo/name:version`
  - `file://../local-package`
- Require lockfile entries to include package ref, resolved version, digest, signature ref, source registry, manifest digest, and resolved timestamp.

#### Likely Files
- `internal/spec/types.go`
- `internal/spec/validate.go`
- `internal/packages/types.go`
- `internal/packages/manifest.go`
- `internal/packages/lock.go`
- `internal/state/schema/package.go`
- `tests/spec/spec_test.go`
- `docs/packages/authoring.md`

#### Design
A minimal dependency should look like this:

```yaml
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/api@sha256:abc123
      runtime:
        port: 8080
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: 1.x
      config:
        mode: managed
        engine: postgres
        size: small
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
```

A lockfile entry should be deterministic and production-safe:

```json
{
  "schema": "skiff.lock/v1alpha1",
  "packages": [
    {
      "name": "db",
      "ref": "skiff.dev/postgres-ha",
      "version": "1.2.0",
      "digest": "sha256:...",
      "signature_ref": "oci://registry/skiff/postgres-ha.sig@sha256:...",
      "manifest_digest": "sha256:...",
      "source": "oci://registry/skiff/postgres-ha:1.2.0"
    }
  ]
}
```

Package manifests must be declarative first:

```json
{
  "apiVersion": "skiff.dev/package/v1alpha1",
  "kind": "Package",
  "name": "postgres-ha",
  "version": "1.2.0",
  "exports": {
    "dependencies": ["postgres-ha"],
    "operation_profiles": ["primary-switchover-update"],
    "doctor_checks": ["postgres.replica_lag"]
  },
  "plugin": {
    "manifest": "plugin.json"
  }
}
```

#### Testing / Validation
- Unit test valid and invalid `stack.dependencies[]`.
- Unit test lockfile canonical JSON.
- Golden test package manifest examples.
- Test unknown fields fail with source path.
- Test production deploy rejects missing lockfile.
- Test dev mode allows local unsigned packages only when explicitly configured.

#### Gotchas
Do not let package config become arbitrary `map[string]any` past the parser boundary. Decode each package config into typed schemas when compiling or validating.

#### Acceptance Criteria
- Specs can declare package dependencies.
- Lockfiles are schema-versioned and canonical.
- Production deploys require digest-locked package refs.
- Validation errors are clear enough for agents and humans.

---

## Implement package resolver, cache, and CLI

### ID
skiff-m4-002

### Priority
P0

### Type
feature

### Labels
packages, cli, security

### Dependencies
skiff-m4-001

### Description
Implement package resolution and the `skiff pkg` command family.

#### Subtasks
- Add `skiff pkg add <ref>`.
- Add `skiff pkg update [name]`.
- Add `skiff pkg list`.
- Add `skiff pkg explain <name|ref>`.
- Add `skiff pkg verify <name|ref>`.
- Add `skiff pkg bundle <dir>`.
- Resolve local `file://` refs and OCI refs first.
- Verify digest and signature before writing lockfile.
- Store fetched package contents in a content-addressed cache.
- Print package exports, permissions, operation profiles, and hook runtimes in `pkg explain`.
- Support `--format json`, `--no-color`, `--yes`, and `--trace-id`.

#### Likely Files
- `internal/cli/pkg.go`
- `internal/packages/resolver.go`
- `internal/packages/cache.go`
- `internal/packages/signing.go`
- `internal/packages/explain.go`
- `internal/packages/errors.go`
- `internal/cli/command.go`
- `internal/cli/command_test.go`

#### Design
`skiff pkg add skiff.dev/postgres-ha` should:

1. Resolve the configured package registry.
2. Select a version.
3. Fetch package manifest and content.
4. Verify digest and signature.
5. Write or update `skiff.lock.json`.
6. Print next commands.

Example JSON output:

```json
{
  "ok": true,
  "trace_id": "tr_...",
  "package": {
    "name": "postgres-ha",
    "ref": "skiff.dev/postgres-ha",
    "version": "1.2.0",
    "digest": "sha256:..."
  },
  "lockfile": "skiff.lock.json",
  "recommended_actions": [
    {
      "id": "explain",
      "command": "skiff pkg explain postgres-ha --format json",
      "mutating": false
    }
  ]
}
```

#### Testing / Validation
- Resolver tests for digest mismatch.
- Resolver tests for missing signature.
- Resolver tests for duplicate local dependency names.
- CLI golden tests for all `pkg` subcommands.
- Cache tests proving content-addressed reuse.
- JSON output tests proving no ANSI/spinner output in JSON mode.

#### Gotchas
Do not update the lockfile during `skiff deploy`. Deploy consumes the lockfile. Only `pkg add` and `pkg update` should mutate it.

#### Acceptance Criteria
- Packages can be added, verified, explained, and locked.
- Production deploy cannot accidentally float package versions.
- Package provenance is machine-readable.

---

## Integrate packages into compiler, explain, plan, and deploy

### ID
skiff-m4-003

### Priority
P0

### Type
feature

### Labels
compiler, deploy, packages

### Dependencies
skiff-m4-002

### Description
Compile package dependencies into typed IR resources before provider lowering.

#### Subtasks
- Replace the current Stack compiler limitation that supports only one service plus one managed database or object store.
- Load `skiff.lock.json` during validate/compile/plan/explain/deploy when `stack.dependencies[]` is present.
- Expand package dependency templates into typed IR:
  - `ManagedDatabase`
  - `StatefulGroup`
  - security groups
  - secret refs
  - database/object bindings
  - runtime env
  - operation profile registrations
- Add package provenance to generated IR resources.
- Include package lock digest in release manifests, runtime manifests, operation intents, and saga intents.
- Show package-derived resources in `skiff explain`.
- Ensure direct mode can compile using only `skiff.yaml`, `skiff.lock.json`, package cache, and object storage.

#### Likely Files
- `internal/compiler/stack.go`
- `internal/compiler/packages.go`
- `internal/ir/types.go`
- `internal/deploy/deploy.go`
- `internal/explain/`
- `tests/compiler/compiler_test.go`
- `tests/recipes/`

#### Design
A package-expanded resource should carry provenance:

```json
{
  "logical_id": "managed-database:payments-db",
  "kind": "ManagedDatabase",
  "source": [
    {"path": "$.stack.dependencies[0]"},
    {
      "package": "skiff.dev/postgres-ha",
      "version": "1.2.0",
      "digest": "sha256:..."
    }
  ]
}
```

Explain output should say:

```text
Dependency db
  package: skiff.dev/postgres-ha@1.2.0
  mode: managed
  resources:
    managed database payments-db
    secret DATABASE_URL
    security group rule api -> db:5432
  operations:
    failover
    backup
    restore
```

#### Testing / Validation
- Compile API + HA Postgres.
- Compile API + Redis.
- Compile API + JetStream.
- Explain output includes package provenance.
- Deploy output includes package lock digest.
- Direct mode succeeds without `skiffd`.

#### Gotchas
Do not hide cloud primitives. Even if a package creates them, explain must still show managed database, StatefulGroup members, volumes, security groups, target groups, and provider IDs when known.

#### Acceptance Criteria
- Stack dependencies compile into typed IR.
- Package provenance is visible and durable.
- Direct mode remains first-class.

---

## Define operation profile API

### ID
skiff-m4-004

### Priority
P0

### Type
feature

### Labels
saga, ops, packages

### Dependencies
skiff-m4-001

skiff-m2-002

### Description
Define package-declared operation profiles that render typed saga graphs.

#### Subtasks
- Add operation profile schema.
- Add typed params schema for each profile.
- Add profile fields:
  - `name`
  - `kind`
  - `target_kinds`
  - `summary`
  - `params`
  - `defaults`
  - `risk`
  - `reversibility`
  - `required_capabilities`
  - `graph_template`
- Add built-in profile families:
  - `ordered_in_place_update`
  - `replace_member_move_volume`
  - `primary_switchover_update`
  - `partition_quorum_rolling_update`
  - `raft_group_rolling_update`
  - `slot_aware_failover_update`
  - `shard_allocation_rolling_update`
- Ensure rendered saga graphs include package digest and lockfile digest.
- Validate params before durable writes.
- Add operation profile explain output.

#### Likely Files
- `internal/ops/profile.go`
- `internal/ops/render.go`
- `internal/saga/templates/`
- `pkg/sagaapi/`
- `tests/ops/profile_test.go`

#### Design
Operation profiles are the bridge between package UX and explicit sagas.

Example profile concept:

```yaml
name: primary-switchover-update
kind: primary_switchover_update
targetKinds:
  - StatefulGroup
params:
  releaseID:
    type: string
    required: true
  candidate:
    type: string
  returnPrimary:
    type: boolean
    default: false
risk: high
reversibility: partially_reversible
```

A rendered saga might contain:

```text
verify_cluster_healthy
verify_candidate_caught_up
move_primary_to_candidate
update_old_primary
verify_old_primary_caught_up
optional_failback
update_candidate
verify_final_topology
```

#### Testing / Validation
- Render primary switchover graph.
- Render Kafka partition quorum update graph.
- Render JetStream RAFT group update graph.
- Invalid params fail before object writes.
- Rendered graph is deterministic and canonical.

#### Gotchas
Do not call compensation rollback unless it truly restores prior state. Most stateful compensations are harm reduction, not exact rollback.

#### Acceptance Criteria
- Packages can declare operation profiles.
- Operation profiles render immutable saga graphs.
- Risk and reversibility are visible before execution.

---

## Make `skiff ops` the normal operation entrypoint

### ID
skiff-m4-005

### Priority
P0

### Type
feature

### Labels
cli, ux, ops

### Dependencies
skiff-m4-004

### Description
Make operational saga creation discoverable and simple through `skiff ops`.

#### Subtasks
- Add `skiff ops list <target>`.
- Add `skiff ops plan <target> <operation>`.
- Add `skiff ops run <target> <operation>`.
- Keep existing:
  - `ops inspect`
  - `ops events`
  - `ops resume`
  - `ops watch`
  - `ops approve`
  - `ops reject`
  - `ops cancel`
  - `ops compensate`
- Show package-provided operations with summaries, params, risk, reversibility, and example commands.
- Emit operation ID and saga ID in every mutating JSON output.
- Add `--plan-only`, `--dry-run`, `--yes`, and `--trace-id`.
- Support direct mode and API mode.

#### Likely Files
- `internal/cli/ops.go`
- `internal/client/ops.go`
- `internal/ops/store.go`
- `internal/ops/service.go`
- `tests/golden/cli/`

#### Design
Human output for `ops list db`:

```text
Operations for db

backup
  risk: low
  reversibility: reversible

restore
  risk: high
  reversibility: partially_reversible

primary-switchover-update
  risk: high
  reversibility: partially_reversible
  package: skiff.dev/postgres-ha@1.2.0
```

JSON output should include facts and commands:

```json
{
  "ok": true,
  "target": "db",
  "operations": [
    {
      "name": "primary-switchover-update",
      "risk": "high",
      "reversibility": "partially_reversible",
      "command": "skiff ops plan db primary-switchover-update --format json",
      "mutating": false
    }
  ]
}
```

#### Testing / Validation
- Golden tests for `ops list`.
- Golden tests for `ops plan`.
- Golden tests for `ops run --plan-only`.
- Direct and API output parity tests.
- JSON output always includes trace ID and next commands.

#### Gotchas
Do not make users know saga kinds for normal work. `saga` is the recovery/debug layer; `ops` is the user-facing operation layer.

#### Acceptance Criteria
- Users can discover and run package operations without knowing saga internals.
- Every operation is resumable and auditable.
- JSON output is agent-friendly.

---

## Deprecate confusing CLI paths

### ID
skiff-m4-006

### Priority
P1

### Type
task

### Labels
cli, migration, ux

### Dependencies
skiff-m4-005

### Description
Separate user-friendly commands from recovery/debug commands.

#### Subtasks
- Mark `skiff saga start` deprecated in human help.
- Include `deprecated: true` and replacement command in JSON output for `saga start`.
- Route `skiff stateful update-release` through `ops run <group> update-release`.
- Route `skiff stateful replace-member` through `ops run <group> replace-member`.
- Keep `skiff saga inspect/watch/resume/approve/reject/cancel/compensate`.
- Update command grouping so `pkg`, `deploy`, `ops`, `database`, and `stateful` are normal workflows; `saga` is advanced recovery.
- Update docs and examples.

#### Likely Files
- `internal/cli/saga.go`
- `internal/cli/stateful.go`
- `internal/cli/ops.go`
- `internal/cli/command.go`
- `docs/dev/cli.md`

#### Testing / Validation
- Existing command tests continue passing.
- Deprecated commands print replacement commands.
- Compatibility aliases produce the same durable operation/saga objects as new commands.
- JSON output remains valid and spinner-free.

#### Gotchas
Do not remove behavior abruptly. Existing examples and operators may still rely on old command names.

#### Acceptance Criteria
- Normal UX points to `ops`.
- Advanced saga recovery remains available.
- Old commands are compatibility aliases, not separate implementations.

---

## Extend plugin and recipe capability APIs for package steps

### ID
skiff-m4-007

### Priority
P0

### Type
feature

### Labels
plugin, recipe, saga

### Dependencies
skiff-m4-004

### Description
Allow packages to implement application-specific safety checks and step execution without arbitrary provider mutation.

#### Subtasks
- Add package step capability declarations.
- Add typed params and result schemas for package steps.
- Extend current stateful recipe capabilities beyond:
  - stop
  - start
  - health
  - backup
  - restore
  - detect role
- Add step capability methods:
  - `Plan`
  - `Run`
  - `Resume`
  - `Compensate`
  - `Doctor`
- Add host adapter that maps package step capability responses into saga step results.
- Ensure package hooks cannot receive raw cloud SDK clients.
- Store provider operation IDs and step results before waiting.
- Redact secrets from params, results, events, and doctor findings.

#### Likely Files
- `pkg/pluginapi/types.go`
- `pkg/sagaapi/`
- `internal/plugins/`
- `internal/stateful/recipe.go`
- `internal/saga/steps/package/`
- `tests/conformance/plugin/`

#### Design
Package steps are typed extension points. They can inspect application state through approved runtime/admin endpoints or package-owned commands, and they can return typed Skiff results.

Example step kinds:

```text
postgres.verify_replica_lag
postgres.switchover
mysql.verify_group_members
kafka.verify_isr
nats.jetstream.verify_quorum
redis.cluster.verify_slots
search.verify_cluster_health
```

#### Testing / Validation
- Fake package step can plan, run, resume, and doctor.
- Unsupported step kind fails with actionable error.
- Failed package step writes structured failure with retriable flag.
- Secret redaction tests.

#### Gotchas
Do not let package steps mutate cloud resources directly. Cloud changes still go through provider abstractions or Skiff saga steps.

#### Acceptance Criteria
- Packages can provide typed operational steps.
- Step execution is resumable and auditable.
- Package actions are transparent to direct mode and `skiffd`.

---

## Create package conformance harness

### ID
skiff-m4-008

### Priority
P0

### Type
task

### Labels
packages, tests, conformance

### Dependencies
skiff-m4-002

skiff-m4-007

### Description
Create a reusable conformance suite for package manifests, schemas, compiler integration, and operation profiles.

#### Subtasks
- Add `skiff pkg verify --conformance`.
- Support conformance against local package directories and OCI refs.
- Add fixtures under `tests/fixtures/packages/`.
- Validate:
  - package manifest schema
  - lockfile compatibility
  - config schemas
  - rendered IR
  - operation profile explain output
  - rendered saga graphs
  - doctor checks
  - CLI examples
- Add optional live validation hooks used by Apple e2e package tests.

#### Likely Files
- `tests/conformance/packages/package_suite.go`
- `tests/conformance/packages/fake_package_test.go`
- `internal/packages/conformance.go`
- `internal/cli/pkg.go`

#### Testing / Validation
- Conformance suite passes against a fake package.
- Conformance suite fails with useful diagnostics for broken manifests.
- CLI examples from package docs are parsed and checked.

#### Gotchas
Keep conformance deterministic. Do not require network or Apple provider for the base conformance suite.

#### Acceptance Criteria
- Package authors can verify a package locally.
- Skiff can test package compatibility without deploying a workload.
- Later package beads reuse this harness.

---

## Add Apple StatefulGroup e2e support

### ID
skiff-m4-009

### Priority
P0

### Type
feature

### Labels
apple, stateful, e2e, provider

### Dependencies
skiff-m4-005

skiff-m3-013

### Description
Extend the Apple Silicon provider and e2e harness so non-managed packages can be validated as real StatefulGroups.

#### Subtasks
- Add Apple provider support for multi-member StatefulGroup deployment.
- Create one Apple container per member.
- Create one persistent Apple volume per member.
- Attach each volume to the member container at the configured mount path.
- Assign stable member identity, member ordinal, and host ports.
- Support member replacement with volume movement.
- Support in-place member update.
- Persist resource/provider IDs into object state.
- Run against RustFS object state, matching the existing Apple e2e pattern.
- Add gated make target `make e2e-apple-stateful`.
- Add env gate `SKIFF_APPLE_STATEFUL_E2E=1`.

#### Likely Files
- `internal/provider/applecontainer/provider.go`
- `internal/provider/applecontainer/stateful.go`
- `tests/e2e/apple_stateful_test.go`
- `tests/e2e/harness_test.go`
- `docs/dev/e2e-matrix.md`
- `Makefile`

#### Design
The existing Apple e2e validates RustFS object state, signed releases, runner lifecycle, direct status/events/doctor/ops, local `skiffd`, and canary saga replay. This bead extends that harness from one Caddy service to a real three-member StatefulGroup.

The test should create:

```text
RustFS object state
  +
member-0 Apple container + persistent volume
member-1 Apple container + persistent volume
member-2 Apple container + persistent volume
```

Each member should expose:
- workload health port
- admin/status port
- metrics/debug endpoint if needed by package tests

#### Testing / Validation
- Gated Apple e2e deploys a three-member StatefulGroup.
- Test runs ordered in-place update.
- Test runs replace-member/move-volume.
- Test verifies object state, events, operation IDs, saga IDs, provider IDs, direct status, and local `skiffd` API status.
- Test verifies data survives member restart and replacement.

#### Gotchas
The Apple provider is a local validation provider, not a production provider. Do not introduce production semantics that only Apple supports.

#### Acceptance Criteria
- Non-managed package tests can deploy live StatefulGroups locally.
- Apple e2e reports include cleanup commands and provider IDs.
- Stateful operation sagas execute against real member processes and volumes.

---

## Build `skiff-opsem` realistic stateful test app

### ID
skiff-m4-010

### Priority
P0

### Type
feature

### Labels
stateful, e2e, testing, operations

### Dependencies
skiff-m4-009

### Description
Create a lightweight test stateful app that behaves like common distributed systems for operation validation without requiring heavyweight dependencies.

#### Subtasks
- Create a small Go workload named `skiff-opsem`.
- Package it as digest-pinned OCI image or tarball artifact for Apple e2e.
- Persist member state on the mounted volume.
- Expose HTTP admin APIs for:
  - role
  - generation
  - term
  - leader
  - lag
  - quorum
  - partitions
  - ISR
  - slots
  - shards
  - relocation
  - drain
  - stepdown
  - promote
  - catch-up
  - fail injection
- Implement modes:
  - `primary-replica`
  - `raft-groups`
  - `partition-isr`
  - `slot-cluster`
  - `shard-cluster`
- Add deterministic fixtures for three-member clusters.
- Add failure injection:
  - replica lag too high
  - quorum would be lost
  - ISR below min
  - slot coverage missing
  - red shard health
  - catch-up timeout

#### Likely Files
- `tests/fixtures/opsem/`
- `tests/fixtures/opsem/main.go`
- `tests/fixtures/opsem/Dockerfile`
- `tests/e2e/opsem_harness_test.go`
- `internal/testworkloads/opsem/` if shared code is preferred

#### Design
`skiff-opsem` should be boring and explicit. It is not a database or message broker. It is a live process with durable local state and admin APIs that model the operational semantics packages care about.

Example endpoints:

```text
GET  /healthz
GET  /admin/state
POST /admin/stepdown
POST /admin/promote
POST /admin/drain
POST /admin/catch-up
POST /admin/fail
POST /admin/recover
```

Example state in `partition-isr` mode:

```json
{
  "member": 1,
  "partitions": [
    {
      "topic": "orders",
      "partition": 0,
      "leader": 0,
      "replicas": [0, 1, 2],
      "isr": [0, 1, 2],
      "min_isr": 2
    }
  ]
}
```

#### Testing / Validation
- Unit tests for each mode.
- Unit tests for persisted state reload.
- Apple e2e deploys three members.
- Apple e2e restarts one member and verifies state survives.
- Apple e2e replaces one member and verifies volume movement.

#### Gotchas
Do not overbuild a simulator. The app only needs enough behavior to validate package operation logic and saga execution.

#### Acceptance Criteria
- `skiff-opsem` can model all required package operation semantics.
- Tests can inject unsafe states deterministically.
- Package validation can run without Kafka, Redis, OpenSearch, MySQL, Postgres, or NATS images.

---

## Validate operation DSL with live semantic e2e

### ID
skiff-m4-011

### Priority
P0

### Type
task

### Labels
saga, dsl, e2e, validation

### Dependencies
skiff-m4-010

skiff-m4-008

### Description
Create the real validation test for the operation profile API/DSL using live `skiff-opsem` StatefulGroups on Apple provider.

#### Subtasks
- Add `tests/e2e/apple_operation_profiles_test.go`.
- Deploy `skiff-opsem` in each mode.
- Resolve a test package fixture for each operation profile.
- Render the operation profile into a saga graph.
- Run the saga through `skiff ops run`.
- Verify durable objects:
  - operation intent
  - operation control
  - operation events
  - saga intent
  - saga graph
  - saga control
  - saga events
  - audit record
- Verify direct mode resume after interruption.
- Verify local `skiffd` can inspect operation and saga state.
- Verify unsafe states block before mutation.

#### Likely Files
- `tests/e2e/apple_operation_profiles_test.go`
- `tests/fixtures/packages/opsem-*`
- `internal/ops/profile.go`
- `internal/saga/templates/`

#### Test Scenarios
- `primary-replica`: promote member 1, update old primary member 0, fail back to member 0, update member 1.
- `raft-groups`: update one member only if every RAFT group keeps quorum.
- `partition-isr`: block update when target member removal would drop ISR below min ISR.
- `slot-cluster`: promote replica and verify slot ownership remains complete.
- `shard-cluster`: disable allocation, update one member, re-enable allocation, wait for green/yellow policy.

#### Acceptance Criteria
- Operation DSL is validated by real live member processes, not only unit tests.
- Unsafe operation plans fail before durable mutation where possible.
- Resume works after an intentionally interrupted saga.
- Reports include operation IDs, saga IDs, package refs, provider IDs, object paths, facts, and cleanup status.

---

## Create HA Postgres package

### ID
skiff-m4-012

### Priority
P0

### Type
feature

### Labels
packages, postgres, database, stateful

### Dependencies
skiff-m4-008

skiff-m4-011

### Description
Create `skiff.dev/postgres-ha` with both managed and self-managed modes.

#### Subtasks
- Add managed mode using AWS RDS/Aurora/Multi-AZ where available.
- Add self-managed mode using Patroni-style Postgres semantics.
- Add package config:
  - `mode: managed | self-managed`
  - `version`
  - `size`
  - `storage`
  - `backups`
  - `replicas`
  - `maxReplicaLagBytes`
  - `synchronous`
- Add app binding export for `DATABASE_URL`.
- Add managed operations:
  - failover
  - backup
  - restore
  - rotate credentials
  - inspect writer/readers
- Add self-managed operations:
  - primary switchover update
  - backup
  - restore
  - rejoin replica
  - verify replica lag
- Add `postgres.verify_replica_lag`.
- Add `postgres.switchover`.
- Add `postgres.verify_timeline`.

#### Best-Practice Gates
- Managed mode should prefer provider-native HA and failover APIs.
- Self-managed mode should gate switchover on candidate health and replica lag.
- Patroni-style operations should distinguish planned switchover from emergency failover.
- Manual/emergency failover should be high risk.

#### Apple Validation
Use `skiff-opsem primary-replica`.

Required scenario:

```text
db-1 is primary
db-2 is replica
promote db-2
update db-1
fail back to db-1
update db-2
verify final primary and replica caught up
```

#### Testing / Validation
- Managed compile/plan tests do not require Apple provider.
- Self-managed package passes Apple validation.
- JSON output distinguishes provider-managed failover from member-level operations.
- Unsafe replica lag blocks planned switchover.

#### Acceptance Criteria
- API users can declare HA Postgres with one dependency.
- Operators can run safe primary switchover update.
- Managed and self-managed behavior is explicit in explain and JSON output.

---

## Create HA MySQL package

### ID
skiff-m4-013

### Priority
P0

### Type
feature

### Labels
packages, mysql, database, stateful

### Dependencies
skiff-m4-008

skiff-m4-011

### Description
Create `skiff.dev/mysql-ha` with both managed and self-managed modes.

#### Subtasks
- Add managed mode using RDS/Aurora MySQL or RDS Multi-AZ cluster where available.
- Add self-managed mode using InnoDB Cluster / Group Replication semantics.
- Add MySQL Router binding as the default application endpoint.
- Add config:
  - `mode`
  - `version`
  - `size`
  - `storage`
  - `replicas`
  - `router`
  - `singlePrimary`
- Add operations:
  - controlled primary change
  - rolling update
  - backup
  - restore
  - rejoin instance
  - router health check
- Add step kinds:
  - `mysql.verify_group_members`
  - `mysql.verify_primary`
  - `mysql.set_primary`
  - `mysql.verify_router`

#### Best-Practice Gates
- Self-managed default is single-primary with at least three members.
- Verify all expected members are `ONLINE` before disruptive operations.
- Use controlled primary change for planned operations.
- Emergency failover is separate, high risk, and must not be used for normal update.
- Multi-primary mode requires explicit config and elevated risk classification.

#### Apple Validation
Use `skiff-opsem primary-replica`.

Required scenario:
- Verify primary.
- Move primary to candidate.
- Update old primary.
- Optionally fail back.
- Update candidate.
- Verify router endpoint points to current primary.

#### Testing / Validation
- Managed compile/plan tests.
- Self-managed Apple package validation.
- Unsafe member state blocks controlled primary change.
- Router binding is emitted as app dependency output.

#### Acceptance Criteria
- Users can depend on HA MySQL simply.
- Operators can run controlled primary update.
- Emergency failover is clearly separate from planned switchover.

---

## Create Kafka package

### ID
skiff-m4-014

### Priority
P0

### Type
feature

### Labels
packages, kafka, stateful, streaming

### Dependencies
skiff-m4-008

skiff-m4-011

### Description
Create `skiff.dev/kafka` for KRaft Kafka clusters.

#### Subtasks
- Emit StatefulGroup brokers with stable identities and volumes.
- Default to three brokers for HA.
- Default replication factor to 3.
- Default `min.insync.replicas` to 2.
- Disable unclean leader election by default.
- Add operation profile `partition_quorum_rolling_update`.
- Add steps:
  - `kafka.verify_cluster`
  - `kafka.verify_isr`
  - `kafka.verify_no_under_replicated_partitions`
  - `kafka.move_leadership`
  - `kafka.verify_broker_caught_up`
- Expose bootstrap connection binding.

#### Best-Practice Gates
- Block update when stopping the target broker would violate min ISR.
- Block update on under-replicated or offline partitions unless explicit break-glass.
- Prefer moving leadership away from the broker before update where supported.
- Verify catch-up before moving to the next broker.

#### Apple Validation
Use `skiff-opsem partition-isr`.

Required scenarios:
- Successful broker rolling update with ISR intact.
- Blocked update when target broker is required to satisfy min ISR.
- Resume after interruption while broker is catching up.

#### Testing / Validation
- Package conformance passes.
- Apple validation passes.
- Explain output surfaces broker IDs, partitions affected, ISR, min ISR, and under-replicated partition count.

#### Acceptance Criteria
- Kafka package models broker-safe rolling updates.
- Users do not need to hand-write ISR checks.
- Unsafe partition states block mutation.

---

## Create NATS JetStream package

### ID
skiff-m4-015

### Priority
P0

### Type
feature

### Labels
packages, nats, jetstream, stateful

### Dependencies
skiff-m4-008

skiff-m4-011

### Description
Create `skiff.dev/nats-jetstream` with JetStream-aware operations.

#### Subtasks
- Emit StatefulGroup with unique `server_name` per member.
- Configure cluster name and routes.
- Configure JetStream store dir on persistent volume.
- Expose client, route, and monitor ports.
- Default stream replicas to 3 unless overridden.
- Add operation profile `raft_group_rolling_update`.
- Add steps:
  - `nats.jetstream.verify_quorum`
  - `nats.jetstream.verify_stream_leaders`
  - `nats.jetstream.stepdown_leader`
  - `nats.jetstream.verify_catch_up`
- Use `nats stream info -j` or `$JS.API.STREAM.INFO.<stream>` for real package implementation docs.

#### Best-Practice Gates
- Verify meta quorum.
- Verify each stream/consumer RAFT group keeps quorum after excluding the member being updated.
- Verify stream leader exists.
- Avoid updating multiple members at once.
- Verify catch-up before proceeding.

#### Apple Validation
Use `skiff-opsem raft-groups`.

Required scenarios:
- Successful update when every RAFT group has quorum.
- Blocked update when excluding target member would lose quorum.
- Leader stepdown before update.
- Catch-up wait after restart.

#### Testing / Validation
- Existing `examples/stateful/jetstream/skiff.yaml` should still validate.
- Package-backed JetStream example becomes preferred.
- JSON facts include stream, leader, replicas, current members, excluded member, and required quorum.

#### Acceptance Criteria
- JetStream operations are quorum-aware.
- `VerifyQuorum` is implemented as a JetStream-specific package step.
- Operators can see exactly which streams/consumers gate an update.

---

## Create Redis package family

### ID
skiff-m4-016

### Priority
P0

### Type
feature

### Labels
packages, redis, stateful, cache

### Dependencies
skiff-m4-008

skiff-m4-011

### Description
Create Redis HA packages for both Sentinel-style HA and Redis Cluster slots.

#### Subtasks
- Create `skiff.dev/redis-ha`.
- Create `skiff.dev/redis-cluster`.
- `redis-ha` uses primary/replica plus Sentinel semantics.
- `redis-cluster` uses slot ownership and master/replica semantics.
- Add bindings for Redis endpoint and Sentinel/cluster discovery.
- Add `redis-ha` operations:
  - sentinel-aware failover update
  - backup/snapshot
  - restore
  - replica rejoin
- Add `redis-cluster` operations:
  - slot-aware failover update
  - verify slots
  - verify replicas
  - manual failover
- Require explicit high-risk approval for `FORCE` and `TAKEOVER`-style operations.

#### Best-Practice Gates
- Sentinel mode verifies Sentinel quorum and observed new primary.
- Cluster mode verifies all slots are covered.
- Cluster mode verifies replica promotion through normal manual failover by default.
- `TAKEOVER` semantics are critical risk and should require break-glass.

#### Apple Validation
Use:
- `skiff-opsem primary-replica` for `redis-ha`.
- `skiff-opsem slot-cluster` for `redis-cluster`.

Required scenarios:
- Sentinel-style promotion and update.
- Slot-aware failover without slot loss.
- Blocked operation when slot coverage would be incomplete.
- High-risk path requires approval.

#### Testing / Validation
- Package conformance passes.
- Apple validation passes for both packages.
- JSON output reports role, slot coverage, config epoch, and approval requirement.

#### Acceptance Criteria
- Redis simple HA and sharded cluster are separate, clear packages.
- Dangerous failover modes are not hidden behind normal update.
- Slot safety is validated before mutation.

---

## Create Elastic/OpenSearch package family

### ID
skiff-m4-017

### Priority
P0

### Type
feature

### Labels
packages, elasticsearch, opensearch, search

### Dependencies
skiff-m4-008

skiff-m4-011

### Description
Create search packages for Elasticsearch and OpenSearch with shared shard-aware operation profiles.

#### Subtasks
- Create `skiff.dev/opensearch-ha`.
- Create `skiff.dev/elasticsearch-ha`.
- Share common search package internals where possible.
- Emit StatefulGroup with:
  - stable node identity
  - persistent volumes
  - discovery seed hosts
  - TLS-ready config
  - data/master role config where supported
- Default to at least three nodes for HA.
- Add operation profile `shard_allocation_rolling_update`.
- Add steps:
  - `search.verify_cluster_health`
  - `search.disable_replica_allocation`
  - `search.flush`
  - `search.stop_node`
  - `search.start_node`
  - `search.enable_allocation`
  - `search.wait_for_recovery`

#### Best-Practice Gates
- Verify health before operation.
- Block red health unless break-glass approval is given.
- Disable replica allocation before a short node restart/update.
- Flush before restart where appropriate.
- Re-enable allocation and wait for recovery before next member.
- Mark downgrade unsupported or irreversible.

#### Apple Validation
Use `skiff-opsem shard-cluster`.

Required scenarios:
- Successful rolling update from green health.
- Blocked update from red health.
- Allocation disable/enable sequence is visible in saga events.
- Recovery wait completes before next node.

#### Testing / Validation
- Package conformance passes.
- Apple validation passes.
- Explain output shows shard allocation setting changes and health wait criteria.

#### Acceptance Criteria
- Search packages provide safe rolling operation defaults.
- Red cluster health blocks normal mutation.
- OpenSearch and Elasticsearch share behavior where safe but remain separate packages.

---

## Add package validation matrix and CI/release gates

### ID
skiff-m4-018

### Priority
P0

### Type
task

### Labels
e2e, ci, packages, validation

### Dependencies
skiff-m4-012

skiff-m4-013

skiff-m4-014

skiff-m4-015

skiff-m4-016

skiff-m4-017

### Description
Add a single validation entrypoint proving every non-managed package can deploy a stateful app and run its operations through Apple provider.

#### Subtasks
- Add `make e2e-apple-stateful-packages`.
- Add env gate `SKIFF_APPLE_STATEFUL_PACKAGES_E2E=1`.
- Run package conformance for each first-party package.
- Deploy `skiff-opsem` through Apple provider for every non-managed package mode.
- Run each package’s required operation profile scenarios.
- Verify direct mode status, events, doctor, ops inspect, ops watch, and saga inspect.
- Verify local `skiffd` can rebuild and inspect state from RustFS.
- Produce JSON reports with:
  - package name
  - package version/digest
  - mode
  - operation IDs
  - saga IDs
  - provider IDs
  - object paths
  - facts
  - blocked unsafe scenarios
  - cleanup status
  - recommended next commands
- Update e2e capability matrix.
- Update GA checklist.

#### Likely Files
- `tests/e2e/apple_stateful_packages_test.go`
- `tests/e2e/coverage_matrix_data_test.go`
- `docs/dev/e2e-matrix.md`
- `docs/release/ga-checklist.md`
- `Makefile`

#### Package Matrix
Required Apple validation:

| Package | Mode | `skiff-opsem` mode |
|---|---|---|
| `postgres-ha` | self-managed | `primary-replica` |
| `mysql-ha` | self-managed | `primary-replica` |
| `kafka` | self-managed | `partition-isr` |
| `nats-jetstream` | self-managed | `raft-groups` |
| `redis-ha` | self-managed | `primary-replica` |
| `redis-cluster` | self-managed | `slot-cluster` |
| `opensearch-ha` | self-managed | `shard-cluster` |
| `elasticsearch-ha` | self-managed | `shard-cluster` |

Managed packages:
- Compile.
- Plan.
- Explain.
- Provider contract tests.
- Optional AWS live gates.

#### Testing / Validation
- Local package conformance remains mandatory and deterministic.
- Apple stateful package tests are optional but release-blocking unless waived.
- Reports include enough information to resume or debug.

#### Gotchas
Do not make normal PR CI depend on Apple Silicon. This is a gated e2e/release validation target.

#### Acceptance Criteria
- Every non-managed package has live StatefulGroup operation validation.
- Managed package validation is clearly separated.
- Release checklist can prove package operational safety.

---

## Add docs, examples, and migration guide

### ID
skiff-m4-019

### Priority
P1

### Type
docs

### Labels
docs, examples, ux

### Dependencies
skiff-m4-018

### Description
Document the package-first UX, operation profiles, and validation model.

#### Subtasks
- Add package UX docs.
- Add operation profile docs.
- Add `skiff-opsem` validation docs.
- Add migration guide from `stack.databases[]` to `stack.dependencies[]`.
- Add examples:
  - API + HA Postgres
  - API + HA MySQL
  - API + Kafka
  - API + NATS JetStream
  - API + Redis HA
  - API + Redis Cluster
  - API + OpenSearch
- Update existing stateful docs to explain:
  - in-place release update
  - VM replacement with volume movement
  - package-specific topology operation
- Document `skiff ops` as the normal operation path.
- Document `skiff saga` as advanced recovery/debug path.

#### Likely Files
- `docs/packages/overview.md`
- `docs/packages/operations.md`
- `docs/packages/validation.md`
- `docs/recipes/api-postgres-ha.md`
- `docs/recipes/api-mysql-ha.md`
- `docs/recipes/api-kafka.md`
- `docs/recipes/api-jetstream.md`
- `docs/recipes/api-redis.md`
- `docs/recipes/api-opensearch.md`
- `docs/recipes/stateful-group.md`
- `docs/dev/cli.md`

#### Testing / Validation
- Docs link check passes.
- Example specs validate.
- Example specs explain.
- Commands in docs have matching CLI tests or smoke coverage.

#### Acceptance Criteria
- A new user can add HA Postgres to an API service without learning sagas.
- An operator can discover and run complex stateful operations.
- The docs explain why package operations are explicit sagas, not hidden controllers.

---

## Final milestone acceptance

M4 is complete when:

- `stack.dependencies[]` and `skiff.lock.json` are implemented and documented.
- `skiff pkg` supports add, update, list, explain, verify, and bundle.
- Package-expanded resources carry provenance in IR, explain, release/runtime manifests, operations, and sagas.
- `skiff ops` can list, plan, and run package operations.
- `skiff saga start` is deprecated for normal creation.
- Package step capabilities are typed, auditable, resumable, and direct-mode compatible.
- First-party packages exist for:
  - HA Postgres
  - HA MySQL
  - Kafka
  - NATS JetStream
  - Redis HA
  - Redis Cluster
  - OpenSearch HA
  - Elasticsearch HA
- Every non-managed package passes Apple StatefulGroup package validation.
- Managed package modes pass compile, plan, explain, and provider contract tests.
- `skiff-opsem` validates operation semantics without heavyweight dependencies.
- Docs and examples make the simple path obvious and the complex path possible.
