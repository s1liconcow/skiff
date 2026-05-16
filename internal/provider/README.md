# Provider

`internal/provider` is the boundary for cloud primitives.

Provider implementations may translate Skiff IR into AWS or future cloud APIs, but cloud SDK types must not leak into user-facing specs, provider-neutral IR, saga APIs, or CLI output structs.
