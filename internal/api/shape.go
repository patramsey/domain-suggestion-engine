package api

import (
	"sort"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
)

// ShapeOptions are the request-level settings Shape needs.
type ShapeOptions struct {
	Count           int      // results to return
	NumTLDs         int      // size of the allowed TLD list (scales the diversity cap)
	LLMShare        float64  // tier balance: share of results reserved for LLM names
	CommonWordSlots int      // per 10 results, for very common single words
	Unavailable     []string // normalized domains to exclude
	Tokens          []string // parsed query tokens, for rescoring common words
}

// ShapeStats records how many candidates survived each step, for logging.
type ShapeStats struct {
	AfterDiversityCap, AfterTierBalance, AfterSLDDedup int
}

// Shape turns a ranked candidate pool into the final result list: TLD
// diversity cap, LLM/algorithmic tier balance, SLD dedup, the
// unavailable_domains filter, backfill to Count, and common-word slots.
//
// It is the whole post-scoring pipeline, exported so evaluation tools can run
// the exact production logic over a fixed candidate pool — re-ranking one
// recorded LLM pool under two configurations is a paired comparison, free of
// the run-to-run LLM noise that separate server runs carry.
func Shape(ranked []scorer.ScoredCandidate, o ShapeOptions) ([]scorer.ScoredCandidate, ShapeStats) {
	var st ShapeStats

	// Diversity cap on the full pool before tier selection, so tierBalance
	// can find non-homogeneous candidates.
	capped := diversityCap(ranked, o.Count, o.NumTLDs)
	st.AfterDiversityCap = len(capped)

	final := tierBalance(capped, o.Count, o.LLMShare)
	st.AfterTierBalance = len(final)

	final = sldDedup(final)
	st.AfterSLDDedup = len(final)
	sort.Slice(final, func(i, j int) bool { return final[i].Score > final[j].Score })

	pool := ranked
	if len(o.Unavailable) > 0 {
		pool = filterUnavailable(ranked, o.Unavailable)
		final = filterUnavailable(final, o.Unavailable)
	}

	// Top up from the pre-cap pool if the cap left the result short.
	final = backfillToCount(final, pool, o.Count)

	// Very common single words: great names that are usually taken, ranked
	// by quality instead of being demoted out.
	if o.CommonWordSlots > 0 {
		slotPool := capped
		if len(o.Unavailable) > 0 {
			slotPool = filterUnavailable(capped, o.Unavailable)
		}
		final = reserveCommonWords(final, slotPool, o.Count, o.CommonWordSlots, func(c algorithmic.Candidate) float64 {
			return scorer.ScoreWithoutCommonWordPenalty(c, o.Tokens)
		})
	}
	return final, st
}
