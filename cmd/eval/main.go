// Prompt evaluation harness. Runs multiple prompt variants against fixed queries
// and reports objective quality metrics for comparison.
//
// Usage: GEMINI_API_KEY=$(cat ~/.gemini-api-key) go run ./cmd/eval
// Results are saved to eval-results/ — see eval-results/README.md for history.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/llm"
	"github.com/patlivet/domain-suggestion-engine/internal/parser"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
	"github.com/patlivet/domain-suggestion-engine/internal/tlds"
)

// --- prompt variants ---
// Add new variants here to test against the current baseline.
// Historical variants and results are documented in eval-results/README.md.

type promptVariant struct {
	name             string
	system           string
	temperature      float64
	variantOverrides []string // optional: replaces the 3 creative-variant instructions (must be len 3)
}

var variants = []promptVariant{
	{name: "current", system: llm.SystemPrompt, temperature: 1.0},
	// Add experimental variants below, e.g.:
	// {name: "my-experiment", system: myPrompt, temperature: 1.0},
}

// --- test queries ---
// 16 queries across 8 distinct verticals for statistically stable results.
// Generic threshold is 25% of query count (currently 4/16).

var testQueries = []string{
	// wellness / fitness
	"yoga studio",
	"meditation and mindfulness app for anxiety",

	// tech / SaaS
	"AI-powered legal document review tool for small law firms",
	"developer tool for automating code reviews with AI",
	"team project management and async communication tool",

	// community / media
	"indie game developer community and showcase",
	"podcast hosting and analytics platform",

	// food / drink
	"a denver based coffee shop that serves beer at night",
	"craft beer subscription box monthly delivery",

	// existing domain input
	"patspizza.com",

	// marketplaces
	"vintage clothing resale marketplace",
	"freelance marketplace for creative professionals",

	// physical / local
	"neighborhood barbershop in brooklyn",

	// other verticals
	"personal finance and budgeting app for millennials",
	"online learning platform for professional photography",
	"sustainable outdoor gear and apparel brand",
}

// --- result types ---

type queryResult struct {
	variant      string
	query        string
	parsedTokens []string
	candidates   []algorithmic.Candidate
	durMs        int64
	tokens       int
	estCostUSD   float64
	err          error
}

type metrics struct {
	count        int
	tldDiversity int
	avgSLDLen    float64
	durMs        int64
	tokens       int
	estCostUSD   float64
}

// --- main ---

func main() {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "GEMINI_API_KEY not set")
		os.Exit(1)
	}
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-3.1-flash-lite"
	}

	resolvedTLDs, err := tlds.Resolve(tlds.Filter{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve TLDs: %v\n", err)
		os.Exit(1)
	}
	tldSet := make(map[string]struct{}, len(resolvedTLDs))
	for _, t := range resolvedTLDs {
		tldSet[t] = struct{}{}
	}
	icannSet := tlds.DefaultRegistry.ICANNSet()

	total := len(variants) * len(testQueries)
	fmt.Printf("=== Prompt Evaluation ===\nModel: %s | %d variants × %d queries = %d LLM call groups\n\n",
		model, len(variants), len(testQueries), total)

	// rate limit: max 3 concurrent groups (each group = 3 parallel variant calls internally)
	sem := make(chan struct{}, 3)
	var mu sync.Mutex
	var results []queryResult
	var wg sync.WaitGroup

	done := 0
	for _, v := range variants {
		for _, q := range testQueries {
			wg.Add(1)
			go func(v promptVariant, q string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				client := llm.NewClient(apiKey, model)
				client.Temperature = v.temperature

				tokens := parser.Parse(q, icannSet)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				t0 := time.Now()
				cands, usage, callErr := client.EvalGenerate(ctx, v.system, q, tokens, resolvedTLDs, tldSet, 20, v.variantOverrides)
				durMs := time.Since(t0).Milliseconds()

				const inputPrice = 0.075 / 1_000_000
				const outputPrice = 0.30 / 1_000_000
				cost := float64(usage.PromptTokens)*inputPrice + float64(usage.CandidateTokens)*outputPrice

				r := queryResult{
					variant:      v.name,
					query:        q,
					parsedTokens: tokens,
					candidates:   cands,
					durMs:        durMs,
					tokens:       usage.TotalTokens,
					estCostUSD:   cost,
					err:          callErr,
				}

				mu.Lock()
				results = append(results, r)
				done++
				fmt.Printf("  [%d/%d] %-20s %s\n", done, total, v.name, truncate(q, 45))
				mu.Unlock()
			}(v, q)
		}
	}
	wg.Wait()

	printReport(results)
	if err := saveResults(model, results); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save results: %v\n", err)
	}
}

