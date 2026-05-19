## Define stable agent JSON envelope and schema IDs

### ID
skiff-m5-001

### Priority
P0

### Type
task

### Labels
agent, json, schema, cli, foundation

### Dependencies
None

### Description
Introduce a stable machine-readable response envelope for all agent-facing commands. The current CLI already supports `--format json` and `json-pretty`, but outputs are command-specific and do not consistently include a schema name, mode, freshness, request ID, or normalized error metadata. Agents need a stable envelope so they can parse outputs without consulting help text or command-specific source code.

This bead does not require converting every existing command immediately. It establishes shared types and helpers, then updates the first batch of agent-facing commands: `version`, `status`, `doctor`, `solve`, `events`, `ops`, and future `agent` commands.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Create a reusable envelope model for success and error outputs.
- Add `schema` string to agent-facing success and error envelopes.
- Add `trace_id` consistently. Preserve existing `--trace-id` behavior.
- Add optional `request_id` where available from skiffd responses or generated locally.
- Add optional `mode` for outputs that operate in API/direct mode.
- Add optional `freshness` where the underlying result already exposes it.
- Add optional `warnings` array for non-fatal compatibility or redaction warnings.
- Add optional `recommended_actions` with typed safety metadata.
- Ensure errors include `ok: false`, `schema`, `code`, `summary`, `trace_id`, optional `fields`, optional `sources`, and optional `recommended_actions`.
- Add a small helper that returns canonical schema strings such as:
  - `skiff.error/v1`
  - `skiff.version/v1`
  - `skiff.status/v1`
  - `skiff.doctor/v1`
  - `skiff.agent-action-graph/v1`
  - `skiff.event/v1`
  - `skiff.event-stream-delivery/v1`
- Update json-pretty to preserve the same envelope and only change formatting/color.
- Add golden fixtures for envelope shape.

#### Likely Files
- `internal/cli/json_output.go`
- `internal/cli/command.go`
- `internal/cli/root.go`
- `internal/cli/client_commands.go`
- `internal/cli/doctor.go`
- `internal/cli/solve.go`
- `internal/cli/ops.go`
- `internal/agent/envelope.go` or `internal/cli/envelope.go`
- `internal/agent/schemas.go`
- `internal/client/client.go`
- `internal/skiffd/server.go`
- `internal/cli/*_test.go`
- `internal/agent/*_test.go`

#### Design
Add a small, boring envelope layer. Do not over-abstract too early.

Suggested core structs:

```go
type Envelope[T any] struct {
    OK       bool     `json:"ok"`
    Schema   string   `json:"schema"`
    TraceID  string   `json:"trace_id,omitempty"`
    RequestID string  `json:"request_id,omitempty"`
    Mode     string   `json:"mode,omitempty"`
    Warnings []Warning `json:"warnings,omitempty"`
    Result   T        `json:"result,omitempty"`
}

type ErrorEnvelope struct {
    OK                 bool                `json:"ok"`
    Schema             string              `json:"schema"`
    Code               string              `json:"code"`
    Summary            string              `json:"summary"`
    TraceID            string              `json:"trace_id,omitempty"`
    RequestID          string              `json:"request_id,omitempty"`
    Fields             any                 `json:"fields,omitempty"`
    Sources            map[string]string   `json:"sources,omitempty"`
    RecommendedActions []RecommendedAction `json:"recommended_actions,omitempty"`
}
```

Keep existing command-specific top-level fields for backwards compatibility where needed, but agent commands should prefer the generic `result` field. For old commands, either:
- include both old shape and `schema`, or
- only use the full envelope when `--agent` is enabled.

Prefer not to break scripts that currently expect `{"ok":true,"status":{...}}` from `skiff status --format json`.

A safe compromise:
- Existing commands with plain `--format json` keep current shape plus a `schema` field.
- Commands invoked through `--agent` or `skiff agent ...` use the full envelope with `result`.

#### Testing / Validation
- Add golden tests for JSON output of `version`, `status`, `doctor`, `solve`.
- Add golden tests for common errors:
  - unknown global flag,
  - missing service,
  - config validation failure,
  - unsupported format,
  - API URL required.
- Validate that `json-pretty` changes formatting only.
- Ensure old JSON output remains parseable by tests that emulate existing consumers.
- Ensure `schema` is present in every agent-mode JSON response.

#### Gotchas
- Avoid changing existing output field names without compatibility handling.
- `json-pretty` currently rewrites `--format json-pretty` to `json` internally and wraps stdout; make sure envelope behavior does not depend on the pre-rewrite format.
- Some commands write JSON with `json.NewEncoder` directly. Replace only where necessary in this bead; capture the rest in later tests.
- Do not include empty `result: null` in error responses.

#### Acceptance Criteria
- Agent-facing JSON includes a stable `schema`.
- Error outputs include a stable `skiff.error/v1`-style schema.
- Existing `--format json` consumers are not broken.
- `--format json-pretty` remains human-friendly but machine-equivalent after stripping ANSI color and whitespace.
- Golden tests document the envelope.

---

## Define durable agent object-state schemas and path helpers

### ID
skiff-m5-002

### Priority
P0

### Type
task

### Labels
agent, state, audit, schema, paths, foundation

### Dependencies
skiff-m5-001

### Description
Define the durable object-state layout for agent runs, saved agent context, saved action plans, guarded apply attempts, approval references, and optional LLM prompt/transcript records. This bead fixes a systems-level gap in the first draft: agent-native behavior must not become transient CLI memory or skiffd-only state. The durable state must be create-only where it represents history and must use path helpers rather than ad hoc string concatenation.

This bead does not execute plans or invoke LLMs. It creates the typed documents and path helpers that later beads use.

#### Staff Review
Added during staff review. Without this bead, later agent audit, idempotency, transcript, and replay work could drift into a parallel logging system or skiffd memory. That would violate the project rule that object storage is durable truth and `skiffd` is a rebuildable facade.

#### Subtasks
- Define schema-versioned documents for:
  - `AgentRunRecord`: one durable record per `agent context`, `agent plan`, `agent apply`, `agent ask`, or `agent assist` invocation.
  - `AgentContextSnapshot`: optional saved redacted context bundle with input selectors and redaction summary.
  - `AgentPlanRecord`: saved action graph plus source context hash, planning constraints, and validation status.
  - `AgentApplyAttempt`: durable attempt record for each guarded step execution, including idempotency key, target, actor, policy decision, approval reference, result, and failure summary.
  - `LLMTranscriptRecord`: optional redacted prompt/response metadata and content references; only written when explicitly enabled.
- Add path helpers. Suggested layout:
  - `agent-runs/<run>/run.json`
  - `agent-runs/<run>/context.json`
  - `agent-runs/<run>/plan.json`
  - `agent-runs/<run>/apply/<attempt>.json`
  - `agent-runs/<run>/llm/<transcript>.json`
  - `agent-runs/by-trace/<trace-id>/<run>.json` as optional derived/index-like reference if needed.
- Add `agent-runs` to reserved path segments if the path validator reserves top-level object-state namespaces.
- Make run, attempt, and transcript IDs validate with existing ID rules. Prefer ULID-style or existing Skiff ID conventions.
- Use create-only writes for all agent run/history documents. Do not overwrite agent run records.
- Ensure any large logs/metrics/prompt bodies are either compacted, redacted, or stored behind explicit references with byte limits.
- Add canonical JSON marshal/unmarshal helpers or use existing canonical state helpers.
- Add schema fields that support replay without secrets:
  - command args and normalized selectors,
  - capability IDs,
  - input hashes,
  - redaction policy version,
  - trace ID and request ID,
  - actor and profile,
  - risk budget and approval decision,
  - created_at/completed_at with injected clock in tests.
- Add helper constructors that default `schema_version`, IDs, timestamps, actor, and trace ID.
- Document which fields are safe to expose in CLI/MCP and which are internal audit-only.

#### Likely Files
- `internal/state/paths/paths.go`
- `internal/state/paths/paths_test.go`
- `internal/state/schema/schema.go`
- `internal/state/schema/agent.go` if the schema package is split later
- `internal/agent/state.go`
- `internal/agent/state_test.go`
- `internal/state/canonical/*`
- `docs/bead_plans/skiff_beads_m5_agent_native.md`

#### Design
Agent run records are immutable historical records, not control documents. They should be written with object-store `Create`, not `CompareAndSwap`.

Example run record:

```json
{
  "schema_version": "skiff.agent-run/v1",
  "run_id": "arun_01J...",
  "kind": "agent.plan",
  "service": "payments-api",
  "env": "prod",
  "actor": {"id":"sre-bot","type":"agent"},
  "profile": "operator",
  "trace_id": "tr_01J...",
  "request_id": "req_01J...",
  "input_hash": "sha256:...",
  "context_ref": "agent-runs/arun_01J.../context.json",
  "plan_ref": "agent-runs/arun_01J.../plan.json",
  "redaction_policy": "skiff.redaction/v1",
  "created_at": "2026-05-19T18:00:00Z",
  "completed_at": "2026-05-19T18:00:02Z",
  "status": "succeeded"
}
```

Example apply attempt:

```json
{
  "schema_version": "skiff.agent-apply-attempt/v1",
  "attempt_id": "aat_01J...",
  "run_id": "arun_01J...",
  "plan_id": "plan_01J...",
  "step_id": "rollback_to_stable",
  "capability": "rollback.start",
  "idempotency_key": "incident-123-rollback",
  "target": {"kind":"service","name":"payments-api"},
  "actor": {"id":"sre-bot","type":"agent"},
  "risk": "medium",
  "reversibility": "reversible",
  "policy_decision": "approval_required",
  "approval_ref": "sagas/saga_01J.../events/01J...-approved.json",
  "operation_id": "op_01J...",
  "saga_id": "saga_01J...",
  "created_at": "2026-05-19T18:01:00Z",
  "status": "submitted"
}
```

Do not store plaintext prompts containing secrets. Prompt/transcript documents must be redacted by `skiff-m5-020` before persistence and must be opt-in.

#### Testing / Validation
- Unit test every path helper, including invalid names, invalid IDs, path traversal attempts, reserved segments, and empty IDs.
- Unit test canonical JSON output for each new document type.
- Unit test create-only semantics with memory object store: repeated `Create` for the same run/attempt must fail.
- Unit test that redaction metadata is mandatory for context/transcript documents.
- Unit test that no document constructor accepts nil/empty actor for mutating apply attempts.
- Add golden JSON fixtures for run, context, plan, apply attempt, and transcript metadata.

#### Gotchas
Do not introduce a new durable database, SQLite file, local history directory, or skiffd-only memory log. Do not hand-concatenate object keys outside `internal/state/paths`. Do not store raw unredacted logs or prompts by default. Do not make agent run records mutable control documents; they are immutable history.

#### Acceptance Criteria
- Agent run/history object-state schemas are defined and canonical.
- Path helpers exist for all agent run artifacts.
- Tests prove invalid paths are rejected and create-only semantics are used.
- Later beads can persist context, plans, apply attempts, audit records, and LLM transcripts without inventing a parallel storage model.

---

## Add global `--agent` mode

### ID
skiff-m5-003

### Priority
P0

### Type
task

### Labels
agent, cli, flags, ergonomics

### Dependencies
skiff-m5-001

### Description
Add an explicit global `--agent` mode so users and tools can opt into agent-safe behavior without remembering a bundle of flags. Today agents must know to pass `--format json`, `--no-color`, `--trace-id`, and command-specific flags. `--agent` should make agent execution obvious and safe by default.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add `Agent bool` and `agentSet bool` to `rootOptions`.
- Parse global `--agent` and `--agent=<bool>` in `internal/cli/root.go`.
- Add env var support for `SKIFF_AGENT=1` if config/env loading supports such global switches. If not, implement only CLI flag now and document env var as a follow-up.
- When `--agent` is enabled:
  - default `Format` to `json` unless explicitly overridden;
  - treat `json-pretty` as display-only and never as an agent parser format;
  - default `NoColor` to true;
  - default non-interactive behavior;
  - include schema/envelope fields from `skiff-m5-001`;
  - generate a trace ID if none was supplied;
  - prefer fresh reads for agent-specific context/plan commands;
  - do not imply `--yes`.
- Add a `--non-interactive` flag if current commands need a clearer distinction from `--yes`.
- Ensure command-specific `--format` still wins if explicitly passed.
- Add `--agent` to help text, global flags help, and shell completion.
- Add tests for precedence:
  - `skiff --agent doctor svc` uses JSON;
  - `skiff --agent --format human doctor svc` is either rejected or explicitly allowed according to final design;
  - `skiff --format json --agent doctor svc` remains JSON;
  - `skiff --agent --no-color=false ...` does not emit color in JSON.

#### Likely Files
- `internal/cli/root.go`
- `internal/cli/command.go`
- `internal/cli/client_commands.go`
- `internal/cli/json_output.go`
- `internal/cli/doctor.go`
- `internal/cli/solve.go`
- `internal/cli/ops.go`
- `internal/cli/*_test.go`

#### Design
`--agent` is a mode switch, not just a formatting alias.

Suggested semantics:

```text
--agent
  implies --format=json
  implies --no-color
  implies non-interactive
  does not imply --yes
  generates trace_id when omitted
  enables strict agent envelopes where supported
```

Generated trace IDs can be simple and deterministic enough in tests via injected clock or test hook:

```text
tr_<timestamp_or_random_suffix>
```

If deterministic generation is difficult, make tests assert non-empty pattern rather than exact value.

#### Testing / Validation
- Add parser tests to `parseRootArgs`.
- Add command integration tests using stdout/stderr buffers.
- Verify `--agent` before and after command works only if global flags are intentionally supported before commands. Do not silently accept global flags after subcommands unless existing parser already does.
- Verify unknown command in `--agent` mode returns JSON error instead of human usage.

