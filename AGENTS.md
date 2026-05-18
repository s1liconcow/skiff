# AGENTS.md

This file gives coding agents and automation tools the context needed to work safely in the Skiff repository.

Skiff is a clusterless cloud deployment and operations platform. Its core rule is:

```text
Object storage is durable truth.
skiffd is the normal facade with rebuildable in-memory views.
The CLI can operate directly for recovery.
One VM runs one workload replica by default.
Operations are explicit sagas.
```

Do not infer architecture from Kubernetes, Terraform, or operator frameworks. Skiff has its own model.

## Branch workflow

Prefer keeping work directly on `main` in this repository. Use short-lived branches
only when a task explicitly needs isolation, then merge them back into `main`
promptly.

## What Skiff is

Skiff deploys and operates cloud-native workloads by compiling simple specs into cloud primitives.

Mapping:

```text
VM = pod
cloud autoscaling group = deployment
cloud load balancer target group = service
cloud IAM role = workload identity
object storage = durable desired state and audit history
skiff-runner = VM-local workload reconciler
skiffd = stateless API/TUI/agent facade
skiff saga = explicit operational journey
```

Skiff is AWS-first but must be provider-extensible.

## Non-negotiable invariants

When implementing anything, preserve these invariants:

1. **Object storage is the durable source of truth.**  
   Durable state belongs in object storage as signed or schema-versioned documents.

2. **`skiffd` is not a database.**  
   It can maintain in-memory indexes, event streams, auth context, plugin execution, and TUI/API state. It must be rebuildable from object storage.

3. **Direct CLI mode must keep working.**  
   A user or agent must be able to diagnose and recover when `skiffd` is down.

4. **Write object storage before memory.**  
   If a command mutates state, it must write the durable object first, then update in-memory views.

5. **Control docs are also lock docs.**  
   Do not add separate lock files. Use compare-and-swap updates on the relevant control document.

6. **Immutable history is create-only.**  
   Release objects, operation intents, saga intents, saga graphs, events, and audit records must not be overwritten.

7. **Every long-running operation must be resumable.**  
   Store provider operation IDs and step results before waiting.

8. **Every mutating production operation must be auditable.**  
   Include actor, trace ID, target, operation/saga ID, risk, and summary.

9. **Sagas are typed operational graphs.**  
   Avoid hidden always-running reconciliation. Avoid arbitrary mystery controllers.

10. **The VM is the workload isolation boundary by default.**  
    Do not introduce multi-service VM packing unless a future explicit design changes this.

11. **Security is defaulted.**  
    Do not rely on users to remember to sign releases, pin artifacts, encrypt state, or avoid SSH.

12. **Cloud primitives must remain visible.**  
    Explain ASGs, target groups, launch templates, IAM roles, log groups, and provider IDs.

## Expected repository layout

Use this layout unless a bead explicitly says otherwise:

```text
cmd/
  skiff/
  skiffd/
  skiff-runner/
  skiff-worker/

internal/
  artifact/
  auth/
  authz/
  bootstrap/
  cicd/
  cli/
  client/
  compiler/
  config/
  cost/
  debug/
  deploy/
  doctor/
  drift/
  events/
  gc/
  importers/
  index/
  ir/
  objstore/
  observability/
  ops/
  plugins/
  provider/
  release/
  runner/
  saga/
  security/
  spec/
  state/
  stateful/
  status/
  terraform/
  tui/

pkg/
  pluginapi/
  sagaapi/
  sdk/

docs/
examples/
tests/
```

Avoid creating public `pkg/` APIs unless the work clearly requires external plugin or SDK use.

## Durable object-state layout

Use path helpers. Do not hand-concatenate object keys in business logic.

Representative paths:

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
resources/by-provider/<provider>/<kind>/<id>.json

indexes/services.json
indexes/active-sagas.json
indexes/recent-events.json

