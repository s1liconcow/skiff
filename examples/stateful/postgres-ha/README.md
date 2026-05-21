# postgres-ha Apple Demo Image

This directory builds the local Apple Silicon image used by the
`skiff.dev/postgres-ha` package demo:

```bash
make demo-apple-postgres-ha-images
```

The image runs a real Postgres 16 server with its data directory on the Skiff
StatefulGroup volume and exposes the package admin API on port `8008`.
The deployable StatefulGroup spec is [`skiff.yaml`](skiff.yaml); the image ref
in that spec is the tag built by the make target above. The spec uses
`metadata.env: local` because the local Apple image tag is mutable; production
specs still use digest-pinned OCI refs.

Useful endpoints:

- `GET /healthz`
- `GET /admin/state`
- `POST /admin/promote`
- `POST /admin/stepdown`
- `POST /admin/catch-up`
- `POST /demo/write`
- `GET /demo/read`

`/demo/write` and `/demo/read` execute real SQL against the local Postgres
member so the Apple demo can prove stateful reads/writes against the mounted
volume. The package operation endpoints are intentionally compatible with the
first-party `postgres-ha-plugin` package steps.
