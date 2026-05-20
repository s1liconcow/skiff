# Quickstart

Skiff has three practical first runs:

- **Local**: fastest path, no cloud credentials, file object state, fake
  provider.
- **Apple Silicon**: local RustFS object state, local Linux VMs, Caddy, and
  `skiffd`.
- **Live AWS**: realistic bootstrap, initialization, cost review, and a gated
  stateless service deploy against AWS primitives.

All paths start with direct mode because object storage is the durable source
of truth and direct mode is the recovery path when `skiffd` is unavailable.

## Local

The local path writes real Skiff object state and exercises the normal operator
loop without creating cloud resources or starting a workload process. Provider
health, logs, and metrics are synthetic because the fake provider is standing in
for AWS.

Build the local binaries when needed:

```bash
make build
export PATH="$PWD/bin:$PATH"
```

Run the scripted demo:

```bash
make demo-local
```

That command validates `examples/service/http-hello/skiff.yaml`, writes a local
`.skiffconfig`, plans AWS-shaped primitives, publishes signed release objects,
runs a canary saga, prints status/logs/metrics/doctor/events/ops output, and
renders a read-only TUI frame.

Run the same path as an isolated smoke test:

```bash
make demo-test
```

To walk the local flow by hand, create a direct fake-provider context plus an
AWS planning context over the same local state directory:

```bash
cat > .skiffconfig <<EOF
apiVersion: skiff.dev/v1alpha1
kind: SkiffConfig
current-context: local-fake
contexts:
  - name: local-fake
    context:
      mode: direct
      env: prod
      provider: fake
      region: local
      state: "file://$PWD/.skiff-demo-state"
  - name: local-aws-plan
    context:
      mode: direct
      env: prod
      provider: aws
      region: us-west-2
      state: "file://$PWD/.skiff-demo-state"
EOF

export SKIFF_CONFIG="$PWD/.skiffconfig"
unset SKIFF_CONTEXT
```

Inspect the active context and validate the example service:

```bash
skiff config get-contexts
skiff config show
skiff validate examples/service/http-hello/skiff.yaml
```

Preview provider-visible AWS primitives without mutating state:

```bash
SKIFF_CONTEXT=local-aws-plan skiff plan examples/service/http-hello/skiff.yaml
```

Deploy locally through the fake provider:

```bash
skiff deploy examples/service/http-hello/skiff.yaml
```

Expected result: `deploy op_... succeeded`, a `release: rel_...` line, and a
signature line. Skiff writes release/runtime manifests, operation intent/control
documents, service control, resource records, events, and audit records into
the local object-state directory.

Run a staged canary saga:

```bash
skiff deploy examples/service/http-hello/skiff.yaml \
  --canary \
  --canary-bake 0s \
  --canary-metric request_count \
  --canary-threshold 1
```

Inspect the service and durable operation history:

```bash
skiff status http-hello --fresh
skiff logs http-hello --limit 20
skiff metrics http-hello
skiff doctor http-hello
skiff ops events http-hello --limit 30
skiff ops list --all --service http-hello
```

Open the terminal UI:

```bash
skiff tui http-hello --once --read-only
skiff tui http-hello --read-only
```

After a second release, roll back to the previous stable release:

```bash
skiff rollback http-hello --to <base-release>
```

Use the first deploy's `release: rel_...` value as `<base-release>`. Rollback
is an explicit saga with durable events and audit output.

## Apple Silicon

The Apple Silicon path is for a real local workload run. It starts RustFS for
S3-compatible object state, runs Caddy inside Apple Container, writes signed
release/runtime manifests, starts a local `skiffd`, and leaves a generated
context you can keep using after the command exits.

Prerequisites:

- Apple silicon Mac.
- Apple Container installed and running, with `container` on `PATH`.
- `skiff` and `skiffd` installed on `PATH`.
- Network access to pull the RustFS and Caddy OCI images.

Install local Skiff binaries if needed:

```bash
SKIFF_INSTALL_VERSION=v0.1.0 SKIFF_INSTALL_DIR="$HOME/.local/bin" scripts/install.sh
export PATH="$HOME/.local/bin:$PATH"
skiff version
skiffd version
container --version
```

