# Stateful Group

Managed databases remain the default recommendation for production state.
`StatefulGroup` is for deliberate cases where a workload needs named VM members,
single-writer durable volumes, stable DNS identity, and recipe-specific recovery.

It is not a Kubernetes StatefulSet clone. A member is explicitly:

```text
member ordinal + VM instance + durable volume + stable DNS + generation
```

## Spec Shape

```yaml
apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: postgres
  env: prod
stateful:
  replicas: 1
  members:
    - ordinal: 0
      zone: us-west-2a
      dnsName: postgres-0.internal.example.com
  volume:
    size: 100Gi
    type: gp3
    mountPath: /var/lib/postgresql
    encrypted: true
  recipe:
    name: postgres-single
  update:
    strategy: ordered
```

## Durable Control

Each member has a CAS control document:

```text
stateful/<group>/members/<member>/control.json
```

The member control stores instance ID, volume ID, DNS name, generation, phase,
lease, provider operation IDs, and replacement progress. The lease lives inside
the same control document it protects. There are no separate lock objects.

## Replacement Flow

Replacement is conservative because duplicate writers are worse than downtime:

```text
acquire member lease
record replacement intent in member control
fence old VM
record provider fencing operation
detach volume
record detach operation
launch replacement in the same zone
record replacement instance ID
attach volume
update DNS
run recipe recovery
verify recipe health
publish new instance ID and generation
release member lease
```

Every provider operation ID is stored before the next provider step starts, so
the replacement can resume after interruption. Stale runners must compare their
member generation with the control document and refuse to write as an old
generation.

## Recipe Hooks

Recipes provide application-specific behavior:

- start
- stop
- health
- backup
- restore
- role detection

The platform owns fencing, volumes, stable identity, and control documents.
Recipes decide how a database or other stateful workload recovers safely after
the VM and volume mechanics are complete.

## Limits

Skiff does not automate stateful scale-down in this foundation. Deleting or
detaching durable volumes remains a separate explicit operation with snapshots,
retention checks, approval, and audit records.
