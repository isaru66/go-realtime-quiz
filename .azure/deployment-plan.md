# Azure Deployment Plan

> **Status:** Planning

Generated: 2026-11-15

---

## 1. Project Overview

**Goal:** Add an `infra` structure for Azure Terraform provisioning that is compatible with
the Azure Developer CLI (`azd`). This step is a scaffold only; deployment is not planned yet.

**Path:** Add Components (the repository is at architecture Phase 1 and has no application code yet)

---

## 2. Requirements

| Attribute | Value |
|-----------|-------|
| Classification | Development scaffold (production hardening planned for development Phase 7) |
| Scale | Medium (multi-replica stateless backend, in-cluster Redis Cluster) |
| Budget | Balanced |
| **Subscription** | Deferred: chosen through `azd env` when provisioning first runs; not needed for this scaffold |
| **Location** | `southeastasia3` (confirmed by the user; region code `asse3`) |

---

## 3. Components Detected

| Component | Type | Technology | Path |
|-----------|------|------------|------|
| backend | API + WebSocket | Go (not yet implemented) | `src/backend` |
| frontend | SPA | React/TypeScript/Vite (not yet implemented) | `src/frontend` |
| Kubernetes manifests | Kustomize overlays (not yet authored) | Kubernetes | `deploy/kubernetes` |

---

## 4. Recipe Selection

**Selected:** AZD + Terraform (`infra.provider: terraform`, `infra.path: ./infra`)

**Rationale:** The user explicitly requested Terraform in `infra` that is compatible with `azd`.
Terraform runs in WSL only, according to user preference.
Terraform state is stored locally under `.azure/<env>`, which is the azd default and the user's choice.

---

## 5. Architecture

**Stack:** Containers on AKS (user choice; matches the project specification)

### Service Mapping

| Component | Azure Service | Notes |
|-----------|---------------|-------|
| backend | AKS (`host: aks`) | Kustomize overlay `deploy/kubernetes/overlays/development` |
| frontend | AKS (`host: aks`) | Same overlay; static assets served from pods |
| Redis Cluster | In-cluster StatefulSet on AKS | User choice; no Azure Managed Redis |
| Quiz configuration | Kubernetes ConfigMap | Managed outside Azure resources |

### Supporting Services (planned modules; not provisioned by this scaffold)

| Service | Purpose |
|---------|---------|
| Virtual network | AKS node and ingress subnets |
| Azure Container Registry | Images pushed by `azd deploy` |
| Log Analytics + Azure Monitor managed Prometheus | Logs and metrics |
| Key Vault + Workload Identity | Redis/admin secrets outside ConfigMaps |
| Managed Identity | AKS kubelet image pull (AcrPull), workload access to Key Vault |

---

## 6. Provisioning Limit Checklist

### Phase 1: Resource Inventory (scaffold only)

| Resource Type | Number to Deploy | Total After Deployment | Limit/Quota | Notes |
|---------------|------------------|------------------------|-------------|-------|
| Microsoft.Resources/resourceGroups | 1 | Current + 1 | 980 per subscription | Official Azure subscription limits documentation; no quota CLI applies |

### Phase 2: Quotas

**Status:** Not checked. No subscription or current usage has been confirmed.
AKS vCPU, ACR, Key Vault, and public IP quotas **must** be checked with azure-quotas
when those modules are implemented in development Phase 7, against the confirmed subscription and region.

---

## 7. Execution Checklist

### Phase 1: Planning
- [x] Analyze workspace
- [x] Gather requirements (AKS, in-cluster Redis, local state, scaffold scope)
- [x] Select recipe (AZD + Terraform)
- [x] Plan architecture
- [x] Prepare resource inventory
- [ ] **User approved this plan**

### Phase 2: Execution (scaffold)
- [ ] Add `azure.yaml` (terraform provider; backend/frontend `host: aks` with Kustomize)
- [ ] Add `infra/` provider, variables, `main.tfvars.json`, resource group, azd outputs
- [ ] Reserve `infra/modules/{network,aks,acr,monitoring,keyvault}`
- [ ] Update `.gitignore` for azd environments and Terraform working files
- [ ] Update README and design docs
- [ ] Validate in WSL: `terraform fmt -check`, `terraform init -backend=false`, `terraform validate`
- [ ] Update status to "Ready for Validation" only when real AKS resources and Dockerfiles exist (Phase 7)

### Phase 3–4: Validation and Deployment
- Deferred to development Phase 7 (requires Dockerfiles, manifests, and AKS/ACR modules).

---

## 8. Validation Proof

| Check | Command Run | Result | Timestamp |
|-------|-------------|--------|-----------|
| _Not yet run_ | | | |

---

## 9. Files to Generate

| File | Purpose | Status |
|------|---------|--------|
| `.azure/deployment-plan.md` | This plan | ✅ |
| `azure.yaml` | azd project configuration | ⏳ |
| `infra/provider.tf` | Terraform + azurerm provider (local state) | ⏳ |
| `infra/variables.tf` | azd-supplied inputs | ⏳ |
| `infra/main.tfvars.json` | azd `${VAR}` substitution | ⏳ |
| `infra/main.tf` | Resource group, tags, module composition point | ⏳ |
| `infra/outputs.tf` | UPPERCASE outputs written to azd env | ⏳ |
| `infra/modules/*` | Reserved module directories | ⏳ |
| `infra/README.md` | Usage and azd/AKS contract | ⏳ |

---

## 10. Next Steps

> Current: Planning, awaiting approval

1. Generate scaffold files after approval.
2. Validate Terraform in WSL.
3. Implement AKS, ACR, network, monitoring, and Key Vault modules in development Phase 7.

## 11. Naming convention amendment

Status: Approved and implemented. The original Terraform scaffold used
`rg-${environment_name}`. The user confirmed that `asse3` corresponds to Azure
location `southeastasia3` and selected environment-specific names, for example
`rg-go-realtime-dev-asse3`. Azure's published region list confirms the location:
https://learn.microsoft.com/en-us/azure/reliability/regions-list

Plan: define a location-to-region-code mapping for the confirmed location and
reject unmapped locations rather than silently produce an incorrect suffix.
Use `rg-go-realtime-${environment_name}-${region_code}` for the resource group,
preserving the azd environment tag. Validate the composed name against resource
group rules and length constraints using the existing 64-character environment
limit. Update the azd usage examples and document
that future resources use their Azure abbreviation first and region code last,
adapted to their specific Azure naming restrictions.

This amendment provisions no resources; quota checks are not applicable to the
code-only change. Actual subscription capacity remains unchecked.

Updated the resource-group name, the default Azure location, azd usage examples,
and naming guidance. Terraform formatting, validation, and three mock-provider
naming tests passed in WSL. Existing resources, if any, would require a separate
migration decision; nothing was provisioned or renamed in Azure.
