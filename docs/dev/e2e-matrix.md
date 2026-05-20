# E2E Capability Matrix

Skiff has three e2e modes:

- Local: mandatory, deterministic, no cloud credentials. Uses file object state and the fake provider.
- Apple silicon: optional. Uses Apple `container`, RustFS as S3-compatible object state, and Caddy as a digest-pinned OCI workload.
- AWS: optional and explicitly gated. The default smoke gate proves plan/explain lowering without mutation; `SKIFF_AWS_E2E_LIVE_APPLY=1` uses SDK-backed live adapters to create/update core AWS primitives.

Run the mandatory suite:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/gomod go test ./tests/e2e
make e2e-local
```

Run optional modes:

```bash
make e2e-apple-container
make e2e-apple-stateful
SKIFF_OPSEM_E2E=1 SKIFF_E2E_OPSEM_IMAGE=registry.example/skiff-opsem@sha256:... make e2e-apple-stateful
SKIFF_OPSEM_PROFILES_E2E=1 SKIFF_E2E_OPSEM_IMAGE=registry.example/skiff-opsem@sha256:... make e2e-apple-opsem-profiles
SKIFF_E2E_OPSEM_IMAGE=registry.example/skiff-opsem@sha256:... make e2e-apple-stateful-packages
SKIFF_AWS_E2E=1 SKIFF_AWS_E2E_STATE=s3://skiff-e2e-state SKIFF_AWS_E2E_REGION=us-west-2 SKIFF_AWS_E2E_PREFIX=skiff-e2e-$(date +%s) make e2e-aws
SKIFF_AWS_E2E=1 SKIFF_AWS_E2E_LIVE_APPLY=1 SKIFF_AWS_E2E_STATE=s3://skiff-e2e-state SKIFF_AWS_E2E_REGION=us-west-2 SKIFF_AWS_E2E_PREFIX=skiff-e2e-$(date +%s) SKIFF_AWS_VPC_ID=vpc-... SKIFF_AWS_SUBNET_IDS=subnet-a,subnet-b SKIFF_AWS_AMI_ID=ami-... make e2e-aws
```

CI runs `make e2e-local` on pull requests. The `e2e` workflow also exposes
manual `apple`, `aws`, and `all` modes, plus the scheduled AWS smoke gate. Use
the `aws_live_apply` workflow input, or set the matching live-apply secrets, to
turn the AWS smoke gate into a live primitive deployment.

| Capability | Local | Apple silicon | AWS | Evidence |
|---|---|---|---|---|
| validate | covered | not_applicable | not_applicable | `TestLocalCLIEndToEndCapabilityMatrix` runs `skiff validate`. |
| compile | covered | not_applicable | not_applicable | `TestLocalCLIEndToEndCapabilityMatrix` runs `skiff compile`. |
| plan | covered | not_applicable | gated | Local and AWS smoke use AWS lowering without credentials or mutation. |
| explain | covered | not_applicable | gated | Local and AWS smoke explain visible AWS primitives. |
| release signing and verification | covered | covered | gated | Local verifies release/runtime manifests; Apple publishes signed runtime manifests into RustFS and verifies the fetched release through the CLI; AWS live apply publishes signed release/runtime objects before provider mutation. |
| deploy | covered | optional | gated | Local deploys twice through the fake provider; Apple proves runner-side rollout; AWS live apply deploys core IAM, security group, log group, target group, launch template, and ASG primitives when live gates are present. |
| operation watch | covered | optional | gated | Local starts rollout operations and watches durable operation events; Apple rolls to a second release. |
| status | covered | optional | gated | Local reads direct-mode status from object state; Apple reads direct status and local `skiffd` API status from RustFS-backed S3 state. |
| events | covered | optional | gated | Local reads service events and writes report object paths; Apple writes runner, operation, and saga events into RustFS and replays canary saga events through local `skiffd`. |
| logs | covered | not_applicable | gated | Local queries fake-provider logs through the same CLI provider factory used by direct mode. |
| metrics | covered | not_applicable | gated | Local queries fake-provider metrics through the same CLI provider factory used by direct mode. |
| doctor | covered | optional | gated | Local runs direct-mode doctor and rejects critical findings; Apple runs direct and local `skiffd` API doctor against RustFS-backed status, resource, and event objects. |
| canary | covered | optional | gated | Local runs a one-stage canary saga; Apple starts a three-stage rolling canary in direct mode and monitors it through local `skiffd`. |
| rollback | covered | not_applicable | gated | Local rolls back to the previous stable release and verifies service control. |
| direct mode | covered | covered | gated | Local uses `--direct`; Apple uses direct mode against RustFS for runner recovery checks and to start the canary saga. |
| StatefulGroup live apply | covered | gated | gated | Apple StatefulGroup e2e deploys three local member containers, one persistent Apple volume per member, ordered in-place update, replace-member with volume movement, direct inspect, and local `skiffd` API status. |
| Operation semantic fixtures | covered | gated | not_applicable | `tests/fixtures/opsem` models primary/replica, raft, ISR, slot, and shard semantics with deterministic failure injection; the gated opsem Apple harness validates live member state and volume movement with the supplied opsem image. |
| Operation profile DSL semantic e2e | covered | gated | not_applicable | `TestOpsemAppleOperationProfilesE2E` locks opsem package fixtures, runs `skiff ops run`, executes rendered package-step saga graphs against live Apple StatefulGroups, verifies durable operation/saga/audit objects, direct resume, unsafe-state blocking, and local `skiffd` inspect. |
| Stateful package validation matrix | covered | gated | not_applicable | `TestStatefulPackageValidationMatrixData` tracks first-party package rows and implemented fixtures; `make e2e-apple-stateful-packages` runs the live Apple package validation gate with package name, version/digest, mode, operation IDs, saga IDs, provider IDs, object paths, facts, blocked unsafe scenarios, cleanup, and next-command report data. |
| drift | covered | not_applicable | gated | Local runs drift against persisted fake-provider resource records. |
| debug collect | covered | not_implemented | gated | Local direct mode collects a redacted bundle through the fake provider and writes durable audit/event records. AWS remains gated behind a live SSM client adapter. |
| cost advisor | covered | fixture | gated | Local runs `skiff cost explain` with supplied metrics and AWS EC2/RDS price-list fixtures. Live AWS pricing fetches remain gated by explicit `--aws-pricing`. |
| provider conformance | covered | not_applicable | gated | CI runs `go test ./tests/conformance/provider`; local e2e documents the entry point. |
| plugin conformance | covered | not_applicable | not_applicable | CI runs `go test ./tests/conformance/plugin`; local e2e runs `skiff plugin validate`. |
| runner signed release | covered | covered | gated | Local runner fixture serves a signed release; Apple runs signed OCI releases in local Linux VMs. |

## Reports

Set `SKIFF_E2E_REPORT_DIR` to collect JSON reports. Reports include trace ID, operation IDs, saga IDs, provider IDs, object paths, facts, cleanup status, and recommended next commands. Without the variable, tests write reports to a test temp directory and log the path.

## Apple Silicon Environment

- `SKIFF_APPLE_CONTAINER_E2E=1` or `SKIFF_CONTAINER_E2E=1` enables the Apple test.
- `SKIFF_APPLE_STATEFUL_E2E=1` enables the Apple StatefulGroup test.
- `SKIFF_OPSEM_E2E=1` enables the live `skiff-opsem` Apple harness.
- `SKIFF_OPSEM_PROFILES_E2E=1` enables the live operation-profile DSL semantic harness.
- `SKIFF_APPLE_STATEFUL_PACKAGES_E2E=1` enables the live stateful package validation gate. The `e2e-apple-stateful-packages` make target sets it automatically.
- `SKIFF_E2E_CADDY_IMAGE` defaults to `docker.io/library/caddy:2-alpine` and is resolved to a digest before release publication.
- `SKIFF_E2E_CADDY_NEXT_IMAGE` optionally rolls the second release to a different digest-pinned Caddy image.
- `SKIFF_E2E_STATEFUL_IMAGE` defaults to `docker.io/library/busybox:1.36` and is resolved to a digest before StatefulGroup deployment.
- `SKIFF_E2E_OPSEM_IMAGE` points to an OCI image built from `tests/fixtures/opsem/Dockerfile`; it must be digest-pinned or registry-resolvable.
- `SKIFF_E2E_RUSTFS_IMAGE` defaults to `docker.io/rustfs/rustfs:latest`.

The test creates unique Apple container names and RustFS buckets and registers cleanup handlers for containers and volumes. It also writes operation, resource, event, saga, and audit objects into RustFS so direct-mode status, events, doctor, ops inspection, release verification, local `skiffd` API reads, and canary event-stream replay run against S3-compatible object state.

The Apple StatefulGroup test creates a RustFS-backed object-state bucket plus three Apple member containers and three persistent Apple volumes. It records provider IDs and cleanup commands in the JSON report, then runs ordered in-place update and replace-member/move-volume sagas against the live member processes.

The operation-profile semantic harness creates one opsem StatefulGroup per semantic mode, resolves the matching package fixture from `tests/fixtures/packages/`, renders and writes package-backed operation/saga documents through `skiff ops run`, and executes the rendered package-step graph against the live member admin APIs. It intentionally interrupts the RAFT scenario once to prove direct resume and injects unsafe states to prove package gates stop before live member mutation.

## Stateful Package Validation

The package validation gate is release-blocking unless explicitly waived, but it
does not run in normal PR CI. Each implemented first-party self-managed package
must have deterministic package conformance and a live Apple `skiff-opsem`
scenario before the package bead can count as operationally safe.

| Package | Mode | `skiff-opsem` mode | Status |
|---|---|---|---|
| `postgres-ha` | self-managed | `primary-replica` | covered |
| `mysql-ha` | self-managed | `primary-replica` | covered |
| `kafka` | self-managed | `partition-isr` | covered |
| `nats-jetstream` | self-managed | `raft-groups` | covered |
| `redis-ha` | self-managed | `primary-replica` | covered |
| `redis-cluster` | self-managed | `slot-cluster` | covered |
| `opensearch-ha` | self-managed | `shard-cluster` | covered |
| `elasticsearch-ha` | self-managed | `shard-cluster` | covered |

## AWS Environment

- `SKIFF_AWS_E2E=1` enables AWS smoke gates.
- `SKIFF_AWS_E2E_STATE` names the S3 object-state bucket URI.
- `SKIFF_AWS_E2E_REGION` or `AWS_REGION` selects the region.
- `SKIFF_AWS_E2E_PREFIX` must be unique for the run and is recorded in reports.
- `SKIFF_AWS_E2E_LIVE_APPLY=1` requests SDK-backed live apply after the non-mutating plan/explain gate passes.
- `SKIFF_AWS_VPC_ID`, `SKIFF_AWS_SUBNET_IDS`, and `SKIFF_AWS_AMI_ID` are required live-shape inputs for the legacy direct e2e harness. The quickstart bootstrap path can instead write those defaults to `envs/<env>/root.json`, using `/skiff/runner/ami/al2023/x86_64/stable` by default and the AWS public AL2023 SSM AMI parameter as an explicit fallback.
- `SKIFF_AWS_ALB_LISTENER_ARN` is required for services with listener rules unless a bootstrap environment root supplies an ALB listener.
- `SKIFF_AWS_LOAD_BALANCER_SECURITY_GROUP_REF` is required when VM ingress uses the `load-balancer` source unless a bootstrap environment root supplies an ALB security group.

AWS tests must remain optional for PRs. Live runs use a prefix-derived service name, Skiff tags, and JSON reports with provider IDs, operation IDs, durable object paths, trace IDs, cleanup status, and follow-up cleanup/status commands. Automatic destructive cleanup is intentionally not performed by the current smoke test; cleanup status and tag-discovery commands are recorded so operators can remove isolated test resources explicitly.

## Failure Triage

Useful first commands:

```bash
skiff status http-hello --direct --state file://<state-dir> --env prod --provider fake --region local --format json
skiff ops events http-hello --direct --state file://<state-dir> --env prod --provider fake --region local --format json
skiff drift http-hello --direct --state file://<state-dir> --env prod --provider fake --region local --format json
```

For Apple failures, inspect the logged report path and Apple container logs for the RustFS and Caddy containers. For AWS failures, preserve the JSON report because it carries trace IDs, object paths, provider IDs, and the prefix needed for cleanup.
