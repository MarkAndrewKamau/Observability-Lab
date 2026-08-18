# The observability backends: kube-prometheus-stack (Prometheus + Grafana +
# Alertmanager), Grafana Tempo (traces) and Loki (operational logs). Grafana is
# pre-wired with Tempo and Loki datasources so all three pillars land in one UI.
terraform {
  required_providers {
    helm       = { source = "hashicorp/helm" }
    kubernetes = { source = "hashicorp/kubernetes" }
  }
}

# Provisioned Grafana dashboard: the kube-prometheus-stack Grafana sidecar
# imports any ConfigMap in its namespace labelled grafana_dashboard=1, so the
# dashboard is version-controlled and reproducible (no click-ops).
resource "kubernetes_config_map_v1" "dashboard_obs_lab" {
  metadata {
    name      = "obs-lab-dashboard"
    namespace = var.namespace
    labels    = { grafana_dashboard = "1" }
  }
  data = {
    "obs-lab.json" = file("${path.module}/dashboards/obs-lab.json")
  }
  depends_on = [helm_release.kube_prometheus_stack]
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

# Fluent Bit DaemonSet (Phase 7): tails every container's stdout, parses the
# structured JSON logs and labels them by the "stream" field each service
# stamps on every line — operational logs go to Loki now; Phase 8 swaps the
# security stream's sink to Wazuh. The $stream/$service record accessors in
# the Labels setting promote those JSON fields to Loki stream labels.
resource "helm_release" "fluent_bit" {
  name             = "fluent-bit"
  namespace        = var.namespace
  create_namespace = true
  repository       = "https://fluent.github.io/helm-charts"
  chart            = "fluent-bit"
  version          = "0.58.1"
  timeout          = 600
  depends_on       = [helm_release.loki]

  values = [yamlencode({
    kind = "DaemonSet"
    # Also collect logs from the kind control-plane node.
    tolerations = [{
      key      = "node-role.kubernetes.io/control-plane"
      operator = "Exists"
      effect   = "NoSchedule"
    }]
    resources = var.fluent_bit_resources

    config = {
      # Tail all container stdout files. inotify_watcher Off: kind nodes cap
      # fs.inotify.max_user_instances at 128 per UID (shared with kubelet and
      # containerd-shim), and tail's inotify mode exhausts it -> EMFILE crash
      # at startup ("Too many open files"). Stat polling is fine for a lab.
      # parser cri-log-key strips the containerd framing (timestamp stream
      # flag) so the kubernetes filter can JSON-parse the line (Merge_Log).
      inputs = <<-EOT
        [INPUT]
            Name tail
            Path /var/log/containers/*.log
            Tag kube.*
            parser cri-log-key
            inotify_watcher Off
            Skip_Long_Lines On
            Refresh_Interval 5
      EOT
      filters = <<-EOT
        [FILTER]
            Name kubernetes
            Match kube.*
            Merge_Log On
            Keep_Log Off
            K8S-Logging.Parser On
            K8S-Logging.Exclude On
      EOT
      outputs = <<-EOT
        [OUTPUT]
            Name loki
            Match *
            Host loki-gateway.${var.namespace}.svc.cluster.local
            Port 80
            Labels job=fluentbit, stream=$stream, service=$service
            Line_Format json
            Retry_Limit 3
      EOT
      # Fluent Bit 5 renamed the CRI content key to "message", which the
      # kubernetes filter's Merge_Log (expects "log") never sees; the image's
      # builtin cri parser also shadows our "stream" field. Define a parser
      # that emits "log" (name must differ from the builtin "cri" or the
      # duplicate is fatal in v5).
      customParsers = <<-EOT
        [PARSER]
            Name cri-log-key
            Format regex
            Regex ^(?<time>[^ ]+) (?:stdout|stderr) (?:F|P) (?<log>.*)$
            Time_Key time
            Time_Format %Y-%m-%dT%H:%M:%S.%L%z
            Time_Keep On
      EOT
    }
  })]
}
