variable "resource_group_name" { type = string }
variable "location" { type = string }
variable "name" { type = string }
variable "tags" { type = map(string) }

resource "azurerm_container_registry" "main" {
  name                = var.name
  resource_group_name = var.resource_group_name
  location            = var.location
  sku                 = "Standard"
  admin_enabled       = false
  tags                = var.tags
}

output "id" { value = azurerm_container_registry.main.id }
output "name" { value = azurerm_container_registry.main.name }
output "login_server" { value = azurerm_container_registry.main.login_server }