#### Gotchas
- Current root parser stops at first non-flag command. Global flags after the command are parsed by command-specific flag sets, not by root. Be explicit in docs.
- Do not make `--agent` imply `--yes`; that would let agents mutate without approvals.
- Do not assume all commands can immediately honor agent mode. Unsupported cases should return a JSON error with a recommended action, not human stderr.

#### Acceptance Criteria
- `--agent` is documented in global help.
- Agent mode produces JSON by default.
- Agent mode never produces ANSI color.
- Agent mode does not auto-confirm mutations.
- Agent mode has tests for flag precedence and error behavior.

---

## Add JSONL streaming format for event and operation watches

### ID
skiff-m5-004

### Priority
P0

### Type
task

### Labels
agent, events, streaming, jsonl, cli

### Dependencies
skiff-m5-001

### Description
Make streaming outputs explicit by adding `--format jsonl` for watch-style commands. Today watch commands can emit a sequence of JSON objects under `--format json`, which is workable but ambiguous. Agents should know whether a command returns a single JSON document or newline-delimited stream.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add `jsonl` as a supported output format for watch commands only.
- Update format validation helpers to understand `jsonl` separately from single-document JSON.
- Update `skiff events --watch`, `skiff ops watch`, and any saga watch path to support `jsonl`.
- Preserve existing `--format json` streaming behavior if current tests or users rely on it, but document `jsonl` as preferred for agents.
- Emit one JSON object per line, no arrays, no commas, no final summary envelope required.
- Each line should include:
  - `ok`,
  - `schema`,
  - `trace_id`,
  - `event` or error/resync metadata,
  - `last_event_id`,
  - optional `resume_command`.
- For resync required events, emit `ok:false`, `code:"RESYNC_REQUIRED"`, and `summary`.
- Add tests that split stdout by newline and decode each line independently.

#### Likely Files
- `internal/cli/json_output.go`
- `internal/cli/command.go`
- `internal/cli/ops.go`
- `internal/client/api.go`
- `internal/client/client.go`
- `internal/events/*`
- `internal/cli/events*_test.go`
- `internal/cli/ops*_test.go`

#### Design
Define constants:

```go
const (
    formatJSON = "json"
    formatJSONPretty = "json-pretty"
    formatJSONL = "jsonl"
)
```

Do not treat `jsonl` as a normal JSON format in `isJSONFormat` if that helper is used to select single-document envelopes. Consider adding:

```go
func isMachineFormat(format string) bool
func isSingleJSONFormat(format string) bool
func isStreamJSONFormat(format string) bool
```

Line example:

```json
{"ok":true,"schema":"skiff.event-stream-delivery/v1","trace_id":"tr_...","event":{"id":"evt_..."},"last_event_id":"evt_..."}
```

Resync example:

```json
{"ok":false,"schema":"skiff.event-stream-delivery/v1","code":"RESYNC_REQUIRED","summary":"event stream subscriber fell behind; reconnect with --after evt_123","last_event_id":"evt_123","resume_command":"skiff events --watch --after evt_123 --format jsonl"}
```

#### Testing / Validation
- Unit test format helpers.
- Integration test watch with fake event stream.
- Test resync required line shape.
- Test that `json-pretty` is rejected for watch commands if it cannot safely represent a stream.
- Test that `--agent` selects `jsonl` for watch commands only if command is explicitly watch-oriented.

#### Gotchas
- Do not pretty-print JSONL. Each event must be one physical line.
- Do not emit logs or human status lines to stdout in JSONL mode.
- Be careful with current `prettyJSONWriter` buffering; it is inappropriate for JSONL.
- Make sure stderr remains reserved for diagnostics only and never interleaves with stdout data.

#### Acceptance Criteria
- Agents can run watch commands with `--format jsonl`.
- Each output line decodes independently as JSON.
- Resync is machine-readable and includes a resume command.
- Existing non-watch JSON behavior is unchanged.

---

## Implement schema registry and schema export command

### ID
skiff-m5-005

### Priority
P0

### Type
task

### Labels
agent, schemas, json-schema, contracts

### Dependencies
skiff-m5-001

### Description
Create a registry of stable schema IDs and add a command to list and export schemas. Agents need to discover and validate Skiff outputs without reading Go source. This bead is a foundation for capabilities, MCP, OpenAPI, contract tests, and external tool integration.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define schema IDs and versions in a central package.
- Add `skiff agent schemas list --format json`.
- Add `skiff agent schemas get <schema-id> --format json`.
- Add JSON Schemas for:
  - error envelope,
  - warning,
  - recommended action,
  - status output,
  - doctor output,
  - action graph,
  - action step,
  - API operation,
  - expected validation,
  - event stream delivery,
  - agent context bundle,
  - capabilities output.
- Use hand-authored JSON Schemas initially if code generation is too much for M4.
- Add schema files under versioned paths.
- Add tests that every advertised schema can be loaded and parsed as JSON.
- Add tests that golden command outputs validate against schemas where feasible.

#### Likely Files
- `internal/agent/schema_ids.go`
- `internal/agent/schemas.go`
- `internal/agent/schema_registry.go`
- `internal/cli/agent.go`
- `schemas/skiff/*.schema.json` or `internal/agent/schemas/*.json`
- `docs/agents/schemas.md`
- `internal/agent/schema_registry_test.go`
- `internal/cli/agent_schemas_test.go`

#### Design
Schema IDs should be concise and stable:

```text
skiff.error/v1
skiff.warning/v1
skiff.recommended-action/v1
skiff.status/v1
skiff.doctor/v1
skiff.agent-capabilities/v1
skiff.agent-context/v1
skiff.agent-action-graph/v1
skiff.agent-action-step/v1
skiff.agent-api-operation/v1
skiff.expected-validation/v1
skiff.event-stream-delivery/v1
```

The CLI output for `schemas list` should include enough metadata for tools:

```json
{
  "ok": true,
  "schema": "skiff.schema-list/v1",
  "schemas": [
    {
      "id": "skiff.agent-action-graph/v1",
      "kind": "json-schema",
      "description": "Agent recovery/action plan graph",
      "command": "skiff agent schemas get skiff.agent-action-graph/v1 --format json"
    }
  ]
}
```

Keep schema files vendored in repo so there is no network dependency.

#### Testing / Validation
- Parse every schema as JSON in tests.
- Check every schema ID advertised by capabilities exists in registry.
- Validate at least one golden output per schema with a JSON Schema validator if adding a validator is acceptable.
- If no validator dependency is desired, add structural smoke tests and document manual validation.

#### Gotchas
- Go `omitempty` and JSON Schema `required` can drift. Do not over-constrain optional fields.
- Do not expose internal-only fields as stable if they may change.
- Avoid schema IDs that include Go package names.
- Version schemas only when shape changes incompatibly; do not put date stamps in schema names.

#### Acceptance Criteria
- `skiff agent schemas list --format json` works.
- `skiff agent schemas get skiff.agent-action-graph/v1 --format json` works.
- Capabilities and agent outputs reference existing schema IDs.
- Tests fail if an advertised schema is missing.

---

## Build agent capability registry

### ID
skiff-m5-006

### Priority
P0

### Type
task

### Labels
agent, capabilities, tools, discovery

### Dependencies
skiff-m5-005

### Description
Implement a typed registry of Skiff operations that agents can discover. Capabilities are the machine-readable version of CLI help: they describe available tools, input schemas, output schemas, mutability, risk, reversibility, approval requirements, and command/API renderings.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Create `Capability` and `CapabilityRegistry` types.
- Add `skiff agent capabilities --format json`.
- Add capability entries for read-only operations:
  - `version.get`
  - `status.get`
  - `doctor.diagnose`
  - `events.list`
  - `events.watch`
  - `logs.query`
  - `metrics.query`
  - `ops.list`
  - `ops.inspect`
  - `config.show`
- Add capability entries for planned/guarded operations:
  - `agent.context`
  - `agent.plan`
  - `agent.validate_plan`
  - `agent.apply`
  - `rollback.start`
  - `ops.approve`
  - `ops.resume`
  - stateful recovery actions where supported.
- Include rendered CLI examples and future API operation descriptors.
- Include required auth/profile hints where applicable.
- Include whether capability is available in `api`, `direct`, or both.
- Include `stability` field: `stable`, `experimental`, or `deprecated`.
- Include `tags` to support filtering: `read`, `mutating`, `diagnostic`, `recovery`, `stateful`, `release`.
- Add filtering flags:
  - `--mode api|direct`
  - `--mutating true|false`
  - `--tag <tag>`
  - `--profile <agent-profile>` once profiles exist.
- Add tests for registry completeness and uniqueness.

#### Likely Files
- `internal/agent/capability.go`
- `internal/agent/capability_registry.go`
- `internal/agent/schema_ids.go`
- `internal/cli/agent.go`
- `internal/cli/command.go`
- `internal/cli/completion` logic in `internal/cli/client_commands.go` or new file
- `docs/agents/capabilities.md`
- `internal/agent/capability_test.go`
- `internal/cli/agent_capabilities_test.go`

#### Design
Suggested shape:

```go
type Capability struct {
    Name             string
    Summary          string
    Description      string
    InputSchema      string
    OutputSchema     string
    Mutating         bool
    Risk             schema.Risk
    Reversibility    schema.Reversibility
    RequiresApproval bool
    Modes            []config.Mode
    Stability        string
    Tags             []string
    CLI              CLIRendering
    API              *APIRendering
}
```

JSON example:

```json
{
  "name": "doctor.diagnose",
  "summary": "Diagnose service health and recommend actions",
  "input_schema": "skiff.doctor-diagnose-input/v1",
  "output_schema": "skiff.doctor/v1",
  "mutating": false,
  "risk": "low",
  "reversibility": "reversible",
  "requires_approval": false,
  "modes": ["api", "direct"],
  "cli": {
    "template": "skiff doctor {service} --fresh --format json",
    "argv": ["skiff", "doctor", "{service}", "--fresh", "--format", "json"]
  },
  "api": {
    "method": "GET",
    "path": "/v1/doctor",
    "query": {"service": "{service}", "fresh": "true"}
  }
}
```

Capabilities must be generated from one registry used by:
- CLI `agent capabilities`;
- MCP tool generation;
- OpenAPI hints where useful;
- plan validation.

#### Testing / Validation
- Registry has no duplicate names.
- Every capability references registered input/output schema IDs.
- Every CLI argv starts with `skiff` placeholder or uses binary substitution.
- Mutating capabilities include non-empty risk and reversibility.
- Read-only capabilities never require approval unless intentionally policy-gated.
- Golden test `skiff agent capabilities --format json`.

#### Gotchas
- Do not make command strings authoritative. Capabilities should contain typed operation identity.
- Do not list mutating capabilities as executable until idempotency and approval beads are done. Mark them `stability:"planned"` or `requires_approval:true`.
- Be careful that direct mode and API mode may not support identical capabilities.

#### Acceptance Criteria
- Agents can discover Skiff tools through `skiff agent capabilities`.
- Each capability declares schema, mutability, risk, approval, and mode support.
- Registry is test-covered and later reusable by MCP/OpenAPI.

---

## Build typed capability executor registry

### ID
skiff-m5-007

### Priority
P0

### Type
task

### Labels
agent, capabilities, executor, safety, cli, api

### Dependencies
skiff-m5-005

skiff-m5-006

### Description
Build a typed executor registry that connects discoverable capabilities to safe execution paths. The capability registry describes what exists; the executor registry defines how Skiff actually invokes a capability without shelling out or scraping command strings. This bead fixes a systems-level risk in the first draft: `skiff agent apply`, MCP mutating tools, and skiffd agent endpoints must not execute rendered shell commands as their primary mechanism.

The executor registry must support both CLI/direct mode and skiffd/API mode while preserving Skiff's invariants: object storage durable truth, direct recovery, typed sagas, idempotency, auditability, and clear provider errors.

#### Staff Review
Added during staff review. Capabilities alone are not enough. Without an executor registry, later code would be tempted to run `step.command` or `/bin/sh -c`, which would be unsafe, hard to audit, brittle, and hostile to direct-mode recovery.

#### Subtasks
- Define an internal capability executor interface. Suggested shape:

```go
type Executor interface {
    CapabilityID() string
    Validate(ctx context.Context, input any, policy ExecutionPolicy) ([]Finding, error)
    Plan(ctx context.Context, input any, opts PlanOptions) (*ExecutionPlan, error)
    Execute(ctx context.Context, input any, opts ExecuteOptions) (*ExecutionResult, error)
    RenderCommand(input any, opts RenderOptions) string
}
```

- Add typed input structs for core capabilities that appear in current doctor/solve output:
  - `status.get`
  - `events.list`
  - `events.watch`
  - `logs.query`
  - `metrics.query`
  - `doctor.diagnose`
  - `rollout.watch`
  - `rollback.start`
  - `stateful.status`
  - `stateful.logs`
  - `stateful.metrics`
  - `stateful.snapshot`
  - `stateful.replace_member`
  - `stateful.resume`
- Add typed output structs or references to existing CLI/client/domain output types.
- Keep `RenderCommand` as a convenience for humans and compatibility only. It must not be the source of truth for execution.
- Route read-only capabilities through `client.Interface` where possible so API/direct modes share behavior.
- Route mutating capabilities through existing operation/saga/start/resume APIs or domain functions, not raw provider calls.
- Add a dry-run/planning path for mutating operations where available.
- Add executor metadata for mutability, risk, reversibility, required approvals, idempotency requirements, supported modes, and direct-mode support.
- Add validation that an executor cannot be registered with mismatched capability metadata.
- Add clear errors for unsupported mode, missing direct-mode config, unsupported provider, approval missing, idempotency key missing, and unsafe risk budget.
- Ensure future MCP and skiffd agent endpoints call the same executor registry as the CLI.

