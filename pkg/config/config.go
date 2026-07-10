// Package config loads service configuration from environment variables with
// sane defaults, so the same binary runs identically in local, dev and prod —
// only the env differs (12-factor). Values live in Helm values files per
// environment; nothing sensitive is hard-coded.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config is the union of settings any service in the lab may need. Unused
// fields are simply ignored by a given service.
type Config struct {
	ServiceName string // logical name, e.g. "gateway"
	Env         string // "dev" | "prod" | "local"
	HTTPAddr    string // ":8080"
	LogLevel    string // "debug" | "info" | "warn" | "error"

	// Downstream dependencies.
	OrdersURL   string // gateway -> orders base URL
	PostgresDSN string // orders/worker -> PostgreSQL
	AMQPURL     string // orders/worker -> RabbitMQ
	AMQPQueue   string // work queue name

	// Edge auth (a deliberately simple shared-secret bearer token for the lab).
	AuthToken string

	// Telemetry (wired in Phase 3).
	OTLPEndpoint string // OTel Collector OTLP/HTTP endpoint

	ShutdownGrace time.Duration
}

// Load builds a Config for the named service from the environment.
func Load(serviceName string) Config {
	return Config{
		ServiceName:   serviceName,
		Env:           getenv("ENV", "local"),
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		LogLevel:      getenv("LOG_LEVEL", "info"),
		OrdersURL:     getenv("ORDERS_URL", "http://localhost:8081"),
		PostgresDSN:   getenv("POSTGRES_DSN", "postgres://obs:obs@localhost:5432/obs?sslmode=disable"),
		AMQPURL:       getenv("AMQP_URL", "amqp://obs:obs@localhost:5672/"),
		AMQPQueue:     getenv("AMQP_QUEUE", "orders.created"),
		AuthToken:     getenv("AUTH_TOKEN", "dev-secret-token"),
		OTLPEndpoint:  getenv("OTLP_ENDPOINT", ""),
		ShutdownGrace: getdur("SHUTDOWN_GRACE", 10*time.Second),
	}
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getdur(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return def
}
