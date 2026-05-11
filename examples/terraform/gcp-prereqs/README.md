# OpenServe GCP Prerequisites

This Terraform module creates all GCP infrastructure required to run openserve in your own GCP project (BYOC deployment). It provisions:

- **GKE cluster** with Workload Identity enabled
- **Cloud SQL** (PostgreSQL 15) for state
- **Memorystore Redis** for caching
- **Cloud Storage bucket** for model weights
- **BigQuery dataset** for usage metering
- **KMS encryption** for CMEK support
- **VPC network** with private IP ranges and Cloud NAT for outbound traffic
- **Service accounts** with IAM bindings for secure pod-to-GCP authentication

## Prerequisites

Install the required tools:

```bash
# Install gcloud CLI
# https://cloud.google.com/sdk/docs/install

# Install Terraform
# https://www.terraform.io/downloads

# Install kubectl
# https://kubernetes.io/docs/tasks/tools/

# Install Helm
# https://helm.sh/docs/intro/install/
```

Enable billing on your GCP project and ensure you have the Editor role (or equivalent permissions).

## Quick Start

1. Copy the example tfvars file:
```bash
cp terraform.tfvars.example terraform.tfvars
```

2. Edit `terraform.tfvars` with your GCP project ID and desired settings:
```hcl
gcp_project     = "my-gcp-project-id"
region          = "us-central1"
postgres_tier   = "db-g1-small"   # Change to db-custom-2-7680 for production
redis_memory_gb = 1               # Change to 4+ for production
gpu_spot        = true            # Set false for production workloads
```

3. Initialize and apply:
```bash
terraform init
terraform apply
```

4. Retrieve your kubeconfig:
```bash
gcloud container clusters get-credentials openserve \
  --region us-central1 \
  --project my-gcp-project-id
```

5. Get the Helm values snippet:
```bash
terraform output helm_values_snippet
```

## Installation with Helm

Once Terraform has finished, use the output `helm_values_snippet` to install openserve:

```bash
helm install openserve ./openserve-chart \
  --namespace openserve --create-namespace \
  --set clusterName=$(terraform output -raw gke_cluster_name) \
  --set gcpProject=$(terraform output -raw gcp_project) \
  [... additional flags from terraform output helm_values_snippet ...]
```

Or save the snippet and source it:
```bash
terraform output helm_values_snippet > /tmp/helm_values.txt
# Copy and paste the helm install command from the output
```

## Important Notes

### Cloud SQL Deletion Protection

The Cloud SQL instance has `deletion_protection = true` to prevent accidental deletion of your database. To destroy it:

```hcl
# Edit main.tf:
deletion_protection = false

# Then destroy
terraform destroy
```

### Private Endpoints

- **Cloud SQL** is accessible only via private IP within the VPC
- **Redis** is accessible only via private IP within the VPC
- **GKE nodes** cannot access the internet directly; use Cloud NAT for outbound traffic

To access these from your local machine, use `gcloud sql connect` or set up a bastion host.

### Cost Optimization

- **System nodes** are always on (e2-standard-4): ~$30/month
- **Cloud SQL** (db-g1-small): ~$30/month
- **Redis** (1GB BASIC): ~$10/month
- **GPU nodes** (L4): $0.35/GPU-hour on-demand, ~$0.15/GPU-hour on spot

At idle (no GPU nodes), expect ~$70-80/month.

### Production Checklist

For production deployments:

- [ ] Change `postgres_tier` to `db-custom-2-7680` or higher
- [ ] Change `redis_memory_gb` to 4 or more
- [ ] Set `gpu_spot = false` for stable workloads
- [ ] Update `master_authorized_networks_config` in main.tf to restrict API server access (currently `0.0.0.0/0`)
- [ ] Enable `STANDARD_HA` for Redis instead of `BASIC`
- [ ] Enable regional availability for Cloud SQL (currently `ZONAL`)
- [ ] Set up monitoring and alerting via Cloud Monitoring

## Troubleshooting

### API not enabled errors
Terraform will automatically enable required APIs. If you see quota errors, wait a few minutes and retry.

### Private IP connectivity issues
Ensure the VPC peering connection is active:
```bash
gcloud services-networking peering-services-connections list
```

### Workload Identity debugging
```bash
# Check SA binding
gcloud iam service-accounts get-iam-policy openserve-operator

# Verify pod can assume role
kubectl describe sa openserve-operator -n openserve
```

## Cleanup

To destroy all infrastructure:

```bash
# Set deletion_protection = false in main.tf for Cloud SQL
terraform destroy
```

This will delete:
- GKE cluster and all workloads
- Cloud SQL database (if deletion_protection=false)
- Redis cache
- GCS bucket (if force_destroy=false, objects must be deleted first)
- BigQuery dataset
- All VPC networking
- All service accounts and IAM bindings
