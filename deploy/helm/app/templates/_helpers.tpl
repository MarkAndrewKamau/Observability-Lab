{{/* Common labels applied to every object. */}}
{{- define "app.labels" -}}
app.kubernetes.io/part-of: obs-lab
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{/* Per-service selector labels. Call with (dict "svc" "gateway" "root" $). */}}
{{- define "app.selectorLabels" -}}
app.kubernetes.io/name: {{ .svc }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
{{- end -}}
