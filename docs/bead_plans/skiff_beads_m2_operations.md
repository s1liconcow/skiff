# Skiff Beads - Milestone 2: Stateless Deploys, Observability, Doctor, and Sagas

**Milestone:** M2 - Operations  
**Audience:** Junior and mid-level engineers implementing Skiff under senior staff guidance.  
**Source of truth:** This document is self-contained. Do not rely on any earlier design note to understand the work.  
**Core product rule:** Object storage remains the durable source of truth. `skiffd` provides the normal API/TUI/agent facade and hot in-memory views. The CLI can operate directly for recovery.

## Objective

Turn the foundation into a usable platform for stateless services. Implement AWS service deployment, release publishing, rollouts, rollback, canary sagas, logs, metrics, status, doctor, event streaming, and agent-oriented remediation.

## Deliverable outcome

At the end of this milestone, a team can deploy a stateless service on AWS, watch rollout progress, tail logs, inspect metrics, run doctor, canary a release, roll back safely, and use sagas for explicit operational journeys.

## Dependency overlay

```text
skiff-m2-001 ── skiff-m2-002 ── skiff-m2-003
         │              │
         │              ├─ skiff-m2-009
         │              └─ skiff-m2-018
         │
skiff-m1 compiler/provider chain ── skiff-m2-004 ── skiff-m2-005 ── skiff-m2-006 ── skiff-m2-007 ── skiff-m2-008
                                                                                 │
                                                                                 └─ skiff-m2-009
Observability chain: skiff-m2-010 + skiff-m2-011 ── skiff-m2-012 ── skiff-m2-013 ── skiff-m2-016
UX chain: skiff-m2-015 + skiff-m2-017 + skiff-m2-020
Release gate: skiff-m2-019 depends on deploy, rollout, observability, status, and doctor.
```

## Shared implementation conventions

- Every deploy, rollback, restore, rotation, and repair is represented as an explicit operation or saga with immutable intent, CAS-updated control, and append-only events.
- Provider actions must be resumable. Store cloud operation IDs in operation or saga control objects before waiting on them.
- Prefer typed step kinds over arbitrary scripting. Scripts can be implementation details, not the public operational model.
- Every user-facing operation must have `plan`, `start`, `watch`, `doctor`, and `events` paths where applicable.
- Human output should explain. JSON output should enable agents to act deterministically.
- All operations must classify risk and reversibility when there is a mutating production action.
- Never hide cloud primitives. Surface ASG, target group, launch template, alarm, and instance identifiers in explain/doctor output.

---

## Implement saga data model and object-state persistence

### ID
skiff-m2-001

### Priority
P0

### Type
task

### Labels
saga, state, operations

### Dependencies
skiff-m1-005

skiff-m1-007

skiff-m1-008

### Description
Create the durable saga model used for canaries, rollbacks, database restores, key rotations, failovers, and other operational journeys.

#### Subtasks
- Define SagaIntent, SagaGraph, SagaNode, SagaControl, StepResult, StepFailure, and SagaEvent schemas.
- Add path helpers for saga intent, graph, control, events, artifacts, and result objects.
- Implement create-only writes for intent and graph.
- Implement CAS updates for saga control.
- Implement event append for saga events.
- Add risk and reversibility fields to saga and step schemas.
- Add `skiff ops inspect` read path with JSON output.

#### Likely Files
- `internal/saga/types.go`
- `internal/saga/state.go`
- `internal/saga/paths.go`
- `internal/state/schema/saga.go`
- `cmd/skiff/saga.go`
- `tests/saga/state_test.go`

#### Design
A saga is an explicit operational graph:

```text
intent.json  - immutable request
graph.json   - immutable planned graph
control.json - mutable CAS state and lease
events/*     - immutable timeline
```

Unlike an always-running operator, a saga is bounded and inspectable. The user or agent should be able to explain every step before execution.

`SagaNode` should include:

```go
ID string
Kind string
Requires []string
Params map[string]any
Retry *RetryPolicy
Compensate *CompensationSpec
Risk RiskLevel
Reversibility Reversibility
```

Risk examples: low, medium, high, critical. Reversibility examples: reversible, compensatable, partially_reversible, irreversible.

#### Testing / Validation
Unit test creating a saga from intent and graph. Test duplicate create failures. Test CAS control updates under contention. Test schema round-trips with golden examples. Test `skiff ops inspect --format json`.

#### Gotchas
Do not overload service operations and sagas into one schema. Service operations are simple; sagas coordinate graphs. Also avoid storing secrets in saga params; use secret references or opaque result references.

#### Acceptance Criteria
- Saga intent and graph are immutable.
- Saga control is CAS-updated.
- Saga events are append-only.
- `skiff ops inspect` shows risk, reversibility, current step, and state.

---

## Build saga executor, scheduler, and compensation engine

### ID
skiff-m2-002

### Priority
P0

### Type
task

### Labels
saga, executor, resumability

### Dependencies
skiff-m2-001

skiff-m1-010

### Description
Implement the core engine that executes saga graphs, resumes interrupted sagas, handles retries, and runs compensation steps when supported.

#### Subtasks
- Implement dependency resolution for ready nodes in a DAG.
- Implement saga lease acquisition and heartbeat using saga control CAS.
- Implement Step interface with Plan, Run, Resume, Compensate, Doctor, and ValidateParams.
- Implement retry policy handling with bounded backoff.
- Implement compensation in reverse topological order.
- Persist step results and cloud operation IDs before long waits.
- Add `skiff saga start`, `skiff ops watch`, `skiff ops resume`, `skiff saga cancel`, and `skiff saga compensate` skeletons.

