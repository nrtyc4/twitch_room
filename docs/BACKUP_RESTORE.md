# Backup and Restore — Twitch Room (PostgreSQL)

## Scope
The only durable state lives in PostgreSQL: `user_watch_state`, `publisher_settings`, `subscriptions`, and the `sessions` table (if `postgresSessionBackend` is in use). In-memory deployments have no durable state.

## Backup
```sh
# Logical dump (recommended for small/medium deployments)
pg_dump -Fc -h <host> -U twitch_room -d twitch_room -f twitch_room_$(date +%F).dump

# Or via docker compose
docker compose exec db pg_dump -Fc -U twitch_room twitch_room > backup.dump
```

## Restore
```sh
# Drop/recreate the database first if restoring over existing data:
dropdb -h <host> -U twitch_room twitch_room && createdb -h <host> -U twitch_room twitch_room
pg_restore -h <host> -U twitch_room -d twitch_room --clean --if-exists backup.dump
```

## Verification
1. Restart the service; it runs `initSchema` (CREATE TABLE IF NOT EXISTS) so missing tables are recreated.
2. Hit `/health` and `/metrics`; confirm `twitch_room_active_watch_states` and subscription counts match pre-backup.
3. Log in as a test user and confirm watch state + settings + subscriptions are present.

## Cadence
- Daily automated dump retained for 14 days; weekly retained for 8 weeks.
- Test a restore into a staging database at least once per release.