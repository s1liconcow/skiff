# State

`internal/state` owns Skiff durable object-state schemas, path helpers, and CAS control documents.

State code must write durable objects before any in-memory view is updated. Mutable control documents use object-store ETags as fencing tokens; immutable history remains create-only.
