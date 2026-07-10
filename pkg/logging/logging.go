// Package logging provides structured JSON logging with two guarantees the lab
// depends on:
//
//  1. PII/secret masking — every log message and string attribute is passed
//     through pkg/masking before it is written, so tokens, phone numbers, IDs
//     and account data can never reach Loki (or a console) unmasked. Reserved
//     structural keys (trace/span IDs, service name) are exempt so telemetry
//     correlation keeps working.
//
//  2. Stream classification — every record carries a "stream" attribute of
//     either "operational" or "security". Fluent Bit later routes operational
//     logs to Loki and security logs to Wazuh purely on this field.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/markandrewkamau/observability-lab/pkg/masking"
)

// Stream constants classify a log line for downstream routing.
const (
	StreamKey         = "stream"
	StreamOperational = "operational"
	StreamSecurity    = "security"
)

// reserved keys are never masked — they are structural, machine-generated, and
// safe, and masking them would break trace/log correlation.
var reserved = map[string]struct{}{
	slog.TimeKey:  {},
	slog.LevelKey: {},
	slog.MessageKey: {},
	"service":     {},
	"trace_id":    {},
	"span_id":     {},
	StreamKey:     {},
}

// maskHandler wraps another slog.Handler, masking the message and all string
// attribute values (except reserved keys) before delegating. It owns the
// "stream" classification so exactly one stream key is emitted per record even
// when the stream is overridden (operational -> security).
type maskHandler struct {
	inner  slog.Handler
	masker *masking.Masker
	stream string
}

func (h maskHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h maskHandler) Handle(ctx context.Context, r slog.Record) error {
	// Mask the message text (never reserved, always user-influenced).
	nr := slog.NewRecord(r.Time, r.Level, h.masker.Mask(r.Message), r.PC)
	stream := h.stream
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == StreamKey { // per-record override, don't double-emit
			stream = a.Value.String()
			return true
		}
		nr.AddAttrs(h.maskAttr(a))
		return true
	})
	nr.AddAttrs(slog.String(StreamKey, stream))
	return h.inner.Handle(ctx, nr)
}

func (h maskHandler) maskAttr(a slog.Attr) slog.Attr {
	if _, ok := reserved[a.Key]; ok {
		return a
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.masker.Mask(a.Value.String()))
	case slog.KindGroup:
		grp := a.Value.Group()
		out := make([]slog.Attr, 0, len(grp))
		for _, g := range grp {
			out = append(out, h.maskAttr(g))
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	default:
		return a
	}
}

func (h maskHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	masked := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if a.Key == StreamKey { // intercept: set the stream, never emit twice
			h.stream = a.Value.String()
			continue
		}
		masked = append(masked, h.maskAttr(a))
	}
	h.inner = h.inner.WithAttrs(masked)
	return h
}

func (h maskHandler) WithGroup(name string) slog.Handler {
	h.inner = h.inner.WithGroup(name)
	return h
}

// New returns a JSON logger writing to stdout, tagged with the service name and
// defaulting to the operational stream, with PII masking always applied.
func New(service, level string) *slog.Logger {
	return NewTo(os.Stdout, service, level)
}

// NewTo is New with an explicit writer (used by tests).
func NewTo(w io.Writer, service, level string) *slog.Logger {
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(level)})
	h := maskHandler{inner: base, masker: masking.New(), stream: StreamOperational}
	return slog.New(h).With(slog.String("service", service))
}

// Security returns a derived logger whose records are tagged as the security
// stream, so auth/security events are routed to Wazuh rather than Loki.
func Security(l *slog.Logger) *slog.Logger {
	return l.With(slog.String(StreamKey, StreamSecurity))
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
