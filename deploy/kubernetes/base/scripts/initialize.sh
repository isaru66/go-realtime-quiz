#!/bin/sh
set -eu
export REDISCLI_AUTH="${REDIS_PASSWORD:?required}"
: "${REDIS_NODES:?space-separated hostnames required}"
set -- $REDIS_NODES
total=$#
case "$total" in
  1) expected_primaries=1; expected_replicas=0; replicas_per_primary=0 ;;
  6) expected_primaries=3; expected_replicas=3; replicas_per_primary=1 ;;
  *) echo "Expected one or six Redis endpoints, found $total" >&2; exit 1 ;;
esac
unique="$(printf '%s\n' "$@" | sort -u | wc -l)"
[ "$unique" -eq "$total" ] || { echo "Duplicate Redis endpoints" >&2; exit 1; }
fresh=0
addresses=""
for host in $REDIS_NODES; do
  addresses="$addresses $host:6379"
  attempts=0
  until redis-cli -h "$host" ping | grep -q PONG; do
    attempts=$((attempts + 1))
    [ "$attempts" -lt 120 ] || { echo "Redis unavailable: $host" >&2; exit 1; }
    sleep 2
  done
  info="$(redis-cli -e -h "$host" cluster info)"
  info="$(printf '%s\n' "$info" | tr -d '\r')"
  if echo "$info" | grep -qx 'cluster_known_nodes:1' &&
     echo "$info" | grep -qx 'cluster_slots_assigned:0' &&
     [ "$(redis-cli -e -h "$host" dbsize)" = 0 ]; then
    fresh=$((fresh + 1))
  elif ! echo "$info" | grep -qx "cluster_known_nodes:$total"; then
    echo "Redis topology mismatch at $host; refusing to change existing state. See operations.md for migration." >&2
    exit 1
  fi
done
if [ "$fresh" -eq "$total" ]; then
  if [ "$total" -eq 1 ]; then
    redis-cli -e -h "$1" cluster addslotsrange 0 16383
  else
    # Password is inherited through REDISCLI_AUTH, never a command-line argument.
    # Intentional word splitting: addresses are operator-supplied DNS hostnames.
    redis-cli -e --cluster create $addresses --cluster-replicas 1 --cluster-yes
  fi
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
    info="$(redis-cli -e -h "$host" cluster info)"
    info="$(printf '%s\n' "$info" | tr -d '\r')"
    if echo "$info" | grep -qx 'cluster_state:ok' &&
       echo "$info" | grep -qx 'cluster_slots_ok:16384' &&
       echo "$info" | grep -qx "cluster_known_nodes:$total"; then
      healthy=$((healthy + 1))
    fi
    replication="$(redis-cli -e -h "$host" info replication)"
    replication="$(printf '%s\n' "$replication" | tr -d '\r')"
    if echo "$replication" | grep -qx 'role:master' &&
       echo "$replication" | grep -qx "connected_slaves:$replicas_per_primary"; then
      primaries=$((primaries + 1))
    elif echo "$replication" | grep -qx 'role:slave' &&
         echo "$replication" | grep -qx 'master_link_status:up'; then
      replicas=$((replicas + 1))
    fi
  done
  [ "$healthy" -eq "$total" ] && [ "$primaries" -eq "$expected_primaries" ] &&
    [ "$replicas" -eq "$expected_replicas" ] && break
  attempts=$((attempts + 1))
  [ "$attempts" -lt 60 ] || { echo "Cluster unhealthy; inspect CLUSTER NODES/INFO. No state was reset." >&2; exit 1; }
  sleep 2
done
redis-cli --cluster check "$(echo "$REDIS_NODES" | awk '{print $1}'):6379"
