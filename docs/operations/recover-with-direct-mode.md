# Recover With Direct Mode

`skiffd` is the normal API and TUI facade, but it is not Skiff's durable state
store. When `skiffd` is unavailable, use the CLI directly against object
storage.

Inspect service state:

```bash
skiff --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 status payments-api --format json
```

Inspect active sagas and events:

```bash
skiff --direct --state s3://skiff-state-prod events --scope saga --saga saga_01J... --format json
skiff --direct --state s3://skiff-state-prod saga inspect saga_01J... --format json
```

Resume a waiting or interrupted saga:

```bash
skiff --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 saga resume saga_01J... --format json
```

Roll back a service without `skiffd`:

```bash
skiff --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 rollback payments-api --to previous-stable --approval-id approval_01J... --format json
```

Do not create a replacement database or queue to recover the control plane.
Durable state is object storage: immutable release/operation/saga objects,
append-only events and audits, and CAS control documents.
