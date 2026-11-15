# Azure infrastructure (Terraform + azd)

`azure.yaml` sets `infra.provider: terraform` and `infra.path: ./infra`, so azd
runs this Terraform for `azd provision` and `azd up`.

## Current scope

This is a scaffold. It provisions only a resource group tagged with
`azd-env-name`. The AKS, container registry, network, monitoring, and Key Vault
modules are reserved under `modules/` and will be implemented in Phase 7.

```text
infra/
|-- provider.tf          # Terraform and azurerm versions; local state
|-- variables.tf         # Inputs supplied by azd
|-- main.tfvars.json     # azd ${VAR} substitution -> Terraform variables
|-- main.tf              # Resource group and module composition
|-- outputs.tf           # UPPERCASE outputs written to the azd environment
`-- modules/
    |-- network/         # VNet and subnets for AKS and ingress
    |-- aks/             # Cluster, node pools, workload identity, ingress
    |-- acr/             # Container registry and AcrPull for the kubelet
    |-- monitoring/      # Log Analytics and managed Prometheus
    `-- keyvault/        # Secrets for Redis/admin credentials (never ConfigMaps)
```

## Usage

Run azd and Terraform from **WSL (Ubuntu)**, never from Windows directly. This
keeps local runs consistent with the Ubuntu GitHub Actions runners that will
run Terraform in CI. azd invokes the `terraform` executable on the host where
azd runs, so install both `azd` and `terraform` inside WSL.

```bash
azd auth login
azd env new dev
azd env set AZURE_LOCATION southeastasia3
azd provision          # terraform init/plan/apply in ./infra
```

azd sets `AZURE_ENV_NAME`, `AZURE_LOCATION`, and `AZURE_SUBSCRIPTION_ID`, then
substitutes them into `main.tfvars.json`. Use `${VAR}` syntax there, not
`{{ .Env.* }}`. For other inputs, define a variable and either add it to
`main.tfvars.json` or set it with `azd env set TF_VAR_<name> <value>`.

## Naming

Start Azure resource names with their resource-type abbreviation and end them
with the region code. The resource group uses
`rg-go-realtime-<environment>-<region-code>`; for environment `dev` in
`southeastasia3`, this is `rg-go-realtime-dev-asse3`. The environment component
keeps different azd environments distinct. Resource names use lowercase.

The currently supported mapping is `southeastasia3` to `asse3`. To use another
Azure region, add its agreed code to `region_codes` in `main.tf` and add the
location to the validation in `variables.tf`. Unknown locations are rejected
instead of receiving the wrong suffix. Future modules should follow the same
ordering, adapted to each resource's character and length restrictions.

Changing the name of an existing Terraform-managed resource group may require
replacement or migration. Review a Terraform plan before applying such changes.

Terraform state is local under `.azure/<env>/infra/`, which Git ignores. Before
the environment is shared or used in CI, add an `azurerm` backend in
`provider.tf`.

To validate without Azure access:

```bash
cd infra
terraform fmt -check -recursive
terraform init -backend=false
terraform validate
```

## azd contract for AKS services (Phase 7)

Before `azd deploy` can work, the Terraform must add these outputs:

| Output | Purpose |
| --- | --- |
| `AZURE_AKS_CLUSTER_NAME` | Cluster that azd deploys to |
| `AZURE_CONTAINER_REGISTRY_ENDPOINT` | Registry where azd pushes service images |

The kubelet identity also needs `AcrPull` on the registry. The services also
need `src/backend/Dockerfile`, `src/frontend/Dockerfile`, and the Kustomize
overlay at `deploy/kubernetes/overlays/development`. Redis Cluster runs inside
AKS, so no Azure Redis resource is planned.

The `.terraform.lock.hcl` pins provider hashes for linux_amd64 (GitHub
Actions `ubuntu-latest`), linux_arm64 (WSL on Arm), darwin_arm64, and
windows_amd64. After changing the provider version, run `terraform providers
lock` again with those platforms.