#### Likely Files
- `internal/saga/executor.go`
- `internal/saga/graph.go`
- `internal/saga/lease.go`
- `internal/saga/compensation.go`
- `internal/saga/steps/interface.go`
- `cmd/skiff/saga_start.go`
- `cmd/skiff/saga_watch.go`
- `tests/saga/executor_test.go`

#### Design
The saga executor can run inside CLI direct mode, `skiffd`, or an optional worker. Coordination is object-state based, so executor location does not matter.

Execution loop:

```text
acquire saga lease
load graph and control
find ready nodes
run each ready node
write step result
append event
CAS control
if failure and compensation exists, compensate completed steps
if waiting for approval, mark waiting
if all done, mark completed
```

Every step must be idempotent. `Resume` is not optional for long-running cloud actions.

#### Testing / Validation
Use fake step implementations to test DAG execution, parallel-ready nodes, retries, failure, compensation, waiting nodes, and resume after process restart. Test that a lost lease stops execution safely.

#### Gotchas
Do not run destructive compensation automatically for irreversible steps. Compensation is not rollback. Make logs and events clear when a failure is not automatically reversible.

#### Acceptance Criteria
- Saga executor can complete a multi-step graph.
- Saga executor can resume after interruption.
- Compensation runs in correct order for compensatable steps.
- Lost leases prevent stale executors from mutating saga state.

---

## Implement built-in check and approval saga steps

### ID
skiff-m2-003

### Priority
P0

### Type
task

### Labels
saga, checks, approval

### Dependencies
skiff-m2-002

### Description
Add foundational saga steps for preflight checks, service health checks, metrics gates, target health checks, and manual approvals.

#### Subtasks
- Implement `check.preflight` for object-state, provider, identity, and service control sanity.
- Implement `check.service_healthy` using provider service inspection.
- Implement `check.target_health` using provider target group inspection.
- Implement `check.metrics_gate` with pluggable metric client.
- Implement `approval.manual` with waiting state and approve/reject commands.
- Implement `approval.change_window` placeholder with explicit TODO capability.
- Add human and JSON output for approval-required states.

#### Likely Files
- `internal/saga/steps/check/preflight.go`
- `internal/saga/steps/check/service_healthy.go`
- `internal/saga/steps/check/metrics_gate.go`
- `internal/saga/steps/approval/manual.go`
- `cmd/skiff/saga_approve.go`
- `cmd/skiff/saga_reject.go`
- `tests/saga/steps/checks_test.go`

#### Design
Checks and approvals make sagas safe and legible. A canary without metrics gates is just a slower deploy. A restore without approval before cutover is dangerous.

Approval JSON should be agent-friendly:

```json
{
  "state": "waiting_for_approval",
  "step": "approval_before_cutover",
  "risk": "high",
  "facts": ["shadow service healthy", "backup exists"],
  "approve_command": "skiff ops approve saga_... --step approval_before_cutover",
  "reject_command": "skiff ops reject saga_... --step approval_before_cutover"
}
```

#### Testing / Validation
Use fake providers and fake metric clients. Test pass/fail metrics gates. Test approval transitions from waiting to approved/rejected. Test that rejected approval marks saga failed or compensated according to graph policy.

#### Gotchas
Metrics can be noisy. The step should report observed values, thresholds, sample window, and baseline. Do not encode hard production thresholds globally; read them from saga params or service spec.

#### Acceptance Criteria
- Preflight, service health, target health, metrics gate, and manual approval steps exist.
- Approval commands mutate saga control through CAS.
- Check failures include structured evidence.
- JSON output includes next commands for agents.

---

## Compile stateless Service specs into AWS deployable resources

### ID
skiff-m2-004

### Priority
P0

### Type
task

### Labels
compiler, aws, service

### Dependencies
skiff-m1-013

skiff-m1-014

### Description
Extend the compiler and AWS lowering so a Service spec can produce all required AWS resources for a stateless VM-as-pod service.

#### Subtasks
- Lower IR service resources into AWS launch template, ASG, target group, listener rule, security groups, IAM role/profile, log group, and metric config.
- Generate runner user-data from state bucket, service, env, release/control key, and region.
- Generate least-privilege workload IAM policy based on spec identity references.
- Add security group rules for ALB-to-instance ingress and allowed egress.
- Support public HTTP ingress and internal HTTP ingress.
- Add explain output showing each cloud primitive and why it exists.

#### Likely Files
- `internal/compiler/service.go`
- `internal/provider/aws/lower_service.go`
- `internal/provider/aws/iam_service.go`
- `internal/provider/aws/security_groups.go`
- `internal/provider/aws/launch_template.go`
- `internal/explain/explain.go`
- `tests/provider/aws/lower_service_test.go`

#### Design
The Service compiler should make the cloud mapping explicit:

```text
Service -> IAM role/profile
Service -> Launch Template
Service -> Auto Scaling Group
Service -> Target Group
Service ingress -> Listener rule
Service logs -> Log group
```

Do not introduce a scheduler. One ASG represents one service pool; one EC2 instance represents one replica.

Runner user-data should point to object state rather than embedding release contents.

