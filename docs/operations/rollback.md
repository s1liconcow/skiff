# Rollback

Rollback is an explicit operation that moves a service back to a known release.
It writes durable object state before provider actions and remains diagnosable
through operation and saga events.

## Command

```bash
skiff rollback payments-api \
  --to previous-stable \
  --operation-id op_01Jrollback \
  --saga-id saga_01Jrollback \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --format json
```

Expected output includes `ok: true`, `result.operation_id`,
`result.to_release`, `result.saga_id`, and recommended inspection commands.

## What Changes

Rollback writes:

- `services/<service>/operations/<op>/intent.json`
- `services/<service>/operations/<op>/control.json`
- append-only operation and saga events
- an audit record with actor, trace ID, target, risk, and summary
- a CAS update to `services/<service>/control.json`

The provider sees visible rollout IDs and target health checks. Skiff does not
delete release objects or hide the old failed operation.

## Diagnose Failure

```bash
skiff ops inspect op_01Jrollback --service payments-api --direct --state s3://skiff-state-prod --format json
skiff ops watch payments-api --operation op_01Jrollback --direct --state s3://skiff-state-prod --format json
skiff logs payments-api --since 20m --direct --state s3://skiff-state-prod --format json
```

## Recovery

If rollback fails before desired release changes, production remains on the
current stable release. If it fails after desired release changes, inspect target
health and either resume the operation or roll forward to another known-good
release with a new operation ID.

Do not overwrite release manifests. Release history is create-only.
