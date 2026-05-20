# Skiff Runner Image

The runner image is the VM base for workload replicas. It is rebuilt from this
directory for Amazon Linux 2023 x86_64 and arm64 and must not depend on `skiffd`
for boot.

Included components:

- `skiff-runner` at `/usr/local/bin/skiff-runner`
- systemd unit `skiff-runner.service`
- optional OpenTelemetry collector config at `/etc/skiff/collector.yaml`
- runner user-data at `/etc/skiff/runner.json`
- local runner state and event files under `/var/lib/skiff/runner/`

Build flow:

```bash
scripts/build-release.sh v0.1.0
packer init build/runner-image
packer build \
  -var "skiff_version=v0.1.0" \
  -var "artifact_dir=$PWD/dist" \
  -var "provenance_commit=$(git rev-parse HEAD)" \
  build/runner-image/packer.pkr.hcl
```

The supported release automation is `.github/workflows/runner-image.yml`. It
builds both Linux release archives, live-boots AL2023 builders for x86_64 and
arm64, smoke-checks the installed runner binary against `/etc/skiff/runner.json`,
uploads Packer manifests, and can publish regional SSM parameters when
`publish_ssm` is enabled. When `deprecate_previous` is enabled, it schedules
older Skiff runner AMIs for AWS deprecation while keeping the most recent
published images. Packer tags AMIs and snapshots with:

- `skiff.dev/managed=true`
- `skiff.dev/role=runner`
- `skiff.dev/version=<tag>`
- `skiff.dev/arch=<x86_64|arm64>`
- `skiff.dev/provenance-source`
- `skiff.dev/provenance-commit`

The SSM publication script writes both immutable version parameters and the
selected channel parameter:

```text
/skiff/runner/ami/al2023/x86_64/v0.1.0
/skiff/runner/ami/al2023/x86_64/stable
/skiff/runner/ami/al2023/arm64/v0.1.0
/skiff/runner/ami/al2023/arm64/stable
```

`skiff bootstrap aws` defaults to the x86_64 stable Skiff-owned parameter. For
regions or accounts where an official runner AMI has not been published yet,
pass the AWS public AL2023 fallback explicitly:

```bash
skiff bootstrap aws \
  --env quickstart \
  --region us-west-2 \
  --runner-ami-ssm-parameter /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64
```

That fallback path installs the pinned runner release through cloud-init on
first boot. Official Skiff runner AMIs already contain `skiff-runner` and
`skiff-worker`, so launch-template user-data only writes `/etc/skiff/runner.json`
and starts `skiff-runner.service`.

The image boots exactly one workload replica per VM. User-data supplies object
state, service, env, control key, and region. The runner reads service control
directly from object storage, verifies the signed release/runtime manifests, and
renders the workload systemd unit locally.

Upgrade and rollback happen by changing the environment root or service launch
template to another AMI ID or SSM parameter, then replacing workload VMs through
the normal rollout path. Direct recovery keeps working because the runner reads
object state directly and the CLI can inspect or edit the same control documents
with `skiff --direct --state <uri> ...` when `skiffd` is down.
