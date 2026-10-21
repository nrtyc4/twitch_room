# Twitch Room

Twitch Room is a small Go service for publishing which Twitch streams a user is currently watching, and letting people who subscribe to that user see it in a feed. It uses a publish/subscribe model: each user can decide how people subscribe to them (`open` or `approved`) and who can see their watch state (`public`, `subscribers`, or `private`).

## Architecture

The service is intentionally split into a few layers:

- Core domain and business rules in `internal/service`
- In-memory repository for local development
- HTTP layer in `main.go`
- Security boundary enforced by authenticated user identity and ownership checks

## Security model

This is the production-safe approach the service aims to use:

- Twitch OAuth provides user identity.
- The API should never trust a client-provided `user_id` value.
- Users can always read or update their own watch state.
- Other users see a watch state only if the publisher allows it: it is public to everyone, visible to approved subscribers, or private.
- Subscriptions are `open` (anyone can subscribe instantly) or `approved` (subscribe requests must be accepted by the publisher).
- Feed visibility is opt-in and not globally public by default.
- HTTPS in production: the service can serve TLS directly or sit behind a reverse proxy; session cookies are Secure and HttpOnly, and a strict Content-Security-Policy, `nosniff`, frame-denial and HSTS headers are applied.
- Rate limiting: per-user API limits plus per-IP limits on the OAuth endpoints; all request bodies are capped and the HTTP server enforces read/write timeouts.
- CORS: cross-origin requests are only allowed for origins listed in `ALLOWED_ORIGINS` (comma-separated, supports `*` wildcards such as `chrome-extension://*` or `moz-extension://*`). When unset, no cross-origin requests are permitted — except in development (dev auth enabled, non-production), where it defaults to `chrome-extension://*,moz-extension://*` so the browser extension works out of the box.
- `ALLOW_DEV_AUTH` (the `X-User-ID` shortcut) is intended for local development only and refuses to be used with `APP_ENV=production`.

## Production Deployment (Docker Compose)

For deployment, we use `docker-compose` to manage the service and its dependencies (PostgreSQL).

1.  **Setup Environment:**
    Create a `.env` file in the root directory and populate it with your production secrets:
    ```bash
    cp .env.example .env
    # Edit .env with actual credentials for all services
    ```

2.  **Run Services:**
    Start both the database and the application service using Docker Compose:
    ```bash
    docker-compose up --build
    ```

    To label the build in `/metrics` (and on the Grafana dashboard), pass the
    version through:

    ```bash
    APP_VERSION="$(git describe --tags --always --dirty)" docker-compose up --build
    ```

    The stack also starts Prometheus on <http://localhost:9090>, scraping the
    service's `/metrics` (see [Scraping and alerting](#scraping-and-alerting)).

3.  **Secrets Management in Production:**
    *   **Database Credentials (`DATABASE_DSN`):** Use a dedicated secrets manager (e.g., AWS Secrets Manager, HashiCorp Vault) and inject the credentials into the `postgres` service environment variables at runtime, rather than relying solely on `.env`.
    *   **OAuth Secrets:** The `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`, etc., must be injected via environment variables or a dedicated secrets store when running the container.

## Configuration (Docker Compose)

| Variable | Purpose | Default in .env | Required | Notes |
|----------|---------|-----------------|----------|-------|
| `PORT` | Service port | `8080` | No | Container exposed port. |
| `ALLOW_DEV_AUTH` | Enable `X-User-ID` auth for development | `true` | No | Refuses to start if `APP_ENV=production`. |
| `WATCH_STATE_TTL_MINUTES` | Lifetime of a published watch state | `5` | No | |
| `WATCH_REFRESH_SECONDS` | How often Twitch metadata is re-resolved | `60` | No | Only active when Twitch enrichment is enabled. |
| `DATABASE_DSN` | PostgreSQL connection string | (Must be set) | Yes | Format: `postgres://user:pass@host:port/dbname` |
| `TWITCH_CLIENT_ID` | Twitch OAuth client ID | (Must be set) | For real OAuth | |
| `TWITCH_CLIENT_SECRET` | Twitch OAuth client secret | (Must be set) | For real OAuth | |
| `TWITCH_REDIRECT_URI` | Twitch OAuth redirect URI | (Must be set) | For real OAuth | |
| `APP_REDIRECT_AFTER_LOGIN` | Post-login URL in the app | `/` | No | Browser is redirected here with `?user_id=...` |
| `TWITCH_ROOM_AUTH_TOKEN` | Static token for Bearer authentication | (empty) | No | For `Authorization: Bearer <token>`. |
| `TLS_ENABLED` | Serve HTTPS directly | `false` | For HTTPS | Enables HSTS and HTTP→HTTPS redirects. |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | PEM certificate and key paths | (empty) | With `TLS_ENABLED` | |
| `TLS_REDIRECT_PORT` | Plain-HTTP port that 301s to HTTPS | (empty) | No | e.g. `80` → HTTPS on `PORT`. |
| `COOKIE_SECURE` | Force `Secure` on session cookies | `false` | Behind a TLS proxy | |
| `TRUSTED_PROXY_CIDRS` | Comma-separated proxy networks | (empty) | Behind a proxy | |
| `ALLOWED_HOSTS` | Comma-separated list of Host values | (empty) | No | |
| `ALLOWED_ORIGINS` | Comma-separated list of CORS origins (supports `*` wildcards, e.g. `chrome-extension://*,moz-extension://*`) | (empty) | No | Empty disables CORS; in dev (dev auth, non-production) it defaults to the extension origins. |
| `APP_ENV` | `production` or empty | (empty) | No | |
| `LOG_LEVEL` | Logging verbosity | `info` | No | |
| `APP_VERSION` | Build version reported by `/metrics` | `dev` | No | Interpolated by Docker Compose into the image build (`-ldflags "-X main.version=..."`). |

