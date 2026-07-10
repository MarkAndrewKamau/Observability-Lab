package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestLogMasksPII proves that PII passed to the logger — in the message or in a
// string attribute — is redacted in the emitted JSON, while structural fields
// (service, trace_id) are preserved.
func TestLogMasksPII(t *testing.T) {
	var buf bytes.Buffer
	log := NewTo(&buf, "orders", "info").With("trace_id", "abc123deadbeef")

	log.Info("payment for card 4111 1111 1111 1111",
		"phone", "+254712345678",
		"email", "jane.doe@acme.com",
		"trace_id", "abc123deadbeef",
	)

	out := buf.String()
	for _, leaked := range []string{"4111 1111 1111 1111", "254712345678", "jane.doe@acme.com"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("PII leaked to log output: %q in %s", leaked, out)
		}
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if rec["service"] != "orders" {
		t.Fatalf("service field lost: %v", rec["service"])
	}
	if rec["trace_id"] != "abc123deadbeef" {
		t.Fatalf("trace_id must NOT be masked, got %v", rec["trace_id"])
	}
}

// TestSecurityStream verifies auth events are tagged for Wazuh routing.
func TestSecurityStream(t *testing.T) {
	var buf bytes.Buffer
	log := Security(NewTo(&buf, "gateway", "info"))
	log.Warn("failed login attempt", "user_id", "42")

	var rec map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &rec)
	if rec[StreamKey] != StreamSecurity {
		t.Fatalf("expected stream=%s, got %v", StreamSecurity, rec[StreamKey])
	}
}
