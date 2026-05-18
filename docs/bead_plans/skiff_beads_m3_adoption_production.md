---

## Implement release candidate and promotion workflow for CI/CD

### ID
skiff-m3-001

### Priority
P0

### Type
task

### Labels
cicd, release, promotion

### Dependencies
skiff-m2-006

skiff-m2-020

### Description
Implement Skiff release candidates, environment promotion, and CI/CD-friendly commands so pipelines can build artifacts and ask Skiff to publish or promote signed releases.

#### Subtasks
- Define ReleaseCandidate schema with git metadata, artifact digest, CI provider/run URL, checks, SBOM/provenance references, and created actor.
- Add object paths for candidates under service prefixes.
- Implement `skiff release candidate create`.
- Implement `skiff release promote <service> --from <env> --to <env>`.
- Validate promotion requirements such as staging stable duration, contract tests passed, scan evidence present, and approval context.
- Add CI-friendly JSON and Markdown plan output.
- Emit events and audit records for candidate creation and promotion.

#### Likely Files
- `internal/release/candidate.go`
- `internal/release/promotion.go`
- `cmd/skiff/release_candidate.go`
- `cmd/skiff/promote.go`
- `internal/cicd/evidence.go`
- `tests/release/promotion_test.go`

#### Design
CI/CD should build and prove artifacts. Skiff should publish and roll them out.

Candidate object example:

```json
{
  "schema": "skiff.release-candidate/v1",
  "service": "payments-api",
  "env": "staging",
  "artifact": "registry.example.com/payments-api@sha256:abc123",
  "git": {"repo":"github.com/acme/payments","sha":"abc123"},
  "ci": {"provider":"github-actions","run_id":"123"},
  "checks": {"tests":"passed","contract":"passed","policy":"passed"}
}
```

Promotion should preserve the same artifact digest across environments.

#### Testing / Validation
Unit test candidate creation, missing evidence, promotion from unknown release, promotion with insufficient stable time, and successful promotion. Golden-test Markdown PR comment output and JSON output.

#### Gotchas
Do not rebuild artifacts for production promotion. Build once and promote immutable digests. Do not let CI mutate production cloud state without Skiff operation records.

#### Acceptance Criteria
- CI can create release candidates.
- Promotion reuses immutable artifact digests.
- Promotion requirements are validated and actionable.
- Markdown and JSON outputs are suitable for CI logs and PR comments.

---

## Generate GitHub Actions, GitLab CI, and Buildkite templates

### ID
skiff-m3-002

### Priority
P1

### Type
task

### Labels
cicd, templates, adoption

### Dependencies
skiff-m3-001

skiff-m2-019

### Description
Provide generated CI/CD templates that demonstrate test, build, contract, plan, candidate, staging deploy, and production promotion flows.

#### Subtasks
- Create `skiff ci generate github-actions`.
- Create `skiff ci generate gitlab`.
- Create `skiff ci generate buildkite`.
- Include examples for direct mode and API mode.
- Include OIDC/IAM role assumptions as documented placeholders.
- Add pipeline steps for `skiff validate`, `skiff contract test`, `skiff plan`, `skiff release candidate create`, `skiff deploy`, and `skiff release promote`.
- Add docs explaining what each pipeline step proves.

#### Likely Files
- `cmd/skiff/ci_generate.go`
- `internal/cicd/templates/github_actions.yaml.tmpl`
- `internal/cicd/templates/gitlab.yml.tmpl`
- `internal/cicd/templates/buildkite.yml.tmpl`
- `docs/adoption/cicd.md`
- `tests/cicd/templates_test.go`

#### Design
Templates reduce adoption friction. They should be safe defaults, not minimal snippets.

Generated GitHub Actions shape:

```text
test-build-plan
  -> deploy-staging on main
  -> promote-prod through protected environment
```

Every template should prefer immutable artifact digests, not mutable tags. JSON outputs should be archived as CI artifacts.

#### Testing / Validation
Golden-test generated templates. Run YAML syntax validation. Add docs tests that ensure commands mentioned in templates exist. Optionally run template smoke tests in a fixture repo.

#### Gotchas
Keep provider-specific auth placeholders explicit. Do not embed account IDs or assumed role names in templates without variables.

#### Acceptance Criteria
- Users can generate CI templates for major systems.
- Generated templates use immutable artifact digests.
- Templates include Skiff validation, contract, candidate, deploy, and promote steps.
- Docs explain safe CI/CD boundaries.

---

## Implement Terraform generate and adopt workflows

### ID
skiff-m3-003

### Priority
P0

### Type
task

### Labels
terraform, adoption, infra

### Dependencies
skiff-m2-004

skiff-m2-005

skiff-m2-006

### Description
Allow Terraform-heavy teams to adopt Skiff by generating Terraform for stable infrastructure shape and adopting Terraform outputs while Skiff owns releases and operations.

#### Subtasks
- Implement Terraform renderer for AWS service resources from IR.
- Generate `main.tf`, `variables.tf`, `outputs.tf`, and README.
- Add `skiff terraform generate <spec> --out <dir>`.
- Add `skiff adopt terraform <dir|mapping>` to record resource ownership in object state.
- Support ownership modes: direct, terraform-infra-skiff-release, external.
- Ensure Skiff deploy can run against Terraform-owned infra without reapplying Terraform.
- Document boundary between Terraform state and Skiff state.

