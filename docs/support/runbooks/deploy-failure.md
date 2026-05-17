# Runbook: Deploy Failure

Use this when `skiff deploy` returns a provider, release, policy, or rollout
error.

## Triage

```bash
skiff doctor <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --fresh --format json --trace-id <trace-id>
skiff events --scope operation --service <service> --operation <op-id> --direct --state <state-uri> --format json --trace-id <trace-id>
skiff ops inspect <op-id> --service <service> --direct --state <state-uri> --format json --trace-id <trace-id>
```

## Recovery

If the operation has a stored provider operation ID and is resumable:

```bash
skiff ops resume <op-id> --service <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --format json --trace-id <trace-id>
```

If a stable release exists and the rollout is unhealthy:

```bash
skiff rollback <service> --to previous-stable --direct --state <state-uri> --env <env> --provider aws --region <region> --approval-id <approval-id> --format json --trace-id <trace-id>
```

## Evidence To Capture

- trace ID
- operation ID
- release ID
- provider rollout ID
- release and operation object paths
- doctor findings and recommended actions
