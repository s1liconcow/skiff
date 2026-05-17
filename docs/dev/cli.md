# CLI contract

The `skiff` CLI is both a human operator tool and an agent interface.

## Global flags

Root-level flags can appear before initial client commands:

```bash
skiff --direct --state file:///var/lib/skiff-state --env prod --provider aws --region us-west-2 status --format json
skiff --api --api-url http://127.0.0.1:8585 events --format json --limit 20
```

Supported global flags:

```text
--config
--env
--provider
--region
--state
--api
--direct
--format
--no-color
--yes
--trace-id
```

`--direct` reads durable object state directly. `--api` calls `skiffd` through the API client. Both modes use the same client command surface for `status` and config-backed `events`.

JSON mode is non-interactive and emits valid JSON only on stdout. Human-mode diagnostics go to stderr.

## Exit codes

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

JSON error envelope:

```json
{
  "ok": false,
  "code": "CONFIG_INVALID",
  "summary": "config validation failed",
  "trace_id": "tr_...",
  "recommended_actions": [
    {
      "id": "inspect_config",
      "command": "skiff config show --format json",
      "mutating": false
    }
  ]
}
```

## Authorization

`skiff authz explain` explains the default authorization and approval decision
for a proposed operation without mutating object state.

```bash
skiff authz explain --action restore --service payments-api --env prod --risk high --actor-type agent --actor-id agent-one --format json
skiff authz explain --action restore --service payments-api --env prod --risk high --approval-id approval_01J... --format json
```

High-risk production operations such as restore, failover, debug, rotation, and
garbage collection require approval context unless the actor is explicit
break-glass. Agents can request plans, but execution is denied until approval
context is present.

## Drift And GC

`skiff drift` compares Skiff resource records with provider inspection. It is
read-only and reports missing, changed, orphaned, unsafe, and informational
findings.

```bash
skiff drift payments-api --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

`skiff gc plan` is conservative and read-only. `skiff gc apply --yes` audits
cleanup decisions and skips protected stateful resources unless a later explicit
workflow proves snapshot and retention policy.

```bash
skiff gc plan --service payments-api --direct --state s3://skiff-state-prod --env prod --format json
skiff gc apply --service payments-api --direct --state s3://skiff-state-prod --env prod --approval-id approval_01J... --yes --format json
```

Completion scripts are generated with:

```bash
skiff completion bash
skiff completion zsh
skiff completion fish
```

## Explain

`skiff explain` compiles a Service spec and shows the provider primitives Skiff will use. For AWS, the output includes IAM roles and instance profiles, security groups, CloudWatch logs and metrics, target groups, listener rules, launch templates, Auto Scaling Groups, and the runner user-data that points at object state.

```bash
skiff explain examples/service/skiff.yaml --provider aws --region us-west-2 --state s3://skiff-state-prod
skiff explain examples/service/skiff.yaml --format json --trace-id tr_explain
skiff explain examples/service/skiff.yaml --plugin ./plugins/mtls --format json
```

`skiff plugin` validates trusted extension manifests, lists configured plugins, explains declared permissions and typed patches, and runs local command plugins during development.

```bash
skiff plugin validate ./plugins/mtls --format json
skiff plugin explain ./plugins/mtls --spec examples/service/skiff.yaml --format json
skiff plugin dev --plugin ./plugins/mtls --hook mutate_ir --request request.json --format json
```

## Plan

`skiff plan` renders the dry-run provider changes for a spec. JSON output includes the action, cloud kind, provider name, fingerprint, and desired payload for each resource so agents can inspect create/update/no-op decisions before any mutating deploy path runs.

```bash
skiff plan examples/service/skiff.yaml --provider aws --region us-west-2 --state s3://skiff-state-prod
skiff plan examples/service/skiff.yaml --format json --trace-id tr_plan
```

## Deploy

`skiff deploy` is direct-mode first. `--dry-run` and `--plan-only` render the deploy plan and write no object state. A mutating deploy requires an explicit signing seed so release manifests are signed before service control is updated.

```bash
skiff deploy examples/service/skiff.yaml --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --dry-run --format json
skiff deploy examples/service/skiff.yaml --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --release-id rel_01J... --signing-seed-base64 <seed>
```

## Release Candidates And Promotion

CI systems can record immutable release candidate evidence before asking Skiff to promote an artifact. Candidate objects are create-only service-scoped records under object state and include the artifact digest, git metadata, CI metadata, and named evidence checks.

```bash
skiff release candidate create --direct --state s3://skiff-state-staging --env staging --provider aws --region us-west-2 --service payments-api --candidate-id cand_01J... --release-id rel_01J... --artifact-uri registry.example.com/payments-api@sha256:... --artifact-digest sha256:... --check tests=passed --check contract=passed --check policy=passed --check scan=passed --format json
```

Promotion is plan-first and evidence-gated. It validates the candidate, required checks, optional stable duration, and production approval context, then records a `release.promote` operation intent/control plus events and audit records when requirements pass.

```bash
skiff promote payments-api --direct --state s3://skiff-state-staging --env staging --provider aws --region us-west-2 --from staging --to prod --candidate cand_01J... --min-stable-duration 30m --approval-id approval_01J... --format markdown
skiff promote payments-api --direct --state s3://skiff-state-staging --env staging --provider aws --region us-west-2 --from staging --to prod --candidate cand_01J... --approval-id approval_01J... --format json
```

## Rollout

`skiff rollout watch` resumes from the provider rollout ID stored in operation control and emits stable rollout status values such as `starting`, `rolling_out`, `succeeded`, `failed`, `cancelled`, and `rolling_back`.

```bash
skiff rollout watch payments-api --operation op_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

## Logs

`skiff logs` queries the hot cloud log backend. JSON entries include timestamp, message, provider source, and labels for service, env, release, instance, region, and zone when available.
AWS service logs use the CloudWatch log group `/skiff/<env>/<service>`. Runner user-data includes the expected CloudWatch forwarding config for stdout/stderr, a stream template of `{service}/{release}/{instance}`, and a future cold archive prefix under `services/<service>/log-archives/<env>/`.

```bash
skiff logs payments-api --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --since 20m --release rel_01J... --format json
skiff logs payments-api --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --follow --instance i-abc123 --format json
```

## Metrics

`skiff metrics` queries the hot cloud metrics backend. JSON series include metric name, unit, provider source, service labels, and timestamped points. AWS core metrics cover ASG capacity, target health, ALB request count, ALB latency, ALB 5xx, and EC2 CPU.

Runtime manifests also carry the app metrics endpoint (`/metrics` by default), which the runner can render into local collector config with service/env/release/instance labels.

```bash
skiff metrics payments-api --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --since 15m --format json
skiff metrics payments-api --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --metric aws.elb.request_count --release rel_01J... --format json
```