Start the persistent local workload context:

```bash
make demo-apple-context
```

The command prints an environment file under
`.skiff-demo-reports/apple-container/`. Source the exact file from the latest
run:

```bash
source .skiff-demo-reports/apple-container/<run>.env
```

The sourced environment selects the direct Apple Container context and exports:

- `$SKIFF_APPLE_GREEN_SPEC`
- `$SKIFF_APPLE_RED_SPEC`
- `$SKIFF_APPLE_BLUE_SPEC`
- `$SKIFF_APPLE_CADDY_URL`
- `$SKIFF_APPLE_RELEASE_SIGNING_KEY_ID`
- `$SKIFF_APPLE_RELEASE_SIGNING_KEY_REF`

The generated Skiff contexts include a `keychain://...` release signing key
reference. The demo creates or reuses that OS-keychain signer automatically, so
the follow-on `skiff deploy` commands exercise the same path as used with AWS KMS.

macOS may not prompt when the login keychain is already unlocked. To prove the
demo signer exists without printing the secret, run:

```bash
security find-generic-password \
  -s "$SKIFF_APPLE_RELEASE_SIGNING_KEYCHAIN_SERVICE" \
  -a "$SKIFF_APPLE_RELEASE_SIGNING_KEYCHAIN_ACCOUNT" \
  >/dev/null && echo "keychain signer found"

skiff config show
skiff release list caddy-web --limit 1
```

Inspect the live local service:

```bash
skiff config get-contexts
skiff status caddy-web --fresh
skiff logs caddy-web --limit 20
skiff ops events caddy-web --limit 20
```

Publish and canary the healthy GREEN release:

```bash
skiff deploy "$SKIFF_APPLE_GREEN_SPEC" \
  --canary \
  --canary-stages 50,100 \
  --canary-bake 0s \
  --canary-metric request_count \
  --canary-threshold 1

curl "$SKIFF_APPLE_CADDY_URL"
```

Expected result: the page says `GREEN`.

Try the RED release. It intentionally omits `/healthz`, so the canary fails
target health and compensates back to the previous stable GREEN release:

```bash
skiff deploy "$SKIFF_APPLE_RED_SPEC" \
  --canary \
  --canary-stages 50,100 \
  --canary-bake 0s \
  --canary-metric request_count \
  --canary-threshold 1

curl "$SKIFF_APPLE_CADDY_URL"
```

Expected result: the canary reports `status=failed`, and the page still says
`GREEN`.

Publish and canary the healthy BLUE release:

```bash
skiff deploy "$SKIFF_APPLE_BLUE_SPEC" \
  --canary \
  --canary-stages 50,100 \
  --canary-bake 0s \
  --canary-metric request_count \
  --canary-threshold 1

curl "$SKIFF_APPLE_CADDY_URL"
```

Expected result: the page says `BLUE`.

Use direct mode for recovery-style inspection:

```bash
skiff status caddy-web --fresh
skiff doctor caddy-web --fresh
skiff ops events caddy-web --limit 30
```

Use the local `skiffd` facade for the normal API/TUI path:

```bash
SKIFF_CONTEXT=local-apple-skiffd skiff status caddy-web --fresh
SKIFF_CONTEXT=local-apple-skiffd skiff tui caddy-web --read-only
```

Stop the persistent demo resources:

```bash
make demo-apple-down
```

Remove all Skiff-named Apple Container demo/e2e containers and RustFS volumes:

```bash
make clean-apple-containers
```

For a cleanup-safe smoke run that stops Apple containers and in-process
`skiffd` before exit, use:

```bash
make demo-apple-container
```

## Live AWS

Use this path in a disposable AWS account or isolated environment first. It
does real AWS mutation when you reach the deploy step.

The current live AWS adapter creates and updates core stateless service
primitives: IAM role/profile, security groups, CloudWatch log group, target
group, launch template, and Auto Scaling Group. Generated stack recipes with
RDS, Secrets Manager, and workload S3 buckets are useful for initialization,
planning, and cost analysis, but this quickstart deploys the stateless
`http-hello` service because live apply for those managed resource kinds is not
part of the current path.

