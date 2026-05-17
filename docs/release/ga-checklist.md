# GA Release Checklist

Use this checklist before tagging the first GA release. Every item must have a
named owner and a link to evidence in CI, a readiness report, or a release
artifact.

## Release Gate

| Area | Required evidence | Status |
|---|---|---|
| Build | `make build` produces `skiff`, `skiffd`, `skiff-runner`, and plugin binaries | required |
| Unit and integration tests | `go test ./...` passes on the release commit | required |
| Vet | `go vet ./...` passes on the release commit | required |
| Local e2e | `make e2e-local` passes without cloud credentials | required |
| Readiness | `make readiness` passes and uploads `skiff.readiness/v1` JSON | required |
| Provider conformance | `go test ./tests/conformance/provider` passes | required |
| Plugin conformance | `go test ./tests/conformance/plugin` passes | required |
| Apple silicon e2e | `make e2e-apple-container` passes or is explicitly waived | optional, waiver required |
| AWS e2e | `SKIFF_AWS_E2E=1 make e2e-aws` passes or live cloud proof is explicitly waived | optional, waiver required |

## Product Invariants

- Object storage is durable truth for release, operation, saga, event, audit,
  and control documents.
- `skiffd` is a stateless facade and can rebuild views from object storage.
- Direct CLI mode works for status, events, rollback, operation resume, and saga
  resume.
- Mutating production actions produce audit records with actor, trace ID,
  target, operation or saga ID, risk, and summary.
- Control docs use CAS and embedded leases. No separate lock objects are added.
- Release and runtime manifests are create-only and signed for production use.
- Long-running operations store provider operation IDs before waiting.

## Support Handoff

- Review the support runbooks in [../support/runbooks/README.md](../support/runbooks/README.md).
- Verify issue templates capture trace IDs, object-state paths, provider IDs,
  operation or saga IDs, command output, and cleanup status where relevant.
- Confirm the known limitations section in
  [release-notes-template.md](release-notes-template.md) is filled in for the
  release.
- Confirm [../compatibility.md](../compatibility.md) names the supported CLI,
  `skiffd`, runner, AWS provider, plugin API, and schema versions.
- Confirm [../dev/onboarding.md](../dev/onboarding.md) is current enough for a
  new engineer to run tests, inspect state, and handle a direct recovery drill.

## Sign-Off

| Role | Owner | Evidence |
|---|---|---|
| Release owner | TBD | CI run URL |
| Support owner | TBD | Runbook review notes |
| Security owner | TBD | Approval/audit review |
| Provider owner | TBD | Provider conformance and AWS gate evidence |
| Docs owner | TBD | Docs test run |

Do not tag GA while required evidence is missing. If an optional gate is waived,
record the reason in the release notes.
