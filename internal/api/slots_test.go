package api

import (
	"fmt"
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
	"github.com/patlivet/domain-suggestion-engine/internal/wordlist"
)

// Very common words (SCOWL ≤ 20) used below: late, mint, core, flow, state.
func sc(sld, tld, source string, score float64) scorer.ScoredCandidate {
	return scorer.ScoredCandidate{Candidate: algorithmic.Candidate{SLD: sld, TLD: tld, Source: source}, Score: score}
}

// plus20 stands in for scorer.ScoreWithoutCommonWordPenalty: the test pool's
// scores already include a 0.20 common-word penalty.
func plus20(scores map[string]float64) func(algorithmic.Candidate) float64 {
	return func(c algorithmic.Candidate) float64 { return scores[c.SLD+"."+c.TLD] }
}

func names(out []scorer.ScoredCandidate) []string {
	var n []string
	for _, s := range out {
		n = append(n, s.Name())
	}
	return n
}

func TestReserveCommonWordsFillsSlotsByQuality(t *testing.T) {
	final := []scorer.ScoredCandidate{
		sc("duskbrew", "cafe", "llm", 0.90), sc("lenscraft", "studio", "llm", 0.80),
		sc("hopchest", "pub", "llm", 0.70), sc("coff", "ee", "algorithmic", 0.65),
		sc("wintertap", "co", "llm", 0.60),
	}
	pool := append([]scorer.ScoredCandidate{
		sc("mint", "cafe", "llm", 0.72), sc("mint", "bar", "llm", 0.50), // same SLD twice
		sc("late", "pub", "llm", 0.55), sc("core", "io", "llm", 0.30),
	}, final...)
	rescore := plus20(map[string]float64{"mint.cafe": 0.92, "mint.bar": 0.70, "late.pub": 0.75, "core.io": 0.50})

	out := reserveCommonWords(final, pool, 5, 4, rescore) // round(5×4/10) = 2 slots

	want := []string{"mint.cafe", "duskbrew.cafe", "lenscraft.studio", "late.pub", "coff.ee"}
	if got := names(out); len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	}
	if out[0].Score != 0.92 || out[3].Score != 0.75 {
		t.Errorf("reserved words should carry their rescored score: %v, %v", out[0].Score, out[3].Score)
	}
}

func TestReserveCommonWordsPrefersReplacingSameTier(t *testing.T) {
	final := []scorer.ScoredCandidate{
		sc("duskbrew", "cafe", "llm", 0.90), sc("hopchest", "pub", "llm", 0.70),
		sc("coff", "ee", "algorithmic", 0.40), // lowest overall, but algorithmic
	}
	pool := append([]scorer.ScoredCandidate{sc("mint", "cafe", "llm", 0.60)}, final...)
	out := reserveCommonWords(final, pool, 3, 4, plus20(map[string]float64{"mint.cafe": 0.80})) // 1 slot
	got := names(out)
	if len(got) != 3 || got[1] != "mint.cafe" || got[2] != "coff.ee" {
		t.Errorf("mint (llm) should replace the lowest llm name, keeping coff.ee: %v", got)
	}
}

func TestReserveCommonWordsKeepsCommonWordsAlreadyShown(t *testing.T) {
	final := []scorer.ScoredCandidate{
		sc("duskbrew", "cafe", "llm", 0.90), sc("flow", "studio", "llm", 0.50), sc("state", "co", "llm", 0.45),
	}
	pool := final
	out := reserveCommonWords(final, pool, 3, 7, plus20(map[string]float64{"flow.studio": 0.70, "state.co": 0.65})) // 2 slots
	got := names(out)
	if len(got) != 3 || got[0] != "duskbrew.cafe" || got[1] != "flow.studio" || got[2] != "state.co" {
		t.Errorf("got %v", got)
	}
	if out[1].Score != 0.70 {
		t.Errorf("common words already shown should be rescored, got %v", out[1].Score)
	}
}

