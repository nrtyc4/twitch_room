#!/bin/sh
# Entry point for the twitch-room Prometheus image: render the config (see
# render-config.sh) and hand over to Prometheus.
#
# The config file is passed explicitly here because the base image's own CMD
# would otherwise re-point --config.file at the un-rendered copy.
set -eu

/usr/local/bin/render-config.sh

exec /bin/prometheus --config.file=/tmp/prometheus/prometheus.yml "$@"
