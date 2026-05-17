# Terraform Adoption

Skiff can generate Terraform for stable AWS infrastructure and then adopt the
resulting provider IDs into object state. This is an adoption bridge:

- Terraform owns infrastructure shape.
- Skiff owns releases, runtime manifests, service control, operations, sagas,
  rollout, rollback, status, events, and audit history.
- Object storage remains Skiff's durable truth for operational state.

## Generate a Module

```bash
skiff terraform generate ./skiff.yaml \
  --out ./terraform/skiff-payments-api \
  --provider aws \
  --region us-west-2 \
  --state s3://skiff-state-prod \
  --format json \
  --trace-id tr_tf_generate
```

The generated directory contains:

| File | Purpose |
| --- | --- |
| `main.tf` | AWS resources lowered from the Skiff IR. |
| `variables.tf` | Editable environment inputs such as VPC, subnets, AMI, and listener ARN. |
| `outputs.tf` | `skiff_resources` output used by adoption. |
| `README.md` | Apply and adoption commands for operators. |

## Adopt Terraform Outputs

After `terraform apply`, export the Skiff output and record it in object state:

```bash
terraform output -json skiff_resources > skiff_resources.json

skiff adopt terraform skiff_resources.json \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --format json \
  --trace-id tr_tf_adopt
```

Adoption writes resource records under both object-state indexes:

```text
resources/by-logical/<kind>/<name>.json
resources/by-provider/<provider>/<kind>/<id>.json
```

Records include ownership metadata:

```json
{
  "ownership": {
    "mode": "terraform-infra-skiff-release",
    "source": "terraform",
    "managed_by": "terraform"
  }
}
```

## Ownership Modes

| Mode | Meaning |
| --- | --- |
| `direct` | Skiff owns and applies the provider resource. |
| `terraform-infra-skiff-release` | Terraform owns stable infra; Skiff owns releases and operations. |
| `external` | Skiff records the resource for visibility but does not apply shape. |

When a deploy plan finds a Terraform-owned or external adopted resource record,
the AWS provider marks that resource as `no-op` and carries the provider ID into
the deploy result. Missing records remain normal create/update plans, which
prevents Skiff from silently assuming Terraform ownership.

## Deploy After Adoption

```bash
skiff deploy ./skiff.yaml \
  --direct \
  --state s3://skiff-state-prod \
  --env prod \
  --provider aws \
  --region us-west-2 \
  --release-id rel_20260517 \
  --signing-seed-base64 "$SKIFF_RELEASE_SIGNING_SEED" \
  --format json \
  --trace-id tr_tf_deploy
```

Skiff still writes immutable release/runtime manifests before updating service
control. Terraform-owned infrastructure is not reapplied during deploy.
