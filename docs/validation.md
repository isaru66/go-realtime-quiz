# Implementation and validation report

Validated November 15, 2026. All development phases were authorized; application,
deployment, infrastructure, tests, and operational documentation are implemented.
This is not a claim that a production Kubernetes environment has been certified.

## Executed checks

| Area | Evidence |
| --- | --- |
| Backend | `go test -race ./...`, `go vet ./...`, and binary/Docker builds |
| Protocol | Python contract suite: examples, both event directions, structural redaction, invalid messages, partition slots, document links |
| Frontend | 27 unit tests, TypeScript checking, production build, non-root Docker image, nginx/health/SPA checks |
| Redis deployment | Real six-node authenticated Cluster, three primaries/three replicas, AOF and persistent volumes |
| Cross-process gameplay | External WebSocket integration against separate backend/backend2 containers; 12 players, three questions, simultaneous duplicate retries |
| Scoring and recovery | Consistent published leaderboards, no duplicate scoring, cross-backend recovery, backend2 restart, all 12 identities/scores recovered on backend A |
| Redis failure | Stopped an isolated Redis primary; replica promotion restored readiness; authenticated retained identities/scores recovered; stopped node restarted |
| Backups | Three-primary backup/restore, 30 fixture keys, checksums and restart persistence in isolated Redis containers |
| Load smoke | k6 1.3.0: two concurrent games, ten players plus two hosts, 30 accepted answers, 24/24 checks passed |
| Initial smoke latency | Answer acknowledgment p95 10ms; WebSocket connect p95 25.93ms, in this local Docker environment only |
| Distributed load | Two games, 100 players plus two hosts split across both backend replicas; all 300 answers acknowledged, 306/306 checks passed |
| Distributed load latency | Answer acknowledgment p95 174.1ms; WebSocket connect p95 291.04ms; elevated test-only join limit 1000/minute |
| Infrastructure | Terraform format/validate and three mocked tests; both Kustomize overlays rendered and 58 resources schema-validated |

The full distributed regression command is in
[tests/integration](../tests/integration/README.md); the measured scenario took
approximately 80 seconds, explicitly discovering and failing the primary holding
the game's metadata. The 100-player k6 scenario took approximately 78 seconds.
Logs and temporary credentials used for these checks are session artifacts, not
committed source. No administrative/player credential values are reported here.

## Browser validation

The Azure-inspired blue/white UI was exercised through the running frontend:
joining a real lobby, persisting secure identity, refreshing, and restoring the
same player with one roster membership. A real backend restart exposed an
invalid browser-originated WebSocket close code, which is covered by a frontend
regression test. The fixed build was refreshed, migrated its saved identity to
the requested snake_case format without changing the player ID, and recovered
automatically to Connected after another real backend restart, without a page
refresh or UI error. A temporary 502 during pod unavailability was retried
successfully. At 390px viewport width the lobby had no horizontal overflow.

The first 100-player run exposed a race between a new socket's initial snapshot
and an older in-flight shared room read. The socket now uses a reader-sequence
catch-up barrier, so stale buffered reads cannot falsely trigger failover
recovery. Re-running the complete 100-player scenario passed every check.

## Deliberate implementation choices

- Go Fiber v2 implements HTTP and WebSocket transport as requested.
- Redis Lua handles answer validation, uniqueness, scoring, state revision,
  expiry, deadline indexing, and journaling atomically. Lifecycle/member changes
  use revision-checked atomic commits; no process-local production state store.
- Room-scoped Pub/Sub is only a wakeup. Shared local readers coalesce updates
  and reconcile durable Redis state; complete snapshots, not stream-event replay,
  are the authoritative recovery contract. Missed animation events are allowed.
- Redis metadata is a hash with a JSON `data` field; members/answers remain
  separate hashes, participants a set, scores sorted sets, and history a stream.
- ConfigMap reload keeps last-known-good local catalogs and pins complete backend
  quiz snapshots per game. No admin form writes Kubernetes resources.
- Fixed scoring and one accepted answer per question are the supported v1 policy.
- Administrative authentication is a deployment-secret bootstrap with expiring
  HttpOnly cookies, CSRF, and separate game capability, not a production SSO system.
- Creation receipt encryption/routing uses one shared deployment key. Keep it
  stable for retained sessions; multi-key online rotation is not implemented.

## Not validated here / deployment prerequisites

No live Kubernetes/AKS cluster, production DNS/TLS issuer, storage class,
off-cluster backup destination, Azure subscription credentials, or target
capacity/RPO/RTO was supplied. Consequently:

- AKS provisioning, real Ingress/TLS, NetworkPolicy enforcement, Kubernetes
  rolling termination/rescheduling, metrics-server/HPA behavior, and live cluster
  restore are not claimed as executed.
- The k6 smoke is not a maximum connection-capacity or horizontal-scaling study.
  Run larger source-IP-distributed scenarios and measure resource saturation
  before choosing production replica/Redis sizing.
- Redis asynchronous replication may lose recently acknowledged writes on
  failover. The failure test proves recovery of replicated state, not zero RPO.
- Default Redis pod traffic is authenticated but not encrypted. External browser
  traffic requires TLS; the backend supports verified Redis TLS when a suitably
  configured Redis deployment and CA are supplied.
- Backup RDB files are taken per primary, not as a globally atomic cluster
  snapshot. Follow the quiesced backup/restore procedure and validate the chosen
  durability policy before production use.

The [operations guide](operations.md) describes the environment-specific
provisioning, TLS, scaling, backup, restore, and failure exercises still required.
