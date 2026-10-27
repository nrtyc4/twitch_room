# Specification: Twitch Room Service

- Status: Approved (living document)
- Owner: natth
- Date: 2026-10-27

## Intent

Provide a headless HTTP API that lets a user publish which Twitch streams they are currently watching, and lets subscribers see those streams in a feed, with publisher-controlled subscription and visibility settings.

## Scope

### In scope

- Watch-state publish/read/delete for authenticated users.
- Subscription lifecycle (subscribe, unsubscribe, approve/reject) with open/approved modes.
- Publisher settings (subscription mode, visibility).
- Twitch OAuth login/session management.
- Twitch enrichment of published streams (metadata, live status).
- Public, unauthenticated share pages for public publishers.
- Prometheus metrics endpoint and deploy assets.
- Browser extension client (no build step).

### Out of scope

- Embedded web UI in the service.
- Multi-instance shared rate limiting/cache (per-process today).
- Account billing or analytics beyond metrics.

## Requirements

1. Authenticated users can publish a watch state with 1-50 stream IDs.
2. Stream IDs may be channel names or twitch.tv URLs, normalized and validated.
3. Watch state expires after configurable TTL.
4. Readers may view a publisher's state only per visibility rules (public/subscribers/private) and subscription status.
5. Feed returns active watch states from publishers the viewer actively subscribes to.
6. Publishers control subscription mode (open/approved) and visibility (public/subscribers/private).
7. Twitch enrichment resolves channel metadata when credentials are configured; unknown channels rejected with 400.
8. OAuth sessions persist in PostgreSQL when configured, memory otherwise.
9. `/metrics` exposes Prometheus families and is gated by bearer token when configured.
10. Public share pages exist only for public publishers; all other cases return 404.
11. Input identifiers validated (1-100 chars `[A-Za-z0-9_-]`).
12. Optional `ALLOWED_USERS` allowlist restricts which Twitch logins may use the service; empty allows everyone.

## Design

- Layered: HTTP in `main.go`; domain logic in `internal/service`; storage in `internal/repository` (`MemoryStore`, `PostgresStore`) plus `ServiceAdapter`.
- Auth via Twitch OAuth or dev auth (`X-User-ID`); sessions in DB/memory.
- Middleware: logging, body limit, security headers, HTTPS redirect, metrics (outermost).
- Metrics registry in `internal/metrics`; deploy assets in `deploy/`.

## Acceptance criteria

- [x] `gofmt -l .` produces no output for new/edited Go files.
- [x] `go vet ./...` passes.
- [x] `go test ./...` passes offline.
- [x] `go test -race ./...` passes in CI.
- [x] Versioned build succeeds.
- [x] Postgres-backed repository tests pass when DB available.
- [x] Deploy assets reference only real metric families (deploy_test.go).

## Implementation plan

- [x] Core service and repository layers
- [x] HTTP API and auth
- [x] Twitch enrichment and refresh job
- [x] Subscriptions and visibility
- [x] Public share pages
- [x] Input validation hardening
- [x] Graceful shutdown and trusted proxy handling
- [x] Prometheus metrics endpoint
- [ ] Prometheus scrape/alert/dashboard wired to live service (optional)
- [ ] Version label from ldflags in /metrics (optional)
- [x] Optional ALLOWED_USERS allowlist (committed)

## Open questions

- None tracked. Future enhancements listed in `.SUMMARY` Next Move.

## Verification

- `gofmt -l .`
- `go vet ./...`
- `go test ./...`
- `go test -race ./...`
- `go build -ldflags "-X main.version=<version>" -o twitch-room .`