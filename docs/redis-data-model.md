# Redis Cluster data and transaction contract

Status: implemented contract. The initial implementation uses snapshot-first
recovery rather than replaying individual stream entries to browsers.
See [architecture](architecture.md), [protocol](websocket-protocol.md), and
[interaction sequences](sequences.md).

## 1. Routing and keys

Use database 0 and the prefix `quiz:v1`. A PIN is a string matching
`^[1-9][0-9]{5}$`. Its coordination partition is `integer(PIN) % 64`, formatted
as two decimal digits. The partition count is a v1 storage invariant, not a
runtime tuning knob; changing it requires migration. Each tag hashes to a
different Redis Cluster slot. Many partitions may belong to the same primary.

Example: PIN `123456` belongs to partition `00`; all room transactions use the
literal hash tag `{quiz:p00}`. Session IDs are server-generated UUID v4 values,
not PINs. Let `T = quiz:v1:{quiz:p00}` and `S` be a session UUID.

| Key suffix after `T:` | Type | Contents |
| --- | --- | --- |
| `pin:123456` | String | Current immutable session ID |
| `create:<owner_digest>:<idempotency_digest>` | String | Request fingerprint and committed creation response |
| `game:S` | Hash | `data` field contains JSON metadata: PIN, session ID, quiz/catalog version, host-capability digest, state/revisions, question index/times, lifecycle policy |
| `quiz:S` | String | Immutable normalized full selected quiz JSON, including correct answers |
| `players:S` | Hash | Player UUID -> JSON membership record |
| `participants:S` | Set | UUIDs of memberships not explicitly left |
| `answers:S` | Hash | `q:<question_id>:<player_id>` -> accepted-answer JSON; `cmd:<player_id>:<event_id>` -> answer fingerprint/reference |
| `scores:S` | Sorted set | Player UUID -> authoritative total, including the open question |
| `published:S` | Sorted set | Player UUID -> total as of the most recently closed question |
| `events:S` | Stream | One internal change record per committed room revision |
| `commands:S` | Hash | `<principal>:<event_id>` -> fingerprint and durable command outcome |
| `due` | Sorted set | `<PIN>:<S>` -> earliest of question deadline and effective session expiry |
| `active` | Sorted set | `<PIN>:<S>` -> effective session expiry, for bounded metrics/reconciliation |
| `auth:<session_digest>` | String | Administrative web session identity, role, CSRF digest, expiry |
| `limit:<scope>:<digest>:<window>` | String counter | Fixed-window rate-limit count |

`due`, `active`, administrative sessions, and limit keys are partition resources,
not room resources. An admin session's partition is derived from its random
session token digest; it need not match a game. Validate the session separately
before a room transaction and pass the authenticated principal, never trust a
principal from the browser. Logout/revocation is checked on each host command.

Membership JSON contains name, SHA-256 reconnect-token digest, explicit-left flag,
connection generation, current connection ID, heartbeat expiry, and current
score-independent status. SHA-256 is appropriate for uniformly random 256-bit
tokens, not human passwords. Compare supplied digests in constant time in Go,
then require the same stored digest and generation in the mutation script to
prevent a check/use race. Raw tokens never enter Redis keys, streams, or logs.

Answer JSON contains option ID, server acceptance timestamp, question ID,
accepted command ID, correctness, and awarded points. It is internal data.
An accepted answer also writes its command-reference field in the same hash,
so reusing an event ID for a different question/option is an idempotency conflict.
These fields are bounded by twice the player/question product; rejected answer
commands are not persisted. A new command ID for an already answered question
returns `already_answered`, even if it chooses the same option.
Command outcomes contain no raw credentials; the creation response stores the
host capability encrypted with a deployment Secret shared by all backend pods,
bound to the session ID through authenticated encryption.
Keep that deployment key unchanged until those receipts expire; automated key
rotation/migration is not implemented.
This permits retrying a lost creation response without storing a bearer token in
plaintext. Encryption must use authenticated encryption with a random nonce.

IDs used in composite hash fields cannot contain separators: question/option IDs are
`[A-Za-z0-9_-]{1,64}`, player/session IDs are UUIDs, names are 1-40 Unicode
characters after trimming, and host principals are server-issued opaque IDs.
Quiz version is the lowercase hexadecimal SHA-256 of canonical catalog JSON:
sort object keys recursively, preserve all array order, use UTF-8 compact JSON
with no insignificant whitespace, and allow only bounded integer numbers.
Normalize omitted optional fields to documented defaults before hashing.
The normalized runtime snapshot preserves authored question/option order.
Phase 3 must pin the encoder and test equivalent YAML and meaningful order changes.

## 2. Boundedness and time

