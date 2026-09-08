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

3.  **Secrets Management in Production:**
    *   **Database Credentials (`DATABASE_DSN`):** Use a dedicated secrets manager (e.g., AWS Secrets Manager, HashiCorp Vault) and inject the credentials into the `postgres` service environment variables at runtime, rather than relying solely on `.env`.
    *   **OAuth Secrets:** The `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET`, etc., must be injected via environment variables or a dedicated secrets store when running the container.

## Configuration (Docker Compose)

| Variable | Purpose | Default in .env | Required | Notes |
|----------|---------|-----------------|----------|-------|
| `PORT` | Service port | `8080` | No | Container exposed port. |
| `DATABASE_DSN` | PostgreSQL connection string | (Must be set) | Yes | Format: `postgres://user:pass@host:port/dbname` |
| `TWITCH_CLIENT_ID` | Twitch OAuth client ID | (Must be set) | For real OAuth | Must match the service's expected environment variable. |
| `TWITCH_CLIENT_SECRET` | Twitch OAuth client secret | (Must be set) | For real OAuth | Must match the service's expected environment variable. |
| `TWITCH_REDIRECT_URI` | Twitch OAuth redirect URI | (Must be set) | For real OAuth | Must match the service's expected environment variable. |
| `APP_REDIRECT_AFTER_LOGIN` | Post-login URL in the app | `/` | No | Browser is redirected here with `?user_id=...` after OAuth. |
| `WATCH_REFRESH_SECONDS` | How often Twitch metadata is re-resolved for published states | `60` | No | Only active when Twitch enrichment is enabled. Refreshing metadata does not extend a state's lifetime. |
| `TLS_ENABLED` | Serve HTTPS directly using `TLS_CERT_FILE`/`TLS_KEY_FILE` | `false` | For HTTPS | Enables HSTS and HTTP→HTTPS redirects on the TLS listener. |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | PEM certificate and key paths | (empty) | With `TLS_ENABLED` | Loaded by the HTTPS listener. |
| `TLS_REDIRECT_PORT` | Optional plain-HTTP port that 301s to HTTPS | (empty) | No | e.g. `80` → HTTPS on `PORT`. |
| `COOKIE_SECURE` | Force `Secure` on session cookies | `false` | Behind a TLS proxy | Set when TLS terminates at a reverse proxy so cookies are still Secure. |
| `TRUSTED_PROXY_CIDRS` | Comma-separated proxy networks (`10.0.0.0/8`) whose `X-Forwarded-For`/`X-Forwarded-Proto` headers are honoured | (empty) | Behind a proxy | Empty trusts nobody; forwarded headers are ignored unless the connection source matches. |
| `ALLOWED_HOSTS` | Comma-separated list of Host values redirected to by the HTTPS redirector | (empty) | No | Requests for other hosts get `400` (prevents host-header injection / open redirects). |
| `APP_ENV` | `production` or empty | (empty) | No | Refuses to start if combined with `ALLOW_DEV_AUTH=true`. |

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
- After the user authorizes, Twitch redirects back to `/auth/twitch/callback?code=...&state=...`. The service exchanges the code for an access token, resolves the Twitch user, and redirects the browser to `APP_REDIRECT_AFTER_LOGIN?user_id=<login>` (the web UI reads this and remembers it).
- The client then authenticates subsequent API calls with that `user_id` (see Authentication below).

The `state` parameter is stored server-side at the start of the flow and verified on the callback to prevent CSRF.

## Web UI

The service ships a small embedded single-page app (no build step, no external CDNs). Open the service root in a browser (`http://localhost:8080/` when running locally or via Docker):

- **Identity:** the bar at the top accepts a user id directly (dev auth) or you can start the real flow with **Login with Twitch**, which redirects back to `/` with the resolved `user_id` in the query string.
- **My watch:** publish or clear the streams you are currently viewing (raw channel names or full `twitch.tv` URLs). Streams are resolved against the Twitch API when credentials are configured, so the feed shows live titles, games, thumbnails, and viewer counts.
- **Feed:** shows watch state from publishers you follow with an active subscription, auto-refreshing. Each published stream renders as a card with its thumbnail, channel name, title, game, and a live badge.
- **People:** follow/unfollow other users by id.
- **Requests:** approve or reject pending subscribe requests when you run `approved` mode.
- **Settings:** choose `open`/`approved` subscription mode and `public`/`subscribers`/`private` visibility.

The UI authenticates with the `X-User-ID` header (dev auth) or automatically via the session cookie issued after a real Twitch login. In real deployments the session cookie path should be preferred over trusting client-supplied user ids.

## API

- `Publish` request body: `{"stream_ids": ["shroud", "https://twitch.tv/xqc"]}` — accepts channel names or full `twitch.tv` URLs (deduplicated). Response includes a `streams` array.
- `Twitch enrichment:` when `TWITCH_CLIENT_ID` and `TWITCH_CLIENT_SECRET` are set (they are in `.env` but intentionally absent from the Docker build), published stream IDs are resolved against the Twitch Helix API using the app client-credentials flow. Each stream gains `channel_name`, `title`, `game_name`, `thumbnail_url`, `viewer_count`, and `is_live`; IDs that do not match an existing channel are rejected with `400`. If Twitch is unreachable at publish time, the service degrades to storing plain IDs. With no credentials, plain IDs are stored as-is. While enrichment is enabled a background job re-resolves the metadata for every active state every `WATCH_REFRESH_SECONDS` (channels going live or offline update in the feed without a re-publish; the state's own `updated_at` — and therefore its expiry — is preserved).
- `GET /health`
- `POST /v1/watch` — publish the streams you are currently watching
- `DELETE /v1/watch` — stop watching (clears your published watch state)
- `GET /v1/users/{user_id}/watch` — read a user's watch state (self, public publishers, or active subscribers)
- `GET /v1/feed` — feed of active subscriptions
- `GET|PUT /v1/settings` — read or update `subscription_mode` and `visibility`
- `GET /v1/subscriptions` — list the current user's subscriptions
- `POST /v1/subscriptions/{publisher_id}` — subscribe (201 if open, 202 if pending approval)
- `DELETE /v1/subscriptions/{publisher_id}` — unsubscribe
- `GET /v1/subscription_requests` — pending subscribe requests (publisher view)
- `POST /v1/subscription_requests/{user_id}/approve` — accept a pending request
- `POST /v1/subscription_requests/{user_id}/reject` — reject a pending request
- `GET /auth/twitch`
- `GET /auth/twitch/callback`

## Notes

The service uses PostgreSQL (`DATABASE_DSN`) when configured and falls back to an in-memory repository otherwise. OAuth session tokens are stored in the database too, so sessions survive a restart and work across multiple instances (memory sessions are used when no database is configured). When Twitch credentials are configured, publisher display names in the feed are resolved from the Twitch users API (cached briefly) and fall back to the raw user ID.

## Running the tests

`go test ./...` runs fully offline against in-memory stores. The repository package also ships Postgres-backed tests that exercise the real storage layer; they skip automatically unless a database is available. To run them, point `TEST_DATABASE_DSN` (falls back to `DATABASE_DSN`) at a database you don't mind getting rows written to the tables for (every test cleans up after itself):

```bash
TEST_DATABASE_DSN="postgres://twitch_room:twitch_room@localhost:5432/twitch_room?sslmode=disable" \
  go test ./internal/repository/ -run Postgres -v
```
