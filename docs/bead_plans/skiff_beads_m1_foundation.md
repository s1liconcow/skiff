# Skiff Beads - Milestone 1: Core Object-State Foundation and Runner MVP

**Milestone:** M1 - Foundation  
**Audience:** Junior and mid-level engineers implementing Skiff under senior staff guidance.  
**Source of truth:** This document is self-contained. Do not rely on any earlier design note to understand the work.  
**Core product rule:** Object storage is the durable source of truth. `skiffd` is the normal API/TUI/agent facade with rebuildable in-memory views. The CLI has direct recovery mode. No extra datastore is required for correctness.

## Objective

Build the minimum reliable substrate: repository structure, configuration, object-storage state, CAS control documents, signing primitives, `skiffd` with in-memory views, direct/API CLI modes, AWS bootstrap, and a runner that can boot one signed workload.

## Deliverable outcome

At the end of this milestone, Skiff can be bootstrapped, state can be inspected, a signed release can be published into object storage, and `skiff-runner` can fetch and run that release without any durable service other than object storage.

## Dependency overlay

The bead IDs below are intentionally ordered, but the dependency field on each bead is authoritative. When in doubt, implement dependencies first.

```text
skiff-m1-001
  ├─ skiff-m1-002
  ├─ skiff-m1-003 ── skiff-m1-004 ── skiff-m1-015 ── skiff-m1-020
  ├─ skiff-m1-005 ── skiff-m1-006 ── skiff-m1-016 ── skiff-m1-017 ── skiff-m1-018
  ├─ skiff-m1-007
  ├─ skiff-m1-008
  ├─ skiff-m1-009 ── skiff-m1-010
  ├─ skiff-m1-011
  └─ skiff-m1-012 ── skiff-m1-013 ── skiff-m1-014
                             └─ skiff-m1-019
All core paths converge on skiff-m1-021, the runner fixture release gate.
```

## Shared implementation conventions

- Use Go for all binaries and libraries.
- Use small packages with narrow interfaces; avoid provider-specific code leaking into spec, IR, saga, or CLI packages.
- Every mutating operation must be idempotent or explicitly guarded by a compare-and-swap control document.
- Every externally visible command must support `--format json`, `--no-color`, `--yes`, and `--trace-id` where applicable.
- Every object key written to the state bucket must be deterministic and documented by the path helpers.
- Never write memory cache first. Write object storage first, then update `skiffd` in-memory views from the successful write.
- Tests should favor tiny deterministic fixtures over broad flaky integration scenarios.
- Error messages must tell the user or agent the resource, operation, observed state, and next reasonable command.

---

## Initialize repository, module boundaries, and developer tooling

### ID

skiff-m1-001

### Priority

P0

### Type

task

### Labels

repo, go, foundation

### Dependencies

- (none)

### Description

Create the initial Skiff repository structure, Go module setup, lint/test tooling, and binary entrypoints. This bead creates the skeleton that every later bead depends on.

### Subtasks

- Create top-level Go module and initial package directories.
- Add `cmd/skiff`, `cmd/skiffd`, and `cmd/skiff-runner` entrypoints with placeholder commands.
- Add `internal/` packages for state, object storage, spec, compiler, provider, release, runner, index, and security.
- Add `pkg/` packages only for public extension APIs that we deliberately expose later.
- Add Makefile or Taskfile targets for build, test, lint, generate, and local smoke tests.
- Add CI skeleton that runs `go test ./...` and formatting checks.
- Document package ownership and layering rules in `docs/dev/package-boundaries.md`.

### Likely Files

- `go.mod`
- `go.sum`
- `cmd/skiff/main.go`
- `cmd/skiffd/main.go`
- `cmd/skiff-runner/main.go`
- `internal/state/README.md`
- `internal/objstore/README.md`
- `internal/provider/README.md`
- `Makefile`
- `.github/workflows/ci.yml`
- `docs/dev/package-boundaries.md`

### Design

Keep package boundaries strict from day one. The `spec` package parses user-facing YAML. The `compiler` package lowers specs into typed IR. The `provider` package realizes IR through cloud APIs. The `state` package reads and writes Skiff object-state documents. The CLI and `skiffd` orchestrate these packages but should not contain business logic.

Use placeholder commands initially:

```bash
skiff version
skiffd version
skiff-runner version
```

Each command should print version, commit, build date, and JSON output when requested. This seems small, but it establishes the command shape that all future work follows.

### Testing / Validation

Run `go test ./...`, `go vet ./...`, and formatting in CI. Add a smoke test that executes all three binaries with `version --format json` and validates parseable JSON. Confirm the repository builds on a clean checkout without cloud credentials.

### Gotchas

Avoid creating public APIs too early. Most packages should stay under `internal/` until a plugin or SDK requirement is concrete. Avoid circular dependencies by keeping `internal/types` small and by not letting provider code import CLI packages.

### Acceptance Criteria

- Fresh checkout builds all three binaries.
- `go test ./...` passes.
- Version commands produce human and JSON output.
- Package boundary README clearly explains allowed imports.

---

## Define configuration loading for CLI, skiffd, and runner

### ID

skiff-m1-002

### Priority

P0

### Type

task

### Labels

config, cli, skiffd, runner

### Dependencies

- skiff-m1-001

### Description

Implement a shared but mode-aware configuration system for direct CLI mode, API mode, `skiffd`, and `skiff-runner`.

### Subtasks

- Define config structs for environment, state bucket URI, cloud provider, region, KMS key, auth mode, and log level.
- Support config from file, environment variables, and flags with clear precedence.
- Add `skiff config show --format json` for debugging effective config.
- Add runner config parsing from cloud-init/user-data JSON.
- Validate required fields differently for direct CLI, API CLI, `skiffd`, and runner modes.
- Ensure config errors are actionable and include the source field that failed.

### Likely Files

- `internal/config/config.go`
- `internal/config/loader.go`
- `internal/config/validate.go`
- `cmd/skiff/config.go`
- `cmd/skiffd/main.go`
- `cmd/skiff-runner/main.go`
- `tests/config/testdata/*.yaml`

### Design

