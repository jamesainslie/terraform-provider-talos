# Example: Merge Talos cluster kubeconfig with existing ~/.kube/config

# First, get the kubeconfig from the Talos cluster
resource "talos_cluster_kubeconfig" "this" {
  client_configuration = talos_machine_secrets.this.client_configuration
  node                 = var.control_plane_nodes[0]
}

# Merge with existing kubeconfig, reading from ~/.kube/config
data "talos_kubeconfig_merge" "this" {
  kubeconfig_raw       = talos_cluster_kubeconfig.this.kubeconfig_raw
  base_kubeconfig_path = "~/.kube/config"

  # Optional: override the cluster/context/user names
  cluster_name = "my-talos-cluster"

  # Optional: set this cluster as the current context
  set_current = false
}

# Write the merged config using local_file resource
resource "local_file" "kubeconfig" {
  content         = data.talos_kubeconfig_merge.this.merged_kubeconfig_raw
  filename        = pathexpand("~/.kube/config")
  file_permission = "0600"
}

# Optionally save a backup of the original config
resource "local_file" "kubeconfig_backup" {
  count = data.talos_kubeconfig_merge.this.base_kubeconfig_backup != "" ? 1 : 0

  content         = data.talos_kubeconfig_merge.this.base_kubeconfig_backup
  filename        = pathexpand("~/.kube/config.backup")
  file_permission = "0600"
}

# Access the names that were used
output "cluster_name" {
  value = data.talos_kubeconfig_merge.this.cluster_name_out
}

output "context_name" {
  value = data.talos_kubeconfig_merge.this.context_name_out
}

