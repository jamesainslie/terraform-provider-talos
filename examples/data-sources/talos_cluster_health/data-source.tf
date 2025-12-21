# Example: Wait for cluster health before proceeding
data "talos_cluster_health" "example" {
  client_configuration = talos_machine_secrets.example.client_configuration
  endpoints            = ["192.168.1.10", "192.168.1.11", "192.168.1.12"]
  control_plane_nodes  = ["192.168.1.10", "192.168.1.11", "192.168.1.12"]
  worker_nodes         = ["192.168.1.20", "192.168.1.21"]

  timeouts = {
    read = "10m"
  }
}

# Access overall health status
output "cluster_healthy" {
  value = data.talos_cluster_health.example.healthy
}

# Access per-role health
output "control_plane_healthy" {
  value = data.talos_cluster_health.example.control_plane_healthy
}

output "workers_healthy" {
  value = data.talos_cluster_health.example.workers_healthy
}

output "kubernetes_healthy" {
  value = data.talos_cluster_health.example.kubernetes_healthy
}

# Access detailed node health
output "node_status" {
  value = [for node in data.talos_cluster_health.example.nodes : {
    address = node.address
    role    = node.role
    healthy = node.healthy
  }]
}

# Access Kubernetes component health
output "kubernetes_api_server_healthy" {
  value = data.talos_cluster_health.example.kubernetes.api_server.healthy
}

output "etcd_members" {
  value = data.talos_cluster_health.example.kubernetes.etcd.members
}

# Example: Skip Kubernetes checks for faster pre-bootstrap health check
data "talos_cluster_health" "pre_bootstrap" {
  client_configuration   = talos_machine_secrets.example.client_configuration
  endpoints              = ["192.168.1.10"]
  control_plane_nodes    = ["192.168.1.10"]
  skip_kubernetes_checks = true

  timeouts = {
    read = "5m"
  }
}

