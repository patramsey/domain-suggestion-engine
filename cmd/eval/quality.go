package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patlivet/domain-suggestion-engine/internal/parser"
	"github.com/patlivet/domain-suggestion-engine/internal/quality"
)

// qualityStats summarises the quality flags over a set of suggestions.
type qualityStats struct {
	Names              int     `json:"names"`
	TypoRate           float64 `json:"typo_rate"`
	CommonWordRate     float64 `json:"common_word_rate"`
	MeanSpecificity    float64 `json:"mean_specificity"`    // over names with known specificity
	UnknownSpecificity int     `json:"unknown_specificity"` // names where it can't be computed
}

// resultQuality holds stats over all suggestions and over the top 10 by
// score (what users see). Suggestions are saved in score order.
type resultQuality struct {
	All   qualityStats `json:"all"`
	Top10 qualityStats `json:"top10"`
}

// annotateRun sets the quality flags on every suggestion in run and the
// per-result summaries. Query tokens are re-parsed so old snapshots work.
func annotateRun(run *savedRun, icannSet map[string]struct{}) {
	tokens := make(map[string][]string, len(run.Queries))
	for _, q := range run.Queries {
		tokens[q] = parser.Parse(q, icannSet)
	}
	for i := range run.Results {
		r := &run.Results[i]
		var others [][]string
		for _, q := range run.Queries {
			if q != r.Query {
				others = append(others, tokens[q])
			}
		}
		for j := range r.Suggestions {
			s := &r.Suggestions[j]
			s.Typo = quality.IsTypo(s.SLD)
			s.CommonWord = quality.IsCommonWord(s.SLD)
			s.Specificity = nil
			if v, ok := quality.Specificity(s.SLD, tokens[r.Query], others); ok {
				s.Specificity = &v
			}
		}
		q := resultQualityOf(r.Suggestions)
		r.Quality = &q
	}
}

func resultQualityOf(sugs []savedSuggestion) resultQuality {
	return resultQuality{All: summarize(sugs), Top10: summarize(topByScore(sugs, 10))}
}

// topByScore returns the first n suggestions of a copy of sugs sorted by
// Score descending (sort.SliceStable, so ties keep their saved order). The
// input slice is never reordered — snapshots are not guaranteed to be saved
// in score order (old snapshots are saved in LLM order).
func topByScore(sugs []savedSuggestion, n int) []savedSuggestion {
	sorted := make([]savedSuggestion, len(sugs))
	copy(sorted, sugs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Score > sorted[j].Score })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

func summarize(sugs []savedSuggestion) qualityStats {
	if len(sugs) == 0 {
		return qualityStats{}
	}
	st := qualityStats{Names: len(sugs)}
	var typo, common, known int
	var spec float64
	for _, s := range sugs {
		if s.Typo {
			typo++
		}
		if s.CommonWord {
			common++
		}
		if s.Specificity != nil {
			spec += *s.Specificity
			known++
		} else {
			st.UnknownSpecificity++
		}
	}
	st.TypoRate = float64(typo) / float64(len(sugs))
	st.CommonWordRate = float64(common) / float64(len(sugs))
	if known > 0 {
		st.MeanSpecificity = spec / float64(known)
	}
	return st
}

// printQuality prints pooled quality stats per variant for all kept names
// and for each query's top 10.
func printQuality(run savedRun) {
	fmt.Printf("\n\n=== QUALITY METRICS (pooled across queries and runs) ===\n\n")
	fmt.Printf("%-20s  %-6s  %6s  %6s  %8s  %9s  %12s\n",
		"Variant", "Scope", "Names", "Typo%", "Common%", "Mean spec", "Unknown spec")
	fmt.Println(strings.Repeat("-", 80))
	for _, v := range run.Variants {
		var all, top []savedSuggestion
		for _, r := range run.Results {
			if r.Variant != v || r.Error != "" {
				continue
			}
			all = append(all, r.Suggestions...)
			top = append(top, topByScore(r.Suggestions, 10)...)
		}
		for _, row := range []struct {
			scope string
			st    qualityStats
		}{{"all", summarize(all)}, {"top10", summarize(top)}} {
			fmt.Printf("%-20s  %-6s  %6d  %5.1f%%  %7.1f%%  %9.3f  %12d\n",
				v, row.scope, row.st.Names, row.st.TypoRate*100, row.st.CommonWordRate*100,
				row.st.MeanSpecificity, row.st.UnknownSpecificity)
		}
	}
	fmt.Println("  Typo/Common: lower is better. Mean spec: higher = more specific to the query.")
}