Initial configurable ceilings: 1,000 players/game, 100 questions/quiz,
8 options/question, 256 KiB selected-quiz snapshot, 64 KiB inbound WebSocket frame,
1 MiB outbound snapshot, 4,096 stream entries/game, and 20,000 retained mutating
command receipts/game.
Reject excess work explicitly (`capacity_exceeded`); never evict a command
receipt and silently re-execute its command. Join/reconnect generations and
answer records provide their own idempotency without accumulating command
receipts; host mutations and explicit leave use `commands:S`.

All stored timestamps are integer Unix milliseconds from Redis `TIME` inside
scripts; wire times are UTC RFC 3339 strings with millisecond precision.
Durations/points and resulting total scores are bounded integers, with
`questions * maximum_points <= 2^53 - 1`. Redis sorted-set scores must represent
the totals exactly. Question opening records `deadline = now + duration`.
An answer is valid only when `now < deadline`; closure is allowed when
`now >= deadline`. A question cannot extend beyond the session's expiry.
Set its deadline to the lesser of `now + duration` and effective expiry.

Default effective expiry is the minimum of `last_activity + 2h`, `created + 24h`,
and (after finishing) `finished + 1h`. Only first join, newly accepted answers,
and successful host transitions refresh activity. Reconnect, heartbeats,
duplicates, invalid commands, and spectators do not keep a game alive.
At creation, freeze these policy values in the game. Store `expires_at`
explicitly and update both partition indexes whenever it changes.

Every room key and creation receipt has a fixed physical `PEXPIREAT` of
`created + 24h + 10m` (or the configured absolute lifetime plus cleanup grace).
Assign that expiry inside every script that first creates a key. Logical checks
deny gameplay immediately at effective expiry, independent of cleanup or TTL.
Redis must use `noeviction`; memory exhaustion is an availability failure, not
permission to lose game state by eviction.

The expiry worker marks `EXPIRED`, emits a final change, removes `due`/`active`
members, and deletes the PIN mapping only if it still equals S. Keep remaining
room data until physical expiry as an authenticated expiry tombstone; snapshots
of expired games contain no answers/roster. Creation retry receipts must return
`session_expired`, not recreate expired sessions. Once metadata is physically
gone, return `session_unavailable`, not a guessed authenticated expiry.
PIN mappings share the physical TTL; creation may reclaim a logically expired
mapping atomically after checking the old game's expiry, even if a worker lags.
Its old `due` member remains safe because it includes S.

Partition indexes have no TTL. Workers remove entries whose room metadata is
missing and scan bounded batches (100 members/iteration). Rate counters expire
after their 60-second window plus 60 seconds; admin sessions expire after 1 hour,
without sliding renewal. A Redis-backed logout deletes the session.

## 3. Creation and PIN reservation

1. Authenticate host/admin, validate the request shape, and require an
   `Idempotency-Key` UUID. Fingerprint the normalized request and owner.
2. Derive a partition from HMAC-SHA-256 of `(owner, idempotency key)` using a
   stable server Secret; use the low six bits. All retries choose that partition.
3. Read any existing receipt to identify its original session. A matching receipt
   takes precedence over current catalog/version validation: a catalog reload
   must not invalidate recovery of an already committed game. With no receipt,
   validate the selected quiz/version, generate a random six-digit PIN uniformly
   from that partition, generate S and a 256-bit host capability, and prepare
   the pinned quiz snapshot.
4. Call `create_game`
   with the receipt key, its original-game metadata key (if a receipt exists),
   candidate PIN key, candidate old-game metadata key (if mapped), all new room
   keys, and `due`/`active` in `KEYS`. Verify both receipt and mapping still match
   the pre-read values; otherwise return a retryable conflict.
5. Before any writes, check an existing receipt: identical fingerprint returns
   the original response only if its original session is still live; changed
   request returns `idempotency_conflict`. Expired original sessions return
   `session_expired`, without another reservation.
   Check capacity, quiz limits, all key types, and PIN vacancy/logical expiry.
6. On success atomically store metadata/snapshot, PIN mapping, encrypted receipt,
   revision 1 (`game_created` internal record), and scheduler entries.
   Retry collisions at most 32 times, then report `capacity_exceeded`.

This avoids a cross-slot global PIN registry or two-phase reservation. Same-key
concurrent creation requests serialize on their partition receipt even when
candidate PINs differ. A fresh request key represents an intentional new game.
Creation receipts are retained through the original physical expiry, and keys
must not be retried after that retention horizon. Key rotation must preserve
the routing Secret until its creation receipts expire.

## 4. Script contracts