#### Testing / Validation
Golden-test AWS lowered resources for a minimal service and a public HTTP service. Test IAM policy generation for secret references. Test security group rules. Test explain output includes all primitives.

#### Gotchas
AWS name limits can bite target groups and listener rules. Use shared naming helpers. Be careful not to open ingress from the world to instances; traffic should come from the load balancer security group.

#### Acceptance Criteria
- Minimal Service IR lowers to AWS resources.
- Generated launch template includes runner user-data.
- IAM and security groups are least-privilege by default.
- `skiff explain` shows the cloud mapping.

---

## Implement AWS direct plan/apply for core service resources

### ID
skiff-m2-005

### Priority
P0

### Type
task

### Labels
aws, apply, deploy

### Dependencies
skiff-m2-004

skiff-m1-015

### Description
Implement AWS direct apply for the core resources needed to run a stateless service.

#### Subtasks
- Implement plan diff for IAM role/profile, security groups, launch template, ASG, target group, listener rule, and log group.
- Implement idempotent create/update for each resource.
- Record resource objects under `resources/by-logical` and `resources/by-provider`.
- Add dry-run plan rendering with create/update/no-op/delete-not-supported summaries.
- Do not delete resources in this bead; defer GC to a later milestone.
- Add provider throttling retry and timeout handling.

#### Likely Files
- `internal/provider/aws/plan.go`
- `internal/provider/aws/apply.go`
- `internal/provider/aws/iam.go`
- `internal/provider/aws/ec2.go`
- `internal/provider/aws/asg.go`
- `internal/provider/aws/elb.go`
- `internal/provider/aws/logs.go`
- `internal/state/resources.go`
- `tests/provider/aws/apply_test.go`

#### Design
Apply should be conservative and idempotent. It should reconcile known Skiff-managed resources, create missing resources, and update safe mutable fields. Destructive changes require explicit later GC or replacement flows.

Every applied resource should be tagged and recorded in object state:

```text
resources/by-logical/services/payments-api/asg.json
resources/by-provider/aws/asg/skiff-prod-payments-api.json
```

The resource record is not Terraform state; it is a Skiff-owned summary for diagnosis, drift, and explain.

#### Testing / Validation
Use mocked AWS clients for unit tests. Add a local fake provider for plan/apply e2e. Optional AWS integration should deploy a tiny service infrastructure with no traffic and clean up manually or through tagged test cleanup.

#### Gotchas
AWS eventual behavior means create calls may return before resources are fully usable. Add waiters only where needed, and record intermediate events. Avoid implicit deletes in apply; accidental deletion is worse than drift.

#### Acceptance Criteria
- AWS direct apply can create/update core service resources idempotently.
- Plans are human-readable and JSON-readable.
- Resource records are written to object state.
- Apply never deletes resources by default.

---

## Implement release publishing and deploy operation orchestration

### ID
skiff-m2-006

### Priority
P0

### Type
task

### Labels
deploy, release, operation

### Dependencies
skiff-m1-006

skiff-m1-007

skiff-m1-008

skiff-m2-005

### Description
Implement `skiff deploy` through release publishing, service lease acquisition, cloud apply, desired release update, and operation event logging.

#### Subtasks
- Implement deploy operation intent creation.
- Acquire service lease before mutating service control.
- Publish signed release and runtime manifest objects.
- Apply cloud infrastructure plan if needed.
- Update service control desired release through CAS.
- Create operation control and append events for each phase.
- Expose deploy progress through CLI and skiffd.
- Support `--dry-run`, `--plan-only`, `--yes`, and `--format json`.

#### Likely Files
- `internal/deploy/deploy.go`
- `internal/deploy/release_publish.go`
- `internal/deploy/operation.go`
- `cmd/skiff/deploy.go`
- `internal/skiffd/routes_deploy.go`
- `tests/deploy/deploy_test.go`

#### Design
Deploy is the first full Skiff operation. It should be explicit and inspectable.

Flow:

```text
validate spec
compile IR
plan provider changes
create operation intent
acquire service lease
create signed release objects
apply infra plan
CAS service desired_release
append events
start rollout in later bead
```

This bead can stop after desired release update if rollout support is not ready, but command shape should anticipate rollout.

#### Testing / Validation
Unit test deploy flow with memory state and fake provider. Test lease held. Test failed release publish. Test failed provider apply. Test dry-run writes nothing. Test JSON output includes operation ID and next command.

#### Gotchas
Do not update desired release before release objects are written and verified. If apply fails after release publish, leave the release immutable and mark operation failed; do not delete history.

#### Acceptance Criteria
- `skiff deploy --dry-run` produces a plan and writes no state.
- `skiff deploy` publishes signed release objects.
- Service control desired release is updated under lease.
- Operation events document each phase.

---

## Implement ASG instance refresh rollout and watcher

### ID
skiff-m2-007

### Priority
P0

### Type
task

### Labels
aws, rollout, asg

### Dependencies
skiff-m2-006

### Description
Start and watch AWS Auto Scaling Group instance refreshes as Skiff rollout primitives.

#### Subtasks
- Implement provider `StartRollout` for ASG instance refresh.
- Store provider rollout ID in operation control before waiting.
- Implement provider `WatchRollout` polling ASG instance refresh status.
- Map AWS rollout statuses into Skiff statuses.
- Append rollout events for started, checkpoint, healthy, failed, cancelled, and completed states.
- Support configurable min healthy percentage and instance warmup.
- Expose `skiff ops watch`.

