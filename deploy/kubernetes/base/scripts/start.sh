#!/bin/sh
set -eu
umask 077
: "${REDIS_PASSWORD:?required}"
case "$REDIS_PASSWORD" in *[!A-Za-z0-9+/=_-]*) echo "Redis password must use base64/base64url characters" >&2; exit 1;; esac
host="${REDIS_ANNOUNCE_HOSTNAME:-${HOSTNAME}.${REDIS_DNS_SUFFIX:-redis}}"
cat > /data/runtime.conf <<EOF
bind 0.0.0.0
protected-mode yes
port 6379
dir /data
cluster-enabled yes
cluster-config-file nodes.conf
cluster-node-timeout 5000
cluster-announce-hostname ${host}
cluster-preferred-endpoint-type hostname
appendonly yes
appendfsync everysec
save 900 1
maxmemory 384mb
maxmemory-policy noeviction
requirepass ${REDIS_PASSWORD}
masterauth ${REDIS_PASSWORD}
EOF
exec redis-server /data/runtime.conf
