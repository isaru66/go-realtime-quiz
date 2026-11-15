# Operations

## Scope and guarantees

Kubernetes defaults to **one Redis 7.4.7 pod in cluster mode**, owning all 16384
slots with no replicas. An opt-in Kustomize component restores **six pods: three
primaries and three replicas**. Compose remains six-node by default and is not
switchable through this component. Both modes retain authentication, AOF
`appendfsync everysec`, `noeviction`, and per-node persistent volumes. Backend
state is shared, never sticky-session dependent. Two backend processes are
mandatory for cross-process testing.

Single-pod Redis has **no automatic failover**: restarts, voluntary evictions,
and node loss interrupt service until Redis recovers. A surviving PVC preserves
data; loss of its disk requires restoring a backup. Six-node Redis uses
asynchronous replication: an acknowledged answer can be lost during
failover. AOF's nominal one-second fsync interval is not a hard data-loss bound
during disk stalls, host loss, or replica promotion. No zero-loss or
distributed-atomic backup guarantee is made.

Never run `down -v`, delete Redis PVCs, `FLUSHALL`, `CLUSTER RESET`, or initialize
an empty replacement against existing state as routine recovery. Bootstrap
assigns slots only when **all configured nodes are empty, unassigned singletons**.
Only one or six distinct endpoints are supported. Otherwise it validates all
slots and either one primary with no replicas or three healthy primary/replica
pairs; mixed state fails with recovery guidance, without rewriting existing state.

## Local Compose

From the repository root, Docker Engine and the Docker Compose v2 plugin:

```bash
umask 077
cp deploy/compose/.env.example deploy/compose/.env
# Replace all three placeholders independently. This prints random values;
# don't paste them into tracked files, logs, or shell history:
openssl rand -base64 48 | tr -d '\n'; echo
docker compose --env-file deploy/compose/.env -f deploy/compose/compose.yaml config --quiet
docker compose --env-file deploy/compose/.env -f deploy/compose/compose.yaml up --build -d
docker compose --env-file deploy/compose/.env -f deploy/compose/compose.yaml ps
curl --fail http://127.0.0.1:18080/readyz
curl --fail http://127.0.0.1:18081/readyz
```

`.env` is ignored; `.env.example` contains placeholders, not usable defaults.
Alternatively supply all three variables through your shell/secret manager.
Generate `REDIS_PASSWORD`, `ADMIN_CREDENTIAL`, and `APP_SECRET` independently;
`APP_SECRET` is a **raw** base64 string, at least 32 characters, not a decoded
key. Redis's generated configuration permits base64/base64url characters only
to avoid Redis-config injection. Its `/data/runtime.conf` is mode 0600 and
includes credentials; protect volumes like Secrets. Credentials are runtime
environment values, visible to Docker administrators, never committed defaults.

Open `http://localhost:8088`. Backend `backend` and `backend2` are exposed only
on `127.0.0.1:18080` and `127.0.0.1:18081`. Frontend port 8088 is published on
all interfaces; restrict the host firewall when needed. Frontend nginx proxies
to `backend:8080`; direct backend ports intentionally permit cross-pod tests.
There are no published Redis ports: cluster redirects announce Docker DNS names
reachable **inside** the Compose network, not host localhost.

```bash
# Inspect authenticated cluster from its network without putting password in argv.
docker compose --env-file deploy/compose/.env -f deploy/compose/compose.yaml exec redis-0 \
  sh -ec 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli --cluster check redis-0:6379'
# Run Redis-connected Go integration commands in backend containers or a Go
# runner attached to quiz_default. Do NOT replace REDIS_ADDRS with localhost.
# Restart retains all data and the same Secret values:
docker compose --env-file deploy/compose/.env -f deploy/compose/compose.yaml restart redis-0
docker compose --env-file deploy/compose/.env -f deploy/compose/compose.yaml down
```

`redis-init` must complete successfully before either backend starts. Redis
healthchecks during bootstrap test authenticated PING, not cluster readiness
(which would deadlock bootstrap); the init script verifies the finished cluster.
Do not change Redis credentials between restarts without a coordinated rotation.

### Backend environment contract

