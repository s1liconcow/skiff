# Runbook: Lease Held

Use this when a mutating command returns `LEASE_HELD`.

## Triage

```bash
skiff status <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --fresh --format json --trace-id <trace-id>
skiff ops inspect <op-id> --service <service> --direct --state <state-uri> --format json --trace-id <trace-id>
skiff events --scope service --service <service> --direct --state <state-uri> --format json --trace-id <trace-id>
```

## Decision

- If the lease is active, wait or contact the owner in the control document.
- If the lease is expired, resume or retry the operation with direct mode.
- If repeated commands contend, stop automation and appoint one owner.

## Recovery

```bash
skiff ops resume <op-id> --service <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --format json --trace-id <trace-id>
```

Do not delete a lock object. Skiff leases live inside the control document they
protect, and the ETag is the fencing token.
