package api

import (
	"math"
	"sort"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
	"github.com/patlivet/domain-suggestion-engine/internal/wordlist"
)

// reserveCommonWords keeps very common single words (SCOWL ≤ 20: "mint",
// "forge") in the results. They are strong names but usually registered, so
// the availability penalty would otherwise push nearly all of them out; the
// caller learns which are taken and refines with unavailable_domains.
//
// Up to round(count × slotsPer10 / 10) of the best such words — chosen from
// pool and scored with rescore (the score without the common-word penalty) —
// are placed in the results. Each one not already shown replaces the
// lowest-scoring other name, from the same tier where possible so the
// LLM/algorithmic balance holds, or fills a free slot. Pool entries have
// already passed the TLD diversity cap and the unavailable_domains filter;
// SLDs stay unique.
//
// The result is laid out in blocks of 10 (a page): each block holds at most
// slotsPer10 common words, the best remaining ones, and is sorted by score.
// Without the cap the reserved words, scored without their penalty, would
// usually take the first few positions.
func reserveCommonWords(final, pool []scorer.ScoredCandidate, count, slotsPer10 int, rescore func(algorithmic.Candidate) float64) []scorer.ScoredCandidate {
	want := int(math.Round(float64(count*slotsPer10) / 10))
	if want <= 0 {
		return final
	}

	// best-scoring common word per SLD, by the rescored score
	best := map[string]scorer.ScoredCandidate{}
	for _, list := range [][]scorer.ScoredCandidate{final, pool} {
		for _, c := range list {
			if !wordlist.IsCommon(c.SLD) {
				continue
			}
			r := c
			r.Score = rescore(c.Candidate)
			if cur, ok := best[c.SLD]; !ok || r.Score > cur.Score {
				best[c.SLD] = r
			}
		}
	}
	chosen := make([]scorer.ScoredCandidate, 0, len(best))
	for _, c := range best {
		chosen = append(chosen, c)
	}
	sort.Slice(chosen, func(i, j int) bool {
		if chosen[i].Score != chosen[j].Score {
			return chosen[i].Score > chosen[j].Score
		}
		return chosen[i].Name() < chosen[j].Name()
	})
	if len(chosen) > want {
		chosen = chosen[:want]
	}

	out := make([]scorer.ScoredCandidate, len(final))
	copy(out, final)
	for _, c := range chosen {
		if i := indexOfSLD(out, c.SLD); i >= 0 {
			out[i] = c // already shown: show the chosen TLD at its rescored score
			continue
		}
		if len(out) < count {
			out = append(out, c)
			continue
		}
		if i := lowestReplaceable(out, c.Source); i >= 0 {
			out[i] = c
		}
	}
	return layoutBlocks(out, slotsPer10)
}

// resultBlock is the page size the per-block cap applies to.
const resultBlock = 10

// layoutBlocks orders list in blocks of resultBlock, each holding at most
// maxCommon common words (more only when nothing else is left), each sorted
// by score.
func layoutBlocks(list []scorer.ScoredCandidate, maxCommon int) []scorer.ScoredCandidate {
	var commons, others []scorer.ScoredCandidate
	for _, c := range list {
		if wordlist.IsCommon(c.SLD) {
			commons = append(commons, c)
		} else {
			others = append(others, c)
		}
	}
	byScore := func(s []scorer.ScoredCandidate) {
		sort.SliceStable(s, func(i, j int) bool { return s[i].Score > s[j].Score })
	}
	byScore(commons)
	byScore(others)

	out := make([]scorer.ScoredCandidate, 0, len(list))
	for len(commons)+len(others) > 0 {
		size := min(resultBlock, len(commons)+len(others))
		nc := min(maxCommon, len(commons))
		no := min(size-nc, len(others))
		nc = size - no // backfill with common words when others run out
		block := append(append([]scorer.ScoredCandidate{}, commons[:nc]...), others[:no]...)
		commons, others = commons[nc:], others[no:]
		byScore(block)
		out = append(out, block...)
	}
	return out
}

func indexOfSLD(list []scorer.ScoredCandidate, sld string) int {
	for i, c := range list {
		if c.SLD == sld {
			return i
		}
	}
	return -1
}

// lowestReplaceable returns the index of the lowest-scoring name that is not
// a common word, preferring one from source; -1 if every name is a common word.
func lowestReplaceable(list []scorer.ScoredCandidate, source string) int {
	pick := func(sameSource bool) int {
		idx := -1
		for i, c := range list {
			if wordlist.IsCommon(c.SLD) || (sameSource && c.Source != source) {
				continue
			}
			if idx < 0 || c.Score < list[idx].Score {
				idx = i
			}
		}
		return idx
	}
	if i := pick(true); i >= 0 {
		return i
	}
	return pick(false)
}
