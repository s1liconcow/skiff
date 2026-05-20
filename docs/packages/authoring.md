# Package Authoring

Skiff packages are declarative bundles for infrastructure dependencies and their
explicit operation profiles. A package can describe a managed dependency, a
self-managed StatefulGroup, exported bindings, doctor checks, and typed saga
steps, but it does not get arbitrary access to cloud provider clients.

## Stack Dependencies

A Stack can declare package-provided dependencies with `stack.dependencies[]`:

```yaml
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: payments
  env: prod
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/payments-api@sha256:abc123
      runtime:
        port: 8080
        health:
          path: /healthz
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: 1.x
      config:
        mode: managed
        engine: postgres
        size: small
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
```

Fields:

- `name`: local dependency name used by bindings and operation targets.
- `uses`: package reference. Supported forms are `skiff.dev/name`,
  `oci://registry/repo/name:version`, and `file://../local-package`.
- `version`: exact semantic version or semantic version range.
- `config`: package-specific JSON/YAML object. The parser preserves it as raw
  JSON until a package-specific typed schema validates it.

Production package use must be resolved through `skiff.lock.json`; the public
spec is not the trust root.

## `skiff.package.json`

Package manifests are strict JSON:

```json
{
  "apiVersion": "skiff.dev/package/v1alpha1",
  "kind": "Package",
  "name": "postgres-ha",
  "version": "1.2.0",
  "exports": {
    "dependencies": ["postgres-ha"],
    "operation_profiles": ["primary-switchover-update"],
    "doctor_checks": ["postgres.verify_replica_lag"]
  },
  "plugin": {
    "manifest": "plugin.json"
  }
}
```

Unknown manifest fields are rejected. Plugin manifest paths must be relative
package paths.

## `skiff.lock.json`

Lockfiles pin package resolution for production and direct recovery:

```json
{
  "schema": "skiff.lock/v1alpha1",
  "packages": [
    {
      "name": "db",
      "ref": "skiff.dev/postgres-ha",
      "version": "1.2.0",
      "digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "signature_ref": "oci://registry.example.com/skiff/postgres-ha.sig@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "source": "oci://registry.example.com/skiff/postgres-ha:1.2.0",
      "manifest_digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "resolved_at": "2026-05-20T02:43:35Z"
    }
  ]
}
```

Production entries must include exact version, package digest, manifest digest,
signature reference, source registry, and resolution timestamp. Local unsigned
`file://` packages are a development escape hatch only when explicitly allowed
by the caller.
