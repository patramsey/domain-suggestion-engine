package main

import (
	"context"
	"fmt"
	"math"
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
			out = append(out, pair{ID: fmt.Sprintf("p%04d", len(out)), Query: q, A: an[i], B: bn[i]})
		}
	}
	return out
}

// comparePairs asks each pair in both orders and returns how often side A won.
// Asking both ways cancels the model's preference for whichever name it sees
// first; disagreements between the two orders are counted as half a win each.
func comparePairs(ctx context.Context, c *llm.Client, model string, pairs []pair, size, conc int) (aWins, answers int, cost float64, err error) {
	ask := make([]pair, 0, len(pairs)*2)
	for _, p := range pairs {
		ask = append(ask, p, swapped(p))
	}
	winners, cost, err := askPairs(ctx, c, model, ask, size, conc)
	if err != nil {
		return 0, 0, cost, err
	}
	for _, p := range pairs {
		if w, ok := winners[p.ID]; ok {
			answers++
			if w == "a" {
				aWins++
			}
		}
		if w, ok := winners[p.ID+"r"]; ok {
			answers++
			if w == "b" { // reversed: "b" is the original A
				aWins++
			}
		}
	}
	return aWins, answers, cost, nil
}

// binomialCI is a 95% Wald interval on the win rate, enough to see whether a
// result clears a coin flip.
func binomialCI(wins, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}
	p := float64(wins) / float64(n)
	se := math.Sqrt(p * (1 - p) / float64(n))
	return math.Max(0, p-1.96*se), math.Min(1, p+1.96*se)
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
	pairs := pairUp(topNames(sa, *aVar, *top), topNames(sb, *bVar, *top), *perQuery, *seed)
	if len(pairs) == 0 {
		return fmt.Errorf("no shared queries between the two snapshots")
	}
	c, err := newClient(*model, *thinking)
	if err != nil {
		return err
	}
	labelA, labelB := label(*aPath, *aVar), label(*bPath, *bVar)
	fmt.Fprintf(os.Stderr, "Comparing %s vs %s over %d pairs, both orders ...\n", labelA, labelB, len(pairs))
	aWins, answers, cost, err := comparePairs(context.Background(), c, *model, pairs, *size, 3)
	if err != nil {
		return err
	}
	rate := share(aWins, answers)
	lo, hi := binomialCI(aWins, answers)
	fmt.Printf("\n=== PAIRWISE COMPARISON (%s, %s) ===\n\n", *model, costLabel(*model, cost))
	fmt.Printf("  %s wins %.1f%% of %d answers (95%% CI %.1f–%.1f%%)\n", labelA, rate*100, answers, lo*100, hi*100)
	fmt.Printf("  %s wins %.1f%%\n", labelB, (1-rate)*100)
	switch {
	case lo > 0.5:
		fmt.Printf("\n  %s is preferred.\n", labelA)
	case hi < 0.5:
		fmt.Printf("\n  %s is preferred.\n", labelB)
	default:
		fmt.Printf("\n  Too close to call: the interval spans 50%%.\n")
	}
	return nil
}

func label(path, variant string) string {
	if variant != "" {
		return variant
	}
	return trimExt(path)
}
