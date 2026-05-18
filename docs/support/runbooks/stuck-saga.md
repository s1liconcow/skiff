# Runbook: Stuck Saga

Use this when a saga is waiting, interrupted, or no longer making progress.

## Triage

```bash
skiff ops inspect <saga-id> --direct --state <state-uri> --format json --trace-id <trace-id>
skiff ops events <saga-id> --direct --state <state-uri> --format json --trace-id <trace-id>
skiff doctor <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --fresh --format json --trace-id <trace-id>
```

## Recovery

Resume the saga if the current step is idempotent or waiting for a provider
operation that has already been recorded:

```bash
skiff ops resume <saga-id> --direct --state <state-uri> --env <env> --provider aws --region <region> --format json --trace-id <trace-id>
```

If the saga is waiting for approval, capture the approval ID before resuming:

```bash
skiff ops resume <saga-id> --direct --state <state-uri> --env <env> --provider aws --region <region> --approval-id <approval-id> --format json --trace-id <trace-id>
```

## Evidence To Capture

- saga ID and graph path
- current step
- provider operation IDs stored in step results
- approval ID, if any
- compensation status, if any