#### Likely Files
- `internal/provider/aws/rollout.go`
- `internal/deploy/rollout.go`
- `cmd/skiff/rollout.go`
- `internal/state/operation_control.go`
- `tests/provider/aws/rollout_test.go`

#### Design
ASG instance refresh is the default rollout mechanism for VM-as-pod services. Store the instance refresh ID immediately after starting it so another executor can resume watching.

Provider status mapping should be stable:

```text
Pending -> starting
InProgress -> rolling_out
Successful -> succeeded
Failed -> failed
Cancelling/Cancelled -> cancelled
Rollback* -> rolling_back
```

Rollout watch should combine provider status with target health when possible.

#### Testing / Validation
Use mocked AWS responses for status transitions. Test resume behavior from an operation control with a stored refresh ID. Test timeout handling. Optional AWS test should start a refresh on a tiny ASG only in a gated environment.

#### Gotchas
Instance refresh can take longer than command execution. Do not rely on one CLI process staying alive. Operation state must allow `skiff ops resume` or `skiff ops watch` from another process.

#### Acceptance Criteria
- Skiff can start ASG instance refresh.
- Rollout ID is persisted before watch.
- Rollout watch emits events and JSON statuses.
- Rollout can be resumed from object state.

---

## Implement rollback operation and rollback saga template

### ID
skiff-m2-008

### Priority
P0

### Type
task

### Labels
rollback, saga, deploy

### Dependencies
skiff-m2-002

skiff-m2-007

### Description
Implement rollback to previous stable release as both a direct operation and a saga template that can be invoked by humans, CI, or agents.

#### Subtasks
- Read service control stable release and release history.
- Create rollback operation intent.
- Acquire service lease.
- Update desired release to previous stable through CAS.
- Start ASG instance refresh against previous release.
- Watch health and mark rollback complete.
- Implement `skiff rollback <service> --to previous-stable`.
- Register `deployment.rollback` saga template.

#### Likely Files
- `internal/deploy/rollback.go`
- `internal/saga/templates/rollback.go`
- `cmd/skiff/rollback.go`
- `tests/deploy/rollback_test.go`
- `tests/saga/templates/rollback_test.go`

#### Design
Rollback must be easy, boring, and safe. The default target is `previous-stable`, not an arbitrary release. Arbitrary release rollback can be supported with explicit `--to`.

JSON output should include:

```json
{
  "operation": "rollback",
  "from_release": "2026.05.16.1",
  "to_release": "2026.05.15.3",
  "risk": "low",
  "reversibility": "reversible"
}
```

#### Testing / Validation
Test rollback with no stable release, missing target release object, lease held, failed rollout, and successful rollback. Test agent JSON recommended command shape.

#### Gotchas
Do not mark a release stable until rollout success. Otherwise rollback may point to a bad release. If stable release is missing, recommend `skiff release list` rather than guessing.

#### Acceptance Criteria
- `skiff rollback` rolls service back to previous stable release.
- Rollback is represented as operation/saga state.
- Rollback events are auditable.
- Failure cases produce actionable diagnostics.

---

## Implement canary deployment saga template

### ID
skiff-m2-009

### Priority
P0

### Type
task

### Labels
canary, saga, rollout

### Dependencies
skiff-m2-003

skiff-m2-007

skiff-m2-008

### Description
Implement a canary saga that advances rollout in stages with bake periods, health checks, metrics gates, and automatic compensation through rollback.

#### Subtasks
- Define canary saga template parameters: service, release, stages, bake duration, metric gates, rollback policy.
- Implement step kinds for starting partial rollout or equivalent AWS-compatible staged rollout.
- Implement bake/wait step.
- Run target-health and metrics gates after each stage.
- On failure, pause or cancel rollout and invoke rollback compensation.
- Expose `skiff deploy --canary` and `skiff saga start canary-deploy`.
- Show canary progress in human and JSON output.

#### Likely Files
- `internal/saga/templates/canary_deploy.go`
- `internal/saga/steps/service/canary.go`
- `internal/saga/steps/time/sleep.go`
- `cmd/skiff/deploy.go`
- `tests/saga/templates/canary_test.go`

#### Design
Canarying is an operational journey, not hidden magic. The saga graph should be explainable:

```text
preflight
publish release
start 5%
bake
metrics gate
start 25%
bake
metrics gate
start 100%
verify
mark stable
```

AWS may require implementation choices such as temporary ASG desired capacity manipulation, launch template version weighting alternatives, or controlled instance refresh checkpoints. The public saga model should remain stable even if AWS mechanics evolve.

#### Testing / Validation
Use fake provider rollout stages to test success and failure. Test metrics gate failure triggers rollback compensation. Test `skiff saga explain` shows all stages before execution.

#### Gotchas
ASG instance refresh does not natively support arbitrary traffic-weighted canaries like a service mesh. Be honest in explain output about the mechanism used. Avoid overpromising per-request canary precision.

#### Acceptance Criteria
- `skiff deploy --canary` creates and runs a canary saga.
- Failure during a canary stage triggers rollback compensation.
- Canary plan is explainable before execution.
- JSON output includes stage, gate, and next action.

---

## Implement hot logs integration and CLI tailing

### ID
skiff-m2-010

### Priority
P0

### Type
task

### Labels
logs, cloudwatch, cli, observability

### Dependencies
skiff-m2-005

skiff-m1-011

### Description
Implement near-real-time service logs through cloud logging, plus CLI/TUI/agent access with consistent service/release/instance filters.

