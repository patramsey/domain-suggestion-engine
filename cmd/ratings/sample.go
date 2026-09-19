package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
)

// snapshot is the subset of an eval snapshot this tool reads.
type snapshot struct {
	Model   string       `json:"model"`
	Results []snapResult `json:"results"`
}

type snapResult struct {
	Query       string           `json:"query"`
	Quality     *struct{}        `json:"quality"` // presence shows the snapshot was annotated
	Suggestions []snapSuggestion `json:"suggestions"`
}

type snapSuggestion struct {
	Name        string   `json:"name"`
	Score       float64  `json:"score"`
	Typo        bool     `json:"typo"`
	CommonWord  bool     `json:"common_word"`
	Specificity *float64 `json:"specificity"`
}

// keyEntry is one unique (query, domain) with its metrics; key.json holds these.
type keyEntry struct {
	ID          string   `json:"id"`
	Query       string   `json:"query"`
	Domain      string   `json:"domain"`
	Typo        bool     `json:"typo"`
	CommonWord  bool     `json:"common_word"`
	Specificity *float64 `json:"specificity"`
	Models      []string `json:"models"`
	// Prefilled holds a rating carried over from the history file for a
	// (query, domain) already rated in an earlier round. Non-empty entries
	// are kept in key.json but omitted from items.json (the page never
	// shows them again).
	Prefilled string `json:"prefilled,omitempty"`
}

// item is what the rating page shows: no model or metric information.
type item struct {
	ID     string `json:"id"`
	Query  string `json:"query"`
	Domain string `json:"domain"`
}

func runSample(args []string) error {
	fs := flag.NewFlagSet("sample", flag.ContinueOnError)
	n := fs.Int("n", 150, "number of names to rate")
	seed := fs.Int64("seed", 1, "random seed (sampling is deterministic per seed)")
	out := fs.String("out", "", "output directory for items.json and key.json")
	top := fs.Int("top", 0, "only use each result's top N names by score (0 = all)")
	byModel := fs.Bool("by-model", false, "sample equally from each model's own names, without flag stratification (for model comparisons)")
	history := fs.String("history", "", "rating history file; chosen (query, domain) pairs already rated here are hidden from items.json but kept in key.json with their stored rating (default: no history)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || fs.NArg() == 0 {
		return fmt.Errorf("need -out DIR and at least one snapshot")
	}
	var snaps []snapshot
	for _, p := range fs.Args() {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var s snapshot
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		snaps = append(snaps, s)
	}
	pool, err := poolFrom(snaps, *top)
	if err != nil {
		return err
	}
	var chosen []keyEntry
	if *byModel {
		chosen = sampleByModel(pool, *n, *seed)
	} else {
		chosen = sample(pool, *n, *seed)
	}

	keyEntries := chosen
	visible := blind(chosen)
	nPrefilled := 0
	if *history != "" {
		hist, err := loadHistory(*history)
		if err != nil {
			return err
		}
		keyEntries, visible, nPrefilled = prefill(chosen, hist)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(*out, "items.json"), visible); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(*out, "key.json"), keyEntries); err != nil {
		return err
	}
	fmt.Printf("Sampled %d of %d unique names into %s\n", len(chosen), len(pool), *out)
	if nPrefilled > 0 {
		fmt.Printf("Prefilled %d of %d chosen names from history.\n", nPrefilled, len(chosen))
	}
	return nil
}

// prefill marks chosen entries whose (query, domain) is already in the
// rating history with the stored rating, and returns the visible (blind)
// list with those entries removed, plus how many were prefilled. It is a
// pure function so sampling and prefilling can be tested independently.
func prefill(chosen []keyEntry, hist map[[2]string]historyEntry) (keyEntries []keyEntry, visible []item, n int) {
	keyEntries = make([]keyEntry, len(chosen))
	for i, e := range chosen {
		if h, ok := hist[[2]string{e.Query, e.Domain}]; ok {
			e.Prefilled = h.Rating
			n++
		}
		keyEntries[i] = e
		if e.Prefilled == "" {
			visible = append(visible, item{ID: e.ID, Query: e.Query, Domain: e.Domain})
		}
	}
	return keyEntries, visible, n
}

// poolFrom merges snapshots into unique (query, domain) entries. If topN > 0,
// only each result's top N suggestions by score are considered.
func poolFrom(snaps []snapshot, topN int) ([]keyEntry, error) {
	byKey := map[string]*keyEntry{}
	var order []string
	for _, s := range snaps {
		for _, r := range s.Results {
			if r.Quality == nil && len(r.Suggestions) > 0 {
				return nil, fmt.Errorf("snapshot for model %q is not annotated; run: go run ./cmd/eval -rescore FILE", s.Model)
			}
			sugs := r.Suggestions
			if topN > 0 {
				sugs = topByScore(sugs, topN)
			}
			for _, sg := range sugs {
				k := r.Query + "\x00" + sg.Name
				e, ok := byKey[k]
				if !ok {
					e = &keyEntry{Query: r.Query, Domain: sg.Name, Typo: sg.Typo, CommonWord: sg.CommonWord, Specificity: sg.Specificity}
					byKey[k] = e
					order = append(order, k)
				}
				if !containsStr(e.Models, s.Model) {
					e.Models = append(e.Models, s.Model)
				}
			}
		}
	}
	sort.Strings(order) // deterministic before seeded shuffling
	pool := make([]keyEntry, len(order))
	for i, k := range order {
		pool[i] = *byKey[k]
	}
	return pool, nil
}

