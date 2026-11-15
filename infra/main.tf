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

locals {
  # Globally unique names stay stable and within ACR/Key Vault length limits.
  suffix = substr(sha256("${var.subscription_id}/${lower(var.environment_name)}"), 0, 10)
  prefix = "quiz-${substr(lower(var.environment_name), 0, 24)}-${local.region_code}"
}

module "network" {
  source              = "./modules/network"
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location
  name                = "vnet-${local.prefix}"
  tags                = local.tags
}

module "monitoring" {
  source              = "./modules/monitoring"
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location
  name                = local.prefix
  tags                = local.tags
}

module "acr" {
  source              = "./modules/acr"
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location
  name                = "acrquiz${local.suffix}${local.region_code}"
  tags                = local.tags
}

module "keyvault" {
  source              = "./modules/keyvault"
  resource_group_name = azurerm_resource_group.main.name
  location            = var.location
  name                = "kv-quiz-${local.suffix}-${local.region_code}"
  tenant_id           = data.azurerm_client_config.current.tenant_id
  tags                = local.tags
}

module "aks" {
  source                     = "./modules/aks"
  resource_group_name        = azurerm_resource_group.main.name
  location                   = var.location
  name                       = "aks-${local.prefix}"
  subnet_id                  = module.network.aks_subnet_id
  log_analytics_workspace_id = module.monitoring.log_analytics_workspace_id
  admin_group_object_ids     = var.aks_admin_group_object_ids
  node_vm_size               = var.node_vm_size
  zones                      = var.availability_zones
  tags                       = local.tags
}

resource "azurerm_role_assignment" "acr_pull" {
  scope                = module.acr.id
  role_definition_name = "AcrPull"
  principal_id         = module.aks.kubelet_object_id
}

resource "azurerm_monitor_data_collection_rule_association" "aks_prometheus" {
  name                    = "prometheus"
  target_resource_id      = module.aks.id
  data_collection_rule_id = module.monitoring.prometheus_rule_id
}
