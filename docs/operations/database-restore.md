# Database Restore

Database restore is an explicit saga, not background reconciliation. Object
storage records the intent, graph, control document, events, and step result
artifacts before the workflow can continue after interruption.

The safe default is restore-to-new-instance-then-cutover:

```text
preflight
verify restore point
snapshot current database
restore new database
wait available
smoke query
shadow service test, when a service is attached
approval before cutover
update secret pointer
roll service
verify service
```

Start with a dry run:

```bash
skiff database restore orders-db \
  --to 2026-05-17T02:00:00Z \
  --mode new-db-cutover \
  --secret-ref secret://managed-database/orders-db/connection-url \
  --service orders-api \
  --direct --state s3://skiff-state-prod \
  --env prod --provider aws --region us-west-2 \
  --dry-run --format json
```

Execute the saga with production approval context:

```bash
skiff database restore orders-db \
  --to 2026-05-17T02:00:00Z \
  --mode new-db-cutover \
  --secret-ref secret://managed-database/orders-db/connection-url \
  --service orders-api \
  --direct --state s3://skiff-state-prod \
  --env prod --provider aws --region us-west-2 \
  --approval-id approval_01J... \
  --format json
```

The saga stops at `approve-cutover` after the restored database checks pass.
Approve only after inspecting the saga and confirming the restored database is
the desired target:

```bash
skiff ops inspect saga_01J... --direct --state s3://skiff-state-prod --format json
skiff ops approve saga_01J... --step approve-cutover --reason "restore checks passed" --direct --state s3://skiff-state-prod --format json
skiff ops resume saga_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Failure before cutover leaves production unchanged. The restore step
compensates by retiring or marking the restored database for cleanup. If failure
happens after the secret pointer update, compensation attempts to restore the
previous secret version when the provider returns enough version information.
Do not describe this as rollback unless the previous application state has
actually been restored.
