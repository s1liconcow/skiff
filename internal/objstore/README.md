# Object Store

`internal/objstore` defines the provider-neutral coordination substrate used by Skiff state.

The base interface intentionally supports only:

- `Create` for immutable objects and create-if-absent writes.
- `CompareAndSwap` for mutable control documents.
- `Get`, `Head`, and prefix `List` for inspection and rebuilds.

Do not add delete operations to this package for normal state flows. Immutable history should remain create-only, and mutable state should be fenced through ETags on the relevant control document.

Backends:

- `memory` is deterministic and intended for unit/integration tests.
- `s3` uses native S3 conditional writes: `If-None-Match: *` for `Create` and `If-Match: <etag>` for `CompareAndSwap`. S3 ETags are treated as opaque fencing tokens, never content hashes.
