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
