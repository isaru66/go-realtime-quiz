# Backend

Reserved for the Go service, implemented in Phases 3 and 4.

- `cmd/api`: process entry point and dependency wiring.
- `internal/config`: YAML catalog validation, reload, and immutable versions.
- `internal/game`: lifecycle, scoring, domain types, and repository contracts.
- `internal/redis`: Cluster persistence, atomic operations, and room messaging.
- `internal/transport`: REST/WebSocket, authorization, and public projections.
- `internal/observability`: JSON logging, metrics, and correlation.

Redis is the shared source of game/player state. Local connections and immutable
catalog caches are disposable. Do not introduce an in-memory production fallback.

Go module, dependencies, Dockerfile, tests, and run commands will be added during
implementation. See [the design](../../docs/architecture.md).
