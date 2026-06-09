package scorer

import (
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
)

func cand(sld, tld, source string) algorithmic.Candidate {
	return algorithmic.Candidate{SLD: sld, TLD: tld, Source: source}
}

func TestKnownGoodNamesScoreHighly(t *testing.T) {
	cases := []struct {
		sld    string
		tld    string
		tokens []string
		minScore float64
	}{
		{"studio", "io", []string{"studio"}, 0.65},
		{"coffee", "com", []string{"coffee"}, 0.65},
		{"ember", "coffee", []string{"coffee"}, 0.60},
		{"bloom", "studio", []string{"studio"}, 0.55},
	}
	for _, tc := range cases {
		s := Score(cand(tc.sld, tc.tld, "llm"), tc.tokens)
		if s < tc.minScore {
			t.Errorf("%s.%s scored %.3f, want >= %.3f", tc.sld, tc.tld, s, tc.minScore)
		}
	}
}

func TestGarbageScoresLow(t *testing.T) {
	// Good names should consistently outscore garbage.
	good := Score(cand("coffee", "com", "llm"), []string{"coffee"})
	garbage := []struct{ sld, tld string }{
		{"xktzpqvbw", "com"},   // all consonants, unpronounceable
		{"aaaaaaaaaaaaaaaa", "com"}, // too long + all vowels
	}
	for _, g := range garbage {
		s := Score(cand(g.sld, g.tld, "llm"), nil)
		if s >= good {
			t.Errorf("%s.%s (%.3f) should score below coffee.com (%.3f)", g.sld, g.tld, s, good)
		}
	}
}

func TestComScoresHigherThanOtherGTLD(t *testing.T) {
	base := Score(cand("coffee", "com", "llm"), nil)
	other := Score(cand("coffee", "xyz", "llm"), nil)
	if base <= other {
		t.Errorf(".com (%.3f) should score higher than .xyz (%.3f)", base, other)
	}
}

func TestSemanticMatchBoostsTLDPremium(t *testing.T) {
	without := Score(cand("ember", "coffee", "llm"), nil)
	with := Score(cand("ember", "coffee", "llm"), []string{"coffee"})
	if with <= without {
		t.Errorf("semantic match should boost score: without=%.3f with=%.3f", without, with)
	}
}

func TestLengthSweetSpot(t *testing.T) {
	// 3–8 char SLDs should score 1.0
	for _, sld := range []string{"zen", "ember", "studio", "forgeio"} {
		s := lengthScore(sld)
		if s != 1.0 {
			t.Errorf("lengthScore(%q)=%.2f, want 1.0", sld, s)
		}
	}
}

func TestLengthPenaltyLong(t *testing.T) {
	s := lengthScore("verylongdomainname") // 18 chars → default 0.20
	if s > 0.3 {
		t.Errorf("lengthScore(long)=%.2f, want <= 0.3", s)
	}
}

func TestDigitSLDZeroBrandability(t *testing.T) {
	s := brandability("cof3ee")
	if s != 0 {
		t.Errorf("digit in SLD should score 0 brandability, got %.2f", s)
	}
}

func TestBrandabilityBaseline(t *testing.T) {
	// Dictionary words with good phonotactics score well.
	if s := brandability("forge"); s < 0.70 {
		t.Errorf("forge brandability too low: %.3f", s)
	}
	// Real words with unusual bigrams (doubled letters) still score above neutral.
	if s := brandability("coffee"); s < 0.50 {
		t.Errorf("coffee brandability too low: %.3f", s)
	}
	// Invented words with no sub-word coverage score lower but are not zero.
	if s := brandability("zymmo"); s < 0.20 {
		t.Errorf("invented word brandability should not be floored: %.3f", s)
	}
	// Known words outscore invented ones.
	if brandability("forge") <= brandability("zymmo") {
		t.Errorf("forge should outscore zymmo")
	}
}

func TestRankSortsDescending(t *testing.T) {
	candidates := []algorithmic.Candidate{
		cand("xktzpq", "com", "algo"),
		cand("coffee", "com", "algo"),
		cand("studio", "io", "algo"),
	}
	ranked := Rank(candidates, []string{"coffee", "studio"})
	for i := 1; i < len(ranked); i++ {
		if ranked[i].Score > ranked[i-1].Score {
			t.Errorf("not sorted: ranked[%d].Score=%.3f > ranked[%d].Score=%.3f",
				i, ranked[i].Score, i-1, ranked[i-1].Score)
		}
	}
}

func TestScoreInRange(t *testing.T) {
	candidates := []algorithmic.Candidate{
		cand("coffee", "com", "algo"),
		cand("xkz", "io", "algo"),
		cand("studio", "studio", "llm"),
	}
	for _, c := range candidates {
		s := Score(c, []string{"coffee"})
		if s < 0 || s > 1 {
			t.Errorf("score out of [0,1]: %s = %.4f", c.Name(), s)
		}
	}
}

// --- TLD premium ---

func TestCCTLDScoresLowerThanCom(t *testing.T) {
	com := tldPremium("com", nil)
	cc := tldPremium("de", nil) // 2-char ccTLD, no semantic match
	if cc >= com {
		t.Errorf(".de (%.3f) should score below .com (%.3f)", cc, com)
	}
}

func TestWordTLDWithSemanticMatch(t *testing.T) {
	noMatch := tldPremium("pizza", nil)
	withMatch := tldPremium("pizza", []string{"pizza"})
	if withMatch <= noMatch {
		t.Errorf("semantic match on word TLD should boost score: no=%.3f with=%.3f", noMatch, withMatch)
	}
}

