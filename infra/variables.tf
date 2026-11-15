variable "environment_name" {
  type        = string
  description = "azd environment name (AZURE_ENV_NAME); used in resource names and the azd-env-name tag."

  validation {
    condition     = can(regex("^[a-zA-Z0-9-]{1,64}$", var.environment_name))
    error_message = "environment_name must be 1-64 characters: letters, numbers, and hyphens."
  }
}

variable "location" {
  type        = string
  description = "Azure region (AZURE_LOCATION)."
  default     = "southeastasia3"

  validation {
    condition     = contains(["southeastasia3"], lower(var.location))
    error_message = "location must have a region-code mapping in infra/main.tf; currently supported: southeastasia3."
  }
}

variable "subscription_id" {
  type        = string
  description = "Target Azure subscription ID (AZURE_SUBSCRIPTION_ID)."
}
