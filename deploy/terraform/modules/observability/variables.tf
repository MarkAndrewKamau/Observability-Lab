variable "namespace" {
  type    = string
  default = "monitoring"
}
variable "grafana_admin_password" {
  type      = string
  sensitive = true
  default   = "admin"
}
variable "grafana_nodeport" {
  type    = number
  default = 30030
}
variable "prometheus_retention" {
  type    = string
  default = "6h"
}
variable "prometheus_resources" {
  type    = any
  default = {}
}
variable "alertmanager_resources" {
  type    = any
  default = {}
}
variable "persistence_enabled" {
  type    = bool
  default = false
}
