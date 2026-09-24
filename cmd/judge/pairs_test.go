package main

import (
	"strings"
	"testing"
)

func TestBuildPairsPrefersDisagreements(t *testing.T) {
	entries := []historyEntry{
		{Query: "tea", Domain: "steep.shop", Rating: "good"},
		{Query: "tea", Domain: "leafy.io", Rating: "okay"},
		{Query: "tea", Domain: "xqz.tea", Rating: "bad"},
		{Query: "yoga", Domain: "mat.guru", Rating: "good"},
		{Query: "yoga", Domain: "flow.studio", Rating: "good"}, // same rating: no pair
	}
	pairs := buildPairs(entries, 10, 1)
	if len(pairs) != 2 {
		t.Fatalf("got %d pairs, want 2 (tea good-vs-okay and good-vs-bad)", len(pairs))
	}
	for _, p := range pairs {
		if p.Query != "tea" {
			t.Errorf("unexpected pair for %q", p.Query)
		}
		if p.Better != p.A && p.Better != p.B {
			t.Errorf("Better %q is neither side of the pair", p.Better)
		}
	}
}

func TestBuildPairsCapsPerQuery(t *testing.T) {
	var entries []historyEntry
	for i := range 10 {
		entries = append(entries, historyEntry{Query: "tea", Domain: string(rune('a'+i)) + ".shop", Rating: "good"})
		entries = append(entries, historyEntry{Query: "tea", Domain: string(rune('a'+i)) + ".io", Rating: "okay"})
	}
	if got := len(buildPairs(entries, 3, 1)); got != 3 {
		t.Errorf("got %d pairs, want the per-query cap of 3", got)
	}
}

func TestPairMessageShowsBothNames(t *testing.T) {
	msg := pairMessage([]pair{{ID: "p1", Query: "tea", A: "steep.shop", B: "leafy.io"}})
	for _, want := range []string{"p1", "tea", "steep.shop", "leafy.io"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestParseWinners(t *testing.T) {
	got, err := parseWinners(`[{"id":"p1","winner":"a"},{"id":"p2","winner":"B"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if got["p1"] != "a" || got["p2"] != "b" {
		t.Errorf("got %v, want normalized a/b", got)
	}
	if _, err := parseWinners(`[{"id":"p1","winner":"neither"}]`); err == nil {
		t.Error("want an error for a winner outside a/b")
	}
}

// Each pair is asked twice, once in each order; a judge that always picks the
// first name it sees scores 50% and shows up in the consistency number.
func TestScorePairsCountsConsistency(t *testing.T) {
	pairs := []pair{
		{ID: "p1", Query: "q", A: "x.com", B: "y.com", Better: "x.com"},
		{ID: "p2", Query: "q", A: "z.com", B: "w.com", Better: "w.com"},
	}
	// p1: both orders pick x — correct and consistent.
	// p2: both orders pick whichever was shown first — wrong and inconsistent.
	winners := map[string]string{"p1": "a", "p1r": "b", "p2": "a", "p2r": "a"}
	res := scorePairs(pairs, winners)
	// Correct counts answers, not pairs: p1 is right in both orders, p2 in one.
	if res.N != 2 || res.Answers != 4 || res.Correct != 3 {
		t.Errorf("N=%d Answers=%d Correct=%d, want 2, 4 and 3", res.N, res.Answers, res.Correct)
	}
	if res.Accuracy() != 0.75 {
		t.Errorf("Accuracy = %v, want 0.75", res.Accuracy())
	}
	if res.Consistent != 1 {
		t.Errorf("Consistent=%d, want 1", res.Consistent)
	}
}
