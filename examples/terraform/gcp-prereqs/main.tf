# Estimated monthly cost at idle: ~$80 (Cloud SQL db-g1-small + Redis 1GB + GKE system nodes). GPU nodes are pay-per-use.

terraform {
  required_version = ">= 1.6"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

provider "google" {
  project = var.gcp_project
  region  = var.region
}

# Random suffix for unique resource naming
resource "random_id" "suffix" {
  byte_length = 4
}

# ==============================================================================
# Enable Required APIs
# ==============================================================================

# GKE API
resource "google_project_service" "gke" {
  service = "container.googleapis.com"
  # Keep resource alive even if not referenced by other resources
  disable_on_destroy = false
}

# Cloud SQL API
resource "google_project_service" "cloudsql" {
  service            = "sqladmin.googleapis.com"
  disable_on_destroy = false
}

# Memorystore Redis API
resource "google_project_service" "redis" {
  service            = "redis.googleapis.com"
  disable_on_destroy = false
}

# Cloud Storage API
resource "google_project_service" "storage" {
  service            = "storage.googleapis.com"
  disable_on_destroy = false
}

# BigQuery API
resource "google_project_service" "bigquery" {
  service            = "bigquery.googleapis.com"
  disable_on_destroy = false
}

# Secret Manager API
resource "google_project_service" "secretmanager" {
  service            = "secretmanager.googleapis.com"
  disable_on_destroy = false
}

# Cloud KMS API
resource "google_project_service" "cloudkms" {
  service            = "cloudkms.googleapis.com"
  disable_on_destroy = false
}

# IAM Credentials API (required for Workload Identity)
resource "google_project_service" "iamcredentials" {
  service            = "iamcredentials.googleapis.com"
  disable_on_destroy = false
}

# Cloud Monitoring API
resource "google_project_service" "monitoring" {
  service            = "monitoring.googleapis.com"
  disable_on_destroy = false
}

# Cloud Logging API
resource "google_project_service" "logging" {
  service            = "logging.googleapis.com"
  disable_on_destroy = false
}

# Service Networking API (required for private IP ranges for Cloud SQL/Redis)
resource "google_project_service" "servicenetworking" {
  service            = "servicenetworking.googleapis.com"
  disable_on_destroy = false
}

# ==============================================================================
# VPC Network
# ==============================================================================

resource "google_compute_network" "vpc" {
  name                    = "openserve-vpc"
  auto_create_subnetworks = false
  description             = "VPC network for openserve cluster"

  depends_on = [
    google_project_service.gke,
  ]
}

# ==============================================================================
# Subnet with Secondary IP Ranges
# ==============================================================================

resource "google_compute_subnetwork" "subnet" {
  name          = "openserve-subnet"
  ip_cidr_range = var.subnet_cidr
  region        = var.region
  network       = google_compute_network.vpc.id
  description   = "Subnet for openserve cluster"

  # Enable Private Google Access for Cloud SQL/Redis connectivity
  private_ip_google_access = true

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = "10.20.0.0/16"
  }

  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = "10.30.0.0/20"
  }
}

# ==============================================================================
# Private IP Range for Cloud SQL / Redis (VPC Peering)
# ==============================================================================

resource "google_compute_global_address" "private_ip_range" {
  name          = "openserve-private-ip-range"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  address       = "10.40.0.0"
  network       = google_compute_network.vpc.id

  depends_on = [
    google_project_service.servicenetworking,
  ]
}

# VPC peering connection for private services (Cloud SQL, Redis)
resource "google_service_networking_connection" "private_vpc_connection" {
  network                 = google_compute_network.vpc.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip_range.name]

  depends_on = [
    google_project_service.servicenetworking,
  ]
}

# ==============================================================================
# Cloud Router and NAT for Outbound Traffic
# ==============================================================================

resource "google_compute_router" "router" {
  name    = "openserve-router"
  region  = var.region
  network = google_compute_network.vpc.id

  depends_on = [
    google_project_service.gke,
  ]
}

resource "google_compute_router_nat" "nat" {
  name                               = "openserve-nat"
  router                             = google_compute_router.router.name
  region                             = google_compute_router.router.region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"

  log_config {
    enable = false
  }
}

# ==============================================================================
# GKE Cluster
# ==============================================================================

