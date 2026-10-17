package main

import (
	"math/rand"
	"sort"
)

// pairOutcome is one head-to-head comparison scored from side A's view: 1 if
// A won in every order it was asked, 0 if it lost every time, 0.5 for a split.
type pairOutcome struct {
	id, query string
	score     float64
}

// outcomes scores each pair once, from whichever of its two orders came back.
// The two orders of a pair are one observation, not two: the judge answered
// both orders the same way in every calibration run.
func outcomes(pairs []pair, winners map[string]string) []pairOutcome {
	var out []pairOutcome
	for _, p := range pairs {
		var wins, asked float64
		if w, ok := winners[p.ID]; ok {
			asked++
			if w == "a" {
				wins++
			}
		}
		if w, ok := winners[p.ID+"r"]; ok {
			asked++
			if w == "b" { // reversed: "b" is the original A
				wins++
			}
		}
		if asked > 0 {
			out = append(out, pairOutcome{id: p.ID, query: p.Query, score: wins / asked})
		}
	}
	return out
}

// summary is a win rate with a 95% interval from a bootstrap that resamples
// whole queries. Pairs from the same query share names and the same LLM
// output, so they are not independent; resampling queries keeps that in the
// interval, where a per-answer binomial interval hid it.
type summary struct {
	rate, lo, hi   float64
	pairs, queries int
}

const bootstrapRounds = 2000

func pairStats(outs []pairOutcome, seed int64) summary {
	if len(outs) == 0 {
		return summary{}
	}
	byQuery := map[string][]float64{}
	for _, o := range outs {
		byQuery[o.query] = append(byQuery[o.query], o.score)
	}
	queries := make([]string, 0, len(byQuery))
	for q := range byQuery {
		queries = append(queries, q)
	}
	sort.Strings(queries) // deterministic order for a fixed seed

	mean := func(pick func(i int) string, n int) float64 {
		var sum float64
		var cnt int
		for i := range n {
			for _, s := range byQuery[pick(i)] {
				sum += s
				cnt++
			}
		}
		return sum / float64(cnt)
	}
	st := summary{rate: mean(func(i int) string { return queries[i] }, len(queries)), pairs: len(outs), queries: len(queries)}

	rng := rand.New(rand.NewSource(seed))
	reps := make([]float64, bootstrapRounds)
	for b := range reps {
		reps[b] = mean(func(int) string { return queries[rng.Intn(len(queries))] }, len(queries))
	}
	sort.Float64s(reps)
	st.lo = reps[int(0.025*bootstrapRounds)]
	st.hi = reps[int(0.975*bootstrapRounds)-1]
	return st
}
