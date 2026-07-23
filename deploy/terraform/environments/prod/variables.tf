variable "kubeconfig" {
  type    = string
  default = "~/.kube/config"
}
variable "kube_context" {
  type    = string
  default = "kind-obs-lab"
}
variable "persistence_enabled" {
  type    = bool
  default = false
}
variable "replicas" {
  type    = number
  default = 1
}
variable "image_tag" {
  type    = string
  default = "dev"
}
variable "auth_token" {
  type      = string
  sensitive = true
  default   = "dev-secret-token"
}
variable "grafana_admin_password" {
  type      = string
  sensitive = true
  default   = "admin"
}
variable "prometheus_retention" {
  type    = string
  default = "6h"
}
variable "service_monitor_enabled" {
  type    = bool
  default = true
}
variable "loadgen_enabled" {
  type    = bool
  default = false
}
variable "app_resources" {
  type    = any
  default = {}
}
variable "pg_resources" {
  type    = any
  default = {}
}
variable "mq_resources" {
  type    = any
  default = {}
}
variable "prometheus_resources" {
  type    = any
  default = {}
}
