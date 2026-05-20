# Stateful Member Replacement

Use this when one StatefulGroup member is unhealthy, fenced, or lost. The goal
is to replace exactly one member while preserving durable object state, volume
identity, audit history, and direct recovery.

## First Checks

Collect status and doctor output directly from object storage:

```bash
skiff stateful status ledger-stream --fresh --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff stateful doctor ledger-stream --fresh --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Record these IDs before mutating anything:

- trace ID
- group name
- member ordinal
- member generation
- instance provider ID
- volume provider ID
- active operation ID
- active saga ID

## Snapshot Before Replacement

If the member volume is reachable and policy allows it, create a snapshot before
replacement. This is mutating and compensatable, not a rollback.

```bash
skiff stateful snapshot ledger-stream \
  --member 0 \
  --backup-id backup_01J... \
  --reason "pre-replacement safety snapshot" \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --format json \
  --trace-id tr_01J...
```

## Replace The Member

Replacement is high risk because duplicate writers are worse than downtime. The
saga must persist fencing, detach, launch, attach, DNS, recipe recovery, and
health step results before moving forward.

```bash
skiff ops run ledger-stream replace-member \
  --member 0 \
  --reason "member failed recipe health" \
  --approval-id approval_01J... \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --format json \
  --trace-id tr_01J...
```

If the command is interrupted, resume from object state:

```bash
skiff ops resume saga_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Watch immutable saga events:

```bash
skiff ops watch saga_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

## Verify

```bash
skiff stateful inspect ledger-stream --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff stateful logs ledger-stream --member 0 --since 20m --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff stateful metrics ledger-stream --member 0 --since 20m --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Expected facts after success:

- member generation increased
- old instance is fenced or terminated according to provider result
- replacement instance ID is recorded in member control
- volume ID is unchanged unless an explicit restore path was used
- DNS points at the replacement member
- recipe health is nominal
- audit events include actor, trace ID, target, operation ID, saga ID, risk, and summary

Do not delete old volumes or snapshots from this runbook. Use a separate GC plan
with retention checks and explicit approval.
