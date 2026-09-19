package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/tlds"
)

func specPtr(v float64) *float64 { return &v }

func TestSummarize(t *testing.T) {
	sugs := []savedSuggestion{
		{SLD: "pizzaria", Typo: true, Specificity: specPtr(0.2)},
		{SLD: "late", CommonWord: true, Specificity: specPtr(0.0)},
		{SLD: "hopsmith", Compound: true}, // specificity unknown
		{SLD: "ovenly", Specificity: specPtr(0.1)},
	}
	got := summarize(sugs)
	if got.Names != 4 || got.TypoRate != 0.25 || got.CommonWordRate != 0.25 || got.CompoundRate != 0.25 || got.UnknownSpecificity != 1 {
		t.Errorf("summarize = %+v", got)
	}
	if math.Abs(got.MeanSpecificity-0.1) > 1e-9 { // mean of 0.2, 0.0, 0.1
		t.Errorf("MeanSpecificity = %v, want 0.1", got.MeanSpecificity)
	}
	if empty := summarize(nil); empty != (qualityStats{}) {
		t.Errorf("summarize(nil) = %+v, want zero", empty)
	}
}

func TestResultQualityTop10(t *testing.T) {
	sugs := make([]savedSuggestion, 12)
	sugs[0].Typo = true  // inside top 10
	sugs[11].Typo = true // outside top 10
	q := resultQualityOf(sugs)
	if q.All.Names != 12 || q.Top10.Names != 10 {
		t.Fatalf("names all=%d top10=%d", q.All.Names, q.Top10.Names)
	}
	if q.Top10.TypoRate != 0.1 {
		t.Errorf("Top10.TypoRate = %v, want 0.1", q.Top10.TypoRate)
	}
}

// Snapshots are not guaranteed to be sorted by score (old snapshots are saved
// in LLM order). Top-10 metrics must use score order, not slice position.
func TestResultQualityTop10UsesScoreOrder(t *testing.T) {
	sugs := make([]savedSuggestion, 12)
	for i := range sugs {
		sugs[i] = savedSuggestion{SLD: fmt.Sprintf("s%d", i), Score: float64(i + 1)}
	}
	// The two highest-scored suggestions sit at the end of the slice and are
	// flagged as typos; positional top-10 (indices 0-9) would miss them.
	sugs[10].Score, sugs[10].Typo = 100, true
	sugs[11].Score, sugs[11].Typo = 99, true

	original := make([]savedSuggestion, len(sugs))
	copy(original, sugs)

	q := resultQualityOf(sugs)
	if q.Top10.TypoRate != 0.2 {
		t.Errorf("Top10.TypoRate = %v, want 0.2", q.Top10.TypoRate)
	}
	if !reflect.DeepEqual(sugs, original) {
		t.Errorf("resultQualityOf reordered the saved slice: got %+v, want %+v", sugs, original)
	}
}

func TestAnnotateRunSetsFlags(t *testing.T) {
	run := savedRun{
		Queries: []string{"craft beer delivery", "yoga studio", "coffee shop"},
		Results: []savedQueryResult{{
			Query: "craft beer delivery",
			Suggestions: []savedSuggestion{
				{SLD: "pizzaria"}, {SLD: "late"}, {SLD: "hopsmith"}, {SLD: "ale"},
			},
		}},
	}
	annotateRun(&run, tlds.DefaultRegistry.ICANNSet())
	s := run.Results[0].Suggestions
	if !s[0].Typo || s[1].Typo || s[2].Typo {
		t.Errorf("typo flags = %v %v %v", s[0].Typo, s[1].Typo, s[2].Typo)
	}
	if !s[1].CommonWord || s[3].CommonWord { // late=10; ale=35
		t.Errorf("common flags late=%v ale=%v", s[1].CommonWord, s[3].CommonWord)
	}
	if s[3].Specificity == nil {
		t.Error("ale should have a computable specificity")
	}
	if run.Results[0].Quality == nil || run.Results[0].Quality.All.Names != 4 {
		t.Errorf("Quality = %+v", run.Results[0].Quality)
	}
}

