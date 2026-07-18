kube_context = "kind-obs-lab" # same lab cluster; a real prod would target its own

# Prod: persistent storage, HA replicas, long retention, larger footprint.
persistence_enabled  = true
replicas             = 2
image_tag            = "dev"
prometheus_retention = "15d"

# NOTE: real prod would inject these from a secret manager / CI, not a file.
auth_token             = "prod-change-me"
grafana_admin_password = "prod-change-me"

app_resources = {
  requests = { cpu = "50m", memory = "64Mi" }
  limits   = { cpu = "500m", memory = "256Mi" }
}
pg_resources = {
  requests = { cpu = "250m", memory = "256Mi" }
  limits   = { cpu = "1", memory = "1Gi" }
}
mq_resources = {
  requests = { cpu = "250m", memory = "256Mi" }
  limits   = { cpu = "1", memory = "1Gi" }
}
prometheus_resources = {
  requests = { cpu = "250m", memory = "512Mi" }
  limits   = { cpu = "2", memory = "2Gi" }
}
