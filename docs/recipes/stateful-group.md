# Stateful Group

Managed databases remain the default recommendation for production state.
`StatefulGroup` is for deliberate cases where a workload needs named VM members,
single-writer durable volumes, stable DNS identity, and recipe-specific recovery.
Use managed state for ordinary relational databases, queues, and caches when the
cloud service gives you backups, failover, upgrades, and support boundaries.
Use StatefulGroup when the workload process itself is the stateful system and
the operator needs to see and control every member, volume, DNS record, provider
operation, and recovery saga.

It is not a Kubernetes StatefulSet clone. A member is explicitly:

```text
member ordinal + VM instance + durable volume + stable DNS + generation
```

## Spec Shape

```yaml
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: postgres
  env: prod
stateful:
  replicas: 1
  members:
    - ordinal: 0
      zone: us-west-2a
      dnsName: postgres-0.internal.example.com
  volume:
    size: 100Gi
    type: gp3
    mountPath: /var/lib/postgresql
    encrypted: true
  recipe:
    name: postgres-single
  update:
    strategy: ordered
```

Examples:

- `examples/stateful/jetstream/skiff.yaml` models a three-member NATS JetStream
  cluster with digest-pinned OCI artifact, stable DNS, snapshot policy, and
  ordered updates.
- `examples/stateful/single-member/skiff.yaml` models a one-member durable
  JetStream process for replacement, backup, and restore tutorials.

Validate, plan, and explain the example without writing object state:

```bash
skiff validate examples/stateful/jetstream/skiff.yaml --format json
skiff stateful plan examples/stateful/jetstream/skiff.yaml --provider fake --region local --format json
skiff explain examples/stateful/jetstream/skiff.yaml --provider aws --format json
```

## Durable Control

StatefulGroup object state is explicit. Immutable saga and operation documents
are create-only. Control documents are mutable only through CAS.

```text
stateful/<group>/control.json
stateful/<group>/members/<member>/control.json
stateful/<group>/backups/<backup>/record.json
stateful/<group>/restores/<restore>/record.json

services/<group>/operations/<op>/intent.json
services/<group>/operations/<op>/control.json
services/<group>/operations/<op>/events/<ulid>.json

sagas/<saga>/intent.json
sagas/<saga>/graph.json
sagas/<saga>/control.json
sagas/<saga>/events/<ulid>.json

audit/<yyyy-mm-dd>/<ulid>.json
```

The group control stores desired replicas, member summaries, active operation,
lease, and trace ID. Each member control stores instance ID, volume ID, DNS
name, generation, phase, lease, provider operation IDs, release/runtime manifest
keys, and replacement progress. The lease lives inside the same control
document it protects. There are no separate lock objects.

`skiffd` can rebuild views from these documents. Direct CLI mode reads the same
object state when `skiffd` is unavailable.

## Replacement Flow

Replacement is conservative because duplicate writers are worse than downtime:

```text
acquire member lease
record replacement intent in member control
fence old VM
record provider fencing operation
detach volume
record detach operation
launch replacement in the same zone
record replacement instance ID
attach volume
update DNS
run recipe recovery
verify recipe health
publish new instance ID and generation
release member lease
```

Every provider operation ID is stored before the next provider step starts, so
the replacement can resume after interruption. Stale runners must compare their
member generation with the control document and refuse to write as an old
generation.

Plan the replacement from direct object state:

```bash
skiff stateful doctor ledger-stream --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Start the replacement only after the high-risk action is approved:

```bash
skiff stateful replace-member ledger-stream \
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

## Recipe Hooks

Recipes provide application-specific behavior:

- start
- stop
- health
- backup
- restore
- role detection

The platform owns fencing, volumes, stable identity, and control documents.
Recipes decide how a database or other stateful workload recovers safely after
the VM and volume mechanics are complete.

## Release Update Versus Replacement

Stateful release updates and member replacements are different operator actions.

`skiff deploy <StatefulGroup spec>` updates software in place on existing named
members. It keeps the VM instance, durable volume, DNS identity, and member
generation stable while writing new release/runtime manifest keys into member
control documents.

`skiff stateful replace-member <group> --member <n>` replaces the VM that owns a
member. It must fence the old writer, detach the existing volume, launch a new
VM, attach the volume, update DNS, and publish a new member generation.

If an operation would update the release and replace the VM, model it as two
explicit operations so the volume-moving risk remains visible.

## Ordered Release Update

Ordered release updates are explicit sagas, not hidden reconciliation. A member
is drained, updated in place, health-checked, and recorded before the next member
starts. The saga records step results so it can resume after an interruption.
It does not detach volumes, launch replacement VMs, or change member generation.

```bash
skiff stateful update-release ledger-stream \
  --release-id rel_2026_05_18_ledger \
  --members 0 \
  --max-unavailable 1 \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --format json
```

Resume from object state:

```bash
skiff stateful resume saga_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

## Backup And Restore

Backups create immutable backup records and append saga events. Restores are
high-risk and should stop at an approval gate before attaching or recovering
state from a snapshot.

```bash
skiff stateful backup plan ledger-stream \
  --members 0 \
  --backup-id backup_01J... \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --format json
```

```bash
skiff stateful snapshot ledger-stream \
  --member 0 \
  --backup-id backup_01J... \
  --reason "pre-maintenance snapshot" \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --format json
```

```bash
skiff stateful restore plan ledger-stream \
  --member 0 \
  --backup-id backup_01J... \
  --restore-id restore_01J... \
  --format json
```

```bash
skiff stateful restore apply ledger-stream \
  --member 0 \
  --backup-id backup_01J... \
  --restore-id restore_01J... \
  --approval-id approval_01J... \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --format json
```

Do not describe restore compensation as rollback unless it truly restores the
prior state. A compensation may only reduce harm, such as detaching a failed
restore volume or marking a snapshot for review.

## Status, Doctor, Logs, And Metrics

Use status and doctor first. They keep facts, hypotheses, and mutating actions
separate for agents and operators.

```bash
skiff stateful status ledger-stream --fresh --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff stateful doctor ledger-stream --fresh --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff stateful logs ledger-stream --member 0 --since 20m --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff stateful metrics ledger-stream --member 0 --metric CPUUtilization --since 20m --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

## Limits

Skiff does not automate stateful scale-down in this foundation. Deleting or
detaching durable volumes remains a separate explicit operation with snapshots,
retention checks, approval, and audit records.
