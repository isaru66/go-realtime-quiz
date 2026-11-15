# Quiz Live frontend

React, TypeScript, Vite, and Tailwind application with an Azure/Fluent-inspired
blue-and-white responsive interface. No quiz questions or deployment credentials
are bundled in the client.

## Development

Requires Node.js 22+ and npm.

```sh
cd src/frontend
npm ci
npm run dev
```

Open the displayed Vite URL. Vite proxies `/api` and `/ws` to
`http://127.0.0.1:8080`. Run the Go backend separately. Local backend cookies
require `COOKIE_SECURE=false` (`quiz_admin` cookie); production requires HTTPS
and the backend's `__Host-quiz_admin` cookie. JavaScript never reads either cookie.
Add the frontend origin to the backend's explicit allowed-origin configuration.

Routes: `/` is the player experience, `/#host` the host workspace, and `/#admin`
the authenticated read-only catalog/validation view. `/?pin=123456` prefills join.
Admin credentials are supplied at runtime through the sign-in form.

```sh
npm test
npm run typecheck
npm run build
npm run preview
```

Tests cover secure identity creation/preservation, retry backoff and actionable
errors, protocol validation/redaction, and WebSocket session recovery.
There is no separately configured linter. The build performs strict TypeScript
checking (including unused symbols), then emits production assets in `dist`.

## Recovery and privacy

Before the first join, localStorage stores a cryptographically generated UUIDv4
and canonical base64url token containing 32 random bytes, with name and PIN.
Successful snapshots additionally persist immutable session ID and expiry.
The `quiz.player.v1` JSON object uses `player_id`, `player_name`, `game_pin`,
`reconnect_token`, `session_id`, and `expires_at` (unavailable optional game
fields are omitted). Existing camelCase records are validated and migrated
without changing their UUID, name, PIN, token, session, or expiry. Invalid legacy
records are preserved rather than overwritten.
Storage writes must succeed before a join is sent. Neither transport loss,
unknown sessions, generic authentication errors, local clocks, nor elapsed saved
expiry delete player credentials. Only a confirmed server leave, authenticated
expiry, or explicit confirmed forget clears game fields; player UUID is retained.
Player joins use the Web Locks API and reread saved identity before writing, so
concurrent tabs cannot replace existing recovery credentials. Browsers without
Web Locks cannot start a join.
The exceptional corrupt-storage recovery action explicitly forgets the entire
device identity only after confirmation.

Host CSRF, capability, and the complete pending creation request/idempotency UUID
live in sessionStorage. Uncertain creation is retried using the same request key.
Do not duplicate a host tab as a transfer mechanism: browsers may copy
sessionStorage to an opened tab. Host capability transfer is deliberate, not
automatic. No host credential is included in a QR or URL.

Sockets negotiate `quiz.v1`. The checked-in `src/shared/protocol-v1.schema.json`
is an exact frontend copy of `docs/protocol-v1.schema.json`; synchronize it when
the contract changes. A test compares both schemas to prevent drift; run tests
from the repository checkout, where the canonical `docs` contract is available.
Ajv validates every inbound message, including closed
objects and role-private payload redaction. Snapshots replace state atomically.
Recovery binds accept an authoritative revision rollback; older snapshots on the
same connection are ignored. Answer acceptance never displays correctness.
An uncertain answer is replayed with its original ID only after recovery proves
the same question is open and no answer was accepted. Host controls are not
automatically replayed: recover state and deliberately choose the next action.

Ordinary connection loss retries indefinitely with full jitter (500ms base,
30s cap). Attempts reset after a successful recovery snapshot, not an open TCP
socket. A server retry-after can extend waits to 60s. A replaced/policy-closed
connection pauses until the user explicitly recovers in that tab.
Drain notices and bind timeouts close the browser socket with private code
4000 and retain automatic recovery. Server-originated 1012/1013 closes also
recover; browsers must not send those reserved close codes themselves.

## Container

Build context **must be `src/frontend`**:

```sh
docker build -t quiz-frontend src/frontend
docker run --rm --network YOUR_NETWORK -p 8088:8080 quiz-frontend
```

The multi-stage image runs nginx as UID 101 on port 8080 and serves `/healthz`.
`/api/` and `/ws` proxy to `backend:8080`, the Compose/Kubernetes Service name.
Hashed `/assets/` files are immutable-cacheable; the SPA entry point revalidates.
The WebSocket proxy supports upgrades and 75-second idle read timeouts.
Terminate TLS at the deployment ingress. The application uses same-origin
requests and never injects secrets at build time.
