package main

import (
	"math"
	"testing"
)

// A feature that always favours the better name must get a positive weight,
// and a pure-noise feature a far smaller one.
func TestFitRecoversASignal(t *testing.T) {
	var diffs [][]float64
	for i := range 200 {
		noise := float64(i%7)/7 - 0.5
		diffs = append(diffs, []float64{0.4, noise})
	}
	w := fit(diffs, 400, 0.05, 1e-4)
	if w[0] <= 0 {
		t.Errorf("signal weight = %v, want positive", w[0])
	}
	if math.Abs(w[1]) > math.Abs(w[0]) {
		t.Errorf("noise weight %v should be smaller than signal %v", w[1], w[0])
	}
}

func TestPairAccuracy(t *testing.T) {
	diffs := [][]float64{{1, 0}, {-1, 0}, {0.5, 0}} // 2 of 3 ordered correctly
	if got := pairAccuracy(diffs, []float64{1, 0}); math.Abs(got-2.0/3) > 1e-9 {
		t.Errorf("accuracy = %v, want 2/3", got)
	}
	// A weight vector of zeros cannot order anything: every pair is a tie.
	if got := pairAccuracy(diffs, []float64{0, 0}); got != 0.5 {
		t.Errorf("tie accuracy = %v, want 0.5", got)
	}
}

func TestFoldsGroupByQuery(t *testing.T) {
	queries := []string{"a", "b", "c", "d", "e"}
	folds := foldsByQuery(queries, 2)
	if len(folds) != 2 {
		t.Fatalf("got %d folds, want 2", len(folds))
	}
	seen := map[string]int{}
	for _, f := range folds {
		for _, q := range f {
			seen[q]++
		}
	}
	for _, q := range queries {
		if seen[q] != 1 {
			t.Errorf("query %q appears in %d folds, want 1", q, seen[q])
		}
	}
}
