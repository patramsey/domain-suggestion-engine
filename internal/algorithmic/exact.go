package algorithmic

import (
	"strings"
)

// ExactTLDGenerator produces domain candidates where one of the query tokens
// matches an available TLD (e.g. "chicago plumbing" + .plumbing → "chicago.plumbing",
// "react developer" + .dev → "react.dev").
type ExactTLDGenerator struct{}

// NewExactTLDGenerator creates a new ExactTLDGenerator.
func NewExactTLDGenerator() *ExactTLDGenerator {
	return &ExactTLDGenerator{}
}

func (g *ExactTLDGenerator) Name() string { return "exact" }

func (g *ExactTLDGenerator) Generate(tokens []string, tlds []string) []Candidate {
	if len(tokens) < 2 || len(tlds) == 0 {
		return nil
	}

	// Index allowed TLDs
	tldSet := make(map[string]struct{}, len(tlds))
	for _, t := range tlds {
		tldSet[t] = struct{}{}
	}

	seen := make(map[string]struct{})
	var out []Candidate

	add := func(sld, tld string) {
		if len(sld) < 2 || len(sld) > 20 || sld == tld {
			return
		}
		if !sldRe.MatchString(sld) {
			return
		}
		key := sld + "." + tld
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, Candidate{SLD: sld, TLD: tld})
	}

	for i, tok := range tokens {
		var matchedTLD string
		// 1. Direct match: token equals TLD
		if _, ok := tldSet[tok]; ok {
			matchedTLD = tok
		} else {
			// 2. Inflection match: e.g. "plumbers" -> "plumbing" or "plumber"
			for t := range tldSet {
				if tldMatchesToken(tok, t) {
					matchedTLD = t
					break
				}
			}
		}

		if matchedTLD == "" {
			continue
		}

		// Other tokens form the SLD
		var others []string
		for j, o := range tokens {
			if j != i {
				others = append(others, o)
			}
		}

		// Single remaining token
		if len(others) == 1 {
			add(others[0], matchedTLD)
		} else if len(others) > 1 {
			// Concatenation in original order
			joined := strings.Join(others, "")
			add(joined, matchedTLD)

			// Individual remaining tokens
			for _, o := range others {
				add(o, matchedTLD)
			}
		}
	}

	return out
}

func tldMatchesToken(tok, tld string) bool {
	// Exact
	if tok == tld {
		return true
	}
	// Common inflections (plural/singular)
	if tok+"s" == tld || tok+"es" == tld || strings.TrimSuffix(tok, "s") == tld || strings.TrimSuffix(tok, "es") == tld {
		return true
	}
	// Prefix match for 3+ letter TLDs (e.g. .dev for "developer", .app for "application", .law for "lawyer")
	if len(tld) >= 3 && len(tok) > len(tld) && strings.HasPrefix(tok, tld) {
		return true
	}
	if len(tok) >= 3 && len(tld) > len(tok) && strings.HasPrefix(tld, tok) {
		return true
	}
	// Shared morphological root of at least 4 chars (e.g. "plumber"/"plumbing", "baker"/"bakery")
	pLen := commonPrefixLen(tok, tld)
	if pLen >= 4 && (len(tok)-pLen <= 3) && (len(tld)-pLen <= 3) {
		return true
	}
	return false
}

func commonPrefixLen(a, b string) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