func TestReserveCommonWordsFewerAvailableThanSlots(t *testing.T) {
	final := []scorer.ScoredCandidate{sc("duskbrew", "cafe", "llm", 0.9), sc("hopchest", "pub", "llm", 0.8)}
	pool := append([]scorer.ScoredCandidate{sc("mint", "cafe", "llm", 0.5)}, final...)
	out := reserveCommonWords(final, pool, 2, 10, plus20(map[string]float64{"mint.cafe": 0.7})) // wants 2, only 1 exists
	if got := names(out); len(got) != 2 || got[0] != "duskbrew.cafe" || got[1] != "mint.cafe" {
		t.Errorf("got %v", got)
	}
}

func TestReserveCommonWordsAppendsWhenShort(t *testing.T) {
	final := []scorer.ScoredCandidate{sc("duskbrew", "cafe", "llm", 0.9)}
	pool := append([]scorer.ScoredCandidate{sc("mint", "cafe", "llm", 0.5)}, final...)
	out := reserveCommonWords(final, pool, 5, 2, plus20(map[string]float64{"mint.cafe": 0.7})) // 1 slot, room to spare
	if got := names(out); len(got) != 2 {
		t.Errorf("want duskbrew + mint appended, got %v", got)
	}
}

func TestReserveCommonWordsDisabled(t *testing.T) {
	final := []scorer.ScoredCandidate{sc("duskbrew", "cafe", "llm", 0.9)}
	pool := append([]scorer.ScoredCandidate{sc("mint", "cafe", "llm", 0.5)}, final...)
	out := reserveCommonWords(final, pool, 5, 0, plus20(map[string]float64{"mint.cafe": 0.7}))
	if got := names(out); len(got) != 1 || got[0] != "duskbrew.cafe" {
		t.Errorf("slots=0 must not change results, got %v", got)
	}
}

// 4 reserved words that outscore everything: at most 2 per block of 10, each
// block sorted by score.
func TestReserveCommonWordsCapsEachBlockOfTen(t *testing.T) {
	var final []scorer.ScoredCandidate
	for i := range 20 {
		final = append(final, sc(fmt.Sprintf("coined%c", 'a'+i), "cafe", "llm", 0.80-float64(i)*0.01))
	}
	commons := []scorer.ScoredCandidate{sc("mint", "cafe", "llm", 0.7), sc("late", "pub", "llm", 0.7), sc("core", "io", "llm", 0.7), sc("flow", "bar", "llm", 0.7)}
	pool := append(commons, final...)
	rescore := plus20(map[string]float64{"mint.cafe": 0.99, "late.pub": 0.98, "core.io": 0.97, "flow.bar": 0.96})

	out := reserveCommonWords(final, pool, 20, 2, rescore)

	if len(out) != 20 {
		t.Fatalf("got %d results, want 20", len(out))
	}
	for b := 0; b < 2; b++ {
		block := out[b*10 : b*10+10]
		n := 0
		for i, c := range block {
			if wordlist.IsCommon(c.SLD) {
				n++
			}
			if i > 0 && c.Score > block[i-1].Score {
				t.Errorf("block %d not sorted at %d: %v", b, i, names(block))
			}
		}
		if n != 2 {
			t.Errorf("block %d has %d common words, want 2: %v", b, n, names(block))
		}
	}
	if out[0].Name() != "mint.cafe" || out[1].Name() != "late.pub" {
		t.Errorf("best common words should lead block 1: %v", names(out[:10]))
	}
}

func TestReserveCommonWordsDoesNotDisplaceHighScoring(t *testing.T) {
	// A high-scoring list should not have its candidates replaced by a low-scoring common word
	final := []scorer.ScoredCandidate{
		sc("duskbrew", "cafe", "llm", 0.90),
		sc("hopchest", "pub", "llm", 0.85),
	}
	// mint.cafe has a low rescored score of 0.35 (below floor 0.50 and far below 0.85)
	pool := append([]scorer.ScoredCandidate{sc("mint", "cafe", "llm", 0.15)}, final...)
	rescore := plus20(map[string]float64{"mint.cafe": 0.35})

	out := reserveCommonWords(final, pool, 2, 10, rescore)
	got := names(out)
	if len(got) != 2 || got[0] != "duskbrew.cafe" || got[1] != "hopchest.pub" {
		t.Errorf("low-scoring common word should not displace high-scoring candidates, got: %v", got)
	}
}
