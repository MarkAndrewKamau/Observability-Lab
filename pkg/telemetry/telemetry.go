// Package telemetry wires OpenTelemetry tracing for every service: an OTLP/HTTP
// exporter pointed at the OTel Collector, a batching tracer provider, and the
// W3C trace-context + baggage propagators that carry the trace across HTTP
// calls and RabbitMQ messages. Calling Init once at startup is all a service
// needs; instrumentation then uses the global tracer/propagator.
package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// ShutdownFunc flushes and stops the tracer provider; call it on exit.
type ShutdownFunc func(context.Context) error

// Init installs the global tracer provider and propagators. If endpoint is
// empty (e.g. local unit runs) tracing is a no-op but propagators are still set,
// so context plumbing works without a collector. endpoint is host:port of the
// collector's OTLP/HTTP receiver, e.g. "localhost:4318".
func Init(ctx context.Context, service, endpoint string) (ShutdownFunc, error) {
	// Propagators are always set so traceparent flows across HTTP and the queue.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	if endpoint == "" {
		otel.SetTracerProvider(sdktrace.NewTracerProvider()) // no exporter
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(service)),
	)
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		// Short batch timeout keeps the demo/smoke-test latency low.
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(time.Second)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Tracer returns a named tracer from the global provider.
func Tracer(name string) trace.Tracer { return otel.Tracer(name) }
