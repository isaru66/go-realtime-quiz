# Real-time load scenarios

Run against a disposable environment, never a shared production quiz:

```sh
ADMIN_CREDENTIAL='your-local-secret' k6 run tests/load/gameplay.js
GAMES=5 PLAYERS=20 BASE_URL=https://quiz.example.test ORIGIN=https://quiz.example.test \
  ADMIN_CREDENTIAL='deployment-secret' k6 run tests/load/gameplay.js
```

For cross-backend local load, set
`BACKEND_URLS=http://127.0.0.1:18080,http://127.0.0.1:18081` and
`ORIGIN=http://localhost:8088`. WebSocket VUs alternate between the two backends.
An isolated NAT-heavy test can start Compose with `JOIN_RATE_PER_MINUTE=1000`;
do not carry elevated test limits into production without a deliberate policy.

Each room has a host VU and simultaneous player VUs, waits for joining, answers
every question, advances after server closure, and checks final completion.
The default 2 rooms / 5 players is a smoke load, not a capacity claim.
Thresholds are initial test targets (100% checks, p95 connection <1s, answer
acknowledgment <500ms), not measured production guarantees.

The default per-IP join policy is 30/minute. Use distributed load generators
with realistic source IPs for larger join bursts, or explicitly configure a
test-only rate policy; do not bypass production rate controls by forging
forwarded headers. The backend trusts such headers only from configured proxies.
Record k6 output, image versions, topology, CPU/memory, Redis operation latency,
event metrics, and reconnect/failover outcomes for every capacity report.
Use `tests/integration` for credential, duplicate scoring, cross-backend, and
backend restart checks; an idle WebSocket count alone does not establish capacity.
