# Deploys the obs-lab app umbrella chart (gateway/orders/worker) from the local
# Helm chart. Env-specific scalars come in as variables; the Helm values shape
# lives here so dev and prod stay identical except for the knobs that differ.
terraform {
  required_providers {
    helm = { source = "hashicorp/helm" }
  }
}

resource "helm_release" "app" {
  name             = var.release_name
  namespace        = var.namespace
  create_namespace = true
  chart            = var.chart_path

  values = [yamlencode({
    env       = var.env_name
    logLevel  = var.log_level
    authToken = var.auth_token
    image = {
      repository = var.image_repository
      tag        = var.image_tag
      pullPolicy = "Never"
    }
    postgres = { dsn = var.postgres_dsn }
    amqp     = { url = var.amqp_url, queue = var.amqp_queue }
    otlp     = { endpoint = var.otlp_endpoint }
    services = {
      gateway = { replicas = var.replicas, httpPort = 8080, nodePort = var.gateway_nodeport, resources = var.resources }
      orders  = { replicas = var.replicas, httpPort = 8081, resources = var.resources }
      worker  = { replicas = var.replicas, httpPort = 8082, resources = var.resources }
    }
  })]

  wait    = true
  timeout = 300
}
