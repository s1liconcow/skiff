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