#### Likely Files
- `internal/agent/capabilities.go`
- `internal/agent/executor.go`
- `internal/agent/executor_test.go`
- `internal/agent/executors/status.go`
- `internal/agent/executors/doctor.go`
- `internal/agent/executors/events.go`
- `internal/agent/executors/observability.go`
- `internal/agent/executors/rollback.go`
- `internal/agent/executors/stateful.go`
- `internal/cli/agent.go`
- `internal/client/client.go`
- `internal/cli/rollback.go`
- `internal/cli/stateful.go`
- `internal/ops/*`
- `internal/saga/*`

#### Design
`ActionStep` should eventually contain both:

```json
{
  "api_operation": {
    "operation": "rollback.start",
    "target": {"kind":"service","name":"payments-api"},
    "params": {"to":"2026.05.15.3"},
    "mutating": true
  },
  "command": "skiff rollback payments-api --to 2026.05.15.3 --format json"
}
```

The executor consumes `api_operation`, not `command`. The command is still useful for humans, logs, recommended actions, and compatibility.

For CLI mode, the executor can call the same internal functions used by existing CLI commands. If an existing command has all logic inline in `internal/cli/*.go`, refactor only enough to expose a small domain helper. Do not create huge abstractions all at once.

For skiffd/API mode, the executor should call typed client/server methods. If a server endpoint does not yet exist, return a clear unsupported-mode error until `skiff-m5-032` adds it.

#### Testing / Validation
- Unit test registry rejects duplicate capability IDs.
- Unit test every core executor validates required fields and mode support.
- Unit test read-only executors never require approval or idempotency keys.
- Unit test mutating executors require idempotency keys once `skiff-m5-015` lands.
- Unit test `RenderCommand` output is stable but not used during `Execute`.
- Integration test a fake direct-mode rollback executor writes/returns operation IDs without shelling out.
- Integration test unsupported API mode for a not-yet-implemented mutating endpoint returns a clear `UNSUPPORTED_AGENT_MODE` style error.

#### Gotchas
Do not call `/bin/sh -c`. Do not tokenize and execute rendered commands. Do not duplicate business logic in MCP or skiffd handlers. Do not bypass existing saga/operation audit paths for mutating operations. Keep provider-specific execution behind provider/domain interfaces.

#### Acceptance Criteria
- Skiff has a typed executor registry for core agent capabilities.
- Rendered commands remain compatibility artifacts, not execution primitives.
- `agent apply`, MCP tools, and skiffd agent endpoints have a single safe execution substrate to depend on.
- Tests prove shell command strings are not used for execution.

---

## Replace command-string inference with typed recommended actions

### ID
skiff-m5-008

### Priority
P0

### Type
task

### Labels
agent, doctor, actions, typed-operations, safety

### Dependencies
skiff-m5-006

skiff-m5-007

### Description
Make recommended actions typed and authoritative. Current solve logic derives an `APIOperation` by scanning rendered command strings for substrings such as `rollback`, `logs`, `metrics`, and `stateful`. That is fragile for agents. Agents need a typed operation model where command strings are only renderings.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Locate the doctor/result types that define `RecommendedAction`.
- Extend `RecommendedAction` to include:
  - `kind` or `operation`,
  - `target`,
  - `params`,
  - `api_operation`,
  - `expected_validation`,
  - `risk`,
  - `reversibility`,
  - `requires_approval`,
  - `safety`.
- Keep existing `command` field for human rendering.
- Update doctor diagnosis code to populate typed operation fields directly.
- Update `internal/agent/solve.go` so `stepFromAction` copies `action.APIOperation` instead of inferring from command text.
- Leave command-string inference as temporary fallback only, with a warning or test marker.
- Add a TODO or deprecation comment that fallback must be removed after all action producers become typed.
- Update JSON schema for recommended actions.
- Add tests for each action family:
  - inspect status,
  - inspect logs,
  - inspect metrics,
  - events,
  - rollback,
  - rollout watch,
  - stateful status,
  - stateful logs,
  - stateful snapshot,
  - stateful replace-member,
  - doctor rerun.

#### Likely Files
- `internal/doctor/*`
- `internal/agent/solve.go`
- `internal/agent/action_graph.go`
- `internal/agent/capability_registry.go`
- `internal/state/schema/*`
- `internal/cli/doctor.go`
- `internal/cli/solve.go`
- `internal/agent/*_test.go`
- `internal/doctor/*_test.go`

#### Design
Preferred model:

```go
type RecommendedAction struct {
    ID                 string
    Kind               string
    Summary            string
    Command            string
    APIOperation       *agent.APIOperation
    Mutating           bool
    Safety             string
    Risk               schema.Risk
    Reversibility      schema.Reversibility
    RequiresApproval   bool
    ExpectedValidation []agent.ExpectedValidation
}
```

If importing `internal/agent` from `internal/doctor` creates a package cycle, move shared action operation types into a neutral package, for example:

```text
internal/action
internal/operation
internal/control
```

Suggested package split:

```text
internal/control/types.go
  Operation
  Target
  ExpectedValidation
  RecommendedAction
```

Then both `doctor` and `agent` can import `internal/control`.

#### Testing / Validation
- Unit test that `agent.Solve` does not need command parsing for typed actions.
- Keep a test for fallback inference so legacy actions still work until migration is complete.
- Golden-test doctor JSON output with typed action metadata.
- Golden-test solve output for one read-only and one mutating action.

#### Gotchas
- Avoid package import cycles.
- Do not remove `command` because human output and existing scripts may use it.
- Preserve existing JSON field names where possible.
- Risk/reversibility defaults must remain conservative. Missing risk on a mutating action should default to `medium` or fail validation.

#### Acceptance Criteria
- New doctor recommended actions include typed operation metadata.
- `agent.Solve` prefers typed operation metadata.
- Command-string parsing is fallback only.
- Tests prove at least one mutating and one read-only action are typed end-to-end.

---

## Stabilize ActionGraph v1

### ID
skiff-m5-009

### Priority
P0

### Type
task

### Labels
agent, action-graph, plan, contracts

### Dependencies
skiff-m5-008

### Description
Turn the existing `ActionGraph` into a stable v1 agent plan contract. The current graph already contains excellent fields such as goal, status, confidence, facts, findings, hypotheses, steps, API operation, safety, risk, reversibility, dependencies, and expected validation. This bead formalizes it as the primary agent plan output.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add `schema:"skiff.agent-action-graph/v1"` to the graph output envelope.
- Add `graph_id` or `plan_id` generated deterministically from service, goal, trace ID, and findings where possible.
- Add `created_at` only with injectable clock.
- Add `input` field that records service, goal, fresh, max risk, and source command.
- Add `policy` or `constraints` field to record max risk, allowed mutating, profile, and approval behavior.
- Add `summary` field for a one-sentence plan summary.
- Add `status` enum validation:
  - `no_action`
  - `plan_ready`
  - `approval_required`
  - `blocked`
  - `validation_failed`
- Add `blocked_reasons` for missing context, unsupported goal, missing permission, or policy denial.
- Ensure every step has:
  - stable `id`,
  - `kind`,
  - `operation`,
  - `target`,
  - `params`,
  - `mutating`,
  - `risk`,
  - `reversibility`,
  - `requires_approval`,
  - `requires`,
  - `expected_validation`.
- Add graph-level `recommended_next_command`.
- Add graph validation helper used by tests and `skiff agent validate`.
- Update JSON schema.

#### Likely Files
- `internal/agent/action_graph.go`
- `internal/agent/solve.go`
- `internal/agent/validate.go`
- `internal/agent/schema_ids.go`
- `internal/cli/solve.go`
- `internal/cli/agent.go`
- `internal/agent/*_test.go`

#### Design
The graph is the typed plan. It should be safe to save it to disk and later pass it to `skiff agent apply`.

Example shape:

```json
{
  "schema": "skiff.agent-action-graph/v1",
  "plan_id": "plan_01J...",
  "goal": "restore-health",
  "status": "approval_required",
  "confidence": 0.84,
  "service": "payments-api",
  "health": "critical",
  "constraints": {
    "max_risk": "medium",
    "allow_mutating": false,
    "profile": "readonly"
  },
  "steps": [
    {
      "id": "inspect_logs",
      "kind": "logs.query",
      "summary": "Inspect recent service logs",
      "api_operation": {
        "operation": "logs.query",
        "target": {"kind":"service","name":"payments-api"},
        "params": {"since":"20m"},
        "mutating": false
      },
      "mutating": false,
      "risk": "low",
      "reversibility": "reversible",
      "requires_approval": false,
      "requires": []
    }
  ]
}
```

#### Testing / Validation
- Unit test graph ID generation if deterministic.
- Unit test graph validation catches:
  - duplicate step IDs,
  - missing dependencies,
  - mutating step without risk,
  - approval-required step missing approval metadata,
  - unknown operation kind.
- Golden-test `skiff solve <svc> --format json`.
- Golden-test `skiff agent plan <svc> --format json` after the agent namespace bead.

#### Gotchas
- Do not add non-deterministic IDs that make golden tests brittle unless test hooks exist.
- Do not remove existing fields unless compatibility bead handles migration.
- Keep graphs serializable without requiring access to live clients.

#### Acceptance Criteria
- ActionGraph is documented as `skiff.agent-action-graph/v1`.
- Every generated graph validates locally.
- Graph output includes enough metadata for `agent apply`.
- Existing `solve` behavior still works.

---

## Add top-level `agent` command namespace

### ID
skiff-m5-010

### Priority
P0

### Type
task

### Labels
agent, cli, help, ux

### Dependencies
skiff-m5-003

skiff-m5-006

skiff-m5-009

### Description
Promote agent functionality out of hidden/dev-ish commands into an obvious `skiff agent` namespace. Keep `solve` as a compatibility alias, but make `agent` the primary product surface.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add `agent` to root command dispatch in `internal/cli/command.go`.
- Add root help entry:
  - `agent     Machine-readable context, plans, tools, and guarded actions for AI agents`
- Add `skiff help agents` or `skiff help agent`.
- Implement `runAgent`.
- Implement subcommands:
  - `skiff agent capabilities`
  - `skiff agent schemas list`
  - `skiff agent schemas get <schema-id>`
  - `skiff agent plan <service>`
  - stub `skiff agent context <service>` if `skiff-m5-011` is not complete yet;
  - stub `skiff agent validate <plan.json>` if `skiff-m5-014` is not complete yet.
- Ensure `skiff agent plan` delegates to solve graph logic.
- Add `solve` alias note in help:
  - `solve      Alias for agent plan; kept for compatibility`
- Update completions to include `agent`.
- Add tests for help and command dispatch.

#### Likely Files
- `internal/cli/command.go`
- `internal/cli/agent.go`
- `internal/cli/solve.go`
- `internal/cli/client_commands.go`
- `internal/agent/*`
- `internal/cli/*_test.go`
- `docs/agents/README.md`

#### Design
Initial command tree:

```text
skiff agent capabilities [--mode api|direct] [--mutating true|false] [--tag <tag>]
skiff agent schemas list
skiff agent schemas get <schema-id>
skiff agent plan <service> [--goal restore-health] [--fresh] [--max-risk medium]
skiff agent context <service> [--since 30m] [--max-events 50]
skiff agent validate <plan.json>
skiff agent apply <plan.json> --step <step-id>
skiff agent ask <service> --prompt <text>
skiff agent assist <service> --prompt <text>
```

Only implement subcommands whose dependencies are complete. Other subcommands can return a clear JSON error in agent mode with a recommended action, but avoid shipping long-lived stubs if they confuse users.

#### Testing / Validation
- `skiff agent --help` prints agent usage.
- `skiff help agent` prints agent usage.
- `skiff agent capabilities --format json` returns capability registry.
- `skiff agent plan <svc> --format json` matches `solve` graph semantics.
- Unknown agent subcommand returns JSON error under `--agent`.

#### Gotchas
- Current root parser treats `help` specially; ensure `skiff agent help` and `skiff help agent` both work.
- Keep command parsing consistent with existing splitArgs patterns.
- Do not overfit to API mode; direct mode is an important break-glass path.

#### Acceptance Criteria
- `agent` is visible in top-level help.
- `skiff agent plan` works.
- `solve` remains usable.
- Capabilities and schemas are reachable under `agent`.
- Tests cover help, dispatch, and unknown subcommands.

---

## Implement agent context bundles

### ID
skiff-m5-011

### Priority
P0

### Type
task

### Labels
agent, context, diagnostics, observability

### Dependencies
skiff-m5-001

skiff-m5-010

### Description
Create `skiff agent context <service>` to return a compact, redacted, agent-ready context bundle. Agents should not have to manually run `status`, `doctor`, `events`, `ops`, `logs`, and `metrics` just to understand a service. Context bundles should reduce tool-call thrash while preserving byte limits, redaction, direct-mode support, and source/freshness metadata. The context bundle is the canonical input for planning, LLM prompt generation, MCP resources, and incident handoffs.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define `AgentContext` type.
- Implement `skiff agent context <service> --format json`.
- Include:
  - service identity,
  - env/provider/region,
  - status,
  - doctor diagnosis,
  - facts,
  - findings,
  - hypotheses,
  - recommended actions,
  - recent events,
  - active operations,
  - active sagas,
  - freshness,
  - redaction summary,
  - commands used to gather context,
  - warnings.
- Add flags:
  - `--since <duration>` default `30m`,
  - `--max-events <n>` default `50`,
  - `--include status,doctor,events,ops,logs,metrics`,
  - `--exclude <component>`,
  - `--compact`,
  - `--max-bytes <n>`,
  - `--fresh`,
  - `--redact=true`.