| Variable | Local value / production requirement |
|---|---|
| `LISTEN_ADDR` | `:8080` |
| `QUIZ_PATH` | `/config/quizzes.yaml` |
| `PROTOCOL_SCHEMA_PATH` | `/app/protocol-v1.schema.json`, included in backend image |
| `REDIS_ADDRS` | Single mode: `redis-0.redis:6379`; six-node mode: all six comma-separated client endpoints; no cluster-bus ports |
| `REDIS_PASSWORD` | Runtime Secret, shared across Redis/backend/init/backup |
| `ADMIN_CREDENTIAL` | Runtime Secret, never a Vite variable |
| `APP_SECRET` | Shared raw random string of at least 32 characters |
| `ALLOWED_ORIGINS` | Local: `http://localhost:8088,http://localhost:5173`; production: exact HTTPS origin |
| `COOKIE_SECURE` | `false` locally (`quiz_admin`), `true` in production (`__Host-quiz_admin`) |
| `TRUSTED_PROXIES` | Compose trusts only its frontend at `172.30.0.10`; Kubernetes overlays trust the configured cluster pod CIDR, with backend ingress restricted to ingress-nginx by NetworkPolicy |
| `REDIS_TLS` | `false` for supplied in-cluster Redis; see TLS boundary below |
| `REDIS_CA_FILE` | Mounted CA bundle path when connecting to TLS-enabled Redis |
| `GAME_IDLE_SECONDS` | Default `7200` (two hours) |
| `GAME_LIFETIME_SECONDS` | Default `86400` (24 hours absolute lifetime) |
| `RESULT_RETENTION_SECONDS` | Default `3600` (one hour) |
| `JOIN_RATE_PER_MINUTE` | Default `30` joins per source IP per minute; allowed `1..100000` |

Lifetime settings accept `1..604800` seconds; idle and result retention cannot
exceed absolute lifetime. Policies are captured when a game is created, so
changing defaults does not rewrite existing games. Compose accepts these four
optional values through the shell or its env file. Kubernetes supplies them
through the generated non-secret `quiz-runtime-config` ConfigMap; override its
literals with a Kustomize generator merge in the environment overlay. The
generated name hash rolls backend pods when defaults change.

For disposable load tests, raise `JOIN_RATE_PER_MINUTE` enough for the planned
join/reconnect burst; for example, prefix the documented Compose `up` command
with `JOIN_RATE_PER_MINUTE=1000` to recreate backends with that setting. Do not
raise or disable production controls just to obtain a larger benchmark. For
legitimate shared-NAT deployments, size the per-IP budget to expected users and
monitor abuse; forwarded addresses are only trustworthy from configured proxy
networks, never arbitrary client headers. See `tests/load/README.md`.

WebSocket capacity is 4096 connections per backend process. Redis TLS validates
a server CA only; client-certificate authentication is not implemented.

## Kubernetes prerequisites and Azure provisioning

Tools: Terraform >=1.9, azd, Azure CLI, kubectl with Kustomize, Helm.
Authenticate to the intended subscription; **review cost, identity, quota,
region/SKU/zone support and the Terraform plan before provisioning**:

```bash
azd auth login
az login
az account set --subscription "$AZURE_SUBSCRIPTION_ID"
azd env new dev
azd env set AZURE_LOCATION southeastasia3
# Use Entra group object IDs, not user display names:
export TF_VAR_aks_admin_group_object_ids='["YOUR-ADMIN-GROUP-OBJECT-ID"]'
azd provision
```

Terraform preserves `southeastasia3` -> `asse3` and the existing resource-group
naming contract. The modules create a VNet and dedicated AKS/reserved ingress
subnets, Entra/RBAC AKS with Cilium policies and autoscaled nodes, non-admin ACR,
kubelet `AcrPull`, Log Analytics, managed Prometheus collection, and RBAC Key
Vault with purge protection. There is no Azure Redis service and no plaintext
secret value in Terraform. The reserved ingress subnet is not automatically
used by the nginx LoadBalancer. AKS uses an outbound public load balancer;
private AKS endpoints, private registry/vault endpoints, firewall allowlists,
backup storage accounts, ingress DNS/certificates, and alert routing require
environment-specific configuration and are not silently provisioned.

Local Terraform state must be migrated to an access-controlled Azure Storage
backend with locking before shared/CI production use. Configure the backend in
`infra/provider.tf` with non-secret backend arguments; authenticate using Entra
credentials. Never commit state or put access keys in backend files.

