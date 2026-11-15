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

variable "aks_admin_group_object_ids" {
  type        = list(string)
  description = "Microsoft Entra group object IDs authorized to administer AKS; assign before production deployment."
  default     = []
}

variable "node_vm_size" {
  type        = string
  description = "AKS node SKU; confirm quota and availability in southeastasia3 before provisioning."
  default     = "Standard_D2s_v5"
}

variable "availability_zones" {
  type        = list(string)
  description = "AKS availability zones supported by the selected region and VM SKU."
  default     = ["1", "2", "3"]
}