// rescoreFile annotates an existing snapshot with the quality metrics, runs
// any extra annotation hooks, and writes it back in place. Snapshots saved before the queries list existed
// fall back to the queries found in their results.
func rescoreFile(path string, icannSet map[string]struct{}, hooks ...func(*savedRun)) (savedRun, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return savedRun{}, err
	}
	var run savedRun
	if err := json.Unmarshal(data, &run); err != nil {
		return savedRun{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(run.Queries) == 0 {
		seen := map[string]bool{}
		for _, r := range run.Results {
			if !seen[r.Query] {
				seen[r.Query] = true
				run.Queries = append(run.Queries, r.Query)
			}
		}
	}
	annotateRun(&run, icannSet)
	for _, hook := range hooks {
		hook(&run)
	}
	out, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return savedRun{}, err
	}
	return run, writeFileAtomic(path, out)
}

// writeFileAtomic writes data to path by writing a temp file in the same
// directory and renaming it over path, so a partial write or crash cannot
// corrupt an existing file. The temp file is removed on any failure.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(tmpPath)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// runStats is one run's pooled top-10 quality and yield for one variant.
type runStats struct {
	Run          int
	Top10        qualityStats
	KeptPerQuery float64
}

// statsByRun pools each run's top-10-by-score suggestions across queries for
// one variant (errored results excluded), sorted by run.
func statsByRun(run savedRun, variant string) []runStats {
	type acc struct {
		top     []savedSuggestion
		kept, n int
	}
	by := map[int]*acc{}
	for _, r := range run.Results {
		if r.Variant != variant || r.Error != "" {
			continue
		}
		a := by[r.Run]
		if a == nil {
			a = &acc{}
			by[r.Run] = a
		}
		a.top = append(a.top, topByScore(r.Suggestions, 10)...)
		a.kept += len(r.Suggestions)
		a.n++
	}
	var out []runStats
	for k, a := range by {
		out = append(out, runStats{Run: k, Top10: summarize(a.top), KeptPerQuery: float64(a.kept) / float64(a.n)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Run < out[j].Run })
	return out
}

// noiseBand is max − min of vals: the run-to-run spread used by the spec's
// noise rule.
func noiseBand(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	return hi - lo
}

// printQualityByRun prints per-run top-10 metrics and each metric's noise
// band, for snapshots with more than one run.
func printQualityByRun(run savedRun) {
	for _, v := range run.Variants {
		st := statsByRun(run, v)
		if len(st) < 2 {
			continue
		}
		fmt.Printf("\n=== PER-RUN TOP-10 (%s) ===\n", v)
		fmt.Printf("%4s  %7s  %8s  %9s  %11s\n", "Run", "Typo%", "Common%", "Mean spec", "Kept/query")
		var typo, common, spec, kept []float64
		for _, s := range st {
			fmt.Printf("%4d  %6.1f%%  %7.1f%%  %9.3f  %11.1f\n", s.Run, s.Top10.TypoRate*100, s.Top10.CommonWordRate*100, s.Top10.MeanSpecificity, s.KeptPerQuery)
			typo = append(typo, s.Top10.TypoRate)
			common = append(common, s.Top10.CommonWordRate)
			spec = append(spec, s.Top10.MeanSpecificity)
			kept = append(kept, s.KeptPerQuery)
		}
		fmt.Printf("band  %6.1f%%  %7.1f%%  %9.3f  %11.1f   (max − min across runs)\n",
			noiseBand(typo)*100, noiseBand(common)*100, noiseBand(spec), noiseBand(kept))
	}
}