Prerequisites:

- AWS credentials for the target account and region.
- Terraform is optional. The default bootstrap path uses Skiff's AWS SDK
  client; `--emit terraform` writes an equivalent Terraform backend when you
  want Terraform state.
- No custom AMI is required for this quickstart. Bootstrap defaults runner VMs
  to Skiff's public runner AMI SSM parameter for Amazon Linux 2023 x86_64,
  `/skiff/runner/ami/al2023/x86_64/stable`. If that parameter has not been
  published in your target region yet, pass AWS's public Amazon Linux 2023
  fallback with `--runner-ami-ssm-parameter`; that fallback cloud-init path
  installs a pinned Skiff runner release on first boot.
- A real digest-pinned artifact ref that the runner VM can fetch. Do not deploy
  the placeholder `registry.example.com` artifact from the repository examples.

Set run variables:

```bash
make build
export PATH="$PWD/bin:$PATH"

export AWS_REGION="${AWS_REGION:-us-west-2}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-$AWS_REGION}"
export SKIFF_ENV="quickstart"
```

`AWS_REGION` is for the AWS SDK, AWS CLI, optional Terraform provider,
bootstrap, and pricing commands. `SKIFF_ENV` is the Skiff environment name.
Bootstrap derives the state bucket name, the `s3://...` state URI, traceable
root object path, and local context from those values. When
`SKIFF_COMPANY_NAME` is set, the default bucket uses the company slug; without a
company name, bootstrap uses an autogenerated identifier.

Bootstrap the state substrate. `skiff bootstrap aws` shows the bucket,
KMS alias, IAM roles, managed VPC/subnets/NAT, bucket policy, and environment
root object. The managed private network includes a NAT gateway so private
workload VMs can fetch artifacts; that is a billable AWS resource. Keep
`--ingress private` for the smallest safe quickstart surface. Use
`--ingress public` instead when you want bootstrap to create an
internet-facing ALB. If you also set `SKIFF_COMPANY_NAME` and
`SKIFF_DOMAIN_NAME`, bootstrap uses friendly resource names, creates an
environment ingress base domain at `<env>.<domain>`, issues an ACM certificate
covering that base domain and `*.<env>.<domain>`, and stores the shared HTTPS
listener in the environment root. Without a domain, public ingress falls back
to the AWS-generated ALB DNS name. The Skiff-owned contract is the environment
root and provider resource graph. Both the direct AWS SDK path and the
Terraform emitter use that same contract.

Bootstrap directly through Skiff:

```bash
mkdir -p .skiff-live
cd .skiff-live

skiff bootstrap aws \
  --network managed \
  --ingress private \
  --yes \
  --out bootstrap

export SKIFF_CONFIG="$PWD/.skiffconfig"
skiff config show
```

To use Terraform instead, add `--emit terraform`, then run Terraform's normal
init/apply loop:

```bash
skiff bootstrap aws \
  --network managed \
  --ingress private \
  --emit terraform \
  --out bootstrap

terraform -chdir=bootstrap init
terraform -chdir=bootstrap apply
```

For a public quickstart with DNS and certificates, set the friendly inputs
first and switch ingress to `public`:

```bash
export SKIFF_COMPANY_NAME="Acme"
export SKIFF_DOMAIN_NAME="example.com"

skiff bootstrap aws \
  --network managed \
  --ingress public \
  --yes \
  --out bootstrap
```

That generates one shared internet-facing ALB for the environment, an
HTTP-to-HTTPS redirect listener, an ACM certificate for `quickstart.example.com`
and `*.quickstart.example.com`, Route53 DNS validation records, and alias
records for the base domain and wildcard service hosts. If you already manage
the certificate, pass `--certificate-arn arn:aws:acm:...:certificate/...`; if
Route53 zone discovery is ambiguous, pass `--hosted-zone-id`.