#### Likely Files
- `internal/terraform/render.go`
- `internal/terraform/aws_service.go`
- `cmd/skiff/terraform.go`
- `internal/adopt/terraform.go`
- `docs/adoption/terraform.md`
- `tests/terraform/*.go`

#### Design
Terraform is an adoption bridge, not Skiff’s database. The preferred hybrid model:

```text
Terraform owns stable infra shape.
Skiff owns release pointer, rollout, rollback, sagas, diagnosis, and events.
```

Generated Terraform should include outputs Skiff can adopt:

```hcl
output "skiff_resources" {
  value = {
    asg = aws_autoscaling_group.service.name
    target_group_arn = aws_lb_target_group.service.arn
    launch_template_id = aws_launch_template.service.id
  }
}
```

Adoption writes resource records and ownership metadata into object state.

#### Testing / Validation
Golden-test Terraform output for minimal and ingress services. Validate generated HCL with a parser or `terraform validate` when available. Test adopt with fixture output JSON. Test deploy against adopted resources using fake provider.

#### Gotchas
Do not parse arbitrary Terraform state deeply in v1. Prefer explicit outputs or mapping files. Avoid letting Skiff modify Terraform-owned shape unless the user runs a take-ownership command.

#### Acceptance Criteria
- `skiff terraform generate` creates valid Terraform module files.
- `skiff adopt terraform` records resource ownership.
- Skiff deploy can use Terraform-owned infrastructure for release/rollout.
- Docs clearly explain Terraform vs Skiff ownership.

---

## Implement Kubernetes importer and shadow deployment workflow

### ID
skiff-m3-004

### Priority
P0

### Type
task

### Labels
kubernetes, migration, adoption

### Dependencies
skiff-m2-004

skiff-m2-006

skiff-m2-009

### Description
Create an importer that converts simple Kubernetes workloads into Skiff specs and supports shadow deployment plus weighted cutover.

#### Subtasks
- Parse Kubernetes Deployment, Service, Ingress, HPA, ConfigMap, Secret references, and PodDisruptionBudget where possible.
- Generate Skiff Service spec with artifact, runtime port, health, scale, ingress, env/config, and warnings.
- Reject or warn on unsupported features: privileged containers, hostPath, arbitrary sidecars, complex init containers, CRDs, StatefulSets without recipe, and service mesh annotations.
- Add `skiff import kube` command.
- Add `skiff deploy --shadow` behavior for Skiff service without production traffic.
- Add traffic cutover saga template for moving from existing endpoint to Skiff endpoint in stages.
- Generate migration report in Markdown and JSON.

#### Likely Files
- `internal/importer/kube/parser.go`
- `internal/importer/kube/convert.go`
- `cmd/skiff/import_kube.go`
- `internal/saga/templates/traffic_cutover.go`
- `docs/adoption/kubernetes.md`
- `tests/importer/kube/*.go`

#### Design
The importer is not a Kubernetes emulator. It is a migration assistant. It should be honest:

```text
Imported:
  Deployment -> Service
  Service port -> runtime.port
  Ingress host -> network.ingress.host

Needs attention:
  Secret reference must be mapped to cloud secret
  Sidecar ignored; consider plugin/addon
Unsupported:
  hostPath volume
```

The shadow deployment path lets teams compare Skiff before moving traffic.

#### Testing / Validation
Use fixture Kubernetes YAMLs. Test clean import, import with warnings, and unsupported import with errors. Test migration report output. Test traffic cutover saga with fake provider.

#### Gotchas
Kubernetes manifests often rely on annotations and controllers. Do not silently drop meaningful behavior. Warnings must be explicit and path-specific.

#### Acceptance Criteria
- `skiff import kube` produces Skiff specs for simple apps.
- Unsupported Kubernetes features are reported clearly.
- Shadow deployment path exists.
- Weighted cutover saga is available for migration.

---

## Implement plugin API for typed capabilities and saga steps

### ID
skiff-m3-005

### Priority
P0

### Type
task

### Labels
plugins, capabilities, saga

### Dependencies
skiff-m2-001

skiff-m2-004

skiff-m2-013

### Description
Create a plugin system that lets trusted extensions add typed IR patches, runtime addons, doctor checks, and saga step kinds without becoming hidden always-running controllers.

#### Subtasks
- Define plugin manifest schema with name, version, hooks, permissions, and capabilities.
- Define gRPC plugin protocol for Validate, MutateIR, RuntimeAddons, DoctorChecks, and SagaStep execution.
- Implement plugin registry loading from configured paths or signed package references.
- Implement permission declaration and host-side enforcement for allowed patch kinds.
- Implement local plugin dev runner.
- Add `skiff plugin list`, `skiff plugin validate`, and `skiff plugin explain`.
- Require plugins to produce typed patches rather than arbitrary cloud mutations.

#### Likely Files
- `pkg/pluginapi/types.go`
- `api/proto/plugin.proto`
- `internal/plugins/host.go`
- `internal/plugins/registry.go`
- `internal/plugins/permissions.go`
- `cmd/skiff/plugin.go`
- `docs/plugins/authoring.md`
- `tests/plugins/*.go`

