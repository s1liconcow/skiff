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
from a digest-pinned OCI image through the runner lifecycle, writes operation,
resource, event, and audit objects, validates create-only release objects and
CAS-controlled service control, runs direct-mode status/events/doctor/ops
against RustFS, verifies the fetched release through the CLI, rolls to a second
release, starts a local `skiffd` backed by the same RustFS object state, then
starts a three-stage rolling canary in direct mode and monitors the resulting
service, saga, doctor, and event-stream views through `skiffd`.

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
`SKIFF_AWS_E2E_REGION`, and a unique `SKIFF_AWS_E2E_PREFIX`. Live apply is
further gated by `SKIFF_AWS_E2E_LIVE_APPLY=1` plus provider-shape inputs such
as `SKIFF_AWS_VPC_ID`, `SKIFF_AWS_SUBNET_IDS`, and `SKIFF_AWS_AMI_ID`.

See `docs/dev/e2e-matrix.md` for the full capability matrix, run modes, cleanup
expectations, and failure triage commands.
