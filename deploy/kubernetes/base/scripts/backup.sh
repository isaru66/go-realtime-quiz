#!/bin/sh
set -eu
umask 077
export REDISCLI_AUTH="${REDIS_PASSWORD:?required}"
: "${REDIS_NODES:?required}" "${BLOB_CONTAINER_URL:?required}" "${BLOB_SAS_TOKEN:?required}"
case "$BLOB_CONTAINER_URL" in https://*.blob.core.windows.net/*) ;; *) echo "Use an HTTPS Azure Blob container URL" >&2; exit 1;; esac
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
root="/work/$stamp"
mkdir -p "$root"
cd "$root"
started="$(date -u +%FT%TZ)"
seed="$(echo "$REDIS_NODES" | awk '{print $1}')"
topology() {
  redis-cli -h "$seed" cluster nodes |
    awk '{gsub("myself,","",$3); printf "%s %s %s %s",$1,$3,$4,$7; for(i=9;i<=NF;i++) printf " %s",$i; printf "\n"}' | sort
}
topology > topology.before
echo '[]' > files.json
count=0
for host in $REDIS_NODES; do
  redis-cli -h "$host" cluster info | grep -q 'cluster_state:ok'
  role="$(redis-cli -h "$host" --raw info replication | tr -d '\r' | sed -n 's/^role://p')"
  [ "$role" = master ] || continue
  id="$(redis-cli -h "$host" cluster myid | tr -d '\r')"
  redis-cli -h "$host" cluster nodes > "$id.nodes"
  redis-cli -h "$host" --rdb "$id.rdb"
  sha="$(sha256sum "$id.rdb" | awk '{print $1}')"
  jq --arg host "$host" --arg id "$id" --arg sha "$sha" \
    '. + [{hostname:$host,node_id:$id,rdb:($id+".rdb"),sha256:$sha,topology:($id+".nodes")}]' files.json > next.json
  mv next.json files.json
  count=$((count + 1))
done
[ "$count" -eq 3 ] || { echo "Expected exactly 3 primaries, found $count; discard backup" >&2; exit 1; }
topology > topology.after
cmp topology.before topology.after || { echo "Topology changed during backup; discard set and retry" >&2; exit 1; }
jq -n --arg started "$started" --arg completed "$(date -u +%FT%TZ)" \
  --slurpfile files files.json \
  '{format:1,started_at:$started,completed_at:$completed,consistency:"independent per-primary RDB snapshots; NOT distributed atomic",files:$files[0]}' > manifest.json
upload() {
  # curl reads the credential through stdin instead of exposing it in argv.
  printf 'url = "%s/%s/%s?%s"\n' "${BLOB_CONTAINER_URL%/}" "$stamp" "$1" "${BLOB_SAS_TOKEN#\?}" |
    curl --config - --silent --show-error --fail --retry 3 \
      -X PUT -H 'x-ms-blob-type: BlockBlob' --upload-file "$1"
}
for file in *.rdb *.nodes; do upload "$file"; done
# Manifest is the completion marker, uploaded last. Ignore sets without it.
upload manifest.json
echo "Completed backup set $stamp (three independent snapshots)"
