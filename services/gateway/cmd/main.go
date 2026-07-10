// Command gateway is the public edge API. It authenticates callers with a
// bearer token (emitting security-stream auth events destined for Wazuh), then
// forwards the order to the internal Orders API over HTTP. In Phase 3 the
// outbound request carries the W3C traceparent so the trace spans gateway →
// orders → queue → worker.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/markandrewkamau/observability-lab/pkg/config"
	"github.com/markandrewkamau/observability-lab/pkg/httpmw"
	"github.com/markandrewkamau/observability-lab/pkg/logging"
	"github.com/markandrewkamau/observability-lab/pkg/metrics"
)

type server struct {
	cfg    config.Config
	log    *slog.Logger
	seclog *slog.Logger // security stream (auth events) → Wazuh
	m      *metrics.Metrics
	client *http.Client
}

func main() {
	cfg := config.Load("gateway")
	log := logging.New(cfg.ServiceName, cfg.LogLevel)
	m := metrics.New(cfg.ServiceName)

	s := &server{
		cfg:    cfg,
		log:    log,
		seclog: logging.Security(log),
		m:      m,
		client: &http.Client{Timeout: 5 * time.Second},
	}

	mux := http.NewServeMux()
	mux.Handle("POST /api/orders", httpmw.Chain(http.HandlerFunc(s.handleOrder),
		httpmw.Recover(log), httpmw.Observe(log, m, "/api/orders")))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.Handle("GET /metrics", m.Handler())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("gateway listening", "addr", cfg.HTTPAddr)
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

func (s *server) handleOrder(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		// Security-stream event: unauthorized attempt (routed to Wazuh).
		s.seclog.Warn("authentication failed",
			"event", "auth_failure",
			"remote", clientIP(r),
			"path", r.URL.Path,
		)
		s.m.Transactions.WithLabelValues("checkout", "failure").Inc()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.seclog.Info("authentication succeeded", "event", "auth_success", "remote", clientIP(r))

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		s.m.Transactions.WithLabelValues("checkout", "failure").Inc()
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Best-effort decode purely for a masked, structured log line.
	var peek struct {
		CustomerID string `json:"customer_id"`
	}
	_ = json.Unmarshal(body, &peek)
	s.log.Info("forwarding order to orders api", "customer_id", peek.CustomerID)

	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost,
		strings.TrimRight(s.cfg.OrdersURL, "/")+"/orders", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Error("orders api unreachable", "err", err.Error())
		s.m.Transactions.WithLabelValues("checkout", "failure").Inc()
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	outcome := "success"
	if resp.StatusCode >= 400 {
		outcome = "failure"
	}
	s.m.Transactions.WithLabelValues("checkout", outcome).Inc()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// authenticate checks the bearer token (a deliberately simple shared secret).
func (s *server) authenticate(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(h, "Bearer ")
	return ok && token == s.cfg.AuthToken
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