#### Design
Plugins should extend Skiff while preserving explainability. They can add things like mTLS, WAF, egress rules, database recipe steps, or diagnostics. They must not run uncontrolled reconciliation loops.

Plugin patch example:

```json
{
  "op": "add",
  "path": "/resources/-",
  "value": {
    "kind": "SecurityGroupRule",
    "name": "payments-to-orders-mtls",
    "from": "payments-api",
    "to": "orders-api",
    "port": 8443
  }
}
```

`skiff explain` should show which plugin added what.

#### Testing / Validation
Test plugin loading, version mismatch, invalid manifest, denied patch, allowed patch, doctor check registration, and saga step registration. Use a tiny fixture plugin.

#### Gotchas
Plugins are a major security boundary. Do not let plugins directly receive broad cloud clients by default. Host should mediate typed requests and patch application.

#### Acceptance Criteria
- Plugins can register typed capabilities.
- Plugin patches are validated and explainable.
- Denied plugin actions fail with clear diagnostics.
- Plugin authoring docs and dev runner exist.

---

## Build mTLS capability plugin

### ID
skiff-m3-006

### Priority
P1

### Type
task

### Labels
plugins, mtls, security

### Dependencies
skiff-m3-005

skiff-m2-004

skiff-m2-014

### Description
Implement an optional mTLS plugin that can add service-to-service mTLS or ingress client-certificate validation without making service mesh the default.

#### Subtasks
- Define mTLS plugin config schema for ingress client verification and service-to-service mode.
- Implement IR mutation for load balancer trust store/listener configuration where supported.
- Implement runtime addon for local proxy/certificate agent mode.
- Add IAM/security group patches required by the chosen mode.
- Add doctor checks for certificate freshness, proxy health, and policy mismatch.
- Add example specs for permissive and strict mTLS.
- Add explain output for all changes introduced by the plugin.

#### Likely Files
- `plugins/mtls/plugin.go`
- `plugins/mtls/manifest.yaml`
- `plugins/mtls/runtime.go`
- `plugins/mtls/doctor.go`
- `examples/plugins/mtls/*.yaml`
- `tests/plugins/mtls/*.go`

#### Design
mTLS is optional. A user should add it explicitly:

```yaml
addons:
  - name: mtls
    mode: strict
    config:
      outbound:
        - service: orders-api
          port: 8443
```

The plugin should be transparent:

```text
will add local proxy unit
will issue workload cert
will add egress payments-api -> orders-api:8443
will add doctor checks for cert expiry
```

Do not silently install a global service mesh.

#### Testing / Validation
Unit test IR patches and runtime addon rendering. Test doctor findings for expired cert and failed proxy. Add fixture explain output. Integration can be fake-provider based initially.

#### Gotchas
Certificate management can become its own platform. Keep v1 scoped to explicit service pairs and clear rotation behavior. Do not intercept all traffic by default.

#### Acceptance Criteria
- mTLS plugin can be enabled through addons.
- All plugin-introduced resources are visible in explain output.
- Doctor can detect core mTLS failures.
- mTLS is not enabled by default.

---

## Implement managed database recipe and API-plus-database stack template

### ID
skiff-m3-007

### Priority
P0

### Type
task

### Labels
recipe, database, stack, adoption

### Dependencies
skiff-m2-005

skiff-m3-001

### Description
Create the first high-value customer recipe: an API service connected to a managed relational database with secrets, network binding, backups, and safe deploy defaults.

#### Subtasks
- Define ManagedDatabase spec with engine, version, size, storage, backups, region, and network settings.
- Compile Stack specs containing Service plus ManagedDatabase plus bindings.
- Generate cloud resources for managed database, secret reference, security group rules, and service environment references.
- Add `skiff init stack api-database <name>`.
- Generate example app, spec, and CI template.
- Implement status and doctor checks for database availability and service connectivity.
- Add docs for credential handling and backup defaults.

#### Likely Files
- `internal/spec/managed_database.go`
- `internal/compiler/stack.go`
- `internal/provider/aws/database.go`
- `cmd/skiff/init_stack.go`
- `examples/stacks/api-database/`
- `docs/recipes/api-database.md`
- `tests/recipes/api_database_test.go`

#### Design
The recipe should make the most common app path easy:

```text
API service
  -> managed database
  -> secret reference
  -> private network access
  -> logs/metrics
  -> safe rollout
```

The user should not need to manually wire IAM, secret names, security groups, and environment references for the happy path.

Example binding:

```yaml
bindings:
  - from: api
    to: db
    as: DATABASE_URL
```

Skiff should create or reference a cloud secret and inject a reference, not store plaintext in object state.

#### Testing / Validation
Use fake provider unit tests for database resource compilation. Golden-test generated stack spec. Test doctor detects unavailable database and missing service binding. Optional AWS integration can be manual due to cost.

#### Gotchas
Managed database creation can be slow and expensive. Make dry-run and explain excellent. Avoid destructive operations on existing databases without explicit sagas and approvals.

#### Acceptance Criteria
- `skiff init stack api-database` generates a working stack template.
- Stack compiler wires service, database, network, and secret references.
- Doctor includes database connectivity checks.
- Dry-run explains cost/risk-relevant resources.

---

## Implement database backup and restore sagas

### ID
skiff-m3-008

### Priority
P0

### Type
task

### Labels
saga, database, restore, operations

