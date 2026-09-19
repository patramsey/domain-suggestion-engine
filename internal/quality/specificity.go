package quality

import "github.com/patlivet/domain-suggestion-engine/internal/scorer"

// Specificity is how much more relevant sld is to its own query than, on
// average, to the other queries in the set: positive means the name belongs
// to this concept, near zero means it would fit any business. ok is false
// when relevance to the own query, or to every other query, cannot be
// computed (e.g. a coined name with no recognisable sub-words).
func Specificity(sld string, query []string, others [][]string) (float64, bool) {
	own, ok := scorer.ConceptRelevance(sld, query)
	if !ok {
		return 0, false
	}
	var sum float64
	n := 0
	for _, o := range others {
		if r, ok := scorer.ConceptRelevance(sld, o); ok {
			sum += r
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return own - sum/float64(n), true
}