```bash
azd env get-values
# Populate these non-secret values from the azd environment outputs:
az aks get-credentials --resource-group "$AZURE_RESOURCE_GROUP" \
  --name "$AZURE_AKS_CLUSTER_NAME" --overwrite-existing
# kubelogin may be needed for Entra authentication.
kubectl apply -f deploy/kubernetes/base/namespace.yaml
```

The provisioning identity needs permission to create role assignments. The
deploying identity needs AKS RBAC authorization and ACR push; kubelet pull
permission alone does not permit deployment. Key Vault CSI is enabled, but
application Secrets are not automatically copied from the vault. Provision a
separately authorized secret-delivery mechanism or create the Kubernetes
Secrets below. Backend ServiceAccounts have no RBAC bindings and no API tokens.

### Secret, ingress, storage and image setup

Create `quiz-secrets` from a protected file with exactly the three application
variables (a filled Compose `.env` is suitable for local development):

```bash
kubectl -n quiz-platform create secret generic quiz-secrets \
  --from-env-file=deploy/compose/.env --dry-run=client -o yaml | kubectl apply -f -
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm repo update
# Select an organization-approved pinned chart version before installation:
helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx \
  --version "$INGRESS_NGINX_CHART_VERSION" --namespace ingress-nginx --create-namespace \
  --set controller.replicaCount=2 \
  --set controller.config.use-forwarded-headers=false \
  --set controller.config.compute-full-forwarded-for=false
kubectl -n quiz-platform create secret tls quiz-ingress-tls \
  --cert=deploy/tls.crt --key=deploy/tls.key
```

Do not commit certificate keys or secret-file output. Use your secret manager's
certificate paths instead of the illustrative paths above. Configure ingress
DNS to the controller LoadBalancer; replace `quiz.example.com` in Ingress
`rules` and `tls.hosts`, plus backend `ALLOWED_ORIGINS`, using an
environment-specific Kustomize patch. Never leave placeholder hosts or local
cookie settings in production. Nginx WebSocket read/send timeouts are 3600s;
idle sockets can still disconnect and must reconnect.

Development uses the cluster's default dynamic StorageClass. Production
requests `managed-csi-premium`. Single mode uses one 10Gi Redis PVC and needs
one eligible worker; this does not change the Terraform AKS pool minimum.
Six-node production needs six distinct workers and six 10Gi PVCs; AKS autoscaler
can scale the provided pool from three to six. Ensure quota, capacity and CSI
permissions for the selected mode. Base/development placement across three
workers **does not guarantee** each primary is on a different worker from its
replica. Production anti-affinity separates all six, but zone-level placement and
primary/replica zone diversity still need verification after bootstrap.
PDBs restrict voluntary evictions only, not node/zone failures.

### Select Redis topology

Both `overlays/development` (also used by azd) and `overlays/production` default
to a single pod in **cluster mode**, not standalone Redis. The existing Go
ClusterClient and cluster readiness checks work unchanged. Bootstrap assigns
all slots to that pod and validates existing state on subsequent runs.

For six pods, add this top-level entry to the selected overlay's
`kustomization.yaml` (append to `components` if it already exists):

```yaml
components:
  - ../../components/redis-cluster
```

The component changes Redis replicas, backend discovery, init endpoints, and
backup endpoints together. Do not override just the replica count. Remove the
component entry to select single mode for a **fresh deployment**. Existing
populated deployments must follow the migration guidance below.

```bash
kubectl kustomize deploy/kubernetes/overlays/production
kubectl apply -k deploy/kubernetes/overlays/production
kubectl -n quiz-platform wait --for=condition=complete job/redis-init --timeout=600s
kubectl -n quiz-platform rollout status deployment/backend --timeout=300s
```

Substitute `development` when appropriate. Complete the secrets/image setup
before applying; neither mode provisions backup storage or enables backups.

**Upgrade warning for existing six-pod deployments:** enable the component
**before** applying this default change, including before `azd deploy`.
Otherwise the StatefulSet would be scaled down and backends would point at
only one shard. Bootstrap's refusal cannot undo that scale-down.

