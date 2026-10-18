#!/bin/sh
# Render the twitch-room scrape config into /tmp/prometheus.
#
# Prometheus does not expand environment variables inside its configuration, so
# the bearer token for /metrics cannot be templated in prometheus.yml directly.
# Instead this script writes TWITCH_ROOM_AUTH_TOKEN to the file that
# credentials_file points at.
#
# With no token configured the service serves /metrics publicly, and the
# authorization block is removed from the rendered config; the patterns below
# must stay in step with the block in prometheus.yml (TestDeployArtifacts
# checks that, and the block is four and six spaces deep).
set -eu

src=/etc/prometheus/prometheus.yml
dst=/tmp/prometheus/prometheus.yml
token_file=/tmp/prometheus/metrics_token
token="${TWITCH_ROOM_AUTH_TOKEN:-}"

mkdir -p /tmp/prometheus

if [ -n "$token" ]; then
    printf '%s' "$token" > "$token_file"
    chmod 600 "$token_file"
    cp "$src" "$dst"
else
    rm -f "$token_file"
    grep -v -e '^    authorization:$' \
            -e '^      type: Bearer$' \
            -e '^      credentials_file:' \
        "$src" > "$dst"
fi
