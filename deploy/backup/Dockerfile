FROM redis:7.4.7-alpine
USER root
RUN apk add --no-cache curl jq && mkdir -p /work && chown redis:redis /work
USER redis
ENTRYPOINT ["/bin/sh", "/opt/backup/backup.sh"]