resource "google_container_cluster" "primary" {
  name            = "openserve"
  region          = var.region
  description     = "GKE cluster for openserve BYOC deployment"
  network         = google_compute_network.vpc.name
  subnetwork      = google_compute_subnetwork.subnet.name
  networking_mode = "VPC_NATIVE"

  remove_default_node_pool = true
  initial_node_count       = 1

  # VPC-native cluster IP allocation
  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }

  # Workload Identity configuration for pod-to-SA binding
  workload_identity_config {
    workload_pool = "${var.gcp_project}.svc.id.goog"
  }

  # Private cluster: nodes on private IPs only
  private_cluster_config {
    enable_private_nodes    = true
    enable_private_endpoint = false
    master_ipv4_cidr_block  = "172.16.0.32/28"
  }

  # Master authorized networks: allow all IPs (TODO: restrict in production)
  master_authorized_networks_config {
    cidr_blocks {
      cidr_block   = "0.0.0.0/0"
      display_name = "All"
    }
  }

  # Enable CSI drivers for persistent storage
  addons_config {
    gcs_fuse_csi_driver_config {
      enabled = true
    }
    gce_persistent_disk_csi_driver_config {
      enabled = true
    }
  }

  # Release channel: keep cluster auto-updated
  release_channel {
    channel = "REGULAR"
  }

  depends_on = [
    google_project_service.gke,
    google_service_networking_connection.private_vpc_connection,
  ]
}

# ==============================================================================
# Node Pools
# ==============================================================================

# System node pool (CPU): stable, not spot, runs cluster components
resource "google_container_node_pool" "system" {
  name       = "system"
  cluster    = google_container_cluster.primary.id
  node_count = null

  autoscaling {
    min_node_count = 1
    max_node_count = 3
  }

  node_config {
    machine_type = "e2-standard-4"
    disk_size_gb = 50
    disk_type    = "pd-ssd"

    spot = false

    workload_metadata_config {
      mode = "GKE_METADATA"
    }

    labels = {
      role = "system"
    }

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform",
    ]
  }
}

# L4 GPU node pool: for small-to-medium models, prefers spot instances for cost
resource "google_container_node_pool" "gpu_l4" {
  name       = "gpu-l4"
  cluster    = google_container_cluster.primary.id
  node_count = null

  autoscaling {
    min_node_count     = 0
    max_node_count     = 10
    location_policy    = "BALANCED"
  }

  node_config {
    machine_type = "g2-standard-24"
    disk_size_gb = 50
    disk_type    = "pd-ssd"

    spot = var.gpu_spot

    guest_accelerators {
      type              = "nvidia-l4"
      count             = 1
      gpu_driver_config {
        gpu_driver_version = "LATEST"
      }
    }

    workload_metadata_config {
      mode = "GKE_METADATA"
    }

    labels = {
      role                             = "gpu-l4"
      "cloud.google.com/gke-accelerator" = "nvidia-l4"
    }

    taint {
      key    = "nvidia.com/gpu"
      value  = "present"
      effect = "NO_SCHEDULE"
    }

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform",
    ]
  }
}

# ==============================================================================
# KMS Keyring and Crypto Key for CMEK
# ==============================================================================

resource "google_kms_key_ring" "openserve" {
  name     = "openserve"
  location = "global"

  depends_on = [
    google_project_service.cloudkms,
  ]
}

resource "google_kms_crypto_key" "model_cache" {
  name            = "model-cache-key"
  key_ring        = google_kms_key_ring.openserve.id
  rotation_period = "7776000s" # 90 days
  purpose         = "ENCRYPT_DECRYPT"
}

# ==============================================================================
# Cloud Storage Bucket (Model Weight Cache)
# ==============================================================================

resource "google_storage_bucket" "models" {
  name          = "${var.gcp_project}-openserve-models-${random_id.suffix.hex}"
  location      = var.region
  force_destroy = false
  description   = "Model weight cache for openserve inference"

  uniform_bucket_level_access = true

  versioning {
    enabled = true
  }

  encryption {
    default_kms_key_name = google_kms_crypto_key.model_cache.id
  }

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      age_days             = 90
      is_live              = false # Delete non-current versions
    }
  }

  depends_on = [
    google_project_service.storage,
    google_kms_crypto_key.model_cache,
  ]
}

# ==============================================================================
# Cloud SQL (Postgres)
# ==============================================================================

