# API plus Kafka

Use the Kafka package for a self-managed KRaft cluster where partition quorum
and broker identity matter during updates.

```bash
skiff pkg add skiff.dev/kafka --lockfile skiff.lock.json
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
    - name: kafka
      uses: skiff.dev/kafka
      version: "1.0.0"
      config:
        mode: stateful
        replicas: 3
        volume:
          size: 500Gi
        artifact:
          type: oci
          ref: registry.example.com/kafka@sha256:...
        runtime:
          command: ["/opt/kafka/bin/kafka-server-start.sh", "/etc/kafka/server.properties"]
          ports:
            broker: 9092
            controller: 9093
            health: 8080
          health:
            path: /healthz
            port: 8080
  bindings:
    - from: api
      to: kafka
      as: KAFKA_BROKERS
```

```bash
skiff validate skiff.yaml
skiff explain skiff.yaml --provider aws --region us-west-2 --state s3://skiff-state-prod --format json
skiff ops run orders-kafka partition-quorum-rolling-update --param release_id=rel_01J... --param partition_selector={} --approval-id approval_01J... --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --format json
```
