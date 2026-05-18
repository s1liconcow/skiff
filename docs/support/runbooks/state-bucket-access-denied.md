# Runbook: State Bucket Access Denied

Use this when commands report S3, KMS, IAM, or object-state access denied.

## Triage

```bash
skiff doctor <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --fresh --format json --trace-id <trace-id>
skiff ops events <service> --direct --state <state-uri> --format json --trace-id <trace-id>
skiff config show --direct --state <state-uri> --env <env> --provider aws --region <region> --format json --trace-id <trace-id>
```

## Checks

- Confirm the state bucket URI, region, and KMS key match the environment.
- Confirm the caller can read service control, release manifests, operation
  control, saga control, events, and audit prefixes.
- Confirm deployer or `skiffd` write permissions are scoped to CAS control docs
  and create-only history objects.
- Confirm runner permissions are read-scoped to the service it runs.

## Recovery

Fix IAM or KMS policy first. Do not copy object state into another database or
create lock files.

After access is restored:

```bash
skiff status <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --fresh --format json --trace-id <trace-id>
skiff ops resume <op-id> --service <service> --direct --state <state-uri> --env <env> --provider aws --region <region> --format json --trace-id <trace-id>
```