**Changing modes with data is a migration, not an in-place switch.** Stop
writers, take and verify a complete backup, and restore into a separately
configured target namespace with new volumes using the restore drill below.
Patch its namespace and `REDIS_DNS_SUFFIX` consistently with that namespace.
Preserve the old deployment/PVCs and original application secrets until target
validation and traffic cutover succeed. Never scale six to one, reuse old
cluster metadata, or reset/delete PVCs to make initialization pass. Redeploying
without changing modes preserves the existing StatefulSet/PVC identities.

### Apply versioned images

For explicit, reproducible deployment, build/push versioned images, then copy
an overlay to your environment configuration and set its `images` entries:

```bash
az acr login --name "$AZURE_CONTAINER_REGISTRY_NAME"
docker build -f src/backend/Dockerfile -t "$AZURE_CONTAINER_REGISTRY_ENDPOINT/backend:$TAG" .
docker build -t "$AZURE_CONTAINER_REGISTRY_ENDPOINT/frontend:$TAG" src/frontend
docker build -t "$AZURE_CONTAINER_REGISTRY_ENDPOINT/redis-backup:$TAG" deploy/backup
docker push "$AZURE_CONTAINER_REGISTRY_ENDPOINT/backend:$TAG"
docker push "$AZURE_CONTAINER_REGISTRY_ENDPOINT/frontend:$TAG"
docker push "$AZURE_CONTAINER_REGISTRY_ENDPOINT/redis-backup:$TAG"
# Run from the selected overlay directory; these persist your image pins:
cd deploy/kubernetes/overlays/production
kustomize edit set image "quiz-backend=$AZURE_CONTAINER_REGISTRY_ENDPOINT/backend:$TAG" \
  "quiz-frontend=$AZURE_CONTAINER_REGISTRY_ENDPOINT/frontend:$TAG" \
  "quiz-redis-backup=$AZURE_CONTAINER_REGISTRY_ENDPOINT/redis-backup:$TAG"
cd ../../../..
kubectl apply -k deploy/kubernetes/overlays/production
kubectl -n quiz-platform wait --for=condition=complete job/redis-init --timeout=600s
kubectl -n quiz-platform rollout status deployment/backend --timeout=300s
kubectl -n quiz-platform rollout status deployment/frontend --timeout=300s
```

Use immutable digests for a release. `azure.yaml` also describes azd's
development AKS services with backend's repository-root build context and
per-service Kustomize image edits. With azd versions gating this feature, run
`azd config set alpha.aks.kustomize on` before `azd deploy`.
The explicit image-pin/apply workflow above is the production source of truth;
do not mix azd development deploys and production apply against one namespace.
Changing generated scripts changes a Job's immutable template: delete the
**completed** `redis-init` Job before reapplying; the new Job validates existing
state, not resets it. StatefulSet updates are `OnDelete`: replace Redis pods
one at a time after checking primary/replica health and waiting for resync in
six-node mode. Single mode needs a maintenance window: restarting its only pod
interrupts service. The Redis PDB permits one unavailable pod in either mode.

### Configuration reload and TLS boundary

`config/quizzes.yaml` is the authoring sample. Kustomize's safe file-load
boundary requires its mirrored `deploy/kubernetes/base/quizzes.yaml`:

```bash
cp config/quizzes.yaml deploy/kubernetes/base/quizzes.yaml
cmp config/quizzes.yaml deploy/kubernetes/base/quizzes.yaml
kubectl kustomize deploy/kubernetes/overlays/production
```

Commit both sample files together. The ConfigMap generator hashes changes and
the whole directory mounts read-only at `/config`, never `subPath`. Correct
answers are backend-only; frontend never receives this volume. Kubernetes
ConfigMap values are not suitable for credentials. Reload and per-pod version
convergence are eventual; existing games retain their immutable snapshots.

External HTTP uses TLS ingress. Supplied Redis traffic is **authenticated but
plaintext inside explicit NetworkPolicies**. Redis server/bus TLS and internal
mTLS are **not implemented by these overlays**. This boundary must be accepted
before production or replaced with a TLS-enabled Redis deployment. For an
external TLS cluster, patch `REDIS_ADDRS`, set `REDIS_TLS=true`, mount its CA
Secret directory, set `REDIS_CA_FILE`, and allow its client endpoint in egress.
Backend TLS configuration alone does not enable server TLS; never flip it
against the plaintext StatefulSet. Also adapt bootstrap/backup tooling if
replacing the supplied Redis topology.

