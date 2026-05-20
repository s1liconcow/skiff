# Skiff Plugin Authoring

Skiff plugins extend the compiler, runtime preparation, doctor checks, and saga
step catalog without becoming hidden controllers. A plugin returns typed data to
the Skiff host. The host validates permissions, writes durable state when a
workflow mutates production, and keeps provider APIs behind Skiff.

## Manifest

Plugins start with a JSON manifest:

```json
{
  "apiVersion": "skiff.dev/plugin/v1alpha1",
  "kind": "Plugin",
  "name": "mtls-egress",
  "version": "0.1.0",
  "runtime": {
    "kind": "command",
    "command": ["./bin/mtls-egress-plugin"]
  },
  "hooks": ["mutate_ir", "doctor_checks", "saga_step"],
  "permissions": {
    "allowed_patch_kinds": ["SecurityGroupRule"],
    "doctor_checks": true,
    "saga_step_kinds": ["mtls.issue-certificate"],
    "package_step_kinds": ["postgres.verify_replica_lag"]
  },
  "capabilities": [
    {
      "kind": "ir_patch",
      "name": "mtls-security-group",
      "patch_kinds": ["SecurityGroupRule"]
    },
    {
      "kind": "doctor_check",
      "name": "mtls-diagnostics",
      "doctor_checks": ["MTLS_CERTIFICATE_EXPIRING"]
    },
    {
      "kind": "saga_step",
      "name": "issue-certificate",
      "saga_step_kinds": ["mtls.issue-certificate"]
    },
    {
      "kind": "package_step",
      "name": "postgres-safety",
      "package_steps": [
        {
          "kind": "postgres.verify_replica_lag",
          "summary": "verify replica lag before switchover",
          "params": {
            "target": {"type": "string", "required": true},
            "admin_token": {"type": "string", "secret": true}
          },
          "result": {
            "admin_token": {"type": "string", "secret": true}
          },
          "risk": "low",
          "reversibility": "reversible"
        }
      ]
    }
  ]
}
```

The local development runtime is `command`: Skiff sends one JSON object on
stdin with `hook` and `request`, and the plugin returns a hook-specific JSON
response on stdout. The gRPC service shape is defined in `api/proto/plugin.proto`
for packaged plugins.

## Typed Patches

Plugins must emit typed IR patches. They cannot directly mutate cloud resources
or receive broad cloud clients.

Example `mutate_ir` response:

```json
{
  "patches": [
    {
      "op": "add",
      "path": "/resources/security_groups/security-group:payments-api/rules/-",
      "kind": "SecurityGroupRule",
      "value": {
        "direction": "egress",
        "protocol": "tcp",
        "from_port": 8443,
        "destination": "10.0.0.0/8"
      },
      "summary": "allow mTLS egress"
    }
  ]
}
```

The host rejects patch kinds that are not declared in
`permissions.allowed_patch_kinds`. `skiff explain --plugin <path>` shows which
plugin emitted each accepted patch before provider lowering.

## Package Steps

Package steps are typed saga steps exposed by a package plugin. They run through
the `package_step` hook and receive only a JSON request with saga context,
declared params, previous step results, and package provenance. They do not
receive cloud SDK clients. Cloud mutations still go through Skiff provider
steps.

Package step responses can return `succeeded`, `failed`, `waiting`, or
`running`, structured failures with `retriable`, and provider operation IDs that
Skiff persists for resume. Mark secret params and result fields with
`"secret": true`; the host redacts those fields from hook requests, persisted
results, provider operation descriptions, and doctor finding summaries.

## CLI Workflow

```bash
skiff plugin validate ./plugins/mtls --format json
skiff plugin list --plugin ./plugins/mtls --format json
skiff plugin explain ./plugins/mtls --spec examples/service/skiff.yaml --format json
skiff plugin dev --plugin ./plugins/mtls --hook mutate_ir --request request.json --format json
skiff explain examples/service/skiff.yaml --plugin ./plugins/mtls --format json
```

For packaged plugins, configure signed package references with a digest and a
signature reference. The host treats unresolved package refs as declarations
until a registry installer fetches and verifies the package.