Configuration should not silently guess production state. Require explicit `env`, `provider`, `region`, and state bucket URI outside local tests. Keep the default local state backend as an in-memory or filesystem test backend, not an implicit cloud bucket.

Precedence should be:

1. command-line flags
2. environment variables
3. config file
4. compiled defaults

Recommended config file:

```yaml
env: prod
provider: aws
region: us-west-2
stateBucket: s3://skiff-state-prod
kmsKey: alias/skiff-prod
mode: api
apiURL: https://skiff.example.com
```

The runner user-data should be minimal and immutable enough to bootstrap:

```json
{
  "skiff": {
    "env": "prod",
    "service": "payments-api",
    "provider": "aws",
    "region": "us-west-2",
    "state_bucket": "s3://skiff-state-prod",
    "control_key": "services/payments-api/control.json"
  }
}
```

### Testing / Validation

Unit test precedence order with table-driven tests. Test invalid configs for each mode. Add tests for redacting sensitive values in `config show`. Validate runner user-data parsing with malformed JSON, missing fields, and unknown fields.

### Gotchas

Do not put secret values in config. Secret references are acceptable; plaintext secrets are not. Do not make `skiffd` required in CLI config because direct mode is a core recovery path.

### Acceptance Criteria

- Config can be loaded from flags, env vars, and file with deterministic precedence.
- `skiff config show --format json` redacts sensitive values.
- Runner config validation rejects missing state bucket, service, env, or control key.
- Direct mode and API mode are both representable.

---

## Implement object store interface and deterministic in-memory backend

### ID

skiff-m1-003

### Priority

P0

### Type

task

### Labels

state, object-store, testing

### Dependencies

- skiff-m1-001

### Description

Create the provider-neutral object store abstraction used by Skiff state, release objects, event logs, and tests. Add an in-memory backend with real compare-and-swap semantics.

### Subtasks

- Define `ObjectStore` with `Get`, `Head`, `Create`, `CompareAndSwap`, and `List`.
- Define versioned object metadata containing key, ETag, version ID, timestamps, metadata, and content type.
- Define canonical errors: not found, already exists, precondition failed, conflict, permission denied.
- Implement an in-memory backend that enforces create-if-absent and ETag-based CAS.
- Make ETags deterministic per object version for test assertions.
- Add concurrency tests with multiple goroutines contending on the same key.

### Likely Files

- `internal/objstore/store.go`
- `internal/objstore/errors.go`
- `internal/objstore/memory/store.go`
- `internal/objstore/memory/store_test.go`

### Design

The object store interface is Skiff’s core coordination substrate. Keep it intentionally small:

```go
type ObjectStore interface {
    Get(ctx context.Context, key string) (*Object, error)
    Head(ctx context.Context, key string) (*ObjectMeta, error)
    Create(ctx context.Context, key string, body []byte, opts PutOptions) (*ObjectMeta, error)
    CompareAndSwap(ctx context.Context, key string, previousETag string, body []byte, opts PutOptions) (*ObjectMeta, error)
    List(ctx context.Context, prefix string, opts ListOptions) ([]ObjectMeta, error)
}
```

`Create` must fail if the object already exists. `CompareAndSwap` must fail if the provided ETag is not the current ETag. All state primitives must be built on this interface, not on cloud-specific SDK types.

### Testing / Validation

Run race-enabled tests for concurrent writes. Test create-if-absent, failed create, successful CAS, failed CAS, prefix list ordering, and metadata preservation. Add property-style tests where concurrent increments via CAS produce a final version count equal to successful updates.

### Gotchas

Do not implement delete in the base interface unless a later bead proves it is needed. Deletion is dangerous for append-only history and should be handled through lifecycle policies or explicit GC APIs, not casual state code.

### Acceptance Criteria

- ObjectStore interface compiles and is cloud-neutral.
- In-memory backend enforces CAS correctly under concurrency.
- Canonical errors are used by all tests.
- No state package imports a cloud SDK.

---

## Implement AWS S3 object store backend with conditional writes

### ID

skiff-m1-004

### Priority

P0

### Type

task

### Labels

aws, s3, object-store, cas

### Dependencies

- skiff-m1-003

- skiff-m1-002

### Description

Implement the production AWS object store backend using S3 conditional writes for create-if-absent and compare-and-swap updates.

### Subtasks

- Create `s3store.Store` implementing `ObjectStore`.
- Use `If-None-Match: *` for `Create`.
- Use `If-Match: <etag>` for `CompareAndSwap`.
- Normalize S3 ETags by stripping quotes before exposing them.
- Map AWS errors into canonical object-store errors.
- Support server-side KMS encryption options.
- Add request logging with trace IDs but never log object bodies by default.
- Add integration tests gated by an environment variable for real S3 buckets.

### Likely Files

- `internal/objstore/s3/store.go`
- `internal/objstore/s3/errors.go`
- `internal/objstore/s3/store_test.go`
- `internal/aws/session.go`
- `tests/integration/test_s3_store.go`

### Design

This backend must be boring and exact. Skiff correctness depends on conditional writes. Do not emulate CAS with read-then-unconditional-write.

The expected S3 behavior:

- `Create` succeeds only when no current object exists.
- `CompareAndSwap` succeeds only when the ETag matches the currently stored object.
- A failed conditional write returns a canonical precondition/conflict error that callers can retry or surface.

Example implementation shape:

```go
func (s *Store) CompareAndSwap(ctx context.Context, key, etag string, body []byte, opts PutOptions) (*ObjectMeta, error) {
    out, err := s.client.PutObject(ctx, &s3.PutObjectInput{
        Bucket: aws.String(s.bucket),
        Key: aws.String(key),
        Body: bytes.NewReader(body),
        IfMatch: aws.String(etag),
        ContentType: aws.String(opts.ContentType),
        ServerSideEncryption: types.ServerSideEncryptionAwsKms,
        SSEKMSKeyId: aws.String(opts.KMSKeyID),
    })
    if err != nil { return nil, classify(err) }
    return metaFromPut(key, out), nil
}
```

### Testing / Validation

Unit test AWS error classification with fake Smithy errors. Integration test against a temporary bucket when `SKIFF_TEST_S3_BUCKET` is set. Validate concurrent CAS behavior with two writers. Confirm KMS options are passed to S3 calls using a stub client.

