# Package Boundaries

Skiff keeps durable state in object storage and keeps cloud-specific behavior behind narrow package boundaries.

## Command Packages

`cmd/skiff`, `cmd/skiffd`, and `cmd/skiff-runner` are entrypoints only. They parse process arguments, call internal packages, and exit with documented status codes. Business logic should live under `internal/`.

## Core Internal Packages

- `internal/spec` parses, defaults, and validates user-facing service specs.
- `internal/compiler` lowers validated specs into provider-neutral IR.
- `internal/objstore` defines object-storage primitives used for durable truth.
- `internal/state` owns object-state schemas, path helpers, and CAS control documents.
- `internal/release` owns immutable release and runtime manifest behavior.
- `internal/security` owns signing, digest, redaction, and policy primitives.
- `internal/index` builds rebuildable in-memory views from object storage.
- `internal/runner` owns VM-local runner state and workload lifecycle.
- `internal/provider` owns cloud-provider interfaces and implementations.

## Import Rules

- CLI rendering must not be imported by state, release, runner, compiler, or provider packages.
- Provider packages must not import command packages.
- Cloud SDK types must remain inside provider or object-store backend leaf packages.
- State mutation helpers must depend on `internal/objstore`, not on cloud SDK clients.
- Rebuildable indexes may read state objects, but durable correctness must not depend on index memory.
- Public `pkg/` APIs should not be introduced until a plugin or SDK contract requires them.

## Durable State Rules

Use path helpers for object keys once they exist. Immutable objects are written with create-only semantics. Mutable control documents are updated with compare-and-swap and carry any lease that protects them.
