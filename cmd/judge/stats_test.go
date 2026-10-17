package main

import (
	"math"
	"testing"
)

// A pair is one observation: both orders agreeing count once, not twice.
func TestOutcomesScoreEachPairOnce(t *testing.T) {
	pairs := []pair{
		{ID: "p1", Query: "q1", A: "x", B: "y"},
		{ID: "p2", Query: "q1", A: "x", B: "z"},
		{ID: "p3", Query: "q2", A: "x", B: "w"},
	}
	winners := map[string]string{
		"p1": "a", "p1r": "b", // A wins both orders
		"p2": "a", "p2r": "a", // A forward, B reversed: a split
		"p3": "b", // only one order answered: B
	}
	got := outcomes(pairs, winners)
	want := map[string]float64{"p1": 1, "p2": 0.5, "p3": 0}
	if len(got) != 3 {
		t.Fatalf("got %d outcomes, want 3", len(got))
	}
	for _, o := range got {
		if o.score != want[o.id] {
			t.Errorf("%s: score %v, want %v", o.id, o.score, want[o.id])
		}
	}
}

// When one query is all wins and another all losses, the honest conclusion
// is "no idea" — two independent observations. Counting answers made this
// look like hundreds.
func TestBootstrapByQueryReflectsClustering(t *testing.T) {
	var outs []pairOutcome
	for range 100 {
		outs = append(outs, pairOutcome{query: "a", score: 1}, pairOutcome{query: "b", score: 0})
	}
	st := pairStats(outs, 1)
	if math.Abs(st.rate-0.5) > 1e-9 {
		t.Errorf("rate = %v, want 0.5", st.rate)
	}
	if st.lo > 0.05 || st.hi < 0.95 {
		t.Errorf("CI = %.2f–%.2f; with two queries it should span almost 0–1", st.lo, st.hi)
	}
	if st.pairs != 200 || st.queries != 2 {
		t.Errorf("counted %d pairs over %d queries, want 200 over 2", st.pairs, st.queries)
	}
}

func TestBootstrapNarrowsWithMoreQueries(t *testing.T) {
	var outs []pairOutcome
	for q := range 40 {
		for i := range 10 {
			s := 0.0
			if i < 6 {
				s = 1 // 60% wins, spread evenly over 40 queries
			}
			outs = append(outs, pairOutcome{query: string(rune('A' + q)), score: s})
		}
	}
	st := pairStats(outs, 1)
	if st.lo <= 0.5 {
		t.Errorf("CI %.3f–%.3f: a consistent 60%% over 40 queries should clear 50%%", st.lo, st.hi)
	}
	again := pairStats(outs, 1)
	if again != st {
		t.Error("the bootstrap must be deterministic for a fixed seed")
	}
}

// With a paired design most names appear in both arms; only the ones that
// differ say anything about the configurations.
func TestOnlyDifferences(t *testing.T) {
	a := map[string][]string{"q": {"shared.com", "only-a.com", "also-shared.io"}}
	b := map[string][]string{"q": {"also-shared.io", "shared.com", "only-b.com"}, "other": {"x.com"}}
	da, db := onlyDifferences(a, b)
	if len(da["q"]) != 1 || da["q"][0] != "only-a.com" {
		t.Errorf("A side = %v, want [only-a.com]", da["q"])
	}
	if len(db["q"]) != 1 || db["q"][0] != "only-b.com" {
		t.Errorf("B side = %v, want [only-b.com]", db["q"])
	}
	if _, ok := db["other"]; ok {
		t.Error("a query missing from one side cannot be paired and should be dropped")
	}
}