resource "google_sql_database_instance" "postgres" {
  name              = "openserve-postgres-${random_id.suffix.hex}"
  database_version  = "POSTGRES_15"
  region            = var.region
  deletion_protection = true
  description       = "PostgreSQL database for openserve state"

  settings {
    tier              = var.postgres_tier
    availability_type = "ZONAL" # REGIONAL for HA in production

    ipv4_enabled         = false
    private_network      = google_compute_network.vpc.id

    backup_configuration {
      enabled                        = true
      start_time                     = "03:00"
      transaction_log_retention_days = 7
      backup_retention_settings {
        retained_backups = 7
      }
    }

    database_flags {
      name  = "max_connections"
      value = "100"
    }
  }

  depends_on = [
    google_service_networking_connection.private_vpc_connection,
  ]
}

resource "google_sql_database" "openserve" {
  name     = "openserve"
  instance = google_sql_database_instance.postgres.name
}

resource "random_password" "postgres_password" {
  length  = 32
  special = true
}

resource "google_sql_user" "openserve" {
  name     = "openserve"
  instance = google_sql_database_instance.postgres.name
  password = random_password.postgres_password.result
}

# ==============================================================================
# Secret Manager Secrets
# ==============================================================================

resource "google_secret_manager_secret" "postgres_password" {
  secret_id = "openserve-postgres-password"
  replication {
    automatic = true
  }

  depends_on = [
    google_project_service.secretmanager,
  ]
}

resource "google_secret_manager_secret_version" "postgres_password" {
  secret      = google_secret_manager_secret.postgres_password.id
  secret_data = random_password.postgres_password.result
}

# ==============================================================================
# Memorystore Redis
# ==============================================================================

resource "google_redis_instance" "cache" {
  name           = "openserve-redis"
  tier           = "BASIC" # Use STANDARD_HA for production
  memory_size_gb = var.redis_memory_gb
  region         = var.region
  redis_version  = "REDIS_7_0"
  display_name   = "Redis cache for openserve"

  connect_mode = "PRIVATE_SERVICE_ACCESS"
  auth_enabled = true

  authorized_network = google_compute_network.vpc.id

  depends_on = [
    google_project_service.redis,
    google_service_networking_connection.private_vpc_connection,
  ]
}

resource "google_secret_manager_secret" "redis_auth_string" {
  secret_id = "openserve-redis-auth-string"
  replication {
    automatic = true
  }

  depends_on = [
    google_project_service.secretmanager,
  ]
}

resource "google_secret_manager_secret_version" "redis_auth_string" {
  secret      = google_secret_manager_secret.redis_auth_string.id
  secret_data = google_redis_instance.cache.auth_string
}

# ==============================================================================
# Service Accounts for Workload Identity
# ==============================================================================

resource "google_service_account" "openserve_operator" {
  account_id   = "openserve-operator"
  display_name = "Service account for openserve operator"
}

resource "google_service_account" "openserve_control_api" {
  account_id   = "openserve-control-api"
  display_name = "Service account for openserve control API"
}

# ==============================================================================
# IAM Bindings for Service Accounts
# ==============================================================================

# Operator: BigQuery, Storage, Secret Manager access
resource "google_project_iam_member" "operator_bigquery" {
  project = var.gcp_project
  role    = "roles/bigquery.dataViewer"
  member  = "serviceAccount:${google_service_account.openserve_operator.email}"
}

resource "google_project_iam_member" "operator_storage" {
  project = var.gcp_project
  role    = "roles/storage.objectViewer"
  member  = "serviceAccount:${google_service_account.openserve_operator.email}"
}

resource "google_project_iam_member" "operator_secretmanager" {
  project = var.gcp_project
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.openserve_operator.email}"
}

# Control API: Secret Manager access
resource "google_project_iam_member" "control_api_secretmanager" {
  project = var.gcp_project
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.openserve_control_api.email}"
}

# ==============================================================================
# Workload Identity Bindings (K8s SA -> GCP SA)
# ==============================================================================

resource "google_service_account_iam_member" "operator_workload_identity" {
  service_account_id = google_service_account.openserve_operator.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.gcp_project}.svc.id.goog[openserve/openserve-operator]"
}

resource "google_service_account_iam_member" "control_api_workload_identity" {
  service_account_id = google_service_account.openserve_control_api.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.gcp_project}.svc.id.goog[openserve/openserve-control-api]"
}

# ==============================================================================
# BigQuery Dataset
# ==============================================================================

resource "google_bigquery_dataset" "usage" {
  dataset_id    = "openserve_usage"
  region        = var.region
  description   = "openserve token usage and cost metering"
  friendly_name = "openserve Usage"

  depends_on = [
    google_project_service.bigquery,
  ]
}
