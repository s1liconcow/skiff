# Skiff Runner Image

The runner image is the VM base for workload replicas. It must be rebuildable
from this directory and must not depend on `skiffd` for boot.

Included components:

- `skiff-runner` at `/usr/local/bin/skiff-runner`
- systemd unit `skiff-runner.service`
- optional OpenTelemetry collector config at `/etc/skiff/collector.yaml`
- runner user-data at `/etc/skiff/runner.json`

Build flow:

```bash
scripts/build-release.sh v0.1.0
packer init build/runner-image
packer build \
  -var "skiff_version=v0.1.0" \
  -var "artifact_dir=$PWD/dist" \
  build/runner-image/packer.pkr.hcl
```

The image boots exactly one workload replica per VM. User-data supplies object
state, service, env, control key, and region. The runner reads service control
directly from object storage, verifies the signed release/runtime manifests, and
renders the workload systemd unit locally.
