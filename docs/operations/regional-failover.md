# Regional Failover

Regional failover is an explicit saga for multi-region services and managed
databases. It is not an always-running controller. The plan must say when the
operation becomes partially irreversible because writes move to the promoted
region.

## Dry Run

```bash
skiff failover orders \
  --database orders-db \
  --from-region us-west-2 \
  --to-region us-east-1 \
  --dry-run \
  --format json
```

Expected output shows the saga graph, risk level, reversibility, and approval
step before any object-state mutation.

## Execute

```bash
skiff failover orders \
  --database orders-db \
  --from-region us-west-2 \
  --to-region us-east-1 \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --approval-id approval_01J... \
  --format json
```

## What Changes

The saga records immutable intent and graph objects, a CAS-controlled saga
control document, step results, events, and audit records. Provider-visible
objects include regional service capacity, global traffic policy IDs, database
writer/replica IDs, secret version IDs, and health or lag checks.

Typical graph:

```text
preflight
verify-secondary-capacity
verify-replica-lag
approve-failover
freeze-writes, when configured
promote-secondary-database
update-writer-secret
shift-traffic-10
verify-service
shift-traffic-100
```

## Diagnose Failure

```bash
skiff ops inspect saga_01Jfailover --direct --state s3://skiff-state-prod --format json
skiff ops events saga_01Jfailover --direct --state s3://skiff-state-prod --format json
skiff doctor orders --direct --state s3://skiff-state-prod --fresh --format json
```

## Recovery

Before the writer secret changes, compensation can usually leave production on
the original region. After the writer secret changes and new writes land in the
secondary region, failback is a new high-risk plan, not rollback. Run a separate
failover or repair saga with approval and explicit data-divergence checks.
