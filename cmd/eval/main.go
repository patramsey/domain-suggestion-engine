// Prompt evaluation harness. Runs multiple prompt variants against fixed queries
// and reports objective quality metrics for comparison.
//
// Usage: GEMINI_API_KEY=$(cat ~/.gemini-api-key) go run ./cmd/eval [flags]
//
//	-model        Gemini model ID (default $GEMINI_MODEL, else gemini-3.5-flash-lite)
//	-thinking     thinkingLevel: minimal (default), low, medium, high
//	-temperature  0–2; 0 (default) uses each variant's own temperature
//	-runs         times to run each query (default 1)
//	-queries      query set: core (default), hard, all
//	-variant      prompt variant(s): comma-separated names, or all (default current)
//	-label        short label added to the snapshot filename
//	-rescore      re-annotate an existing snapshot with quality metrics (no API calls)
//	-avail        DNS-check each query's top 20 names for likely availability (works with -rescore)
//	-resolver     DNS resolver for -avail (default 1.1.1.1:53)
//
// Results are saved to eval-results/ — see eval-results/README.md for history.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/evalset"
	"github.com/patlivet/domain-suggestion-engine/internal/llm"
	"github.com/patlivet/domain-suggestion-engine/internal/parser"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
	"github.com/patlivet/domain-suggestion-engine/internal/tlds"
)

// --- result types ---

type queryResult struct {
	variant      string
	query        string
	run          int
	parsedTokens []string
	candidates   []algorithmic.Candidate
	durMs        int64
	usage        llm.TokenUsage
	funnel       llm.Funnel
	estCostUSD   float64
	costKnown    bool
	err          error
}

type metrics struct {
	count          int
	tldDiversity   int
	avgSLDLen      float64
	p50Ms          int64
	p90Ms          int64
	thoughtsTokens int
	estCostUSD     float64
	costKnown      bool
}

// --- main ---

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: %v\n", err)
		os.Exit(2)
	}
	if cfg.Rescore != "" {
		run, err := rescoreFile(cfg.Rescore, tlds.DefaultRegistry.ICANNSet(), cfg.availHook()...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval: rescore: %v\n", err)
			os.Exit(1)
		}
		printQuality(run)
		printQualityByRun(run)
		printAvailability(run)
		fmt.Printf("\nUpdated: %s\n", cfg.Rescore)
		return
	}
	queries, _ := evalset.Queries(cfg.QuerySet)                  // validated by parseConfig
	variants, _ = selectVariants(allVariants, cfg.VariantFilter) // validated by parseConfig

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "GEMINI_API_KEY not set")
		os.Exit(1)
	}
	if _, ok := prices[cfg.Model]; !ok {
		fmt.Fprintf(os.Stderr, "warning: no price for %s in pricing.go; cost will be reported as n/a\n", cfg.Model)
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

	total := len(variants) * len(queries) * cfg.Runs
	fmt.Printf("=== Prompt Evaluation ===\nModel: %s | thinking %s | %d variants × %d queries (%s) × %d runs = %d LLM call groups\n\n",
		cfg.Model, cfg.ThinkingLevel, len(variants), len(queries), cfg.QuerySet, cfg.Runs, total)

	// rate limit: max 3 concurrent groups (each group = 3 parallel variant calls internally)
	sem := make(chan struct{}, 3)
	var mu sync.Mutex
	var results []queryResult
	var wg sync.WaitGroup

	done := 0
	for run := 1; run <= cfg.Runs; run++ {
		for _, v := range variants {
			for _, q := range queries {
				wg.Add(1)
				go func(run int, v promptVariant, q string) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()

					client := llm.NewClient(apiKey, cfg.Model)
					client.Temperature = cfg.effectiveTemperature(v)
					client.ThinkingLevel = cfg.ThinkingLevel

					tokens := parser.Parse(q, icannSet)
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()

					t0 := time.Now()
					cands, usage, funnel, callErr := client.EvalGenerate(ctx, v.system, q, tokens, resolvedTLDs, tldSet, 20, v.variantOverrides)
					durMs := time.Since(t0).Milliseconds()
					cost, costKnown := estimateCost(cfg.Model, usage)

					r := queryResult{
						variant:      v.name,
						query:        q,
						run:          run,
						parsedTokens: tokens,
						candidates:   cands,
						durMs:        durMs,
						usage:        usage,
						funnel:       funnel,
						estCostUSD:   cost,
						costKnown:    costKnown,
						err:          callErr,
					}

					mu.Lock()
					results = append(results, r)
					done++
					fmt.Printf("  [%d/%d] run %d %-16s %s\n", done, total, run, v.name, truncate(q, 45))
					mu.Unlock()
				}(run, v, q)
			}
		}
	}
	wg.Wait()

	// deterministic order: by variant, then query order, then run
	queryIdx := make(map[string]int, len(queries))
	for i, q := range queries {
		queryIdx[q] = i
	}
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.variant != b.variant {
			return a.variant < b.variant
		}
		if a.query != b.query {
			return queryIdx[a.query] < queryIdx[b.query]
		}
		return a.run < b.run
	})

	printReport(queries, results)
	run := buildSnapshot(cfg, queries, results)
	annotateRun(&run, icannSet)
	for _, hook := range cfg.availHook() {
		hook(&run)
	}
	printQuality(run)
	printQualityByRun(run)
	printAvailability(run)
	if err := writeSnapshot(run, cfg.Label); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save results: %v\n", err)
	}
}

