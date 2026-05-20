# Packages Overview

Packages are the package-first way to add stateful infrastructure to a Skiff
stack without learning saga internals. A stack service declares a dependency,
locks the package in `skiff.lock.json`, and binds the service to the dependency
through an environment variable or secret reference.

Object storage remains the durable source of truth. A package does not get to
run an opaque controller. It exports typed dependency resources, typed operation
profiles, and typed package steps that Skiff compiles into visible cloud
primitives and explicit saga graphs.

## Basic Shape

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
        ref: registry.example.com/payments-api@sha256:...
      runtime:
        port: 8080
        health:
          path: /healthz
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: "1.0.0"
      config:
        mode: managed
        version: "16"
        replicas: 2
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
```

The dependency compiles into either managed cloud resources or a
`StatefulGroup`, depending on package support and `config.mode`.

## Modes

`managed` uses the provider's managed service when the package exports one, for
example RDS or a managed cache. The application receives a secret reference such
as `secret://managed-database/payments-db/connection-url`.

`self-managed` or `stateful` creates named Skiff members, one VM per member by
default, with durable volumes, stable DNS identity, member control documents,
and package operation profiles. The application receives a Skiff reference such
as `skiff://stateful/payments-db`.

## Normal Workflow

```bash
skiff pkg add skiff.dev/postgres-ha --lockfile skiff.lock.json
skiff pkg verify postgres-ha --lockfile skiff.lock.json --conformance --format json
skiff validate skiff.yaml
skiff explain skiff.yaml --provider aws --region us-west-2 --state s3://skiff-state-prod --format json
skiff deploy skiff.yaml --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Use `skiff ops` for package operations:

```bash
skiff ops run payments-db primary-switchover-update --param release_id=rel_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```

Use `skiff saga` only when inspecting, resuming, approving, rejecting,
canceling, or compensating the explicit saga graph behind an operation.

## Migration From `stack.databases[]`

Older stack examples used `stack.databases[]` for simple managed database
dependencies. Package-first stacks use `stack.dependencies[]` instead.

Before:

```yaml
stack:
  databases:
    - name: db
      engine: postgres
      version: "16"
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
```

After:

```yaml
stack:
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: "1.0.0"
      config:
        mode: managed
        version: "16"
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
```

The binding stays the same. The package lock adds the missing supply-chain
metadata: package source, version, digest, signature reference, and exported
operation profiles.