// An old-format snapshot (no config, funnel, run or quality) must gain
// quality fields without acquiring fake zero-valued blocks.
func TestRescoreFileOldSnapshot(t *testing.T) {
	old := `{
  "date": "2026-06-09T04:39:34Z",
  "model": "gemini-3.1-flash-lite",
  "variants": ["current"],
  "queries": ["craft beer delivery", "yoga studio"],
  "results": [{
    "query": "craft beer delivery", "variant": "current",
    "suggestions": [{"name": "pizzaria.beer", "sld": "pizzaria", "tld": "beer", "score": 0.9, "source": "llm"}],
    "dur_ms": 1500, "tokens": 4000, "est_cost_usd": 0.0008
  }]
}`
	path := filepath.Join(t.TempDir(), "run-old.json")
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rescoreFile(path, tlds.DefaultRegistry.ICANNSet()); err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["config"]; ok {
		t.Error("rescore added an empty config block")
	}
	res := raw["results"].([]any)[0].(map[string]any)
	for _, k := range []string{"funnel", "run", "prompt_tokens"} {
		if _, ok := res[k]; ok {
			t.Errorf("rescore added zero-valued %q", k)
		}
	}
	if res["est_cost_usd"].(float64) != 0.0008 {
		t.Errorf("est_cost_usd changed: %v", res["est_cost_usd"])
	}
	if _, ok := res["quality"]; !ok {
		t.Error("quality summary missing")
	}
	sug := res["suggestions"].([]any)[0].(map[string]any)
	if sug["typo"] != true {
		t.Errorf("pizzaria not flagged as typo: %v", sug)
	}
}

// A snapshot with no "queries" key at all must backfill Queries from the
// unique queries seen in Results, in first-seen order (one query repeated
// across two results), and still get every suggestion annotated.
func TestRescoreFileBackfillsQueriesFromResults(t *testing.T) {
	old := `{
  "date": "2026-06-09T04:39:34Z",
  "model": "gemini-3.1-flash-lite",
  "variants": ["current"],
  "results": [
    {
      "query": "craft beer delivery", "variant": "current",
      "suggestions": [{"name": "pizzaria.beer", "sld": "pizzaria", "tld": "beer", "score": 0.9, "source": "llm"}],
      "dur_ms": 1500, "tokens": 4000, "est_cost_usd": 0.0008
    },
    {
      "query": "yoga studio", "variant": "current",
      "suggestions": [{"name": "ale.studio", "sld": "ale", "tld": "studio", "score": 0.5, "source": "llm"}],
      "dur_ms": 1200, "tokens": 3000, "est_cost_usd": 0.0006
    },
    {
      "query": "craft beer delivery", "variant": "current",
      "suggestions": [{"name": "hopsmith.beer", "sld": "hopsmith", "tld": "beer", "score": 0.7, "source": "llm"}],
      "dur_ms": 1300, "tokens": 3500, "est_cost_usd": 0.0007
    }
  ]
}`
	path := filepath.Join(t.TempDir(), "run-old-no-queries.json")
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	run, err := rescoreFile(path, tlds.DefaultRegistry.ICANNSet())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"craft beer delivery", "yoga studio"}
	if len(run.Queries) != len(want) || run.Queries[0] != want[0] || run.Queries[1] != want[1] {
		t.Errorf("Queries = %v, want %v", run.Queries, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	gotQueries := raw["queries"].([]any)
	if len(gotQueries) != 2 || gotQueries[0] != "craft beer delivery" || gotQueries[1] != "yoga studio" {
		t.Errorf("written queries = %v", gotQueries)
	}
	for i, r := range raw["results"].([]any) {
		res := r.(map[string]any)
		sug := res["suggestions"].([]any)[0].(map[string]any)
		if _, ok := sug["typo"]; !ok {
			t.Errorf("result %d: suggestion missing typo annotation: %v", i, sug)
		}
		if _, ok := res["quality"]; !ok {
			t.Errorf("result %d: missing quality summary", i)
		}
	}
}

// rescoreFile must not corrupt an irreplaceable snapshot: if the file can't
// be parsed, it returns an error and leaves the original bytes untouched.
func TestRescoreFileUnparseableLeavesOriginalUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-bad.json")
	original := []byte("{ not valid json")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rescoreFile(path, tlds.DefaultRegistry.ICANNSet()); err == nil {
		t.Fatal("expected an error for an unparseable snapshot")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Errorf("original file was modified: got %q, want %q", got, original)
	}
}