### Dependencies
skiff-m3-007

skiff-m2-003

### Description
Implement explicit database backup and restore sagas, with restore-to-new-instance-then-cutover as the safe default.

#### Subtasks
- Implement saga steps: database.snapshot, database.verify_restore_point, database.restore_to_new_instance, database.wait_available, database.run_smoke_query, secret.update_pointer, service.rollout_restart.
- Implement `skiff backup database <name>`.
- Implement `skiff restore database <name> --to <time> --mode new-db-cutover`.
- Add approval gate before cutover.
- Implement compensation: delete/mark restored DB before cutover, restore previous secret version after cutover when safe.
- Add shadow service test step against restored database when an attached service is provided.
- Add event artifacts for restore plan and result.

#### Likely Files
- `internal/saga/steps/database/*.go`
- `internal/saga/templates/database_restore.go`
- `internal/saga/templates/database_backup.go`
- `cmd/skiff/database.go`
- `docs/operations/database-restore.md`
- `tests/saga/database_restore_test.go`

#### Design
Restore is the canonical example of a saga. It is not stable state; it is an operational journey.

Default graph:

```text
preflight
verify restore point
snapshot current DB
restore new DB
smoke test
shadow API against restored DB
approval before cutover
update connection secret
roll service
verify
retain old DB
```

The plan must say what is reversible and what is not.

#### Testing / Validation
Use fake database provider. Test restore success, smoke failure before cutover, approval rejection, secret update failure after cutover, and compensation. Golden-test dry-run plan output.

#### Gotchas
Do not default to in-place destructive restore. Restore-to-new-instance is safer. Cloud database restore times vary; steps must be resumable and store provider restore IDs.

#### Acceptance Criteria
- `skiff restore database` creates an explicit saga.
- Restore plan includes approval before cutover.
- Failure before cutover leaves production unchanged.
- Restore events and results are auditable.

---

## Implement secret and credential rotation sagas

### ID
skiff-m3-009

### Priority
P0

### Type
task

### Labels
saga, secrets, rotation, security

### Dependencies
skiff-m2-002

skiff-m3-007

### Description
Implement secret/credential rotation workflows that can create a new secret version, canary consumers, promote the version, roll services, and retire old credentials safely.

#### Subtasks
- Implement steps: secret.create_version, secret.validate_version, secret.update_pointer, secret.restore_previous_version, service.canary_with_secret, service.roll_consumers, credential.disable_old.
- Add `skiff rotate secret <secret-ref> --consumers <services>`.
- Support database credential rotation through managed database recipe hooks.
- Add approval gates for production rotations when policy requires.
- Implement delayed disable/delete of old credentials as separate scheduled saga or operation.
- Add doctor checks for consumers using stale secret versions.

#### Likely Files
- `internal/saga/steps/secret/*.go`
- `internal/saga/templates/secret_rotation.go`
- `cmd/skiff/rotate.go`
- `internal/doctor/checks/secrets.go`
- `docs/operations/secret-rotation.md`
- `tests/saga/secret_rotation_test.go`

#### Design
Rotation should not mean immediate deletion. Safe rotation graph:

```text
preflight
create new credential/secret version
validate new version
canary one consumer
promote secret pointer
roll remaining consumers
verify
schedule old credential disable
```

If canary fails, restore previous version and stop.

#### Testing / Validation
Fake secret manager and service provider tests. Test success, canary failure, secret permission denied, stale consumer detection, and delayed-disable scheduling.

#### Gotchas
Environment variable injection of secrets is convenient but weaker. Rotation docs should recommend runtime fetch where mature apps can support it. Do not store plaintext secret values in state events.

#### Acceptance Criteria
- `skiff rotate secret` creates a safe rotation saga.
- Canary failure restores previous secret pointer.
- Old credential deletion is delayed and explicit.
- Doctor can identify stale or failed consumers.

---

## Implement key and certificate rotation operational templates

### ID
skiff-m3-010

### Priority
P1

### Type
task

### Labels
saga, kms, certificates, security

### Dependencies
skiff-m3-009

skiff-m3-006

### Description
Add operational templates for encryption key alias rotation and certificate rotation with safe verification and delayed destructive actions.

#### Subtasks
- Implement key rotation template that creates candidate key, re-encrypts eligible secret/material references, canaries consumers, promotes alias, verifies, and schedules old key disable.
- Implement certificate rotation template for plugin-managed or ingress-managed certificates.
- Add `skiff rotate key <alias>` and `skiff rotate cert <name>` commands.
- Classify destructive old-key deletion as a separate high-risk approval operation.
- Add doctor checks for cert expiry and key policy mismatch.
- Add policy hooks requiring approvals for production key rotations.

#### Likely Files
- `internal/saga/templates/key_rotation.go`
- `internal/saga/templates/cert_rotation.go`
- `internal/saga/steps/security/*.go`
- `cmd/skiff/rotate.go`
- `internal/doctor/checks/certificates.go`
- `docs/operations/key-cert-rotation.md`

#### Design
Key and certificate rotation crosses components. It must be transparent and staged.

Do not delete old keys in the same automatic flow. The default final step is:

```text
schedule old key disable after retention
```

Deletion requires a separate explicit approval.

Certificate rotation should include verification from the point of view of consumers, not just successful issuance.

