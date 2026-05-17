# Runbook: Canary Failure

Use this when a canary deploy fails a health, target-health, or metric gate.

## Triage

```bash
skiff saga inspect <saga-id> --direct --state <state-uri> --format json --trace-id <trace-id>
skiff events --scope saga --saga <saga-id> --direct --state <state-uri> --format json --trace-id <trace-id>
skiff doctor <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --fresh --format json --trace-id <trace-id>
skiff logs <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --since 20m --format json --trace-id <trace-id>
skiff metrics <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --since 20m --format json --trace-id <trace-id>
```

## Recovery

If the canary saga is waiting or interrupted:

```bash
skiff saga resume <saga-id> --direct --state <state-uri> --env <env> --provider aws --region <region> --format json --trace-id <trace-id>
```

If the canary release is bad and a stable release exists:

```bash
skiff rollback <service> --to previous-stable --direct --state <state-uri> --env <env> --provider aws --region <region> --approval-id <approval-id> --format json --trace-id <trace-id>
```

## Notes

Do not describe saga compensation as rollback unless it restores the prior
release. Record whether the response is rollback, compensation, or roll-forward.
