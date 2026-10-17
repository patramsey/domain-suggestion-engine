package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"

	"github.com/patlivet/domain-suggestion-engine/internal/llm"
)

// snapshot is the part of an eval run this tool reads.
type snapshot struct {
	Model   string `json:"model"`
	Results []struct {
		Query       string `json:"query"`
		Variant     string `json:"variant"`
		Run         int    `json:"run"`
		Suggestions []struct {
			Name  string  `json:"name"`
			Score float64 `json:"score"`
		} `json:"suggestions"`
	} `json:"results"`
}

// topNames returns each query's top n names by score, for one variant
// ("" = any) and the first run only, so both sides are compared like for like.
func topNames(s snapshot, variant string, n int) map[string][]string {
	out := map[string][]string{}
	for _, r := range s.Results {
		if (variant != "" && r.Variant != variant) || (r.Run != 0 && r.Run != 1) {
			continue
		}
		if _, seen := out[r.Query]; seen {
			continue
		}
		sug := append([]struct {
			Name  string  `json:"name"`
			Score float64 `json:"score"`
		}{}, r.Suggestions...)
		sort.SliceStable(sug, func(i, j int) bool { return sug[i].Score > sug[j].Score })
		for i := 0; i < min(n, len(sug)); i++ {
			out[r.Query] = append(out[r.Query], sug[i].Name)
		}
	}
	return out
}

// pairUp draws head-to-head pairs, one name from each side, per shared query.
func pairUp(a, b map[string][]string, perQuery int, seed int64) []pair {
	var queries []string
	for q := range a {
		if _, ok := b[q]; ok {
			queries = append(queries, q)
		}
	}
	sort.Strings(queries)
	rng := rand.New(rand.NewSource(seed))
	var out []pair
	for _, q := range queries {
		an, bn := append([]string{}, a[q]...), append([]string{}, b[q]...)
		rng.Shuffle(len(an), func(i, j int) { an[i], an[j] = an[j], an[i] })
		rng.Shuffle(len(bn), func(i, j int) { bn[i], bn[j] = bn[j], bn[i] })
		for i := 0; i < min(perQuery, min(len(an), len(bn))); i++ {
			if an[i] == bn[i] {
				continue // the two sides can share candidates (e.g. a re-ranking)
			}
			out = append(out, pair{ID: fmt.Sprintf("p%04d", len(out)), Query: q, A: an[i], B: bn[i]})
		}
	}
	return out
}

// comparePairs asks each pair in both orders and scores each pair once, from
// side A's view. Asking both ways cancels the model's preference for
// whichever name it sees first.
func comparePairs(ctx context.Context, c *llm.Client, model string, pairs []pair, size, conc int) ([]pairOutcome, float64, error) {
	ask := make([]pair, 0, len(pairs)*2)
	for _, p := range pairs {
		ask = append(ask, p, swapped(p))
	}
	winners, cost, err := askPairs(ctx, c, model, ask, size, conc)
	if err != nil {
		return nil, cost, err
	}
	return outcomes(pairs, winners), cost, nil
}

// printVerdict reports a pair-level win rate with its query-bootstrap
// interval, and whether it clears 50%.
func printVerdict(labelA, labelB string, st summary) {
	fmt.Printf("  %s wins %.1f%% of %d pairs across %d queries (95%% CI %.1f–%.1f%%, bootstrap by query)\n",
		labelA, st.rate*100, st.pairs, st.queries, st.lo*100, st.hi*100)
	fmt.Printf("  %s wins %.1f%%\n", labelB, (1-st.rate)*100)
	switch {
	case st.lo > 0.5:
		fmt.Printf("\n  %s is preferred.\n", labelA)
	case st.hi < 0.5:
		fmt.Printf("\n  %s is preferred.\n", labelB)
	default:
		fmt.Printf("\n  Too close to call: the interval spans 50%%.\n")
	}
	fmt.Println("  Each pair is one observation (both orders together); a judge that agrees with")
	fmt.Println("  the human ~65% of the time also shrinks real differences, so a tie is weak evidence.")
}

