# CI/CD Templates

Skiff CI/CD templates make CI the release authoring layer and Skiff the
deployment and operation layer. CI builds and proves an immutable artifact.
Skiff records signed releases, release candidate evidence, operation intents,
events, and audit records in object state before production changes proceed.

Generate templates:

```bash
skiff ci generate github-actions --service payments-api --out .github/workflows/skiff.yml
skiff ci generate gitlab --service payments-api --out .gitlab-ci.yml
skiff ci generate buildkite --service payments-api --out .buildkite/pipeline.yml
```

Use `--format json` when an agent needs the generated template and command list
as machine-readable output.

## Safe Boundaries

CI owns tests, builds, scans, SBOM/provenance, and approval routing. Skiff owns
release signing, durable object-state writes, provider plans, deploy operation
records, promotion requirement checks, and rollout visibility.

The generated templates default to direct mode for mutating commands:

```bash
skiff release candidate create ... --direct --state "$SKIFF_STATE" --format json
skiff deploy skiff.staging.json --direct --state "$SKIFF_STATE" --format json
skiff promote "$SKIFF_SERVICE" --direct --state "$SKIFF_STATE" --format json
```

Direct mode keeps recovery possible when `skiffd` is unavailable. The templates
also include an API-mode status example:

```bash
skiff status "$SKIFF_SERVICE" --api --api-url "$SKIFF_API_URL" --format json
```

Use API mode for freshness checks, dashboards, and agent read paths when
`skiffd` is available. Keep durable mutating production commands direct until
the API deploy/promote path provides equivalent object-state and audit behavior.

## Pipeline Proofs

`skiff validate` proves the user-facing spec decodes, defaults, and validates.
The templates archive `skiff-validate.json`.

`skiff contract test` proves the CI-rendered spec compiles to Skiff IR and uses
an immutable artifact reference with a `sha256:<hex>` digest. This prevents
mutable tags from becoming release candidates.

`skiff plan` proves the provider lowering is inspectable before object-state or
cloud mutation. The plan output is archived as `skiff-plan.json`.

`skiff release candidate create` records immutable candidate evidence in object
state. The templates include checks for tests, contract, and plan, plus git and
CI metadata.

`skiff deploy` publishes a signed release and updates desired service state
through a recorded operation. The generated templates render `skiff.staging.json`
with the immutable image digest before deploying.

`skiff promote` validates the release candidate and protected-environment
approval context before recording the production promotion operation.

## OIDC And IAM Placeholders

The templates intentionally use variables and secrets instead of account IDs:

- GitHub Actions expects `secrets.SKIFF_DEPLOY_ROLE_ARN` and grants
  `id-token: write`.
- GitLab uses an `id_tokens` block with `SKIFF_OIDC_AUDIENCE`.
- Buildkite expects the pipeline environment to provide cloud OIDC exchange
  variables such as `AWS_ROLE_TO_ASSUME`.

Bind those roles to least-privilege Skiff deployer permissions for the target
state bucket, signing key access, and provider APIs. Do not embed permanent
cloud keys in generated templates.

## Immutable Artifacts

Every generated template builds and pushes an OCI image, resolves the registry
digest, and constructs:

```bash
IMAGE_REF="$IMAGE_REPO@$IMAGE_DIGEST"
```

The release candidate stores both `--artifact-uri "$IMAGE_REF"` and
`--artifact-digest "$IMAGE_DIGEST"`. Production promotion reuses the candidate;
it does not rebuild or retag the artifact.
