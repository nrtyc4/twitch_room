# AGENTS.md — Twitch Room

Go service (`module twitch_room`, Go 1.27) exposing a headless HTTP API for publishing Twitch watch state + subscriptions. Primary client is the browser extension in `extension/` (no build step; load unpacked).

## Commands

- Format check (must print nothing): `gofmt -l .`
- Vet: `go vet ./...`
- Tests (offline, in-memory stores): `go test ./...`
- Tests with race detector (CI): `go test -race ./...`
- Build: `go build -ldflags "-X main.version=<version>" -o twitch-room .`
- Single package: `go test ./internal/<pkg>/...`
- Single test: `go test ./internal/<pkg>/ -run <TestName> -v`
- Postgres-backed repository tests (skip without DB): set `TEST_DATABASE_DSN` (falls back to `DATABASE_DSN`), then `go test ./internal/repository/ -run Postgres -v`
- E2E (Playwright, needs a running service): `cd e2e && npm test` (`API_BASE` env overrides base URL, default `http://localhost:8080`)
- Run locally: `go run .` with env vars exported (`.env` is NOT auto-loaded; see `.env.example`). Dev auth needs `ALLOW_DEV_AUTH=true`.

CI (`.github/workflows/ci.yml`) runs `gofmt -l`, `go vet ./...`, `go test -race ./...`, then a versioned build.

## Architecture

- `main.go` — HTTP layer, routing, middleware, handlers, graceful shutdown.
- `internal/service` — domain logic + business rules; `MemoryRepository` for dev.
- `internal/repository` — storage (`MemoryStore`, `PostgresStore`) plus `ServiceAdapter` bridging to `service.Repository`.
- `internal/{auth,security,config,metrics,validate,twitchapi,identity,secrets,performance}` — supporting packages.
- `deploy/` — Prometheus scrape config, alert rules, Grafana dashboard; `deploy_test.go` asserts these reference only real metric families, so keep them in sync when adding metrics.

Storage: PostgreSQL when `DATABASE_DSN` is set, otherwise in-memory. OAuth sessions are stored in the DB too (memory sessions when no DB).

## Conventions / gotchas

- All Go sources must be `gofmt`-clean; CI fails otherwise.
- `.env` is auto-loaded by `secrets.LoadDotEnv(".env")` if present (existing env wins); production should set env vars directly.
- `ALLOW_DEV_AUTH=true` with `APP_ENV=production` refuses to start.
- CORS defaults to `chrome-extension://*,moz-extension://*` only in dev (dev auth on, non-production); production must set `ALLOWED_ORIGINS`.
- Twitch enrichment is active only when `TWITCH_CLIENT_ID` + `TWITCH_CLIENT_SECRET` are set; then unknown channel IDs are rejected with 400, and a background job re-resolves metadata every `WATCH_REFRESH_SECONDS`. Without creds, plain IDs are stored as-is.
- Request contexts flow into service/repo/Twitch calls; use the `...Context` method variants.
- Input validation lives in `internal/validate` (1–100 chars `[A-Za-z0-9_-]`, max 50 streams per state); stream IDs may be channel names or `twitch.tv` URLs (normalized first).
- Rate limiters, watch-state cache, OAuth state store are per-process — run a single instance (or front with shared store).
- Changing a store interface requires updating all four implementations + both interfaces together (`service.Repository`, `repository.WatchStateStore`, `MemoryRepository`, `ServiceAdapter`, `MemoryStore`, `PostgresStore`).
- `/metrics` is gated by `TWITCH_ROOM_AUTH_TOKEN` when set (Bearer); renders one scrape behind (current request appears next scrape). Middleware order keeps metrics outermost so 400/413/etc. are counted.
- Share pages `/u/{user_id}` exist only for publishers with visibility `public`; all other cases return 404.
- Postgres tests write rows but clean up; point at a throwaway DB.
- Extension has no build step; edit files directly.

## Agent tool-use protocols

See `.agent-rules.md` (referenced by `.opencode/opencode.json` `instructions`).