#### Testing / Validation
Use fake KMS/cert providers. Test policy denial without approval. Test candidate cert issued but not promoted. Test expiry doctor finding. Test old key disable scheduling.

#### Gotchas
Key rotation can break every consumer at once if mishandled. Require canary verification and clear blast-radius output. Avoid generic key deletion automation.

#### Acceptance Criteria
- Key and cert rotation templates exist.
- Old destructive actions require explicit approval.
- Doctor can flag expiry or policy mismatch.
- Rotation plans show blast radius and reversibility.

---

## Implement multi-region service and managed database journey

### ID
skiff-m3-011

### Priority
P1

### Type
task

### Labels
multiregion, database, failover, recipe

### Dependencies
skiff-m3-007

skiff-m3-008

skiff-m2-009

### Description
Create a customer journey and implementation path for API service plus multi-region managed database, including regional deploy, read replica, failover, and failback workflows.

#### Subtasks
- Define MultiRegionStack spec with primary region, secondary regions, traffic policy, database replication mode, and failover policy.
- Compile regional service resources and regional database resources through provider abstractions.
- Implement global traffic abstraction or provider-specific DNS/load-balancer patches.
- Implement regional failover saga template.
- Implement failback planning template with explicit irreversibility warnings.
- Add docs and example for API plus multi-region database.
- Add status view showing each region’s service health, database role, replication lag, and traffic weight.

#### Likely Files
- `internal/spec/multiregion.go`
- `internal/compiler/multiregion.go`
- `internal/saga/templates/regional_failover.go`
- `internal/status/multiregion_status.go`
- `cmd/skiff/failover.go`
- `examples/stacks/api-multiregion-database/`
- `docs/recipes/api-multiregion-database.md`

#### Design
This is a premium customer journey, not the first deploy path. It should still be modeled cleanly.

Failover saga:

```text
preflight
verify secondary capacity
verify database replica lag
freeze or drain writes if required
promote secondary database
update writer secret/endpoint
shift 10% traffic
metrics gate
shift 100% traffic
mark secondary primary
```

The plan must clearly state when the operation becomes partially irreversible because new writes occur in the promoted region.

#### Testing / Validation
Use fake providers for multi-region compile and failover. Test status aggregation. Test failover plan risk classification. Test that failback is not automatically offered as simple rollback after writes.

#### Gotchas
Multi-region database behavior is provider-specific. Keep the public recipe honest and capability-driven. Do not pretend every database engine has the same promotion/failback semantics.

#### Acceptance Criteria
- Multi-region stack example exists.
- Regional failover saga is planned and executable with fake provider.
- Status shows per-region service/database health.
- Irreversible points are clearly surfaced.

---

## Implement Statefully managed VM group foundation

### ID
skiff-m3-012

### Priority
P1

### Type
task

### Labels
stateful, storage, recipes

### Dependencies
skiff-m2-002

skiff-m2-014

skiff-m3-005

### Description
Implement the foundation for named stateful VM members with durable volumes, stable identity, fencing, snapshots, and recipe hooks.

#### Subtasks
- Define StatefulGroup and StatefulMember specs and control docs.
- Implement provider interface for volume create/attach/detach/snapshot, DNS update, and instance fencing.
- Implement member control lease and generation fencing.
- Implement replacement saga for a failed member.
- Implement ordered update saga skeleton.
- Add recipe hook interface for start, stop, health, backup, restore, and role detection.
- Add docs emphasizing managed state as the default and StatefulGroup as deliberate.

#### Likely Files
- `internal/spec/stateful.go`
- `internal/state/stateful_control.go`
- `internal/provider/stateful.go`
- `internal/saga/templates/stateful_replace_member.go`
- `internal/stateful/recipe.go`
- `docs/recipes/stateful-group.md`
- `tests/stateful/*.go`

#### Design
StatefulGroup is not a Kubernetes StatefulSet clone. It is explicit:

```text
member 0 = VM identity + durable volume + stable DNS + generation
member 1 = VM identity + durable volume + stable DNS + generation
```

Replacement flow:

```text
acquire member lease
fence old VM
detach volume
launch replacement in same zone
attach volume
boot same member identity
run recipe recovery
verify
```

The platform provides safe mechanics; recipes provide app-specific logic.

#### Testing / Validation
Fake provider tests for member replacement, failed fencing, failed attach, and resume after replacement started. Test generation increments and stale runner refusal.

#### Gotchas
Duplicate writers are worse than downtime. Fencing must be explicit and provider-confirmed before reattaching a single-writer volume. Do not automate stateful scale-down in v1.

#### Acceptance Criteria
- StatefulGroup control docs and provider interface exist.
- Member replacement saga is implemented with fencing.
- Recipe hook interface exists.
- Stateful operations are explicit and auditable.

---

## Implement drift detection and safe garbage collection

### ID
skiff-m3-013

### Priority
P0

### Type
task

### Labels
drift, gc, safety

### Dependencies
skiff-m2-005

skiff-m1-010

### Description
Detect cloud drift from Skiff desired IR/resource records and implement conservative cleanup planning for stale resources and old state artifacts.

