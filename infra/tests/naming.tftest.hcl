mock_provider "azurerm" {
  mock_data "azurerm_client_config" {
    defaults = {
      tenant_id = "00000000-0000-0000-0000-000000000001"
    }
  }
}

variables {
  environment_name = "dev"
  subscription_id  = "00000000-0000-0000-0000-000000000000"
}

run "names_resource_group_with_environment_and_region_code" {
  command = plan

  assert {
    condition     = azurerm_resource_group.main.name == "rg-go-realtime-dev-asse3"
    error_message = "The resource group name must include the resource abbreviation, environment, and region code."
  }

  assert {
    condition     = azurerm_resource_group.main.tags["azd-env-name"] == "dev"
    error_message = "The azd environment tag must be preserved."
  }
}

run "normalizes_environment_name" {
  command = plan

  variables {
    environment_name = "Test"
  }

  assert {
    condition     = azurerm_resource_group.main.name == "rg-go-realtime-test-asse3"
    error_message = "The resource group name must use lowercase."
  }
}

run "rejects_unmapped_regions" {
  command = plan

  variables {
    location = "southeastasia"
  }

  expect_failures = [var.location]
}
