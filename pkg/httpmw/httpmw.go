// Package httpmw provides composable net/http middleware for the lab's services:
// panic recovery, structured request logging (masked, operational stream) and
// Prometheus RED metrics. OpenTelemetry span middleware is added in Phase 3.
package httpmw

import (
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/markandrewkamau/observability-lab/pkg/metrics"
)

// statusRecorder captures the response status code for logging/metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Chain applies middlewares in order (outermost first).
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Trace starts a server span for each request (extracting any incoming
// traceparent) using the OpenTelemetry global provider. Use it as the outermost
// middleware so the span encloses the whole handler.
func Trace(route string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, route)
	}
}

// Recover converts panics into 500s and an error log rather than crashing.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					log.Error("panic recovered", "panic", v, "path", r.URL.Path)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Observe logs each request (operational stream) and records RED metrics.
// route is the templated path used as a low-cardinality metric label.
func Observe(log *slog.Logger, m *metrics.Metrics, route string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			dur := time.Since(start)

			m.HTTPRequests.WithLabelValues(r.Method, route, http.StatusText(rec.status)).Inc()
			m.HTTPDuration.WithLabelValues(r.Method, route).Observe(dur.Seconds())
			log.Info("http request",
				"method", r.Method,
				"route", route,
				"status", rec.status,
				"duration_ms", dur.Milliseconds(),
			)
		})
	}
}
