package main

import (
	"fmt"

	"github.com/patlivet/domain-suggestion-engine/internal/llm"
)

// stats compares the judge's verdicts with the human's over the names both rated.
type stats struct {
	N         int                       // names rated by both
	Missing   int                       // rated by the human, not returned by the judge
	Exact     int                       // same verdict
	GoodVsNot int                       // agree on good vs not-good, the decision the eval gates on
	Confusion map[string]map[string]int // human rating → judge rating → count
	humanGood int
	judgeGood int
}

func (s stats) ExactRate() float64      { return share(s.Exact, s.N) }
func (s stats) GoodVsNotRate() float64  { return share(s.GoodVsNot, s.N) }
func (s stats) HumanGoodShare() float64 { return share(s.humanGood, s.N) }
func (s stats) JudgeGoodShare() float64 { return share(s.judgeGood, s.N) }

func share(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// agreement compares two id → rating maps.
func agreement(human, judge map[string]string) stats {
	st := stats{Confusion: map[string]map[string]int{}}
	for id, h := range human {
		j, ok := judge[id]
		if !ok {
			st.Missing++
			continue
		}
		st.N++
		if st.Confusion[h] == nil {
			st.Confusion[h] = map[string]int{}
		}
		st.Confusion[h][j]++
		if h == j {
			st.Exact++
		}
		if (h == "good") == (j == "good") {
			st.GoodVsNot++
		}
		if h == "good" {
			st.humanGood++
		}
		if j == "good" {
			st.judgeGood++
		}
	}
	return st
}

// costLabel formats spend, or "n/a" for a model with no price in
// internal/llm/pricing.go — printing $0.0000 there would read as free.
func costLabel(model string, cost float64) string {
	if !llm.IsPriced(model) {
		return "cost n/a"
	}
	return fmt.Sprintf("$%.4f", cost)
}
