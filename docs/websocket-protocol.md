# HTTP and WebSocket protocol v1

Status: implemented v1 contract. Machine-readable wire shapes are in
[protocol-v1.schema.json](protocol-v1.schema.json); executable examples and
negative cases live in `tests/contracts/test_protocol.py`.
See [Redis transactions](redis-data-model.md) for consistency and storage rules.
The [wire examples](protocol-v1.examples.json) use conspicuous all-`A` token
placeholders; applications must generate fresh random tokens, never copy them.

## 1. Connection and compatibility

Serve `/ws` using same-origin WSS through the TLS ingress. Require WebSocket
subprotocol `quiz.v1`; reject an incompatible handshake with HTTP 426.
Validate Origin against an explicit deployment allowlist before upgrading.
Never put credentials in the URL or subprotocol. Browser cookies alone do not
authorize a host socket: `host_connect` also proves CSRF and game capability.

Use UTF-8 JSON text messages, maximum 64 KiB inbound; no binary frames or
compression initially. Reject unknown event types/properties, duplicate JSON
object keys, invalid UUIDs/times, unsupported versions, or oversized inputs.
Use the schema's `client` definition for inbound and `server` for outbound
validation, not just the root union. Schema `format` checks must be enabled.
UUIDs must be canonical lowercase v4 (enforced by the schema and backend).
Tokens encode 32 random bytes as canonical
unpadded base64url (43 characters); decode and re-encode to reject noncanonical
encodings, not only a regex match.

Breaking changes require a new protocol/subprotocol. v1 objects are closed:
even additive wire fields require negotiated v2; clients must not silently
discard unknown private fields. Internal storage versions evolve separately.

All envelopes require:

| Field | Meaning |
| --- | --- |
| `event_id` | UUID v4; client command idempotency key or server event ID |
| `event_type` | One of the direction-specific events below |
| `protocol_version` | Integer `1` |
| `game_pin` | Six-digit string, never a number |
| `timestamp` | UTC RFC 3339 timestamp; client time is diagnostic only |
| `payload` | Event-specific closed object |
| `session_id` | Required after initial join; immutable UUID, not PIN |
| `player_id` | Required for player commands/directed results; absent on host commands |
| `revision` | Server-only room revision on authenticated state/event messages |
| `reply_to` | Server-only original command UUID for a direct response |

`error`/`reconnect_failed` may lack session/revision if authentication or lookup
failed. Never echo unvalidated identifiers or credentials. If a message cannot
be safely associated with a valid PIN/envelope, close 1008 instead of inventing
an error envelope. All valid commands receive one correlated success/error;
asynchronous changes and snapshots can also follow. The successful reply is not
a second room mutation.

## 2. Client events

| Event | Required identity / payload | Permission and behavior |
| --- | --- | --- |
| `join_game` | Player ID; name and reconnect token | First join in LOBBY; optional session ID must match if supplied. Matching existing credentials recover without another player. |
| `reconnect` | Session/player IDs; token; optional `last_revision` | Restore identity on any pod, including retained final results; a verified prior leave returns `game_left` instead. Last revision is a hint, not an authorization or replay promise. |
| `host_connect` | Session ID; host capability and CSRF token | Also requires live admin session cookie and ownership. No player ID. |
| `submit_answer` | Session/player IDs; question and option IDs | Bound player socket; one immutable accepted answer per question. |
| `leave_game` | Session/player IDs; empty payload | Explicit authenticated leave; keep local identity until confirmed. |
| `start_game` | Session ID; expected control revision | Bound owner host; LOBBY only. |
| `next_question` | Session ID; expected control revision | Bound owner host; QUESTION_CLOSED only. Finishes after last question. |
| `end_game` | Session ID; expected control revision | Bound owner host; finish early, closing an open question. |
| `request_snapshot` | Session ID; empty payload | Bound player/host; returns role-specific authoritative snapshot. |
| `ping` | Session ID; empty payload | Bound player/host; correlated `pong` with server time. |

First bind must arrive within 10 seconds. A socket binds to one session and one
role; switching games requires a new socket. After binding, compare every
envelope's session/PIN/player against the socket context. Host role is not a
payload option. Anonymous users cannot subscribe to rooms or request snapshots.
New members cannot join after start, but existing members can reconnect.
Names can be edited before first join; reconnect does not silently rename a
membership. Explicitly left credentials cannot rejoin the same session.

The browser generates/persists player UUID and reconnect token before sending
join. On `game_joined`, additionally persist session ID and expiry alongside the
four required identity fields. If the first acknowledgment was lost, retry the
same join credentials; a PIN remapped to a different session cannot be recovered
without a matching token. Once session ID is known, always use `reconnect`.
Client retryable mutations reuse the original event ID and semantic payload.
Do not blindly replay a timed-out answer: recover the snapshot first, then retry
that exact command only if the question is still open and no answer is recorded.

## 3. Server events and audience

