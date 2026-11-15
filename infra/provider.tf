terraform {
  required_version = ">= 1.9.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 5.14"
    }
  }

  # Local state is used for now. azd stores it under .azure/<environment>/.
  # Add an azurerm backend before the environment is shared or used in CI.
}

provider "azurerm" {
  features {}
  subscription_id = var.subscription_id
}
