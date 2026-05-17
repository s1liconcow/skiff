# Runbook: Runner Failure

Use this when runner state is not `Serving`, health checks fail, or a workload
VM is suspected unhealthy.

## Triage

```bash
skiff status <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --fresh --format json --trace-id <trace-id>
skiff doctor <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --fresh --format json --trace-id <trace-id>
skiff debug collect <service> --instance <provider-instance-id> --direct --state <state-uri> --env <env> --provider aws --region <region> --approval-id <approval-id> --format json --trace-id <trace-id>
skiff logs <service> --instance <provider-instance-id> --direct --state <state-uri> --env <env> --provider aws --region <region> --since 20m --format json --trace-id <trace-id>
```

## Recovery

If the workload is failing after a new release:

```bash
skiff rollback <service> --to previous-stable --direct --state <state-uri> --env <env> --provider aws --region <region> --approval-id <approval-id> --format json --trace-id <trace-id>
```

If provider capacity is failing, inspect the visible cloud primitive IDs in the
doctor facts before changing capacity.

## Evidence To Capture

- runner state and health
- instance/provider ID
- release digest
- target group health
- debug bundle ID
- operation or saga ID
