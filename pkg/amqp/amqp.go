// Package amqp is a thin RabbitMQ helper for publishing and consuming JSON
// messages on a durable queue. Messages carry an application headers table,
// which is where Phase 3 injects/extracts the W3C traceparent so a trace flows
// from the HTTP producer through the queue into the worker.
//
// The client tolerates broker restarts: a background reconnector redials with
// backoff whenever the connection dies, and Publish/Consume wait for a live
// channel instead of failing outright. Consume re-registers on a fresh channel
// after a drop, and unacked deliveries are requeued by the broker.
package amqp

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	initialBackoff = time.Second
	maxBackoff     = 30 * time.Second
)

// Client owns a connection + channel and declares the work queue. It is safe
// for concurrent use by multiple producers.
type Client struct {
	url   string
	queue string

	mu      sync.Mutex
	conn    *amqp.Connection
	ch      *amqp.Channel
	closing bool
	done    chan struct{} // closed by Close to stop the reconnector
}

// Dial connects to RabbitMQ, declares a durable queue and starts the
// background reconnector.
func Dial(url, queue string) (*Client, error) {
	c := &Client{url: url, queue: queue, done: make(chan struct{})}
	if err := c.reconnect(); err != nil {
		return nil, err
	}
	go c.watch()
	return c, nil
}

// reconnect (re)dials the broker and declares the work queue.
func (c *Client) reconnect() error {
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return fmt.Errorf("amqp dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("amqp channel: %w", err)
	}
	if _, err := ch.QueueDeclare(c.queue, true, false, false, false, nil); err != nil {
		ch.Close()
		conn.Close()
		return fmt.Errorf("declare queue %q: %w", c.queue, err)
	}
	c.mu.Lock()
	c.conn, c.ch = conn, ch
	c.mu.Unlock()
	return nil
}

// watch redials forever while the broker is unreachable, so producers and
// consumers simply wait for a live channel. It exits when Close is called.
func (c *Client) watch() {
	backoff := initialBackoff
	for {
		c.mu.Lock()
		closing, conn := c.closing, c.conn
		c.mu.Unlock()
		if closing {
			return
		}
		notify := make(chan *amqp.Error, 1)
		conn.NotifyClose(notify)
		if conn.IsClosed() {
			<-notify
		}
		select {
		case <-notify:
		case <-c.done:
			return
		}
		for {
			if err := c.reconnect(); err == nil {
				backoff = initialBackoff
				break
			}
			select {
			case <-c.done:
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// channel returns the live channel, or nil while the broker is down.
func (c *Client) channel() *amqp.Channel {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || c.conn.IsClosed() || c.ch == nil {
		return nil
	}
	return c.ch
}

// Publish sends body to the work queue with the given headers (persistent).
// It retries until a channel is live again, or ctx is done.
func (c *Client) Publish(ctx context.Context, body []byte, headers amqp.Table) error {
	for {
		if ch := c.channel(); ch != nil {
			err := ch.PublishWithContext(ctx, "", c.queue, false, false, amqp.Publishing{
				ContentType:  "application/json",
				DeliveryMode: amqp.Persistent,
				Headers:      headers,
				Body:         body,
			})
			if err == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Handler processes a single delivery; returning an error nacks (no requeue,
// to avoid poison-message loops in the lab).
type Handler func(ctx context.Context, d amqp.Delivery) error

// Consume blocks, dispatching deliveries to h until ctx is cancelled. If the
// broker connection drops, it waits for the reconnector and resumes consuming
// on a fresh channel; the broker requeues anything left unacked.
func (c *Client) Consume(ctx context.Context, h Handler) error {
	wait := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return nil
		}
	}
	for {
		ch := c.channel()
		if ch == nil {
			fmt.Fprintf(os.Stderr, "DBG consume: no channel\n")
			if err := wait(); err != nil {
				return err
			}
			continue
		}
		if err := ch.Qos(16, 0, false); err != nil {
			fmt.Fprintf(os.Stderr, "DBG consume: qos err %v\n", err)
			if err := wait(); err != nil {
				return err
			}
			continue
		}
		deliveries, err := ch.Consume(c.queue, "", false, false, false, false, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "DBG consume: consume err %v\n", err)
			if err := wait(); err != nil {
				return err
			}
			continue
		}
		fmt.Fprintf(os.Stderr, "DBG consume: registered\n")
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case d, ok := <-deliveries:
				if !ok {
					fmt.Fprintf(os.Stderr, "DBG consume: deliveries closed\n")
					break // channel died; resume at the outer loop
				}
				if err := h(ctx, d); err != nil {
					_ = d.Nack(false, false)
				} else {
					_ = d.Ack(false)
				}
			}
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

// Close stops the reconnector and tears down the connection. It is safe to
// call more than once.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return nil
	}
	c.closing = true
	close(c.done)
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}
