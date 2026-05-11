output "gke_cluster_name" {
  description = "Name of the GKE cluster"
  value       = google_container_cluster.primary.name
}

output "gcs_bucket_name" {
  description = "GCS bucket name for model weight cache"
  value       = google_storage_bucket.models.name
}

output "postgres_private_ip" {
  description = "Private IP address of the Cloud SQL instance"
  value       = google_sql_database_instance.postgres.private_ip_address
}

output "postgres_connection_name" {
  description = "Cloud SQL connection string (project:region:instance-name) for JDBC/client connections"
  value       = google_sql_database_instance.postgres.connection_name
}

output "redis_host" {
  description = "Private IP address of the Memorystore Redis instance"
  value       = google_redis_instance.cache.host
}

output "redis_port" {
  description = "Port of the Memorystore Redis instance"
  value       = google_redis_instance.cache.port
}

output "postgres_password_secret_name" {
  description = "Secret Manager secret name storing the Postgres password"
  value       = google_secret_manager_secret.postgres_password.id
}

output "redis_auth_secret_name" {
  description = "Secret Manager secret name storing the Redis auth string"
  value       = google_secret_manager_secret.redis_auth_string.id
}

output "helm_values_snippet" {
  description = "Helm values to pass to 'helm install openserve' command. Use: helm install openserve ./openserve-chart [the values below]"
  value = <<-EOT
# Add these flags to your helm install command:
# helm install openserve ./openserve-chart \
#   --namespace openserve --create-namespace \
#   --set clusterName=${google_container_cluster.primary.name} \
#   --set gcpProject=${var.gcp_project} \
#   --set postgres.host=${google_sql_database_instance.postgres.private_ip_address} \
#   --set postgres.port=5432 \
#   --set postgres.database=openserve \
#   --set postgres.user=openserve \
#   --set postgres.passwordSecret=${google_secret_manager_secret.postgres_password.id} \
#   --set redis.host=${google_redis_instance.cache.host} \
#   --set redis.port=${google_redis_instance.cache.port} \
#   --set redis.authSecret=${google_secret_manager_secret.redis_auth_string.id} \
#   --set gcs.bucketName=${google_storage_bucket.models.name} \
#   --set bigquery.datasetId=${google_bigquery_dataset.usage.dataset_id} \
#   --set operatorServiceAccount=${google_service_account.openserve_operator.email} \
#   --set controlApiServiceAccount=${google_service_account.openserve_control_api.email}
  EOT
}
