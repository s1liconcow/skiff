# Compatibility

Skiff components expose build information through `version` endpoints and CLI
output. Production builds should use semantic versions such as `1.2.3`; `dev`
versions are accepted for local development and test fixtures.

## GA Compatibility Matrix

| Component | Compatibility rule | GA support claim |
|---|---|---|
| CLI -> skiffd | CLI calls `skiffd` version endpoints and warns when the server is older than the supported semantic version range. | Same minor version recommended. Direct mode remains available if `skiffd` is unavailable. |
| CLI -> object state | Direct mode reads object-state schemas directly and uses centralized path helpers. | Supported for recovery and diagnosis. |
| skiffd -> object state | `skiffd` rebuilds in-memory views from object storage and may serve fresh reads from durable objects. | No durable database dependency. |
| skiff-runner -> release manifest | `min_runner_version` must be empty, `dev`, or less than or equal to the runner version. | Runner refuses unsupported releases. |
| skiff-runner -> schema | release and runtime manifest `schema_version` must equal `skiff.state/v1`. | Unsupported schema versions fail closed. |
| AWS provider -> IR | AWS lowering keeps provider-specific SDK types behind `internal/provider/aws`. | AWS is the first supported provider after conformance and gated e2e. |
| Future providers -> IR | Providers must pass `tests/conformance/provider` before support is claimed. | Not GA until conformance and docs exist. |
| Plugins -> host | plugin manifests declare the plugin API version and are validated before hooks run. | Plugin API is supported for validated plugins. |
| Spec schema -> compiler | unknown fields are rejected unless an explicit compatibility option allows them. | `skiff.state/v1` and current public spec only. |

## Upgrade Rules

- Upgrade `skiffd` before depending on new API-only behavior.
- Upgrade `skiff-runner` before publishing releases that require a newer
  `min_runner_version`.
- Keep direct-mode recovery commands available during component upgrades.
- Do not change object-state schema compatibility without a migration note in
  the release notes.
- Providers or plugins that compile but do not pass conformance are experimental.

## Verification Commands

```bash
skiff version --format json
skiffd version --format json
skiff-runner version --format json
go test ./tests/conformance/provider ./tests/conformance/plugin
```

For release readiness, also run:

```bash
make readiness
make e2e-local
```
