# Prod environment: HA replicas, persistent storage, long retention.
# Composes the shared modules; env-specific knobs come from terraform.tfvars.
locals {
  app_namespace    = "obs"
  mon_namespace    = "monitoring"
  chart_path       = abspath("${path.root}/../../../helm/app")
  datastores_chart = abspath("${path.root}/../../../helm/datastores")
}

module "datastores" {
  source              = "../../modules/datastores"
  namespace           = local.app_namespace
  chart_path          = local.datastores_chart
  persistence_enabled = var.persistence_enabled
  pg_resources        = var.pg_resources
  mq_resources        = var.mq_resources
}

module "observability" {
  source                 = "../../modules/observability"
  namespace              = local.mon_namespace
  grafana_admin_password = var.grafana_admin_password
  grafana_nodeport       = 30030
  prometheus_retention   = var.prometheus_retention
  prometheus_resources   = var.prometheus_resources
  persistence_enabled    = var.persistence_enabled
}

module "app" {
  source        = "../../modules/app"
  namespace     = local.app_namespace
  chart_path    = local.chart_path
  env_name      = "prod"
  auth_token    = var.auth_token
  image_tag     = var.image_tag
  replicas      = var.replicas
  resources     = var.app_resources
  postgres_dsn  = "postgres://obs:obs@obs-postgres.${local.app_namespace}.svc.cluster.local:5432/obs?sslmode=disable"
  amqp_url      = "amqp://obs:obs@obs-rabbitmq.${local.app_namespace}.svc.cluster.local:5672/"
  otlp_endpoint = "obs-tempo.${local.mon_namespace}.svc.cluster.local:4318"

  # Data stores and Tempo must exist before the app tries to reach them.
  depends_on = [module.datastores, module.observability]
}