From here, commands that load Skiff config get `mode`, `env`, `provider`,
`region`, object-state URI, and the live-apply setting from the `quickstart`
context.
The deploy path reads VPC, subnet, ingress, and runner defaults from the
bootstrap-written `envs/<env>/root.json` object. That root object includes the
runner AMI SSM parameter `/skiff/runner/ami/al2023/x86_64/stable`. Official
Skiff runner AMIs already contain `skiff-runner` and `skiff-worker`; if you
explicitly choose the AWS public AL2023 fallback parameter, the root object also
contains the pinned runner install metadata used by launch-template user-data.
For public DNS, the root object's `ingress.base_domain` is the environment ingress name,
`ingress.default_host_template` defaults services to
`{service}.quickstart.example.com`, and `provider_dns_name` keeps the raw ALB
hostname visible for debugging.

Initialize a realistic app template:

```bash
skiff init stack api-database orders \
  --dir orders \
  --artifact registry.example.com/orders-api@sha256:REPLACE_WITH_DIGEST \
  --yes

skiff validate orders/skiff.yaml
skiff plan orders/skiff.yaml
```

Before using that stack for production, replace the artifact with a real
digest-pinned image. The plan should show the API service, RDS database,
Secrets Manager secret reference, security groups, launch template, ASG, target
group, logs, and metrics.

Run cost analysis before deploying anything. Refreshing pricing data is a local
file write plus public AWS price-list reads. Cost commands use `AWS_REGION`
when a region flag is not supplied:

Generate a local pricing catalog and get an idea of costs:

```bash
skiff cost pricing update

skiff cost explain orders --file orders/skiff.yaml
```

Prepare a stateless service spec for the live deploy:

```bash
mkdir -p http-hello
cp ../examples/service/http-hello/skiff.yaml http-hello/skiff.yaml
sed -i.bak "s/^  env: prod$/  env: ${SKIFF_ENV}/" http-hello/skiff.yaml
```

Edit `http-hello/skiff.yaml` before deploying:

- Replace `artifact.ref` with a real OCI ref pinned by digest.
- Keep `artifact.digest` consistent with that OCI digest.
- Keep `metadata.env: quickstart` so the service reads the matching bootstrap
  root object.
- Keep `network.ingress.type: private` for the private bootstrap path. If you
  bootstrapped with `--ingress public`, set `network.ingress.type:
  public-http`; the default service host becomes
  `<service>.quickstart.<domain>`, and the ALB listener ARN, certificate, and
  load-balancer security group come from the environment root.

Validate and plan the AWS mapping:

```bash
skiff validate http-hello/skiff.yaml

skiff plan http-hello/skiff.yaml
```

Preflight the mutating deploy path without writing object state:

```bash
skiff deploy http-hello/skiff.yaml --plan-only
```

Deploy only after the plan, cost output, and AWS account boundary look right:

```bash
skiff deploy http-hello/skiff.yaml
```

Skiff writes immutable release/runtime manifests, operation intent/control
objects, service control, resource records, events, and audit records before or
alongside the provider-visible changes they describe. Release IDs, operation
IDs, trace IDs, and the release signing key are generated automatically.
AWS bootstrap defaults to an asymmetric AWS KMS release signing key and writes
only the key reference plus public trust metadata into the Skiff context and
environment root. Runners verify release manifests from that public trust
without access to the signing key. For local-only fallback, bootstrap supports
`--signing-backend keychain`; `--signing-seed-base64` remains an escape hatch
for tests and legacy automation.

Inspect the live service through direct mode:

```bash
skiff status http-hello --fresh

skiff ops events http-hello
```

Record the AWS resource IDs before cleanup or follow-up operations:

```bash
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=skiff.dev/service,Values=http-hello Key=skiff.dev/env,Values="$SKIFF_ENV"
```

Direct Skiff bootstrap writes an AWS CLI teardown script when `--out bootstrap`
is set. It asks you to type the environment name before deleting tagged
quickstart resources and the versioned state bucket:

```bash
bash bootstrap/teardown-aws-cli.sh
```

If you used `--emit terraform`, Terraform cleanup uses Terraform's normal
confirmation prompt:

```bash
terraform -chdir=bootstrap destroy
```
