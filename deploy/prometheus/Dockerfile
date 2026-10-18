# Prometheus preloaded with the twitch-room scrape configuration.
#
# The config is baked in rather than bind-mounted from the host so the compose
# service works without Docker Desktop file sharing; the metrics token is
# injected at startup from TWITCH_ROOM_AUTH_TOKEN (see docker-entrypoint.sh).
FROM prom/prometheus:v2.54.1

# The base image runs as nobody; copying and chmod need root for this step.
USER root
COPY prometheus.yml twitch_room_alerts.yml /etc/prometheus/
COPY render-config.sh docker-entrypoint.sh /usr/local/bin/
RUN chmod 0755 /usr/local/bin/render-config.sh /usr/local/bin/docker-entrypoint.sh
USER nobody

# The base image points --config.file at the un-rendered config; the entrypoint
# passes the rendered one instead.
CMD ["--storage.tsdb.path=/prometheus"]
ENTRYPOINT ["/bin/sh", "/usr/local/bin/docker-entrypoint.sh"]
