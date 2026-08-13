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

Run the service:

```bash
go run .
```

Then send requests like:

```bash
curl -H "Authorization: Bearer dev-token" -H "X-User-ID: user-1" \
  -H "Content-Type: application/json" \
  -d '{"stream_ids":["stream-1","stream-2"]}' \
  http://localhost:8080/v1/watch
```

Set the following environment variables for a stricter demo auth setup:

```bash
export TWITCH_ROOM_AUTH_TOKEN="your-secret-token"
export ALLOW_DEV_AUTH="true"
```

## API

- `GET /health`
- `POST /v1/watch`
- `GET /v1/users/{user_id}/watch`
- `GET /v1/feed`

## Notes

This first version uses an in-memory repository. It is intentionally simple so the core watch-sharing behavior is easy to test before adding persistence or real OAuth integration.
