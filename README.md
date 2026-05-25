# Skiff

<div align="center">

[![CI](https://github.com/s1liconcow/skiff/actions/workflows/ci.yml/badge.svg)](https://github.com/s1liconcow/skiff/actions/workflows/ci.yml)
[![E2E](https://github.com/s1liconcow/skiff/actions/workflows/e2e.yml/badge.svg)](https://github.com/s1liconcow/skiff/actions/workflows/e2e.yml)
![Go](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)
![Status](https://img.shields.io/badge/status-active%20development-2ea44f)

**Kubernetes-class operational leverage without Kubernetes-class cost.**

Skiff deploys and operates cloud-native VM workloads without a cluster control plane.

```bash
curl -fsSL https://raw.githubusercontent.com/s1liconcow/skiff/main/scripts/install.sh | \
  SKIFF_INSTALL_VERSION=0.2 SKIFF_INSTALL_DIR="$HOME/.local/bin" bash
```

</div>

## TL;DR

### The Problem

Many services need safe canary deploys, rollbacks, logs, metrics, cost visibility, agent-first ux, and operational runbooks like database restores, secret rotations, stateful failover, debug workflows. They need Kubernetes-class operational leverage without Kubernetes-class cost.  

Terraform is good at describing stable infrastructure shape. Kubernetes is a strong fit when the benefits of large-scale orchestration outweigh the cost and complexity of operating it. Skiff is meant to occupy a smaller space: a low-dependency, secure-by-default, well-lit path for running stateful and stateless services in the cloud.

Skiff does not run a distributed control plane, bin-pack workloads, create overlay networks, manage its own load balancers, or keep durable state anywhere other than object storage. That restraint is intentional. Bin-packing makes sense for Google-scale companies running many clusters on bare metal. In ordinary cloud environments, it often adds little value while introducing a second security model on top of cloud IAM. Containers are useful packaging and isolation tools (Skiff uses OCI), but they are not a strong tenancy boundary, and the long history of container escape vulnerabilities has made that clear. 

### The Solution

Skiff compiles simple service specs into cloud primitives, writes desired state and audit history to object storage, and runs explicit operations such as deploys as typed sagas.

Here is how Skiff maps to Kubernetes:
```text
VM = pod
cloud autoscaling group = deployment
cloud load balancer target group = service
cloud IAM role = workload identity
object storage = durable desired state and audit history
skiffd = stateless facade with rebuildable in-memory views
skiff CLI direct mode = recovery path
```


### Why Use Skiff?

| Need | Skiff behavior | Concrete example |
|---|---|---|
| Deploy without a cluster database | Release and runtime manifests are signed, immutable objects | `skiff deploy skiff.yaml --format json` |
| Recover when the facade is down | The CLI can operate directly against object storage and cloud APIs | `skiff --direct --state s3://skiff-state-prod status payments-api` |
| See real cloud resources | ASGs, target groups, launch templates, IAM roles, log groups, and provider IDs stay visible | `skiff explain skiff.yaml --provider aws` |
| Run risky operations explicitly | Rollback, restore, rotation, cutover, and failover are typed saga graphs | `skiff database restore orders-db --dry-run --format json` |
| Give agents deterministic output | JSON separates facts, hypotheses, recommended commands, risk, and reversibility | `skiff doctor payments-api --fresh --format json` |
| Avoid hidden locks | Leases live inside the control document they protect | service `control.json` is updated with CAS |

## Quick Example

Run the local fake-provider path. It writes real Skiff object state and exercises validation, planning, release publication, deploy, status, logs, metrics, doctor, operation events, and the read-only TUI frame without cloud credentials.

```bash
git clone https://github.com/s1liconcow/skiff.git
cd skiff
make build
export PATH="$PWD/bin:$PATH"

make demo-local

skiff status http-hello --fresh
skiff logs http-hello --limit 20
skiff metrics http-hello
skiff doctor http-hello
skiff ops events http-hello --limit 30
skiff tui http-hello --once --read-only
```

For the full walkthrough, including Apple Container and live AWS paths, see [docs/quickstart.md](docs/quickstart.md).

## Design Philosophy

### Object Storage Is Durable Truth

Skiff writes durable state as signed or schema-versioned documents in object storage. Service control docs, release manifests, saga graphs, events, audit records, and resource summaries must survive `skiffd` restarts and outages.

Example:

```text
services/payments-api/control.json
services/payments-api/releases/rel_01J.../release.json
services/payments-api/operations/op_01J.../intent.json
services/payments-api/operations/op_01J.../events/01J....json
audit/2026-05-19/01J....json
```

### `skiffd` Is A Facade, Not The Database

`skiffd` serves API requests, powers the TUI, streams events, hosts plugins, enforces auth, and keeps fast in-memory views. Those views are rebuildable from object storage. If `skiffd` is unavailable, direct mode remains the recovery path.

```bash
skiff --direct --state s3://skiff-state-prod doctor payments-api --fresh --format json
```

### Operations Are Explicit Sagas

Long-running operations are graph-shaped workflows with typed steps, persisted provider operation IDs, resumable control documents, risk, reversibility, approvals, and append-only events.

```bash
skiff deploy skiff.yaml --canary --canary-stages 5,25,100 --format json
skiff ops watch payments-api --operation op_01J... --format json
skiff ops approve saga_01J... --step approve-cutover --format json
```

### One VM Runs One Workload Replica By Default

The VM is the workload isolation boundary. That makes identity, metrics, logs, health checks, debug sessions, and blast radius easier to reason about than multi-service VM packing.

### Security Is Defaulted

Production releases are signed, artifacts are digest-pinned, state buckets are encrypted and versioned, runner access is scoped, secrets are references rather than plaintext values, and mutating operations write audit records.

## Comparison

| Capability | Skiff | Terraform | Kubernetes | Nomad/systemd-style VM ops |
|---|---|---|---|---|
| Durable operation history | Object-state ledger with immutable events | Terraform state tracks resource shape | Cluster API stores current objects and events | Usually custom logs or scripts |
| Normal deploy unit | One workload replica per VM | Not a deploy orchestrator | Pod/container | Job/task/service process |
| Recovery without API server | Direct CLI over object storage and provider APIs | Terraform can apply from state backend | Usually needs API server/etcd | Depends on local tooling |
| Rollback/restore/rotation model | Explicit saga graphs | Usually external runbooks | Controllers/operators | Usually scripts |
| Cloud primitive visibility | First-class output | First-class resources | Often abstracted behind controllers | Direct but inconsistent |
| Always-running reconciliation | Avoided by design | No | Common | Varies |
| Agent-friendly JSON | Required for operator commands | Provider-dependent | kubectl JSON exists, semantics vary | Usually custom |

Skiff is not a smaller Kubernetes and not a Terraform replacement. A useful shorthand is:

```text
Terraform for shape. Skiff for journeys.
```

## Installation

### Release Installer

The installer downloads a release archive for Linux or macOS, verifies `checksums.txt`, copies `skiff`, `skiffd`, `skiff-runner`, and `skiff-worker`, then runs `skiff version`.

```bash
curl -fsSL https://raw.githubusercontent.com/s1liconcow/skiff/main/scripts/install.sh | \
  SKIFF_INSTALL_VERSION=0.2 SKIFF_INSTALL_DIR="$HOME/.local/bin" bash

export PATH="$HOME/.local/bin:$PATH"
skiff version
```

Set `SKIFF_INSTALL_PUBLIC_KEY` to require archive signature verification through `cosign` or `minisign`.

### Build From Source

```bash
git clone https://github.com/s1liconcow/skiff.git
cd skiff
make build
export PATH="$PWD/bin:$PATH"
skiff version
```

### Install From Source

```bash
git clone https://github.com/s1liconcow/skiff.git
cd skiff
make install PREFIX="$HOME/.local"
skiff version
```

### Install From A Local Release Artifact

```bash
scripts/build-release.sh 0.2

SKIFF_INSTALL_VERSION=0.2 \
SKIFF_INSTALL_BASE_URL="file://$PWD/dist" \
SKIFF_INSTALL_DIR="$HOME/.local/bin" \
scripts/install.sh
```

### Go Install For CLI-Only Development

This installs the `skiff` CLI only. Use `make build` or the release installer when you also need `skiffd`, `skiff-runner`, and `skiff-worker`.

```bash
go install github.com/s1liconcow/skiff/cmd/skiff@latest
skiff version
```

## Quick Start

### Local No-Cloud Path

```bash
make build
export PATH="$PWD/bin:$PATH"
make demo-local
```

Then inspect the service:

```bash
skiff status http-hello --fresh
skiff logs http-hello --limit 20
skiff metrics http-hello
skiff doctor http-hello
skiff ops list --all --service http-hello
```

### Live AWS Bootstrap

Use this in an isolated AWS account or disposable environment first. The deploy step can create real billable resources.

```bash
make build
export PATH="$PWD/bin:$PATH"
export AWS_REGION="${AWS_REGION:-us-west-2}"
export AWS_DEFAULT_REGION="$AWS_REGION"
export SKIFF_ENV="quickstart"

mkdir -p .skiff-live
cd .skiff-live

skiff bootstrap aws \
  --network managed \
  --ingress private \
  --yes \
  --out bootstrap

export SKIFF_CONFIG="$PWD/.skiffconfig"
skiff config show
```

Bootstrap creates or records the environment object-state root, encrypted and versioned state bucket, KMS release signer, IAM roles, managed network substrate, runner defaults, explicit environment class/release policy, and optional ingress resources. Bootstrap defaults to `development`, so unsigned local/dev code is allowed intentionally; use `--class production` or `--class staging` for production-like controls. If you use `--env prod` or `--env production` without `--class production`, bootstrap asks for confirmation; pass `--yes` only when the non-production class is intentional. For public ingress with DNS:

```bash
export SKIFF_COMPANY_NAME="Acme"
export SKIFF_DOMAIN_NAME="example.com"

skiff bootstrap aws \
  --network managed \
  --ingress public \
  --yes \
  --out bootstrap
```

Prepare and plan a service:

```bash
skiff init stack api-database orders \
  --dir orders \
  --artifact registry.example.com/orders-api@sha256:REPLACE_WITH_DIGEST \
  --yes

skiff validate orders/skiff.yaml
skiff plan orders/skiff.yaml
skiff cost explain orders --file orders/skiff.yaml
```

Deploy only after the plan, cost output, AWS account boundary, artifact digest, and ingress model look correct:

```bash
skiff deploy orders/skiff.yaml --plan-only
skiff deploy orders/skiff.yaml
skiff status orders-api --fresh
```

## Command Reference

Global flags can appear before most client commands:

```bash
skiff --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 status payments-api --format json
skiff --api --api-url http://127.0.0.1:8585 ops events payments-api --format json --limit 20
```

| Command | Purpose | Example |
|---|---|---|
| `skiff init` | Create starter service or stack specs | `skiff init stack api-database orders --dir orders --artifact registry.example.com/orders-api@sha256:... --yes` |
| `skiff validate` | Parse, default, and validate a spec | `skiff validate examples/service/http-hello/skiff.yaml --show-defaulted` |
| `skiff plan` | Preview provider changes | `skiff plan skiff.yaml --provider aws --region us-west-2` |
| `skiff explain` | Show the cloud primitives Skiff will use | `skiff explain skiff.yaml --provider aws --format json` |
| `skiff bootstrap aws` | Create or emit the AWS state substrate | `skiff bootstrap aws --env prod --class production --region us-west-2 --network managed --ingress private --yes` |
| `skiff deploy` | Publish signed release state and deploy it | `skiff deploy skiff.yaml --canary --canary-stages 5,25,100` |
| `skiff release candidate create` | Record immutable CI evidence | `skiff release candidate create --service payments-api --candidate-id cand_01J... --release-id rel_01J... --artifact-uri registry.example.com/payments-api@sha256:... --artifact-digest sha256:... --check tests=passed --format json` |
| `skiff release promote` | Validate and record promotion intent | `skiff release promote payments-api --from staging --to prod --candidate cand_01J... --approval-id approval_01J... --format json` |
| `skiff release list` | List immutable release manifests | `skiff release list payments-api --limit 5` |
| `skiff release verify` | Verify a signed release manifest | `skiff release verify --file release.json --runtime-manifest runtime-manifest.json --public-key local-deploy=BASE64_PUBLIC_KEY --format json` |
| `skiff status` | Show service status | `skiff status payments-api --fresh --format json` |
| `skiff logs` | Query provider logs | `skiff logs payments-api --since 20m --instance i-abc123 --format json` |
| `skiff metrics` | Query provider metrics | `skiff metrics payments-api --metric aws.elb.request_count --since 15m --format json` |
| `skiff doctor` | Diagnose health and recommend actions | `skiff doctor payments-api --fresh --format json` |
| `skiff cost explain` | Explain shape, capacity, and cost recommendations | `skiff cost explain payments-api --file skiff.yaml --pricing-scheme on-demand` |
| `skiff cost pricing update` | Refresh local pricing catalog | `skiff cost pricing update --region us-west-2 --out .skiff-pricing.json` |
| `skiff rollback` | Start a rollback saga | `skiff rollback payments-api --to previous-stable --format json` |
| `skiff ops list` | List operation records | `skiff ops list --all --service payments-api` |
| `skiff ops inspect` | Inspect an operation or saga | `skiff ops inspect --service payments-api --operation op_01J... --format json` |
| `skiff ops events` | List recent service, operation, or saga events | `skiff ops events payments-api --limit 30` |
| `skiff ops watch` | Follow an operation event stream | `skiff ops watch payments-api --operation op_01J... --format json` |
| `skiff ops approve` | Approve a waiting saga step | `skiff ops approve saga_01J... --step approve-cutover --format json` |
| `skiff ops resume` | Resume an operation or saga | `skiff ops resume saga_01J... --format json` |
| `skiff database backup` | Create and verify a managed database restore point | `skiff database backup orders-db --direct --state s3://skiff-state-prod --format json` |
| `skiff database restore` | Restore through an approval-gated saga | `skiff database restore orders-db --to 2026-05-17T02:00:00Z --mode new-db-cutover --secret-ref secret://managed-database/orders-db/connection-url --format json` |
| `skiff rotate secret` | Rotate a secret pointer and consumers | `skiff rotate secret secret://managed-database/orders-db/connection-url --consumers orders-api --dry-run --format json` |
| `skiff rotate key` | Rotate key material through a saga template | `skiff rotate key alias/skiff/prod/state --consumers payments-api --dry-run --format json` |
| `skiff rotate cert` | Rotate certificate references | `skiff rotate cert payments-api-mtls --consumers payments-api --dry-run --format json` |
| `skiff failover` | Plan or run a regional failover saga | `skiff failover orders --database orders-db --from-region us-west-2 --to-region us-east-1 --dry-run --format json` |
| `skiff cutover` | Create weighted traffic cutover | `skiff cutover payments-api --from kube --to skiff --percent 10 --format json` |
| `skiff debug collect` | Create a redacted diagnostic bundle | `skiff debug collect payments-api --instance i-abc123 --approval-id approval_01J... --format json` |
| `skiff drift` | Compare resource records with provider inspection | `skiff drift payments-api --format json` |
| `skiff gc plan` | Plan conservative cleanup | `skiff gc plan --service payments-api --format json` |
| `skiff gc apply` | Apply approved cleanup | `skiff gc apply --service payments-api --approval-id approval_01J... --yes --format json` |
| `skiff stateful` | Plan, apply, and inspect StatefulGroups | `skiff stateful plan examples/stateful/single-member/skiff.yaml --provider aws --region us-west-2` |
| `skiff import kube` | Convert Kubernetes manifests into Skiff specs | `skiff import kube ./k8s --out skiff.yaml` |
| `skiff terraform generate` | Generate Terraform modules for Skiff specs | `skiff terraform generate skiff.yaml --out infra/skiff/payments-api` |
| `skiff adopt terraform` | Record externally managed resources in object state | `skiff adopt terraform infra/skiff/payments-api --format json` |
| `skiff ci generate` | Generate CI/CD templates | `skiff ci generate github-actions --service payments-api --spec skiff.yaml --state s3://skiff-state-prod --provider aws --region us-west-2 --out .github/workflows/skiff.yml` |
| `skiff contract test` | Run CI contract checks | `skiff contract test skiff.yaml --artifact-uri registry.example.com/payments-api@sha256:... --artifact-digest sha256:...` |
| `skiff authz explain` | Explain policy and approval decisions | `skiff authz explain --action restore --service payments-api --env prod --risk high --format json` |
| `skiff plugin` | Validate and run trusted plugins | `skiff plugin validate --plugin ./plugins/mtls --format json` |
| `skiff config` | Inspect and switch contexts | `skiff config get-contexts` |
| `skiff tui` | Open the terminal operations dashboard | `skiff tui payments-api --read-only` |
| `skiff version` | Print client and optional server version | `skiff version --format json` |

Discover the complete surface:

```bash
skiff help
skiff help workflows
skiff help adoption
skiff help dev
skiff help all
```

## Configuration

Skiff reads a config file with named contexts. Contexts make the same command work in direct recovery mode or through `skiffd`.

```yaml
# .skiffconfig
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: prod-direct
contexts:
  - name: prod-direct
    context:
      mode: direct
      env: prod
      provider: aws
      region: us-west-2
      state: s3://skiff-acme-prod-us-west-2-state
  - name: prod-api
    context:
      mode: api
      env: prod
      provider: aws
      region: us-west-2
      state: s3://skiff-acme-prod-us-west-2-state
      api_url: https://skiffd.prod.example.com
```

Use it:

```bash
export SKIFF_CONFIG="$PWD/.skiffconfig"
skiff config get-contexts
skiff config use-context prod-direct
skiff status payments-api --fresh
```

Common environment defaults:

| Variable | Used for |
|---|---|
| `SKIFF_CONFIG` | Config file path |
| `SKIFF_CONTEXT` | Context override |
| `SKIFF_ENV` | Environment name |
| `AWS_REGION` / `AWS_DEFAULT_REGION` | AWS SDK, bootstrap, provider, and pricing region |
| `SKIFF_COMPANY_NAME` | Friendly bootstrap bucket/resource names |
| `SKIFF_DOMAIN_NAME` | Public ingress base domain and wildcard service hosts |

Example service spec:

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
    tls:
      enabled: true
      certRef: aws-acm://us-west-2/certificate/payments-api
```

## Architecture

```text
Humans / agents / CI
        |
        v
      skiff CLI  <-----------------------------+
        |                                      |
        | API mode                             | direct recovery mode
        v                                      |
      skiffd                                   |
  API / TUI / auth / plugins / sagas           |
  rebuildable in-memory indexes                |
        |                                      |
        v                                      |
object storage state bucket <------------------+
        |
        +--> immutable release manifests
        +--> operation and saga intents
        +--> CAS control documents with leases
        +--> append-only events and audit records
        +--> rebuildable indexes
        |
        v
cloud provider primitives
  ASG / target group / launch template / IAM / logs / metrics
        |
        v
VM workload replicas
  skiff-runner -> verified artifact -> one workload process
```

Required components:

| Component | Role |
|---|---|
| `skiff` | CLI/TUI entrypoint for humans, CI, and agents |
| `skiff-runner` | VM-local runner that fetches signed manifests and starts the workload |
| object storage state bucket | Durable desired state, release ledger, operation events, and audit history |
| cloud provider primitives | AWS-first implementation surface |

Normal product components:

| Component | Role |
|---|---|
| `skiffd` | Stateless API facade with in-memory indexes, event streaming, auth, plugin execution, and TUI support |
| `skiff-worker` | Optional operation/saga resumer for long-running workflows |
| plugins | Typed extensions for mTLS, diagnostics, runtime add-ons, and saga steps |

## Durable State Model

Object storage is plain, inspectable, and durable.

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

| Object type | Mutation rule |
|---|---|
| release manifest | create-only |
| runtime manifest | create-only |
| operation intent | create-only |
| operation event | create-only |
| saga intent | create-only |
| saga graph | create-only |
| saga event | create-only |
| audit event | create-only |
| service control | compare-and-swap only |
| operation control | compare-and-swap only |
| saga control | compare-and-swap only |
| derived indexes | rebuildable |

Control documents are also lock documents. Skiff does not create separate lock files.

## Agent Output

All user-facing commands should support `--format json`, `--no-color`, `--yes`, and `--trace-id` where applicable. JSON mode emits valid JSON only and includes enough context for a human or agent to continue after interruption.

Example:

```json
{
  "ok": false,
  "code": "CANARY_FAILED",
  "summary": "Canary failed metrics gate",
  "trace_id": "tr_01J...",
  "facts": [
    {"type": "target_health", "message": "new release targets are healthy"},
    {"type": "metrics_gate", "message": "new release error rate is above threshold"}
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

Exit codes:

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

## Development

```bash
make build
make test
make readiness
make e2e-local
```

Use repository-local Go caches when running tests manually:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/gomod go test ./...
```

Optional gates:

```bash
make e2e-apple-container
SKIFF_AWS_E2E=1 make e2e-aws
```

Real cloud tests are gated, isolated by unique names/prefixes, heavily tagged, and cleanup-safe. See [docs/production-readiness.md](docs/production-readiness.md) and [docs/dev/e2e-matrix.md](docs/dev/e2e-matrix.md).

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `checksum for skiff_... not found` during install | `SKIFF_INSTALL_VERSION` does not match a published archive | Use a tag that has release assets, or build local artifacts with `scripts/build-release.sh 0.2` and set `SKIFF_INSTALL_BASE_URL=file://$PWD/dist` |
| `config validation failed` | Missing context, state URI, provider, env, or region | Run `skiff config show --format json` and pass explicit flags such as `--direct --state s3://... --env prod --provider aws --region us-west-2` |
| `release signing seed required` or signing errors | Non-fake deploy lacks a signing key reference | Use bootstrap-created `aws-kms://...`, pass `--signing-key-ref`, or use `--signing-backend keychain` for local fallback |
| `PRECONDITION_FAILED`, `LEASE_HELD`, or `LEASE_LOST` | A CAS control document changed or another actor holds the embedded lease | Rerun `skiff status <service> --fresh --format json`, inspect `skiff ops events <service>`, then retry or resume the operation |
| Service is unhealthy after deploy | Target health, runner state, or app health checks failed | Run `skiff doctor <service> --fresh --format json`, then follow the recommended non-mutating commands before rollback |
| Logs or metrics are empty | Provider observability resources are missing, stale, or not yet emitting | Run `skiff explain skiff.yaml`, `skiff status <service> --fresh`, then `skiff doctor <service> --fresh --format json` |
| Direct mode cannot find state | Wrong object-state URI or credentials | Verify the context with `skiff config show`, then test direct access with `skiff --direct --state <uri> ops events <service> --format json` |

## Limitations

| Limitation | Current state |
|---|---|
| AWS is first | Provider interfaces are intended to be extensible, but AWS is the main implementation target today |
| Not a Kubernetes emulator | Imports can help migration, but unsupported Kubernetes behavior must warn or fail clearly |
| No generic bin packing | One VM runs one workload replica by default |
| `skiffd` is not required for recovery | Some normal API/TUI workflows use `skiffd`, but correctness must not depend on it |
| Package managers are not the primary install path yet | Use the release installer, source build, or local release artifacts |
| Live cloud operations can cost money | Bootstrap and deploy make billable AWS resources visible, especially NAT gateways, ALBs, ACM, hosted zones, ASGs, and state buckets |
| Production use requires real artifact discipline | Use digest-pinned artifacts, release signing, least-privilege IAM, and approval gates |

## FAQ

### Is Skiff a Kubernetes replacement?

No. Skiff does not mirror Kubernetes APIs or schedule containers into a cluster. It uses cloud primitives directly and treats the VM as the workload isolation boundary.

### Is Skiff a Terraform replacement?

No. Terraform can still own stable infrastructure shape. Skiff owns releases, rollouts, rollback, diagnosis, sagas, and operation state. Skiff can also emit Terraform for supported bootstrap and service shapes.

### Why object storage instead of a database?

Object storage gives Skiff durable, inspectable, versionable truth that survives `skiffd`. Mutable state uses compare-and-swap control documents. Immutable history is create-only.

### What happens when `skiffd` is down?

Use direct mode:

```bash
skiff --direct --state s3://skiff-state-prod status payments-api --fresh
skiff --direct --state s3://skiff-state-prod rollback payments-api --to previous-stable --format json
```

### How does Skiff handle long-running operations?

Operations are saga graphs. Step results, provider operation IDs, control state, events, and audit records are stored before waiting so a worker, CLI, or `skiffd` can resume.

### Where do secrets go?

Skiff stores secret references, not plaintext secret values, in object state and events. Use cloud secret managers or another secure store.

### Can agents use Skiff safely?

Yes. JSON output is a first-class contract. Recommendations include command strings, mutation metadata, safety, reversibility, operation IDs, saga IDs, and trace IDs.

### How do I learn the deeper design?

Start with these:

- [docs/quickstart.md](docs/quickstart.md)
- [docs/dev/cli.md](docs/dev/cli.md)
- [docs/dev/object-state-layout.md](docs/dev/object-state-layout.md)
- [docs/install.md](docs/install.md)
- [docs/production-readiness.md](docs/production-readiness.md)

## About Contributions

*About Contributions:* Please don't take this the wrong way, but I do not accept outside contributions for any of my projects. I simply don't have the mental bandwidth to review anything, and it's my name on the thing, so I'm responsible for any problems it causes; thus, the risk-reward is highly asymmetric from my perspective. I'd also have to worry about other "stakeholders," which seems unwise for tools I mostly make for myself for free. Feel free to submit issues, and even PRs if you want to illustrate a proposed fix, but know I won't merge them directly. Instead, I'll have Claude or Codex review submissions via `gh` and independently decide whether and how to address them. Bug reports in particular are welcome. Sorry if this offends, but I want to avoid wasted time and hurt feelings. I understand this isn't in sync with the prevailing open-source ethos that seeks community contributions, but it's the only way I can move at this velocity and keep my sanity.

## License

No standalone license file is currently committed. Treat this repository as source-available unless a license is added.
