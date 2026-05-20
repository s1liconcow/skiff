# API plus HA MySQL

Use the MySQL HA package for an API that needs MySQL with explicit primary
switchover, router verification, backup, restore, and topology checks.

```bash
skiff pkg add skiff.dev/mysql-ha --lockfile skiff.lock.json
```

```yaml
apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: orders
  env: prod
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: registry.example.com/orders-api@sha256:...
      runtime:
        port: 8080
        health:
          path: /healthz
  dependencies:
    - name: mysql
      uses: skiff.dev/mysql-ha
      version: "1.0.0"
      config:
        mode: self-managed
        replicas: 3
        volume:
          size: 100Gi
        artifact:
          type: oci
          ref: registry.example.com/mysql-ha@sha256:...
        runtime:
          command: ["/usr/local/bin/mysql-ha"]
          ports:
            mysql: 3306
            router: 6446
            health: 8080
          health:
            path: /healthz
            port: 8080
  bindings:
    - from: api
      to: mysql
      as: DATABASE_URL
```

```bash
skiff validate skiff.yaml
skiff pkg verify mysql-ha --lockfile skiff.lock.json --conformance --format json
skiff ops run orders-mysql primary-switchover-update --param release_id=rel_01J... --param candidate=orders-mysql-1 --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```
