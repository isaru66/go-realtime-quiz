variable "resource_group_name" { type = string }
variable "location" { type = string }
variable "name" { type = string }
variable "tags" { type = map(string) }
variable "subnet_id" { type = string }
variable "log_analytics_workspace_id" { type = string }
variable "admin_group_object_ids" { type = list(string) }
variable "node_vm_size" { type = string }
variable "zones" { type = list(string) }

resource "azurerm_user_assigned_identity" "control_plane" {
  name                = "id-${var.name}"
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = var.tags
}

resource "azurerm_role_assignment" "network" {
  scope                = var.subnet_id
  role_definition_name = "Network Contributor"
  principal_id         = azurerm_user_assigned_identity.control_plane.principal_id
}

resource "azurerm_user_assigned_identity" "kubelet" {
  name                = "id-kubelet-${var.name}"
  resource_group_name = var.resource_group_name
  location            = var.location
  tags                = var.tags
}

resource "azurerm_role_assignment" "kubelet_identity_operator" {
  scope                = azurerm_user_assigned_identity.kubelet.id
  role_definition_name = "Managed Identity Operator"
  principal_id         = azurerm_user_assigned_identity.control_plane.principal_id
}

resource "azurerm_kubernetes_cluster" "main" {
  name                              = var.name
  resource_group_name               = var.resource_group_name
  location                          = var.location
  dns_prefix                        = var.name
  sku_tier                          = "Standard"
  oidc_issuer_enabled               = true
  workload_identity_enabled         = true
  local_account_disabled            = true
  role_based_access_control_enabled = true
  node_provisioning_profile {
    mode = "Manual"
  }
  azure_active_directory_role_based_access_control {
    azure_rbac_enabled     = true
    admin_group_object_ids = var.admin_group_object_ids
  }
  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.control_plane.id]
  }
  kubelet_identity {
    client_id                 = azurerm_user_assigned_identity.kubelet.client_id
    object_id                 = azurerm_user_assigned_identity.kubelet.principal_id
    user_assigned_identity_id = azurerm_user_assigned_identity.kubelet.id
  }
  default_node_pool {
    name                 = "system"
    vm_size              = var.node_vm_size
    vnet_subnet_id       = var.subnet_id
    zones                = var.zones
    auto_scaling_enabled = true
    min_count            = 3
    max_count            = 6
    node_count           = 3
    os_disk_size_gb      = 64
    upgrade_settings {
      max_surge = "1"
    }
  }
  network_profile {
    network_plugin      = "azure"
    network_plugin_mode = "overlay"
    network_data_plane  = "cilium"
    network_policy      = "cilium"
    pod_cidr            = "10.244.0.0/16"
    service_cidr        = "10.41.0.0/16"
    dns_service_ip      = "10.41.0.10"
    load_balancer_sku   = "standard"
  }
  oms_agent {
    log_analytics_workspace_id = var.log_analytics_workspace_id
  }
  monitor_metrics {}
  key_vault_secrets_provider {
    secret_rotation_enabled = true
  }
  tags       = var.tags
  depends_on = [azurerm_role_assignment.network, azurerm_role_assignment.kubelet_identity_operator]
  lifecycle {
    ignore_changes = [default_node_pool[0].node_count]
  }
}

output "id" { value = azurerm_kubernetes_cluster.main.id }
output "name" { value = azurerm_kubernetes_cluster.main.name }
output "kubelet_object_id" { value = azurerm_user_assigned_identity.kubelet.principal_id }
