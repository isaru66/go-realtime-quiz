# Gameplay interaction sequences

Status: implemented interaction design. Labels marked atomic refer to
[Redis transaction contracts](redis-data-model.md).
Wire names and recovery behavior follow the [protocol](websocket-protocol.md).

## Joining (including a lost acknowledgment)

```mermaid
sequenceDiagram
    participant B as Player browser
    participant L as localStorage
    participant A as Backend pod A
    participant R as Redis Cluster room partition
    participant H as Host on backend pod B
    B->>L: Persist UUID, name, PIN, secure token before sending
    B->>A: WSS quiz.v1 / join_game
    A->>R: Rate-limit, resolve PIN to immutable session
    A->>R: Subscribe room notifications, await acknowledgment
    A->>R: Atomic join(token digest, connection ID)
    Note over R: Check LOBBY for new member; create once, fence old sockets
    R-->>A: Membership and generation
    A->>R: Atomic authorized snapshot
    R-->>A: Redacted snapshot at revision N
    A-->>B: game_joined(snapshot, session ID, revision N)
    B->>L: Persist session ID and expiry
    A->>R: Best-effort notification after commit
    R-->>H: Room wakeup; read durable snapshot
    H-->>H: Project player_joined and host snapshot
    Note over A,R: Reader-sequence barrier skips stale reads; reconcile if notification lost
    opt First acknowledgment lost before session ID was saved
        B->>A: Retry join_game with identical UUID and token
        A->>R: Atomic bind existing membership, no new player/score
        A-->>B: game_joined(current snapshot)
    end
```

Different tokens for an existing UUID fail without revealing its membership.
A missing PIN never silently creates a room. A late new member is rejected;
valid existing membership recovery does not require the room to remain LOBBY.

## Answering, closing, and cross-pod delivery

```mermaid
sequenceDiagram
    participant P as Player on pod A
    participant A as Backend pod A
    participant R as Redis Cluster room partition
    participant W as Deadline worker on any pod
    participant G as Gateway pod B
    participant H as Host/players on pod B
    P->>A: submit_answer(command UUID, session, question, option)
    A->>R: Atomic submit(keys, bound identity/generation)
    Note over R: Check Redis TIME before deadline, membership, option, answer uniqueness
    Note over R: Store answer + private total + revision + stream change together
    R-->>A: Accepted option/time (no correctness)
    A-->>P: answer_received(reply_to)
    A->>R: Best-effort room notification
    R-->>G: Wake reader; durable state snapshot is source
    G-->>H: Redacted snapshot (published score unchanged)
    opt Concurrent duplicate or lost acknowledgment retry
        P->>A: Same command and option
        A->>R: Read original acceptance; never add points again
        A-->>P: answer_received(original acceptance)
    end
    W->>R: Poll due index; atomic close matching question
    Note over R: TIME >= deadline; copy totals to published, record closure, append change
    W->>R: Best-effort room notification
    R-->>G: Wake reader (or periodic reconciliation detects revision)
    G-->>H: question_ended, own answer_result OR host statistics
    G-->>H: leaderboard_updated, state_snapshot
    Note over A,P: Pod A independently catches up the same room for its local sockets
```

Answers at/after deadline fail even if no worker has closed the question.
Closing and answering cannot interleave within their atomic transactions.
Host early end is the sole authorized pre-deadline closure path.
Any Redis failover loss is surfaced by recovered authoritative snapshots, not
hidden by promising exactly-once delivery.

## Reconnecting to another pod

```mermaid
sequenceDiagram
    participant B as Player browser
    participant L as localStorage
    participant A as Old backend pod A
    participant C as New backend pod C
    participant R as Redis Cluster room partition
    A--xB: Network loss or pod termination
    Note over B,L: Keep identity and game credentials; display recovery status
    B->>B: Exponential backoff with full jitter, capped interval
    B->>L: Read UUID, session ID, PIN, token
    B->>C: WSS quiz.v1 / reconnect(last revision hint)
    C->>R: Rate-limit; verify session and token digest
    C->>R: Subscribe and await acknowledgment
    C->>R: Atomic bind new connection generation
    Note over R: Same membership/answers/score; old socket fenced
    C->>R: Atomic audience snapshot
    R-->>C: Current question, own answer, published score, state
    C-->>B: reconnect_success(snapshot)
    B->>B: Replace state, restore answer status, reset backoff
    C->>R: Reconcile durable state after reader-sequence barrier
    C-->>B: Lifecycle projections then latest snapshot
    opt Old socket still alive
        A->>R: Heartbeat or command with old generation
        R-->>A: connection_replaced
        A-->>B: Error then 1008; old tab stops automatic retry
    end
    alt Verified expired membership
        C-->>B: reconnect_failed(session_expired)
        B->>L: Clear game credentials, preserve player UUID
    else Redis unavailable or unknown old session
        C-->>B: Retryable outage OR session_unavailable
        Note over B,L: Preserve identity; retry only transient errors
    end
```

Expiry/outage branches represent alternatives to a successful bind, not messages
sent after successful recovery. The new gateway never depends on old pod memory.
An idle disconnected membership remains in the game; presence lease expiry is
not `player_left`.
