package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patlivet/domain-suggestion-engine/internal/quality"
)

type rating struct {
	ID     string `json:"id"`
	Rating string `json:"rating"` // good | okay | bad
}

type row struct {
	e     keyEntry
	value float64 // good=3, okay=2, bad=1
}

var ratingValue = map[string]float64{"good": 3, "okay": 2, "bad": 1}

// Thresholds for deciding whether a metric's comparison against the ratings
// is trustworthy enough to call, and the significance level used to call it.
const (
	minFlagGroup = 5    // typo/common word: need at least this many flagged AND unflagged rated names
	minSpecPairs = 10   // specificity: need at least this many rated names with known specificity
	alpha        = 0.05 // p below this counts as significant
)

func runAnalyze(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory with key.json and ratings.json")
	iters := fs.Int("iters", 10000, "permutation-test iterations")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var key []keyEntry
	var ratings []rating
	if err := readJSON(filepath.Join(*dir, "key.json"), &key); err != nil {
		return err
	}
	if err := readJSON(filepath.Join(*dir, "ratings.json"), &ratings); err != nil {
		return err
	}
	ratings, nPrefilled := withPrefilled(key, ratings)
	rows, err := join(key, ratings)
	if err != nil {
		return err
	}
	rows = recomputeFlags(rows)
	fmt.Printf("Rated %d of %d sampled names.\n\n", len(rows), len(key))
	if nPrefilled > 0 {
		fmt.Printf("Used %d prefilled ratings from history.\n", nPrefilled)
	}
	fmt.Println("Flags recomputed from the current word list; specificity from key.json.")

	var typo, common, good []bool
	for _, r := range rows {
		typo = append(typo, r.e.Typo)
		common = append(common, r.e.CommonWord)
		good = append(good, r.value == ratingValue["good"])
	}
	c := compareFlag(typo, good, *iters, 1)
	fmt.Printf("%-12s flagged n=%3d rated good %5.1f%% | unflagged n=%3d rated good %5.1f%% | diff %+5.1f pts (p=%.3f)\n",
		"Typo", c.nFlagged, c.goodFlagged*100, c.nUnflagged, c.goodUnflagged*100,
		(c.goodFlagged-c.goodUnflagged)*100, c.p)
	enough := c.nFlagged >= minFlagGroup && c.nUnflagged >= minFlagGroup
	fmt.Printf("  → Typo: %s\n", verdict(c.goodFlagged < c.goodUnflagged, c.p, enough))

	c = compareFlag(common, good, *iters, 1)
	fmt.Printf("%-12s flagged n=%3d rated good %5.1f%% | unflagged n=%3d rated good %5.1f%% | diff %+5.1f pts (p=%.3f)\n",
		"Common word", c.nFlagged, c.goodFlagged*100, c.nUnflagged, c.goodUnflagged*100,
		(c.goodFlagged-c.goodUnflagged)*100, c.p)
	fmt.Println("  → Common word: not judged — it is an availability proxy, and these ratings assume availability.")

	if ms := compareModels(rows); len(ms) > 0 {
		fmt.Println("\nBy model (names only that model produced):")
		for _, m := range ms {
			fmt.Printf("  %-26s n=%3d rated good %5.1f%%\n", m.model, m.n, m.good*100)
		}
	}

	var spec, val []float64
	for _, r := range rows {
		if r.e.Specificity != nil {
			spec = append(spec, *r.e.Specificity)
			val = append(val, r.value)
		}
	}
	if len(spec) >= 3 {
		rho := spearman(spec, val)
		p := permSpearman(spec, val, rho, *iters, 1)
		fmt.Printf("%-12s Spearman rho=%+.3f between specificity and rating, n=%d (p=%.3f)\n", "Specificity", rho, len(spec), p)
		fmt.Printf("  → %s: %s\n", "Specificity", verdict(rho > 0, p, len(spec) >= minSpecPairs))
	} else {
		fmt.Printf("%-12s too few rated names with known specificity (n=%d)\n", "Specificity", len(spec))
		fmt.Printf("  → %s: %s\n", "Specificity", verdict(false, 1, false))
	}
	fmt.Println("\nTypo agrees when flagged names are rated good LESS often; specificity agrees when rho is positive;")
	fmt.Println("both need p < 0.05. Small n makes p unreliable; report n with every claim.")
	return nil
}

// verdict states plainly whether a metric's comparison agrees with the
// ratings: not enough rated names to judge, or agrees/disagrees based on
// whether the effect ran in the expected direction with p < alpha.
func verdict(agreesInExpectedDirection bool, p float64, enough bool) string {
	switch {
	case !enough:
		return "TOO FEW rated names to judge"
	case agreesInExpectedDirection && p < alpha:
		return "AGREES with your ratings"
	default:
		return "DOES NOT AGREE with your ratings"
	}
}

// sldOf returns the SLD (the label before the first '.'), lowercased. A
// domain with no dot returns the whole string lowercased.
func sldOf(domain string) string {
	sld, _, _ := strings.Cut(domain, ".")
	return strings.ToLower(sld)
}

