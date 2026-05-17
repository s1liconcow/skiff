# Secure Debug

Skiff debug starts with a read-only diagnostic bundle. Interactive access is a
separate, audited provider path and must not require inbound SSH.

## Collect a Bundle

```bash
skiff debug collect payments-api \
  --instance i-abc123 \
  --reason "investigate elevated 5xx" \
  --approval-id approval_01J... \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --format json \
  --out debug-payments-api.json
```

Expected output includes `ok: true`, `bundle.bundle_id`,
`bundle.release_digest`, `bundle.target_health`, `bundle.collector_status`,
redactions, recent logs, metrics, and recommended next commands.

## What Is Collected

The bundle gathers best-effort incident context:

- service control from object storage
- stable or desired release ID and digest
- provider debug-session metadata
- provider resource inspection and target health
- recent logs and metrics
- runner, systemd, disk, OOM, and collector status fields
- redaction notes and continuation commands

Missing provider observations become bundle findings instead of crashing the
command. The bundle never includes plaintext secrets.

## Audit And Authorization

`debug collect` currently runs in direct mode so both the service event and
audit record are durable even when `skiffd` is unavailable. Production debug is
high risk and requires approval context:

```bash
skiff authz explain \
  --action debug \
  --service payments-api \
  --env prod \
  --risk high \
  --approval-id approval_01J... \
  --format json
```

Skiff writes:

- `services/<service>/events/<ulid>.json` with `debug.bundle_collected`
- `audit/<yyyy-mm-dd>/<ulid>.json` with action `debug.collect`

The audit record includes actor, trace ID, target, risk, approval ID, bundle ID,
instance ID, and reason.

## Provider Path

The provider interface owns debug-session setup. On AWS the intended live path
is Systems Manager Session Manager for shell, command, and port-forward access,
so workload security groups do not need inbound SSH. Provider implementations
must return visible provider IDs and session IDs, and unsupported live debug
paths must fail explicitly instead of falling back to SSH.

Interactive commands run a read-only bundle preflight first and then start the
provider session:

```bash
skiff debug shell payments-api --instance i-abc123 --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff debug port-forward payments-api --instance i-abc123 --remote 8080 --local 18080 --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff debug command payments-api --instance i-abc123 --exec "systemctl status payments-api" --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```