// --- reporting ---

func printReport(queries []string, results []queryResult) {
	byVariant := make(map[string][]queryResult)
	for _, r := range results {
		byVariant[r.variant] = append(byVariant[r.variant], r)
	}

	// generic threshold: >25% of queries (scales with query set size)
	genericThreshold := int(float64(len(queries))*0.25) + 1
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

	fmt.Printf("\n\n=== AGGREGATE SUMMARY (%d queries, per-query averages across runs) ===\n\n", len(queries))
	fmt.Printf("%-20s  %7s  %8s  %9s  %13s  %7s  %7s  %9s  %10s\n",
		"Variant", "Results", "TLD Div", "Avg SLD", "Generic SLDs", "p50 ms", "p90 ms", "Think tok", "$/query")
	fmt.Println(strings.Repeat("-", 108))

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
		cost := "n/a"
		if m.costKnown {
			cost = fmt.Sprintf("%.6f", m.estCostUSD)
		}
		fmt.Printf("%-20s  %7d  %8d  %9.1f  %5d (%4.0f%%)  %7d  %7d  %9d  %10s\n",
			vName, m.count, m.tldDiversity, m.avgSLDLen,
			len(generics), genericPct,
			m.p50Ms, m.p90Ms, m.thoughtsTokens, cost)
	}

	printFunnel(variantOrder, byVariant)

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

	fmt.Printf("\n\n=== TOP 5 SUGGESTIONS PER QUERY (run 1) ===\n")
	for _, q := range queries {
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
	var totalCount, totalTLDDiv, totalThoughts int
	var totalSLDLen, totalCost float64
	var durs []int64
	costKnown := true
	for _, r := range results {
		if r.err != nil {
			continue
		}
		totalCount += len(r.candidates)
		totalTLDDiv += tldDiversity(r.candidates)
		totalSLDLen += avgSLDLen(r.candidates)
		totalThoughts += r.usage.ThoughtsTokens
		totalCost += r.estCostUSD
		costKnown = costKnown && r.costKnown
		durs = append(durs, r.durMs)
	}
	n := len(durs)
	if n == 0 {
		return metrics{}
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	return metrics{
		count:          totalCount / n,
		tldDiversity:   totalTLDDiv / n,
		avgSLDLen:      totalSLDLen / float64(n),
		p50Ms:          percentile(durs, 0.50),
		p90Ms:          percentile(durs, 0.90),
		thoughtsTokens: totalThoughts / n,
		estCostUSD:     totalCost / float64(n),
		costKnown:      costKnown,
	}
}

// percentile returns the nearest-rank percentile of sorted values.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	return sorted[i]
}

