package main

import "testing"

func TestBandsSplitsByScoreOrder(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g"} // already best-first
	got := bands(names, 3)
	if len(got) != 3 {
		t.Fatalf("got %d bands, want 3", len(got))
	}
	if got[0][0] != "a" {
		t.Errorf("band 0 should start with the best name, got %v", got[0])
	}
	total := 0
	for _, b := range got {
		total += len(b)
	}
	if total != len(names) {
		t.Errorf("bands dropped names: %d of %d", total, len(names))
	}
	if b := bands([]string{"a"}, 3); len(b) != 1 {
		t.Errorf("one name cannot fill 3 bands: %v", b)
	}
}

func TestCrossBandPairsRecordTheGap(t *testing.T) {
	ranked := map[string][]string{"tea": {"a", "b", "c", "d", "e", "f"}}
	pairs := crossBandPairs(ranked, 3, 4, 1)
	if len(pairs) == 0 {
		t.Fatal("no pairs built")
	}
	for _, p := range pairs {
		if p.Gap < 1 || p.Gap > 2 {
			t.Errorf("gap %d out of range for 3 bands", p.Gap)
		}
		if p.Better == "" {
			t.Error("the higher-scored name should be recorded as Better")
		}
		if p.Better != p.A && p.Better != p.B {
			t.Errorf("Better %q is neither side", p.Better)
		}
	}
}

func TestRankAgreementByGap(t *testing.T) {
	pairs := []bandPair{
		{pair: pair{ID: "s0", A: "hi", B: "lo", Better: "hi"}, Gap: 1},
		{pair: pair{ID: "s1", A: "lo2", B: "hi2", Better: "hi2"}, Gap: 2},
	}
	// s0: both orders back the higher-scored name (A forward, B reversed).
	// s1: both orders back the lower-scored one, which sits at A forward.
	winners := map[string]string{"s0": "a", "s0r": "b", "s1": "a", "s1r": "b"}
	got := rankAgreement(pairs, winners)
	if got[1].Answers != 2 || got[1].Correct != 2 {
		t.Errorf("gap 1: %+v, want 2 of 2", got[1])
	}
	if got[2].Answers != 2 || got[2].Correct != 0 {
		t.Errorf("gap 2: %+v, want 0 of 2", got[2])
	}
}

// Two sides of a comparison can hold the same candidate (comparing two
// rankings of one pool); a name must never be pitted against itself.
func TestPairUpSkipsIdenticalNames(t *testing.T) {
	same := map[string][]string{"tea": {"steep.shop", "leafy.io"}}
	for _, p := range pairUp(same, same, 4, 1) {
		if p.A == p.B {
			t.Errorf("pair %s has the same name on both sides: %s", p.ID, p.A)
		}
	}
}
