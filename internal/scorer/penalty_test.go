package scorer

import (
	"math"
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
)

func TestAvailabilityPenaltyByWordLevel(t *testing.T) {
	roomy := "zzz-test-roomy"                                                         // not in tldFreeRate → neutral
	common := availabilityPenalty(algorithmic.Candidate{SLD: "late", TLD: roomy})     // SCOWL 10
	level35 := availabilityPenalty(algorithmic.Candidate{SLD: "tether", TLD: roomy})  // SCOWL 35
	rare := availabilityPenalty(algorithmic.Candidate{SLD: "catalyst", TLD: roomy})   // SCOWL 40
	coined := availabilityPenalty(algorithmic.Candidate{SLD: "hopsmith", TLD: roomy}) // not a word
	tldPart := crowdingWeight * (1 - neutralFreeRate)
	for name, got := range map[string]float64{
		"common": common - commonWordPenalty, "level35": level35 - level35Penalty, "rare": rare, "coined": coined,
	} {
		if math.Abs(got-tldPart) > 1e-9 {
			t.Errorf("%s: word part wrong (penalty minus word part = %v, want %v)", name, got, tldPart)
		}
	}
}

func TestAvailabilityPenaltyCrowdedTLD(t *testing.T) {
	var crowded, roomy string
	lo, hi := 2.0, -1.0
	for tld, r := range tldFreeRate {
		if r < lo {
			lo, crowded = r, tld
		}
		if r > hi {
			hi, roomy = r, tld
		}
	}
	pc := availabilityPenalty(algorithmic.Candidate{SLD: "hopsmith", TLD: crowded})
	pr := availabilityPenalty(algorithmic.Candidate{SLD: "hopsmith", TLD: roomy})
	if pc <= pr {
		t.Errorf("crowded %s penalty %v should exceed roomy %s penalty %v", crowded, pc, roomy, pr)
	}
}

func TestScoreSubtractsPenalty(t *testing.T) {
	c := algorithmic.Candidate{SLD: "late", TLD: "cafe", Source: "algo"}
	if got, want := Score(c, nil), clamp(baseScore(c, nil)-availabilityPenalty(c)); math.Abs(got-want) > 1e-9 {
		t.Errorf("Score = %v, want base − penalty = %v", got, want)
	}
}
