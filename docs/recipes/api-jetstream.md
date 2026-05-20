# API plus NATS JetStream

Use the NATS JetStream package when the API needs a durable stream with explicit
RAFT quorum checks and stable member identity.

```bash
skiff pkg add skiff.dev/nats-jetstream --lockfile skiff.lock.json
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
    - name: stream
      uses: skiff.dev/nats-jetstream
      version: "1.0.0"
      config:
        mode: stateful
        replicas: 3
        volume:
          size: 50Gi
        artifact:
          type: oci
          ref: registry.example.com/nats@sha256:...
        runtime:
          command: ["/nats-server", "-js"]
          ports:
            client: 4222
            route: 6222
            monitoring: 8222
          health:
            path: /healthz
            port: 8222
  bindings:
    - from: api
      to: stream
      as: NATS_URL
```

```bash
skiff validate skiff.yaml
skiff ops run orders-stream raft-group-rolling-update --param release_id=rel_01J... --param group_selector={} --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```
