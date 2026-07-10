// Package masking provides deterministic redaction of sensitive data (PII and
// secrets) from arbitrary strings before they are emitted to logs, traces or
// metrics. It is intentionally dependency-free (stdlib only) so it can be unit
// tested in isolation and reused by every service and by the custom log
// exporter.
//
// The masker is conservative: it prefers over-masking to leaking. Each rule
// keeps just enough of a value to remain debuggable (e.g. the last 4 digits of
// a card) while guaranteeing the sensitive portion never survives.
package masking

import (
	"regexp"
	"strings"
)

// Category identifies the class of sensitive data a rule matches. It is
// exported so callers (e.g. the log exporter) can count masking events by type.
type Category string

const (
	CategoryToken    Category = "token"    // bearer tokens, API keys, JWTs
	CategoryPhone    Category = "phone"    // E.164 and common local formats
	CategoryEmail    Category = "email"    // email addresses
	CategoryCard     Category = "card"     // payment card numbers (PAN)
	CategoryIBAN     Category = "iban"     // bank account (IBAN)
	CategoryNationID Category = "national" // national ID / account numbers
	CategorySSN      Category = "ssn"      // US-style SSN
)

// rule pairs a compiled pattern with a replacement function.
type rule struct {
	cat     Category
	re      *regexp.Regexp
	replace func(match string) string
}

// Masker applies an ordered set of rules to redact sensitive substrings.
// A zero Masker is not usable; construct one with New.
type Masker struct {
	rules []rule
	// hits records, per category, how many redactions occurred on the last
	// call. It is intentionally not concurrency-safe on its own — callers that
	// need counts should use MaskCount which returns them explicitly.
}

// New returns a Masker configured with the default rule set covering tokens,
// phone numbers, emails, payment cards, IBANs, national IDs and SSNs.
func New() *Masker {
	return &Masker{rules: defaultRules()}
}

// keepLast returns a redaction that replaces all but the last n characters of
// the digit/alnum core with '*', preserving overall length hints.
func keepLast(n int) func(string) string {
	return func(match string) string {
		// Preserve non-alphanumeric separators so structure stays legible,
		// but mask alphanumerics except the final n.
		core := make([]rune, 0, len(match))
		for _, r := range match {
			if isAlnum(r) {
				core = append(core, r)
			}
		}
		if len(core) <= n {
			return strings.Repeat("*", len(core))
		}
		keep := string(core[len(core)-n:])
		return strings.Repeat("*", len(core)-n) + keep
	}
}

func fixed(label string) func(string) string {
	return func(string) string { return label }
}

func isAlnum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func defaultRules() []rule {
	return []rule{
		// JWTs: three base64url segments separated by dots.
		{CategoryToken, regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), fixed("[REDACTED_JWT]")},
		// Bearer / Authorization header values.
		{CategoryToken, regexp.MustCompile(`(?i)\b(bearer|token|apikey|api_key|access_token|secret)\b\s*[:=]?\s*["']?[A-Za-z0-9._\-]{12,}`), fixed("[REDACTED_TOKEN]")},
		// Generic high-entropy secret-looking values (>=24 alnum chars).
		{CategoryToken, regexp.MustCompile(`\b[A-Za-z0-9]{24,}\b`), fixed("[REDACTED_SECRET]")},
		// Payment card numbers (13-19 digits, optional spaces/dashes).
		{CategoryCard, regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`), keepLast(4)},
		// IBAN: 2 letters, 2 check digits, up to 30 alnum.
		{CategoryIBAN, regexp.MustCompile(`\b[A-Z]{2}\d{2}[A-Z0-9]{10,30}\b`), keepLast(4)},
		// Email addresses.
		{CategoryEmail, regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`), maskEmail},
		// US SSN.
		{CategorySSN, regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`), fixed("[REDACTED_SSN]")},
		// Phone numbers: E.164 (+254712345678) and common grouped forms.
		{CategoryPhone, regexp.MustCompile(`\+?\d[\d\s().\-]{7,}\d`), keepLast(3)},
		// National ID / account numbers: standalone 6-12 digit runs.
		{CategoryNationID, regexp.MustCompile(`\b\d{6,12}\b`), keepLast(2)},
	}
}

// maskEmail keeps the domain but redacts the local part, e.g.
// jane.doe@acme.com -> [REDACTED]@acme.com
func maskEmail(match string) string {
	at := strings.LastIndex(match, "@")
	if at <= 0 {
		return "[REDACTED_EMAIL]"
	}
	return "[REDACTED]" + match[at:]
}

// Mask returns s with every sensitive substring redacted.
func (m *Masker) Mask(s string) string {
	out, _ := m.MaskCount(s)
	return out
}

// MaskCount returns the redacted string plus a per-category count of how many
// substrings were redacted. The counts feed the custom log exporter's
// masked_pii_events_total metric.
func (m *Masker) MaskCount(s string) (string, map[Category]int) {
	counts := map[Category]int{}
	for _, r := range m.rules {
		s = r.re.ReplaceAllStringFunc(s, func(match string) string {
			counts[r.cat]++
			return r.replace(match)
		})
	}
	return s, counts
}

// Contains reports whether s still contains anything the masker would redact.
// Used by tests to assert that output is clean.
func (m *Masker) Contains(s string) bool {
	for _, r := range m.rules {
		if r.re.MatchString(s) {
			return true
		}
	}
	return false
}
