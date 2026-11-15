# Go Fiber backend

The Go service uses Fiber v2, Fiber WebSocket, go-redis ClusterClient, validated
YAML configuration, structured slog JSON, and Prometheus metrics. Redis is the
only source of game/player state; local room hubs and connections are disposable.

```sh
go test -race ./...
go vet ./...
go build ./cmd/api
```

For a complete local environment use Compose from the root README. Standalone
development requires an actual Redis Cluster whose advertised node addresses
are reachable from the Go process; mapping a single node's port is not enough.

| Environment | Meaning |
| --- | --- |
| `LISTEN_ADDR` | Default `:8080` |
| `QUIZ_PATH` | Default `../../config/quizzes.yaml`, mounted directory in Kubernetes |
| `PROTOCOL_SCHEMA_PATH` | Default `../../docs/protocol-v1.schema.json` |
| `REDIS_ADDRS` | Comma-separated cluster discovery nodes |
| `REDIS_PASSWORD` | Required Redis authentication secret |
| `REDIS_TLS`, `REDIS_CA_FILE` | Enable verified Redis TLS and optional custom CA |
| `ADMIN_CREDENTIAL` | Required administrative secret, at least 20 characters |
| `APP_SECRET` | Required shared secret, at least 32 characters, for routing and encrypted creation receipts |
| `ALLOWED_ORIGINS` | Exact comma-separated browser origins; no wildcard |
| `COOKIE_SECURE` | Defaults true; false only for HTTP local development |
| `TRUSTED_PROXIES` | Optional comma-separated proxy IPs/CIDRs allowed to supply forwarded client IP |
| `JOIN_RATE_PER_MINUTE` | Default 30 attempts/IP/minute; tune for trusted NAT-heavy environments and isolated load tests |
| `GAME_IDLE_SECONDS` | Default 7200 |
| `GAME_LIFETIME_SECONDS` | Default 86400 |
| `RESULT_RETENTION_SECONDS` | Default 3600 |

Game policy values must be positive, no larger than seven days, with idle/result
retention no greater than lifetime; each game captures its starting policy.
`APP_SECRET` must remain unchanged across pods and for retained sessions: rotating
it without a receipt/routing migration prevents recovering creation responses.
Do not pass administrative/player credentials in URLs or log message bodies.

`/healthz` and `/livez` check the process. `/readyz` checks catalog availability
and Redis Cluster state on all discovered masters. `/metrics` is intended only
for the monitoring network. The binary's `-healthcheck` mode probes local
readiness for shell-free container health checks.

Lua validates deadlines against Redis TIME and performs answer uniqueness,
scoring, event journaling, and scheduling atomically. Less frequent lifecycle
changes use revision-checked atomic commits. Room-scoped Pub/Sub wakes shared
local room readers; periodic authoritative snapshots recover missed notifications,
pod loss, and trimmed history. Slow writers have a five-second deadline; hubs
coalesce state rather than growing a per-socket backlog.

Configuration reloads every two seconds with last-known-good retention. Active
sessions always use their Redis-persisted quiz snapshot, not the current file.
See the root [protocol](../../docs/websocket-protocol.md) and
[integration instructions](../../tests/integration/README.md).