| Event | Payload | Audience / emission |
| --- | --- | --- |
| `game_joined` | Player snapshot | Correlated join reply, only joining socket |
| `reconnect_success` | Player snapshot | Correlated recovery reply; fences prior socket |
| `reconnect_failed` | Error | Correlated failure on attempted recovery; preserve credentials except verified expiry |
| `host_connected` | Host snapshot | Correlated authenticated host bind |
| `state_snapshot` | Role-specific snapshot | After revisions, requested resync, periodic reconciliation |
| `player_joined` | Public membership | Room, first membership only |
| `player_left` | Public membership marked left | Room, explicit leave only, not a TCP disconnect |
| `game_started` | Empty | Room, before first `question_started` |
| `question_started` | Public question, start and deadline | Room, after committed opening |
| `question_timer` | Question ID, server time, deadline | Room sockets, approximately every 5s while open; not a state mutation |
| `answer_received` | Own question/option, accepted flag/time | Correlated reply to submitting player; never correctness/points |
| `question_ended` | Question ID, correct option, closed time | Room, only after closure |
| `answer_result` | Own answered/correct flags and points, correct option | Individual player after closure, including unanswered zero result |
| `answer_statistics` | Option counts and total count | Host only, after closure |
| `leaderboard_updated` | Published leaderboard | Room after closure and on finishing |
| `game_finished` | Empty | Room; resulting snapshot carries final leaderboard |
| `game_left` | Empty | Correlated leave receipt or verified prior-leave outcome during recovery, before closing socket |
| `command_accepted` | Empty | Correlated successful host command, including an idempotent retry |
| `pong` | Server time | Correlated ping response, not durable |
| `server_draining` | Empty | Authenticated local sockets before restart; not durable |
| `error` | Error | Requesting socket only; include `reply_to` when available |

Server messages get fresh UUIDs; retried responses may have new IDs and the same
`reply_to`. Replayed projections may also get new IDs. Suppress repeat animations
by `(session, revision, event_type, player_id)` rather than assuming network
exactly-once delivery. A snapshot is replaceable state, not an animation.

One revision may produce several events; different audiences see different
subsets. No consecutive public-revision requirement exists. Lifecycle ordering
within a projection is: game_started, question_started; or question_ended,
answer_result, answer_statistics (host only), leaderboard_updated,
game_finished if applicable. A complete `state_snapshot` follows the batch.
If behind, skip transient animations and send the newest snapshot instead.
Never send a historical open-question snapshot after a newer closed state.

## 4. Snapshot rules

Snapshots contain state, role, quiz ID/version, Redis server time, expiry,
control revision, roster with lease-derived connection status, and published
leaderboard. Envelope session/revision identify the snapshot's atomic boundary.
Connection generation remains backend-only; recovery uses complete snapshots,
not a client-controlled stream cursor.

- LOBBY: no question, answer, correctness, or statistics.
- QUESTION_OPEN: current public question, no correct option, result or counts.
  Players see only their own accepted option/time and previously published score.
  Leaderboard remains the previous closed question's totals.
- QUESTION_CLOSED: public question, `closed_result`, own result for players,
  aggregate statistics for host, updated published scores.
- FINISHED: final published board; retain last closed question/result if any.
  Ending from LOBBY has no question/result.
- EXPIRED: metadata and empty roster/leaderboard only; no own score or answers.

The JSON Schema enforces structural redaction. Backend checks must additionally
prove that any closed result belongs to a truly closed question in that session,
that `own_*` belongs to the socket principal, and open-question `own_score` and
leaderboard values came from `published`, not hidden current totals.
Do not serialize the pinned quiz, credentials, command receipts, raw per-player
answers, or future questions into public messages, even for the host.

Apply snapshots atomically in the UI. Ignore older snapshots on the same live
connection; allow equal revision updates for presence/server time. After a
recovery bind or Redis failover resync, replace state even if the revision went
backward: rollback is possible under the stated durability policy. The gateway
closes sockets with 1012 on detected Redis history regression to force that
explicit recovery boundary. Do not ignore a server-confirmed rollback forever.

## 5. HTTP API and authentication

All JSON responses use explicit DTOs; standard errors are
`{"request_id":"<uuid>","error":{"code":"...","message":"...","retryable":false}}`
with optional `retry_after_ms`. Return `X-Request-ID` and honor a valid incoming
UUID or generate one. Request/response bodies and authorization headers must
not be logged. All state-changing cookie requests require CSRF token and
allowed Origin; use `Cache-Control: no-store` for all API responses.

