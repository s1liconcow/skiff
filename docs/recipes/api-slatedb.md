# API plus SlateDB object store

This recipe creates one API service and one workload object store for SlateDB data. Skiff object storage remains the durable control plane; the SlateDB bucket is separate workload data and is represented as an explicit cloud primitive with scoped IAM access.

Generate the starter:

```bash
skiff init stack api-slatedb orders --dir examples/stacks/api-slatedb --object-store-uri s3://orders-slatedb-prod/slatedb/orders
```

The generated app is a runnable Python API server. Its `Dockerfile` installs
the SlateDB package, and `app.py` opens the bound object store with
`ObjectStore.resolve(SLATEDB_URI)`, builds a database with `DbBuilder`, and
uses async `put` and `get` calls on requests and health checks. The default
container env uses `memory:///` so local smoke tests can start without cloud
credentials; Skiff overrides `SLATEDB_URI` from the stack binding at deploy
time.

The stack binding:

```yaml
bindings:
  - from: api
    to: data
    as: SLATEDB_URI
```

compiles into:

- an API service running one workload replica per VM by default
- a versioned, encrypted object-store bucket for SlateDB table data
- `SLATEDB_URI` in the runtime manifest, pointing at the configured bucket prefix
- a runtime command that starts `/app/app.py`
- scoped workload IAM permissions for listing and reading/writing only the bound object-store prefix
- logs, metrics, target group, launch template, IAM role, and ASG resources for the API

SlateDB is embedded in the API process. There is no managed database endpoint, no database password, and no hidden Skiff database. The API is responsible for opening SlateDB at `SLATEDB_URI`; Skiff makes the object-store dependency visible, grants least-privilege access, and keeps deployment state in Skiff's normal object-state documents.

Build the example artifact before publishing a digest-pinned release:

```bash
docker build -t registry.example.com/orders-api:dev examples/stacks/api-slatedb
```

Preview cloud primitives before deploying:

```bash
skiff explain examples/stacks/api-slatedb/skiff.yaml --provider aws --region us-west-2 --state s3://skiff-state-prod
skiff plan examples/stacks/api-slatedb/skiff.yaml --provider aws --region us-west-2 --state s3://skiff-state-prod --format json
```

The plan should show visible cloud resources, including an S3 bucket, EC2 security group, launch template, Auto Scaling Group, target group, CloudWatch logging/metrics, and the workload IAM role with object-store access.

Expected deployment output includes an operation ID, release ID, and provider resource IDs. The durable Skiff state written by deploy includes immutable release and runtime manifests, service operation intent/control documents, service control, resource records, events, and audit records. SlateDB data stays in the workload object store named by `SLATEDB_URI`.

Diagnose failures through direct mode:

```bash
skiff doctor orders-api --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --fresh --format json
skiff logs orders-api --since 20m --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Rolling back the API release changes the API code and runtime manifest. It does not restore SlateDB data. Data repair or point-in-time recovery must be modeled as an explicit operation or saga so the actor, trace ID, target bucket, risk, and reversibility are auditable.
