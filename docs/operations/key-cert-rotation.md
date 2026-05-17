# Key And Certificate Rotation

Key and certificate rotation are explicit sagas. The first implementation is
plan-first: it creates durable saga intent and graph objects, records audit
events, and makes blast radius visible before any provider-specific security
steps run.

## Key Rotation

```bash
skiff rotate key alias/skiff/prod/state \
  --consumers payments-api,orders-api \
  --canary-consumer payments-api \
  --material-refs secret://payments/api-token,secret://orders/db-url \
  --disable-after 240h \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --approval-id approval_01J... \
  --format json
```

Dry-run first:

```bash
skiff rotate key alias/skiff/prod/state --consumers payments-api --material-refs secret://payments/api-token --dry-run --format json
```

The graph creates a candidate key, re-encrypts declared material references,
validates the candidate, canaries one consumer, waits for manual promotion
approval, promotes the alias, verifies key policy, rolls consumers, and
schedules old-key disable.

Old-key deletion is not part of the automatic flow. It requires a later,
separate critical approval after the disable window and after operators verify
that no consumer still depends on the old key.

## Certificate Rotation

```bash
skiff rotate cert payments-api-mtls \
  --certificate-ref aws-acm://us-west-2/certificate/payments-api \
  --trust-store-ref aws-acm-pca://us-west-2/private-ca/root \
  --consumers payments-api,orders-api \
  --canary-consumer payments-api \
  --retire-after 240h \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --approval-id approval_01J... \
  --format json
```

The graph issues a candidate certificate, validates it, updates only the canary
consumer reference, verifies from the consumer point of view, waits for manual
promotion approval, promotes the certificate reference, rolls consumers, verifies
consumer trust, and schedules old-certificate retirement.

Old-certificate revocation or deletion is separate from rotation and requires
explicit approval.

## Output

JSON output includes:

- `result.saga_id` and `result.operation_id`
- `result.blast_radius`
- `result.reversibility`
- `result.plan` in dry-run mode
- current status and next action when the saga is written

Production key and certificate rotation use the `rotate` authorization action
with high risk, so approval context is required outside dry-run mode.

## Doctor Signals

`skiff doctor` flags recent event evidence for:

- `CERTIFICATE_EXPIRING`
- `CERTIFICATE_EXPIRED`
- `KEY_POLICY_MISMATCH`

Use those findings to decide whether to start a rotation saga or inspect recent
events first.
