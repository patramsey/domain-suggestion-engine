// Builds two result sets from one recorded LLM pool, differing only in
// configuration, so they can be compared without LLM sampling noise.
//
// Usage:
//
//	go run ./cmd/pairgen -snapshot run.json -a hacks -b hacks,compounds,affixes \
//	    -out-a a.json -out-b b.json
//	go run ./cmd/judge compare -a a.json -b b.json -diff-only -per-query 10 -rounds 4
//
// Comparing two separate server runs mixes the effect of a configuration with
// the run-to-run randomness of the LLM, which supplies most of each top 10.
// Here both arms re-rank the same LLM candidates from one eval snapshot, run
// through the production pipeline (scorer.Rank and api.Shape); only the
// algorithmic generators and shaping settings differ. No API calls.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/api"
	"github.com/patlivet/domain-suggestion-engine/internal/parser"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
	"github.com/patlivet/domain-suggestion-engine/internal/tlds"
)

type snapshotSuggestion struct {
	Name   string  `json:"name"`
	SLD    string  `json:"sld"`
	TLD    string  `json:"tld"`
	Score  float64 `json:"score"`
	Source string  `json:"source"`
}

type snapshotResult struct {
	Query       string               `json:"query"`
	Variant     string               `json:"variant"`
	Run         int                  `json:"run"`
	Suggestions []snapshotSuggestion `json:"suggestions"`
}

type snapshot struct {
	Date     string           `json:"date,omitempty"`
	Model    string           `json:"model"`
	Variants []string         `json:"variants"`
	Queries  []string         `json:"queries"`
	Results  []snapshotResult `json:"results"`
}

type settings struct {
	count    int
	llmShare float64
	slots    int
}

// mergePool combines the two tiers' scored candidates the way the handler's
// merge does — a name both tiers produced keeps the LLM copy — and sorts by
// score, highest first.
func mergePool(llm, algo []scorer.ScoredCandidate) []scorer.ScoredCandidate {
	seen := make(map[string]bool, len(llm))
	out := make([]scorer.ScoredCandidate, 0, len(llm)+len(algo))
	for _, c := range llm {
		if !seen[c.Name()] {
			seen[c.Name()] = true
			out = append(out, c)
		}
	}
	for _, c := range algo {
		if !seen[c.Name()] {
			seen[c.Name()] = true
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// arm re-ranks every run-1 result of the snapshot with the given generators.
// LLM candidates keep the score the eval recorded, which includes the LLM
// position bonus; algorithmic candidates are generated and scored fresh.
func arm(snap snapshot, variant string, generators []string, s settings) snapshot {
	icann := tlds.DefaultRegistry.ICANNSet()
	allowed, _ := tlds.Resolve(tlds.Filter{})
	engine := algorithmic.NewEngine(algorithmic.DefaultGenerators(nil, nil), generators)

	out := snapshot{Model: snap.Model, Variants: []string{strings.Join(generators, ",")}}
	for _, r := range snap.Results {
		if (variant != "" && r.Variant != variant) || (r.Run != 0 && r.Run != 1) {
			continue
		}
		tokens := parser.Parse(r.Query, icann)
		var llm []scorer.ScoredCandidate
		for _, sg := range r.Suggestions {
			llm = append(llm, scorer.ScoredCandidate{
				Candidate: algorithmic.Candidate{SLD: sg.SLD, TLD: sg.TLD, Source: "llm"},
				Score:     sg.Score,
			})
		}
		algo := scorer.Rank(engine.Run(tokens, allowed), tokens)
		share := s.llmShare
		if len(generators) == 0 {
			share = 1
		}
		final, _ := api.Shape(mergePool(llm, algo), api.ShapeOptions{
			Count: s.count, NumTLDs: len(allowed), LLMShare: share,
			CommonWordSlots: s.slots, Tokens: tokens,
		})
		res := snapshotResult{Query: r.Query, Variant: out.Variants[0], Run: 1}
		for _, c := range final {
			res.Suggestions = append(res.Suggestions, snapshotSuggestion{
				Name: c.Name(), SLD: c.SLD, TLD: c.TLD, Score: c.Score, Source: c.Source,
			})
		}
		out.Results = append(out.Results, res)
		out.Queries = append(out.Queries, r.Query)
	}
	return out
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	snapPath := flag.String("snapshot", "", "eval snapshot holding the recorded LLM pool")
	variant := flag.String("variant", "", "variant within the snapshot (default: any)")
	genA := flag.String("a", "hacks", "generators for arm A (comma-separated; empty = none)")
	genB := flag.String("b", "hacks,compounds,affixes", "generators for arm B")
	outA := flag.String("out-a", "", "where to write arm A")
	outB := flag.String("out-b", "", "where to write arm B")
	count := flag.Int("count", 20, "results per query")
	llmShare := flag.Float64("llm-share", 0.60, "tier balance, as LLM_SHARE")
	slots := flag.Int("slots", 2, "common-word slots per 10, as COMMON_WORD_SLOTS")
	flag.Parse()
	if *snapPath == "" || *outA == "" || *outB == "" {
		fmt.Fprintln(os.Stderr, "need -snapshot, -out-a and -out-b")
		os.Exit(2)
	}
	b, err := os.ReadFile(*snapPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pairgen: %v\n", err)
		os.Exit(1)
	}
	var snap snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		fmt.Fprintf(os.Stderr, "pairgen: %v\n", err)
		os.Exit(1)
	}
	set := settings{count: *count, llmShare: *llmShare, slots: *slots}
	for _, side := range []struct {
		gens, path string
	}{{*genA, *outA}, {*genB, *outB}} {
		res := arm(snap, *variant, splitList(side.gens), set)
		data, _ := json.MarshalIndent(res, "", " ")
		if err := os.WriteFile(side.path, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "pairgen: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("generators %-28q → %s (%d queries)\n", side.gens, side.path, len(res.Results))
	}
}
