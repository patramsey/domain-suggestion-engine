package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
)

// bandPair is a comparison between two score bands of the same query. Gap is
// how many bands apart they are: a bigger gap means the scorer is claiming a
// bigger quality difference.
type bandPair struct {
	pair
	Gap int
}

// bands splits a query's names, best-first, into n equal slices. Band 0 holds
// the highest-scored names. Fewer names than bands yields fewer bands.
func bands(ranked []string, n int) [][]string {
	if len(ranked) == 0 {
		return nil
	}
	if n > len(ranked) {
		n = len(ranked)
	}
	out := make([][]string, 0, n)
	per := float64(len(ranked)) / float64(n)
	for i := range n {
		lo := int(float64(i) * per)
		hi := int(float64(i+1) * per)
		if i == n-1 {
			hi = len(ranked)
		}
		if hi > lo {
			out = append(out, ranked[lo:hi])
		}
	}
	return out
}

// crossBandPairs draws comparisons between different bands of the same query,
// so the judge is asked about names the scorer ranked far apart. Only the
// scorer's own ordering decides Better — no human label is involved.
func crossBandPairs(ranked map[string][]string, nBands, perQuery int, seed int64) []bandPair {
	var queries []string
	for q := range ranked {
		queries = append(queries, q)
	}
	sort.Strings(queries)
	rng := rand.New(rand.NewSource(seed))

	var out []bandPair
	for _, q := range queries {
		bs := bands(ranked[q], nBands)
		if len(bs) < 2 {
			continue
		}
		for range perQuery {
			i := rng.Intn(len(bs) - 1)
			j := i + 1 + rng.Intn(len(bs)-i-1) // a later, lower-scoring band
			hi := bs[i][rng.Intn(len(bs[i]))]
			lo := bs[j][rng.Intn(len(bs[j]))]
			if hi == lo {
				continue
			}
			p := bandPair{pair: pair{ID: fmt.Sprintf("s%04d", len(out)), Query: q, A: hi, B: lo, Better: hi}, Gap: j - i}
			if rng.Intn(2) == 1 {
				p.A, p.B = p.B, p.A
			}
			out = append(out, p)
		}
	}
	return out
}

// gapResult counts answers for one band gap.
type gapResult struct {
	Answers, Correct int
}

func (g gapResult) Rate() float64 { return share(g.Correct, g.Answers) }

// rankAgreement reports, per band gap, how often the judge preferred the name
// the scorer ranked higher. 50% means the scorer's order carries no
// information at that distance.
func rankAgreement(pairs []bandPair, winners map[string]string) map[int]gapResult {
	out := map[int]gapResult{}
	for _, p := range pairs {
		for _, suffix := range []string{"", "r"} {
			w, ok := winners[p.ID+suffix]
			if !ok {
				continue
			}
			pick := p.A
			if (suffix == "" && w == "b") || (suffix == "r" && w == "a") {
				pick = p.B
			}
			g := out[p.Gap]
			g.Answers++
			if pick == p.Better {
				g.Correct++
			}
			out[p.Gap] = g
		}
	}
	return out
}

func runRankCheck(args []string) error {
	fs := newFlagSet("rankcheck")
	snapPath := fs.String("snapshot", "", "eval snapshot to check the ranking of")
	variant := fs.String("variant", "", "variant within the snapshot (default: any)")
	nBands := fs.Int("bands", 4, "score bands per query")
	perQuery := fs.Int("per-query", 8, "pairs per query")
	canaries := fs.Int("canaries", 40, "pairs with a known human answer, mixed in to check the judge")
	nExamples := fs.Int("examples", 30, "human-settled comparisons shown to the judge (held out of the canaries)")
	history := fs.String("history", "eval-results/ratings/history.json", "human ratings for the canary pairs")
	model := fs.String("model", "gemini-3.5-flash", "model to judge with")
	thinking := fs.String("thinking", "minimal", "Gemini thinkingLevel")
	size := fs.Int("batch", 10, "pairs per request")
	seed := fs.Int64("seed", 1, "sampling seed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *snapPath == "" {
		return fmt.Errorf("-snapshot is required")
	}
	var snap snapshot
	if err := readJSON(*snapPath, &snap); err != nil {
		return err
	}
	ranked := topNames(snap, *variant, 1000) // every name, best-first
	pairs := crossBandPairs(ranked, *nBands, *perQuery, *seed)
	if len(pairs) == 0 {
		return fmt.Errorf("no cross-band pairs from %s", *snapPath)
	}

	// Human-settled comparisons do two jobs here: a held-out slice calibrates
	// the judge in the prompt, and the rest are canaries whose answers we know,
	// so a run reports whether the judge is working before we read anything
	// into the scorer numbers.
	system := pairSystemPrompt
	var canary []pair
	if *canaries > 0 || *nExamples > 0 {
		var entries []historyEntry
		if err := readJSON(*history, &entries); err != nil {
			return err
		}
		all := buildPairs(entries, 3, *seed)
		rand.New(rand.NewSource(*seed)).Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
		if *nExamples+*canaries > len(all) {
			return fmt.Errorf("only %d human-settled pairs available for %d examples + %d canaries", len(all), *nExamples, *canaries)
		}
		system += pairExamples(all[:*nExamples])
		canary = all[*nExamples : *nExamples+*canaries]
		for i := range canary {
			canary[i].ID = fmt.Sprintf("c%04d", i)
		}
	}

	ask := make([]pair, 0, (len(pairs)+len(canary))*2)
	for _, p := range pairs {
		ask = append(ask, p.pair, swapped(p.pair))
	}
	for _, p := range canary {
		ask = append(ask, p, swapped(p))
	}
	rand.New(rand.NewSource(*seed)).Shuffle(len(ask), func(i, j int) { ask[i], ask[j] = ask[j], ask[i] })

	c, err := newClient(*model, *thinking)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Asking %d comparisons (%d scorer pairs + %d canaries, both orders) ...\n",
		len(ask), len(pairs), len(canary))
	winners, cost, err := askPairsWithSystem(context.Background(), c, *model, system, ask, *size, 3)
	if err != nil {
		return err
	}

	fmt.Printf("\n=== RANK CHECK: %s (%s, %s) ===\n\n", label(*snapPath, *variant), *model, costLabel(*model, cost))
	if len(canary) > 0 {
		cr := scorePairs(canary, winners)
		fmt.Printf("  judge vs human on %d canary pairs: %.1f%% (expect ~65-70%%; much lower means do not trust the rest)\n\n",
			cr.N, cr.Accuracy()*100)
	}
	byGap := rankAgreement(pairs, winners)
	gaps := make([]int, 0, len(byGap))
	for g := range byGap {
		gaps = append(gaps, g)
	}
	sort.Ints(gaps)
	fmt.Printf("  %-10s %9s %9s   %s\n", "band gap", "answers", "scorer", "")
	for _, g := range gaps {
		r := byGap[g]
		lo, hi := binomialCI(r.Correct, r.Answers)
		verdict := "no signal (interval spans 50%)"
		if lo > 0.5 {
			verdict = "scorer order agrees with the judge"
		} else if hi < 0.5 {
			verdict = "scorer order is BACKWARDS here"
		}
		fmt.Printf("  %-10d %9d %8.1f%%   %s (95%% CI %.1f-%.1f%%)\n", g, r.Answers, r.Rate()*100, verdict, lo*100, hi*100)
	}
	fmt.Println("\n  Band gap 1 compares neighbouring quartiles; the largest gap compares the top band with the bottom.")
	return nil
}