// printFunnel shows, per variant, where the names the model returned were lost.
func printFunnel(variantOrder []string, byVariant map[string][]queryResult) {
	fmt.Printf("\n\n=== YIELD FUNNEL (totals across all queries and runs) ===\n\n")
	fmt.Printf("%-20s  %9s  %8s  %8s  %7s  %7s  %8s  %8s  %8s  %6s  %7s  %7s  %7s\n",
		"Variant", "Requested", "Returned", "BadFmt", "Trunc", "BadTLD", "DupInVar", "DupAcross", "Kept", "Kept%", "Failed", "Partial", "MaxTok")
	fmt.Println(strings.Repeat("-", 124))
	for _, vName := range variantOrder {
		var f llm.Funnel
		for _, r := range byVariant[vName] {
			f = f.Add(r.funnel)
		}
		keptPct := 0.0
		if f.Requested > 0 {
			keptPct = float64(f.Kept) / float64(f.Requested) * 100
		}
		fmt.Printf("%-20s  %9d  %8d  %8d  %7d  %7d  %8d  %8d  %8d  %5.0f%%  %7d  %7d  %7d\n",
			vName, f.Requested, f.Returned, f.BadFormat, f.Truncated, f.UnknownTLD,
			f.DupInVariant, f.DupAcrossVariants, f.Kept, keptPct,
			f.FailedCalls, f.PartialParses, f.MaxTokenStops)
	}
	fmt.Println("  Failed/Partial/MaxTok count variant calls; the other columns count names.")
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
	Name        string   `json:"name"`
	SLD         string   `json:"sld"`
	TLD         string   `json:"tld"`
	Score       float64  `json:"score"`
	Source      string   `json:"source"`
	Typo        bool     `json:"typo"`
	CommonWord  bool     `json:"common_word"`
	Specificity *float64 `json:"specificity"`   // null when not computable
	DNS         string   `json:"dns,omitempty"` // -avail: "free" or "delegated"; empty when unchecked or the lookup failed
}

type savedQueryResult struct {
	Query          string            `json:"query"`
	Variant        string            `json:"variant"`
	Run            int               `json:"run,omitempty"`
	Suggestions    []savedSuggestion `json:"suggestions"`
	DurMs          int64             `json:"dur_ms"`
	Tokens         int               `json:"tokens"`
	PromptTokens   int               `json:"prompt_tokens,omitempty"`
	OutputTokens   int               `json:"output_tokens,omitempty"`
	ThoughtsTokens int               `json:"thoughts_tokens,omitempty"`
	EstCostUSD     *float64          `json:"est_cost_usd"` // null when the model has no known price
	Funnel         *llm.Funnel       `json:"funnel,omitempty"`
	Quality        *resultQuality    `json:"quality,omitempty"`
	Error          string            `json:"error,omitempty"`
}

type savedRun struct {
	Date     string             `json:"date"`
	Model    string             `json:"model"`
	Config   *snapshotConfig    `json:"config,omitempty"`
	Variants []string           `json:"variants"`
	Queries  []string           `json:"queries"`
	Results  []savedQueryResult `json:"results"`
}

// buildSnapshot converts results into the saved snapshot form.
func buildSnapshot(cfg evalConfig, queries []string, results []queryResult) savedRun {
	snapCfg := buildSnapshotConfig(cfg, variants)
	run := savedRun{
		Date:     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Model:    cfg.Model,
		Config:   &snapCfg,
		Variants: variantNames(),
		Queries:  queries,
	}
	for _, r := range results {
		funnel := r.funnel
		sr := savedQueryResult{
			Query:          r.query,
			Variant:        r.variant,
			Run:            r.run,
			DurMs:          r.durMs,
			Tokens:         r.usage.TotalTokens,
			PromptTokens:   r.usage.PromptTokens,
			OutputTokens:   r.usage.CandidateTokens,
			ThoughtsTokens: r.usage.ThoughtsTokens,
			Funnel:         &funnel,
		}
		if r.costKnown {
			cost := r.estCostUSD
			sr.EstCostUSD = &cost
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
	return run
}

// writeSnapshot writes run to a new timestamped file in eval-results/.
// Commit these files to track suggestion drift over time.
func writeSnapshot(run savedRun, label string) error {
	filename := snapshotName(time.Now(), label)
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("\nSaved: %s\n", filename)
	return nil
}
