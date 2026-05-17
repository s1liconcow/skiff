# Skiff E2E Tests

The local suite is mandatory and runs in normal `go test ./...`. It includes:

- `TestRunnerServesSignedReleaseFixture`, which proves the runner can fetch,
  verify, prepare, start, health-check, and report a signed release.
- `TestLocalCLIEndToEndCapabilityMatrix`, which drives the real CLI against
  file-backed object state and the fake provider. It covers `validate`,
  `compile`, `plan`, `explain`, release verification, `deploy`, `rollout
  watch`, `status`, `events`, `logs`, `metrics`, `doctor`, `canary`,
  `debug collect`, `cost explain`, `rollback`, `drift`, and `plugin validate`.

```bash
make e2e-local
```

Set `SKIFF_E2E_REPORT_DIR` to collect JSON reports containing trace IDs,
operation IDs, saga IDs, provider IDs, object paths, facts, cleanup status, and
recommended next commands.

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
added to CI. The current AWS test proves the explicit gates plus plan/explain
lowering and records the live-apply gap until real AWS apply/discovery adapters
are linked. Use `SKIFF_AWS_E2E=1`, `SKIFF_AWS_E2E_STATE`,
`SKIFF_AWS_E2E_REGION`, and a unique `SKIFF_AWS_E2E_PREFIX`.

See `docs/dev/e2e-matrix.md` for the full capability matrix, run modes, cleanup
expectations, and failure triage commands.
