package scorer

import (
	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/wordlist"
)

// FeatureSet is every signal behind a candidate's score, exposed one by one.
// Score combines them with fixed weights; this is for fitting those weights
// against rated names (cmd/fitweights) and for explaining a score.
type FeatureSet struct {
	Brandability        float64 // the 0.40 signal
	ConceptRelevance    float64 // the 0.30 signal
	TLDPremium          float64 // the 0.15 signal
	LengthScore         float64 // the 0.15 signal
	LLMBonus            float64 // position bonus, 0 for algorithmic candidates
	AvailabilityPenalty float64 // subtracted from the weighted sum

	// Parts and side information, not used by Score itself.
	NGram        float64 // phonotactic half of brandability
	Memorability float64 // sub-word half of brandability
	TLDFreeRate  float64 // share of probe words free on this TLD
	SLDLen       int
	CommonWord   bool // SCOWL ≤ wordlist.CommonMaxLevel
	FromLLM      bool

	// quality.IsCompound and quality.IsTypo are not here: that package imports
	// this one, so callers that want those flags add them (see cmd/fitweights).
}

// Features computes every signal for one candidate.
func Features(c algorithmic.Candidate, tokens []string) FeatureSet {
	fr, ok := tldFreeRate[c.TLD]
	if !ok {
		fr = neutralFreeRate
	}
	f := FeatureSet{
		Brandability:        brandability(c.SLD),
		ConceptRelevance:    conceptRelevance(c.SLD, tokens),
		TLDPremium:          tldPremium(c.SLD, c.TLD, tokens),
		LengthScore:         lengthScore(c.SLD),
		AvailabilityPenalty: availabilityPenalty(c),
		NGram:               ngramBrandability(c.SLD),
		Memorability:        memorability(c.SLD),
		TLDFreeRate:         fr,
		SLDLen:              len(c.SLD),
		CommonWord:          wordlist.IsCommon(c.SLD),
		FromLLM:             c.Source == "llm",
	}
	if f.FromLLM {
		f.LLMBonus = 0.03 + c.LLMRank*0.04
	}
	return f
}

// AdoptionScore is a TLD's real-world adoption score from the generated
// table, derived from the Majestic Million and the IANA root zone. ok is
// false for a TLD missing from the table. Callers use it to tell the
// infrastructure TLDs (com .82, org .77, net .74) from ordinary ones — the
// next-highest is dev at .63, so a threshold of 0.70 sits in that gap.
func AdoptionScore(tld string) (float64, bool) {
	s, ok := tldScores[tld]
	return s, ok
}
