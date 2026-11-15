# Real Redis Cluster / cross-backend integration

Start the Compose environment described in the root README, then run:

```sh
cd src/backend
BACKEND_A_URL=http://127.0.0.1:18080 \
BACKEND_B_URL=http://127.0.0.1:18081 \
ADMIN_CREDENTIAL='your-local-secret' \
  go test -tags=integration ./internal/transport -run TestDistributed -v -count=1 -timeout=4m
```

This tests real WebSockets through two distinct backend processes: idempotent
creation, a 12-player lobby, simultaneous duplicate answer retries, strict game
states, synchronized questions, hidden open-question scores, leaderboard totals,
cross-backend identity recovery, final completion, and lost-leave recovery.
Every received server event is validated against the shared JSON Schema.

Opt into restarting the isolated project's `backend2` during the test:

```sh
QUIZ_ALLOW_FAILURE_TESTS=true \
QUIZ_FAIL_REDIS_SERVICE=auto \
QUIZ_COMPOSE_PROJECT=quiz-session-example \
QUIZ_COMPOSE_ENV=/absolute/path/to/compose.env \
BACKEND_A_URL=http://127.0.0.1:18080 BACKEND_B_URL=http://127.0.0.1:18081 \
ADMIN_CREDENTIAL='your-local-secret' \
  go test -tags=integration ./internal/transport -run TestDistributed -v -count=1 -timeout=4m
```

The project name must start with `quiz-session-`; never point failure tests at a
shared project. Restarted connections recover on backend A with identical player
IDs and published scores. Setting `QUIZ_FAIL_REDIS_SERVICE=auto` also discovers
and stops the primary holding the game's actual metadata, waits for promotion/readiness, verifies retained
identity and scores, and restarts the stopped node. Kubernetes rescheduling and
capacity under load must be exercised separately as documented in operations.

## Redis deployment topology checks

From the repository root, with Docker Engine, Python 3.12+, and Go available:

```sh
docker build -f src/backend/Dockerfile -t quiz-backend:redis-test .
QUIZ_BACKEND_IMAGE=quiz-backend:redis-test \
  python -m unittest discover -s tests/deployment -p test_redis.py -v
```

These tests create uniquely named disposable networks/containers and remove
only their own containers and volumes. They exercise real Redis 7.4.7 single-
and six-node bootstrap, repeated initialization, single-node AOF persistence,
invalid/partial/mismatched state refusal, and one-/three-primary backups with
checksum verification. Uploads are stubbed: Azure connectivity and restore
still require a staging drill. The backend image enables the same cross-process
gameplay test against a single-node cluster, without failover expectations.
Without `QUIZ_BACKEND_IMAGE`, that API test is explicitly skipped.

Render/assert both modes in both Kubernetes environments without editing the
tracked overlays (requires kubectl with Kustomize):

```sh
python -m pip install -r tests/deployment/requirements.txt
python -m unittest discover -s tests/deployment -p test_kubernetes.py -v
```

CI additionally validates all four rendered manifests with kubeconform.