## Authentication

Requests to the watch/feed endpoints must be authenticated. The service accepts, in order of precedence:

1. A valid session cookie set after a real Twitch login (`/auth/twitch` → `/auth/twitch/callback`). The callback issues an HttpOnly, SameSite=Lax session cookie (Secure when served over HTTPS or when `COOKIE_SECURE=true`); the browser then authenticates automatically. `GET /auth/me` reports the session user, and `POST /auth/logout` destroys the session.
2. An `X-User-ID` header with the target user's ID (dev auth). When `TWITCH_ROOM_AUTH_TOKEN` is set, the `X-User-ID` header must also carry a matching `Authorization: Bearer <token>`.
3. A valid Twitch access token in the `Authorization: Bearer <token>` header, validated against Twitch via the identity provider.

The `/auth/twitch`, `/auth/twitch/callback` and `/auth/logout` endpoints are additionally rate-limited per client IP. All request bodies are capped at 1&nbsp;MB (larger ones get `413`), and the HTTP server sets read/write header timeouts so slow clients cannot hold connections open.

Users can always read or update their own watch state. Everyone reads someone else's watch state only when the publisher's visibility setting allows it and (for `subscribers` visibility) the reader has an active subscription. Subscription mode and visibility are enforced server-side regardless of the headers sent.

## Subscriptions and visibility

Each publisher controls two settings via `PUT /v1/settings`:

| Setting | Values | Meaning |
|---|---|---|
| `subscription_mode` | `open` \| `approved` | `open` subscribes instantly; `approved` creates a pending request the publisher must accept. |
| `visibility` | `public` \| `subscribers` \| `private` | `public` is visible to any authenticated user, `subscribers` only to accepted subscribers, `private` to no one. |

Defaults are `open` + `subscribers`. The `/v1/feed` endpoint returns watch state only from publishers the viewer has an **active** subscription to.

Changes to the settings are rate-limited per user (10 writes per minute, counted as `settings_per_user` in the metrics); reads are never throttled, so the UI can still show the current settings. Exceeding the limit returns `429`.

## Twitch OAuth Setup