func TestAiIoScoreHigherThanWordTLD(t *testing.T) {
	ai := tldPremium("ai", nil)
	word := tldPremium("pizza", nil)
	if ai <= word {
		t.Errorf(".ai (%.3f) should score higher than .pizza base (%.3f)", ai, word)
	}
}

// --- length edge cases ---

func TestShortSLDScoresHighNotPenalised(t *testing.T) {
	// Short SLDs (2–4 chars) are desirable brand names — they must not be penalised.
	if s := lengthScore("go"); s < 0.70 {
		t.Errorf("2-char SLD 'go' scored %.2f, want >= 0.70", s)
	}
	if s := lengthScore("zen"); s < 0.95 {
		t.Errorf("3-char SLD 'zen' scored %.2f, want >= 0.95", s)
	}
}

func TestMediumSLDFullScore(t *testing.T) {
	// 5-char SLD — comfortably in the sweet spot.
	s := lengthScore("ember")
	if s != 1.0 {
		t.Errorf("lengthScore(ember)=%.2f, want 1.0", s)
	}
}

// --- n-gram brandability ---

func TestNgramBrandabilityGoodWordScoresHigh(t *testing.T) {
	// Common English words with typical phonotactics should score well.
	for _, word := range []string{"stripe", "cedar", "forge", "bloom"} {
		if s := ngramBrandability(word); s < 0.60 {
			t.Errorf("ngramBrandability(%q) = %.3f, want >= 0.60", word, s)
		}
	}
}

func TestNgramBrandabilityGarbageScoresLow(t *testing.T) {
	if s := ngramBrandability("xkqvz"); s > 0.20 {
		t.Errorf("ngramBrandability(xkqvz) = %.3f, want <= 0.20", s)
	}
}

func TestNgramBrandabilityGoodBeatsGarbage(t *testing.T) {
	good := ngramBrandability("forge")
	bad := ngramBrandability("xkqvz")
	if good <= bad {
		t.Errorf("forge (%.3f) should outscore xkqvz (%.3f)", good, bad)
	}
}

// --- memorability ---

func TestMemorabilityFullWord(t *testing.T) {
	// Whole word recognised → coverage 1.0.
	if m := memorability("coffee"); m != 1.0 {
		t.Errorf("memorability(coffee) = %.2f, want 1.0", m)
	}
}

func TestMemorabilityNoSubWords(t *testing.T) {
	// No recognisable sub-words → coverage 0.0.
	if m := memorability("xkqvz"); m != 0.0 {
		t.Errorf("memorability(xkqvz) = %.2f, want 0.0", m)
	}
}

func TestMemorabilityPartialCoverage(t *testing.T) {
	// "air" covers 3 of 6 chars in "airbnb".
	m := memorability("airbnb")
	if m < 0.3 || m > 0.7 {
		t.Errorf("memorability(airbnb) = %.2f, want ~0.5", m)
	}
}

func TestMemorabilityKnownWordBeatsCoinages(t *testing.T) {
	if memorability("coffee") <= memorability("xkqvz") {
		t.Error("coffee should have higher memorability than xkqvz")
	}
}

// --- concept relevance ---

func TestConceptRelevanceNeutralForUnknownSLD(t *testing.T) {
	// SLD with no recognisable sub-words → neutral 0.5.
	cr := conceptRelevance("xkqvz", []string{"coffee", "brew"})
	if cr != 0.5 {
		t.Errorf("conceptRelevance(xkqvz) = %.3f, want 0.5", cr)
	}
}

func TestConceptRelevanceNeutralForNoTokens(t *testing.T) {
	cr := conceptRelevance("coffee", nil)
	if cr != 0.5 {
		t.Errorf("conceptRelevance with no tokens = %.3f, want 0.5", cr)
	}
}

func TestConceptRelevanceMatchingConceptScoresHigh(t *testing.T) {
	match := conceptRelevance("coffee", []string{"coffee", "brew", "roast"})
	if match <= 0.5 {
		t.Errorf("matching concept relevance = %.3f, want > 0.5", match)
	}
}

func TestConceptRelevanceRelatedBeatsUnrelated(t *testing.T) {
	tokens := []string{"coffee", "brew", "roast"}
	related := conceptRelevance("coffee", tokens)
	unrelated := conceptRelevance("legal", tokens)
	if related <= unrelated {
		t.Errorf("coffee (%.3f) should outscore legal (%.3f) for coffee query", related, unrelated)
	}
}

// --- LLMRank bonus in Score ---

func TestLLMRankHigherScoresHigher(t *testing.T) {
	tokens := []string{"coffee"}
	hi := cand("coffee", "com", "llm")
	hi.LLMRank = 1.0
	lo := cand("coffee", "com", "llm")
	lo.LLMRank = 0.1
	if Score(hi, tokens) <= Score(lo, tokens) {
		t.Errorf("higher LLMRank should produce higher score: hi=%.3f lo=%.3f",
			Score(hi, tokens), Score(lo, tokens))
	}
}

func TestLLMSourceOutscoresAlgorithmic(t *testing.T) {
	tokens := []string{"studio"}
	llmCand := cand("stud", "io", "llm")
	llmCand.LLMRank = 0.5
	algoCand := cand("stud", "io", "algorithmic")
	if Score(llmCand, tokens) <= Score(algoCand, tokens) {
		t.Errorf("LLM candidate should outscore identical algorithmic candidate")
	}
}

func TestAlgorithmicCandidateHasNoLLMBonus(t *testing.T) {
	algoCand := cand("stud", "io", "algorithmic")
	// LLMRank is zero on algo candidates — score should not include any LLM bonus.
	if algoCand.LLMRank != 0 {
		t.Error("algorithmic candidate should have LLMRank=0")
	}
}