audit/<yyyy-mm-dd>/<ulid>.json
```

Object categories:

| Object type | Mutation |
|---|---|
| release manifest | create-only |
| runtime manifest | create-only |
| operation intent | create-only |
| operation event | create-only |
| saga intent | create-only |
| saga graph | create-only |
| saga event | create-only |
| audit event | create-only |
| service control | CAS only |
| operation control | CAS only |
| saga control | CAS only |
| stateful member control | CAS only |
| derived indexes | rebuildable |

## CAS and leases

Mutable state uses compare-and-swap.

Expected interface shape:

```go
type ObjectStore interface {
    Get(ctx context.Context, key string) (*Object, error)
    Head(ctx context.Context, key string) (*ObjectMeta, error)
    Create(ctx context.Context, key string, body []byte, opts PutOptions) (*ObjectMeta, error)
    CompareAndSwap(ctx context.Context, key string, previousETag string, body []byte, opts PutOptions) (*ObjectMeta, error)
    List(ctx context.Context, prefix string, opts ListOptions) ([]ObjectMeta, error)
}
```

Rules:

- `Create` is for immutable objects and must fail if the object already exists.
- `CompareAndSwap` is for control docs and must fail if the ETag is stale.
- A lease must live inside the control document it protects.
- ETag is a fencing token, not a content hash.
- Retry CAS conflicts with bounded backoff.
- Surface `LEASE_HELD`, `LEASE_LOST`, and `PRECONDITION_FAILED` distinctly.

## skiffd rules

`skiffd` is the normal user and agent interface, but it is stateless with respect to durable state.

It may:

- serve API requests
- power the TUI
- maintain in-memory indexes
- stream events
- execute sagas
- host plugins
- enforce auth and approvals
- call cloud provider APIs

It must not:

- become the durable state store
- require a separate database for correctness
- hide uncommitted state only in memory
- mutate memory before object storage
- prevent direct CLI recovery

Memory index rules:

- Rebuild from object storage on startup.
- Publish immutable snapshots through atomic swap or equivalent.
- Include freshness metadata in API responses.
- Support `fresh=true` or equivalent to reload critical objects.
- Treat malformed objects as findings, not fatal server crashes.

## CLI rules

All user-facing commands must support, where applicable:

```bash
--format json
--no-color
--yes
--trace-id
```

JSON mode must:

- emit valid JSON only
- suppress spinners and ANSI escape codes
- include operation IDs or saga IDs for resumability
- include error code, summary, trace ID, and recommended next commands
- classify mutating recommendations by safety and reversibility

Standard exit codes:

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

Direct mode examples:

```bash
skiff --direct --state s3://skiff-state-prod status payments-api
skiff --direct --state s3://skiff-state-prod rollback payments-api --to previous-stable
```

## Saga rules

Sagas are explicit, typed operational workflows.

A saga has:

```text
intent.json
graph.json
control.json
events/*
artifacts/*
```

Each saga step should implement:

```go
type Step interface {
    Kind() string
    Plan(ctx context.Context, req StepRequest) (*StepPlan, error)
    Run(ctx context.Context, req StepRequest) (*StepResult, error)
    Resume(ctx context.Context, req StepRequest) (*StepResult, error)
    Compensate(ctx context.Context, req StepRequest, result StepResult) (*StepResult, error)
    Doctor(ctx context.Context, req StepRequest) ([]Finding, error)
}
```

Step requirements:

- typed params
- idempotent execution
- resumability
- structured result
- risk classification
- reversibility classification
- explicit compensation where possible
- clear events

Risk levels:

```text
low
medium
high
critical
```

Reversibility levels:

```text
reversible
compensatable
partially_reversible
irreversible
```

Do not describe compensation as rollback unless it truly restores prior state.

## Provider rules

Provider code must stay behind `internal/provider`.

Provider interface responsibilities include:

- plan
- apply
- inspect service
- inspect resources
- start rollout
- watch rollout
- rollback support
- logs
- metrics
- debug
- drift inputs
- cost inputs

Do not expose cloud SDK types through public Skiff APIs or common IR.

AWS is first, but common interfaces should leave room for later providers.

Provider resources must be tagged consistently:

```text
skiff.dev/service
skiff.dev/env
skiff.dev/managed
skiff.dev/release
skiff.dev/graph
```

## Compiler and IR rules

The public spec is user-facing. The typed IR is implementation-facing.

Do:

- parse strict YAML/JSON
- reject unknown fields unless explicitly configured otherwise
- apply safe defaults
- validate before compilation
- produce deterministic IR
- include source references for explain output
- keep cloud/provider-specific details out of common spec where possible

Do not:

- mirror Kubernetes object models
- create a loosely typed `map[string]any` resource graph
- leak AWS ARNs into provider-neutral IR
- silently drop unsafe or unsupported configuration

## Release and artifact rules

Production releases must be signed and immutable.

Rules:

- release manifests are create-only
- runtime manifests are create-only
- artifact refs should be digest-pinned in production
- release verification must check schema, digest, signature, service/env, expiry, and rollback policy
- `current.json` or release pointers are not trust roots
- runner must verify before execution

Artifact formats may include:

- binary
- tarball
- OCI image as package format

Do not confuse OCI packaging with Kubernetes container semantics. The VM is the isolation boundary.

## Runner rules

`skiff-runner` runs on each workload VM.

It must:

- parse runner config from user-data/cloud-init
- discover cloud instance identity
- read service control directly from object storage
- fetch and verify signed release/runtime manifests
- prepare artifact
- render systemd unit
- start one workload
- run health checks
- expose local status
- emit state transitions
- handle drain/shutdown

Runner states:

```text
Booting
FetchingManifest
VerifyingRelease
PreparingArtifact
RenderingConfig
StartingWorkload
WaitingForHealth
Serving
Draining
Stopping
Stopped
Failed
```

Do not make the runner depend on `skiffd`.

## Security rules

Do not store plaintext secrets in object state or events.

Use secret references and cloud secret managers.

Default posture:

- encrypted state bucket
- versioned state bucket
- signed release objects
- digest-pinned production artifacts
- least-privilege IAM
- no SSH-first debug
- audited debug sessions
- scoped runner read access
- deployer/skiffd conditional write access
- approval gates for high-risk operations
- redaction in logs, bundles, and CLI output

If a feature needs broader permissions, add:

- a policy explanation
- a test proving least privilege where possible
- an approval or risk classification if production-impacting

## Agent output rules

When producing output for agents:

- facts must be separate from hypotheses
- recommended actions must include command strings
- mutating actions must say `mutating: true`
- mutating actions must include safety and reversibility
- high-risk actions must require approval
- include operation/saga IDs
- include trace ID
- include enough context to continue after interruption

Example:

```json
{
  "ok": false,
  "code": "ROLLOUT_FAILED",
  "summary": "payments-api rollout failed target health",
  "trace_id": "tr_01J...",
  "facts": [
    {"type": "target_health", "message": "1 new target unhealthy"},
    {"type": "runner_state", "message": "runner is WaitingForHealth"}
  ],
  "recommended_actions": [
    {
      "id": "inspect_logs",
      "command": "skiff logs payments-api --instance i-abc123 --since 20m --format json",
      "mutating": false
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

## Testing expectations

Prefer layered tests.

When validating Go code locally, prefer repository-local caches so sandboxed
agents do not fail on system cache permissions:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/gomod go test ./...
```

### Unit tests

Use for:

- schema validation
- path helpers
- canonical JSON
- CAS and lease behavior
- compiler output
- provider lowering
- policy generation
- signing/verification
- saga graph execution

### Integration tests

Use fake object store and fake provider for:

- deploy flow
- rollback flow
- saga execution
- doctor scenarios
- drift detection
- direct recovery mode

### Optional cloud tests

Real cloud tests must be:

- gated by environment variables
- isolated by unique names/prefixes
- heavily tagged
- non-required for normal PRs
- cleanup-safe

### Golden tests

Use for:

- CLI JSON output
- CLI human output where stable
- generated Terraform
- generated CI templates
- example specs
- object-state schemas

### Race tests

Use for:

- CAS control docs
- lease contention
- in-memory index publication
- event streaming

## Bead documents

Implementation work is organized into three milestone bead documents:

```text
skiff_beads_m1_foundation.md
skiff_beads_m2_operations.md
skiff_beads_m3_adoption_production.md
```

When implementing a bead:

1. Read the bead fully.
2. Implement dependencies first.
3. Preserve the architecture in this file.
4. Add tests listed in the bead.
5. Do not widen scope without creating a new bead or note.
6. Update docs/examples when user-facing behavior changes.

## Docs
The docs/ folder has very long implementation designs that can fill up context.  Please treat the bead as the primary source of truth and only consult docs/DESIGN.md to resolve ambiguity.  

## Common gotchas

### Do not add a new durable database

Skiff’s durable state is object storage. If you think a feature needs a database, first ask whether it can be represented as:

- immutable objects
- CAS control docs
- append-only events
- rebuildable in-memory views
- derived index objects

### Do not create separate lock objects

Locks live inside the control document they protect.

### Do not hide provider IDs

Doctor, explain, status, and events should show relevant cloud resource IDs.

### Do not silently ignore unsupported imports

Kubernetes/Terraform/adoption tools must warn or error clearly when behavior cannot be represented.

### Do not make direct mode second-class

Direct mode is the recovery path. It must be tested.

### Do not call compensation rollback unless exact

A saga compensation may reduce harm without restoring prior state. Be precise.

### Do not make plugin actions opaque

Plugins return typed patches or typed step results. They do not mutate cloud resources arbitrarily behind Skiff’s back.

## Recommended implementation loop

For each task:

```text
1. Add or update schema/types.
2. Add path helpers if state is involved.
3. Add tests for invalid input.
4. Implement core logic with fake/memory dependencies.
5. Add CLI/API surface.
6. Add JSON output.
7. Add human output.
8. Add event/audit writes if mutating.
9. Add direct-mode coverage if relevant.
10. Add docs/examples if user-facing.
```

## Code style

- Prefer small, explicit structs over untyped maps.
- Prefer interfaces at package boundaries, not everywhere.
- Keep provider-specific code out of core packages.
- Keep CLI rendering out of business logic.
- Keep object paths centralized.
- Use context everywhere for I/O.
- Preserve original cloud/provider error context.
- Add structured Skiff error codes around provider errors.
- Avoid background goroutines without clear lifecycle management.
- Avoid unbounded in-memory caches.

## Acceptance mindset

A feature is not done when it works once. It is done when:

- it is represented in object state correctly
- it is resumable if long-running
- it is diagnosable through `doctor` or events
- it has JSON output for agents
- it has human output for operators
- it has tests for failure modes
- it is safe by default
- it can be explained without reading source code

<!-- bv-agent-instructions-v2 -->

---

## Beads Workflow Integration

This project uses [beads_rust](https://github.com/Dicklesworthstone/beads_rust) (`br`) for issue tracking and [beads_viewer](https://github.com/Dicklesworthstone/beads_viewer) (`bv`) for graph-aware triage. Issues are stored in `.beads/` and tracked in git.

### Using bv as an AI sidecar

bv is a graph-aware triage engine for Beads projects (.beads/beads.jsonl). Instead of parsing JSONL or hallucinating graph traversal, use robot flags for deterministic, dependency-aware outputs with precomputed metrics (PageRank, betweenness, critical path, cycles, HITS, eigenvector, k-core).

**Scope boundary:** bv handles *what to work on* (triage, priority, planning). `br` handles creating, modifying, and closing beads.

**CRITICAL: Use ONLY --robot-* flags. Bare bv launches an interactive TUI that blocks your session.**

#### The Workflow: Start With Triage

**`bv --robot-triage` is your single entry point.** It returns everything you need in one call:
- `quick_ref`: at-a-glance counts + top 3 picks
- `recommendations`: ranked actionable items with scores, reasons, unblock info
- `quick_wins`: low-effort high-impact items
- `blockers_to_clear`: items that unblock the most downstream work
- `project_health`: status/type/priority distributions, graph metrics
- `commands`: copy-paste shell commands for next steps

```bash
bv --robot-triage        # THE MEGA-COMMAND: start here
bv --robot-next          # Minimal: just the single top pick + claim command

# Token-optimized output (TOON) for lower LLM context usage:
bv --robot-triage --format toon
```

Before claiming, verify current state with `br show <id> --json` or `br ready --json`. `recommendations` can include graph-important blocked or assigned work; only `quick_ref.top_picks` and non-empty `claim_command` fields represent claimable work.

#### Other bv Commands

| Command | Returns |
|---------|---------|
| `--robot-plan` | Parallel execution tracks with unblocks lists |
| `--robot-priority` | Priority misalignment detection with confidence |
| `--robot-insights` | Full metrics: PageRank, betweenness, HITS, eigenvector, critical path, cycles, k-core |
| `--robot-alerts` | Stale issues, blocking cascades, priority mismatches |
| `--robot-suggest` | Hygiene: duplicates, missing deps, label suggestions, cycle breaks |
| `--robot-diff --diff-since <ref>` | Changes since ref: new/closed/modified issues |
| `--robot-graph [--graph-format=json\|dot\|mermaid]` | Dependency graph export |

#### Scoping & Filtering

```bash
bv --robot-plan --label backend              # Scope to label's subgraph
bv --robot-insights --as-of HEAD~30          # Historical point-in-time
bv --recipe actionable --robot-plan          # Pre-filter: ready to work (no blockers)
bv --recipe high-impact --robot-triage       # Pre-filter: top PageRank scores
```

### br Commands for Issue Management

```bash
br ready              # Show issues ready to work (no blockers)
br list --status=open # All open issues
br show <id>          # Full issue details with dependencies
br create --title="..." --type=task --priority=2
br update <id> --status=in_progress
br close <id> --reason="Completed"
br close <id1> <id2>  # Close multiple issues at once
br sync --flush-only  # Export DB to JSONL
```

### Workflow Pattern

1. **Triage**: Run `bv --robot-triage` to find the highest-impact actionable work
2. **Claim**: Use `br update <id> --status=in_progress`
3. **Work**: Implement the task
4. **Complete**: Use `br close <id>`
5. **Sync**: Always run `br sync --flush-only` at session end

### Key Concepts

- **Dependencies**: Issues can block other issues. `br ready` shows only unblocked work.
- **Priority**: P0=critical, P1=high, P2=medium, P3=low, P4=backlog (use numbers 0-4, not words)
- **Types**: task, bug, feature, epic, chore, docs, question
- **Blocking**: `br dep add <issue> <depends-on>` to add dependencies

### Session Protocol

```bash
git status              # Check what changed
git add <files>         # Stage code changes
br sync --flush-only    # Export beads changes to JSONL
git commit -m "..."     # Commit everything
git push                # Push to remote
```

<!-- end-bv-agent-instructions -->
