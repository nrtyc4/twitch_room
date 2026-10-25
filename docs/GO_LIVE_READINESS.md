# Go-Live Readiness Plan — Twitch Room

Target: first production release of the backend service + browser extension.

## Status legend
- [x] Done
- [ ] Pending

## 1. Backend (service)

### Code / security
- [x] Dev auth refuses to start under `APP_ENV=production`
- [x] Input validation (`internal/validate`) at API + service boundary
- [x] Rate limiting (per-user + per-IP, settings limiter)
- [x] Security headers, CSP, HSTS (TLS), CORS
- [x] Request body cap, read/write timeouts, graceful shutdown
- [x] Open-redirect guard on post-login redirect
- [x] Production env misconfiguration warnings at startup
- [x] Identity dev-auth IDs validated; identity cache bounded
- [x] Add `OAUTH_STATE_SECRET` rotation guidance (docs/SECURITY.md)
- [x] Confirm DB connection retry / startup ordering in compose

### Data / storage
- [x] Postgres store with schema init + migrations
- [x] In-memory fallback for local dev
- [x] Delete-user purge (watch state, settings, subscriptions, sessions)
- [x] Backup/restore procedure documented (docs/BACKUP_RESTORE.md); staging restore test pending
- [x] Retention policy for expired watch states (reaper present in RefreshWatchStates)

### Observability
- [x] `/health`, `/metrics` (Prometheus text format)
- [x] Token-gated metrics endpoint
- [x] Prometheus scrape config + alert rules + Grafana dashboard
- [x] Deploy artifact test (`deploy_test.go`)
- [ ] Alert routing (PagerDuty/Slack) wired in production
- [x] Log aggregation: optional JSON handler via LOG_FORMAT=json (default text)

### Build / deploy
- [x] Multi-stage Dockerfile (non-root, healthcheck)
- [x] `.dockerignore`
- [x] Restart policies in compose
- [x] Production compose override
- [x] Version injection via ldflags (`APP_VERSION`)
- [ ] Tag a release (`v1.0.0`) and build with `git describe`
- [ ] Push image to registry (GHCR/Docker Hub) — not yet automated
- [x] CI: Docker build step added; publish workflow on tags (GHCR)

## 2. Browser extension

### Code
- [x] No content scripts / no scraping (ToS compliance)
- [x] Consent gate + privacy policy link
- [x] Delete-my-data calls `DELETE /v1/account`
- [x] Auto-discover Twitch client id from backend
- [x] Host permissions requested at runtime for non-localhost
- [x] Store-ready icons (16/32/48/128)
- [x] Migrate implicit grant → authorization code + PKCE (extension)
- [ ] Remove unused `optional_host_permissions` breadth if possible

### Store submission
- [ ] Host privacy policy at a public URL (content exists in `extension/privacy.html`)
- [ ] Store listing copy + screenshots
- [ ] Justify permissions (`storage`, `identity`, `activeTab`, host)
- [ ] Submit to Chrome Web Store + Firefox Add-ons
- [ ] Decide default API base for production (currently localhost)

## 3. Documentation
- [x] README (architecture, config, API, metrics, deploy)
- [x] SECURITY.md, DEVELOPMENT.md
- [x] TWITCH_COMPLIANCE_AUDIT.md (stale in places — review)
- [x] AGENTS.md
- [x] CHANGELOG.md
- [ ] GO_LIVE_READINESS.md (this file) kept current
- [ ] Release notes for v1.0.0

## 4. Testing
- [x] Unit tests all packages
- [x] Race-free (CI runs `-race` on Linux)
- [x] Smoke scripts (bash + PowerShell)
- [x] E2E (Playwright, health + metrics)
- [x] E2E authenticated flow spec added (e2e/authenticated.spec.js)
- [ ] Load test (rate limits, refresh loop)
- [x] Postgres-backed tests run in CI (service container added)

## 5. Pre-launch verification
- [ ] Deploy to staging with prod override
- [ ] Run smoke scripts against staging
- [ ] Verify OAuth end-to-end with a real Twitch account
- [ ] Verify extension install → login → publish → feed → share page
- [ ] Verify `/metrics` scraping + dashboard panels
- [ ] Verify alert rules fire on synthetic failure
- [ ] Verify graceful shutdown (SIGTERM) drains connections
- [ ] Verify data deletion end-to-end

## 6. Launch
- [ ] Tag release and publish image
- [ ] Publish extension (staged rollout if supported)
- [ ] Announce / update README links
- [ ] Monitor alerts + logs for 24h

## Recommended next actions (prioritized)
1. [x] Add Postgres service container to CI and run `-race` + Postgres tests.
2. [x] Add a Docker build step to CI (push to registry still manual).
3. Host privacy policy at a public URL and finalize store listing (see `docs/STORE_LISTING.md`).
4. Register both redirect URIs in the Twitch app.
5. Tag v1.0.0 and perform staging deploy + full verification.
3. Host privacy policy and prepare store listing.
4. Register both redirect URIs in the Twitch app.
5. Tag v1.0.0 and perform staging deploy + full verification.