#### Subtasks
- Generate log group naming and tags during service compile/apply.
- Configure runner or base image expectation for local log collector forwarding app stdout/stderr.
- Implement AWS CloudWatch Logs query/tail provider methods.
- Implement `skiff logs <service> --follow --since --release --instance --format json`.
- Add log event enrichment schema for service, env, release, instance, region, and zone.
- Add errors for missing log group, no streams, and permission issues.
- Add archive path placeholders for future cold log export.

#### Likely Files
- `internal/provider/provider.go`
- `internal/provider/aws/logs.go`
- `cmd/skiff/logs.go`
- `internal/observability/logs.go`
- `internal/runner/logs.go`
- `tests/observability/logs_test.go`

#### Design
Object storage is not the hot log backend. Skiff uses the cloud log service for near-real-time logs and object storage for archives when configured.

Log filters should map from Skiff identity to provider queries:

```text
service=payments-api
env=prod
release=2026.05.16.1
instance_id=i-abc123
```

Agents need JSON log lines with timestamp, message, labels, and provider source.

#### Testing / Validation
Mock provider log streams for CLI tests. Test `--follow` cancellation. Test filtering by release and instance. Test missing log group returns actionable errors. Do not require real CloudWatch in unit tests.

#### Gotchas
Cloud log APIs paginate and can return events out of order across streams. Merge carefully by timestamp and stream. In follow mode, avoid tight polling loops and high API cost.

#### Acceptance Criteria
- `skiff logs` can tail service logs through provider abstraction.
- Logs can be filtered by release and instance.
- JSON output is stable for agents.
- Missing logs produce actionable diagnostics.

---

## Implement metrics collection model and service metrics queries

### ID
skiff-m2-011

### Priority
P0

### Type
task

### Labels
metrics, observability, cloudwatch

### Dependencies
skiff-m2-004

skiff-m1-017

### Description
Implement Skiff’s metric model for cloud metrics, node metrics, and app metrics, plus initial service metric queries for status, doctor, and canary gates.

#### Subtasks
- Define metric identity envelope: service, env, release, instance, region, zone.
- Generate metric config in runtime manifest.
- Add provider metric query interface.
- Implement AWS CloudWatch queries for ASG capacity, target health counts, ALB request count, ALB latency, ALB 5xx, and instance CPU.
- Add app metric endpoint config to runner/collector config generation.
- Implement `skiff metrics <service>` basic command.
- Make metrics gate steps consume the same metric client.

#### Likely Files
- `internal/observability/metrics.go`
- `internal/provider/aws/metrics.go`
- `internal/runner/collector_config.go`
- `cmd/skiff/metrics.go`
- `tests/observability/metrics_test.go`

#### Design
Metrics should feel service-first. The VM is the pod, so node metrics naturally attach to a service instance.

Metric categories:

```text
cloud metrics: ASG, ALB, target groups
node metrics: CPU, memory, disk, network
app metrics: Prometheus/OTLP exposed locally
```

The runner should generate collector config when app metrics are enabled, but cloud metrics are queried from provider APIs.

#### Testing / Validation
Unit test query construction. Mock metric results for canary gates and doctor. Test missing data behavior. Test collector config generation for an app metrics endpoint.

#### Gotchas
Metric label cardinality can explode. Do not make request IDs, user IDs, emails, or trace IDs metric labels. Those belong in logs/traces.

#### Acceptance Criteria
- Skiff can query core service metrics.
- Metrics gate steps use the metric client.
- Runtime manifest can configure app metrics endpoint.
- Metric identity envelope is consistent with logs and events.

---

## Implement service status and health summary

### ID
skiff-m2-012

### Priority
P0

### Type
task

### Labels
status, health, cli, skiffd

### Dependencies
skiff-m1-010

skiff-m2-007

skiff-m2-010

skiff-m2-011

### Description
Implement a service status view that combines object-state, in-memory indexes, provider health, rollout state, target health, logs availability, and metrics freshness.

#### Subtasks
- Define ServiceStatus model with desired/stable release, operation, rollout, capacity, target health, recent events, logs status, and metric freshness.
- Implement direct and skiffd-backed status clients.
- Add `skiff status <service> --watch --fresh --format json`.
- Add service list summaries with health colors for human output.
- Expose status endpoint in `skiffd`.
- Ensure status can degrade gracefully when one provider API is unavailable.

#### Likely Files
- `internal/status/service_status.go`
- `internal/client/status.go`
- `internal/skiffd/routes_status.go`
- `cmd/skiff/status.go`
- `tests/status/service_status_test.go`

#### Design
Status is the daily entrypoint. It should answer:

```text
What release should be running?
What release is stable?
Is there an active operation or saga?
How many replicas are desired/running/healthy?
Are logs/metrics flowing?
What just happened?
```

Use in-memory index for fast default status and direct object/provider reads for `--fresh`.

#### Testing / Validation
Test status composition with fake index, fake provider, fake logs, and fake metrics. Test degraded status when metrics are unavailable. Test watch mode emits updates without ANSI in JSON mode.

#### Gotchas
Avoid making status overly verbose by default. Human status should summarize and point to `doctor`, `logs`, and `events` for details. JSON can include richer nested fields.

#### Acceptance Criteria
- `skiff status` summarizes service health.
- `--fresh` reloads critical state directly.
- Status works in direct and API modes.
- Degraded dependencies are reported, not hidden.

