# Architecture

This document describes the internal architecture of the Twitch Room service. It
complements the high-level overview in [`README.md`](../README.md) and the
developer workflow in [`DEVELOPMENT.md`](DEVELOPMENT.md).

## Overview

Twitch Room is a single-process Go HTTP service (`module twitch_room`, Go 1.27).
It exposes a headless JSON API plus a small set of HTML pages (share pages and
extension assets). State is kept in either PostgreSQL or an in-memory store.
There is no embedded web UI; the primary client is the browser extension under
`extension/`.

The process is intentionally simple: one `http.Server`, one service object, one
repository adapter. All long-lived background work (session reaping, cache
cleanup, Twitch metadata refresh) is driven by goroutines started in `main()` and
stopped via a `signal.NotifyContext` context.

## Layering

```
┌──────────────────────────────────────────────────────────────┐
│  HTTP layer (main.go)                                        │
│  routing (newRouter), handlers, middleware chain, graceful    │
│  shutdown, OAuth state minting/verification                   │
├──────────────────────────────────────────────────────────────┤
│  internal/auth        identity resolution (dev + Twitch)     │
│  internal/security    CORS, headers, rate limits, IP helpers  │
│  internal/performance watch-state cache                       │
│  internal/metrics     Prometheus registry                     │
├──────────────────────────────────────────────────────────────┤
│  internal/service     domain logic + business rules           │
│  Service, Repository interface, StreamEnricher/UserResolver   │
├──────────────────────────────────────────────────────────────┤
│  internal/repository  storage implementations                │
│  MemoryStore, PostgresStore, ServiceAdapter                   │
├──────────────────────────────────────────────────────────────┤
│  internal/{config,secrets,validate,twitchapi,identity}        │
│  supporting packages                                         │
└──────────────────────────────────────────────────────────────┘
```

Dependencies point downward only. `service` depends on the `Repository`
interface, not on concrete stores; `main` wires a `ServiceAdapter` around a
`WatchStateStore`.

## Key types

- `server` (main.go) — holds all runtime dependencies: `*service.Service`,
  auth middleware, identity provider, rate limiters, caches, session store,
  metrics registry.
- `service.Service` — domain logic. Holds a `Repository`, an action TTL, an
  optional `StreamEnricher`/`UserResolver`, and a `Clock`.
- `service.Repository` — storage contract used by the service.
- `repository.WatchStateStore` — lower-level storage contract.
- `repository.ServiceAdapter` — bridges `WatchStateStore` to `service.Repository`.
- `repository.MemoryStore` / `repository.PostgresStore` — the two backends.

Changing a store interface requires updating all implementations and both
interfaces together (see AGENTS.md).

## Request flow

1. `metricsMiddleware` (outermost) records count + duration for every request,
   including 4xx/5xx.
2. `apiAuthMiddleware` applies a coarse per-IP limit to `/v1/*` before identity
   resolution.
3. Optional TLS enforcement (`ForceHTTPS`), security headers, body cap
   (`MaxBodyBytes` 1 MiB), CORS.
4. `withLogging` logs method/path/duration.
5. `http.ServeMux` routes to handlers.
6. Handlers call `requireUser` to resolve identity (context → session cookie →
   dev auth middleware → identity provider), then service methods.
7. Responses are JSON via `writeJSON`.

Middleware order matters: metrics is outermost so throttled/oversized requests
are still counted.

## Identity and auth

`auth.Middleware` resolves `X-User-ID` (dev auth) and enforces the static
`TWITCH_ROOM_AUTH_TOKEN` when set. Real Twitch OAuth goes through
`identity.TwitchProvider`: `/auth/twitch` mints a signed, expiring state
(HMAC-SHA256) bound to a browser cookie; `/auth/twitch/callback` exchanges the
code and issues a session cookie. Sessions live in PostgreSQL when available,
otherwise in memory.

`requireUser` tries, in order: user id in request context, session cookie,
dev-auth middleware, then the identity provider (real bearer tokens).

## Storage

- `DATABASE_DSN` set → `PostgresStore` (pgx), sessions also persisted.
- otherwise → `MemoryStore`, in-memory sessions.

The service never talks to SQL directly; it uses `service.Repository` via the
adapter. Watch states expire after `WATCH_STATE_TTL_MINUTES` (default 5); expired
states are deleted lazily on read and by the refresh loop.

