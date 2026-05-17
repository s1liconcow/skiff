# Compatibility

Skiff components expose build information through `version` endpoints and CLI
output. Release manifests can declare `min_runner_version`; the runner refuses a
release whose minimum is newer than the running runner binary.

| Component | Compatibility rule |
|---|---|
| CLI -> skiffd | CLI warns when a contacted `skiffd` reports an older semantic version. |
| runner -> release manifest | `min_runner_version` must be empty, `dev`, or less than or equal to the runner version. |
| runner -> schema | release and runtime manifest `schema_version` must equal `skiff.state/v1`. |
| plugins -> host | plugin manifests declare the plugin API version and are validated before hooks run. |

`dev` versions are accepted for local development. Production builds should use
semantic versions such as `1.2.3`.
