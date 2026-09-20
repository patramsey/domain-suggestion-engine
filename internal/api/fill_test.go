package api

import (
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
)

func TestBackfillToCountFillsFromRankedPool(t *testing.T) {
	final := []scorer.ScoredCandidate{sc("duskbrew", "cafe", "llm", 0.90), sc("coff", "ee", "algorithmic", 0.60)}
	ranked := []scorer.ScoredCandidate{
		sc("duskbrew", "cafe", "llm", 0.90),
		sc("hopchest", "pub", "llm", 0.85), // capped out earlier: same TLD group
		sc("duskbrew", "bar", "llm", 0.80), // duplicate SLD, must not be used
		sc("coff", "ee", "algorithmic", 0.60),
		sc("wintertap", "co", "llm", 0.55),
	}
	out := backfillToCount(final, ranked, 4)
	got := names(out)
	want := []string{"duskbrew.cafe", "hopchest.pub", "coff.ee", "wintertap.co"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestBackfillToCountNoOpWhenFull(t *testing.T) {
	final := []scorer.ScoredCandidate{sc("duskbrew", "cafe", "llm", 0.9), sc("hopchest", "pub", "llm", 0.8)}
	ranked := append([]scorer.ScoredCandidate{sc("mint", "cafe", "llm", 0.95)}, final...)
	if got := names(backfillToCount(final, ranked, 2)); len(got) != 2 || got[0] != "duskbrew.cafe" {
		t.Errorf("a full result set must not change: %v", got)
	}
}

func TestBackfillToCountPoolExhausted(t *testing.T) {
	final := []scorer.ScoredCandidate{sc("duskbrew", "cafe", "llm", 0.9)}
	ranked := final
	if got := names(backfillToCount(final, ranked, 5)); len(got) != 1 {
		t.Errorf("nothing to add: %v", got)
	}
}