// --- reporting ---

func printReport(results []queryResult) {
	byVariant := make(map[string][]queryResult)
	for _, r := range results {
		byVariant[r.variant] = append(byVariant[r.variant], r)
	}

	// generic threshold: >25% of queries (scales with query set size)
	genericThreshold := int(float64(len(testQueries))*0.25) + 1
	genericSLDs := make(map[string]map[string]struct{})
	for vName, vResults := range byVariant {
		sldQueries := make(map[string]map[string]struct{})
		for _, r := range vResults {
			for _, c := range r.candidates {
				if sldQueries[c.SLD] == nil {
					sldQueries[c.SLD] = make(map[string]struct{})
				}
				sldQueries[c.SLD][r.query] = struct{}{}
			}
		}
		genericSLDs[vName] = make(map[string]struct{})
		for sld, queries := range sldQueries {
			if len(queries) >= genericThreshold {
				genericSLDs[vName][sld] = struct{}{}
			}
		}
	}

	fmt.Printf("\n\n=== AGGREGATE SUMMARY (%d queries) ===\n\n", len(testQueries))
	fmt.Printf("%-20s  %7s  %8s  %9s  %13s  %8s  %10s\n",
		"Variant", "Results", "TLD Div", "Avg SLD", "Generic SLDs", "ms/q", "$/query")
	fmt.Println(strings.Repeat("-", 90))

	variantOrder := variantNames()
	for _, vName := range variantOrder {
		vResults, ok := byVariant[vName]
		if !ok {
			continue
		}
		m := aggregateMetrics(vResults)
		generics := genericSLDs[vName]
		genericPct := 0.0
		if m.count > 0 {
			totalSuggs := 0
			genericTotal := 0
			for _, r := range vResults {
				totalSuggs += len(r.candidates)
				for _, c := range r.candidates {
					if _, ok := generics[c.SLD]; ok {
						genericTotal++
					}
				}
			}
			if totalSuggs > 0 {
				genericPct = float64(genericTotal) / float64(totalSuggs) * 100
			}
		}
		fmt.Printf("%-20s  %7d  %8d  %9.1f  %5d (%4.0f%%)  %8d  %10.6f\n",
			vName, m.count, m.tldDiversity, m.avgSLDLen,
			len(generics), genericPct,
			m.durMs, m.estCostUSD)
	}

	fmt.Printf("\n\n=== CROSS-QUERY GENERIC SLDs (appeared in >25%% of queries) ===\n")
	for _, vName := range variantOrder {
		generics := genericSLDs[vName]
		if len(generics) == 0 {
			fmt.Printf("  %-20s  (none)\n", vName)
			continue
		}
		words := make([]string, 0, len(generics))
		for sld := range generics {
			words = append(words, sld)
		}
		sort.Strings(words)
		fmt.Printf("  %-20s  %s\n", vName, strings.Join(words, ", "))
	}

	fmt.Printf("\n\n=== TOP 5 SUGGESTIONS PER QUERY ===\n")
	for _, q := range testQueries {
		fmt.Printf("\nQuery: %s\n", q)
		for _, vName := range variantOrder {
			var r *queryResult
			for i := range results {
				if results[i].variant == vName && results[i].query == q {
					r = &results[i]
					break
				}
			}
			if r == nil || r.err != nil {
				fmt.Printf("  %-20s  ERROR\n", vName)
				continue
			}
			ranked := scorer.Rank(r.candidates, r.parsedTokens)
			if len(ranked) > 5 {
				ranked = ranked[:5]
			}
			names := make([]string, len(ranked))
			for i, c := range ranked {
				names[i] = c.Name()
			}
			fmt.Printf("  %-20s  %s\n", vName, strings.Join(names, "  "))
		}
	}
}

