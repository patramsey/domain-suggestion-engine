package algorithmic

import (
	"slices"
)

// CompoundGenerator pairs query tokens to form compound SLDs
// (e.g. "denver" + "coffee" → "denvercoffee.com", "coffeedusk.io").
type CompoundGenerator struct{}

// NewCompoundGenerator creates a new CompoundGenerator.
func NewCompoundGenerator() *CompoundGenerator {
	return &CompoundGenerator{}
}

func (g *CompoundGenerator) Name() string { return "compounds" }

// primaryTLDs defines the preferred order of TLDs to use when generating compounds
// if many TLDs are requested, avoiding candidate pool explosion.
var primaryTLDs = []string{"com", "co", "io", "ai", "app", "dev", "net", "org"}

func (g *CompoundGenerator) Generate(tokens []string, tlds []string) []Candidate {
	if len(tokens) < 2 || len(tlds) == 0 {
		return nil
	}

	// Select up to 6 target TLDs from allowed tlds, prioritizing primaryTLDs
	targetTLDs := selectTargetTLDs(tlds, 6)

	seen := make(map[string]struct{})
	var out []Candidate

	add := func(sld, tld string) {
		if len(sld) < 4 || len(sld) > 18 {
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

	// Pair up tokens
	for i := 0; i < len(tokens); i++ {
		for j := 0; j < len(tokens); j++ {
			if i == j {
				continue
			}
			combined := tokens[i] + tokens[j]
			for _, tld := range targetTLDs {
				add(combined, tld)
			}
		}
	}

	return out
}

// selectTargetTLDs filters and limits tlds to the most relevant/popular up to maxCount.
func selectTargetTLDs(allowed []string, maxCount int) []string {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, t := range allowed {
		allowedSet[t] = struct{}{}
	}

	var selected []string
	// First pick from primaryTLDs if allowed
	for _, p := range primaryTLDs {
		if _, ok := allowedSet[p]; ok && !slices.Contains(selected, p) {
			selected = append(selected, p)
			if len(selected) >= maxCount {
				return selected
			}
		}
	}

	// Fill remaining from allowed in original order
	for _, t := range allowed {
		if !slices.Contains(selected, t) {
			selected = append(selected, t)
			if len(selected) >= maxCount {
				break
			}
		}
	}
	return selected
}
