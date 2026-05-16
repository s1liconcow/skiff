# State

`internal/state` owns Skiff durable object-state schemas, path helpers, and CAS control documents.

State code must write durable objects before any in-memory view is updated. Mutable control documents use object-store ETags as fencing tokens; immutable history remains create-only.

Use:

- `internal/state/paths` for every durable object key.
- `internal/state/schema` for schema-versioned durable documents.
- `internal/state/canonical` for stable JSON serialization and strict decoding.

The operator-facing layout is documented in `docs/dev/object-state-layout.md`.