Every script receives an explicit, same-slot `KEYS` list; do not construct
undeclared Redis key names inside Lua. The caller supplies any old-game and
receipt-original-game keys needed for creation and verifies the pre-reads again
inside the script.
Scripts check logical expiry, data types, input limits, and business conditions
before their first write. Lua errors do not roll back earlier writes.
Handle Redis OOM/transport/runtime errors as uncertain, never as success; probe
the durable receipt/state before retrying. Memory headroom and `noeviction` are
required, but cannot turn Redis failover into a zero-loss guarantee.

| Operation | Preconditions and single atomic effect |
| --- | --- |
| `join` | Live LOBBY for new membership; matching existing token may recover in any non-expired state. A new member creates one hash entry, participant, zero totals, and next generation; duplicate join returns existing membership via the same bind rules as reconnect. Different token or explicitly left membership is rejected. |
| `bind_connection` | Matching token, not left/expired. For a fresh random connection ID, increment generation, set connection ID and heartbeat expiry, and emit one room change. Retrying the same ID returns the same generation. A matching token on a left membership returns a confirmed-leave outcome without rebinding. |
| `heartbeat` | Matching connection ID/generation; extend presence for 45s. Does not change game expiry or room revision. |
| `start` | Authenticated owner/capability, LOBBY, matching control revision and receipt constraints. Open question 0 and set deadline and indexes. |
| `submit_answer` | Matching token/generation and live membership. A known command ID with a changed fingerprint fails `idempotency_conflict`; an identical retry returns original acceptance (also after deadline/finish, while membership remains valid). Another command for an answered question returns `already_answered`. Otherwise require QUESTION_OPEN, matching question ID, valid option and `now < deadline`; persist answer/command reference and increment authoritative score exactly once. |
| `close_question` | QUESTION_OPEN with matching question ID and `now >= deadline`. Copy bounded scores into `published`, record closed time and aggregate option counts, set QUESTION_CLOSED, and clear the question deadline in `due`. Duplicate worker calls are no-ops. |
| `advance` | Authenticated owner/capability, QUESTION_CLOSED, matching control revision. Open next question, or finish if none remain; refresh indexes. |
| `end` | Authenticated owner/capability and matching control revision in any unfinished state. If open, close it immediately and publish accepted scores/results, then FINISHED; shorten expiry to result retention. |
| `leave` | Matching live membership token and generation; receipt checked before left-state rejection. Mark left, remove participant and presence, retain answer/score/name for historical rankings; repeat receipt returns the prior outcome. |
| `expire` | Effective expiry reached. Mark EXPIRED, remove indexes and compare-delete PIN mapping; do not replace a newer session. |
| `snapshot` | Authorized audience and matching session ID; enforce expiry and build an allowlisted atomic view with revision, server time, and control revision. |

Each effective mutation increments `revision` once and `XADD`s exactly one
change record with that revision, kind, affected IDs, timestamp, and bounded
internal projection data. The transaction also stores required command receipts
and updates indexes. Publish a notification only after commit; the notification
is `(S, revision)` on `quiz:v1:{quiz:p00}:room:S`.

`control_revision` starts at 0 and increments only on lifecycle transitions.
Host commands carry that value rather than the busy room revision: player
arrivals/answers must not spuriously invalidate a host click. A repeated host
command ID/fingerprint returns its original outcome before revision checks.
A reused ID with different content fails. Each receipt is keyed by principal,
and stored at most once. Permission and session checks still precede receipt
delivery; logout must not revive old host access.

Join/reconnect accepts the credential digest in one atomic bind so two pods
cannot duplicate memberships. New socket binds fence all older generations.
Every player command derives its identity from the authenticated socket context
and still checks Redis generation; envelope player IDs cannot impersonate users.
The periodic gateway heartbeat detects a superseded socket and closes it.
Presence is a short lease, not membership; disconnect never deletes a player.

Accepted correct answers add the fixed configured question points, others zero.
Public projections always read `published`, not `scores`, while a question is
open. At closure, the snapshot includes correctness only for closed questions
and only the receiving player's own answer; never another player's raw answer.
Host statistics are aggregate counts after closure. Left players remain in final
rankings marked `left`; ties use competition ranks (1, 1, 3) and UUID ascending
as stable display order, never as an extra score.

## 5. Deadline discovery and fencing

All pods scan all 64 partition `due` indexes every 250ms with randomized phase,
using bounded batches and backoff during Redis failures. Query using Redis time
on the owning primary; Go wall time may schedule an early attempt but cannot
authorize closure. No lease or unique worker is required: scripts serialize.

For each `<PIN>:<S>`, load keys and invoke the due script. It checks expiry first,
then the current deadline and question ID; it closes or expires atomically, or
updates the score to the next due time. A stale worker cannot close the next
question using an old deadline. Work remains indexed if a worker/pod dies.
Do not delete an index entry before a successful mutation. On Redis reconnect
resume overdue work; browser traffic is not part of scheduler correctness.

