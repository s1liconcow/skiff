# Runbook: Failed Restore

Use this when a managed database restore saga fails before or after cutover.

## Triage

```bash
skiff ops inspect <saga-id> --direct --state <state-uri> --format json --trace-id <trace-id>
skiff ops events <saga-id> --direct --state <state-uri> --format json --trace-id <trace-id>
skiff doctor <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --fresh --format json --trace-id <trace-id>
```

## Before Cutover

If the failure happened before the secret pointer changed, production should
still point at the original database. Resume after fixing the provider issue:

```bash
skiff ops resume <saga-id> --direct --state <state-uri> --env <env> --provider aws --region <region> --format json --trace-id <trace-id>
```

## After Cutover

If the secret pointer changed, inspect the restore events before acting. Use the
documented database restore operation flow in
[../../operations/database-restore.md](../../operations/database-restore.md).

Capture:

- restore saga ID
- restored database provider ID
- previous and current secret version IDs
- smoke query results
- shadow service test results
- approval ID used for cutover
