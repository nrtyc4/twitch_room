# Twitch Room

Twitch Room is a small Go service for sharing which Twitch streams a user is currently watching with other users.

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
- Users can only read or update their own watch state.
- Feed visibility is opt-in and not globally public by default.
- HTTPS is required in production.
- Rate limiting, secret management, and expiry windows should be added before public deployment.

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

## Authentication

Requests to the watch/feed endpoints must be authenticated. The service accepts either of the following:

- An `X-User-ID` header with the target user's ID (dev auth, or the `user_id` from the OAuth callback). When `TWITCH_ROOM_AUTH_TOKEN` is set, the `X-User-ID` header must also carry a matching `Authorization: Bearer <token>`.
- A valid Twitch access token in the `Authorization: Bearer <token>` header, which is validated against Twitch before being trusted.

Users can only read or update their own watch state; ownership is enforced server-side regardless of the headers sent.

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
4.  Set `ALLOW_DEV_AUTH=false` for production.

The login flow is:

- `GET /auth/twitch` redirects the browser to Twitch's authorization page.
- After the user authorizes, Twitch redirects back to `/auth/twitch/callback?code=...&state=...`. The service exchanges the code for an access token, resolves the Twitch user, and redirects the browser to `APP_REDIRECT_AFTER_LOGIN?user_id=<login>`.
- The client then authenticates subsequent API calls with that `user_id` (see Authentication below).

The `state` parameter is stored server-side at the start of the flow and verified on the callback to prevent CSRF.

## API

- `GET /health`
- `POST /v1/watch`
- `GET /v1/users/{user_id}/watch`
- `GET /v1/feed`
- `GET /auth/twitch`
- `GET /auth/twitch/callback`

## Notes

This first version uses an in-memory repository. It is intentionally simple so the core watch-sharing behavior is easy to test before adding persistence or real OAuth integration.
