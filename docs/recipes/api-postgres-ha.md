# API plus HA Postgres

Use the Postgres HA package when an API needs PostgreSQL plus explicit
switchover, backup, restore, and topology inspection operations.

```bash
skiff pkg add skiff.dev/postgres-ha --lockfile skiff.lock.json
```

Managed RDS/Aurora-style dependency:

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
        maxReplicaLagBytes: 1048576
        requireApproval: true
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
```

Self-managed VM-per-member Postgres:

```yaml
  dependencies:
    - name: db
      uses: skiff.dev/postgres-ha
      version: "1.0.0"
      config:
        mode: self-managed
        version: "16"
        replicas: 3
        maxReplicaLagBytes: 65536
        volume:
          size: 100Gi
        artifact:
          type: oci
          ref: registry.example.com/postgres-ha@sha256:...
        runtime:
          command: ["/usr/local/bin/postgres-ha"]
          ports:
            postgres: 5432
            health: 8008
          health:
            path: /healthz
            port: 8008
```

Validate and inspect before deploy:

```bash
skiff validate skiff.yaml
skiff pkg verify postgres-ha --lockfile skiff.lock.json --conformance --format json
skiff explain skiff.yaml --provider aws --region us-west-2 --state s3://skiff-state-prod --format json
```

Run topology-changing work through `skiff ops`, not raw saga creation:

```bash
skiff ops run payments-db primary-switchover-update --param release_id=rel_01J... --param candidate=payments-db-1 --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```
