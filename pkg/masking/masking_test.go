package masking

import (
	"strings"
	"testing"
)

// TestNoUnmaskedPII is the requirement-driving test: for every sensitive input
// class, the raw secret must NOT survive in the output, and the masker must
// report the output as clean.
func TestNoUnmaskedPII(t *testing.T) {
	m := New()

	cases := []struct {
		name     string
		in       string
		mustGone string // raw substring that must not appear in output
	}{
		{"jwt", "auth eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.abc123DEFxyz done", "eyJhbGciOiJIUzI1NiJ9"},
		{"bearer", "Authorization: Bearer sk_live_9f8e7d6c5b4a3f2e1d0c", "sk_live_9f8e7d6c5b4a3f2e1d0c"},
		{"apikey_kv", `apikey="A1b2C3d4E5f6G7h8"`, "A1b2C3d4E5f6G7h8"},
		{"opaque_secret", "value 8f3a9c2e7b1d4056af22ce90bb17", "8f3a9c2e7b1d4056af22ce90bb17"},
		{"card_spaces", "card 4111 1111 1111 1111 charged", "4111 1111 1111 1111"},
		{"card_plain", "pan=5500005555555559", "5500005555555559"},
		{"iban", "iban GB82WEST12345698765432 ok", "GB82WEST12345698765432"},
		{"email", "user jane.doe@acme.com registered", "jane.doe@acme.com"},
		{"ssn", "ssn 123-45-6789 filed", "123-45-6789"},
		{"phone_e164", "call +254712345678 now", "254712345678"},
		{"phone_grouped", "tel +1-555-123-4567 x", "555-123-4567"},
		{"national_id", "id 3480021 verified", "3480021"},
		{"account", "acct 000123456789 balance", "000123456789"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := m.Mask(c.in)
			if strings.Contains(out, c.mustGone) {
				t.Fatalf("secret leaked: input=%q output=%q leaked=%q", c.in, out, c.mustGone)
			}
			if m.Contains(out) {
				t.Fatalf("output still contains maskable data: %q", out)
			}
		})
	}
}

// TestMaskKeepsLastDigits verifies debuggability: cards keep last 4, phones last 3.
func TestMaskKeepsLastDigits(t *testing.T) {
	m := New()
	out := m.Mask("pan=5500005555555559")
	if !strings.HasSuffix(out, "5559") {
		t.Fatalf("expected last 4 digits preserved, got %q", out)
	}
}

// TestMaskCountsByCategory verifies the exporter-facing counts.
func TestMaskCountsByCategory(t *testing.T) {
	m := New()
	_, counts := m.MaskCount("jane@acme.com and john@acme.com and +254712345678")
	if counts[CategoryEmail] != 2 {
		t.Fatalf("expected 2 email redactions, got %d", counts[CategoryEmail])
	}
	if counts[CategoryPhone] != 1 {
		t.Fatalf("expected 1 phone redaction, got %d", counts[CategoryPhone])
	}
}

// TestCleanTextUnchanged ensures ordinary text is not mangled.
func TestCleanTextUnchanged(t *testing.T) {
	m := New()
	in := "order created status=ok items=3 latency=42ms"
	if out := m.Mask(in); out != in {
		t.Fatalf("clean text was altered: %q -> %q", in, out)
	}
}