### Gotchas

S3 multipart uploads can produce non-content MD5 ETags; Skiff should treat ETag only as an opaque version token. Do not assume ETag is a content hash. Keep body size for control docs small enough to avoid multipart writes.

### Acceptance Criteria

- S3 backend implements the object store interface.
- `Create` and `CompareAndSwap` use S3 conditional headers.
- AWS errors map to canonical object-store errors.
- Integration tests prove contention behavior against real S3 when enabled.

---

## Define object-state paths, schemas, and canonical JSON encoding

### ID

skiff-m1-005

### Priority

P0

### Type

task

### Labels

state, schema, paths

### Dependencies

- skiff-m1-003

### Description

Create the canonical path helpers and JSON schema conventions for all Skiff state objects. This bead makes object state legible and prevents ad hoc key construction.

### Subtasks

- Define path helper functions for services, releases, operations, sagas, resources, indexes, audit events, and observations.
- Define stable Go structs for service control, operation intent/control, release manifest, saga intent/control, resource records, and events.
- Implement canonical JSON serialization with stable field ordering and no timestamp ambiguity.
- Add schema version fields to every durable object.
- Add `skiff state path ...` developer command to print expected paths for a service, release, operation, or saga.
- Document path conventions with examples.

### Likely Files

- `internal/state/paths/paths.go`
- `internal/state/schema/*.go`
- `internal/state/canonical/json.go`
- `cmd/skiff/state.go`
- `docs/dev/object-state-layout.md`
- `tests/state/path_test.go`

### Design

Object keys are part of the user-facing debuggability story. An operator should be able to inspect state with cloud CLI tools and understand what they see.

Required core paths:

```text
services/<service>/control.json
services/<service>/releases/<release>/release.json
services/<service>/releases/<release>/runtime-manifest.json
services/<service>/operations/<op>/intent.json
services/<service>/operations/<op>/control.json
services/<service>/operations/<op>/events/<ulid>.json
sagas/<saga>/intent.json
sagas/<saga>/graph.json
sagas/<saga>/control.json
sagas/<saga>/events/<ulid>.json
resources/by-logical/<kind>/<name>.json
indexes/services.json
audit/<yyyy-mm-dd>/<ulid>.json
```

Never concatenate paths inline in business logic. Always use helpers.

### Testing / Validation

Test path helpers for names with invalid characters, reserved words, and unusual environment names. Test canonical JSON round-trips. Add golden files for representative service control, release, and saga objects.

### Gotchas

Name normalization must be strict. If `payments_api` and `payments-api` both normalize to the same key, reject one explicitly. Avoid allowing arbitrary path separators in service names.

### Acceptance Criteria

- All durable object paths are generated through helpers.
- Every durable object has a schema version field.
- Canonical JSON output is stable across test runs.
- Object-state layout documentation is complete enough for manual inspection.

---

## Implement signed object and release verification primitives

### ID

skiff-m1-006

### Priority

P0

### Type

task

### Labels

security, release, signing

### Dependencies

- skiff-m1-005

### Description

Implement signing and verification primitives for release manifests, runtime manifests, operation intents, and other critical immutable objects.

### Subtasks

- Define `Signer` and `Verifier` interfaces independent of the concrete signing mechanism.
- Implement local test signer/verifier using generated keys for unit tests.
- Implement digest calculation over canonical JSON.
- Attach signature metadata and signer identity to immutable objects.
- Verify object schema, digest, signature, intended env/service, and expiry where applicable.
- Add CLI commands `skiff release verify` and `skiff object verify`.

### Likely Files

- `internal/security/signing/interface.go`
- `internal/security/signing/local.go`
- `internal/security/digest.go`
- `internal/release/verify.go`
- `cmd/skiff/release.go`
- `tests/security/signing_test.go`
- `tests/golden/release/*.json`

### Design

Signing must be an interface so Skiff can support different production signers later. Do not wire the core release code to one specific signing tool. The invariant is what matters:

```text
critical immutable object -> canonical bytes -> digest -> signature -> verification before trust
```

Release verification should check:

1. schema version is supported
2. service/env match the VM or operation target
3. artifact reference is immutable in production
4. runtime manifest digest matches
5. signature is valid
6. release is not expired
7. rollback policy is respected by the caller

### Testing / Validation

Golden tests should verify that changing one byte in a signed release fails verification. Test wrong service/env, expired release, missing signature, invalid digest, and unsupported schema. CLI verification should emit both human-readable and JSON failure reasons.

### Gotchas

Be careful not to sign pretty-printed JSON in one place and compact JSON in another. Always sign canonical bytes. Do not let callers verify `current.json` as if it were a trusted release; pointers are not trust roots.

### Acceptance Criteria

- Signer/verifier interfaces exist and are covered by tests.
- Release verification fails on tampering, wrong env/service, or expiration.
- `skiff release verify` can verify a golden release file.
- Signing code is cloud- and provider-neutral.

---

## Implement service control documents, CAS updates, and leases

### ID

skiff-m1-007

### Priority

P0

### Type

task

### Labels

state, lease, cas, concurrency

### Dependencies

- skiff-m1-004

- skiff-m1-005

### Description

Implement the mutable service control document and lease primitives that coordinate deploys, rollbacks, and service-level operations.

### Subtasks

- Define `ServiceControl` schema with desired release, stable release, active operation, lease, version, and timestamps.
- Implement `GetServiceControl`, `CreateServiceControl`, and `UpdateServiceControlCAS`.
- Implement `AcquireLease`, `HeartbeatLease`, `ReleaseLease`, and stale lease takeover logic.
- Ensure lease and service mutation happen in the same CAS-updated control document.
- Add contention-friendly retry helpers with bounded exponential backoff.
- Add structured errors for lease held, lease lost, stale ETag, and invalid transition.

### Likely Files

- `internal/state/service_control.go`
- `internal/state/lease.go`
- `internal/state/cas.go`
- `internal/state/service_control_test.go`
- `internal/state/lease_test.go`

### Design