---

## Implement doctor engine for service diagnostics

### ID
skiff-m2-013

### Priority
P0

### Type
task

### Labels
doctor, diagnostics, agent

### Dependencies
skiff-m2-012

skiff-m2-010

skiff-m2-011

### Description
Implement `skiff doctor` to diagnose unhealthy services and produce facts, hypotheses, confidence, and recommended actions.

#### Subtasks
- Define Finding, Evidence, Hypothesis, and RecommendedAction models.
- Implement checks for desired-vs-cloud drift, capacity mismatch, target health, runner state, rollout failure, recent bad logs, metrics gates, IAM/secret access symptoms, and log/metric delivery.
- Rank findings by severity and confidence.
- Generate agent-safe JSON with read-only and mutating actions marked separately.
- Add `skiff doctor <service>` CLI and `skiffd` endpoint.
- Add doctor plugin hook placeholder for later extension.

#### Likely Files
- `internal/doctor/types.go`
- `internal/doctor/engine.go`
- `internal/doctor/checks/*.go`
- `cmd/skiff/doctor.go`
- `internal/skiffd/routes_doctor.go`
- `tests/doctor/*.go`

#### Design
Doctor should be opinionated but honest. Separate facts from hypotheses.

Example JSON shape:

```json
{
  "facts": [
    {"type":"target_health","message":"1 target unhealthy"},
    {"type":"runner_state","message":"i-abc123 is WaitingForHealth"}
  ],
  "hypotheses": [
    {"confidence":0.86,"message":"new release starts but health check returns 500"}
  ],
  "recommended_actions": [
    {"kind":"command","mutating":false,"command":"skiff logs payments-api --instance i-abc123 --since 20m"},
    {"kind":"command","mutating":true,"safety":"reversible","command":"skiff rollback payments-api --to previous-stable --yes"}
  ]
}
```

This is one of the main agent amenities.

#### Testing / Validation
Unit test each check with fake inputs. Golden-test doctor output for common scenarios: bad release, no capacity, target group misconfigured, IAM denied, logs unavailable. Test recommendations are stable and safe-classified.

#### Gotchas
Do not overstate certainty. If evidence is weak, say so. Mutating recommendations must be explicit and should prefer reversible actions first.

#### Acceptance Criteria
- `skiff doctor` produces facts, hypotheses, and commands.
- Doctor JSON is deterministic enough for agents.
- Common service failure scenarios are covered by tests.
- Mutating actions are marked with safety and reversibility.

---

## Implement secure debug sessions and diagnostic bundles

### ID
skiff-m2-014

### Priority
P1

### Type
task

### Labels
debug, ssm, security

### Dependencies
skiff-m2-012

skiff-m1-020

### Description
Implement controlled debug access without SSH-first operations, plus diagnostic bundle collection for incidents.

#### Subtasks
- Add provider debug interface for shell, command, port-forward, and bundle collection.
- Implement AWS SSM Session Manager integration for debug shell and port forward.
- Implement `skiff debug collect` to gather runner status, systemd status, recent logs, disk usage, OOM events, release digest, target health, and collector status.
- Add authorization hooks and audit events for every debug session.
- Default to read-only bundle collection before interactive shell.
- Add `--instance` and service-scoped instance selection.

#### Likely Files
- `internal/provider/aws/debug.go`
- `internal/debug/bundle.go`
- `cmd/skiff/debug.go`
- `internal/security/authz.go`
- `tests/debug/*.go`

#### Design
Skiff should not require inbound SSH. Debug should be audited and scoped. Recommended command flow:

```bash
skiff debug collect payments-api --instance i-abc123
skiff debug port-forward payments-api --remote 8080 --local 18080
skiff debug shell payments-api --instance i-abc123
```

The bundle should be safe to share internally and redact obvious secrets.

#### Testing / Validation
Unit test bundle assembly with fake provider and fake runner status. Test audit events. Test permission denied path. AWS SSM integration can be gated behind an environment variable.

#### Gotchas
Interactive shell is powerful and risky. Make it obvious in audit logs. Do not include secrets or full environment variables in bundles unless explicitly requested with a privileged flag.

#### Acceptance Criteria
- `skiff debug collect` creates a useful diagnostic bundle.
- Debug commands are audited.
- SSM-backed debug paths are provider-abstracted.
- No SSH ingress is required.

---

## Implement TUI service and saga dashboard

### ID
skiff-m2-015

### Priority
P1

### Type
task

### Labels
tui, ux, operations

### Dependencies
skiff-m2-012

skiff-m2-013

skiff-m2-002

### Description
Build the initial terminal UI for services, rollouts, sagas, events, doctor findings, logs, and approvals.

#### Subtasks
- Create `skiff tui` command.
- Render service list, selected service detail, recent events, and active sagas.
- Add keybindings for status, doctor, logs, metrics, rollback, saga explain, and approval.
- Use `skiffd` API by default, direct mode if requested.
- Support read-only mode for users without mutation privileges.
- Ensure TUI can display agent recommendations and safety classifications.

#### Likely Files
- `internal/tui/app.go`
- `internal/tui/views/services.go`
- `internal/tui/views/sagas.go`
- `internal/tui/views/events.go`
- `cmd/skiff/tui.go`
- `tests/tui/*.go`

#### Design
The TUI should make operations feel tangible. Example layout:

