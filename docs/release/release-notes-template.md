# Release Notes Template

Use this template for GA and post-GA releases. Keep limitations explicit.

## Summary

- Version:
- Commit:
- Release date:
- Release owner:
- Support owner:

## What Changed

- Added:
- Changed:
- Fixed:
- Removed:

## Required Upgrade Notes

- CLI:
- `skiffd`:
- `skiff-runner`:
- State schema:
- Plugin API:

## Compatibility

Link to [../compatibility.md](../compatibility.md) and list any release-specific
exceptions.

| Component | Supported versions | Notes |
|---|---|---|
| CLI |  |  |
| skiffd |  |  |
| skiff-runner |  |  |
| AWS provider |  |  |
| Spec schema |  |  |
| Plugin API |  |  |

## Known Limitations

- Skiff is not a general Kubernetes replacement for arbitrary workloads.
- One VM runs one workload replica by default; multi-service VM packing is not a
  GA feature.
- AWS is the first supported cloud provider. Future providers must pass the
  conformance suite before support is claimed.
- Real cloud chaos and live AWS e2e are opt-in gates, not required for every PR.
- Sagas compensate where possible; compensation is not called rollback unless it
  restores the prior state.
- Object storage remains the durable state store. Do not add or require a
  separate database for correctness.

## Validation

Paste command output or CI links:

```bash
make readiness
make e2e-local
go test ./...
go vet ./...
```

Optional gates:

```bash
make e2e-apple-container
SKIFF_AWS_E2E=1 make e2e-aws
```

## Support Notes

- Primary runbook index: [../support/runbooks/README.md](../support/runbooks/README.md)
- Direct recovery: [../support/runbooks/skiffd-unavailable.md](../support/runbooks/skiffd-unavailable.md)
- Debug collection: [../operations/debug.md](../operations/debug.md)
- Rollback: [../operations/rollback.md](../operations/rollback.md)

## Rollback Plan

- Previous stable release:
- Command:

```bash
skiff rollback <service> --to previous-stable --direct --state <state-uri> --env <env> --provider aws --region <region> --approval-id <approval-id> --format json
```