## Backups and recovery

The backup CronJob is intentionally **suspended** until its image and an
off-cluster Azure Blob container are configured. The custom backup image
contains `redis-cli`, `curl`, and `jq`; it requires no Kubernetes API access.
Each run discovers the current primaries from the configured stable DNS endpoints,
streams an independent RDB per primary, and records node IDs, topology,
timestamps, and SHA-256 checksums in a manifest. It requires one primary in
single mode or three in six-node mode; mismatched membership, missing healthy
slots, or topology changes abort the set.
Only prefixes containing `manifest.json` are complete. This is **not** a
distributed atomic snapshot: answers across shards may reflect different
times. Prefer backups when no games are active; for a logical consistency
boundary stop all writers and wait for in-flight work before taking the set.

Create a separate Blob account/container with encryption, HTTPS-only access,
retention/lifecycle rules (initial recommendation: 30 days), soft deletion and
restricted operator access. Issue a short-lived container SAS scoped to
create/write only; restore credentials should separately grant read. Supply
`BLOB_CONTAINER_URL=https://ACCOUNT.blob.core.windows.net/CONTAINER` and
`BLOB_SAS_TOKEN` (with or without leading `?`) in an untracked protected file:

```bash
kubectl -n quiz-platform create secret generic backup-storage \
  --from-env-file=deploy/backup-storage.env --dry-run=client -o yaml | kubectl apply -f -
kubectl -n quiz-platform patch cronjob redis-backup --type=merge \
  -p '{"spec":{"suspend":false}}'
kubectl -n quiz-platform create job --from=cronjob/redis-backup redis-backup-manual
kubectl -n quiz-platform logs -f job/redis-backup-manual
```

The policy permits backup HTTPS to public endpoints, not just one storage
account (Kubernetes NetworkPolicy cannot match DNS names). Tighten using an
egress gateway/firewall, or add explicit private-endpoint CIDRs. No backup
credentials belong in a ConfigMap. Size `/work` and ephemeral-storage limits
for the sum of all primary RDBs. Alert on job failures and absence of a complete
manifest. Rotate SAS before expiry; verify the destination isn't the same
cluster's PVC or ephemeral disk.

### Restore drill / full cluster loss

**Cloud object-storage upload and full Kubernetes restore have not been tested
in this environment.** A local Docker drill did exercise three-primary RDB
generation, manifest/checksums, import through three standalone restore servers
into a new six-node cluster, verification of 30 distributed keys, and persistence
after a target restart. Cloud uploads were stubbed in that drill. Complete a
staging drill before relying on backups.

1. Isolate ingress and scale backend Deployments to zero; suspend backup jobs.
   Record incident time and preserve old PVCs/state for investigation.
2. Select a complete backup prefix, download manifest, RDBs and `.nodes`
   topology files using read-only restore credentials. Verify every SHA-256
   and that the saved primary or three primaries cover all 16384 slots without
   gaps/overlaps.
3. Use a new isolated namespace/network and **new** volumes for the selected
   one- or six-node target. Set its namespace and Redis announced DNS suffix
   consistently. Do not mix old AOF files with restored RDBs: Redis prioritizes
   AOF on startup.
4. For each saved primary node ID (one or three), start a temporary
   **standalone**, authenticated Redis of the same version with that RDB as
   `/data/dump.rdb`, `appendonly no`, and no `nodes.conf`. Export/replay its keys
   to an empty freshly bootstrapped target in the selected mode using
   `redis-cli --cluster import TARGET_SEED:6379 --cluster-from HOST:PORT
   --cluster-from-pass ... --cluster-copy` **with the target seed as SOURCE**.
   Here `--cluster-from` is the standalone RDB server, and the positional seed
   is any target primary. Prefer an isolated admin toolbox; that Redis CLI
   flag exposes the source password to local process inspection. Authenticate
   target via `REDISCLI_AUTH`, and do not log commands containing credentials.
   Run this for each saved primary. Do not use `--cluster-replace` to hide
   duplicate/conflicting restored keys; stop and investigate instead.
