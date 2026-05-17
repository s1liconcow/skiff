# Skiff E2E Tests

The local runner fixture test is mandatory and runs in normal `go test ./...`.
The stateless service release-gate flow also runs in normal `go test ./...`
with a high-fidelity fake AWS provider. It exercises `skiff validate`, `plan`,
`deploy`, `rollout watch`, `status`, `logs`, `metrics`, `doctor`, and
`rollback` against file-backed object state without creating cloud resources.

The Apple container/RustFS/Caddy rollout test is optional because it starts
local Linux VMs and pulls OCI images. It exercises RustFS as the S3-compatible
object store, publishes signed release objects into that store, deploys Caddy
from a digest-pinned OCI image through the runner lifecycle, then CAS-updates
service control and rolls to a second release.

```bash
make e2e-apple-container
```

`SKIFF_E2E_CADDY_IMAGE` defaults to `docker.io/library/caddy:2-alpine`; when
given as a tag, the test resolves it to a registry digest before writing the
release manifest. `SKIFF_E2E_CADDY_NEXT_IMAGE` may be omitted to roll to a new
Skiff release ID with the same Caddy image. `SKIFF_E2E_RUSTFS_IMAGE` defaults
to `docker.io/rustfs/rustfs:latest`.

Real AWS stateless-service smoke coverage must be explicitly gated before it is
added to CI. Use `SKIFF_AWS_E2E=1`, a unique service/env prefix, digest-pinned
test artifacts, aggressive cleanup by Skiff tags, and a non-default workflow so
normal PRs never create cloud resources.
