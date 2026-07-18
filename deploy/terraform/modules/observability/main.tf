# The observability backends: kube-prometheus-stack (Prometheus + Grafana +
# Alertmanager), Grafana Tempo (traces) and Loki (operational logs). Grafana is
# pre-wired with Tempo and Loki datasources so all three pillars land in one UI.
terraform {
  required_providers {
    helm = { source = "hashicorp/helm" }
  }
}

resource "helm_release" "kube_prometheus_stack" {
  name             = "kube-prometheus-stack"
  namespace        = var.namespace
  create_namespace = true
  repository       = "https://prometheus-community.github.io/helm-charts"
  chart            = "kube-prometheus-stack"
  timeout          = 900

  values = [yamlencode({
    grafana = {
      adminPassword = var.grafana_admin_password
      service       = { type = "NodePort", nodePort = var.grafana_nodeport }
      additionalDataSources = [
        {
          name = "Tempo", type = "tempo", uid = "tempo"
          url  = "http://obs-tempo.${var.namespace}.svc.cluster.local:3200"
        },
        {
          name = "Loki", type = "loki", uid = "loki"
          url  = "http://loki-gateway.${var.namespace}.svc.cluster.local:80"
        },
      ]
    }
    prometheus = {
      prometheusSpec = {
        retention = var.prometheus_retention
        resources = var.prometheus_resources
        # Discover ServiceMonitors in any namespace, not only chart-labelled ones.
        serviceMonitorSelectorNilUsesHelmValues = false
        podMonitorSelectorNilUsesHelmValues     = false
        ruleSelectorNilUsesHelmValues           = false
      }
    }
    alertmanager = {
      alertmanagerSpec = { resources = var.alertmanager_resources }
    }
  })]
}

resource "helm_release" "tempo" {
  name             = "tempo"
  namespace        = var.namespace
  create_namespace = true
  repository       = "https://grafana.github.io/helm-charts"
  chart            = "tempo"
  timeout          = 600

  values = [yamlencode({
    fullnameOverride = "obs-tempo"
    tempo = {
      receivers = {
        otlp = {
          protocols = {
            http = { endpoint = "0.0.0.0:4318" }
            grpc = { endpoint = "0.0.0.0:4317" }
          }
        }
      }
    }
    persistence = { enabled = var.persistence_enabled, size = "1Gi" }
  })]
}

resource "helm_release" "loki" {
  name             = "loki"
  namespace        = var.namespace
  create_namespace = true
  repository       = "https://grafana.github.io/helm-charts"
  chart            = "loki"
  timeout          = 900

  values = [yamlencode({
    deploymentMode = "SingleBinary"
    loki = {
      auth_enabled = false
      commonConfig = { replication_factor = 1 }
      storage      = { type = "filesystem" }
      schemaConfig = {
        configs = [{
          from         = "2024-01-01"
          store        = "tsdb"
          object_store = "filesystem"
          schema       = "v13"
          index        = { prefix = "index_", period = "24h" }
        }]
      }
    }
    singleBinary = {
      replicas = 1
      # Loki needs a writable data dir (/var/loki) regardless of env — without a
      # mounted volume it crashes on "mkdir /var/loki: read-only file system".
      persistence = { enabled = true, size = "2Gi" }
    }
    # Disable the scale-out targets and caches for a lean single-binary lab.
    read         = { replicas = 0 }
    write        = { replicas = 0 }
    backend      = { replicas = 0 }
    chunksCache  = { enabled = false }
    resultsCache = { enabled = false }
    lokiCanary   = { enabled = false }
    test         = { enabled = false }
    monitoring   = { selfMonitoring = { enabled = false } }
  })]
}
