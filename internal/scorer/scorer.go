package scorer

import (
	"math"
	"sort"
	"strings"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/wordlist"
)

// baseScore computes a composite quality score [0.0, 1.0] for a candidate,
// before the availability penalty is applied. Weights (sum = 1.0):
//   brandability     0.40 — n-gram phonotactics (70%) + sub-word memorability (30%)
//   conceptRelevance 0.30 — GloVe semantic similarity to query
//   tldPremium       0.15 — IANA adoption + word-likeness + semantic match
//   length           0.15 — SLD length curve
// LLM-sourced candidates receive a position-scaled bonus: +0.03 base for all
// LLM suggestions, plus up to +0.04 for the LLM's top-ranked picks (LLMRank=1.0).
// The LLM sorts its output best-first, so position is a free quality signal.
func baseScore(c algorithmic.Candidate, tokens []string) float64 {
	base := clamp(
		brandability(c.SLD)*0.40 +
			conceptRelevance(c.SLD, tokens)*0.30 +
			tldPremium(c.TLD, tokens)*0.15 +
			lengthScore(c.SLD)*0.15,
	)
	if c.Source == "llm" {
		return clamp(base + 0.03 + c.LLMRank*0.04)
	}
	return base
}

// Availability penalties (spec Part 2): likely-taken names are demoted so
// more of the top results are registrable. Values from the offline sweep.
const (
	commonWordPenalty = 0.20 // SCOWL ≤ wordlist.CommonMaxLevel
	level35Penalty    = 0.05 // SCOWL level 35
	crowdingWeight    = 0.15
	neutralFreeRate   = 0.5 // TLDs missing from tldFreeRate
)

// Score is the composite quality score minus the availability penalty,
// clamped to [0, 1].
func Score(c algorithmic.Candidate, tokens []string) float64 {
	return clamp(baseScore(c, tokens) - availabilityPenalty(c))
}

// availabilityPenalty is larger for names that are likely registered: very
// common or common words, and TLDs where most probe labels are taken.
func availabilityPenalty(c algorithmic.Candidate) float64 {
	p := 0.0
	if l, ok := wordlist.Level(c.SLD); ok {
		switch {
		case l <= wordlist.CommonMaxLevel:
			p += commonWordPenalty
		case l == 35:
			p += level35Penalty
		}
	}
	fr, ok := tldFreeRate[c.TLD]
	if !ok {
		fr = neutralFreeRate
	}
	return p + crowdingWeight*(1-fr)
}

// --- Length ---

// lengthScore scores on SLD length: peaks at 3–8 chars (sweet spot for brand names),
// penalises only at extremes. Short names like "zen" are desirable, not penalised.
func lengthScore(sld string) float64 {
	n := len(sld)
	switch {
	case n == 1:
		return 0.30
	case n == 2:
		return 0.75
	case n <= 8:
		return 1.00
	case n <= 10:
		return 0.90
	case n <= 12:
		return 0.75
	case n <= 16:
		return 0.55
	default:
		return 0.20
	}
}

// --- Brandability ---

func brandability(sld string) float64 {
	for _, r := range sld {
		if r >= '0' && r <= '9' {
			return 0.0
		}
		if r == '-' {
			return 0.15
		}
	}
	// 80% phonotactic quality (n-gram model), 20% sub-word recognizability.
	// Memorability acts as a bonus for recognisable compounds and real words;
	// creative coinages with non-English roots are penalised less than before.
	return ngramBrandability(sld)*0.80 + memorability(sld)*0.20
}

// --- TLD Premium ---

func tldPremium(tld string, tokens []string) float64 {
	base, ok := tldScores[tld]
	if !ok {
		// fallback for any TLD not in the generated map
		if len(tld) == 2 {
			base = 0.40
		} else {
			base = 0.55
		}
	}
	if semanticMatch(tld, tokens) {
		switch {
		case base >= 0.85:
			base = 0.90
		case base >= 0.70:
			base = 0.80
		default:
			base = math.Min(base+0.30, 0.90)
		}
	}
	return clamp(base)
}

func semanticMatch(tld string, tokens []string) bool {
	for _, tok := range tokens {
		// Exact: .pizza for "pizza"
		if tld == tok {
			return true
		}
		// Domain hack: token ends with TLD ("shoes" + ".es" → sho.es)
		if len(tld) >= 2 && len(tok) > len(tld) && strings.HasSuffix(tok, tld) {
			return true
		}
		// Prefix overlap — min 4 chars to avoid short accidental matches (.art ≠ "smart")
		if len(tld) >= 4 && strings.HasPrefix(tok, tld) {
			return true
		}
		if len(tok) >= 4 && strings.HasPrefix(tld, tok) {
			return true
		}
	}
	return false
}


func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Rank scores all candidates and returns them sorted by score descending.
func Rank(candidates []algorithmic.Candidate, tokens []string) []ScoredCandidate {
	scored := make([]ScoredCandidate, len(candidates))
	for i, c := range candidates {
		scored[i] = ScoredCandidate{Candidate: c, Score: Score(c, tokens)}
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	return scored
}

// ScoredCandidate pairs a Candidate with its computed score.
type ScoredCandidate struct {
	algorithmic.Candidate
	Score float64
}
