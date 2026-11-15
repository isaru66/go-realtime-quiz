# Real-time Multiplayer Quiz Platform

A runnable, Kubernetes-oriented multiplayer quiz platform with a Go Fiber
REST/WebSocket backend, an Azure-blue-and-white React/TypeScript interface, and
Redis Cluster as the shared authority for games, identities, answers and scores.

## Status

The application, two-backend local environment, six-node Redis Cluster,
Kustomize overlays, Azure Terraform modules, integration tests and load scenarios
are implemented. Players can join, answer synchronized questions, view results,
and reconnect; hosts manage games and admins inspect the versioned quiz catalog.

Local checks are not a production availability or capacity guarantee. Live
AKS provisioning, Kubernetes admission/network enforcement, HPA behavior,
cloud backup upload and Kubernetes restoration require separate staging
validation. See the [measured validation report](docs/validation.md) and
[operations guide](docs/operations.md) for what was actually tested and the
remaining boundaries.

Local validation includes two-backend gameplay, backend restart and Redis
primary failover recovery, browser refresh/reconnect, and a small k6 gameplay
smoke test. These results do not establish production capacity.

## Run locally

Requirements: Docker Engine, Docker Compose v2, and OpenSSL. Run from the
repository root. Backend images build with repository-root context; no local
Go or Node installation is needed for Compose.

```bash
# Optional standalone Compose fallback when the Docker CLI plugin is absent:
# export DOCKER_COMPOSE_BIN=/absolute/path/to/docker-compose
compose() {
  if [ -n "${DOCKER_COMPOSE_BIN:-}" ]; then
    "$DOCKER_COMPOSE_BIN" "$@"
  else
    docker compose "$@"
  fi
}

export QUIZ_COMPOSE_PROJECT="${QUIZ_COMPOSE_PROJECT:-quiz-session-local}"
export QUIZ_COMPOSE_ENV="${QUIZ_COMPOSE_ENV:-$PWD/deploy/compose/.env}"

# Generate three independent secrets once. Reuse this file on every restart.
# To keep credentials outside the checkout, set QUIZ_COMPOSE_ENV beforehand
# to an absolute path in an existing private directory.
umask 077
if [ ! -e "$QUIZ_COMPOSE_ENV" ]; then
  for name in REDIS_PASSWORD ADMIN_CREDENTIAL APP_SECRET; do
    printf '%s=' "$name"
    openssl rand -base64 48 | tr -d '\n'
    printf '\n'
  done > "$QUIZ_COMPOSE_ENV"
fi

compose -p "$QUIZ_COMPOSE_PROJECT" --env-file "$QUIZ_COMPOSE_ENV" \
  -f deploy/compose/compose.yaml config --quiet
compose -p "$QUIZ_COMPOSE_PROJECT" --env-file "$QUIZ_COMPOSE_ENV" \
  -f deploy/compose/compose.yaml up --build --wait --wait-timeout 240
```

The env file requires `REDIS_PASSWORD`, `ADMIN_CREDENTIAL`, and
`APP_SECRET`; there are no committed default secrets. `.env` files are ignored.
`deploy/compose/.env.example` documents the variables. Do not commit generated
secret files under other names, print them into logs, or regenerate them during
a restart. `APP_SECRET` is a raw base64 string, not a decoded key.

| Endpoint | URL |
|---|---|
| Player interface | <http://localhost:8088> |
| Host interface | <http://localhost:8088/#host> |
| Read-only admin interface | <http://localhost:8088/#admin> |
| Backend A readiness | <http://127.0.0.1:18080/readyz> |
| Backend B readiness | <http://127.0.0.1:18081/readyz> |
| Public quiz catalog through frontend | <http://localhost:8088/api/v1/quizzes> |

Use the protected env file's `ADMIN_CREDENTIAL` to sign in as host/admin.
Backend ports bind only to localhost; frontend port 8088 is published on all
interfaces. Frontend nginx proxies to `backend:8080`. `backend` and `backend2`
are independent processes sharing `redis-0` through `redis-5`; the one-shot
`redis-init` service validates cluster initialization before backend startup.
Redis ports are not published to the host. Docker-network Redis discovery is:

```text
REDIS_ADDRS=redis-0:6379,redis-1:6379,redis-2:6379,redis-3:6379,redis-4:6379,redis-5:6379
```

The sample `azure-fundamentals` quiz has three 20-second, 1000-point questions.
Both backends mount `config/quizzes.yaml` read-only. Local cookies use
`quiz_admin` with `COOKIE_SECURE=false`; allowed browser origins are
`http://localhost:8088,http://localhost:5173`. Production HTTPS uses
`__Host-quiz_admin`. No administrative credentials or correct-answer source
files are bundled into the frontend.

