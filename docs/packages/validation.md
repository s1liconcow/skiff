# Package Validation

Package validation protects the boundary between package authoring and live
operation. A package must be locked, verified, and explainable before it becomes
part of a production stack.

## Lock And Verify

```bash
skiff pkg add skiff.dev/postgres-ha --lockfile skiff.lock.json
skiff pkg list --lockfile skiff.lock.json --format json
skiff pkg verify postgres-ha --lockfile skiff.lock.json --conformance --format json
```

The lock file records package name, version, source, digest, signature
reference, and exported capabilities. Validation rejects stack dependencies
that do not have a matching lock entry.

## `skiff-opsem` Harness

`skiff-opsem` is the deterministic operation-semantics harness used by Skiff's
stateful package tests. It exposes member state, health, topology, and unsafe
conditions so package operation profiles can be tested without relying on a
real database cluster.

The gated live checks are:

```bash
SKIFF_OPSEM_E2E=1 SKIFF_E2E_OPSEM_IMAGE=registry.example/skiff-opsem@sha256:... make e2e-apple-stateful
SKIFF_OPSEM_PROFILES_E2E=1 SKIFF_E2E_OPSEM_IMAGE=registry.example/skiff-opsem@sha256:... make e2e-apple-opsem-profiles
SKIFF_E2E_OPSEM_IMAGE=registry.example/skiff-opsem@sha256:... make e2e-apple-stateful-packages
```

These tests render package-backed operation graphs through `skiff ops run`,
execute package steps against live Apple StatefulGroups, verify durable
operation/saga/audit objects, test direct resume, and prove unsafe-state gates
stop before live member mutation.

## What Conformance Covers

Conformance checks package manifests, exported dependencies, operation profile
explain output, rendered saga graph shape, package step declarations, and
validation diagnostics. It is a fast local gate. The `skiff-opsem` tests are the
live semantic gate for topology behavior.

## CI Expectations

Normal PRs should run:

```bash
go test ./tests/conformance/...
go test ./tests/recipes/...
go test ./tests/docs
```

Live package gates stay optional and environment-gated. They must emit enough
JSON evidence to continue after interruption: package name, version/digest,
operation IDs, saga IDs, provider IDs, object paths, facts, cleanup status, and
recommended next commands.
