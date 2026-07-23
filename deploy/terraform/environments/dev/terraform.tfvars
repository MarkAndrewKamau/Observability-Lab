kube_context = "kind-obs-lab"

# Dev: ephemeral, small, short retention.
persistence_enabled  = false
replicas             = 1
image_tag            = "dev"
prometheus_retention = "6h"

# Metrics phase: scrape our services and run a synthetic load generator.
service_monitor_enabled = true
loadgen_enabled         = true

app_resources = {
  requests = { cpu = "25m", memory = "32Mi" }
  limits   = { cpu = "250m", memory = "128Mi" }
}
pg_resources = {
  requests = { cpu = "50m", memory = "128Mi" }
  limits   = { cpu = "500m", memory = "256Mi" }
}
mq_resources = {
  requests = { cpu = "50m", memory = "128Mi" }
  limits   = { cpu = "500m", memory = "512Mi" }
}
prometheus_resources = {
  requests = { cpu = "100m", memory = "256Mi" }
  limits   = { cpu = "1", memory = "1Gi" }
}