#### Subtasks
- Implement drift detector comparing desired IR/resource records against provider-observed state.
- Classify drift as missing, changed, orphaned, unsafe, or informational.
- Add `skiff drift <service>`.
- Implement GC planner for old release artifacts, stale launch template versions, unattached non-stateful resources, old logs indexes, and abandoned operations.
- Require snapshots/retention checks before any stateful cleanup.
- Add `skiff gc plan` and `skiff gc apply --yes`.
- Emit audit events for every GC apply.

#### Likely Files
- `internal/drift/detector.go`
- `internal/gc/planner.go`
- `internal/gc/apply.go`
- `cmd/skiff/drift.go`
- `cmd/skiff/gc.go`
- `tests/drift/*.go`
- `tests/gc/*.go`

#### Design
Drift detection keeps the cloud account from becoming a hidden second source of truth. GC prevents Skiff from accumulating costly leftovers.

Skiff drift asks:

```text
Does cloud reality match Skiff desired IR and resource records?
```

Not:

```text
Does cloud reality match some external tool state?
```

For externally owned resources, report ownership and recommend the owning workflow.

#### Testing / Validation
Fake provider tests for changed ASG min size, unexpected security group ingress, missing target group, orphaned launch template, stale release, and protected stateful volume. Test GC plan is read-only by default.

#### Gotchas
Never auto-delete stateful volumes or databases by default. GC should be plan-first. Direct deletion must require explicit `--yes` and still obey retention/snapshot policy.

#### Acceptance Criteria
- `skiff drift` reports meaningful cloud/state drift.
- `skiff gc plan` is conservative and read-only.
- GC apply audits every deletion or cleanup.
- Stateful resources are protected by default.

---

## Implement cost and shape advisor

### ID
skiff-m3-014

### Priority
P1

### Type
task

### Labels
cost, advisor, autoscaling

### Dependencies
skiff-m2-011

skiff-m2-012

skiff-m3-013

### Description
Add a cost/shape advisor that makes the VM-as-pod tradeoff legible and recommends service shape, replica, warm capacity, and log-volume improvements.

#### Subtasks
- Collect service shape, min/max replicas, observed CPU/memory, request count, target health, warm capacity, and log volume.
- Implement recommendation engine for smaller/larger machine size, min replica adjustment, warm capacity, and noisy logs.
- Add `skiff cost explain <service>`.
- Add advisor warnings into `skiff plan` for obviously expensive defaults.
- Support JSON output for agents and CI.
- Document limitations and estimate confidence.

#### Likely Files
- `internal/cost/advisor.go`
- `internal/cost/models.go`
- `cmd/skiff/cost.go`
- `docs/operations/cost-advisor.md`
- `tests/cost/*.go`

#### Design
Skiff gives up bin-packing by default. The cost advisor should help users see and tune that tradeoff without reintroducing scheduling complexity.

Example output:

```text
payments-api:
  CPU p95 18%, memory p95 41%
  recommendation: try size small instead of medium
  recommendation: reduce min replicas from 12 to 8 after validating SLO
```

Make confidence explicit; do not pretend estimates are billing truth.

#### Testing / Validation
Mock metrics and provider shape data. Test overprovisioned, underprovisioned, high log volume, and warm capacity recommendations. Golden-test human and JSON output.

#### Gotchas
Pricing and discounts vary. Do not hardcode exact billing claims unless provider pricing integration is implemented. Prefer relative recommendations with estimated impact.

#### Acceptance Criteria
- `skiff cost explain` produces useful recommendations.
- Recommendations include confidence and evidence.
- Advisor integrates with plan warnings.
- JSON output is agent-readable.

---

## Implement authorization, approvals, and audit hardening

### ID
skiff-m3-015

### Priority
P0

### Type
task

### Labels
security, authz, audit, approval

### Dependencies
skiff-m2-003

skiff-m2-014

skiff-m3-001

### Description
Harden production operations with authorization checks, approval policies, audit records, and risk-based mutation controls.

#### Subtasks
- Define actor model for user, CI, agent, skiffd, worker, and break-glass identities.
- Define authorization interface and policy decisions for read, deploy, rollback, approve, debug, rotate, restore, failover, and GC.
- Add approval policy rules based on risk, env, service, and operation type.
- Write audit events for all mutating operations and debug sessions.
- Add `skiff authz explain` for a proposed operation.
- Ensure agents can request plans but cannot execute high-risk operations without approval context.
- Add tests for denied, approved, and break-glass flows.

#### Likely Files
- `internal/auth/actor.go`
- `internal/authz/policy.go`
- `internal/authz/explain.go`
- `internal/audit/audit.go`
- `cmd/skiff/authz.go`
- `tests/security/authz_test.go`

#### Design
Security should be defaulted and explainable. A user should see:

```text
You can plan this restore.
You cannot approve this restore.
Approval required from role database-admin.
This operation is high risk because it changes a production database endpoint.
```

Audit events must include actor, action, target, trace ID, risk, approval ID if any, and before/after summaries when safe.

#### Testing / Validation
Unit test authorization matrix. Test approval requirements for production restore, failover, debug shell, key rotation, GC apply, and rollback. Test audit event creation for mutating commands.

#### Gotchas
Avoid building a full enterprise IAM product in v1. Keep the policy interface simple and allow integration hooks. Never let JSON agent mode bypass approval prompts for high-risk actions.

