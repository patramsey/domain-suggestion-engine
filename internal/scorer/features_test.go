package scorer

import (
	"math"
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
)

func TestFeaturesMatchTheScore(t *testing.T) {
	tokens := []string{"coffee", "shop"}
	c := algorithmic.Candidate{SLD: "duskbrew", TLD: "cafe", Source: "llm", LLMRank: 0.5}
	f := Features(c, tokens)

	// The weighted sum of the four signals plus the LLM bonus, minus the
	// penalty, must reproduce Score exactly — otherwise a fitted model would
	// be learning from features the engine does not actually use.
	want := clamp(f.Brandability*0.40 + f.ConceptRelevance*0.30 + f.TLDPremium*0.15 + f.LengthScore*0.15 + f.LLMBonus)
	if math.Abs(clamp(want-f.AvailabilityPenalty)-Score(c, tokens)) > 1e-9 {
		t.Errorf("features do not reconstruct Score: %+v", f)
	}
	if f.NGram == 0 || f.Memorability == 0 {
		t.Errorf("brandability parts should be exposed separately: %+v", f)
	}
}

func TestFeaturesFlags(t *testing.T) {
	tokens := []string{"tea"}
	common := Features(algorithmic.Candidate{SLD: "mint", TLD: "shop"}, tokens)
	if !common.CommonWord || common.AvailabilityPenalty < commonWordPenalty {
		t.Errorf("mint is a very common word: %+v", common)
	}
	algo := Features(algorithmic.Candidate{SLD: "steep", TLD: "shop", Source: "algorithmic"}, tokens)
	if algo.LLMBonus != 0 {
		t.Errorf("algorithmic candidates get no LLM bonus: %v", algo.LLMBonus)
	}
	if algo.SLDLen != 5 {
		t.Errorf("SLDLen = %d, want 5", algo.SLDLen)
	}
}

func TestAdoptionScore(t *testing.T) {
	com, ok := AdoptionScore("com")
	if !ok || com < 0.8 {
		t.Errorf("com adoption = %v, ok = %v; want the highest in the table", com, ok)
	}
	dev, _ := AdoptionScore("dev")
	if dev >= com {
		t.Errorf("dev %v should rank below com %v", dev, com)
	}
	if _, ok := AdoptionScore("zzz-not-a-tld"); ok {
		t.Error("unknown TLD should report ok=false")
	}
}
