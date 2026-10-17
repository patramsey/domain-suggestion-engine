package api

import (
	"strings"
	"time"
)

// maxBodyBytes bounds a /suggest request. The largest legitimate body — 500
// characters of input, 40 unavailable domains and 5 inspire_from domains of
// up to 253 bytes each — is about 13 KB.
const maxBodyBytes = 16 << 10

// maxDomainLen is the longest a domain name can be (RFC 1035).
const maxDomainLen = 253

// invalidDomains returns the entries that are not plausible domain names, at
// most five of them for the error message. The lists are pasted into every
// LLM prompt, so they must be what they claim to be: arbitrary text there is
// both a cost multiplier and a prompt-injection surface.
func invalidDomains(entries []string) []string {
	var bad []string
	for _, e := range entries {
		if !plausibleDomain(strings.ToLower(strings.TrimSpace(e))) {
			if len(e) > 64 {
				e = e[:64] + "…"
			}
			bad = append(bad, e)
			if len(bad) == 5 {
				break
			}
		}
	}
	return bad
}

// plausibleDomain checks shape only: at least two dot-separated labels of
// 1-63 letters, digits or hyphens, no label starting or ending with a hyphen.
func plausibleDomain(s string) bool {
	if s == "" || len(s) > maxDomainLen {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// Availability-check budgets. Unresolved names are reported as null rather
// than held for: the non-stream response must beat the server's 15-second
// write timeout with time to spare, and a stream should not hang either.
const (
	availabilityBudget       = 2 * time.Second
	streamAvailabilityBudget = 8 * time.Second
)
