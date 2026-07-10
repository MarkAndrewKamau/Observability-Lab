// Command orders is the internal Orders API. It accepts an order, stores it in
// PostgreSQL (only the card's last four digits are persisted — never the PAN),
// and publishes an "order.created" message to RabbitMQ for the worker to
// process the payment. It records transaction-level metrics that the SLOs use.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"

	amqpc "github.com/markandrewkamau/observability-lab/pkg/amqp"
	"github.com/markandrewkamau/observability-lab/pkg/config"
	"github.com/markandrewkamau/observability-lab/pkg/httpmw"
	"github.com/markandrewkamau/observability-lab/pkg/logging"
	"github.com/markandrewkamau/observability-lab/pkg/metrics"
	"github.com/markandrewkamau/observability-lab/pkg/postgres"
)

type createOrderReq struct {
	CustomerID  string `json:"customer_id"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	CardNumber  string `json:"card_number"`
	Phone       string `json:"phone"`
}

type createOrderResp struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
}

// orderEvent is the queue payload the worker consumes.
type orderEvent struct {
	OrderID     string `json:"order_id"`
	AmountCents int64  `json:"amount_cents"`
}

type server struct {
	cfg config.Config
	log *slog.Logger
	db  *pgxpool.Pool
	pub *amqpc.Client
	m   *metrics.Metrics
}

func main() {
	cfg := config.Load("orders")
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

	pub, err := amqpc.Dial(cfg.AMQPURL, cfg.AMQPQueue)
	if err != nil {
		log.Error("rabbitmq connect failed", "err", err.Error())
		os.Exit(1)
	}
	defer pub.Close()

	s := &server{cfg: cfg, log: log, db: db, pub: pub, m: m}

	mux := http.NewServeMux()
	mux.Handle("POST /orders", httpmw.Chain(http.HandlerFunc(s.handleCreate),
		httpmw.Recover(log), httpmw.Observe(log, m, "/orders")))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.Handle("GET /metrics", m.Handler())

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("orders listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "err", err.Error())
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	_ = srv.Shutdown(sctx)
}

func (s *server) handleCreate(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req createOrderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, "order_create", http.StatusBadRequest, "invalid json")
		return
	}
	if req.CustomerID == "" || req.AmountCents <= 0 || req.CardNumber == "" {
		s.fail(w, "order_create", http.StatusBadRequest, "missing required fields")
		return
	}

	orderID := uuid.NewString()
	last4 := lastN(digitsOnly(req.CardNumber), 4)

	// The logger masks card_number and phone automatically; we also store only
	// last4 in the DB — the PAN is never persisted.
	s.log.Info("creating order",
		"order_id", orderID,
		"customer_id", req.CustomerID,
		"card_number", req.CardNumber, // masked by logger
		"phone", req.Phone,            // masked by logger
	)

	_, err := s.db.Exec(r.Context(),
		`INSERT INTO orders (id, customer_id, amount_cents, currency, card_last4, status)
		 VALUES ($1,$2,$3,$4,$5,'pending')`,
		orderID, req.CustomerID, req.AmountCents, orZero(req.Currency), last4)
	if err != nil {
		s.log.Error("db insert failed", "order_id", orderID, "err", err.Error())
		s.fail(w, "order_create", http.StatusInternalServerError, "storage error")
		return
	}

	evt, _ := json.Marshal(orderEvent{OrderID: orderID, AmountCents: req.AmountCents})
	// Headers table is where Phase 3 injects the traceparent.
	if err := s.pub.Publish(r.Context(), evt, amqp.Table{}); err != nil {
		s.log.Error("publish failed", "order_id", orderID, "err", err.Error())
		s.fail(w, "order_create", http.StatusInternalServerError, "queue error")
		return
	}
	s.m.QueuePublished.WithLabelValues(s.cfg.AMQPQueue).Inc()

	s.m.Transactions.WithLabelValues("order_create", "success").Inc()
	s.m.TransactionDuration.WithLabelValues("order_create").Observe(time.Since(start).Seconds())

	writeJSON(w, http.StatusAccepted, createOrderResp{OrderID: orderID, Status: "pending"})
}

func (s *server) fail(w http.ResponseWriter, txType string, code int, msg string) {
	s.m.Transactions.WithLabelValues(txType, "failure").Inc()
	http.Error(w, msg, code)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func orZero(s string) string {
	if s == "" {
		return "USD"
	}
	return s
}
