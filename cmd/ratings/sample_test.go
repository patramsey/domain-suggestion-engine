package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func spec(v float64) *float64 { return &v }

func testPool() []keyEntry {
	var pool []keyEntry
	for i := 0; i < 60; i++ {
		e := keyEntry{Query: fmt.Sprintf("q%d", i%6), Domain: fmt.Sprintf("name%d.com", i), Specificity: spec(float64(i) / 100)}
		e.Typo = i%10 == 0      // 6 typos
		e.CommonWord = i%7 == 0 // 9 common words
		pool = append(pool, e)
	}
	return pool
}

func TestSampleDeterministicAndSized(t *testing.T) {
	a := sample(testPool(), 30, 1)
	b := sample(testPool(), 30, 1)
	if len(a) != 30 {
		t.Fatalf("len = %d, want 30", len(a))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Domain != b[i].Domain || a[i].Query != b[i].Query {
			t.Fatalf("same seed gave different samples at %d", i)
		}
	}
}

func TestSampleIncludesEveryFlaggedBucket(t *testing.T) {
	s := sample(testPool(), 30, 1)
	var typo, common int
	ids := map[string]bool{}
	for _, e := range s {
		if e.Typo {
			typo++
		}
		if e.CommonWord {
			common++
		}
		if ids[e.ID] {
			t.Errorf("duplicate id %s", e.ID)
		}
		ids[e.ID] = true
	}
	if typo < 5 || common < 5 {
		t.Errorf("flagged coverage too low: typo=%d common=%d", typo, common)
	}
}

func TestBlindItemsHideMetrics(t *testing.T) {
	data, _ := json.Marshal(blind(sample(testPool(), 10, 1)))
	for _, leak := range []string{"typo", "common", "specificity", "model"} {
		if strings.Contains(strings.ToLower(string(data)), leak) {
			t.Errorf("blind items leak %q: %s", leak, data)
		}
	}
}

func TestPoolFromSnapshotsDedupes(t *testing.T) {
	snaps := []snapshot{
		{Model: "m1", Results: []snapResult{{Query: "q", Quality: &struct{}{}, Suggestions: []snapSuggestion{{Name: "a.com", Typo: true}}}}},
		{Model: "m2", Results: []snapResult{{Query: "q", Quality: &struct{}{}, Suggestions: []snapSuggestion{{Name: "a.com", Typo: true}}}}},
	}
	pool, err := poolFrom(snaps, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) != 1 || len(pool[0].Models) != 2 {
		t.Errorf("pool = %+v", pool)
	}
	snaps[0].Results[0].Quality = nil
	if _, err := poolFrom(snaps[:1], 0); err == nil {
		t.Error("expected error for snapshot without quality annotations")
	}
}

func TestPoolFromTopN(t *testing.T) {
	snaps := []snapshot{{Model: "m", Results: []snapResult{{Query: "q", Quality: &struct{}{}, Suggestions: []snapSuggestion{
		{Name: "low.com", Score: 0.1}, {Name: "high.com", Score: 0.9}, {Name: "mid.com", Score: 0.5},
	}}}}}
	pool, err := poolFrom(snaps, 2)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range pool {
		names = append(names, e.Domain)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "high.com,mid.com" {
		t.Errorf("top-2 pool = %v", names)
	}
}

func TestSampleByModelBalancedAndExcludesShared(t *testing.T) {
	var pool []keyEntry
	for i := 0; i < 30; i++ {
		pool = append(pool, keyEntry{Query: "q", Domain: fmt.Sprintf("a%d.com", i), Models: []string{"m1"}})
	}
	for i := 0; i < 8; i++ {
		pool = append(pool, keyEntry{Query: "q", Domain: fmt.Sprintf("b%d.com", i), Models: []string{"m2"}})
	}
	pool = append(pool, keyEntry{Query: "q", Domain: "shared.com", Models: []string{"m1", "m2"}})
	s := sampleByModel(pool, 20, 1)
	count := map[string]int{}
	for _, e := range s {
		if len(e.Models) != 1 {
			t.Fatalf("shared name sampled: %+v", e)
		}
		count[e.Models[0]]++
	}
	if count["m1"] != 10 || count["m2"] != 8 { // m2 has only 8, so it is capped
		t.Errorf("per-model counts = %v", count)
	}
	if s[0].ID != "r001" {
		t.Errorf("ids not assigned: %q", s[0].ID)
	}
}
