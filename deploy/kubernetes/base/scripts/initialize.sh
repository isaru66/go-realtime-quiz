#!/bin/sh
set -eu
export REDISCLI_AUTH="${REDIS_PASSWORD:?required}"
: "${REDIS_NODES:?space-separated hostnames required}"
fresh=0
total=0
addresses=""
for host in $REDIS_NODES; do
  total=$((total + 1))
  addresses="$addresses $host:6379"
  attempts=0
  until redis-cli -h "$host" ping | grep -q PONG; do
    attempts=$((attempts + 1))
    [ "$attempts" -lt 120 ] || { echo "Redis unavailable: $host" >&2; exit 1; }
    sleep 2
  done
  info="$(redis-cli -h "$host" cluster info | tr -d '\r')"
  if echo "$info" | grep -qx 'cluster_known_nodes:1' &&
     echo "$info" | grep -qx 'cluster_slots_assigned:0' &&
     [ "$(redis-cli -h "$host" dbsize)" = 0 ]; then
    fresh=$((fresh + 1))
  fi
done
if [ "$fresh" -eq "$total" ] && [ "$total" -eq 6 ]; then
  # Password is inherited through REDISCLI_AUTH, never a command-line argument.
  # Intentional word splitting: addresses are operator-supplied DNS hostnames.
  redis-cli --cluster create $addresses --cluster-replicas 1 --cluster-yes
elif [ "$fresh" -ne 0 ]; then
  echo "Mixed fresh/existing Redis state; refusing reset. Recover failed members with CLUSTER MEET/REPLICATE or restore; see operations.md." >&2
  exit 1
fi
attempts=0
while :; do
  healthy=0
  primaries=0
  replicas=0
  for host in $REDIS_NODES; do
    info="$(redis-cli -h "$host" cluster info | tr -d '\r')"
    if echo "$info" | grep -qx 'cluster_state:ok' &&
       echo "$info" | grep -qx 'cluster_slots_ok:16384' &&
       echo "$info" | grep -qx 'cluster_known_nodes:6'; then
      healthy=$((healthy + 1))
    fi
    replication="$(redis-cli -h "$host" info replication | tr -d '\r')"
    if echo "$replication" | grep -qx 'role:master' &&
       echo "$replication" | grep -qx 'connected_slaves:1'; then
      primaries=$((primaries + 1))
    elif echo "$replication" | grep -qx 'role:slave' &&
         echo "$replication" | grep -qx 'master_link_status:up'; then
      replicas=$((replicas + 1))
    fi
  done
  [ "$healthy" -eq 6 ] && [ "$primaries" -eq 3 ] && [ "$replicas" -eq 3 ] && break
  attempts=$((attempts + 1))
  [ "$attempts" -lt 60 ] || { echo "Cluster unhealthy; inspect CLUSTER NODES/INFO. No state was reset." >&2; exit 1; }
  sleep 2
done
redis-cli --cluster check "$(echo "$REDIS_NODES" | awk '{print $1}'):6379"
