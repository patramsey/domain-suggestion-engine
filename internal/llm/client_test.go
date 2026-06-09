package llm

import "testing"

// --- extractPartialPairs ---

func TestExtractPartialPairsFromTruncatedArray(t *testing.T) {
	// Simulates a response cut off mid-array (common when LLM hits output limit).
	truncated := `[{"sld":"forge","tld":"io"},{"sld":"cedar","tld":"co"},{"sl`
	pairs := extractPartialPairs(truncated)
	if len(pairs) != 2 {
		t.Fatalf("expected 2 recovered pairs, got %d", len(pairs))
	}
	if pairs[0].SLD != "forge" || pairs[0].TLD != "io" {
		t.Errorf("unexpected pair[0]: %+v", pairs[0])
	}
	if pairs[1].SLD != "cedar" || pairs[1].TLD != "co" {
		t.Errorf("unexpected pair[1]: %+v", pairs[1])
	}
}

func TestExtractPartialPairsEmpty(t *testing.T) {
	if pairs := extractPartialPairs("no json here"); len(pairs) != 0 {
		t.Errorf("expected 0 pairs from non-JSON input, got %d", len(pairs))
	}
}

func TestExtractJSONFallsBackToPartial(t *testing.T) {
	// Full array parse fails (truncated), partial recovery should succeed.
	truncated := `[{"sld":"forge","tld":"io"},{"sld":"cedar","tl`
	pairs, err := extractJSON(truncated)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if len(pairs) < 1 {
		t.Error("expected at least one recovered pair")
	}
}

func TestExtractJSONFullArrayStillWorks(t *testing.T) {
	input := `[{"sld":"forge","tld":"io"},{"sld":"cedar","tld":"co"}]`
	pairs, err := extractJSON(input)
	if err != nil {
		t.Fatalf("full array parse failed: %v", err)
	}
	if len(pairs) != 2 {
		t.Errorf("expected 2 pairs, got %d", len(pairs))
	}
}

// --- rankedCandidates ---

func TestRankedCandidatesLLMRankDecreasing(t *testing.T) {
	tldSet := map[string]struct{}{"io": {}, "co": {}, "com": {}}
	variant := []rawPair{
		{SLD: "forge", TLD: "io"},
		{SLD: "cedar", TLD: "co"},
		{SLD: "ember", TLD: "com"},
	}
	cands := rankedCandidates([][]rawPair{variant}, tldSet)
	if len(cands) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(cands))
	}
	// Rank should strictly decrease from first to last.
	for i := 1; i < len(cands); i++ {
		if cands[i].LLMRank >= cands[i-1].LLMRank {
			t.Errorf("rank should decrease: [%d]=%.3f >= [%d]=%.3f",
				i, cands[i].LLMRank, i-1, cands[i-1].LLMRank)
		}
	}
	// All ranks must be positive.
	for i, c := range cands {
		if c.LLMRank <= 0 {
			t.Errorf("cands[%d].LLMRank = %.3f, want > 0", i, c.LLMRank)
		}
	}
}

func TestRankedCandidatesCrossVariantDedup(t *testing.T) {
	tldSet := map[string]struct{}{"io": {}, "co": {}}
	v1 := []rawPair{{SLD: "forge", TLD: "io"}, {SLD: "cedar", TLD: "co"}}
	v2 := []rawPair{{SLD: "forge", TLD: "io"}, {SLD: "ember", TLD: "co"}} // forge duplicated
	cands := rankedCandidates([][]rawPair{v1, v2}, tldSet)

	if len(cands) != 3 {
		t.Errorf("expected 3 unique candidates, got %d", len(cands))
	}
	count := 0
	for _, c := range cands {
		if c.SLD == "forge" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("forge should appear exactly once after dedup, got %d", count)
	}
}

func TestRankedCandidatesAllSourcedAsLLM(t *testing.T) {
	tldSet := map[string]struct{}{"io": {}}
	cands := rankedCandidates([][]rawPair{{{SLD: "forge", TLD: "io"}}}, tldSet)
	for _, c := range cands {
		if c.Source != "llm" {
			t.Errorf("expected source=llm, got %q", c.Source)
		}
	}
}