- Use existing client APIs where available.
- For data not available through client API yet, return a clear warning instead of failing the entire context bundle.
- Ensure `--agent` defaults `--format json`, `--fresh`, and redaction on.
- Add schema `skiff.agent-context/v1`.
- Add golden tests.

#### Likely Files
- `internal/agent/context.go`
- `internal/agent/context_builder.go`
- `internal/agent/redaction.go`
- `internal/cli/agent.go`
- `internal/client/client.go`
- `internal/client/api.go`
- `internal/cli/client_commands.go`
- `internal/cli/ops.go`
- `internal/skiffd/server.go` later for HTTP endpoint
- `internal/agent/context_test.go`
- `internal/cli/agent_context_test.go`

#### Design
Context should be a bundle, not a dump.

Example:

```json
{
  "ok": true,
  "schema": "skiff.agent-context/v1",
  "trace_id": "tr_...",
  "result": {
    "service": "payments-api",
    "env": "prod",
    "provider": "aws",
    "region": "us-east-1",
    "window": {"since": "30m"},
    "status": {},
    "doctor": {},
    "recent_events": [],
    "active_operations": [],
    "active_sagas": [],
    "redactions": [
      {"kind":"secret","summary":"secret values omitted from logs and config"}
    ],
    "warnings": [
      {"code":"METRICS_UNAVAILABLE","summary":"metrics query not configured for this service"}
    ]
  }
}
```

Keep full raw data available when it is safe, but provide compact summaries for prompt usage.

#### Testing / Validation
- Unit test include/exclude parsing.
- Unit test max events and max bytes behavior.
- Unit test redaction removes known secret patterns.
- Golden-test minimal context with fake client.
- Test warnings for unavailable components.
- Test `--compact` output is smaller but still includes service, health, findings, and recommended actions.

#### Gotchas
- Do not let logs or events leak secrets into context bundles.
- Avoid making context fail if one optional component fails. Use warnings unless core status/doctor fails.
- Be explicit about freshness. Agents must know whether data is cached.
- Logs/metrics may be provider-specific; do not block this bead on perfect observability coverage.

#### Acceptance Criteria
- `skiff agent context <service> --format json` works.
- Bundle includes status, doctor, recent events, and warnings.
- Bundle is redacted by default.
- Bundle is schema-tagged and golden-tested.

---

## Implement redaction and context safety layer

### ID
skiff-m5-012

### Priority
P0

### Type
task

### Labels
agent, redaction, safety, llm, secrets

### Dependencies
skiff-m5-011

### Description
Before sending context to agents, MCP clients, or LLMs, Skiff needs a reusable redaction layer. This is critical because agent context can include logs, events, configs, resource IDs, and user-provided messages.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Create a redaction package.
- Define redaction policies:
  - `strict`,
  - `standard`,
  - `none` only for local explicit debugging.
