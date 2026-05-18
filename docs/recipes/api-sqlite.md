# API server with local SQLite

This recipe creates a single API server that stores its SQLite database on a
durable member volume. Object storage remains Skiff's durable control plane;
the SQLite file is workload data and is protected by StatefulGroup fencing,
replacement, and snapshot operations.

Generate the starter:

```bash
skiff init stack api-sqlite orders --dir examples/stacks/api-sqlite
```

The generated spec is a `StatefulGroup`, not a managed database stack. That is
intentional: SQLite is a local single-writer database, so the workload runs as
one named member with one encrypted volume mounted at `/var/lib/skiff/sqlite`.
Do not raise replicas above one unless a future recipe explicitly changes the
writer and fencing model.

The recipe compiles into:

- one StatefulGroup member VM
- one encrypted durable block volume for the SQLite file
- stable member identity
- a recipe runtime for the API artifact, health check, metrics endpoint, and SQLite path
- an ordered update policy
- a snapshot policy for the member volume
- a fencing policy for replacement before the volume can move

Preview the stateful graph before deploying:

```bash
skiff validate examples/stacks/api-sqlite/skiff.yaml --format json
skiff stateful plan examples/stacks/api-sqlite/skiff.yaml --provider fake --region local --format json
skiff explain examples/stacks/api-sqlite/skiff.yaml --provider aws --format json
```

For AWS lowering, the visible cloud primitives include an EC2 stateful member,
EBS volume, EBS attachment, Route53 record when a DNS zone is configured, IAM
role, security group, launch template, target group, CloudWatch logs and
metrics, an EBS snapshot policy, and a fencing policy.

Apply only through StatefulGroup workflows so the durable control documents and
member controls are written before provider changes continue:

```bash
skiff stateful apply examples/stacks/api-sqlite/skiff.yaml --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Backups are snapshots of the member volume. Plan first, then create the
snapshot with direct object-state access:

```bash
skiff stateful backup plan orders-api --members 0 --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
skiff stateful snapshot orders-api --member 0 --backup-id backup_01J... --reason "pre-maintenance sqlite snapshot" --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Replacement is high-risk because the SQLite volume must not have two writers.
Use the explicit replacement saga, which fences the old member before moving the
volume:

```bash
skiff stateful replace-member orders-api --member 0 --reason "member failed health" --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Restore is not an API rollback. Use the stateful restore plan and approval gate
when data must move:

```bash
skiff stateful restore plan orders-api --member 0 --backup-id backup_01J... --restore-id restore_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```