// recomputeFlags recomputes each row's Typo and CommonWord from its domain's
// SLD using the current word list, so ratings stay valid as the word list
// changes. Specificity is left as sampled (it comes from key.json).
func recomputeFlags(rows []row) []row {
	out := make([]row, len(rows))
	for i, r := range rows {
		sld := sldOf(r.e.Domain)
		r.e.Typo = quality.IsTypo(sld)
		r.e.CommonWord = quality.IsCommonWord(sld)
		out[i] = r
	}
	return out
}

// join pairs ratings with key entries; unknown ids are ignored.
func join(key []keyEntry, ratings []rating) ([]row, error) {
	byID := make(map[string]keyEntry, len(key))
	for _, e := range key {
		byID[e.ID] = e
	}
	var rows []row
	for _, r := range ratings {
		v, ok := ratingValue[r.Rating]
		if !ok {
			return nil, fmt.Errorf("unknown rating %q for %s", r.Rating, r.ID)
		}
		if e, ok := byID[r.ID]; ok {
			rows = append(rows, row{e: e, value: v})
		}
	}
	return rows, nil
}

type flagComparison struct {
	nFlagged, nUnflagged       int
	goodFlagged, goodUnflagged float64
	p                          float64 // two-sided permutation p-value for the difference
}

// compareFlag compares the share of names rated good among flagged vs
// unflagged names. (Comparing the share rated bad had no power when raters
// used "bad" rarely — 4 of 150 in the first round.)
func compareFlag(flagged, good []bool, iters int, seed int64) flagComparison {
	diff := func(fl []bool) (float64, int, int, float64, float64) {
		var nf, nu, gf, gu int
		for i := range fl {
			if fl[i] {
				nf++
				if good[i] {
					gf++
				}
			} else {
				nu++
				if good[i] {
					gu++
				}
			}
		}
		rf, ru := share(gf, nf), share(gu, nu)
		return rf - ru, nf, nu, rf, ru
	}
	obs, nf, nu, rf, ru := diff(flagged)
	c := flagComparison{nFlagged: nf, nUnflagged: nu, goodFlagged: rf, goodUnflagged: ru, p: 1}
	if nf == 0 || nu == 0 {
		return c
	}
	rng := rand.New(rand.NewSource(seed))
	perm := append([]bool(nil), flagged...)
	hits := 0
	for i := 0; i < iters; i++ {
		rng.Shuffle(len(perm), func(a, b int) { perm[a], perm[b] = perm[b], perm[a] })
		if d, _, _, _, _ := diff(perm); math.Abs(d) >= math.Abs(obs)-1e-12 {
			hits++
		}
	}
	c.p = float64(hits+1) / float64(iters+1)
	return c
}

type modelShare struct {
	model string
	n     int
	good  float64 // share rated good
}

// compareModels reports the share rated good for each model's own names
// (entries produced by exactly one model), sorted by model name.
func compareModels(rows []row) []modelShare {
	type acc struct{ n, good int }
	by := map[string]*acc{}
	for _, r := range rows {
		if len(r.e.Models) != 1 {
			continue
		}
		m := r.e.Models[0]
		if by[m] == nil {
			by[m] = &acc{}
		}
		by[m].n++
		if r.value == ratingValue["good"] {
			by[m].good++
		}
	}
	var out []modelShare
	for m, a := range by {
		out = append(out, modelShare{model: m, n: a.n, good: share(a.good, a.n)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].model < out[j].model })
	return out
}

func share(k, n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(k) / float64(n)
}

// ranks returns 1-based ranks with ties given their average rank.
func ranks(x []float64) []float64 {
	idx := make([]int, len(x))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return x[idx[a]] < x[idx[b]] })
	r := make([]float64, len(x))
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && x[idx[j+1]] == x[idx[i]] {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			r[idx[k]] = avg
		}
		i = j + 1
	}
	return r
}

func pearson(a, b []float64) float64 {
	n := float64(len(a))
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= n
	mb /= n
	var cov, va, vb float64
	for i := range a {
		cov += (a[i] - ma) * (b[i] - mb)
		va += (a[i] - ma) * (a[i] - ma)
		vb += (b[i] - mb) * (b[i] - mb)
	}
	if va == 0 || vb == 0 {
		return 0
	}
	return cov / math.Sqrt(va*vb)
}

func spearman(x, y []float64) float64 { return pearson(ranks(x), ranks(y)) }

func permSpearman(x, y []float64, obs float64, iters int, seed int64) float64 {
	rng := rand.New(rand.NewSource(seed))
	py := append([]float64(nil), y...)
	hits := 0
	for i := 0; i < iters; i++ {
		rng.Shuffle(len(py), func(a, b int) { py[a], py[b] = py[b], py[a] })
		if math.Abs(spearman(x, py)) >= math.Abs(obs)-1e-12 {
			hits++
		}
	}
	return float64(hits+1) / float64(iters+1)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
