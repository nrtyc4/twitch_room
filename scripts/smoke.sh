#!/usr/bin/env bash
# Smoke-test the twitch-room backend against an in-memory store.
# Requires: go, curl. Run from the repo root.
set -u

PORT="${PORT:-8080}"
BASE="http://localhost:${PORT}"
BIN="${TMPDIR:-/tmp}/twitch-room-smoke.exe"
RUNDIR="${TMPDIR:-/tmp}/twitch-room-smoke"
TOKEN="dev-smoke-token"

mkdir -p "$RUNDIR"
echo "Building..."
go build -o "$BIN" . || { echo "build failed"; exit 1; }

echo "Starting server (in-memory)..."
( cd "$RUNDIR" && DATABASE_DSN="" APP_ENV="" ALLOW_DEV_AUTH=true PORT="$PORT" \
    TWITCH_ROOM_AUTH_TOKEN="$TOKEN" ALLOWED_ORIGINS="*" "$BIN" >/tmp/twitch-room-smoke.log 2>&1 ) &
SRVPID=$!
sleep 2

fail=0
check() {
  local name="$1"; shift
  local code
  code=$(curl -s -o /tmp/smoke-body -w "%{http_code}" "$@")
  echo "[$code] $name"
  if [ "$code" -ge 400 ]; then fail=1; cat /tmp/smoke-body; echo; fi
}

AUTH=(-H "Authorization: Bearer $TOKEN" -H "X-User-ID: alice")
ABOB=(-H "Authorization: Bearer $TOKEN" -H "X-User-ID: bob")

echo "--- smoke ---"
check "health" "$BASE/health"
check "metrics (authed)" -H "Authorization: Bearer $TOKEN" "$BASE/metrics"
check "publish alice" -X POST "$BASE/v1/watch" -H "Content-Type: application/json" "${AUTH[@]}" -d '{"stream_ids":["shroud"]}'
check "read alice" "$BASE/v1/users/alice/watch" "${AUTH[@]}"
check "settings alice" "$BASE/v1/settings" "${AUTH[@]}"
check "subscribe bob->alice" -X POST "$BASE/v1/subscriptions/alice" "${ABOB[@]}"
check "feed bob" "$BASE/v1/feed" "${ABOB[@]}"
# make public then share page
curl -s -X PUT "$BASE/v1/settings" -H "Content-Type: application/json" "${AUTH[@]}" -d '{"subscription_mode":"open","visibility":"public"}' >/dev/null
code=$(curl -s -o /tmp/smoke-body -w "%{http_code}" "$BASE/u/alice")
echo "[$code] share page (public)"
[ "$code" = "200" ] || { fail=1; cat /tmp/smoke-body; echo; }
check "delete alice data" -X DELETE "$BASE/v1/account" "${AUTH[@]}"

kill $SRVPID 2>/dev/null
wait $SRVPID 2>/dev/null

if [ "$fail" = "0" ]; then
  echo "SMOKE TEST: PASS"
  exit 0
else
  echo "SMOKE TEST: FAIL"
  echo "--- server log ---"; tail -20 /tmp/twitch-room-smoke.log
  exit 1
fi