// pairUpRounds redraws pairs with successive seeds and keeps the distinct
// ones, so a comparison can buy more answers from the same two top-N pools
// instead of reaching deeper into the ranking, which would change what is
// being measured.
func pairUpRounds(a, b map[string][]string, perQuery, rounds int, seed int64) []pair {
	seen := map[[3]string]bool{}
	var out []pair
	for r := range rounds {
		for _, p := range pairUp(a, b, perQuery, seed+int64(r)) {
			k := [3]string{p.Query, p.A, p.B}
			if seen[k] {
				continue
			}
			seen[k] = true
			p.ID = fmt.Sprintf("p%04d", len(out))
			out = append(out, p)
		}
	}
	return out
}

// onlyDifferences keeps, per query, the names that appear in one side's
// list but not the other's. In a paired comparison (cmd/pairgen) both sides
// share most of their LLM names; comparing shared names against each other
// adds noise and no information about the configurations.
func onlyDifferences(a, b map[string][]string) (map[string][]string, map[string][]string) {
	da, db := map[string][]string{}, map[string][]string{}
	for q, an := range a {
		bn, ok := b[q]
		if !ok {
			continue
		}
		inA, inB := map[string]bool{}, map[string]bool{}
		for _, n := range an {
			inA[n] = true
		}
		for _, n := range bn {
			inB[n] = true
		}
		for _, n := range an {
			if !inB[n] {
				da[q] = append(da[q], n)
			}
		}
		for _, n := range bn {
			if !inA[n] {
				db[q] = append(db[q], n)
			}
		}
	}
	return da, db
}

func runCompare(args []string) error {
	fs := newFlagSet("compare")
	aPath := fs.String("a", "", "snapshot JSON for side A")
	bPath := fs.String("b", "", "snapshot JSON for side B")
	aVar := fs.String("a-variant", "", "variant within snapshot A (default: any)")
	bVar := fs.String("b-variant", "", "variant within snapshot B (default: any)")
	top := fs.Int("top", 10, "names per query to draw from")
	perQuery := fs.Int("per-query", 4, "pairs per query")
	model := fs.String("model", defaultModel, "model to judge with")
	size := fs.Int("batch", 10, "pairs per request")
	seed := fs.Int64("seed", 1, "pairing seed")
	rounds := fs.Int("rounds", 1, "redraw pairings this many times for more answers from the same pools")
	diffOnly := fs.Bool("diff-only", false, "compare only names that are in one side's top N and not the other's (for paired runs from cmd/pairgen)")
	thinking := fs.String("thinking", "minimal", "Gemini thinkingLevel: minimal, low, medium, high")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *aPath == "" || *bPath == "" {
		return fmt.Errorf("-a and -b are required")
	}
	var sa, sb snapshot
	if err := readJSON(*aPath, &sa); err != nil {
		return err
	}
	if err := readJSON(*bPath, &sb); err != nil {
		return err
	}
	ta, tb := topNames(sa, *aVar, *top), topNames(sb, *bVar, *top)
	if *diffOnly {
		ta, tb = onlyDifferences(ta, tb)
	}
	pairs := pairUpRounds(ta, tb, *perQuery, *rounds, *seed)
	if len(pairs) == 0 {
		return fmt.Errorf("no shared queries between the two snapshots")
	}
	c, err := newClient(*model, *thinking)
	if err != nil {
		return err
	}
	labelA, labelB := label(*aPath, *aVar), label(*bPath, *bVar)
	fmt.Fprintf(os.Stderr, "Comparing %s vs %s over %d pairs, both orders ...\n", labelA, labelB, len(pairs))
	outs, cost, err := comparePairs(context.Background(), c, *model, pairs, *size, 3)
	if err != nil {
		return err
	}
	fmt.Printf("\n=== PAIRWISE COMPARISON (%s, %s) ===\n\n", *model, costLabel(*model, cost))
	printVerdict(labelA, labelB, pairStats(outs, *seed))
	return nil
}

func label(path, variant string) string {
	if variant != "" {
		return variant
	}
	return trimExt(path)
}