## 6. History, fan-out, snapshots, and recovery

Stream entries use Redis-generated IDs and include the integer room revision.
Use exact `MAXLEN 4096`; gaps are normal after trimming. Backend-only stream
entries may contain private outcome data; browsers never read Redis or raw
stream records. Fan-out uses audience-specific projections, never the serialized
internal object. Expiry has a final tombstone projection.

The Go implementation deliberately uses durable state snapshots, not stream
replay, as the recovery contract. Streams retain revision/kind/timestamp for
bounded operational history; they do not carry private answer payloads.
A room's one shared reader per pod subscribes, awaits acknowledgment, then reads
atomic state. A newly bound socket also obtains its own atomic snapshot.
The socket captures a reader-sequence barrier after that initial read and ignores
older in-flight reader updates; buffered/coalesced notifications trigger another
read after any race.

The reader checks durable state at least every second without Pub/Sub, batching
bursts at 250ms. Each local socket gets a size-one replaceable update channel,
never a load-balancing consumer group. Lifecycle/roster/own-answer changes are
sent promptly; otherwise snapshots reconcile at least every five seconds.
Snapshots replace state including own acceptance/published score; transient
animations can be skipped. Closed question results are recovered from state,
not inferred from seeing every animation.

Revision regression closes the socket with 1012 and forces a fresh authenticated
recovery boundary. Redis read failure closes with 1013. Writes have a five-second
deadline and there are at most 4,096 sockets/pod; room update queues coalesce
instead of growing. A single serialized snapshot is bounded by the game limits
and 1 MiB target. No exactly-once event replay or historical animation guarantee
is provided.

Guarantees: durable change journaling while retained; idempotent business effects
within a surviving Redis history; eventual snapshot convergence, not exactly-once
WebSocket delivery. Acknowledgment only follows the Redis commit. Async Redis
replication can still lose acknowledged answers/receipts on failover. Recovered
snapshots override optimistic acknowledgments and expose any rollback; do not
promise lossless failover. AOF, replicas, backups, RPO/RTO, and restore validation
are deployment responsibilities in Phases 7-9.

## 7. Rate limiting and failure responses

Use atomic `INCR`/first-`PEXPIRE` scripts: 30 unauthenticated join/reconnect
attempts per trusted client IP/minute, 120 player commands per membership/minute,
60 host control commands per owner/minute, and 10 admin-login attempts per
IP/minute. Charge failed attempts too. Same-slot member limits can precede
mutation in the room script; independent IP/auth limits are separate atomic
checks and deliberately need no cross-slot transaction. Limit before allocating
room subscriptions; cap unauthenticated sockets at the gateway/ingress.
Rate-limit retries may consume budget; duplicates never bypass authorization.

On Redis failure return `temporarily_unavailable`, stop accepting mutations,
mark readiness false, and retain browser credentials. Do not report `wrong_pin`,
`session_expired`, or success because a lookup timed out. Use bounded operation
timeouts (initially 2s), bounded cluster retries, jitter, and circuit protection.
Never blindly retry a non-idempotent operation after an uncertain result.

## 8. Transaction review / implementation acceptance

| Invariant | Required mechanism and Phase 4 regression |
| --- | --- |
| No CROSSSLOT mutations | Every script's declared KEYS share the partition tag; check all 64 tags and every operation |
| Unique PIN / idempotent create | Same-slot mapping, collision retries, shared receipt; concurrent same-key and different-key creation |
| One identity / fenced sockets | Atomic credential bind/generation; simultaneous joins and replacement sockets |
| One scoring effect | Answer field uniqueness, immutable question ID, atomic score update; concurrent submissions and lost reply retry |
| No late answer | Redis TIME strict deadline comparison; exact boundary and worker outage |
| No secret leak | Public score projection and closed-question allowlists; snapshots, stream fan-out, retry/error tests |
| No lost deadline work | Durable same-slot due index, state checks; kill every worker then resume |
| No missed-tail stall | Revision reconciliation without Pub/Sub; drop the last notification |
| Recoverable ordering | Atomic snapshot/cursor, per-socket barrier; race snapshot with submissions and trim history |
| Safe expiry / PIN reuse | Session IDs, compare-delete, logical checks; stale worker/reconnect against a reused PIN |
| Bounded resources | Input/receipt/stream/queue limits, cleanup; exhaust each limit without partial business writes |

See [validation](validation.md) for the subset exercised against real Redis
Cluster and two backend processes. Schema checks alone do not prove these
distributed invariants.
