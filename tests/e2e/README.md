# Skiff E2E Tests

The local runner fixture test is mandatory and runs in normal `go test ./...`.

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

Real AWS smoke coverage must be explicitly gated before it is added to CI. Use
environment variables such as `SKIFF_AWS_E2E=1`, isolated resource prefixes,
and Skiff tags so normal PRs never create cloud resources.