```text
Services       Selected service
payments-api   release, capacity, health, rollout
orders-api     p95, errors, recent events

Active sagas
restore-db     waiting for approval
canary-api     stage 25%, gate pending
```

The TUI is a frontend over the same client interface as CLI commands. Do not duplicate business logic.

#### Testing / Validation
Snapshot-test render models where practical. Unit test keybinding dispatch. Manual test with a fake skiffd serving fixture data. Verify no mutation occurs in read-only mode.

#### Gotchas
Terminal UIs can become brittle. Keep model/view separation clean. Do not make TUI-only operations; every action must have a CLI/API equivalent.

#### Acceptance Criteria
- `skiff tui` shows services, active sagas, and events.
- TUI actions map to existing CLI/API operations.
- Read-only users cannot trigger mutations.
- Approval prompts show risk and reversibility.

---

## Implement agent action graph and solve command

### ID
skiff-m2-016

### Priority
P1

### Type
task

### Labels
agent, doctor, automation

### Dependencies
skiff-m2-013

skiff-m2-008

skiff-m2-009

### Description
Expose deterministic, machine-readable action graphs so external agents can safely diagnose and remediate common Skiff infrastructure problems.

#### Subtasks
- Define ActionGraph model with steps, dependencies, commands, safety, reversibility, risk, and expected validation.
- Implement `skiff solve <service> --goal restore-health --format json`.
- Convert doctor findings into recommended action graphs.
- Support no-op/read-only plans, reversible mutating plans, and high-risk approval-required plans.
- Add command strings and API operation descriptors for each action.
- Add tests for common incident scenarios.

#### Likely Files
- `internal/agent/action_graph.go`
- `internal/agent/solve.go`
- `cmd/skiff/solve.go`
- `tests/agent/solve_test.go`

#### Design
Agents should not scrape prose. They need structured facts and safe next actions.

Example:

```json
{
  "goal": "restore-health",
  "status": "plan_ready",
  "confidence": 0.91,
  "steps": [
    {
      "id": "rollback",
      "command": "skiff rollback payments-api --to previous-stable --yes --format json",
      "mutating": true,
      "reversible": true,
      "requires": []
    },
    {
      "id": "verify",
      "command": "skiff status payments-api --watch --format json",
      "mutating": false,
      "requires": ["rollback"]
    }
  ]
}
```

Skiff should provide the graph; the external agent decides whether to execute within its policy.

#### Testing / Validation
Golden-test action graphs for failed canary, missing capacity, bad target health, and logs unavailable. Ensure mutating commands include `--yes` only when safety classification allows automation.

#### Gotchas
Do not over-automate irreversible operations. High-risk actions should be represented as approval-required, not as direct commands with `--yes`.

#### Acceptance Criteria
- `skiff solve` returns deterministic JSON action graphs.
- Action graph steps include dependencies and safety metadata.
- Common doctor findings map to useful remediation plans.
- Irreversible actions require approval.

---

## Implement operation and saga event streaming

### ID
skiff-m2-017

### Priority
P1

### Type
task

### Labels
events, streaming, skiffd, cli

### Dependencies
skiff-m1-010

skiff-m2-002

skiff-m2-007

### Description
Implement live event streaming from `skiffd` for operations and sagas, with object-log resume for disconnected clients.

#### Subtasks
- Add in-process event bus inside skiffd.
- Publish events after successful object writes.
- Expose SSE, WebSocket, or gRPC stream endpoint for service, operation, and saga scopes.
- Implement CLI watch commands using stream when available and object polling in direct mode.
- Add resume token based on last event ID.
- Handle slow subscribers by dropping buffered events and requiring resume.

#### Likely Files
- `internal/events/bus.go`
- `internal/skiffd/routes_stream.go`
- `internal/client/watch.go`
- `cmd/skiff/ops_watch.go`
- `cmd/skiff/saga_watch.go`
- `tests/events/stream_test.go`

#### Design
Streaming is UX, not correctness. The append-only event objects remain the source of truth. If the stream drops, clients resume from event keys.

Watch modes:

```bash
skiff ops watch op_01J...
skiff ops watch saga_01J...
skiff status payments-api --watch
```

In direct mode, polling object prefixes is acceptable.

#### Testing / Validation
Test streaming with fake events. Test disconnect and resume. Test slow subscriber behavior. Test direct-mode polling fallback.

#### Gotchas
Do not rely on object storage notifications as the only event source. Local writes should publish hints/events immediately, and periodic scans should repair missed events.

#### Acceptance Criteria
- `skiffd` streams operation and saga events.
- CLI watch commands work in API and direct modes.
- Disconnected clients can resume from last event ID.
- Streaming does not replace append-only event logs.

---

## Implement operation resume and optional worker loop

### ID
skiff-m2-018

### Priority
P1

### Type
task

### Labels
resumability, worker, operations

### Dependencies
skiff-m2-002

skiff-m2-007

skiff-m2-017

### Description
Implement a resumable operation model and an optional `skiff-worker` that can pick up incomplete operations and sagas from object state.

#### Subtasks
- Define operation control schema for deploy/rollback operations.
- Implement `skiff ops list`, `skiff ops inspect`, and `skiff ops resume`.
- Implement worker loop that scans active operations/sagas and attempts lease acquisition.
- Resume ASG rollout watchers from stored provider rollout IDs.
- Resume sagas through saga executor.
- Emit events when worker takes over an expired operation.
- Make worker optional; CLI and skiffd can still run operations synchronously.

