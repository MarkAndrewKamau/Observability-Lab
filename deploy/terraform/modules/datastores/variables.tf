variable "namespace" {
  type    = string
  default = "obs"
}
variable "chart_path" {
  type = string
}
variable "persistence_enabled" {
  type    = bool
  default = false
}
variable "pg_storage_size" {
  type    = string
  default = "1Gi"
}
variable "mq_storage_size" {
  type    = string
  default = "1Gi"
}
variable "pg_user" {
  type    = string
  default = "obs"
}
variable "pg_password" {
  type      = string
  sensitive = true
  default   = "obs"
}
variable "pg_database" {
  type    = string
  default = "obs"
}
variable "pg_resources" {
  type    = any
  default = {}
}
variable "mq_user" {
  type    = string
  default = "obs"
}
variable "mq_password" {
  type      = string
  sensitive = true
  default   = "obs"
}
variable "mq_resources" {
  type    = any
  default = {}
}