// topByScore returns the first n of a copy of sugs sorted by score descending
// (stable), leaving sugs unchanged.
func topByScore(sugs []snapSuggestion, n int) []snapSuggestion {
	sorted := append([]snapSuggestion(nil), sugs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Score > sorted[j].Score })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

// sldTracker enforces at most one chosen entry per (query, SLD) pair, so a
// round never contains two TLD variants of the same name for the same
// query. TLD variants of a name rated in an earlier round are unaffected —
// this only dedupes within a single draw.
type sldTracker struct {
	seen map[string]map[string]bool
}

func newSLDTracker() *sldTracker {
	return &sldTracker{seen: map[string]map[string]bool{}}
}

// tryAdd reports whether domain's SLD is new for query, recording it if so.
func (t *sldTracker) tryAdd(query, domain string) bool {
	sld := sldOf(domain)
	if t.seen[query] == nil {
		t.seen[query] = map[string]bool{}
	}
	if t.seen[query][sld] {
		return false
	}
	t.seen[query][sld] = true
	return true
}

// sampleByModel draws up to n/len(models) random entries from each model's
// own names (entries produced by exactly one model), with no flag
// stratification, so a model comparison is not biased by which model trips
// the flags more. Shared names are excluded, and at most one TLD variant of
// a name is kept per query across the whole draw. The result is shuffled
// and given ids r001, r002, ...
func sampleByModel(pool []keyEntry, n int, seed int64) []keyEntry {
	rng := rand.New(rand.NewSource(seed))
	byModel := map[string][]keyEntry{}
	var models []string
	for _, e := range pool {
		if len(e.Models) != 1 {
			continue
		}
		m := e.Models[0]
		if _, ok := byModel[m]; !ok {
			models = append(models, m)
		}
		byModel[m] = append(byModel[m], e)
	}
	sort.Strings(models)
	if len(models) == 0 {
		return nil
	}
	per := n / len(models)
	seen := newSLDTracker()
	var chosen []keyEntry
	for _, m := range models {
		es := byModel[m]
		idx := rng.Perm(len(es))
		got := 0
		for _, k := range idx {
			if got == per {
				break
			}
			e := es[k]
			if !seen.tryAdd(e.Query, e.Domain) {
				continue
			}
			chosen = append(chosen, e)
			got++
		}
	}
	rng.Shuffle(len(chosen), func(a, b int) { chosen[a], chosen[b] = chosen[b], chosen[a] })
	for i := range chosen {
		chosen[i].ID = fmt.Sprintf("r%03d", i+1)
	}
	return chosen
}

// sample picks n entries: up to n/6 each of typos, common words, and high-
// and low-specificity names (top and bottom quartile), then random others.
// At most one TLD variant of a name is kept per query across the whole
// draw. The result is shuffled and given ids r001, r002, ...
func sample(pool []keyEntry, n int, seed int64) []keyEntry {
	rng := rand.New(rand.NewSource(seed))
	idx := rng.Perm(len(pool))
	lo, hi := specQuartiles(pool)
	per := n / 6
	buckets := []func(keyEntry) bool{
		func(e keyEntry) bool { return e.Typo },
		func(e keyEntry) bool { return e.CommonWord },
		func(e keyEntry) bool { return e.Specificity != nil && *e.Specificity >= hi },
		func(e keyEntry) bool { return e.Specificity != nil && *e.Specificity <= lo },
	}
	taken := make([]bool, len(pool))
	seen := newSLDTracker()
	var chosen []keyEntry
	take := func(i int) bool {
		if !seen.tryAdd(pool[i].Query, pool[i].Domain) {
			return false
		}
		taken[i] = true
		chosen = append(chosen, pool[i])
		return true
	}
	for _, in := range buckets {
		got := 0
		for _, i := range idx {
			if got == per || len(chosen) == n {
				break
			}
			if !taken[i] && in(pool[i]) {
				if take(i) {
					got++
				}
			}
		}
	}
	for _, i := range idx {
		if len(chosen) == n {
			break
		}
		if !taken[i] {
			take(i)
		}
	}
	rng.Shuffle(len(chosen), func(a, b int) { chosen[a], chosen[b] = chosen[b], chosen[a] })
	for i := range chosen {
		chosen[i].ID = fmt.Sprintf("r%03d", i+1)
	}
	return chosen
}

// specQuartiles returns the 25th and 75th percentile of known specificities.
func specQuartiles(pool []keyEntry) (lo, hi float64) {
	var v []float64
	for _, e := range pool {
		if e.Specificity != nil {
			v = append(v, *e.Specificity)
		}
	}
	if len(v) == 0 {
		return 0, 0
	}
	sort.Float64s(v)
	return v[len(v)/4], v[(len(v)*3)/4]
}

func blind(entries []keyEntry) []item {
	out := make([]item, len(entries))
	for i, e := range entries {
		out[i] = item{ID: e.ID, Query: e.Query, Domain: e.Domain}
	}
	return out
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
