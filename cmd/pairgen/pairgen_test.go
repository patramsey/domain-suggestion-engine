package main

import (
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
)

func TestMergePoolPrefersLLMAndSorts(t *testing.T) {
	llm := []scorer.ScoredCandidate{
		{Candidate: algorithmic.Candidate{SLD: "duskbrew", TLD: "cafe", Source: "llm"}, Score: 0.8},
		{Candidate: algorithmic.Candidate{SLD: "coff", TLD: "ee", Source: "llm"}, Score: 0.5},
	}
	algo := []scorer.ScoredCandidate{
		{Candidate: algorithmic.Candidate{SLD: "coff", TLD: "ee", Source: "algorithmic"}, Score: 0.9}, // duplicate of an LLM name
		{Candidate: algorithmic.Candidate{SLD: "stud", TLD: "io", Source: "algorithmic"}, Score: 0.6},
	}
	got := mergePool(llm, algo)
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3 (one duplicate dropped)", len(got))
	}
	for _, c := range got {
		if c.Name() == "coff.ee" && c.Source != "llm" {
			t.Error("a name produced by both tiers should keep the LLM copy, as the handler does")
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Fatalf("pool not sorted by score: %v", got)
		}
	}
}

func TestShapedArmsShareTheLLMPool(t *testing.T) {
	snap := snapshot{Results: []snapshotResult{{
		Query: "denver coffee shop",
		Run:   1,
		Suggestions: []snapshotSuggestion{
			{Name: "duskbrew.cafe", SLD: "duskbrew", TLD: "cafe", Score: 0.9, Source: "llm"},
			{Name: "nightcap.coffee", SLD: "nightcap", TLD: "coffee", Score: 0.85, Source: "llm"},
		},
	}}}
	a := arm(snap, "", []string{"hacks"}, settings{count: 10, llmShare: 0.6, slots: 0})
	b := arm(snap, "", []string{}, settings{count: 10, llmShare: 0.6, slots: 0})
	has := func(r snapshotResult, name string) bool {
		for _, s := range r.Suggestions {
			if s.Name == name {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"duskbrew.cafe", "nightcap.coffee"} {
		if !has(a.Results[0], name) || !has(b.Results[0], name) {
			t.Errorf("%s should appear in both arms: both start from the same LLM pool", name)
		}
	}
}
