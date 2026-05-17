# Engineer Onboarding

This checklist is for engineers joining Skiff development or support rotation.

## First Hour

1. Read the repository invariants in `AGENTS.md`.
2. Read [../quickstart.md](../quickstart.md).
3. Read [object-state-layout.md](object-state-layout.md).
4. Run:

```bash
go version
make test
make readiness
```

## Local Development

Use repository-local Go caches when running tests in constrained environments:

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/gomod go test ./...
```

Useful commands:

```bash
make build
make e2e-local
go test ./tests/conformance/provider ./tests/conformance/plugin
go vet ./...
```

## Architecture Drill

Be able to explain:

- why object storage is durable truth
- why `skiffd` is not a database
- how direct CLI mode recovers when `skiffd` is unavailable
- where service control, release manifests, operation controls, saga controls,
  events, resources, and audits live
- why locks live inside control documents
- why one VM runs one workload replica by default

## Support Drill

Run the readiness gate and inspect the report:

```bash
SKIFF_READINESS_REPORT=/tmp/skiff-readiness.json make readiness
```

Practice these runbooks:

- [../support/runbooks/skiffd-unavailable.md](../support/runbooks/skiffd-unavailable.md)
- [../support/runbooks/stuck-saga.md](../support/runbooks/stuck-saga.md)
- [../support/runbooks/lease-held.md](../support/runbooks/lease-held.md)

For each drill, record:

- trace ID
- operation or saga ID
- object-state paths
- provider resource IDs
- exact command output

## Before Taking Support

- Confirm you can run direct-mode status against a test state store.
- Confirm you can distinguish rollback from saga compensation.
- Confirm you know which operations require approval IDs.
- Confirm you know where release notes and known limitations live:
  [../release/release-notes-template.md](../release/release-notes-template.md).
