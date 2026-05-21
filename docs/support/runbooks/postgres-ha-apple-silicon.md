# Runbook: Apple Silicon Postgres HA Stateful Package

Use this runbook for the Apple Silicon operator CUJ: build the first-party
Postgres HA image, deploy the `postgres-ha` StatefulGroup with Skiff, prove
stateful SQL reads/writes against the mounted volume, and run the
`primary-switchover-update` package operation.

The local image tag is not magic. It is built from this checkout:

```bash
make demo-apple-postgres-ha-images
```

That builds `localhost/postgres-ha:apple` from
`examples/stateful/postgres-ha/Dockerfile`.

The deployable StatefulGroup spec for the package image is:

```bash
examples/stateful/postgres-ha/skiff.yaml
```

For the full automated Apple validation, run:

```bash
make demo-apple-postgres-ha
```

The demo target builds `localhost/postgres-ha:apple` if needed, starts RustFS,
applies the live Apple StatefulGroup, verifies a real SQL write/read through
the deployed Postgres member, locks `packages/postgres-ha`, runs
`primary-switchover-update`, verifies unsafe replica lag blocks before
mutation, and writes an evidence report under
`.skiff-demo-reports/apple-postgres-ha`.

The generic AWS/Apple API plus database runbook is:

[`api-postgres-ha-read-write.md`](api-postgres-ha-read-write.md)

For the API plus Postgres HA Apple runbook, build both local images with:

```bash
make demo-apple-api-postgres-ha
```

That builds both `localhost/orders-rpc:apple` and
`localhost/postgres-ha:apple`, starts RustFS, locks the actual
`skiff.dev/postgres-ha` package, deploys the orders API stack, verifies
database-backed read/write operations, and confirms the package operation list
is exposed for the deployed StatefulGroup. Use `metadata.env: local` for those
local Apple tags; production specs still require digest-pinned OCI refs.
