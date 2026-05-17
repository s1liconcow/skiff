# Runbook: skiffd Unavailable

`skiffd` is a stateless facade. Do not recover it by creating another durable
database. Use direct CLI mode against object storage and cloud provider APIs.

## Triage

```bash
skiff --direct --state <state-uri> --env <env> --provider aws --region <region> status <service> --fresh --format json --trace-id <trace-id>
skiff --direct --state <state-uri> events --scope service --service <service> --format json --trace-id <trace-id>
skiff --direct --state <state-uri> doctor <service> --env <env> --provider aws --region <region> --fresh --format json --trace-id <trace-id>
```

## Recovery Actions

Resume an interrupted operation:

```bash
skiff --direct --state <state-uri> --env <env> --provider aws --region <region> ops resume <op-id> --service <service> --format json --trace-id <trace-id>
```

Resume a saga:

```bash
skiff --direct --state <state-uri> --env <env> --provider aws --region <region> saga resume <saga-id> --format json --trace-id <trace-id>
```

Roll back a service:

```bash
skiff --direct --state <state-uri> --env <env> --provider aws --region <region> rollback <service> --to previous-stable --approval-id <approval-id> --format json --trace-id <trace-id>
```

## Restore skiffd Later

After service risk is controlled, restart `skiffd`. It should rebuild in-memory
views from object storage. Verify freshness:

```bash
skiff status <service> --api --api-url <skiffd-url> --fresh --format json --trace-id <trace-id>
```

Related background: [../../operations/recover-with-direct-mode.md](../../operations/recover-with-direct-mode.md).
