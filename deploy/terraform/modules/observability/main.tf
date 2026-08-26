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
  timeout          = 300
  # Don't wait for the DaemonSet to become ready: the security output points
  # at Wazuh, so the pods stay in CrashLoopBackOff until Wazuh exists. Waiting
  # here would deadlock the apply (Wazuh can't deploy until this releases).
  wait       = false
  depends_on = [helm_release.loki]

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
      # Keep_Log On preserves the raw JSON line under "log" (Phase 8: the
      # syslog output ships that raw line to Wazuh). rewrite_tag then splits
      # records by the app's "stream" field: operational/security get their
      # own tags, everything else (fluent-bit itself, infra logs, ...) stays
      # on kube.* and still flows to Loki.
      filters = <<-EOT
        [FILTER]
            Name kubernetes
            Match kube.*
            Merge_Log On
            Keep_Log On
            K8S-Logging.Parser On
            K8S-Logging.Exclude On
        [FILTER]
            Name rewrite_tag
            Match kube.*
            Rule $stream ^operational$ operational false
            Rule $stream ^security$ security false
      EOT
      outputs = <<-EOT
        [OUTPUT]
            Name loki
            Match operational
            Host loki-gateway.${var.namespace}.svc.cluster.local
            Port 80
            Labels job=fluentbit, stream=$stream, service=$service
            Line_Format json
            Retry_Limit 3
        [OUTPUT]
            Name loki
            Match kube.
            Host loki-gateway.${var.namespace}.svc.cluster.local
            Port 80
            Labels job=fluentbit
            Line_Format json
            Retry_Limit 3
        [OUTPUT]
            Name syslog
            Match security
            Host wazuh.${var.namespace}.svc.cluster.local
            Port 514
            Mode udp
            Syslog_Format rfc3164
            Syslog_Message_Key log
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

# cert-manager: the Wazuh chart ships a bundled sub-chart, but the bundled
# copy's CRDs only render when crds.enabled is set, and installing it
# separately lets us guarantee the Certificate/Issuer CRDs exist before
# Wazuh's CRs render. crds.enabled=true emits the CRDs as chart templates;
# startupapicheck (a post-install hook Job that curls the webhook) is flaky
# under kind's constrained resources, so it's disabled.
resource "helm_release" "cert_manager" {
  name             = "cert-manager"
  namespace        = var.namespace
  create_namespace = true
  repository       = "https://charts.jetstack.io"
  chart            = "cert-manager"
  version          = "1.19.3"
  timeout          = 600

  values = [yamlencode({
    crds            = { enabled = true }
    startupapicheck = { enabled = false }
  })]
}

# Wazuh (Phase 8): SIEM stack (manager + indexer + dashboard). A single
# manager master (no workers — this is a lab) listens for the security
# stream Fluent Bit ships on syslog/514, evaluates it against the ruleset
# (including our local_rules.xml rule for the gateway's auth events), and
# forwards alerts to the indexer for the dashboard to visualize.
# Single indexer node, no network policies / PDBs (kind doesn't enforce
# them and they only add friction), small lab-sized storage.
resource "helm_release" "wazuh" {
  name             = "wazuh"
  namespace        = var.namespace
  create_namespace = true
  repository       = "https://morgoved.github.io/wazuh-helm"
  chart            = "wazuh"
  version          = "2.0.4"
  timeout          = 1800
  depends_on       = [helm_release.cert_manager, helm_release.loki]

  values = [yamlencode({
    # cert-manager is deployed separately above; the chart generates its own
    # PKI through it (self-signed issuer).
    cert-manager = { enabled = false }

    indexer = {
      replicas      = 1
      storageSize   = "5Gi"
      networkPolicy = { enabled = false }
      pdb           = { enabled = false }
      # obs-lab-worker2's kube-proxy has been crash-looping for weeks
      # (EMFILE, soft nofile limit 1024), which breaks ClusterIP/DNS from
      # pods on that node; pin the wazuh pods to the healthy worker.
      nodeSelector = { "kubernetes.io/hostname" = "obs-lab-worker" }
      env = {
        # Lean heap: a lab indexer doesn't need the default 1g.
        OPENSEARCH_JAVA_OPTS = "-Xms512m -Xmx512m -Dlog4j2.formatMsgNoLookups=true"
      }
      resources = var.wazuh_indexer_resources
    }

    dashboard = {
      service   = { type = "NodePort", httpPort = 5601, nodePort = var.wazuh_dashboard_nodeport }
      resources = var.wazuh_dashboard_resources
      # OpenSearch Dashboards' default Node heap (1.5g) exceeds the lab-sized
      # memory limit and the pod gets OOM-killed; cap the heap so it fits.
      additionalEnv = [
        { name = "NODE_OPTIONS", value = "--max-old-space-size=512" }
      ]
      nodeSelector = { "kubernetes.io/hostname" = "obs-lab-worker" }
    }

    # No agents: the cluster's own security events arrive via Fluent Bit's
    # syslog output, not via enrolled Wazuh agents.
    agent = { enabled = false }

    wazuh = {
      worker = { enabled = false }
      # Pin the manager master to the healthy worker too (see indexer above).
      nodeSelector = { "kubernetes.io/hostname" = "obs-lab-worker" }
      master = {
        resources     = var.wazuh_manager_resources
        storageSize   = "2Gi"
        networkPolicy = { enabled = false }
        service = {
          type = "ClusterIP"
          ports = [
            { name = "registration", protocol = "TCP", port = 1515, targetPort = 1515 },
            { name = "api", protocol = "TCP", port = 55000, targetPort = 55000 },
            # Fluent Bit ships the security stream here (UDP syslog).
            { name = "syslog", protocol = "UDP", port = 514, targetPort = 514 },
          ]
        }
        # The chart only adds the syslog <remote> to workers; with a single
        # master we add it here instead.
        extraConf = <<-EOT
        <remote>
          <connection>syslog</connection>
          <port>514</port>
          <protocol>udp</protocol>
          <allowed-ips>any</allowed-ips>
          <local_ip>0.0.0.0</local_ip>
        </remote>
        EOT
      }
      # Default local rules plus a rule that turns the gateway's
      # "authentication failed" security-stream events into Wazuh alerts
      # (so they land in wazuh-alerts-* and are visible in the dashboard).
      localRules = <<-EOT
      <!-- Local rules: chart defaults + obs-lab security stream. -->
      <group name="local,syslog,">

        <rule id="100001" level="5">
          <if_sid>5716</if_sid>
          <srcip>1.1.1.1</srcip>
          <description>sshd: authentication failed from IP 1.1.1.1.</description>
          <group>authentication_failed,pci_dss_10.2.4,pci_dss_10.2.5,</group>
        </rule>

        <rule id="100010" level="5">
          <match>authentication failed</match>
          <description>obs-lab: gateway reported a failed authentication (security stream).</description>
          <group>authentication_failed,</group>
        </rule>

      </group>
      EOT
    }
  })]
}
