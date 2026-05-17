# Secret rotation

Secret and credential rotation is an explicit saga. Skiff never writes plaintext
secret values to object state, events, release manifests, or audit records. The
saga works with provider secret references and version IDs only.

Create a plan first:

```bash
skiff rotate secret secret://managed-database/orders-db/connection-url \
  --consumers orders-api,orders-worker \
  --database orders-db \
  --dry-run \
  --format json
```

Execute production rotation only through direct object-state access with approval
context:

```bash
skiff --direct --state s3://skiff-state-prod rotate secret secret://managed-database/orders-db/connection-url \
  --consumers orders-api,orders-worker \
  --canary-consumer orders-api \
  --database orders-db \
  --disable-after 24h \
  --approval-id approval_01J... \
  --format json
```

The rotation graph is:

```text
preflight
create-version
validate-version
update-canary-pointer
canary-consumer
approve-promotion
promote-secret-pointer
roll-consumers
schedule-disable-old
```

If the canary consumer fails, the saga compensates by restoring the previous
secret pointer before stopping. The old credential is not deleted during
promotion. `schedule-disable-old` records a delayed provider operation so
operators have an explicit window to inspect stale consumers.

For managed databases, pass `--database <name>`. Providers can use that hook to
rotate a database credential version, validate it, canary consumers, and only
then promote the secret pointer. Mature applications should prefer runtime
secret fetch or short-lived credential refresh over one-time environment
variable injection, because long-lived environment variables make stale consumer
detection and rotation slower.

Doctor flags stale or failed consumers from recent events:

```bash
skiff doctor orders-api --direct --state s3://skiff-state-prod --format json
```
