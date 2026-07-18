variable "release_name" {
  type    = string
  default = "obs"
}
variable "namespace" {
  type    = string
  default = "obs"
}
variable "chart_path" {
  type = string
}
variable "env_name" {
  type = string
}
variable "log_level" {
  type    = string
  default = "info"
}
variable "auth_token" {
  type      = string
  sensitive = true
}
variable "image_repository" {
  type    = string
  default = "obs-lab"
}
variable "image_tag" {
  type    = string
  default = "dev"
}
variable "replicas" {
  type    = number
  default = 1
}
variable "gateway_nodeport" {
  type    = number
  default = 30080
}
variable "resources" {
  type = any
}
variable "postgres_dsn" {
  type      = string
  sensitive = true
}
variable "amqp_url" {
  type      = string
  sensitive = true
}
variable "amqp_queue" {
  type    = string
  default = "orders.created"
}
variable "otlp_endpoint" {
  type = string
}
