# Production Readiness

Skiff treats production readiness as an executable release gate, not a prose
checklist. The mandatory gate is fake-provider based and safe for pull requests;
Apple silicon and AWS modes stay optional and explicitly gated.

Run the readiness gate locally:

```bash
make readiness
```

Write the machine-readable report to a stable path:

```bash
SKIFF_READINESS_REPORT=artifacts/readiness/production-readiness.json make readiness
```

## Checklist

| Area | Required proof | Gate |
|---|---|---|
| Security | high-risk production debug is denied without approval | `TestProductionReadinessSuite/debug_denial` |
| State durability | service control leases fence concurrent writers with `LEASE_HELD` | `TestProductionReadinessSuite/lease_contention` |
| Direct recovery | direct CLI status and rollback work without `skiffd` | `TestProductionReadinessSuite/direct_recovery` |
| Operation resumability | interrupted operation resumes from stored provider operation IDs | `TestProductionReadinessSuite/operation_resume` |
| Saga resumability | worker resumes a pending typed saga graph from object state | `TestProductionReadinessSuite/saga_resume` |
| Cache recovery | direct mode rebuilds from durable object state after a stale view | `TestProductionReadinessSuite/stale_cache_direct_refresh` |
| Rollout failure | doctor reports failed rollout and recommends safe next commands | `TestProductionReadinessSuite/failed_rollout_diagnosis` |
| Bad release | runner failure evidence recommends rollback to the stable release | `TestProductionReadinessSuite/bad_release_rollback_recommendation` |
| Observability outage | log and metric outages are diagnosed without service mutation | `TestProductionReadinessSuite/observability_outage_diagnosis` |
| Chaos scenarios | fake provider covers instance death, target health failure, ASG rollout failure, and regional capacity outage | `go test ./tests/chaos` |

## Report Contract

The readiness suite emits `skiff.readiness/v1` JSON when
`SKIFF_READINESS_REPORT` is set. Each scenario records:

- facts separated from hypotheses or commands
- object paths touched by the scenario where available
- provider IDs, operation IDs, and saga IDs where relevant
- commands that let an operator or agent continue after interruption
- mutating command metadata, including risk and reversibility

CI uploads the report artifact from the pull-request readiness gate.

## Optional Gates

Use the e2e matrix for environment-specific proof:

```bash
make e2e-local
make e2e-apple-container
SKIFF_AWS_E2E=1 make e2e-aws
```

Real cloud chaos must remain opt-in, isolated by unique prefixes/tags, and
cleanup-safe. Do not make AWS or Apple silicon chaos mandatory for normal PRs.
