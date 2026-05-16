# Skiff E2E Tests

The local runner fixture test is mandatory and runs in normal `go test ./...`.

Real AWS smoke coverage must be explicitly gated before it is added to CI. Use
environment variables such as `SKIFF_AWS_E2E=1`, isolated resource prefixes,
and Skiff tags so normal PRs never create cloud resources.