// writeFileAtomic replaces a file's content in one step and preserves the
// conventional 0644 mode, leaving no temp file behind.
func TestWriteFileAtomicReplacesContentAndPreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "atomic.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new content")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new content" {
		t.Errorf("content = %q, want %q", got, "new content")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "atomic.json" {
		t.Errorf("leftover files in dir: %v", entries)
	}
}

// printQuality must pool each result's top 10 by score (not by slice
// position), and must exclude errored results from both the "all" and
// "top10" rows.
func TestPrintQualityPoolsTop10ByScore(t *testing.T) {
	okSugs := make([]savedSuggestion, 12)
	for i := range okSugs {
		okSugs[i] = savedSuggestion{SLD: fmt.Sprintf("s%d", i), Score: float64(i + 1)}
	}
	run := savedRun{
		Variants: []string{"current"},
		Results: []savedQueryResult{
			{
				Query:   "errored query",
				Variant: "current",
				Error:   "boom",
				Suggestions: []savedSuggestion{
					{SLD: "shouldnotcount1", Score: 5},
					{SLD: "shouldnotcount2", Score: 6},
				},
			},
			{
				Query:       "ok query",
				Variant:     "current",
				Suggestions: okSugs,
			},
		},
	}

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	printQuality(run)
	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	var allNames, top10Names int
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		switch fields[1] {
		case "all":
			fmt.Sscanf(fields[2], "%d", &allNames)
		case "top10":
			fmt.Sscanf(fields[2], "%d", &top10Names)
		}
	}
	if allNames != 12 {
		t.Errorf("all row Names = %d, want 12 (errored result's names excluded)", allNames)
	}
	if top10Names != 10 {
		t.Errorf("top10 row Names = %d, want 10", top10Names)
	}
}

func TestStatsByRunAndNoiseBand(t *testing.T) {
	mk := func(run int, typos int) savedQueryResult {
		var sugs []savedSuggestion
		for i := 0; i < 10; i++ {
			sugs = append(sugs, savedSuggestion{SLD: "x", Score: float64(10 - i), Typo: i < typos})
		}
		return savedQueryResult{Query: "q", Variant: "v", Run: run, Suggestions: sugs}
	}
	run := savedRun{Variants: []string{"v"}, Results: []savedQueryResult{mk(1, 1), mk(2, 3), mk(3, 2),
		{Query: "q", Variant: "other", Run: 1, Suggestions: []savedSuggestion{{SLD: "y", Typo: true}}}}}
	st := statsByRun(run, "v")
	if len(st) != 3 {
		t.Fatalf("want 3 runs, got %+v", st)
	}
	if st[0].Run != 1 || st[0].Top10.TypoRate != 0.1 || st[1].Top10.TypoRate != 0.3 || st[2].Top10.TypoRate != 0.2 {
		t.Errorf("per-run typo rates = %+v", st)
	}
	if st[0].KeptPerQuery != 10 {
		t.Errorf("KeptPerQuery = %v", st[0].KeptPerQuery)
	}
	if b := noiseBand([]float64{0.1, 0.3, 0.2}); math.Abs(b-0.2) > 1e-9 {
		t.Errorf("noiseBand = %v, want 0.2", b)
	}
	if b := noiseBand(nil); b != 0 {
		t.Errorf("noiseBand(nil) = %v", b)
	}
}
