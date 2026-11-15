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
