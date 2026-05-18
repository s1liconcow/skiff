# Kubernetes Adoption

Skiff imports Kubernetes manifests as migration input, not as a Kubernetes
emulator. The importer produces a Skiff Service spec, a JSON result, and a
Markdown migration report with explicit findings for behavior that needs review.

## Import a Service

```bash
skiff import kube ./kube.yaml --env staging --format markdown --trace-id tr_kube_import
skiff import kube ./kube.yaml --env staging --format yaml > skiff.yaml
skiff import kube ./kube.yaml --env staging --format json --trace-id tr_kube_import
```

Supported simple mappings:

| Kubernetes | Skiff |
| --- | --- |
| Deployment first container image | `artifact.type: oci`, `artifact.ref` |
| Deployment container port and Service targetPort | `runtime.port` |
| HTTP readiness or liveness probe | `runtime.health` |
| Deployment replicas and HPA min/max | `scale.min`, `scale.max` |
| Ingress host and TLS secret | `network.ingress` |
| Literal env values and ConfigMap key refs | `runtime.env` |
| Secret key refs | `secrets[]` references only |
| PodDisruptionBudget | migration report finding |

The importer does not copy Kubernetes Secret data. Secret refs are emitted as
`secret://kubernetes/<namespace>/<secret>/<key>` placeholders so operators can
map them to cloud secret manager references before production.

## Findings

Warnings do not block spec generation, but they must be reviewed before cutover.
Unsupported features produce errors and make the JSON result `ok: false`.

Common warnings:

- Sidecars and init containers are not imported.
- Service mesh annotations are reported explicitly.
- `envFrom` is not expanded automatically.
- LoadBalancer Services without Ingress require a Skiff ingress decision.
- PDBs require rollout and capacity review.

Unsupported by the simple importer:

- Privileged containers.
- `hostPath` volumes.
- CRDs and arbitrary custom controllers.
- StatefulSets without a future explicit stateful recipe.

## Shadow Deploy

After reviewing the generated spec, deploy Skiff infrastructure without attaching
ingress listeners:

```bash
skiff deploy ./skiff.yaml \
  --direct \
  --state s3://skiff-state-prod \
  --env staging \
  --shadow \
  --dry-run \
  --format json \
  --trace-id tr_shadow_plan
```

When the plan is acceptable, remove `--dry-run` and provide the normal release
signing flags. Shadow deploys still publish durable Skiff state and provider
resources, but omit production traffic listeners from the compiled graph.

## Weighted Cutover

Create a typed saga to move traffic from the existing Kubernetes endpoint to the
Skiff endpoint in stages:

```bash
skiff cutover payments-api \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --from kube \
  --to skiff \
  --percent 10 \
  --format json \
  --trace-id tr_cutover_10
```

Dry-run first to inspect IDs and next commands without writing saga objects:

```bash
skiff cutover payments-api --env prod --percent 10 --dry-run --format json
```

The cutover saga writes immutable intent and graph documents plus a CAS-controlled
saga control document. It includes preflight, manual approval, weighted traffic
shift, and target health verification steps.

Expected successful output includes a saga ID, current traffic percentage, and
next action. Diagnose cutover with:

```bash
skiff ops inspect saga_01J... --direct --state s3://skiff-state-prod --format json
skiff ops events saga_01J... --direct --state s3://skiff-state-prod --format json
skiff doctor payments-api --direct --state s3://skiff-state-prod --fresh --format json
```

Before 100% cutover, recovery is normally another traffic-shift saga back toward
the Kubernetes target. After 100% cutover, treat rollback as a new explicit
operation and record the chosen target and approval context.
