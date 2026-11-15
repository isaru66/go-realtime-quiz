# azd writes these UPPERCASE outputs to .azure/<environment>/.env.

output "AZURE_LOCATION" {
  value = azurerm_resource_group.main.location
}

output "AZURE_TENANT_ID" {
  value = data.azurerm_client_config.current.tenant_id
}

output "AZURE_RESOURCE_GROUP" {
  value = azurerm_resource_group.main.name
}

output "AZURE_AKS_CLUSTER_NAME" {
  value = module.aks.name
}

output "AZURE_CONTAINER_REGISTRY_ENDPOINT" {
  value = module.acr.login_server
}

output "AZURE_CONTAINER_REGISTRY_NAME" {
  value = module.acr.name
}

output "AZURE_KEY_VAULT_NAME" {
  value = module.keyvault.name
}

output "AZURE_LOG_ANALYTICS_WORKSPACE_ID" {
  value = module.monitoring.log_analytics_workspace_id
}

output "AZURE_PROMETHEUS_WORKSPACE_ID" {
  value = module.monitoring.prometheus_workspace_id
}
