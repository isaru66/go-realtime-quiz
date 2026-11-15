locals {
  region_codes = {
    southeastasia3 = "asse3"
  }
  region_code = lookup(local.region_codes, lower(var.location), "unknown")

  tags = {
    "azd-env-name" = var.environment_name
    "application"  = "go-realtime-quiz"
  }
}

data "azurerm_client_config" "current" {}

resource "azurerm_resource_group" "main" {
  name     = "rg-go-realtime-${lower(var.environment_name)}-${local.region_code}"
  location = var.location
  tags     = local.tags
}

# Planned for Phase 7. See infra/README.md for the outputs required by azd.
# module "network"    { source = "./modules/network" }
# module "monitoring" { source = "./modules/monitoring" }
# module "acr"        { source = "./modules/acr" }
# module "keyvault"   { source = "./modules/keyvault" }
# module "aks"        { source = "./modules/aks" }
