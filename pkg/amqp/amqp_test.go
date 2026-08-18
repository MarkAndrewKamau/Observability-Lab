package amqp

import "testing"

// TestDialRejectsUnreachableBroker verifies Dial fails fast when no broker is
// listening (no retry loop hides a bad configuration).
func TestDialRejectsUnreachableBroker(t *testing.T) {
	c, err := Dial("amqp://127.0.0.1:1", "orders.created")
	if err == nil {
		c.Close()
		t.Fatal("expected dial error against a closed port")
	}
}

// TestCloseIdempotent verifies Close is safe on a client whose connection
// never came up, and that a second Close is a no-op.
func TestCloseIdempotent(t *testing.T) {
	c := &Client{url: "amqp://127.0.0.1:1", queue: "q", done: make(chan struct{})}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
