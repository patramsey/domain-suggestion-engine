package api

import (
	"sort"

	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
)

// backfillToCount tops up final from the ranked pool when the TLD diversity
// cap and tier balance leave fewer than count names — better a slightly less
// diverse result set than a short one. ranked is the full scored list before
// the cap, already filtered for unavailable_domains; SLDs stay unique and the
// result stays sorted by score.
func backfillToCount(final, ranked []scorer.ScoredCandidate, count int) []scorer.ScoredCandidate {
	if len(final) >= count {
		return final
	}
	used := make(map[string]struct{}, len(final))
	for _, c := range final {
		used[c.SLD] = struct{}{}
	}
	out := final
	for _, c := range ranked {
		if len(out) >= count {
			break
		}
		if _, dup := used[c.SLD]; dup {
			continue
		}
		used[c.SLD] = struct{}{}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}
