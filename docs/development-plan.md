# Incremental development plan

Each phase requires approval before the next phase starts. The complete runnable
platform is the end goal, not the output of the architecture phase.

At every gate, report implementation/design changes, generated files, checks run
and their outcomes, and outstanding issues. Do not label planned tests as passed.

## Phase 1 - Architecture and technical design

- Establish `src/backend`, `src/frontend`, `docs`, configuration, deployment,
  integration-test, and load-test directories.
- Document component boundaries, immutable configuration snapshots, authoritative
  deadlines, cross-pod recovery, security, operations, and durability tradeoffs.
- Include the architecture diagram and explicit later-phase acceptance criteria.
- Check scaffold integrity, relative document links, and whitespace.
- Gate: approve architecture, proposed defaults, and implementation sequence.

## Phase 2 - Redis data model and WebSocket protocol

- Specify exact same-slot key/hash-tag layout, coordination partitions, PIN
  reservation, immutable session IDs, TTL cleanup, and due-work discovery.
- Specify atomic mutation preconditions, idempotency, revisions, safe retries,
  Redis Stream retention, Pub/Sub reconciliation, and snapshot ordering.
- Define versioned client/server JSON schemas, host commands, audience-specific
  payloads, errors, authentication, REST routes, and close codes.
- Add joining, answering, and reconnecting sequence diagrams under `docs`.
- Check examples against schemas and review every invariant against transactions.
- Gate: approve contracts before backend implementation.

## Phase 3 - Go backend and quiz engine

- Initialize the Go module and pin dependencies; add server lifecycle, REST and
  WebSocket transports, domain transitions, scoring, and repository contracts.
- Implement YAML startup validation, safe reload, immutable versions, public DTOs,
  authentication boundaries, structured logs, probes, and metrics.
- Add backend Dockerfile, unit tests, and exact local build/run instructions.
- Use test doubles only in tests; do not ship a process-local game-state backend.
- Run focused tests, Go formatting, vet, and build.
- Gate: approve backend behavior; Redis persistence remains Phase 4.

## Phase 4 - Redis Cluster and cross-pod communication

- Implement cluster client configuration, Lua mutations, durable state/history,
  deadline coordination, scoped fan-out, rate limiting, and snapshot recovery.
- Add real multi-node Redis Compose topology, backend wiring, and cluster health
  checks; expose enough backend replicas for cross-process integration.
- Run concurrent answer/retry, cross-pod event, missed-event, timer-worker restart,
  and configuration-pinning integration tests against actual Redis Cluster.
- Gate: approve distributed backend before building user flows.

## Phase 5 - React frontend

- Initialize Vite/React/TypeScript/Tailwind with pinned dependencies and lockfile.
- Implement responsive player, host, read-only admin views, QR join URL, and
  typed connection/event handling without leaking backend quiz content.
- Add frontend Dockerfile/static serving configuration and Compose integration.
- Run type checks, production build, lint, and focused UI tests.
- Gate: approve UI flows; full persistent recovery follows in Phase 6.

## Phase 6 - Persistent identity and reconnection

- Complete localStorage identity lifecycle, secure token bootstrap, idempotent
  join/rejoin, backoff/jitter, status display, and authoritative snapshot restore.
- Cover expired sessions, uncertain submissions, duplicate tabs/old sockets,
  explicit leave, and corrupt/unavailable localStorage behavior.
- Run browser-refresh, network-interruption, cross-backend recovery, and duplicate
  identity/score regression tests.
- Gate: approve reliable identity preservation and recovery UX.

## Phase 7 - Kubernetes deployment

- Add Kustomize Deployments, Services, Redis Cluster lifecycle/bootstrap,
  example quiz ConfigMap, Secret references, TLS ingress, HPA, disruption budgets,
  NetworkPolicies, probes, drain behavior, and environment overlays.
- Add persistence/backup/restore documentation and configurable resource settings.
- Implement the `infra/modules` Terraform for network, AKS, ACR, monitoring, and
  Key Vault. Add the `AZURE_AKS_CLUSTER_NAME` and
  `AZURE_CONTAINER_REGISTRY_ENDPOINT` outputs, then verify `azd provision` and
  `azd deploy`.
  Check quotas for the confirmed subscription and region first; consider remote state.
- Expand README with actual local setup, image build, deployment, secret
  provisioning, configuration updates, operations, and cleanup instructions.
- Render/validate manifests and test rollout, probes, config reload, TLS/WebSocket,
  policy connectivity, Redis bootstrap, and restore on a test cluster.
- Gate: approve deployable infrastructure and explicit durability policy.

## Phase 8 - Automated system tests and load tests

- Consolidate functional coverage from create/join/answer through final results.
- Add k6 scripts for many rooms, join bursts, answer concurrency, and connections.
- Add reproducible browser, multi-pod, backend-restart, Redis-failover, and
  reconnect regressions with configurable endpoints and no committed secrets.
- Record environment, commands, thresholds, and measured outcomes; distinguish
  unavailable infrastructure from successful tests.
- Gate: approve regression suite and capacity/failure validation plan.

## Phase 9 - Horizontal scaling and failure recovery

- Run multi-replica ingress-based games without affinity and verify synchronized
  questions, leaderboard consistency, deadlines, and exact-once scoring effects.
- Scale workloads, roll/kill backends, fail a Redis primary, and interrupt clients.
- Verify stable identity and config versions; quantify latency, event lag, Redis
  throughput, reconnect success, acknowledged-write loss, and recovery times.
- Exercise backup restoration and compare measured outcomes with approved
  capacity and durability targets. Resolve failures before claiming readiness.
- Gate: publish measured deployment confidence and operational limitations.