- Redact:
  - credentials,
  - bearer tokens,
  - AWS keys,
  - private keys,
  - connection strings with passwords,
  - common secret names/values,
  - env vars matching `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `*_KEY`,
  - emails if configured,
  - internal account IDs if configured.
- Add `RedactionReport` with counts and categories, not secret values.
- Add `--redact`, `--redaction-policy`, and config support where needed.
- Integrate with:
  - agent context,
  - prompt pack builder,
  - LLM providers,
  - MCP resources,
  - audit/transcript storage.
- Add test corpus with realistic secret-looking strings.
- Add a hard guard: LLM invocation refuses unredacted context unless `--allow-unredacted-local` is explicitly set.

#### Likely Files
- `internal/agent/redaction.go`
- `internal/redaction/redaction.go` if shared package preferred
- `internal/agent/context.go`
- `internal/agent/prompt.go`
- `internal/llm/*`
- `internal/mcp/*`
- `internal/config/config.go`
- `internal/agent/redaction_test.go`
- `internal/redaction/testdata/*`

#### Design
Redaction function:

```go
type Redactor interface {
    RedactText(input string) (string, RedactionReport)
    RedactJSON(input any) (any, RedactionReport)
}
```

Redaction report example:

```json
{
  "policy": "standard",
  "counts": {
    "aws_access_key": 1,
    "bearer_token": 2,
    "password": 1
  },
  "truncated": false
}
```

Do not overpromise perfect secret detection. The output should say redaction was applied and give categories.

#### Testing / Validation
- Unit tests for common secret patterns.
- Ensure redacted text never contains original test secrets.
- Ensure false positives do not destroy normal service names.
- Snapshot tests for redaction reports.
- LLM invocation test refuses unredacted context by default.

#### Gotchas
- Regex-only redaction has false positives/negatives. Keep patterns conservative and extensible.
- Do not log pre-redaction text in debug logs.
- Do not include redacted values in reports.
- Beware of base64-encoded secrets; detect obvious private key blocks.

#### Acceptance Criteria
- Redaction is reusable and tested.
- Agent context includes a redaction report.
- LLM providers cannot send unredacted context by default.
- Redaction integrates with prompt packs and MCP resources.

---

## Enhance agent planning with goals, risk budgets, and constraints

### ID
skiff-m5-013

### Priority
P0

### Type
task

### Labels
agent, plan, risk, recovery

### Dependencies
skiff-m5-009

skiff-m5-011

### Description
Extend `skiff agent plan` beyond a simple alias for `solve`. Plans should respect explicit goals, risk budgets, mutability constraints, and context sources. This makes planning useful for both read-only assistant agents and operator agents.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add flags to `skiff agent plan`:
  - `--goal restore-health`,
  - `--context <context.json>`,
  - `--max-risk low|medium|high|critical`,
  - `--allow-mutating`,
  - `--require-approval`,
  - `--profile <agent-profile>`,
  - `--fresh`,
  - `--out <path>`.
- Default `--allow-mutating=false` in agent mode unless explicit profile allows it.
- If `--context` is provided, plan from the saved context instead of live reads.
- Support at least one stable goal: `restore-health`.
- Define placeholders for future goals:
  - `reduce-cost`,
  - `explain-drift`,
  - `prepare-rollback`,
  - `collect-debug-context`.
- Apply risk budget filtering:
  - omit steps above max risk,
  - or mark graph blocked if omitted steps are necessary.
- Add `constraints` to graph output.
- Add `blocked_reasons` when policy/risk constraints prevent a useful plan.
- Add `recommended_next_command`, for example:
  - `skiff agent apply plan.json --step inspect_logs`
  - `skiff ops approve ...`
- Add tests for read-only, approval-required, and blocked plans.

#### Likely Files
- `internal/cli/agent.go`
- `internal/agent/solve.go`
- `internal/agent/action_graph.go`
- `internal/agent/context.go`
- `internal/agent/policy.go`
- `internal/agent/validate.go`
- `internal/agent/*_test.go`
- `internal/cli/agent_plan_test.go`

#### Design
The planner should be deterministic and conservative. It should not call an LLM. LLM assistance is a separate bead.

Inputs:

```go
type PlanOptions struct {
    Goal            string
    Service         string
    Context         *AgentContext
    MaxRisk         schema.Risk
    AllowMutating   bool
    RequireApproval bool
    Profile         string
    Fresh           bool
    TraceID         string
    Binary          string
}
```

Risk filtering behavior:

```text
If step.risk > max_risk:
  if step is optional -> omit and add warning
  if step is required to satisfy goal -> keep as blocked step or set graph.status=blocked
```

Do not silently downgrade a plan from “needs rollback” to “no action” because mutating actions are disallowed.

#### Testing / Validation
- Plan from live fake doctor result.
- Plan from saved context JSON.
- Read-only profile produces read-only steps and blocked mutating recommendations.
- Operator profile with max-risk medium includes rollback but requires approval.
- Unsupported goal returns structured error or blocked graph.

#### Gotchas
- Risk ordering must be centralized. Do not compare risk strings lexicographically.
- `--allow-mutating` should not bypass policy or approval requirements.
- Avoid writing plan files unless `--out` is explicit.
- If `--out -`, ensure stdout remains valid JSON.

#### Acceptance Criteria
- `skiff agent plan` accepts goal and risk flags.
- Plans record constraints.
- Risk-constrained plans are honest about blocked actions.
- Planning works from saved context as well as live reads.

---

## Implement agent plan validation and linting

### ID
skiff-m5-014

### Priority
P0

### Type
task

### Labels
agent, validation, plan, safety

### Dependencies
skiff-m5-013

### Description
Add `skiff agent validate <plan.json>` to statically validate agent plans before execution. This provides a local safety check for plans generated by Skiff, saved by CI, modified by humans, or consumed by external agents.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Implement `agent.ValidateActionGraph`.
- Add CLI command `skiff agent validate <plan.json> --format json`.
- Validate:
  - schema ID known and supported,
  - plan ID present,
  - goal present,
  - status known,
  - step IDs unique,
  - dependencies reference existing steps,
  - dependency graph is acyclic,
  - mutating steps include risk/reversibility/safety,
  - steps above max risk are marked blocked or approval-required,
  - approval-required steps do not include `--yes` in rendered command,
  - API operations match known capabilities,
  - expected validations exist after mutating steps.
- Add output:
  - `ok`,
  - `schema:"skiff.agent-plan-validation/v1"`,
  - `valid`,
  - `findings`,
  - `recommended_actions`.
- Add `--strict` to reject experimental fields or unknown capabilities.
- Add tests for every invalid condition.

#### Likely Files
- `internal/agent/validate.go`
- `internal/agent/action_graph.go`
- `internal/agent/capability_registry.go`
- `internal/cli/agent.go`
- `internal/agent/validate_test.go`
- `internal/cli/agent_validate_test.go`

#### Design
Validation output:

```json
{
  "ok": true,
  "schema": "skiff.agent-plan-validation/v1",
  "valid": false,
  "findings": [
    {
      "severity": "high",
      "code": "MUTATING_STEP_MISSING_APPROVAL",
      "path": "steps[2]",
      "summary": "mutating step rollback_previous_stable requires approval metadata"
    }
  ],
  "recommended_actions": [
    {
      "id": "replan",
      "command": "skiff agent plan payments-api --max-risk medium --format json",
      "mutating": false
    }
  ]
}
```

Return exit code:
- `0` when valid;
- `1` when invalid due to user-supplied plan;
- internal error only for parse/IO failures not caused by plan content.

#### Testing / Validation
- Valid graph passes.
- Duplicate step ID fails.
- Cycle fails.
- Missing capability fails in strict mode.
- Mutating step without validation fails.
- Approval-required command containing `--yes` fails.

#### Gotchas
- Do not execute commands during validation.
- Do not require live API access.
- Be careful reading stdin if path is `-`; avoid blocking tests.

#### Acceptance Criteria
- Plans can be validated offline.
- Validation findings are structured and actionable.
- `agent apply` can call the same validator before executing anything.

---

## Add intent and idempotency model for mutating actions

### ID
skiff-m5-015

### Priority
P0

### Type
task

### Labels
agent, idempotency, ops, safety, state

### Dependencies
skiff-m5-002

skiff-m5-008

skiff-m5-013

### Description
Introduce explicit idempotency and intent tracking for mutating agent actions. Agents retry. Network calls fail. LLMs may repeat instructions. Every mutation must be safe to retry and must be associated with a durable intent record.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define an `Intent` object schema:
  - `schema`,
  - `intent_id`,
  - `idempotency_key`,
  - `operation`,
  - `target`,
  - `params_hash`,
  - `actor`,
  - `reason`,
  - `risk`,
  - `reversibility`,
  - `requires_approval`,
  - `approval_id`,
  - `status`,
  - `created_at`,
  - `updated_at`,
  - `result_refs`.
- Add object-state paths for intents.
- Add helper to create or reuse intent by idempotency key.
- Add flags to mutating commands where possible:
  - `--idempotency-key <key>`,
  - `--intent-id <id>`,
  - `--reason <text>`,
  - `--actor <id>`,
  - `--max-risk <risk>`.
- Start with commands most likely to be used by agents:
  - rollback,
  - ops resume,
  - ops approve/reject,
  - stateful replace-member,
  - stateful restore/update where applicable.
- If retrofitting all mutating commands is too large, add TODO tests that identify missing commands.
- Add output fields:
  - `intent_id`,
  - `idempotency_key`,
  - `operation_id`,
  - `saga_id`,
  - `state: created|reused|blocked|approved|rejected`.
- Add tests for idempotent retry with same params and conflict with different params.

#### Likely Files
- `internal/state/schema/*`
- `internal/state/paths/*`
- `internal/ops/intent.go`
- `internal/ops/store.go`
- `internal/cli/ops.go`
- `internal/cli/rollback*.go`
- `internal/cli/stateful*.go`
- `internal/cli/saga*.go`
- `internal/agent/action_graph.go`
- `internal/agent/apply.go`
- `internal/ops/*_test.go`
- `internal/cli/*_test.go`

#### Design
Idempotency semantics:

```text
same idempotency_key + same operation + same target + same params_hash
  -> return existing intent/result

same idempotency_key + different params_hash
  -> reject with IDEMPOTENCY_CONFLICT

missing idempotency_key from agent apply
  -> generate deterministic key from plan_id + step_id, or reject unless --generate-idempotency-key
```

Agent apply should default key:

```text
idempotency_key = <plan_id>:<step_id>
```

Intent statuses:

```text
created
pending_approval
approved
executing
completed
failed
blocked
cancelled
```

#### Testing / Validation
- Create intent once, retry returns same.
- Same key different params fails.
- Missing key in normal human CLI may be allowed if current behavior requires it, but agent apply must always use a key.
- Intent object path is deterministic and valid.
- Intent records never include secrets.

#### Gotchas
- Do not add idempotency only at CLI layer if underlying operation can still duplicate work. Persist intent before mutation.
- Avoid idempotency keys that include raw prompts or secrets.
- Time-dependent params can accidentally create conflicts; normalize params before hashing.
- Be explicit about what is idempotent vs merely deduplicated.

#### Acceptance Criteria
- Mutating agent actions have durable intent records.
- Retrying an agent mutation with the same key is safe.
- Conflicting reuse of an idempotency key is rejected.
- Intent metadata appears in JSON outputs.

---

## Define agent identity, profiles, and risk policy

### ID
skiff-m5-016

### Priority
P0

### Type
task

### Labels
agent, authz, policy, profiles, safety

### Dependencies
skiff-m5-015

### Description
Add an agent profile model that determines which capabilities an agent may use and what risk level is allowed without approval. This gives users a clear way to run read-only agents, diagnostic agents, and operator agents without hardcoding policy in every command.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define `AgentProfile` config model:
  - `name`,
  - `actor_id`,
  - `allow_capabilities`,
  - `deny_capabilities`,
  - `allow_tags`,
  - `max_risk_without_approval`,
  - `max_risk_with_approval`,
  - `allow_mutating`,
  - `llm_allowed`,
  - `mcp_allowed`,
  - `redaction_policy`.
- Add config fields to `internal/config.Config` or a nested `Agent` config object.
- Add config loading/validation for profiles.
- Add CLI flag `--agent-profile <name>` or `--profile <name>` for agent commands.
- Add default built-in profiles:
  - `readonly`: read-only, no LLM execution unless explicitly allowed,
  - `diagnostic`: read-only plus debug context,
  - `operator`: mutating allowed only with approval and idempotency,
  - `breakglass`: direct mode allowed, high risk requires explicit local confirmation.
- Add policy evaluator:
  - `CanPlan`,
  - `CanExecute`,
  - `RequiresApproval`,
  - `MaxRisk`.
- Integrate with capabilities filtering.
- Integrate with `agent plan` constraints.
- Add tests for allow/deny precedence and risk behavior.

#### Likely Files
- `internal/config/config.go`
- `internal/config/*`
- `internal/agent/profile.go`
- `internal/agent/policy.go`
- `internal/agent/capability.go`
- `internal/cli/root.go`
- `internal/cli/agent.go`
- `internal/cli/client_commands.go`
- `internal/authz/*` if existing authz package is appropriate
- `internal/agent/policy_test.go`
- `internal/config/*_test.go`

#### Design
Example config:

```yaml
agent:
  default_profile: readonly
  profiles:
    readonly:
      allow_tags: ["read", "diagnostic"]
      allow_mutating: false
      max_risk_without_approval: low
    operator:
      allow_tags: ["read", "diagnostic", "recovery"]
      allow_mutating: true
      max_risk_without_approval: low
      max_risk_with_approval: medium
      deny_capabilities:
        - database.restore
```

Policy decision output:

```json
{
  "allowed": false,
  "requires_approval": true,
  "reason": "rollback.start is mutating and risk medium exceeds readonly profile"
}
```

#### Testing / Validation
- Built-in profiles validate.
- Explicit deny beats allow.
- Mutating actions denied in readonly profile.
- Medium risk rollback requires approval in operator profile.
- Critical risk denied unless breakglass profile and explicit approval.
- Capabilities command filtered by profile.

#### Gotchas
- Do not store API keys or LLM credentials inside profile objects unless redaction is guaranteed.
- Avoid making profile names provider-specific.
- Keep policy pure and testable; no live API calls.

#### Acceptance Criteria
- Agent profiles can be loaded and validated.
- Capabilities and plans respect profile constraints.
- Policy decisions are structured and test-covered.
- Read-only is the safe default.

---

## Integrate approvals into agent plans and apply flow

### ID
skiff-m5-017

### Priority
P0

### Type
task

### Labels
agent, approvals, ops, saga, safety

### Dependencies
skiff-m5-016

### Description
Make approval requirements explicit in plans and executable flows. Agents may propose actions, but Skiff policy should decide whether they can execute them. Approval metadata must be in the graph, intent, and output, not hidden in human instructions.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define approval metadata:
  - `approval_required`,
  - `approval_reason`,
  - `approval_scope`,
  - `approval_command`,
  - `approval_id`,
  - `policy_ref`.
- Add approval metadata to `ActionStep`.
- Update planner to mark steps requiring approval based on policy and risk.
- Ensure mutating steps requiring approval do not include `--yes` in command rendering.
- Connect approval metadata to existing saga approval commands where possible.
- Add `skiff agent approval request <plan.json> --step <id>` if needed.
- Ensure `ops approve` can reference an intent or saga step.
- Add JSON output for approval request creation.
- Add tests for approval-required plan and approved apply.

#### Likely Files
- `internal/agent/action_graph.go`
- `internal/agent/solve.go`
- `internal/agent/policy.go`
- `internal/agent/apply.go`
- `internal/ops/*`
- `internal/cli/ops.go`
- `internal/cli/saga*.go`
- `internal/cli/agent.go`
- `internal/state/schema/*`
- `internal/agent/*_test.go`
- `internal/cli/*_test.go`

#### Design
Plan step example:

```json
{
  "id": "rollback_previous_stable",
  "kind": "rollback.start",
  "mutating": true,
  "risk": "medium",
  "requires_approval": true,
  "approval": {
    "reason": "mutating action risk medium exceeds operator auto-execute threshold",
    "approval_command": "skiff ops approve saga_01J... --step rollback_previous_stable --format json",
    "policy_ref": "agent.profiles.operator"
  }
}
```

Apply behavior:
- If step requires approval and approval not present, return `ok:false`, `code:"APPROVAL_REQUIRED"`.
- Include a recommended approval command.
- Do not execute the step.
- If approval is present and valid, continue idempotently.

#### Testing / Validation
- Plan marks rollback approval-required under operator profile.
- `agent apply` refuses approval-required step without approval.
- Approval command is stable and machine-readable.
- Approval reuse is idempotent.
- Rejected approval blocks execution.

#### Gotchas
- Existing saga approval may be step-oriented. Make sure plan step IDs can map to saga step IDs or create a bridge object.
- Approval should not be represented only as `--yes`.
- Do not let LLM provider output create approvals.

#### Acceptance Criteria
- Plans include approval metadata.
- Apply refuses unapproved mutating steps.
- Approval request/approval path is machine-readable.
- Tests prove approvals gate execution.

---

## Implement guarded `skiff agent apply`

### ID
skiff-m5-018

### Priority
P0

### Type
task

### Labels
agent, apply, execution, safety, idempotency

### Dependencies
skiff-m5-007

skiff-m5-014

skiff-m5-015

skiff-m5-017

### Description
Add `skiff agent apply` to execute a single approved plan step or a safe subset of plan steps. This command is the guarded executor for agents. It must validate plans, enforce policy, enforce idempotency, call the typed executor registry instead of shelling out, and only execute known capabilities.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Implement `skiff agent apply <plan.json> --step <step-id>`.
- Add optional `--all-read-only` to execute all read-only steps in dependency order.
- Add optional `--dry-run`.
- Add optional `--idempotency-key`.
- Add optional `--approval-id`.
- Add optional `--profile`.
- Validate plan before any execution.
- Check dependencies:
  - required predecessor steps completed or being applied in same `--all-read-only` batch.
- Check policy:
  - profile allows capability,
  - risk within budget,
  - approval present if required.
- Convert typed `api_operation` into execution call.
- Initially support read-only operations by dispatching existing client APIs:
  - doctor,
  - status,
  - events,
  - logs/metrics if existing command APIs are accessible.
- Support mutating operations only when intent/idempotency/approval are implemented for that operation.
- Return structured result with:
  - step ID,
  - operation,
  - intent ID,
  - idempotency key,
  - command rendering,
  - operation/saga IDs,
  - validation result,
  - next recommended command.
- Add tests with fake executor.

#### Likely Files
- `internal/agent/apply.go`
- `internal/agent/executor.go`
- `internal/agent/validate.go`
- `internal/agent/policy.go`
- `internal/cli/agent.go`
- `internal/client/client.go`
- `internal/client/api.go`
- `internal/ops/*`
- `internal/cli/agent_apply_test.go`
- `internal/agent/apply_test.go`

#### Design
Executor interface:

```go
type Executor interface {
    Execute(ctx context.Context, op control.Operation, opts ExecuteOptions) (*ExecuteResult, error)
}
```

Do not implement execution by shelling out to `skiff <command>`. That creates parsing issues and loses typed errors. Shell rendering remains for human hints.

Apply output:

```json
{
  "ok": true,
  "schema": "skiff.agent-apply-result/v1",
  "step_id": "inspect_logs",
  "operation": "logs.query",
  "mutating": false,
  "result": {},
  "next": {
    "recommended_command": "skiff agent apply plan.json --step rollback_previous_stable"
  }
}
```

#### Testing / Validation
- Applying unknown step fails.
- Applying invalid plan fails before execution.
- Applying read-only step succeeds.
- Applying mutating step without approval fails.
- Applying mutating step with approval creates/reuses intent.
- `--dry-run` validates and renders without executing.

#### Gotchas
- Do not execute arbitrary `command` strings from the plan.
- Do not let external or LLM-generated plans invoke capabilities not in registry.
- Do not assume every capability has a typed executor yet. Return a clear unsupported error.
- Avoid running long watch operations through `agent apply`; watch should remain direct command/MCP stream.

#### Acceptance Criteria
- `agent apply` safely executes supported read-only steps.
- Mutating steps are guarded by idempotency and approval.
- Apply never shells out to arbitrary command strings.
- Tests cover success, policy denial, approval required, and dry-run.

---

## Emit agent audit events and run records

### ID
skiff-m5-019

### Priority
P0

### Type
task

### Labels
agent, audit, events, state, traceability

### Dependencies
skiff-m5-002

skiff-m5-018

### Description
Record agent plans, context generation, apply attempts, approvals, and LLM invocations as auditable events and durable run records. Agents should be observable operators, not invisible automation.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define `AgentRun` object:
  - `run_id`,
  - `trace_id`,
  - `actor`,
  - `profile`,
  - `service`,
  - `goal`,
  - `started_at`,
  - `ended_at`,
  - `status`,
  - `context_ref`,
  - `plan_ref`,
  - `intent_refs`,
  - `llm_invocation_refs`,
  - `events`.
- Define object-state paths for agent runs.
- Emit events for:
  - context created,
  - plan created,
  - plan validation failed,
  - apply attempted,
  - apply succeeded,
  - apply blocked,
  - approval required,
  - LLM prompt generated,
  - LLM invocation completed.
- Include trace IDs and run IDs in outputs.
- Add `skiff agent runs list`.
- Add `skiff agent runs inspect <run-id>`.
- Add redaction for stored records.
- Add tests with memory/file object store.

#### Likely Files
- `internal/agent/run.go`
- `internal/agent/audit.go`
- `internal/state/schema/*`
- `internal/state/paths/*`
- `internal/events/*`
- `internal/cli/agent.go`
- `internal/objstore/*`
- `internal/agent/audit_test.go`
- `internal/cli/agent_runs_test.go`

#### Design
Agent run records are not transcripts by default. They are audit metadata. Full prompt/transcript storage is a separate bead.

Event type naming examples:

```text
agent.context.created
agent.plan.created
agent.plan.validation_failed
agent.apply.started
agent.apply.blocked
agent.apply.completed
agent.llm.prompt_created
agent.llm.completed
```

All events should include:

```json
{
  "actor": {"id":"sre-bot","type":"agent"},
  "trace_id": "tr_...",
  "agent_run_id": "arun_01J...",
  "profile": "operator",
  "service": "payments-api"
}
```

#### Testing / Validation
- Context generation emits event when store configured.
- Plan generation emits event.
- Apply blocked emits event.
- Apply success emits event.
- Redaction test ensures prompts/secrets are not in audit event unless transcript storage explicitly enabled.

#### Gotchas
- Some commands may run without configured object store in API mode. If audit store unavailable, return warning, not failure, unless policy requires audit.
- Do not write large context bundles inline in every event. Store refs/hashes.
- Do not store raw LLM output in audit records by default.

#### Acceptance Criteria
- Agent activity is traceable by run ID.
- Events include actor/profile/trace/service.
- Audit records are redacted and tested.
- `agent runs list/inspect` works for stored runs.

---

## Build prompt pack generator for LLM handoff

### ID
skiff-m5-020

### Priority
P0

### Type
task

### Labels
agent, llm, prompt, context, subscription

### Dependencies
skiff-m5-012

skiff-m5-011

### Description
Generate a self-contained prompt pack from Skiff context, plan, logs, metrics, and user instructions. This supports workflows like: “use my Claude subscription to analyze/fix this service with auto-generated Skiff context.”

This bead only generates prompt packs. It does not invoke LLMs; invocation is in the next bead.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define `PromptPack` type:
  - `schema`,
  - `prompt_id`,
  - `service`,
  - `goal`,
  - `user_prompt`,
  - `system_instructions`,
  - `context_summary`,
  - `context_json`,
  - `plan_json`,
  - `constraints`,
  - `redaction_report`,
  - `created_at`,
  - `estimated_tokens` or `estimated_bytes`.
- Add command:
  - `skiff agent prompt <service> --prompt <text> --out <path>`
  - support stdin prompt via `--prompt -`.
- Add flags:
  - `--include context,plan,logs,metrics,events`,
  - `--max-bytes`,
  - `--compact`,
  - `--redaction-policy`,
  - `--goal`,
  - `--profile`.
- Build a default system instruction that tells the model:
  - do not invent Skiff commands;
  - use typed plan/capabilities when available;
  - distinguish facts from hypotheses;
  - propose commands but do not assume approval;
  - do not ask to bypass policy;
  - never expose secrets;
  - return JSON or Markdown according to requested mode.
- Include a rendered “operator handoff” section for local CLI tools that expect plain text.
- Add golden tests for prompt rendering.

#### Likely Files
- `internal/agent/prompt.go`
- `internal/agent/context.go`
- `internal/agent/redaction.go`
- `internal/agent/solve.go`
- `internal/cli/agent.go`
- `internal/agent/prompt_test.go`
- `internal/cli/agent_prompt_test.go`
- `docs/agents/llm_handoff.md`

#### Design
Prompt pack should be useful both as JSON and as text.

JSON output:

```json
{
  "ok": true,
  "schema": "skiff.agent-prompt-pack/v1",
  "prompt_id": "prompt_01J...",
  "service": "payments-api",
  "user_prompt": "fix this service; here are logs, metrics, and generated context",
  "constraints": {
    "allowed_actions": "propose-only",
    "max_risk": "low",
    "profile": "readonly"
  },
  "prompt_text": "You are assisting with Skiff service recovery..."
}
```

The text prompt should be written to a temp file or stdout as requested. Do not include secrets. If context was truncated, say so explicitly.

#### Testing / Validation
- Prompt generation works from live fake context.
- Prompt generation works from saved context.
- Prompt includes redaction report.
- Prompt refuses unredacted LLM handoff unless explicit override.
- Golden prompt is stable enough for review.

#### Gotchas
- Passing huge logs directly to prompts can blow token budgets. Implement truncation with clear notices.
- Avoid telling the LLM it can execute actions unless the actual provider flow supports that.
- Local subscription CLI tools may prefer a plain text prompt; do not force JSON only.

#### Acceptance Criteria
- Users can generate a redacted prompt pack.
- Prompt pack includes Skiff context and constraints.
- Prompt pack is self-contained and safe to hand to an LLM.
- Tests cover truncation and redaction.

---

## Implement LLM provider adapters with local subscriptions first and API keys second

### ID
skiff-m5-021

### Priority
P0

### Type
task

### Labels
agent, llm, claude, openai, api-keys, subscription

### Dependencies
skiff-m5-020

skiff-m5-016

### Description
Add an LLM provider layer that prefers local CLI subscriptions such as `claude -p` when available, while also supporting API-key providers. Local providers must use `exec.Command` with argv arrays and stdin/files, never `/bin/sh -c`, unless the user explicitly configures an unsafe custom-command provider. The goal is to let users use subscriptions they already pay for without forcing Skiff to manage LLM billing.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define `LLMProvider` interface:
  - `Name()`,
  - `Available(ctx)`,
  - `Invoke(ctx, PromptPack, InvokeOptions)`.
- Implement provider resolution order:
  1. explicit `--llm-provider`;
  2. config `agent.default_llm_provider`;
  3. local Claude CLI if `claude` is on PATH;
  4. other configured local CLI provider;
  5. API provider with configured key;
  6. no provider: write prompt pack and return recommended command.
- Implement `local-claude` provider:
  - default command `claude -p <prompt>`;
  - prefer stdin or temp-file mode if supported/configured;
  - no shell interpolation;
  - timeout support;
  - capture stdout/stderr separately;
  - redact before invocation.
- Implement `custom-command` provider:
  - command argv template configured explicitly;
  - placeholders such as `{prompt_file}`, `{prompt_text}`, `{service}`, `{trace_id}`;
  - disabled by default unless configured.
- Implement API provider placeholders:
  - `anthropic-api`,
  - `openai-api`,
  - `custom-http`.
- Read API keys from env/config:
  - `ANTHROPIC_API_KEY`,
  - `OPENAI_API_KEY`,
  - or Skiff config references.
- Never print API keys in config show or errors.
- Add command `skiff agent llm providers --format json`.
- Add tests with fake binaries and fake HTTP clients.

#### Likely Files
- `internal/llm/provider.go`
- `internal/llm/local_cli.go`
- `internal/llm/claude_cli.go`
- `internal/llm/custom_command.go`
- `internal/llm/anthropic.go`
- `internal/llm/openai.go`
- `internal/llm/provider_registry.go`
- `internal/config/config.go`
- `internal/cli/agent.go`
- `internal/agent/prompt.go`
- `internal/llm/*_test.go`
- `internal/cli/agent_llm_test.go`

#### Design
Local Claude example command:

```bash
skiff agent ask payments-api \
  --llm-provider local-claude \
  --prompt "fix this service; here are the logs, metrics, and auto-generated context"
```

Provider config example:

```yaml
agent:
  default_llm_provider: local-claude
  llm:
    providers:
      local-claude:
        type: local-command
        command: ["claude", "-p", "{prompt_text}"]
        timeout: "5m"
      anthropic-api:
        type: anthropic-api
        api_key_env: "ANTHROPIC_API_KEY"
        model: "claude-3-7-sonnet-latest"
```

Security rules:
- Do not use shell by default.
- Do not pass unredacted context.
- Do not execute model-suggested commands.
- Do not auto-apply actions from LLM output.
- Store only redacted prompt/transcript unless explicit local-only override.

#### Testing / Validation
- Fake `claude` binary on PATH is discovered.
- Provider resolution picks explicit provider over default.
- Missing provider returns recommended command and prompt file.
- API key is redacted from errors.
- Timeout cancels subprocess.
- Stderr from provider is captured in structured output but not confused with Skiff stderr.

#### Gotchas
- `claude -p` argument length may exceed OS limits. Support prompt-file or stdin mode.
- Local CLI tools may change flags. Keep command configurable.
- Do not assume a local CLI subscription is authenticated just because binary exists; run a lightweight availability check or handle failure gracefully.
- Never use `/bin/sh -c` for the default Claude provider.

#### Acceptance Criteria
- Skiff can discover and invoke local Claude CLI when configured/available.
- Skiff can fall back to API-key providers.
- LLM invocation is redacted and non-mutating by default.
- Provider behavior is testable without real LLM calls.

---

## Add `skiff agent ask` and `skiff agent assist`

### ID
skiff-m5-022

### Priority
P0

### Type
task

### Labels
agent, llm, assist, cli, subscription

### Dependencies
skiff-m5-021

skiff-m5-013

skiff-m5-012

### Description
Expose LLM-assisted workflows through first-class commands. `ask` should answer questions about a service using generated context. `assist` should help produce remediation suggestions, but must not execute changes. This is where users can launch `claude -p "fix this service, here are the logs, metrics, and other auto-generated context"` through Skiff safely.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Implement:
  - `skiff agent ask <service> --prompt <text>`
  - `skiff agent assist <service> --prompt <text>`
- Support `--prompt -` to read user prompt from stdin.
- Support `--llm-provider`.
- Support `--no-invoke` to generate prompt only.
- Support `--out <path>` for LLM output.
- Support `--context <context.json>` and `--plan <plan.json>`.
- Default behavior:
  - build context,
  - build plan if goal is recovery,
  - redact,
  - invoke provider if available,
  - return structured result.
- Do not execute model suggestions.
- If model returns commands or plan JSON, include them under `model_suggestions`, not `recommended_actions` unless Skiff validates them.
- Add `--expect json|markdown|text` to ask provider for response format.
- Add optional `--validate-suggestions`:
  - parse model-suggested Skiff plan/actions,
  - run `agent validate`,
  - mark suggestions as valid/invalid.
- Add tests with fake provider.

#### Likely Files
- `internal/cli/agent.go`
- `internal/agent/assist.go`
- `internal/agent/prompt.go`
- `internal/llm/provider.go`
- `internal/agent/validate.go`
- `internal/agent/context.go`
- `internal/agent/assist_test.go`
- `internal/cli/agent_ask_test.go`
- `docs/agents/llm_handoff.md`

#### Design
Command behavior example:

```bash
skiff agent assist payments-api \
  --prompt "fix this service; use logs, metrics, and Skiff context" \
  --llm-provider local-claude \
  --format json
```

Output:

```json
{
  "ok": true,
  "schema": "skiff.agent-assist-result/v1",
  "trace_id": "tr_...",
  "service": "payments-api",
  "provider": {
    "name": "local-claude",
    "kind": "local-command"
  },
  "prompt": {
    "prompt_id": "prompt_01J...",
    "redaction_report": {}
  },
  "response": {
    "format": "markdown",
    "text": "..."
  },
  "model_suggestions": [],
  "validated_suggestions": []
}
```

Suggested provider instruction:

```text
You may propose Skiff commands or an agent plan, but you cannot approve or execute mutations. If you propose a mutating action, mark it approval-required and include the expected validation.
```

#### Testing / Validation
- `ask` builds context and invokes fake provider.
- `assist` builds context and plan.
- `--no-invoke` returns prompt file/recommended local command.
- LLM output with suggested plan is validated when flag set.
- Unredacted context is refused by default.

#### Gotchas
- Do not call this command `fix` unless it actually executes safely. `assist` is better because it sets expectations.
- Do not let LLM output become trusted recommended actions without validation.
- Be cautious with stdout: in JSON mode, provider raw output must be a JSON string field, not raw interleaved text.

#### Acceptance Criteria
- Users can use local Claude subscription through `skiff agent assist`.
- Users can configure API-key fallback providers.
- LLM assistance is propose-only by default.
- Output is structured, redacted, and auditable.

---

## Store and replay LLM prompt/transcript records

### ID
skiff-m5-023

### Priority
P1

### Type
task

### Labels
agent, llm, audit, replay, transcripts

### Dependencies
skiff-m5-022

skiff-m5-019

### Description
Optionally store redacted LLM prompt and response transcripts for audit and replay. This is separate from the basic audit run record so users can choose whether they want transcripts persisted.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define `LLMInvocation` object:
  - `invocation_id`,
  - `provider`,
  - `model`,
  - `prompt_id`,
  - `prompt_hash`,
  - `response_hash`,
  - `redaction_report`,
  - `started_at`,
  - `ended_at`,
  - `status`,
  - `token_estimates`,
  - optional redacted prompt text,
  - optional redacted response text.
- Add config:
  - `agent.llm.store_transcripts: false|metadata|redacted-full`.
- Add CLI flags:
  - `--store-transcript`,
  - `--no-store-transcript`,
  - `--transcript-mode metadata|redacted-full`.
- Add commands:
  - `skiff agent llm invocations list`,
  - `skiff agent llm invocations inspect <id>`,
  - `skiff agent llm replay <id> --provider <provider>`.
- Replay must rebuild invocation from stored redacted prompt, not original secret-containing context.
- Add tests for metadata-only and full-redacted storage.

#### Likely Files
- `internal/llm/invocation.go`
- `internal/llm/transcript_store.go`
- `internal/agent/audit.go`
- `internal/state/paths/*`
- `internal/state/schema/*`
- `internal/cli/agent.go`
- `internal/llm/*_test.go`
- `internal/cli/agent_llm_transcripts_test.go`

#### Design
Default should be metadata-only or no transcript, depending on privacy posture. The safest default is no full transcript persistence. Still emit an audit event saying an LLM was invoked and redaction was applied.

Transcript modes:

```text
none
metadata
redacted-full
```

#### Testing / Validation
- Default does not store full prompt/response.
- Metadata mode stores hashes only.
- Redacted-full mode stores prompt/response with secrets removed.
- Replay uses stored prompt and fake provider.
- Inspect output redacts provider config and keys.

#### Gotchas
- Do not store raw prompts before redaction.
- Do not assume provider response is safe; redact response before storage too.
- LLM outputs may include copied context; response redaction is necessary.

#### Acceptance Criteria
- Users can opt into transcript storage.
- Stored transcripts are redacted.
- Replay works from stored redacted prompt.
- Audit records link agent runs and LLM invocations.

---

## Add agent runbook schema and compiler

### ID
skiff-m5-024

### Priority
P1

### Type
task

### Labels
agent, runbooks, workflows, bounded-autonomy

### Dependencies
skiff-m5-014

skiff-m5-018

### Description
Let users define bounded agent-operable runbooks that compile into action graphs. This provides structured autonomy without relying on free-form LLM output.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define `SkiffRunbook` schema:
  - `kind`,
  - `apiVersion`,
  - `metadata`,
  - `inputs`,
  - `steps`,
  - `conditions`,
  - `approval` requirements.
- Add `skiff agent runbook validate <file>`.
- Add `skiff agent runbook plan <file> --service <service> --format json`.
- Support step types:
  - capability invocation,
  - condition,
  - branch,
  - wait/watch,
  - expected validation.
- Integrate with capability registry.
- Integrate with policy/risk evaluator.
- Compile runbook into `ActionGraph`.
- Add example runbook:
  - restore service health,
  - collect debug context,
  - rollback after failed canary.
- Add golden tests.

#### Likely Files
- `internal/agent/runbook/types.go`
- `internal/agent/runbook/parse.go`
- `internal/agent/runbook/compile.go`
- `internal/agent/runbook/validate.go`
- `internal/cli/agent.go`
- `examples/runbooks/*.yaml`
- `docs/agents/runbooks.md`
- `internal/agent/runbook/*_test.go`

#### Design
Example:

```yaml
apiVersion: skiff.dev/v1
kind: SkiffRunbook
metadata:
  name: restore-service-health
inputs:
  service:
    type: string
steps:
  - id: diagnose
    capability: doctor.diagnose
    target:
      kind: service
      name: "{{ service }}"
  - id: inspect-events
    capability: events.list
    requires: [diagnose]
  - id: rollback
    capability: rollback.start
    when: "diagnose.health == 'critical'"
    requires: [diagnose, inspect-events]
    approval: required
    maxRisk: medium
  - id: verify
    capability: doctor.diagnose
    requires: [rollback]
    expected:
      success: "health != 'critical'"
```

Avoid building a full expression language in M4. Simple condition strings can be validated but not evaluated deeply until later. For M4, support a minimal evaluator or compile conditions into blocked/manual steps.

#### Testing / Validation
- Valid runbook compiles.
- Unknown capability fails.
- Mutating step without approval fails.
- Cycle fails.
- Missing input fails.
- Golden compile output matches expected ActionGraph.

#### Gotchas
- Do not let runbooks bypass policy.
- Avoid Turing-complete runbook logic.
- Keep templates explicit; do not evaluate arbitrary code.

#### Acceptance Criteria
- Users can validate and compile runbooks.
- Runbooks produce standard ActionGraphs.
- Policy/risk/approval rules apply to runbook steps.
- Examples document bounded autonomy.

---

## Build simulated incident fixtures and agent eval harness

### ID
skiff-m5-025

### Priority
P1

### Type
task

### Labels
agent, tests, evals, incidents, quality

### Dependencies
skiff-m5-013

skiff-m5-014

### Description
Create test fixtures and an evaluation harness that simulate service incidents and verify agent context/planning behavior. This makes agent-native behavior durable and prevents regressions.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Create fixture object states for incidents:
  - healthy service,
  - failed canary,
  - stale index,
  - rollback in progress,
  - logs unavailable,
  - metrics gate failed,
  - stateful member unhealthy,
  - approval required.
- Add a test harness that runs:
  - `agent context`,
  - `agent plan`,
  - `agent validate`,
  - optional `agent apply --dry-run`.
- Assert expected:
  - graph status,
  - step kinds,
  - risk levels,
  - approval requirements,
  - redaction report,
  - recommended next command.
- Add optional LLM prompt eval:
  - generated prompt includes required sections,
  - generated prompt excludes secrets.
- Add CI job or test target for agent contracts.
- Store golden outputs under testdata.

#### Likely Files
- `internal/agent/testdata/incidents/*`
- `internal/agent/eval.go`
- `internal/agent/eval_test.go`
- `internal/cli/agent_eval_test.go`
- `tests/agent/*`
- `Makefile` or CI config if present
- `docs/agents/evals.md`

#### Design
The eval harness should not call real cloud providers or real LLMs. It should use memory/file object stores and fake clients.

Example fixture:

```text
testdata/incidents/failed_canary/
  state/
  expected_context.json
  expected_plan.json
  expected_validation.json
```

#### Testing / Validation
- Evals pass in unit tests.
- Tests are deterministic.
- Golden updates require intentional review.
- No real credentials required.

#### Gotchas
- Do not make evals too brittle on timestamps or ordering unless deterministic.
- Prefer semantic assertions for steps/risk over exact huge JSON when possible.
- Keep fixture size reasonable.

#### Acceptance Criteria
- At least five incident fixtures exist.
- Agent planning is regression-tested against fixtures.
- Prompt redaction is tested against incident logs.
- CI can run evals without cloud/LLM access.

---

## Implement MCP server skeleton and agent profiles

### ID
skiff-m5-026

### Priority
P1

### Type
task

### Labels
agent, mcp, integration, tools

### Dependencies
skiff-m5-006

skiff-m5-011

skiff-m5-016

### Description
Expose Skiff through a Model Context Protocol server so agent clients can discover and call Skiff tools/resources directly. Start with a local stdio server and read-only profile support.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add command:
  - `skiff mcp serve --profile readonly`
  - `skiff mcp serve --profile operator`
- Add `mcp` to root command dispatch and help.
- Implement a minimal MCP server package.
- Support stdio transport first.
- Generate MCP tool definitions from capability registry.
- Apply agent profile policy when listing/executing tools.
- Return structured content and JSON content where appropriate.
- Add config for MCP:
  - enabled profiles,
  - default profile,
  - redaction policy.
- Add tests using an in-process MCP client or protocol fixtures.
- Document editor/client setup examples.

#### Likely Files
- `internal/cli/command.go`
- `internal/cli/mcp.go`
- `internal/mcp/server.go`
- `internal/mcp/tools.go`
- `internal/mcp/resources.go`
- `internal/agent/capability_registry.go`
- `internal/agent/policy.go`
- `internal/agent/context.go`
- `internal/config/config.go`
- `internal/mcp/*_test.go`
- `docs/agents/mcp.md`

#### Design
Do not duplicate tool definitions by hand. MCP tools should be rendered from the same capability registry used by `skiff agent capabilities`.

Command example:

```bash
skiff mcp serve --profile readonly --config skiff.yaml
```

Initial server can expose:
- tools list,
- tool call for read-only capabilities,
- resources list/read in later beads.

Profile behavior:
- `readonly` hides or denies mutating tools.
- `operator` may list mutating tools but calls still require approval/idempotency.

#### Testing / Validation
- MCP server starts with stdio test harness.
- Read-only profile lists no mutating tools.
- Tool schema matches capability input schema.
- Unknown tool returns structured MCP error.
- Redaction is applied to resource/tool outputs.

#### Gotchas
- MCP library choice matters. If adding a dependency, keep it small and stable.
- Avoid blocking forever in tests. Use context cancellation.
- Do not expose mutating tools until approval/idempotency path is implemented.

#### Acceptance Criteria
- `skiff mcp serve --profile readonly` starts.
- Tool list is generated from capabilities.
- Read-only tools can be called in tests.
- Mutating tools are hidden or denied in readonly profile.

---

## Add MCP resources for Skiff context and object-state views

### ID
skiff-m5-027

### Priority
P1

### Type
task

### Labels
agent, mcp, resources, context

### Dependencies
skiff-m5-026

### Description
Expose Skiff context and durable state as MCP resources. Resources are for context; tools are for actions. This lets agents inspect services, releases, sagas, and events without guessing command syntax.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Define resource URI scheme:
  - `skiff://env/{env}/services/{service}/context`
  - `skiff://env/{env}/services/{service}/status`
  - `skiff://env/{env}/services/{service}/doctor`
  - `skiff://env/{env}/services/{service}/events`
  - `skiff://env/{env}/sagas/{saga_id}`
  - `skiff://env/{env}/operations/{operation_id}`
  - `skiff://schemas/{schema_id}`
  - `skiff://capabilities`
- Implement resource list.
- Implement resource read.
- Integrate redaction.
- Include MIME types:
  - `application/json` for JSON resources,
  - `text/markdown` for rendered summaries if useful.
- Add optional subscribe/watch support only if straightforward; otherwise defer.
- Add tests for URI parsing and resource reads.

#### Likely Files
- `internal/mcp/resources.go`
- `internal/mcp/uris.go`
- `internal/agent/context.go`
- `internal/agent/schema_registry.go`
- `internal/agent/capability_registry.go`
- `internal/client/client.go`
- `internal/mcp/resources_test.go`
- `docs/agents/mcp.md`

#### Design
Resource output should reuse existing agent context and schema registry. Do not create separate shapes.

Example resource:

```text
skiff://env/prod/services/payments-api/context
```

Should return the same content as:

```bash
skiff agent context payments-api --format json
```

with profile/redaction applied.

#### Testing / Validation
- URI parser accepts valid URIs and rejects invalid.
- Context resource returns redacted context.
- Schema resource returns schema JSON.
- Capabilities resource returns filtered capabilities.
- Read-only profile cannot read resources denied by policy.

#### Gotchas
- Resource URIs must not embed secrets.
- Avoid exposing raw object-state paths directly in v1 unless redacted and policy-checked.
- Be careful with service/env names in URI parsing.

#### Acceptance Criteria
- MCP resources expose context, schemas, and capabilities.
- Resource output is redacted and profile-aware.
- Tests cover URI parsing and read behavior.

---

## Add MCP read-only tools

### ID
skiff-m5-028

### Priority
P1

### Type
task

### Labels
agent, mcp, tools, readonly

### Dependencies
skiff-m5-026

skiff-m5-027

### Description
Implement read-only MCP tools backed by Skiff typed executors and capabilities. These tools are safe enough for default agent clients and should become the main integration path for coding/ops assistants.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Implement tool call adapter for read-only capabilities:
  - `skiff.status.get`,
  - `skiff.doctor.diagnose`,
  - `skiff.events.list`,
  - `skiff.agent.context`,
  - `skiff.agent.plan`,
  - `skiff.agent.validate_plan`,
  - `skiff.logs.query` if available,
  - `skiff.metrics.query` if available.
- Generate input schemas from schema registry/capabilities.
- Return structured JSON content.
- Add per-tool timeout.
- Add trace ID propagation.
- Add redaction and profile policy checks.
- Add tests with fake client.

#### Likely Files
- `internal/mcp/tools.go`
- `internal/mcp/tool_adapter.go`
- `internal/agent/executor.go`
- `internal/agent/context.go`
- `internal/agent/solve.go`
- `internal/agent/validate.go`
- `internal/mcp/tools_test.go`

#### Design
Tool names should be stable and namespaced:

```text
skiff.status.get
skiff.doctor.diagnose
skiff.agent.context
skiff.agent.plan
```

Do not expose raw CLI commands as tool names. The CLI rendering can be included in the tool description.

#### Testing / Validation
- Tool list includes read-only tools in readonly profile.
- Calling doctor tool returns doctor schema.
- Calling agent plan returns action graph schema.
- Denied tool call returns structured denial.
- Timeout is enforced.

#### Gotchas
- MCP clients may display tool descriptions to LLMs. Keep descriptions concise and safety-aware.
- Do not include secrets in descriptions or schemas.
- Do not call CLI subprocesses; use typed functions/client APIs.

#### Acceptance Criteria
- Read-only MCP tools work.
- Tools are generated from capabilities.
- Outputs are structured and redacted.
- Profile policy is enforced.

---

## Add MCP mutating tools with approval and idempotency

### ID
skiff-m5-029

### Priority
P2

### Type
task

### Labels
agent, mcp, mutating, approvals, idempotency

### Dependencies
skiff-m5-028

skiff-m5-018

### Description
Expose selected mutating operations through MCP only after approval, idempotency, and policy controls are fully implemented. This should be opt-in and disabled for readonly profiles.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add MCP tools for selected mutating capabilities:
  - `skiff.rollback.start`,
  - `skiff.ops.approve`,
  - `skiff.ops.resume`,
  - selected stateful recovery operations.
- Require:
  - `idempotency_key`,
  - `reason`,
  - actor/profile,
  - approval ID when required.
- Enforce policy using same evaluator as `agent apply`.
- Return `APPROVAL_REQUIRED` instead of executing when approval missing.
- Include recommended approval command/action.
- Add dry-run support.
- Add tests for denied/approval-required/approved execution.

#### Likely Files
- `internal/mcp/tools.go`
- `internal/mcp/mutating_tools.go`
- `internal/agent/apply.go`
- `internal/agent/policy.go`
- `internal/ops/intent.go`
- `internal/cli/ops.go`
- `internal/mcp/mutating_tools_test.go`

#### Design
Mutating tool input must include explicit fields:

```json
{
  "service": "payments-api",
  "to": "previous-stable",
  "idempotency_key": "incident-123-rollback",
  "reason": "restore health after failed canary",
  "approval_id": "approval_01J..."
}
```

Do not let an MCP client call mutating tools with only natural language.

#### Testing / Validation
- Readonly profile does not list mutating tools.
- Operator profile lists mutating tools as approval-required.
- Missing idempotency key fails.
- Missing approval fails for risk > threshold.
- Approved retry is idempotent.

#### Gotchas
- MCP clients may be driven by LLMs; make input schemas strict.
- Do not accept arbitrary `command` string inputs.
- Keep mutating tool set small in v1.

#### Acceptance Criteria
- Mutating MCP tools are opt-in/profile-gated.
- Approval and idempotency are enforced.
- Tests prove no mutation occurs without required metadata.

---

## Generate OpenAPI spec and expose it from skiffd

### ID
skiff-m5-030

### Priority
P1

### Type
task

### Labels
agent, openapi, skiffd, http, discovery

### Dependencies
skiff-m5-005

skiff-m5-006

### Description
Expose skiffd’s HTTP API through OpenAPI so HTTP-native agents and tools can discover endpoints, schemas, and response shapes. This is separate from MCP and useful for clients that prefer REST.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add `/openapi.json` endpoint to skiffd.
- Optionally add `/openapi.yaml` if YAML support is easy.
- Generate or hand-author OpenAPI for current endpoints:
  - `/healthz`,
  - `/readyz`,
  - `/version`,
  - `/v1/env`,
  - `/v1/status`,
  - `/v1/doctor`,
  - `/v1/services`,
  - `/v1/sagas`,
  - `/v1/events/recent`,
  - `/v1/events/stream`,
  - stateful endpoints currently exposed.
- Include schemas from schema registry where possible.
- Include error envelope schema.
- Add tests that `/openapi.json` returns valid JSON and includes expected paths.
- Add docs.

#### Likely Files
- `internal/skiffd/server.go`
- `internal/openapi/spec.go`
- `internal/openapi/schemas.go`
- `internal/agent/schema_registry.go`
- `internal/skiffd/openapi_test.go`
- `docs/agents/openapi.md`

#### Design
Start hand-authored if generation is too time-consuming. The goal is a useful spec, not perfect automation in v1.

Endpoint:

```text
GET /openapi.json
```

Response should include:
- `openapi`,
- `info`,
- `paths`,
- `components.schemas`.

If skiffd supports only JSON on many `/v1` endpoints, make that explicit.

#### Testing / Validation
- Parse OpenAPI JSON in test.
- Assert expected paths exist.
- Assert error envelope schema exists.
- Assert `operationId` values are stable.
- Assert agent endpoints from later bead appear once implemented.

#### Gotchas
- Do not leak config or secrets in example values.
- Avoid drifting schemas. Prefer referencing schema registry or shared definitions.
- SSE `/v1/events/stream` may not fit normal JSON response shape; document it carefully.

#### Acceptance Criteria
- skiffd serves `/openapi.json`.
- Spec includes current core endpoints.
- Spec includes schemas and error envelope.
- Tests catch missing core paths.

---

## Add skiffd agent HTTP endpoints

### ID
skiff-m5-031

### Priority
P1

### Type
task

### Labels
agent, skiffd, http, api

### Dependencies
skiff-m5-006

skiff-m5-011

skiff-m5-013

skiff-m5-018

skiff-m5-030

### Description
Expose agent-native functionality through skiffd, not only the CLI. This lets HTTP clients and remote agents use context, capabilities, planning, validation, and guarded apply without shelling out.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add endpoints:
  - `GET /v1/agent/capabilities`
  - `GET /v1/agent/schemas`
  - `GET /v1/agent/schemas/{id}`
  - `GET /v1/agent/context?service=...`
  - `POST /v1/agent/plan`
  - `POST /v1/agent/validate`
  - `POST /v1/agent/apply`
  - `GET /v1/agent/runs`
  - `GET /v1/agent/runs/{id}`
- Ensure endpoints use same core functions as CLI.
- Add profile/policy enforcement from authenticated actor.
- Add request IDs and trace IDs.
- Add redaction.
- Add OpenAPI entries.
- Add client API methods in `internal/client/api.go`.
- Add CLI agent commands that can use either direct mode or API mode.
- Add tests with `httptest`.

#### Likely Files
- `internal/skiffd/server.go`
- `internal/skiffd/agent_handlers.go`
- `internal/client/client.go`
- `internal/client/api.go`
- `internal/agent/*`
- `internal/openapi/spec.go`
- `internal/skiffd/agent_handlers_test.go`
- `internal/client/api_test.go`

#### Design
Use POST for plan/validate/apply when request bodies are complex. Use GET for schemas/capabilities/context if query params are sufficient.

Example plan request:

```json
{
  "service": "payments-api",
  "goal": "restore-health",
  "max_risk": "medium",
  "allow_mutating": false,
  "profile": "readonly"
}
```

Response is standard envelope with `skiff.agent-action-graph/v1`.

#### Testing / Validation
- Capabilities endpoint filters by profile.
- Context endpoint returns redacted context.
- Plan endpoint returns valid graph.
- Apply endpoint refuses unapproved mutating step.
- OpenAPI includes agent endpoints.

#### Gotchas
- Authenticated actor from skiffd middleware should become agent actor where appropriate.
- Do not let API endpoint bypass CLI policy behavior.
- Avoid duplicating CLI parsing logic in HTTP handlers.

#### Acceptance Criteria
- Agent operations are available over HTTP.
- CLI and HTTP share implementation.
- OpenAPI includes agent endpoints.
- Policy/redaction/trace behavior is consistent.

---

## Write docs, examples, and generated quickstarts

### ID
skiff-m5-032

### Priority
P1

### Type
task

### Labels
docs, examples, adoption, agent

### Dependencies
skiff-m5-010

skiff-m5-022

skiff-m5-029

skiff-m5-031

### Description
Document Skiff’s agent-native surface so users understand the model: typed plans, safe mutations, local subscription LLM handoff, MCP, and OpenAPI. Docs should include copy-paste examples and make the safety boundaries explicit.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Create `docs/agents/README.md`.
- Create docs for:
  - agent mode,
  - capabilities,
  - schemas,
  - context bundles,
  - planning,
  - validation/apply,
  - idempotency,
  - profiles/policy,
  - local LLM subscriptions,
  - API-key LLM providers,
  - MCP,
  - OpenAPI,
  - runbooks.
- Add quickstarts:
  - read-only diagnostic agent,
  - operator agent requiring approval,
  - local Claude subscription handoff,
  - MCP client setup,
  - HTTP/OpenAPI client setup.
- Add examples:
  - `examples/agents/readonly-diagnose.sh`,
  - `examples/agents/local-claude-assist.sh`,
  - `examples/agents/operator-rollback-with-approval.sh`,
  - `examples/agents/mcp-config.json`,
  - `examples/runbooks/restore-service-health.yaml`.
- Update root README agent-native section.
- Add `skiff agent quickstart --profile readonly` optional generator if useful.
- Add docs tests that check mentioned commands exist where feasible.

#### Likely Files
- `docs/agents/README.md`
- `docs/agents/capabilities.md`
- `docs/agents/schemas.md`
- `docs/agents/context.md`
- `docs/agents/plans.md`
- `docs/agents/llm_handoff.md`
- `docs/agents/mcp.md`
- `docs/agents/openapi.md`
- `docs/agents/runbooks.md`
- `examples/agents/*`
- `examples/runbooks/*`
- `README.md`
- `internal/cli/command.go`

#### Design
Docs should use realistic workflows:

Local Claude subscription:

```bash
skiff agent assist payments-api \
  --prompt "Fix this service. Use the generated logs, metrics, events, and Skiff plan. Propose safe next steps only." \
  --llm-provider local-claude \
  --profile diagnostic \
  --format json
```

Prompt-only fallback:

```bash
skiff agent prompt payments-api \
  --prompt "Diagnose this incident" \
  --out /tmp/skiff-payments-api.prompt.txt

claude -p "$(cat /tmp/skiff-payments-api.prompt.txt)"
```

Guarded apply:

```bash
skiff agent plan payments-api --profile operator --max-risk medium --out plan.json
skiff agent validate plan.json
skiff agent apply plan.json --step inspect_logs
skiff agent apply plan.json --step rollback_previous_stable --approval-id approval_01J... --idempotency-key incident-123
```

#### Testing / Validation
- Run markdown link checks if available.
- Run examples in dry-run/fake mode where possible.
- Ensure docs never imply LLMs can approve or execute mutations automatically.
- Ensure docs distinguish local subscription vs API-key provider.

#### Gotchas
- Do not document commands before they exist unless marked “planned”.
- Keep “AI native” framing focused on typed operations, not magic chatbot behavior.
- Avoid provider-specific claims that may change; keep local command configurable.

#### Acceptance Criteria
- Agent docs are complete and self-contained.
- Examples cover read-only, LLM handoff, MCP, and guarded mutation.
- README makes `agent` namespace obvious.
- Docs include safety boundaries.

---

## Add comprehensive agent contract and integration tests

### ID
skiff-m5-033

### Priority
P0

### Type
task

### Labels
tests, contracts, agent, ci

### Dependencies
skiff-m5-001

skiff-m5-002

skiff-m5-004

skiff-m5-005

skiff-m5-006

skiff-m5-007

skiff-m5-014

skiff-m5-018

skiff-m5-022

skiff-m5-026

skiff-m5-030

### Description
Add a comprehensive test suite that locks down agent-facing behavior. Agent contracts must be stable because external tools will parse them.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add golden tests for:
  - `--agent` error output,
  - capabilities output,
  - schemas list/get,
  - context,
  - plan,
  - validation,
  - apply dry-run,
  - JSONL event stream,
  - LLM prompt generation.
- Add schema validation tests for golden outputs.
- Add policy tests for profiles.
- Add idempotency tests for intents.
- Add MCP protocol tests.
- Add OpenAPI parse/path tests.
- Add fake LLM provider tests.
- Add docs/example smoke tests where feasible.
- Add CI target if repo has Makefile/CI:
  - `go test ./...`
  - optional `go test ./... -run Agent`.
- Ensure tests do not require cloud credentials or real LLM providers.

#### Likely Files
- `internal/cli/*_test.go`
- `internal/agent/*_test.go`
- `internal/llm/*_test.go`
- `internal/mcp/*_test.go`
- `internal/openapi/*_test.go`
- `internal/skiffd/*_test.go`
- `tests/agent/*`
- `internal/agent/testdata/*`
- `Makefile` or `.github/workflows/*` if present

#### Design
Tests should validate semantics more than enormous raw snapshots. Use golden files for stable envelopes and important shapes. Use semantic assertions for timestamps, trace IDs, IDs, and ordered steps.

Suggested categories:

```text
contract tests: shape/schema compatibility
unit tests: policy, validation, redaction, planning
integration tests: CLI command to fake client/store
protocol tests: MCP/OpenAPI
```

#### Testing / Validation
This bead is itself about tests. It is complete when the suite catches:
- missing schema IDs,
- malformed JSONL,
- mutating action without risk,
- capability referencing missing schema,
- `agent apply` executing unsupported operation,
- LLM invocation with unredacted context.

#### Gotchas
- Avoid tests that depend on real time. Inject clocks.
- Avoid tests that depend on command availability like real `claude`. Use fake binaries.
- Avoid tests that parse colorized output.

#### Acceptance Criteria
- Agent contracts have golden/semantic tests.
- Tests run offline.
- CI catches schema/capability drift.
- Fake LLM/MCP/OpenAPI tests are included.

---

## Preserve backward compatibility and define migration path

### ID
skiff-m5-034

### Priority
P1

### Type
task

### Labels
compatibility, migration, cli, agent

### Dependencies
skiff-m5-010

skiff-m5-013

skiff-m5-004

### Description
Ensure the new agent-native surface does not break existing CLI users. Preserve `solve`, current JSON shapes where required, and human output unless users explicitly opt into agent mode.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Keep `skiff solve` as an alias for `skiff agent plan`.
- Add deprecation note only if desired; do not remove.
- Ensure existing `--format json` outputs either remain compatible or include additive fields only.
- Clearly distinguish `--format json` vs `--agent` strict envelope behavior.
- Keep `json-pretty` behavior for human readability.
- Preserve existing exit codes.
- Add compatibility tests for old commands:
  - `version`,
  - `status`,
  - `doctor`,
  - `solve`,
  - `events`,
  - `ops`.
- Add migration docs:
  - “For old scripts, keep using existing commands.”
  - “For agents, prefer `skiff --agent` or `skiff agent ...`.”
  - “For streams, prefer `--format jsonl`.”
- Add warnings only in human help/docs, not in machine output unless schema supports warnings.

#### Likely Files
- `internal/cli/command.go`
- `internal/cli/solve.go`
- `internal/cli/json_output.go`
- `internal/cli/*_test.go`
- `docs/agents/migration.md`
- `README.md`

#### Design
Compatibility matrix:

```text
skiff doctor svc --format json
  old shape plus additive schema if safe

skiff --agent doctor svc
  strict agent envelope

skiff agent plan svc --format json
  strict agent action graph envelope

skiff solve svc --format json
  old solve-compatible output with schema added if safe
```

If this distinction gets too complex, implement a transition helper and document exactly what output mode each command uses.

#### Testing / Validation
- Snapshot old JSON output before changes and compare for additive-only behavior.
- Ensure `solve` still returns action graph.
- Ensure human help remains readable.
- Ensure `jsonl` not accidentally accepted by non-stream commands.

#### Gotchas
- Scripts may use `jq .doctor` or `jq .ActionGraph`. Avoid moving fields for old commands.
- Additive fields are usually safe but can still break strict consumers. Consider agent-only schemas for strict changes.
- Do not bury `agent` under dev help.

#### Acceptance Criteria
- Existing commands continue working.
- `solve` alias remains.
- Migration docs explain new preferred agent surface.
- Compatibility tests protect old behavior.

---

## Package and release M5 agent-native milestone

### ID
skiff-m5-035

### Priority
P1

### Type
task

### Labels
release, packaging, docs, agent

### Dependencies
skiff-m5-032

skiff-m5-033

skiff-m5-034

### Description
Prepare the agent-native milestone for release. This includes release notes, versioned schemas, docs, examples, and a final smoke-test checklist.

#### Staff Review
This bead has been reviewed against `AGENTS.md`, `README.md`, and the current code anchors listed at the top of this plan. Preserve object-storage durable truth, direct CLI recovery, typed sagas, JSON/agent output rules, and existing human/JSON compatibility while implementing it. The top-level review table records the specific systems-level decision for this bead.

#### Subtasks
- Add release notes section:
  - `Agent namespace`,
  - `--agent mode`,
  - `capabilities/schemas`,
  - `context/plan/validate/apply`,
  - `local Claude subscription LLM handoff`,
  - `MCP server`,
  - `OpenAPI`.
- Confirm all schema IDs are versioned.
- Confirm all experimental commands are labeled experimental in help/capabilities.
- Run full tests.
- Run examples in dry-run/fake mode.
- Verify docs mention safety boundaries.
- Verify no secrets in testdata.
- Verify `skiff agent capabilities` and MCP/OpenAPI are consistent.
- Create a final “agent-native smoke test” script.
- Add checklist to docs.

#### Likely Files
- `CHANGELOG.md` or release notes location if present
- `docs/agents/README.md`
- `examples/agents/smoke.sh`
- `internal/agent/schema_ids.go`
- `internal/agent/capability_registry.go`
- `internal/mcp/*`
- `internal/openapi/*`
- `README.md`

#### Design
Smoke test should be offline and deterministic:

```bash
skiff --agent version
skiff agent capabilities --format json
skiff agent schemas list --format json
skiff agent schemas get skiff.agent-action-graph/v1 --format json
skiff agent context payments-api --state memory://... --format json
skiff agent plan payments-api --format json
skiff agent validate plan.json --format json
skiff agent prompt payments-api --prompt "diagnose this service" --no-invoke --format json
```

If a real state fixture is needed, include one under examples/testdata.

#### Testing / Validation
- Full unit/integration test pass.
- Smoke script pass.
- Docs examples checked.
- No command requires real cloud or LLM in smoke mode.

#### Gotchas
- Do not mark unstable commands as stable in capabilities.
- Do not release local LLM invocation without redaction guard.
- Do not expose mutating MCP tools unless approvals/idempotency are complete.

#### Acceptance Criteria
- M5 has release notes.
- Agent schemas/capabilities are versioned and tested.
- Smoke test passes.
- Docs and examples are ready for users and implementation agents.