The service control document is both state and lock. Do not create a separate lock object. The same control doc must hold the lease and the mutation being protected.

Example shape:

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
    "owner": "skiffd/instance-a",
    "token": "lease_01J...",
    "generation": 42,
    "expires_at": "2026-05-16T20:05:00Z"
  },
  "version": 18,
  "updated_at": "2026-05-16T20:04:30Z"
}
```

Only the holder of the latest ETag and lease token can continue mutating.

### Testing / Validation

Use the in-memory backend for deterministic concurrent lease tests. Spawn multiple contenders and assert only one lease is acquired. Test lease expiration and takeover. Test heartbeat failure after another writer changes the control doc. Test version increments and updated timestamps.

### Gotchas

Clock skew matters. Use a clock interface in tests and prefer short-lived leases with heartbeat renewal. Never use lease ownership alone as proof; ETag/CAS is the fencing mechanism.

### Acceptance Criteria

- Service control schema is implemented.
- Lease acquisition is atomic with the control document update.
- Concurrent acquisition admits exactly one owner.
- Lease loss is detected and surfaced clearly.

---

## Implement append-only event logs and audit records

### ID

skiff-m1-008

### Priority

P0

### Type

task

### Labels

events, audit, state

### Dependencies

- skiff-m1-004

- skiff-m1-005

### Description

Implement immutable event creation for operations, sagas, service changes, and audit records. Events are the user-visible timeline for Skiff.

### Subtasks

- Define event schemas for operation events, saga events, service events, and audit events.
- Use ULID or comparable sortable IDs in event object keys.
- Write events with create-if-absent only.
- Add optional hash-chain fields for operation/saga events.
- Implement event listing by prefix and bounded recent-event reads.
- Add `skiff events` command for local debugging.
- Ensure event writes are best-effort for diagnostics but mandatory for operation state transitions where auditability is required.

### Likely Files

- `internal/events/event.go`
- `internal/events/log.go`
- `internal/events/hashchain.go`
- `internal/state/eventlog.go`
- `cmd/skiff/events.go`
- `tests/events/eventlog_test.go`

### Design

Events should be immutable, append-only facts. They are not the current state; control docs are current state. Events explain how we got there.

Event keys:

```text
services/payments-api/operations/op_01J/events/01J...-started.json
sagas/saga_01J/events/01J...-approval-required.json
audit/2026-05-16/01J...json
```

A good event includes scope, actor, trace ID, severity, type, message, timestamp, and structured data. Keep messages concise but useful for humans and agents.

### Testing / Validation

Test create-only behavior by attempting duplicate event IDs. Test ordering by ULID. Test list pagination with small limits. Test hash-chain verification for an operation with multiple events.

### Gotchas

Do not let event listing become the only way to derive current state. It is okay to use events to rebuild history, but hot state should live in control docs and in-memory indexes.

### Acceptance Criteria

- Events are written immutably with create-if-absent.
- `skiff events` can list service and saga events.
- Audit events include actor, trace ID, action, and target.
- Duplicate event IDs fail safely.

---

## Implement skiffd stateless server skeleton and API surface

### ID

skiff-m1-009

### Priority

P0

### Type

task

### Labels

skiffd, api, server

### Dependencies

- skiff-m1-002

- skiff-m1-003

- skiff-m1-005

### Description

Build the initial `skiffd` server as a stateless facade over object storage and provider APIs. This bead does not implement full deploys yet; it establishes the API, health, auth hook points, and dependency injection.

### Subtasks

- Create HTTP/gRPC or Connect server skeleton with health, version, status, and config endpoints.
- Add dependency injection for object store, provider, signer/verifier, index, and event bus.
- Add request IDs and trace IDs to every request.
- Add JSON logging and structured error responses.
- Expose `/healthz`, `/readyz`, and `/version`.
- Add basic auth middleware interfaces, even if initial local mode allows unauthenticated dev use.
- Add graceful shutdown.

### Likely Files

- `cmd/skiffd/main.go`
- `internal/skiffd/server.go`
- `internal/skiffd/routes.go`
- `internal/skiffd/errors.go`
- `internal/skiffd/middleware.go`
- `api/proto/skiff.proto`
- `tests/skiffd/server_test.go`

### Design

`skiffd` is not a durable database. It should be restartable at any time. It reads object storage and provider state, writes object storage through CAS, and maintains only rebuildable in-memory views.

Initial endpoints:

```text
GET /healthz
GET /readyz
GET /version
GET /v1/env
GET /v1/services
GET /v1/events/recent
```

All mutating endpoints added later must flow through state primitives, not custom object writes.

### Testing / Validation

Use httptest or Connect test clients. Validate JSON error envelopes. Test graceful shutdown. Test readiness returns false until index has completed initial load. Test trace ID propagation to logs.

### Gotchas

Avoid embedding cloud credentials in `skiffd` config responses. Keep auth and authorization pluggable but do not defer request context design; every handler should receive actor and trace metadata.

### Acceptance Criteria

- `skiffd` starts locally with memory object store.
- Health and version endpoints work in human and JSON modes.
- Server can be stopped gracefully.
- Readiness reflects index initialization state.

---

## Build skiffd in-memory index and refresh loop

### ID

skiff-m1-010

### Priority

P0

### Type

task

### Labels

skiffd, index, state, ux

### Dependencies

- skiff-m1-005

- skiff-m1-007

- skiff-m1-008

- skiff-m1-009

### Description

Implement rebuildable in-memory indexes inside `skiffd` for services, sagas, resources, operations, and recent events.

### Subtasks

- Define immutable `IndexSnapshot` with services, sagas, operations, resources, and recent events.
- Use atomic snapshot publication for lock-light reads.
- Implement full rebuild from object storage prefixes.
- Implement hot refresh for known control docs.
- Implement local update hints after successful writes.
- Add freshness metadata to API responses.
- Add `?fresh=true` behavior for critical endpoints to reload the relevant control object directly.
- Expose index stats and generation via admin endpoint.

### Likely Files

- `internal/index/snapshot.go`
- `internal/index/indexer.go`
- `internal/index/hints.go`
- `internal/index/fresh.go`
- `internal/skiffd/routes_services.go`
- `tests/index/indexer_test.go`

### Design

`skiffd` should feel fast without becoming a source of truth. The index is a view, not state. If it is lost, it is rebuilt from object storage.

Design:

```go
type Index struct {
    current atomic.Value // *IndexSnapshot
}
```

On startup, list `services/*/control.json`, `sagas/*/control.json`, selected resource records, and recent event prefixes. Publish an initial snapshot. Then refresh hot controls periodically and apply hints after local writes.

Every response using the index should include freshness metadata:

```json
{
  "index": {
    "source": "memory",
    "generation": 1842,
    "last_full_scan_at": "2026-05-16T20:58:00Z",
    "freshness_seconds": 4
  }
}
```

### Testing / Validation

Unit test index rebuild from a memory store fixture. Test malformed objects are logged and skipped, not fatal. Test atomic snapshot reads while rebuilds run. Test `fresh=true` uses direct object reads. Test index freshness metadata.

### Gotchas

Do not store unbounded event history in memory. Keep a recent window and lazy-load detailed histories by prefix. Be careful with prefix scans in large buckets; add pagination from the start.

### Acceptance Criteria

- `skiffd` exposes service and saga lists from memory index.
- Index rebuild can recover from an empty process.
- `fresh=true` bypasses stale memory for a specific object.
- Malformed state objects do not crash `skiffd`.

---

## Implement CLI root, modes, JSON output, and API/direct clients

### ID

skiff-m1-011

### Priority

P0

### Type

task

### Labels

cli, agent, direct-mode, api-mode

### Dependencies

- skiff-m1-002

- skiff-m1-009

### Description

Build the `skiff` CLI foundation with direct mode, API mode, structured output, and agent-safe command behavior.

### Subtasks

- Implement root command with global flags: `--config`, `--env`, `--provider`, `--region`, `--state`, `--api`, `--direct`, `--format`, `--no-color`, `--yes`, `--trace-id`.
- Create client interfaces for Skiff operations that can be backed by direct object/provider calls or `skiffd` API calls.
- Implement `skiff version`, `skiff status`, `skiff events`, and `skiff config show` using the shared client interface.
- Define JSON error envelope and exit codes.
- Add shell completion generation.
- Ensure all commands are non-interactive when `--yes` or `--format json` is used.

### Likely Files

- `cmd/skiff/main.go`
- `internal/cli/root.go`
- `internal/cli/output.go`
- `internal/cli/errors.go`
- `internal/client/client.go`
- `internal/client/direct.go`
- `internal/client/api.go`
- `tests/cli/*.go`

### Design

The CLI is a first-class agent interface. Human output can be beautiful, but JSON output must be deterministic and easy to consume.

Exit-code plan:

```text
0 success
1 user/spec error
2 policy denied
3 provider/cloud error
4 rollout/operation failed
5 partial success
6 auth error
7 timeout
8 internal error
```

The client abstraction prevents commands from knowing whether they are using direct mode or API mode.

### Testing / Validation

Golden-test human and JSON output. Test exit codes for representative errors. Test direct mode with memory store and API mode with httptest `skiffd`. Test `--no-color` and `--format json` never emit ANSI codes.

### Gotchas

Do not make prompts appear in CI accidentally. JSON mode must suppress spinners, progress bars, and interactive questions. Do not bury original provider errors; preserve context and add Skiff hints.

### Acceptance Criteria

- CLI supports direct and API clients through the same command surface.
- All initial commands support JSON output.
- Exit codes are documented and tested.
- Agent-safe non-interactive behavior is reliable.

---

## Implement user-facing spec parser, defaults, and validation

### ID

skiff-m1-012

### Priority

P0

### Type

task

### Labels

spec, validation, yaml

### Dependencies

- skiff-m1-001

### Description

Parse Skiff workload specs from YAML/JSON, apply safe defaults, and validate user-facing fields before compilation.

### Subtasks

- Define Go structs for Service, Worker, Job, ManagedDatabase, StatefulGroup, Stack, and shared metadata.
- Implement YAML/JSON decoding with strict unknown-field rejection by default.
- Apply defaults for logs, metrics, health intervals, shutdown grace, machine arch, and rollout strategy.
- Validate names, envs, artifact immutability rules, health checks, scaling bounds, ingress settings, and secret references.
- Produce path-specific diagnostics suitable for CLI, API, and agents.
- Add `skiff validate` command.

### Likely Files

- `internal/spec/types.go`
- `internal/spec/decode.go`
- `internal/spec/defaults.go`
- `internal/spec/validate.go`
- `cmd/skiff/validate.go`
- `tests/spec/*.go`
- `examples/service/skiff.yaml`

### Design

Keep the public spec small and understandable. Do not mirror Kubernetes. The basic service spec should be readable by an application engineer.

Example:

```yaml
apiVersion: skiff.dev/v1alpha1
kind: Service
metadata:
  name: payments-api
  env: prod
artifact:
  type: oci
  ref: registry.example.com/payments-api@sha256:abc123
runtime:
  port: 8080
  health:
    path: /healthz
machine:
  size: small
scale:
  min: 3
  max: 20
network:
  ingress:
    type: public-http
    host: payments.example.com
```

Validation should reject mutable production artifacts, missing health checks for services, public ingress without TLS, invalid names, and unsafe scaling values.

### Testing / Validation

Unit test valid and invalid examples. Golden-test diagnostics. Test strict unknown field rejection. Test production vs non-production validation differences. Test that defaults are visible through `skiff validate --show-defaulted --format yaml`.

### Gotchas

Do not overfit the spec to AWS. Keep provider-specific details behind `provider` overrides when needed, not in the common path. Avoid permissive decoding because typos in infra specs are dangerous.

### Acceptance Criteria

- `skiff validate` works on service examples.
- Validation emits path-specific actionable diagnostics.
- Safe defaults are applied and inspectable.
- Unknown fields are rejected unless an explicit compatibility flag is used.

---

## Define typed IR and base compiler

### ID

skiff-m1-013

### Priority

P0

### Type

task

### Labels

compiler, ir, provider

### Dependencies

- skiff-m1-012

### Description

Compile validated user specs into provider-neutral typed IR that describes cloud resources, runtime manifests, identity, observability, and rollout intent.

### Subtasks

- Define IR graph types for workload identity, instance template, autoscaling group, target group, listener, IAM role, security group rules, log/metric config, and runtime manifest.
- Implement base compiler for a minimal Service spec.
- Implement deterministic resource naming and tagging.
- Attach provenance from spec fields to IR resources for explain output.
- Add `skiff compile --out ir.json` developer command.
- Add semantic diff helpers between two IR graphs.

### Likely Files

- `internal/ir/types.go`
- `internal/ir/resource.go`
- `internal/ir/diff.go`
- `internal/compiler/compiler.go`
- `internal/compiler/service.go`
- `cmd/skiff/compile.go`
- `tests/compiler/*.go`

### Design

The IR is where Skiff becomes explainable. It should not be AWS-specific, but it should be cloud-shaped rather than Kubernetes-shaped.

Every resource should include:

```go
type ResourceMeta struct {
    LogicalID string
    Kind string
    Name string
    Tags map[string]string
    Source []SourceRef
}
```

Required tags in IR:

```text
skiff.dev/service
skiff.dev/env
skiff.dev/managed
skiff.dev/graph
```

`skiff compile` is a dev command, but it is also useful for tests and future provider conformance.

### Testing / Validation

Golden-test IR output for the example service. Test deterministic ordering and naming. Test source references in explain output. Test semantic diff does not flag reorder-only changes.

### Gotchas

Do not let AWS ARNs leak into common IR. Provider lowering can add ARNs later. Avoid creating a generic “resource map” that loses type safety; typed resources are easier to validate and explain.

### Acceptance Criteria

- Minimal Service spec compiles to typed IR.
- IR output is deterministic and golden-tested.
- Resources include Skiff tags and source references.
- `skiff compile` can write machine-readable IR.

---

## Create AWS provider skeleton and tagging/naming conventions

### ID

skiff-m1-014

### Priority

P0

### Type

task

### Labels

aws, provider, ir

### Dependencies

- skiff-m1-013

- skiff-m1-002

### Description

Implement the AWS provider skeleton, SDK client construction, naming rules, tag application, and read-only discovery helpers.

### Subtasks

- Define provider interface with plan, apply placeholder, inspect service, inspect resource, logs, metrics, debug, and rollout hooks.
- Implement AWS client construction from config and environment credentials.
- Implement deterministic AWS resource naming with length limits and collision suffixes.
- Implement common tag conversion from IR.
- Implement read-only discovery for ASG, launch template, target group, IAM role, and log group by Skiff tags.
- Add provider-level error classification.

### Likely Files

- `internal/provider/provider.go`
- `internal/provider/aws/provider.go`
- `internal/provider/aws/names.go`
- `internal/provider/aws/tags.go`
- `internal/provider/aws/discover.go`
- `internal/provider/aws/errors.go`
- `tests/provider/aws/*.go`

### Design

The provider package should translate typed IR into cloud actions. It should not parse YAML, know CLI flags, or mutate Skiff object state directly.

Provider interface sketch:

```go
type Provider interface {
    Name() string
    Plan(ctx context.Context, graph *ir.Graph) (*Plan, error)
    Apply(ctx context.Context, plan *Plan) (*ApplyResult, error)
    InspectService(ctx context.Context, ref ServiceRef) (*ServiceInspection, error)
    StartRollout(ctx context.Context, req RolloutRequest) (*Rollout, error)
}
```

Start with read-only discovery and skeleton plan results. Full apply comes later.

### Testing / Validation

Unit test naming with long service names, special characters, and collision cases. Test tag filters using mocked AWS clients. Test error classification for access denied, not found, throttling, and validation errors.

### Gotchas

AWS resource name limits differ by service. Centralize truncation and suffix logic. Do not depend solely on names for discovery; tags are the long-term source of ownership.

### Acceptance Criteria

- AWS provider can be constructed from config.
- Provider discovers tagged resources read-only.
- Naming and tags are deterministic and tested.
- Provider interface is independent of AWS SDK types.

---

## Implement AWS bootstrap for state bucket, KMS, roles, and policies

### ID

skiff-m1-015

### Priority

P0

### Type

task

### Labels

bootstrap, aws, security

### Dependencies

- skiff-m1-004

- skiff-m1-014

### Description

Implement `skiff bootstrap aws` to create the minimal environment Skiff needs: state bucket, KMS key/alias, bucket policies, and IAM roles.

### Subtasks

- Plan and create state bucket with versioning, public access block, encryption, and lifecycle placeholders.
- Create or discover KMS key/alias for state bucket encryption.
- Create IAM role/policies for deployer, runner, and skiffd with least-privilege prefixes.
- Apply bucket policy requiring TLS and conditional writes on state/control prefixes.
- Write environment root config object after successful bootstrap.
- Support `--dry-run`, `--emit terraform`, and `--yes` modes.
- Make bootstrap idempotent.

### Likely Files

- `cmd/skiff/bootstrap.go`
- `internal/bootstrap/aws.go`
- `internal/bootstrap/policy.go`
- `internal/provider/aws/s3_bootstrap.go`
- `internal/provider/aws/iam_bootstrap.go`
- `examples/bootstrap/aws-minimal.yaml`
- `tests/bootstrap/*.go`

### Design

Bootstrap is the only part that must create the initial state substrate. After bootstrap, normal Skiff operations can be driven by object state.

Minimum created resources:

```text
S3 state bucket
KMS key or alias
bucket policy
deployer role
runner role policy template
skiffd role policy template
root/env control object
```

Bucket policy must block public access and reject unconditional writes for prefixes that require create-if-absent or CAS semantics. Do not make bootstrap require `skiffd`.

### Testing / Validation

Unit test generated policies. Use AWS policy JSON golden files. Integration test in a disposable AWS account if available. Dry-run must show exact resources and policies. Idempotency test should run bootstrap twice against a fake provider and result in no duplicate logical resources.

### Gotchas

Bucket policy conditions are easy to get wrong and can lock out Skiff. Keep an emergency documented break-glass role. Emit policy before apply and keep generated statements minimal.

### Acceptance Criteria

- `skiff bootstrap aws --dry-run` prints a complete plan.
- Bootstrap creates or discovers minimal AWS resources idempotently.
- State bucket uses encryption, versioning, and public access block.
- Conditional write and TLS policies are generated and tested.

---

## Implement runner identity discovery and release manifest fetch

### ID

skiff-m1-016

### Priority

P0

### Type

task

### Labels

runner, aws, release

### Dependencies

- skiff-m1-004

- skiff-m1-006

- skiff-m1-002

### Description

Implement the runner bootstrap path that discovers VM identity, reads the service control document, fetches the desired release, and verifies signed manifests.

### Subtasks

- Parse runner user-data/cloud-init config.
- Discover cloud instance identity and metadata needed for logs and release verification.
- Read service control document from object storage.
- Resolve desired release and fetch release/runtime manifest objects.
- Verify signatures, digests, env/service match, and expiry.
- Persist last accepted release locally for rollback protection policy.
- Emit runner state transitions as local JSON events.

### Likely Files

- `cmd/skiff-runner/main.go`
- `internal/runner/config.go`
- `internal/runner/identity.go`
- `internal/runner/manifest.go`
- `internal/runner/statefile.go`
- `internal/release/fetch.go`
- `tests/runner/manifest_test.go`

### Design

Runner boot path must stay minimal and robust. It should not need `skiffd`. It should need only object storage read access, artifact access, and cloud metadata.

Boot flow:

```text
read config
discover identity
read services/<service>/control.json
fetch desired release
fetch release manifest and runtime manifest
verify everything
write local state
continue to artifact prep
```

Local state should live under `/var/lib/skiff/runner/state.json` and include last accepted release, current state, and verification metadata.

### Testing / Validation

Use memory store for unit tests. Test wrong service/env, missing control doc, missing release, invalid signature, expired release, and stale rollback. Add a fake metadata provider for runner identity.

### Gotchas

Do not assume cloud metadata is always immediately available. Add bounded retries with clear errors. Do not leak release manifest secrets in logs; runtime manifests should contain secret references, not secret values.

### Acceptance Criteria

- Runner can fetch and verify a desired release without skiffd.
- Runner writes a local state file.
- Verification failures are explicit and tested.
- Boot path uses only object store and metadata abstractions.

---

## Implement runner finite-state machine and systemd workload lifecycle

### ID

skiff-m1-017

### Priority

P0

### Type

task

### Labels

runner, systemd, lifecycle

### Dependencies

- skiff-m1-016

### Description

Implement the runner’s explicit finite-state machine and systemd integration for starting, health-checking, draining, and stopping one workload per VM.

### Subtasks

- Define runner states: Booting, FetchingManifest, VerifyingRelease, PreparingArtifact, RenderingConfig, StartingWorkload, WaitingForHealth, Serving, Draining, Stopping, Failed.
- Implement state transition persistence and local status endpoint over Unix socket or localhost.
- Render hardened systemd unit files for workloads.
- Start and restart workload through systemd.
- Implement HTTP and command health checks.
- Implement graceful shutdown and drain hooks.
- Emit runner events and metrics on every state transition.

### Likely Files

- `internal/runner/fsm.go`
- `internal/runner/status.go`
- `internal/runner/systemd.go`
- `internal/runner/health.go`
- `internal/runner/templates/workload.service.tmpl`
- `cmd/skiff-runner/main.go`
- `tests/runner/fsm_test.go`

### Design

The runner should be an idempotent reconciler for one VM, not a scheduler. Systemd is the local process supervisor.

Systemd hardening defaults:

```ini
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
CapabilityBoundingSet=
RestrictSUIDSGID=yes
LockPersonality=yes
```

The runner status endpoint should expose JSON suitable for `skiff doctor`:

```json
{
  "service": "payments-api",
  "release": "2026.05.16.1",
  "state": "Serving",
  "health": "healthy"
}
```

### Testing / Validation

Unit test state transitions with fake systemd and fake health checker. Integration test systemd rendering without requiring root by comparing unit files to goldens. Add local Linux integration tests gated by `SKIFF_TEST_SYSTEMD=1`.

### Gotchas

Be careful with restart loops. Systemd can restart the app, but runner state should still reflect failed health if the app never becomes ready. Do not expose the status endpoint publicly.

### Acceptance Criteria

- Runner FSM is implemented and tested.
- Workload systemd unit rendering is deterministic.
- Health checks move runner into Serving only after success.
- Local status endpoint returns machine-readable runner state.

---

## Implement artifact preparation for OCI, tarball, and binary packages

### ID

skiff-m1-018

### Priority

P0

### Type

task

### Labels

runner, artifact, oci

### Dependencies

- skiff-m1-017

- skiff-m1-006

### Description

Implement artifact preparation for the first supported package types. The VM remains the isolation boundary; containers are treated as packaging when used.

### Subtasks

- Define artifact runtime interface.
- Implement tarball artifact download, digest verification, and unpack.
- Implement binary artifact download, digest verification, permissioning, and command resolution.
- Implement minimal OCI image pull/unpack path or shell out to a well-defined local tool with strict digest pinning.
- Reject mutable image tags in production.
- Place artifacts under deterministic release directories.
- Support local artifact fixtures for tests.

### Likely Files

- `internal/artifact/artifact.go`
- `internal/artifact/tar.go`
- `internal/artifact/binary.go`
- `internal/artifact/oci.go`
- `internal/runner/artifact.go`
- `tests/artifact/*.go`

### Design

Skiff must not confuse artifact format with isolation. The VM is the pod. An OCI image is acceptable as a package format, but Skiff should not promise Kubernetes container semantics.

Directory layout:

```text
/opt/skiff/workloads/<service>/releases/<release>/
/opt/skiff/workloads/<service>/current -> releases/<release>
```

Digest verification is mandatory before execution. Production artifacts must be immutable, ideally digest-pinned.

### Testing / Validation

Test corrupt downloads, wrong digest, missing command, unpack traversal attempts, and idempotent re-prepare of an already prepared release. Add fixture tarball and binary. OCI tests can be gated if they require external tooling.

### Gotchas

Tar extraction must prevent path traversal and symlink surprises. OCI support can become complex; keep v1 minimal and do not expose Docker-compatible promises unless implemented.

### Acceptance Criteria

- Tarball and binary artifacts can be prepared and verified.
- Production mutable tags are rejected.
- Artifact preparation is idempotent.
- Path traversal tests fail safely.

---

## Create local integration harness for object-state workflows

### ID

skiff-m1-019

### Priority

P0

### Type

task

### Labels

testing, integration, local

### Dependencies

- skiff-m1-003

- skiff-m1-007

- skiff-m1-008

- skiff-m1-012

- skiff-m1-013

### Description

Create a local test harness that runs Skiff state workflows entirely against the in-memory object store. This is the release gate for core state semantics.

### Subtasks

- Build test helpers to create envs, services, releases, operations, and events in memory.
- Add end-to-end tests for validate -> compile -> create control doc -> acquire lease -> write release -> update desired release -> append events.
- Add test fixtures for minimal service specs.
- Add utilities for asserting object-state contents by path.
- Add race tests for concurrent service control mutations.
- Ensure tests do not require AWS credentials.

### Likely Files

- `tests/harness/state_harness.go`
- `tests/integration/test_core_state_flow.go`
- `tests/fixtures/services/minimal.yaml`
- `tests/golden/state/*.json`

### Design

Before AWS deployment works, we need proof that the state model is sound. This harness should simulate direct CLI/skiffd workflows using the same state packages that production uses.

Core test chain:

```text
decode spec
apply defaults
validate
compile IR
create service control
acquire service lease
create signed release fixture
CAS desired release
append operation events
release lease
read final state
```

### Testing / Validation

Run in CI on every commit. Use `-race` for the concurrency subset. Golden-test expected object keys and final control documents.

### Gotchas

Do not create a separate fake Skiff implementation in tests. The harness should use real packages and only fake external dependencies such as object storage, signer, and provider.

### Acceptance Criteria

- Core object-state flow passes without cloud credentials.
- Expected state objects are created at documented paths.
- Concurrent mutation tests pass under race detector.
- Test harness is reusable by later milestone tests.

---

## Implement state bucket security policy generation and validation

### ID

skiff-m1-020

### Priority

P0

### Type

task

### Labels

security, policy, aws, state

### Dependencies

- skiff-m1-015

- skiff-m1-006

- skiff-m1-007

### Description

Generate and validate security policies for the state bucket, KMS access, runner reads, deployer writes, and skiffd operations.

### Subtasks

- Define least-privilege policy templates for runner, deployer, skiffd, and break-glass admin.
- Restrict runner role to read only the service control and release prefixes it needs.
- Restrict deployer/skiffd roles to conditional writes on permitted prefixes.
- Require KMS encryption and TLS.
- Add policy lints that fail on wildcard actions/resources outside explicit allowlisted cases.
- Add `skiff policy explain` for generated policies.
- Add tests for dangerous policy regressions.

### Likely Files

- `internal/security/policy/aws_state_bucket.go`
- `internal/security/policy/iam.go`
- `internal/security/policy/lint.go`
- `cmd/skiff/policy.go`
- `tests/security/policy/*.go`

### Design

Security must be defaulted, not delegated to the user. Users should not need to deeply understand bucket policies to get safe behavior.

Policy classes:

```text
runner: read service control and release objects, no writes
deployer: create releases/events, CAS service controls, start rollouts
skiffd: same as deployer plus read indexes/resources
break-glass: tightly documented emergency recovery
```

`skiff policy explain` should show why each permission exists.

### Testing / Validation

Golden-test generated IAM and bucket policies. Lint must catch `s3:*`, `Resource: *`, delete privileges on state prefixes, missing TLS condition, missing encryption condition, and unconditioned writes. Test explain output for each role.

### Gotchas

AWS policy condition keys are subtle. Keep templates small and well-covered. Avoid deleting permissions in default roles; state cleanup should be handled by explicit GC with safeguards.

### Acceptance Criteria

- Generated state policies are least-privilege by default.
- Policy linter blocks dangerous wildcards.
- `skiff policy explain` describes role permissions.
- Runner role cannot write state objects.

---

## End-to-end MVP: boot one VM runner against a signed release fixture

### ID

skiff-m1-021

### Priority

P0

### Type

task

### Labels

e2e, release-gate, runner, aws

### Dependencies

- skiff-m1-015

- skiff-m1-016

- skiff-m1-017

- skiff-m1-018

- skiff-m1-019

- skiff-m1-020

### Description

Prove the foundation by booting a runner, fetching a signed release fixture from object storage, starting a tiny app, and serving health locally. This is the Milestone 1 release gate.

### Subtasks

- Create a tiny hello-world service artifact fixture.
- Publish a signed release and runtime manifest into a test state bucket or memory-backed local runner mode.
- Run `skiff-runner` with fixture config.
- Assert runner reaches Serving state.
- Assert the app health endpoint passes.
- Assert state transitions and local status are correct.
- Add optional AWS EC2 smoke test gated by explicit environment variables.

### Likely Files

- `tests/e2e/test_runner_fixture.go`
- `tests/fixtures/hello-service/`
- `tests/fixtures/releases/`
- `examples/service/hello/skiff.yaml`
- `cmd/skiff-runner/main.go`

### Design

This bead proves that the object-state and runner model works before we implement full AWS deploys. It can run in two modes:

1. local mode using memory/filesystem object store
2. optional AWS mode using a real state bucket and EC2 instance

The local mode is mandatory in CI. AWS mode can be nightly or manual.

### Testing / Validation

CI must run the local e2e. Optional AWS test should create isolated resources, tag them, and clean them up. Validate logs for state transitions. Validate JSON status output.

### Gotchas

Do not let the optional AWS test become required for normal PRs. Keep the fixture app tiny and deterministic. Ensure runner does not require skiffd for this test.

### Acceptance Criteria

- Local e2e proves runner can serve a signed release fixture.
- Runner reaches Serving and exposes healthy status.
- Milestone 1 is not considered complete until this test passes in CI.
- Optional AWS smoke test is documented and gated.
