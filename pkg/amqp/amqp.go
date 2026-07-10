// Package amqp is a thin RabbitMQ helper for publishing and consuming JSON
// messages on a durable queue. Messages carry an application headers table,
// which is where Phase 3 injects/extracts the W3C traceparent so a trace flows
// from the HTTP producer through the queue into the worker.
package amqp

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Client owns a connection + channel and declares the work queue.
type Client struct {
	conn  *amqp.Connection
	ch    *amqp.Channel
	queue string
}

// Dial connects to RabbitMQ and declares a durable queue.
func Dial(url, queue string) (*Client, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("amqp dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("amqp channel: %w", err)
	}
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("declare queue %q: %w", queue, err)
	}
	return &Client{conn: conn, ch: ch, queue: queue}, nil
}

// Publish sends body to the work queue with the given headers (persistent).
func (c *Client) Publish(ctx context.Context, body []byte, headers amqp.Table) error {
	return c.ch.PublishWithContext(ctx, "", c.queue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Headers:      headers,
		Body:         body,
	})
}

// Handler processes a single delivery; returning an error nacks (no requeue,
// to avoid poison-message loops in the lab).
type Handler func(ctx context.Context, d amqp.Delivery) error

// Consume blocks, dispatching deliveries to h until ctx is cancelled.
func (c *Client) Consume(ctx context.Context, h Handler) error {
	if err := c.ch.Qos(16, 0, false); err != nil {
		return fmt.Errorf("qos: %w", err)
	}
	deliveries, err := c.ch.Consume(c.queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("delivery channel closed")
			}
			if err := h(ctx, d); err != nil {
				_ = d.Nack(false, false)
				continue
			}
			_ = d.Ack(false)
		}
	}
}

// HeaderCarrier adapts an AMQP headers table to the OpenTelemetry
// TextMapCarrier interface, so the W3C traceparent can be injected on publish
// and extracted on consume — this is what stitches the queue hop into the trace.
type HeaderCarrier amqp.Table

func (c HeaderCarrier) Get(key string) string {
	if v, ok := c[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (c HeaderCarrier) Set(key, value string) { c[key] = value }

func (c HeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// Close tears down the channel and connection.
func (c *Client) Close() error {
	if c.ch != nil {
		_ = c.ch.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