#### Acceptance Criteria
- Mutating operations pass through authorization.
- Risk-based approvals are enforced.
- Audit events are written for sensitive operations.
- `skiff authz explain` tells users why an action is allowed or denied.

---

## Implement provider conformance and plugin conformance suites

### ID
skiff-m3-016

### Priority
P0

### Type
task

### Labels
testing, conformance, providers, plugins

### Dependencies
skiff-m3-005

skiff-m2-019

### Description
Create conformance suites that define what it means for a provider or plugin to be supported by Skiff.

#### Subtasks
- Define provider conformance test interface and fixtures.
- Test provider plan/apply, service deploy, rollout, rollback, logs, metrics, debug, drift, and resource discovery.
- Define plugin conformance tests for manifest validation, patch permissions, saga steps, doctor checks, and explain output.
- Add fake provider and fake plugin implementations as reference examples.
- Document how future GCP/Azure providers must pass conformance before support claims.
- Integrate conformance tests into CI with fake implementations and optional cloud-backed runs.

#### Likely Files
- `tests/conformance/provider/provider_suite.go`
- `tests/conformance/plugin/plugin_suite.go`
- `internal/provider/fake/`
- `plugins/fake/`
- `docs/dev/conformance.md`

#### Design
Conformance prevents provider and plugin sprawl from degrading Skiff’s simplicity. A provider is not supported because it compiles; it is supported because it passes workflows.

Minimum provider conformance:

```text
deploy stateless service
watch rollout
rollback
logs
metrics
doctor inputs
debug bundle
drift detection
```

Minimum plugin conformance:

```text
valid manifest
typed patch only
permission enforcement
explainable additions
doctor/saga hooks if declared
```

#### Testing / Validation
Run fake conformance in every CI build. Optional AWS conformance can run nightly/manual. Add failure output that tells implementers exactly which capability failed.

#### Gotchas
Do not let optional capabilities block core provider support unless declared required. Keep a capability matrix so users can see what is available.

#### Acceptance Criteria
- Provider conformance suite exists.
- Plugin conformance suite exists.
- Fake provider/plugin pass suites.
- Future providers have a documented support bar.

---

## Implement installer, runner image, and self-hosted skiffd deployment recipe

### ID
skiff-m3-017

### Priority
P0

### Type
task

### Labels
packaging, install, runner, skiffd

### Dependencies
skiff-m1-021

skiff-m2-019

skiff-m3-015

### Description
Package Skiff for real users: CLI installers, runner image/base AMI workflow, and a recipe for deploying `skiffd` itself with Skiff.

#### Subtasks
- Create release builds for Linux/macOS binaries.
- Create install script with checksum/signature verification.
- Define base runner image/AMI build process and required system services.
- Package `skiff-runner` and collector configuration into base image.
- Create `skiffd` service spec and bootstrap/deploy guide.
- Ensure Skiff can recover if `skiffd` is down through direct mode.
- Add version compatibility checks between CLI, skiffd, runner, and release manifests.

#### Likely Files
- `scripts/install.sh`
- `build/runner-image/`
- `cmd/skiff/version.go`
- `examples/skiffd/skiff.yaml`
- `docs/install.md`
- `docs/operations/recover-with-direct-mode.md`
- `tests/packaging/*.go`

#### Design
The product cannot feel real until it is installable. The runner image is especially important because every workload VM depends on it.

Compatibility rules:

```text
release manifest declares min runner version
CLI warns if skiffd is older than supported
runner refuses unsupported manifest schema
```

Self-hosted `skiffd` should be a Skiff Service once bootstrap exists.

#### Testing / Validation
Test install script against local release artifacts. Test version compatibility matrix. Validate runner image build scripts in CI where possible. Smoke test self-hosted skiffd example with fake provider.

#### Gotchas
Do not make skiffd recovery depend on skiffd. Document direct mode clearly. Image builds can become slow; keep build pipeline modular and cacheable.

#### Acceptance Criteria
- CLI and server binaries can be installed from release artifacts.
- Runner base image process is documented and testable.
- `skiffd` deployment recipe exists.
- Version compatibility checks are enforced.

---

## Create customer journey docs, recipes, and golden demos

### ID
skiff-m3-018

### Priority
P0

### Type
task

### Labels
docs, recipes, demo, adoption

### Dependencies
skiff-m3-002

skiff-m3-003

skiff-m3-004

skiff-m3-007

skiff-m3-011

skiff-m3-017

### Description
Create the customer-facing journey library and golden demos that show how to adopt Skiff from scratch, from Kubernetes, from Terraform, and through operational sagas.

#### Subtasks
- Write quickstart: bootstrap, deploy hello service, status, logs, doctor, rollback.
- Write API plus managed database guide.
- Write API plus multi-region managed database guide.
- Write Kubernetes migration guide with import, shadow, and cutover.
- Write Terraform adoption guide.
- Write CI/CD guide with generated templates.
- Write operational saga guides: canary, rollback, database restore, secret rotation, regional failover.
- Create golden demo scripts that can be run locally with fake provider.

#### Likely Files
- `docs/quickstart.md`
- `docs/recipes/api-database.md`
- `docs/recipes/api-multiregion-database.md`
- `docs/adoption/kubernetes.md`
- `docs/adoption/terraform.md`
- `docs/adoption/cicd.md`
- `docs/operations/*.md`
- `demos/*.sh`
- `tests/docs/link_check_test.go`

