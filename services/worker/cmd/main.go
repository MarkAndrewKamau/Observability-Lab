// Command worker consumes "order.created" messages from RabbitMQ, simulates
// payment processing, and updates the order's status in PostgreSQL. In Phase 3
// it extracts the traceparent from the message headers so its work is stitched
// into the same distributed trace as the originating HTTP request. It exposes
// /healthz and /metrics on its own port.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	amqpc "github.com/markandrewkamau/observability-lab/pkg/amqp"
	"github.com/markandrewkamau/observability-lab/pkg/config"
	"github.com/markandrewkamau/observability-lab/pkg/logging"
	"github.com/markandrewkamau/observability-lab/pkg/metrics"
	"github.com/markandrewkamau/observability-lab/pkg/postgres"
)

type orderEvent struct {
	OrderID     string `json:"order_id"`
	AmountCents int64  `json:"amount_cents"`
}

func main() {
	cfg := config.Load("worker")
	log := logging.New(cfg.ServiceName, cfg.LogLevel)
	m := metrics.New(cfg.ServiceName)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Error("postgres connect failed", "err", err.Error())
		os.Exit(1)
	}
	defer db.Close()

	consumer, err := amqpc.Dial(cfg.AMQPURL, cfg.AMQPQueue)
	if err != nil {
		log.Error("rabbitmq connect failed", "err", err.Error())
		os.Exit(1)
	}
	defer consumer.Close()

	// Health/metrics server so Prometheus can scrape the worker.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.Handle("GET /metrics", m.Handler())
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("worker metrics listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server error", "err", err.Error())
			stop()
		}
	}()

	p := &processor{cfg: cfg, log: log, db: db, m: m}
	go func() {
		log.Info("worker consuming", "queue", cfg.AMQPQueue)
		if err := consumer.Consume(ctx, p.handle); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("consume loop ended", "err", err.Error())
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	_ = srv.Shutdown(sctx)
}

type processor struct {
	cfg config.Config
	log *slog.Logger
	db  *pgxpool.Pool
	m   *metrics.Metrics
}

func (p *processor) handle(ctx context.Context, d amqp.Delivery) error {
	start := time.Now()
	var evt orderEvent
	if err := json.Unmarshal(d.Body, &evt); err != nil {
		p.log.Error("bad message", "err", err.Error())
		p.m.QueueConsumed.WithLabelValues(p.cfg.AMQPQueue, "invalid").Inc()
		return nil // don't requeue a malformed message
	}

	// Simulate payment processing: variable latency, ~5% failure rate.
	time.Sleep(time.Duration(20+rand.Intn(80)) * time.Millisecond)
	status, outcome := "paid", "success"
	if rand.Float64() < 0.05 {
		status, outcome = "failed", "failure"
	}

	_, err := p.db.Exec(ctx,
		`UPDATE orders SET status=$1, updated_at=now() WHERE id=$2`, status, evt.OrderID)
	if err != nil {
		p.log.Error("db update failed", "order_id", evt.OrderID, "err", err.Error())
		p.m.QueueConsumed.WithLabelValues(p.cfg.AMQPQueue, "error").Inc()
		return err // requeue-eligible (nack no-requeue in amqp helper)
	}

	p.log.Info("payment processed", "order_id", evt.OrderID, "status", status)
	p.m.QueueConsumed.WithLabelValues(p.cfg.AMQPQueue, outcome).Inc()
	p.m.Transactions.WithLabelValues("payment", outcome).Inc()
	p.m.TransactionDuration.WithLabelValues("payment").Observe(time.Since(start).Seconds())
	return nil
}
