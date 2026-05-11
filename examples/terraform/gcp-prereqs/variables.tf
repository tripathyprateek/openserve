variable "gcp_project" {
  description = "GCP project ID where openserve will be deployed"
  type        = string
}

variable "region" {
  description = "GCP region for resources (e.g., us-central1, us-west1, europe-west1)"
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "GCP zone within the region (e.g., us-central1-a)"
  type        = string
  default     = "us-central1-a"
}

variable "subnet_cidr" {
  description = "CIDR range for the primary subnet (cluster nodes)"
  type        = string
  default     = "10.10.0.0/20"
}

variable "postgres_tier" {
  description = "Cloud SQL machine type tier. Use db-g1-small for dev, db-custom-2-7680 for production."
  type        = string
  default     = "db-g1-small"
}

variable "redis_memory_gb" {
  description = "Memorystore Redis memory size in GB. Use 1 for dev, 4+ for production."
  type        = number
  default     = 1
}

variable "gpu_spot" {
  description = "Use spot GPU nodes for cost savings (~60% cheaper) at risk of eviction. Set false for production workloads."
  type        = bool
  default     = true
}

variable "enable_a100_node_pool" {
  description = "Enable the A100-40G GPU node pool. Only set true when you need 30B+ model inference."
  type        = bool
  default     = false
}