#### Likely Files
- `internal/ops/control.go`
- `internal/ops/resume.go`
- `cmd/skiff/ops.go`
- `cmd/skiff-worker/main.go`
- `internal/worker/loop.go`
- `tests/ops/resume_test.go`

#### Design
Long operations outlive processes. Resume is non-negotiable.

Worker loop:

```text
scan active operation/saga controls
skip active leases
try acquire expired/no lease
inspect provider state
resume from last stored step
append takeover event
continue
```

This is not a queue database. It is object-state-driven recovery.

#### Testing / Validation
Test resume from mid-rollout with fake provider. Test worker ignores active leases. Test expired lease takeover. Test multiple workers contending and only one acquiring.

#### Gotchas
Avoid scanning the entire bucket too often. Use indexes/hot prefixes and backoff. The worker must be safe to run multiple replicas; leases provide coordination.

#### Acceptance Criteria
- `skiff ops resume` can continue interrupted operations.
- `skiff-worker` can resume expired operations and sagas.
- Multiple workers do not double-execute a leased operation.
- Takeover events are auditable.

---

## End-to-end AWS stateless service release gate

### ID
skiff-m2-019

### Priority
P0

### Type
task

### Labels
e2e, aws, release-gate

### Dependencies
skiff-m2-005

skiff-m2-006

skiff-m2-007

skiff-m2-010

skiff-m2-012

skiff-m2-013

### Description
Create the Milestone 2 release-gate test proving Skiff can deploy, observe, diagnose, and roll back a tiny stateless service on AWS or a high-fidelity fake provider.

#### Subtasks
- Create tiny HTTP service artifact with health, logs, and metrics.
- Run full chain: validate, compile, plan, deploy, rollout watch, status, logs, metrics, doctor, rollback.
- Use fake provider in CI and optional real AWS nightly/manual path.
- Record golden event sequence for successful deploy and rollback.
- Assert service control stable release updates only after successful rollout.
- Assert doctor returns no critical findings after success.
- Assert rollback returns service to previous stable.

#### Likely Files
- `tests/e2e/test_stateless_service_flow.go`
- `tests/fixtures/services/http-hello/`
- `tests/golden/events/stateless_deploy.json`
- `examples/service/http-hello/skiff.yaml`

#### Design
This is the credibility test for the stateless service product. It should exercise the real packages and fake only cloud side effects in CI. The optional AWS version proves provider integration.

Required commands in test:

```bash
skiff validate
skiff plan
skiff deploy
skiff status
skiff logs
skiff metrics
skiff doctor
skiff rollback
```

#### Testing / Validation
CI fake-provider e2e must run on every PR. Optional AWS e2e should be isolated by unique env/service names and tags. Verify event logs and control docs, not just CLI output.

#### Gotchas
Fake providers can mask AWS behavior. Keep fake provider realistic around asynchronous rollouts and target health. Do not require real AWS for normal PR velocity.

#### Acceptance Criteria
- Full stateless service flow passes in CI with fake provider.
- Optional AWS flow is documented and gated.
- Deploy, status, logs, doctor, and rollback are all exercised.
- Stable release updates only after rollout success.

---

## Operational UX polish and failure taxonomy

### ID
skiff-m2-020

### Priority
P0

### Type
task

### Labels
ux, errors, agent, release-gate

### Dependencies
skiff-m2-012

skiff-m2-013

skiff-m2-016

skiff-m2-019

### Description
Standardize user-facing failures, JSON envelopes, progress output, and recommended next actions across deploy, rollback, saga, status, doctor, logs, and metrics.

#### Subtasks
- Define canonical failure codes: VALIDATION_FAILED, POLICY_DENIED, ARTIFACT_UNTRUSTED, LEASE_HELD, CLOUD_APPLY_FAILED, ROLLOUT_FAILED, CANARY_FAILED, ROLLBACK_FAILED, OBSERVABILITY_UNAVAILABLE, INTERNAL_ERROR.
- Ensure every command returns consistent JSON error envelopes.
- Add recommended next command for common failures.
- Add progress renderer for long-running operations with no ANSI in JSON/no-color mode.
- Add trace ID to every error and event.
- Golden-test representative command outputs.

#### Likely Files
- `internal/errors/codes.go`
- `internal/cli/progress.go`
- `internal/cli/output.go`
- `internal/agent/recommendations.go`
- `tests/golden/cli/*.json`
- `tests/cli/error_output_test.go`

#### Design
The operating experience must be predictable. A junior engineer or agent should know what to do from the output.

Example error envelope:

```json
{
  "ok": false,
  "code": "CANARY_FAILED",
  "summary": "Canary failed metrics gate",
  "trace_id": "tr_01J...",
  "recommended_actions": [
    {
      "command": "skiff rollback payments-api --to previous-stable --yes --format json",
      "mutating": true,
      "safety": "reversible"
    }
  ]
}
```

#### Testing / Validation
Golden-test JSON and human output for each failure code. Test that `--format json` output is valid JSON with no progress spinners. Test trace IDs propagate to events and logs.

#### Gotchas
Do not create vague `UNKNOWN` errors unless genuinely unavoidable. Preserve provider-specific context while wrapping it in Skiff’s structured envelope.

#### Acceptance Criteria
- Canonical failure taxonomy is implemented.
- All major commands use structured error envelopes.
- Common failures include useful next commands.
- Golden output tests prevent UX regressions.
