# Provider and Plugin Conformance

Skiff providers and plugins must pass reusable conformance suites before they are treated as supported.

## Provider Support Bar

Provider implementations live behind `internal/provider` and must not expose cloud SDK types through Skiff APIs. A conforming provider must:

- compile an IR graph into a provider plan with explicit resource changes and Skiff tags
- apply that plan and write resource records into object storage
- inspect services and individual resources with provider IDs visible
- start, watch, and rollback rollouts with provider operation IDs preserved
- return logs, metrics, and debug session metadata
- let the drift detector compare object-state resource records against provider inspection without false drift

Use `tests/conformance/provider.Run` for the shared suite. `internal/provider/fake` is the reference implementation and is wired into CI through `tests/conformance/provider`.

Cloud-backed providers may add optional integration tests, but they must be gated by environment variables, use isolated names or prefixes, tag all resources, and clean up safely.

## Plugin Support Bar

Plugins are trusted extensions that return typed data to the Skiff host. They do not receive raw cloud clients and must not mutate cloud resources directly. A conforming plugin must:

- load and validate a strict `skiff-plugin.json` manifest
- declare permissions matching the hooks it implements
- emit only typed IR patches allowed by its permissions
- produce explainable patches before the host applies them
- return runtime addons only when the runtime addon permission is declared
- return doctor findings through the plugin doctor hook
- register saga step kinds explicitly and support plan, run, resume, compensate, and doctor phases

Use `tests/conformance/plugin.Run` for the shared suite. `internal/plugins/fake` and `tests/fixtures/plugins/fake` are the reference plugin implementation and manifest.

## Running Locally

```bash
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/gomod go test ./tests/conformance/...
GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/gomod go test ./...
```