| Route | Authorization | Request / response |
| --- | --- | --- |
| `GET /api/v1/quizzes` | Public | `{catalog_version, quizzes:[{id,title,description,question_count}]}`; no questions/options/answers |
| `POST /api/v1/auth/session` | Deployment admin credential; rate-limited | JSON `{credential}`; constant-time credential verification; sets opaque HttpOnly Secure SameSite=Strict `__Host-quiz_admin` cookie (Path=/, no Domain), returns `{csrf_token,expires_at}` |
| `DELETE /api/v1/auth/session` | Admin cookie + CSRF | Revoke Redis session, expire cookie, 204 |
| `GET /api/v1/admin/config` | Admin | `{pod_id,active_version,validation_status,last_attempt_at,errors,quizzes}`; status `valid`/`reload_rejected`, metadata only, sanitized errors without quiz values |
| `POST /api/v1/games` | Admin cookie + CSRF | `Idempotency-Key` UUID header, `{quiz_id,quiz_version}`; 201 `{session_id,game_pin,quiz_id,quiz_version,host_capability,expires_at,control_revision}` |
| `GET /healthz`, `/livez` | Probe network | Process health, 200 or 503, no sensitive diagnostics |
| `GET /readyz` | Probe network | Catalog and Redis readiness, 200 or 503 |
| `GET /metrics` | Monitoring network only | Prometheus text; not publicly exposed through ingress |

`quiz_version` in create is the catalog version from selection; validate it on
the receiving pod and return 409 `configuration_changed` for refresh on mismatch.
A previously committed same-key creation is recovered before catalog checks,
so changing the ConfigMap cannot break a lost-response retry.
Authentication failures use 401, forbidden/CSRF 403, malformed body 400, invalid
quiz 404, state/idempotency conflict 409, rate limit 429 with `Retry-After`, and
Redis/unavailable capacity 503. Unsupported HTTP methods return 405.
HTTP-only codes `configuration_changed` and `quiz_not_found` supplement the
WebSocket error enum. Creation retries return the same 201 body and expiry
while live; no new game/capability. Never put host capabilities in QR links:
QR encodes same-origin `/?pin=<PIN>` only.
Persist the pending creation request and idempotency key in sessionStorage before
sending it; recover an uncertain response with that key, never a new one.

Admin sessions live 1 hour; no default credential is shipped. Redis stores an
opaque session digest/role and CSRF digest. The initial single administrative
credential maps to one stable configured owner principal across logins; web
session IDs are not owner IDs. Browser retains the CSRF token and
host capability in sessionStorage (not URL or player localStorage); reload can
reattach, another tab requires deliberate host transfer. A lost host capability
cannot be recovered from a player identity. Log in again if the admin cookie
expires; reuse the capability while its game remains live. Production OIDC and
multi-user administrative roles remain outside this initial scope.

## 6. Errors, liveness, reconnect, and closing

Wire error codes are enumerated by the schema. `temporarily_unavailable` and
`rate_limited` are retryable, with bounded backoff/optional retry-after.
`stale_control_revision` requires a snapshot before a new deliberate command.
`already_answered` requires a snapshot, not another option submission.
`connection_replaced` closes the old tab's socket and stops its auto-reconnect
until an explicit user recovery action, avoiding two tabs endlessly fencing
each other. Generic invalid-token responses do not confirm membership existence.

`session_expired` is returned only after checking retained membership credentials;
it permits clearing game/session/token fields, never the persistent player UUID.
If Redis data is absent or a PIN now belongs elsewhere, return
`session_unavailable`/`session_mismatch` and preserve local credentials until
explicit leave/forget. Do not create another player automatically on reconnect
failure. `wrong_pin` applies to initial joins only.
If an explicit leave committed but its acknowledgment was lost, authenticated
recovery returns `game_left` without creating a socket generation. This confirms
the earlier leave and permits local cleanup; do not loop forever on a departed
membership or automatically join it again.

Gateway sends WebSocket control Ping every 15s and updates the Redis presence
lease after a valid Pong on the bound generation; close after 45s without Pong.
Browser JS need not implement control Pong. Application `ping` provides a clock
sample and is rate-limited. Never trust clock synchronization to authorize answers.

| Close code | Meaning / client action |
| --- | --- |
| 1000 | Confirmed leave or normal host disconnect; do not auto-rejoin after leave |
| 1003 | Unsupported binary payload |
| 1008 | Invalid protocol, auth failure, or replaced connection; no blind retry |
| 1009 | Frame too large |
| 1012 | Pod drain/history regression; reconnect and recover |
| 1013 | Overload/slow client/temporary Redis outage; backoff and recover |
| 1006 | Browser-observed abnormal loss only; never transmitted by server |

Use full-jitter delay `uniform(0, min(30000, 500 * 2^attempt))` milliseconds,
capping the exponent to avoid overflow. Honor longer retry-after up to 60s.
No retry-count limit for ordinary network loss; reset attempts only after
successful session recovery. Show connecting/connected/recovering/offline.
Only explicit leave or authenticated expiry clears stored game credentials.
Failed requests, browser refresh, pod restart, and network loss never do.

## 7. Contract validation

Run `python -m unittest discover -s tests/contracts -v` with the development
dependencies from `tests/contracts/requirements.txt` installed in an isolated
environment. Tests check both directions, every event type, redaction/invalid
messages, examples, Cluster partition slots, and document links. This is
design-contract validation, not functional backend or failover testing.
