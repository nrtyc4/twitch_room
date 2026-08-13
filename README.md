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

## Local development

### Setup with environment file

1. Copy `.env.example` to `.env`:
```bash
cp .env.example .env
```

2. Edit `.env` and fill in your configuration:
```bash
# For Twitch OAuth (recommended)
TWITCH_CLIENT_ID=your_client_id
TWITCH_CLIENT_SECRET=your_client_secret
TWITCH_REDIRECT_URI=http://localhost:8080/callback

# For database (optional, defaults to in-memory)
DATABASE_DSN=postgres://user:password@localhost:5432/twitch_room

# For development
ALLOW_DEV_AUTH=true
```

3. Run the service:
```bash
go run .
```

The `.env` file is automatically excluded from git, so your secrets will not be committed.

### Secrets Management

Sensitive configuration is managed through:

1. **Environment Variables** — Primary source of secrets (production-safe)
2. **.env Files** — Local development convenience (excluded from git)
3. **Validation** — Service requires valid secrets before startup

Secrets are never logged or exposed in error messages.

**Production Deployment:**
- Use environment variables or secure vaults (HashiCorp Vault, AWS Secrets Manager, etc.)
- Never commit `.env` or other secret files
- Rotate credentials regularly

### Run the service

```bash
go run .
```

Then send requests like:

```bash
curl -H "X-User-ID: user-1" \
  -H "Content-Type: application/json" \
  -d '{"stream_ids":["stream-1","stream-2"]}' \
  http://localhost:8080/v1/watch
```

## Configuration

| Variable | Purpose | Default | Required |
|----------|---------|---------|----------|
| `TWITCH_CLIENT_ID` | Twitch OAuth client ID | — | For real OAuth |
| `TWITCH_CLIENT_SECRET` | Twitch OAuth client secret | — | For real OAuth |
| `TWITCH_REDIRECT_URI` | Twitch OAuth redirect URI | — | For real OAuth |
| `DATABASE_DSN` | PostgreSQL connection string | — | Optional (in-memory fallback) |
| `TWITCH_ROOM_AUTH_TOKEN` | Bearer token for authentication | — | Optional (dev auth fallback) |
| `PORT` | Service port | `8080` | No |
| `ALLOW_DEV_AUTH` | Enable dev auth header | `false` | No |
| `WATCH_STATE_TTL_MINUTES` | Watch state expiry (minutes) | `5` | No |

## API

- `GET /health`
- `POST /v1/watch`
- `GET /v1/users/{user_id}/watch`
- `GET /v1/feed`

## Notes

This first version uses an in-memory repository. It is intentionally simple so the core watch-sharing behavior is easy to test before adding persistence or real OAuth integration.
