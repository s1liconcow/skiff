# API plus multi-region managed database

This recipe models one API service deployed in a primary and secondary region with a managed database writer and replica. Object storage remains durable truth; regional failover is an explicit saga, not a hidden controller.

The stack defines:

- `primaryRegion` and `secondaryRegions`
- one service and one managed database shape reused per region
- a global traffic policy with visible regional weights
- database replication mode and max replica lag
- a failover policy that requires approval and can freeze writes

Compile and inspect the example:

```bash
skiff compile examples/stacks/api-multiregion-database/skiff.yaml --format json
skiff plan examples/stacks/api-multiregion-database/skiff.yaml --provider aws --region us-west-2 --state s3://skiff-state-prod --format json
```

The compiler expands the stack into regional service resources, regional managed database resources, and a provider-neutral global traffic policy. Each regional resource carries `skiff.dev/region` tags so status and drift can explain what exists in each region.

Create a failover plan before mutating anything:

```bash
skiff failover orders --database orders-db --from-region us-west-2 --to-region us-east-1 --dry-run --format json
```

Execute only with direct object-state access and approval context for production:

```bash
skiff --direct --state s3://skiff-state-prod failover orders \
  --database orders-db \
  --from-region us-west-2 \
  --to-region us-east-1 \
  --approval-id approval_123 \
  --format json
```

The failover saga verifies secondary capacity and replica lag, asks for manual approval, optionally freezes writes, promotes the secondary database, updates the writer secret, shifts traffic through a 10% gate, then shifts to 100%.

After the writer secret points at the promoted region and new writes begin, failback is not a rollback. It is a separate plan with its own risk and approval because writes may have diverged.

Expected output from the dry run includes the planned saga ID, current steps,
risk classification, reversibility, and the next approval action. Execution
writes saga intent, graph, control, events, step result artifacts, and audit
records before provider changes continue.

Diagnose regional failures with:

```bash
skiff saga inspect saga_01J... --direct --state s3://skiff-state-prod --format json
skiff events --scope saga --saga saga_01J... --direct --state s3://skiff-state-prod --format json
skiff doctor orders --direct --state s3://skiff-state-prod --env prod --provider aws --region us-west-2 --fresh --format json
```

If failure occurs before writer promotion, compensation can usually keep traffic
and writes in the original region. After promotion, recover through a new
failover or repair plan instead of treating the move as a rollback.