Optional backend defaults are `GAME_IDLE_SECONDS=7200`,
`GAME_LIFETIME_SECONDS=86400`, `RESULT_RETENTION_SECONDS=3600`, and
`JOIN_RATE_PER_MINUTE=30` (per source IP). Set these in the Compose env file or
shell before recreating backends. New games capture the configured lifetime
policy; existing games retain theirs. Disposable load tests behind one source
IP may need a higher join budget; tune deliberately for shared-NAT deployments,
without trusting forged forwarded headers. See
[operations](docs/operations.md#backend-environment-contract) for bounds and
Kubernetes ConfigMap overrides.

Stop without deleting persistent Redis data:

```bash
compose -p "$QUIZ_COMPOSE_PROJECT" --env-file "$QUIZ_COMPOSE_ENV" \
  -f deploy/compose/compose.yaml down
```

Do not add `--volumes` unless intentionally destroying this disposable
environment. See [operations](docs/operations.md) for credential rotation,
Redis inspection, failure recovery and persistence boundaries.

## Project layout

```text
.
|-- src/
|   |-- backend/
|   |   |-- cmd/api/                # Go process entry point
|   |   `-- internal/
|   |       |-- config/            # Validated, versioned quiz catalog
|   |       |-- game/              # Lifecycle, scoring, repository contracts
|   |       |-- redis/             # Cluster state, atomic commands, messaging
|   |       |-- transport/         # REST and WebSocket adapters
|   |       `-- observability/     # Logging, metrics, request correlation
|   `-- frontend/
|       |-- public/
|       `-- src/
|           |-- app/               # Routing and application composition
|           |-- features/
|           |   |-- player/
|           |   |-- host/
|           |   `-- admin/
|           `-- shared/            # Protocol, connection, identity, UI helpers
|-- infra/                         # Azure Terraform used by azd (see infra/README.md)
|   `-- modules/                   # network, aks, acr, monitoring, keyvault
|-- azure.yaml                     # azd project: Terraform provider, AKS services
|-- config/                        # Sample backend-only quiz YAML
|-- deploy/
|   |-- compose/                   # Two backends, frontend, six Redis nodes
|   |-- backup/                    # Cluster-aware backup utility image
|   `-- kubernetes/
|       |-- base/
|       `-- overlays/
|           |-- development/
|           `-- production/
|-- tests/
|   |-- contracts/                 # Executable schema and design checks
|   |-- integration/               # Real Redis Cluster and cross-pod tests
|   `-- load/                      # k6 scenarios
`-- docs/
    |-- architecture.md            # Technical design and architecture diagram
    |-- operations.md              # Deployment, backup, restore, RPO/RTO
    |-- validation.md              # Measured checks and limitations
    |-- redis-data-model.md        # Cluster keys, transactions, recovery
    |-- websocket-protocol.md      # HTTP/WS contracts, authorization, errors
    |-- protocol-v1.schema.json    # Direction-specific wire schemas
    |-- protocol-v1.examples.json  # Join, answer, and recovery examples
    `-- sequences.md               # Joining, answering, reconnecting diagrams
```

## Design and development

Read [the architecture](docs/architecture.md) and
[the development plan](docs/development-plan.md) first.

The
[Redis data model](docs/redis-data-model.md),
[versioned WebSocket contracts](docs/websocket-protocol.md), and
[join/answer/reconnect sequences](docs/sequences.md) define the shared
application contracts. Gameplay state is Redis-backed; replicas do not rely on
sticky sessions or a process-local fallback.

With Go 1.27.3 and Node.js 22 installed:

```bash
(cd src/backend && go test -race ./...)
(cd src/frontend && npm ci && npm test && npm run build)
```

See [real-cluster integration tests](tests/integration/README.md) for two-backend
WebSocket, reconnect and opt-in failure checks, and
[k6 load scenarios](tests/load/README.md) for full gameplay load. Failure tests
must target only your own disposable `quiz-session-*` project. A smoke load is
not a validated production capacity figure.

## Contract checks

Python 3 with `venv` and `pip` is sufficient for schema/design checks:

```sh
python3 -m venv .venv
.venv/bin/python -m pip install -r tests/contracts/requirements.txt
PYTHONDONTWRITEBYTECODE=1 .venv/bin/python -m unittest discover -s tests/contracts -v
git -c core.whitespace=cr-at-eol diff --check
```

Checks cover protocol examples, all event shapes, invalid messages and structural
answer redaction, Redis hash-slot layout, and document links. They do not prove
Redis atomicity, browser behavior, failover, or load capacity.
The whitespace check accommodates existing tracked CRLF files without rewriting
their unrelated lines; new contract files use LF.

## Kubernetes and Azure

Read [operations](docs/operations.md) before deploying. It covers development
and production Kustomize overlays, TLS ingress, external Secret inputs,
persistent storage, images, network policies, bootstrap, backups and recovery.
[Terraform infrastructure](infra/README.md), driven by `azure.yaml` and azd,
defines AKS, ACR, networking, monitoring, Key Vault and kubelet image-pull
permissions; no cloud provisioning happens during local Compose startup.

Redis replicas use asynchronous replication, and acknowledged writes can be
lost during failover. Scheduled per-primary RDB backups are not a distributed
atomic snapshot. Redis's supplied internal transport is authenticated but
plaintext inside NetworkPolicies; server/bus TLS and internal mTLS are not
implemented by the overlays. Accept or replace that boundary before production.
