# Object Store

`internal/objstore` defines the provider-neutral coordination substrate used by Skiff state.

The base interface intentionally supports only:

- `Create` for immutable objects and create-if-absent writes.
- `CompareAndSwap` for mutable control documents.
- `Get`, `Head`, and prefix `List` for inspection and rebuilds.

Do not add delete operations to this package for normal state flows. Immutable history should remain create-only, and mutable state should be fenced through ETags on the relevant control document.
