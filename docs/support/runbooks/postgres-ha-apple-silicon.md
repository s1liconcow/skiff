# Runbook: Apple Silicon Postgres HA Package Demo

The Apple Silicon flow has been collapsed into the single provider-parametric
runbook:

[`api-postgres-ha-read-write.md`](api-postgres-ha-read-write.md)

Use the Apple Silicon context/profile at the top of that file. After
`SKIFF_CONTEXT=postgres-ha-apple` is selected, the install, spec generation,
deploy, read/write smoke, and `primary-switchover-update` commands are the same
as the AWS flow.
