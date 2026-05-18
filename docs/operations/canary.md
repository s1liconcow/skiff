# Canary Deployments

A canary deployment is a typed saga that publishes a signed release, shifts
traffic in explicit stages, checks health or metrics, and records every decision
in object state.

## Command

```bash
skiff deploy examples/service/http-hello/skiff.yaml \
  --canary \
  --canary-stages 5,25,100 \
  --canary-bake 5m \
  --canary-metric request_count \
  --canary-threshold 1 \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --release-id rel_01J... \
  --operation-id op_01J... \
  --signing-seed-base64 "$SKIFF_RELEASE_SIGNING_SEED" \
  --approval-id approval_01J... \
  --format json
```

Expected output includes `ok: true`, `saga_id`, `operation_id`, `release_id`,
`current_steps`, and the current canary `stage`.

## What Changes

Skiff creates immutable release/runtime manifests, operation intent and control
documents, saga intent and graph documents, append-only events, and audit
records. The provider sees visible cloud primitives such as ASG instance refresh
IDs, target groups, listener rules, log groups, and metrics.

## Diagnose Failure

```bash
skiff ops inspect saga_01J... --direct --state s3://skiff-state-prod --format json
skiff ops events saga_01J... --direct --state s3://skiff-state-prod --format json
skiff doctor http-hello --direct --state s3://skiff-state-prod --fresh --format json
```

## Recovery

If the canary stops before 100%, inspect the failed step and either resume after
fixing the provider condition or compensate the saga. If the new release has
become stable and must be undone, use rollback as a separate operation:

```bash
skiff rollback http-hello --to previous-stable --direct --state s3://skiff-state-prod --format json
```

Compensation may reduce impact without restoring the previous stable service.
Only call it rollback when service control and traffic actually return to the
prior release.
