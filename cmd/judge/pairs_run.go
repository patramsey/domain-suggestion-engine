package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/llm"
)

func newFlagSet(name string) *flag.FlagSet { return flag.NewFlagSet(name, flag.ExitOnError) }

func trimExt(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// askPairs sends pair questions in batches and collects the winners.
func askPairs(ctx context.Context, c *llm.Client, model string, pairs []pair, size, conc int) (map[string]string, float64, error) {
	return askPairsWithSystem(ctx, c, model, pairSystemPrompt, pairs, size, conc)
}

func askPairsWithSystem(ctx context.Context, c *llm.Client, model, system string, pairs []pair, size, conc int) (map[string]string, float64, error) {
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		cost     float64
		firstErr error
		done     int
	)
	out := make(map[string]string, len(pairs))
	batches := batchPairs(pairs, size)
	sem := make(chan struct{}, conc)
	for _, b := range batches {
		wg.Add(1)
		go func(b []pair) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			text, usage, err := c.Complete(cctx, system, pairMessage(b))

			mu.Lock()
			defer mu.Unlock()
			if v, ok := llm.EstimateCost(model, usage); ok {
				cost += v
			}
			done++
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			got, err := parseWinners(text)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			for id, w := range got {
				out[id] = w
			}
			fmt.Fprintf(os.Stderr, "  [%d/%d] batches compared\n", done, len(batches))
		}(b)
	}
	wg.Wait()
	return out, cost, firstErr
}

func batchPairs(pairs []pair, size int) [][]pair {
	var out [][]pair
	for i := 0; i < len(pairs); i += size {
		out = append(out, pairs[i:min(i+size, len(pairs))])
	}
	return out
}

func runPairs(args []string) error {
	fs := newFlagSet("pairs")
	history := fs.String("history", "eval-results/ratings/history.json", "human ratings to build pairs from")
	perQuery := fs.Int("per-query", 6, "pairs per query")
	seed := fs.Int64("seed", 1, "pairing seed")
	thinking := fs.String("thinking", "minimal", "Gemini thinkingLevel: minimal, low, medium, high")
	model := fs.String("model", defaultModel, "model to judge with")
	size := fs.Int("batch", 10, "pairs per request")
	nExamples := fs.Int("examples", 0, "held-out settled comparisons to show the judge")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var entries []historyEntry
	if err := readJSON(*history, &entries); err != nil {
		return err
	}
	pairs := buildPairs(entries, *perQuery, *seed)
	if len(pairs) == 0 {
		return fmt.Errorf("no good-vs-worse pairs found in %s", *history)
	}
	system := pairSystemPrompt
	if *nExamples > 0 {
		if *nExamples >= len(pairs) {
			return fmt.Errorf("-examples %d leaves nothing to measure on", *nExamples)
		}
		system += pairExamples(pairs[:*nExamples])
		pairs = pairs[*nExamples:]
		fmt.Fprintf(os.Stderr, "Showing %d settled comparisons; measuring on the other %d.\n", *nExamples, len(pairs))
	}
	c, err := newClient(*model, *thinking)
	if err != nil {
		return err
	}
	ask := make([]pair, 0, len(pairs)*2)
	for _, p := range pairs {
		ask = append(ask, p, swapped(p))
	}
	fmt.Fprintf(os.Stderr, "Asking %d pairs in both orders with %s ...\n", len(pairs), *model)
	winners, cost, err := askPairsWithSystem(context.Background(), c, *model, system, ask, *size, 3)
	if err != nil {
		return err
	}
	res := scorePairs(pairs, winners)
	fmt.Printf("\n=== PAIRWISE AGREEMENT (%s, %d pairs, %s) ===\n\n", *model, res.N, costLabel(*model, cost))
	fmt.Printf("  picks the human's preferred name   %5.1f%%   (coin flip = 50%%)\n", res.Accuracy()*100)
	fmt.Printf("  same answer in both orders         %5.1f%%   (order bias shows up here)\n", res.ConsistentRate()*100)
	return nil
}
