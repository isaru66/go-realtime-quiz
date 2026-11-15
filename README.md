# Real-time Multiplayer Quiz Platform

A Kubernetes-native, Kahoot-like quiz platform planned around React,
TypeScript, Go, WebSocket, and Redis Cluster.

## Status

**Phase 1: architecture and project structure.**

This repository currently contains the scaffold and technical design, not a
runnable application. Backend, frontend, Docker Compose, deployment manifests,
and executable tests will be added through the approval-gated phases below.
No application dependencies or runtime versions have been selected yet.

## Project layout

```text
.
|-- src/
|   |-- backend/
|   |   |-- cmd/api/                # Go process entry point
|   |   `-- internal/
|   |       |-- config/            # Validated, versioned quiz catalog
|   |       |-- game/              # Lifecycle, scoring, repository contracts
|   |       |-- redis/             # Cluster state, atomic commands, messaging
|   |       |-- transport/         # REST and WebSocket adapters
|   |       `-- observability/     # Logging, metrics, request correlation
|   `-- frontend/
|       |-- public/
|       `-- src/
|           |-- app/               # Routing and application composition
|           |-- features/
|           |   |-- player/
|           |   |-- host/
|           |   `-- admin/
|           `-- shared/            # Protocol, connection, identity, UI helpers
|-- infra/                         # Azure Terraform used by azd (see infra/README.md)
|   `-- modules/                   # network, aks, acr, monitoring, keyvault
|-- azure.yaml                     # azd project: Terraform provider, AKS services
|-- config/                        # Sample backend-only quiz YAML
|-- deploy/
|   |-- compose/                   # Local multi-node Redis environment
|   `-- kubernetes/
|       |-- base/
|       `-- overlays/
|           |-- development/
|           `-- production/
|-- tests/
|   |-- integration/               # Real Redis Cluster and cross-pod tests
|   `-- load/                      # k6 scenarios
`-- docs/
    |-- architecture.md            # Technical design and architecture diagram
    `-- development-plan.md        # Phase deliverables and approval criteria
```

`.gitkeep` files preserve intentionally empty directories until implementation.

## Design and development

Read [the architecture](docs/architecture.md) and
[the development plan](docs/development-plan.md) first.

Each phase ends with an explanation, generated-file list, relevant checks,
outstanding issues, and an approval gate. Phase 2 defines the Redis data model,
versioned WebSocket contracts, and join/answer/reconnect sequence diagrams.

## Setup and deployment

There are no setup or deployment commands yet. Runnable instructions will arrive
with the backend in Phase 3, frontend in Phase 5, and Compose/Kubernetes
environments in Phases 4 and 7. Do not treat this scaffold as deployable software.

Azure provisioning uses the Azure Developer CLI with Terraform. Currently,
`azd provision` creates only the resource group. AKS and supporting resources,
plus a working `azd deploy`, arrive in Phase 7. See
[the infrastructure guide](infra/README.md).