5. Verify target slot coverage and, for six-node mode, replica links, plus key
   counts and sampled room state/TTLs against each source. Old absolute deadlines may already have
   expired; restoring does not extend games. Restarting a target with its own
   AOF must retain imported data. Never copy old cluster `nodes.conf` blindly
   into nodes with different identities or addresses.
6. Launch one backend against the restored target with the original
   `APP_SECRET` (creation receipts depend on it), validate admin login,
   snapshot/reconnect behavior and bounded recovery, then restore replicas
   and traffic. Invalidate credentials/rooms that cannot be reconciled.
   Record measured loss and recovery times; only then clean up isolated data.

RPO target for completed off-cluster backups is **15 minutes + backup duration**
under a healthy schedule, not a guaranteed bound: failed jobs or missing
uploads extend it indefinitely. Live failover may lose acknowledged writes
even with AOF. Initial **unverified** RTO objective is 60 minutes after an
operator starts a restore; actual time depends on volume sizes, image pulls,
node provisioning and reconciliation. Test and replace these objectives with
measured values; consistency across shards is not implied by either figure.

### Single-pod recovery

Restart the only Redis pod with its healthy PVC, then wait for cluster readiness
and backend `/readyz` before restoring traffic. There is no replica to promote.
If the PVC is lost, preserve evidence and follow the full restore drill using a
new volume. An empty new pod cannot recover the missing data by bootstrapping.

### Six-node replacement / rolling maintenance

With the same healthy PVC, restart a node and let it rejoin; verify replica
sync before touching another node. With a lost primary, wait for its healthy
replica to promote before replacement. If the PVC is irrecoverable, first
identify the lost node ID, slots and surviving owner using `CLUSTER NODES`.
An operator must use `CLUSTER FORGET` on all survivors, then
`redis-cli --cluster add-node NEW:6379 SEED:6379 --cluster-slave
--cluster-master-id SURVIVING_PRIMARY_ID` with authenticated clients. A failed
primary without a complete surviving replica requires restore/data-loss
acceptance, not automatic empty recreation. Bootstrap intentionally refuses
a mixture of old nodes and a new singleton. Never reshard or reset just to make
the init Job green. Stable announced pod DNS avoids stale **client redirect**
IPs after rescheduling. Redis cluster-bus membership still records peer IPs:
if many pods change IPs simultaneously and gossip cannot reconnect, resolve
their current stable pod DNS, then issue authenticated
`CLUSTER MEET CURRENT_PEER_IP 6379` between existing members. This reconnects
their preserved identities; it must not assign slots, reset IDs, or erase data.
Re-run validation once gossip converges.

## Health, monitoring and validation

Use `/livez` for process health and `/readyz` for valid catalog and usable
Redis. Probe failure should remove traffic, not restart every backend merely
because Redis is down. `/metrics` is internal; ingress does not intentionally
publish it. Watch Redis memory/noeviction errors, replication lag/link state,
slot coverage, AOF persistence errors, disk capacity, Pod restarts, scheduler
lag, WebSocket reconnect rates, config version divergence, and backup age.
Managed Prometheus infrastructure is provisioned, but application scrape
configuration, dashboards and actionable alert receivers must be configured
and verified by operators. NetworkPolicy allows the monitoring namespace and
AKS metrics-agent pods; adjust to your actual collector labels.

```bash
docker compose --env-file deploy/compose/.env -f deploy/compose/compose.yaml config --quiet
cmp config/quizzes.yaml deploy/kubernetes/base/quizzes.yaml
kubectl kustomize deploy/kubernetes/overlays/development
kubectl kustomize deploy/kubernetes/overlays/production
python -m pip install -r tests/deployment/requirements.txt
python -m unittest discover -s tests/deployment -p test_kubernetes.py -v
terraform -chdir=infra fmt -check -recursive
terraform -chdir=infra init -backend=false
terraform -chdir=infra validate
terraform -chdir=infra test
# Against an authorized staging cluster:
kubectl apply --dry-run=server -k deploy/kubernetes/overlays/production
```

Server-side admission, CNI enforcement, Azure provisioning, ingress certificates,
backup upload/restore, and node/zone failure recovery require a real staging
environment and are not established by static rendering or mock Terraform tests.
See `tests/integration/README.md` for isolated real Redis checks of both
topologies, single-pod persistence, and cross-backend gameplay.