To use real Twitch authentication (instead of dev auth), create an application in the [Twitch Developer Console](https://dev.twitch.tv/console/apps):

1.  Create a new application and copy its **Client ID** and **Client Secret**.
2.  Under **OAuth Redirect URLs**, register exactly the value you set for `TWITCH_REDIRECT_URI`. For local development this is `http://localhost:8080/auth/twitch/callback`. Twitch requires HTTPS in production (localhost is the exception).
3.  Populate `.env` with the real values (see `.env.example`):
    ```
    TWITCH_CLIENT_ID=<your client id>
    TWITCH_CLIENT_SECRET=<your client secret>
    TWITCH_REDIRECT_URI=http://localhost:8080/auth/twitch/callback
    APP_REDIRECT_AFTER_LOGIN=/   # where the browser goes after login
    ```
4.  Set `ALLOW_DEV_AUTH=false` for production. (If `APP_ENV=production` is set, the service refuses to start while `ALLOW_DEV_AUTH=true`.)

The login flow is:

- `GET /auth/twitch` redirects the browser to Twitch's authorization page.
- After the user authorizes, Twitch redirects back to `/auth/twitch/callback?code=...&state=...`. The service exchanges the code for an access token, resolves the Twitch user, and redirects the browser to `APP_REDIRECT_AFTER_LOGIN?user_id=<login>` (clients such as the browser extension read this and remember it).
- The client then authenticates subsequent API calls with that `user_id` (see Authentication below).

The `state` parameter is stored server-side at the start of the flow and verified on the callback to prevent CSRF.

## Clients

The service is a headless API: it no longer ships an embedded web UI. The primary client is the browser extension under `extension/`, which talks to the API directly. Any other client (script, mobile app, etc.) can use the same HTTP API described below.

The extension authenticates with Twitch via `chrome.identity` (implicit grant). Register the redirect URI `https://<extension-id>.chromiumapp.org/` in the Twitch developer console, and provide the app's client id through the extension settings (or the `TWITCH_CLIENT_ID` constant in `extension/background.js`). Tokens are kept in `chrome.storage.local`.

The API authenticates with the `X-User-ID` header (dev auth) or automatically via the session cookie issued after a real Twitch login. In real deployments the session cookie path should be preferred over trusting client-supplied user ids.

## API

- `Publish` request body: `{"stream_ids": ["shroud", "https://twitch.tv/xqc"]}` — accepts channel names or full `twitch.tv` URLs (deduplicated). Response includes a `streams` array.
- `Twitch enrichment:` when `TWITCH_CLIENT_ID` and `TWITCH_CLIENT_SECRET` are set (they are in `.env` but intentionally absent from the Docker build), published stream IDs are resolved against the Twitch Helix API using the app client-credentials flow. Each stream gains `channel_name`, `title`, `game_name`, `thumbnail_url`, `viewer_count`, and `is_live`; IDs that do not match an existing channel are rejected with `400`. If Twitch is unreachable at publish time, the service degrades to storing plain IDs. With no credentials, plain IDs are stored as-is. While enrichment is enabled a background job re-resolves the metadata for every active state every `WATCH_REFRESH_SECONDS` (channels going live or offline update in the feed without a re-publish; the state's own `updated_at` — and therefore its expiry — is preserved).
- `GET /health`
- `GET /metrics` — Prometheus text-format metrics for scrapers (see [Metrics](#metrics))
- `POST /v1/watch` — publish the streams you are currently watching
- `DELETE /v1/watch` — stop watching (clears your published watch state)
- `GET /v1/users/{user_id}/watch` — read a user's watch state (self, public publishers, or active subscribers)
- `GET /v1/feed` — feed of active subscriptions
- `GET|PUT|POST /v1/settings` — read or update `subscription_mode` and `visibility`
- `GET /v1/subscriptions` — list the current user's subscriptions
- `POST /v1/subscriptions/{publisher_id}` — subscribe (201 if open, 202 if pending approval)
- `DELETE /v1/subscriptions/{publisher_id}` — unsubscribe
- `GET /v1/subscription_requests` — pending subscribe requests (publisher view)
- `POST /v1/subscription_requests/{user_id}/approve` — accept a pending request
- `POST /v1/subscription_requests/{user_id}/reject` — reject a pending request
- `GET /auth/twitch`
- `GET /auth/twitch/callback`
- `GET /auth/me` — report the current session user
- `POST /auth/logout` — destroy the session (does not delete account data)
- `DELETE /v1/account` — purge all data for the authenticated user (watch state, settings, subscriptions, sessions)

### Input strictness

All user, publisher, subscriber and stream identifiers are validated at the API boundary (and again in the service layer): they must be 1-100 characters of letters, digits, `_` or `-`. Twitch channel URLs are still accepted in `stream_ids` because they are normalised to the channel login first; anything else is rejected with `400` before it reaches storage or the Twitch API. A single watch state is capped at `50` streams. An `X-User-ID` (or `X-User-ID` obtained via a real Twitch login) that violates the rules is rejected with `401`.

## Public share pages

Every publisher gets a read-only, unauthenticated page at `/u/{user_id}` — no login needed. It renders the publisher's current watch state (channel names, live status, viewer count, title and game when enrichment is enabled) with a thumbnail per stream, and auto-refreshes every two minutes. Only publishers whose watch visibility is set to **Public** get a page; subscriber-only, private, missing and expired states all return `404` so the page does not reveal whether a state exists. The share page is scaffolded per-IP to keep public scraping at bay, and a link to it appears in the app's Settings when a publisher switches visibility to Public.

## Metrics

`GET /metrics` serves a dependency-free Prometheus text-format exposition (`text/plain; version=0.0.4`) for scrapers. When a static `TWITCH_ROOM_AUTH_TOKEN` is configured, scrapers must present it as `Authorization: Bearer <token>` (401 otherwise); with no token configured the endpoint is public, like `/health`. Families exposed:

- `twitch_room_http_requests_total{method,code,route}` and `twitch_room_http_request_duration_seconds{route}` (histogram) — request counts, status codes and durations. Route labels are collapsed to the first path segment (`v1/watch`, `v1/users`, …) so the label space stays small.
- `twitch_room_throttled_requests_total{reason}` — requests rejected by a rate limiter (`auth_ip`, `watch_per_user`, `feed_per_user`, `share_ip`, `settings_per_user`).
- `twitch_room_active_watch_states` — publishers whose watch state has not expired.
- `twitch_room_subscriptions{status}` — subscriptions tallied per status (`active`, `pending`).
- `twitch_room_app_info{version}` — build info gauge, where `version` comes from the build (`APP_VERSION`, see [Configuration](#configuration-docker-compose)) and defaults to `dev`.

### Scraping and alerting

Ready-made configuration ships in `deploy/`:

- `deploy/prometheus/` — the scrape config (15s, bearer token), the alert rules, and a two-file image that bakes them in and injects `TWITCH_ROOM_AUTH_TOKEN` at startup.
- `deploy/prometheus/twitch_room_alerts.yml` — alerts for a down target, missing series, >5% 5xx, >1s p95 latency and rate-limit spikes.
- `deploy/grafana/twitch-room-dashboard.json` — an importable Grafana dashboard (request rate and status codes by route, latency percentiles, throttling by reason, subscriptions by status, active watch states and the build version).

`docker-compose.yml` builds that image and runs Prometheus on <http://localhost:9090> alongside the service. The scrape credential comes from the same `TWITCH_ROOM_AUTH_TOKEN` the service reads, so dev and production cannot drift apart; when the variable is empty (public `/metrics`) the generated config simply omits the credential. Because the config lives in the image rather than a bind mount, the stack also works where Docker Desktop does not share the host filesystem — edit `deploy/prometheus/prometheus.yml` and re-run `docker compose up --build` to apply changes.

Set `APP_VERSION` so `twitch_room_app_info` identifies the deployed build (the dashboard shows it).

`go test ./...` includes a check that the scrape config, alert rules and dashboard only reference metric families the service actually exposes, that every exposed family appears in the dashboard, and that the credential block the render script strips still matches `prometheus.yml`.

## Twitch Terms of Service compliance

The browser extension no longer scrapes or modifies twitch.tv pages. Watch state is
entered manually by the user, authentication uses Twitch OAuth, and the extension
ships with a privacy policy and consent gate. Server-side data is purged on logout.
See `TWITCH_COMPLIANCE_AUDIT.md` and `extension/privacy.html`.

## Notes

The service uses PostgreSQL (`DATABASE_DSN`) when configured and falls back to an in-memory repository otherwise. OAuth session tokens are stored in the database too, so sessions survive a restart and work across multiple instances (memory sessions are used when no database is configured). When Twitch credentials are configured, publisher display names in the feed are resolved from the Twitch users API (cached briefly) and fall back to the raw user ID.

## Go best practices & memory efficiency

The codebase follows a few conventions to stay memory-efficient and idiomatic Go:

- **Formatting & tooling:** all Go sources are `gofmt`-clean. CI runs `gofmt -l`, `go vet`, `go test -race`, and the build.
- **Bounded in-memory state:** rate limiters, the OAuth state store, the Twitch user cache, the watch-state cache and the in-memory session store all evict expired entries. Reaping runs lazily on access and on a periodic (one-minute) background ticker so idle keys cannot accumulate indefinitely.
- **Concurrency safety:** session expiry is handled under a single write lock (no check-then-act race); rate-limiter slicing reuses backing arrays only within the locked section.
- **Resource hygiene:** HTTP response bodies are capped with `io.LimitReader` (identity provider and Twitch client); Postgres `Exec`/`Rows.Err()` errors are logged instead of silently dropped; the database connection is closed on graceful shutdown.
- **Context propagation:** request contexts flow into the Twitch enrichment calls (`SetUserWatchState`, `ListFeed`, `ResolveDisplayName`) instead of `context.Background()`.
- **Idioms:** `MaxIDLength` is a `const`; `any` is used instead of `interface{}`; startup logging lives in `main()` (no `init()` side effects).

## Running the tests

`go test ./...` runs fully offline against in-memory stores. The repository package also ships Postgres-backed tests that exercise the real storage layer; they skip automatically unless a database is available. To run them, point `TEST_DATABASE_DSN` (falls back to `DATABASE_DSN`) at a database you don't mind getting rows written to the tables for (every test cleans up after itself):

```bash
TEST_DATABASE_DSN="postgres://twitch_room:twitch_room@localhost:5432/twitch_room?sslmode=disable" \
  go test ./internal/repository/ -run Postgres -v
```