func aggregateMetrics(results []queryResult) metrics {
	if len(results) == 0 {
		return metrics{}
	}
	var totalCount, totalTLDDiv, totalDur, totalTokens int
	var totalSLDLen float64
	var totalCost float64
	n := 0
	for _, r := range results {
		if r.err != nil {
			continue
		}
		n++
		totalCount += len(r.candidates)
		totalTLDDiv += tldDiversity(r.candidates)
		totalSLDLen += avgSLDLen(r.candidates)
		totalDur += int(r.durMs)
		totalTokens += r.tokens
		totalCost += r.estCostUSD
	}
	if n == 0 {
		return metrics{}
	}
	return metrics{
		count:        totalCount / n,
		tldDiversity: totalTLDDiv / n,
		avgSLDLen:    totalSLDLen / float64(n),
		durMs:        int64(totalDur / n),
		tokens:       totalTokens / n,
		estCostUSD:   totalCost / float64(n),
	}
}

func tldDiversity(candidates []algorithmic.Candidate) int {
	seen := make(map[string]struct{})
	for _, c := range candidates {
		seen[c.TLD] = struct{}{}
	}
	return len(seen)
}

func avgSLDLen(candidates []algorithmic.Candidate) float64 {
	if len(candidates) == 0 {
		return 0
	}
	total := 0
	for _, c := range candidates {
		total += len(c.SLD)
	}
	return float64(total) / float64(len(candidates))
}

func variantNames() []string {
	names := make([]string, len(variants))
	for i, v := range variants {
		names[i] = v.name
	}
	return names
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// --- result persistence ---

type savedSuggestion struct {
	Name   string  `json:"name"`
	SLD    string  `json:"sld"`
	TLD    string  `json:"tld"`
	Score  float64 `json:"score"`
	Source string  `json:"source"`
}

type savedQueryResult struct {
	Query       string            `json:"query"`
	Variant     string            `json:"variant"`
	Suggestions []savedSuggestion `json:"suggestions"`
	DurMs       int64             `json:"dur_ms"`
	Tokens      int               `json:"tokens"`
	EstCostUSD  float64           `json:"est_cost_usd"`
	Error       string            `json:"error,omitempty"`
}

type savedRun struct {
	Date     string             `json:"date"`
	Model    string             `json:"model"`
	Variants []string           `json:"variants"`
	Queries  []string           `json:"queries"`
	Results  []savedQueryResult `json:"results"`
}

// saveResults writes a timestamped JSON snapshot to eval-results/.
// Commit these files to track suggestion drift over time.
func saveResults(model string, results []queryResult) error {
	run := savedRun{
		Date:     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Model:    model,
		Variants: variantNames(),
		Queries:  testQueries,
	}
	for _, r := range results {
		sr := savedQueryResult{
			Query:      r.query,
			Variant:    r.variant,
			DurMs:      r.durMs,
			Tokens:     r.tokens,
			EstCostUSD: r.estCostUSD,
		}
		if r.err != nil {
			sr.Error = r.err.Error()
		}
		for _, sc := range scorer.Rank(r.candidates, r.parsedTokens) {
			sr.Suggestions = append(sr.Suggestions, savedSuggestion{
				Name:   sc.Name(),
				SLD:    sc.SLD,
				TLD:    sc.TLD,
				Score:  sc.Score,
				Source: sc.Source,
			})
		}
		run.Results = append(run.Results, sr)
	}

	filename := fmt.Sprintf("eval-results/run-%s.json",
		time.Now().UTC().Format("2006-01-02T150405"))
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return err
	}
	fmt.Printf("\nSaved: %s\n", filename)
	return nil
}