## Twitch enrichment

Active only when `TWITCH_CLIENT_ID` + `TWITCH_CLIENT_SECRET` are set. The
`twitchapi.Client` implements both `StreamEnricher` and `UserResolver`. On
publish, stream logins are resolved against Helix; unknown channels are rejected
with 400. If Twitch is unreachable, plain logins are stored. A background ticker
(`WATCH_REFRESH_SECONDS`, default 60) calls `Service.RefreshWatchStates`, which
re-resolves metadata for all active states while preserving `updated_at` (and
therefore expiry). The enricher's own caches are reaped every minute.

Without credentials, plain IDs are stored as-is and no validation against Twitch
occurs.

## Caching

`performance.WatchStateCache` is a per-user TTL cache in front of
`GetUserWatchState`, invalidated on writes and on metadata refresh. Rate
limiters (`security.RateLimiter`) and the OAuth state store are in-memory and
per-process.

## Background goroutines (started in main)

- Session reaper (1 min).
- Watch-state cache reaper (1 min).
- Enricher cache reaper (1 min), when enrichment is on.
- Metadata refresh loop (`WATCH_REFRESH_SECONDS`), when enrichment is on.
- OAuth state reaper (1 min).

All select on the root context and exit on shutdown.

## Observability and deployment

- `metrics.Registry` is a dependency-free Prometheus text-format registry exposed
  at `GET /metrics`. Gauges for active watch states, subscriptions by status,
  and app info are refreshed from the repository on each scrape. Counters and
  histograms for HTTP requests and throttled requests are updated by middleware.
  `/metrics` is gated by `TWITCH_ROOM_AUTH_TOKEN` when set.
- Ready-made observability assets ship under `deploy/`:
  - `deploy/prometheus/` — scrape config (15s, bearer token), alert rules, and a
    Docker image that bakes them in.
  - `deploy/prometheus/twitch_room_alerts.yml` — alerts for down target, missing
    series, >5% 5xx, >1s p95 latency, rate-limit spikes.
  - `deploy/grafana/twitch-room-dashboard.json` — importable Grafana dashboard.
- `deploy_test.go` asserts these assets reference only real metric families and
  that every exposed family appears in the dashboard.

## Testing

- `go test ./...` runs fully offline against in-memory stores.
- Postgres tests in `internal/repository` skip unless `TEST_DATABASE_DSN` (or
  `DATABASE_DSN`) is set.
- `deploy_test.go` validates the Prometheus/Grafana deploy assets.
- E2E tests live in `e2e/` and run with Playwright (Node) against a running
  service; `API_BASE` overrides the base URL.

## Share pages and extension assets

`/u/{user_id}` renders an unauthenticated HTML page only for publishers with
public visibility; all other cases return 404. `/extension/` serves static files
from an embedded filesystem (`sharePageFS`). `/health` reports status and
version.

## Configuration

All configuration comes from environment variables (see README table). `.env` is
optionally loaded by `secrets.LoadDotEnv` (existing env wins). Production
hardening checks (DB, CORS, auth token, OAuth secret, TLS/cookies) are logged at
startup when `APP_ENV=production`.

## Limitations

- Rate limiters, the watch-state cache, and OAuth state store are in-memory and
  per-process (see `security.RateLimiter`, `performance.WatchStateCache`,
  `oauthStateStore`). Run a single instance, or front instances with a shared
  store, for consistent limits.
- OAuth sessions are persisted to PostgreSQL when `DATABASE_DSN` is set; with no
  database, sessions are in-memory and lost on restart.
- Twitch enrichment is optional and depends on `TWITCH_CLIENT_ID` /
  `TWITCH_CLIENT_SECRET`; without them, plain IDs are stored and no channel
  validation occurs.<|end|><|start|><|start|>assistant<|channel|>analysis<|message|>Now update README Architecture section to link to this doc maybe. The user asked "do we have any documents on project architecture? if not can we add architecture documentation?" I should answer and mention created doc. I could add link in README. Let's edit README Architecture section to reference docs/ARCHITECTURE.md.<|end|><|start|>Assistant to=browser.preview<|channel|>analysis <|constrain|>xml<|message|><rfl:parameter name="path">
docs/ARCHITECTURE.md