# Frontend

Reserved for React, TypeScript, Tailwind CSS, and Vite, implemented in Phases 5
and 6.

- `src/app`: routing and application composition.
- `src/features/player`: join, lobby, questions, results, and leaderboard.
- `src/features/host`: quiz selection, room management, QR code, and controls.
- `src/features/admin`: authenticated, read-only catalog/validation status.
- `src/shared`: typed protocol, WebSocket recovery, local identity, and UI helpers.
- `public`: public static assets only.

Never import backend quiz YAML, correct answers, or deployment credentials into
frontend source or assets. Player credentials must survive refresh and temporary
network interruptions; the backend remains authoritative for scores and deadlines.

Package manifest, lockfile, Dockerfile, tests, and run commands will be added
during implementation. See [the design](../../docs/architecture.md).
