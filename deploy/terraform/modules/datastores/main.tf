# PostgreSQL and RabbitMQ. Bitnami's free chart distribution was sunset, so this
# uses a small self-contained in-repo chart built on the stock upstream images
# (the same ones the local docker-compose stack uses). Service names obs-postgres
# and obs-rabbitmq match the DSNs the app is configured with.
terraform {
  required_providers {
    helm = { source = "hashicorp/helm" }
  }
}

resource "helm_release" "datastores" {
  name             = "datastores"
  namespace        = var.namespace
  create_namespace = true
  chart            = var.chart_path
  timeout          = 600
  wait             = true

  values = [yamlencode({
    postgres = {
      user        = var.pg_user
      password    = var.pg_password
      database    = var.pg_database
      persistence = { enabled = var.persistence_enabled, size = var.pg_storage_size }
      resources   = var.pg_resources
    }
    rabbitmq = {
      user        = var.mq_user
      password    = var.mq_password
      persistence = { enabled = var.persistence_enabled, size = var.mq_storage_size }
      resources   = var.mq_resources
    }
  })]
}
