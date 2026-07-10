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