#### Design
Docs are part of the product. Each journey should start with the user problem, show the Skiff command, explain the object/cloud state that changes, and include recovery commands.

Every guide should answer:

```text
What will Skiff create?
What state objects are written?
What cloud primitives are used?
What is reversible?
What command diagnoses failure?
How do I roll back?
```

The golden demos should be used by sales, docs, and test fixtures.

#### Testing / Validation
Add link checker. Run demo scripts with fake provider in CI where practical. Golden-test CLI output snippets if feasible. Have at least one engineer unfamiliar with the plan execute the quickstart.

#### Gotchas
Avoid hiding complexity with marketing prose. The docs should be compelling but precise. If a flow is not fully implemented, label it as roadmap instead of implying support.

#### Acceptance Criteria
- Quickstart and adoption guides are complete.
- Recipes include commands, expected outputs, and recovery paths.
- Golden demo scripts run with fake provider.
- Docs are self-contained and link-checked.

---

## Production readiness, resilience, and chaos validation pack

### ID
skiff-m3-019

### Priority
P0

### Type
task

### Labels
production, resilience, chaos, release-gate

### Dependencies
skiff-m3-015

skiff-m3-016

skiff-m3-017

skiff-m3-018

### Description
Build the final production readiness suite that validates security, resilience, resumability, and core user journeys before Skiff can be called production-ready.

#### Subtasks
- Define production readiness checklist covering security, state durability, operations, observability, docs, packaging, and recovery.
- Implement resilience tests for interrupted deploy, interrupted saga, lost skiffd, stale cache, lease contention, failed rollout, bad release, log outage, metric outage, and debug denial.
- Implement chaos-style fake provider scenarios for instance death, target health failure, ASG rollout failure, and regional outage.
- Validate direct mode can diagnose and recover when skiffd is unavailable.
- Generate a production readiness report artifact.
- Add release gate in CI requiring fake-provider readiness suite.

#### Likely Files
- `tests/readiness/readiness_suite.go`
- `tests/chaos/fake_provider_scenarios.go`
- `docs/production-readiness.md`
- `cmd/skiff/readiness.go`
- `tests/e2e/test_direct_recovery.go`

#### Design
The product claim is operational simplicity. The readiness suite proves it.

Required scenarios:

```text
skiffd down -> direct CLI status and rollback work
deploy process killed -> operation resumes
saga process killed -> saga resumes
bad release -> canary rollback
log backend unavailable -> app remains healthy and doctor reports observability issue
lease contention -> one writer wins and other receives LEASE_HELD
```

The report should be machine-readable and human-readable.

#### Testing / Validation
Run fake-provider readiness in CI. Optional AWS readiness can run manually/nightly. Test report output and failure summaries. Ensure scenarios create isolated state prefixes.

#### Gotchas
Avoid flaky sleep-heavy tests. Use fake clocks and fake provider transitions where possible. Real cloud chaos tests should be opt-in and heavily tagged.

#### Acceptance Criteria
- Production readiness suite passes in CI with fake provider.
- Direct recovery mode is proven.
- Interrupted operations and sagas resume.
- Readiness report is generated and useful.

---

## GA release checklist and support handoff

### ID
skiff-m3-020

### Priority
P0

### Type
task

### Labels
ga, release, support

### Dependencies
skiff-m3-019

### Description
Prepare Skiff for a first production/GA release with supportability, issue triage, runbooks, and release artifacts.

#### Subtasks
- Create GA release checklist with all milestone acceptance criteria.
- Create support runbooks for deploy failure, canary failure, runner failure, state bucket access denied, lease held, stuck saga, failed restore, and skiffd unavailable.
- Create issue templates for bug, provider issue, plugin issue, documentation issue, and security report.
- Create release notes template emphasizing known limitations.
- Create compatibility matrix for CLI, skiffd, runner, AWS provider, and spec schema versions.
- Create onboarding checklist for new engineers.
- Verify all docs and demos align with implemented features.

#### Likely Files
- `docs/release/ga-checklist.md`
- `docs/support/runbooks/*.md`
- `.github/ISSUE_TEMPLATE/*.md`
- `docs/release/release-notes-template.md`
- `docs/compatibility.md`
- `docs/dev/onboarding.md`

#### Design
GA is not just code completeness. It is the ability for users and support engineers to recover from common failures without original designers.

Runbooks should be command-first:

```bash
skiff doctor payments-api --format json
skiff ops events payments-api --since 1h
skiff rollback payments-api --to previous-stable
skiff --direct --state s3://... status payments-api
```

Known limitations should be explicit. Skiff is not a general Kubernetes replacement for arbitrary workloads; it is a cloud-native VM-as-pod deployment and operations platform.

#### Testing / Validation
Review every runbook against fake-provider scenarios. Have a junior engineer execute the stuck saga and direct recovery runbooks. Validate issue templates capture trace IDs, object-state paths, provider resource IDs, and command output.

#### Gotchas
Do not let launch pressure hide limitations. Clear scope builds trust. Keep runbooks short enough to use during incidents but detailed enough for unfamiliar responders.

#### Acceptance Criteria
- GA checklist is complete and signed off.
- Support runbooks cover common failures.
- Compatibility matrix is published.
- Known limitations are documented honestly.
