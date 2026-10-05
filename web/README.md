# FlowForge Dashboard

React/TypeScript local/demo control plane: Overview, Jobs, detail/Attempts,
Workers, Schedules and Dead Letter. Native REST controls require confirmation
and real server responses. WebSocket is only a transient invalidation hint.

Use Node 24. [Vite](https://vite.dev/guide/) requires Node 20.19+ / 22.12+.
From this directory:

```sh
npm ci --no-audit --no-fund
npm run typecheck
npm test -- --run
npm run build
npm run dev
```

Open http://localhost:5173; default API target is http://127.0.0.1:8080.
Set FLOWFORGE_API_TARGET for another compatible **current schema-8 API**. Vite proxies
REST/WS through /api. Compose's dashboard profile is for a fresh compatible DB.
Do not upgrade the retained schema-4 DB; root scripts/phase8-smoke.ps1 allocates
its own compatible demo with two APIs/two Workers/Vite and scoped cleanup.

Initial navigation/reconnect/subscriber recovery and each 30s interval reconcile
visible resources through REST. Hints coalesce at 250ms; there is no high-frequency
polling or authoritative event FSM. Queue depth includes due QUEUED Jobs with
budget even without a matching Worker. JSON is escaped/collapsed with 16 KiB
render limits. Local times retain full timestamps in tooltips. Persistent logs
remain unimplemented; Phase 9 adds API latency metrics and a link to the optional
Grafana overview. Commands may return real 409s.

No authentication: use loopback/local demonstration only. This is a Vite dev
server, not production serving. The Phase 10 static Caddy gateway implements
private loopback serving and optional public HTTPS/Basic Auth; local/CI acceptance
is recorded in the [completion report](../docs/reports/phase-10-completion.md).
Application users/RBAC and real cloud/public ACME validation remain absent. No secrets
are stored in localStorage. Server defaults allow same origin and exact localhost /
127.0.0.1:5173 origins; FLOWFORGE_WS_ORIGINS configures extras.
See [full contract](../docs/dashboard.md).

Tests cover API errors, bounded retry/coalescing/timer cleanup, lost-hint repair,
stale detail responses, Jobs refresh, REST controls and no optimistic authority.
Browser E2E NOT RUN; process smoke uses real WS clients.
