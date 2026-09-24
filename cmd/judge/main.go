// Rates domain-name suggestions with an LLM, in the same format the human
// rating page produces, so cmd/ratings analyze can read either.
//
// Usage: GEMINI_API_KEY=$(cat ~/.gemini-api-key) go run ./cmd/judge <command>
//
//	rate      -dir DIR [-model M] [-batch N] [-out FILE]
//	          rates DIR/items.json into DIR/judge-ratings.json
//	calibrate -history FILE [-n N] [-seed S] [-model M] [-examples K]
//	          rates names the human already rated and reports agreement
//	pairs     -history FILE [-per-query N] [-seed S] [-model M]
//	          asks which of two rated names is better and scores the answers
//	          against the human's preference, each pair asked in both orders
//	compare   -a SNAPSHOT -b SNAPSHOT [-a-variant V] [-b-variant V]
//	          runs two eval snapshots head to head and reports a win rate
//
// The judge is a measuring instrument for evals only: the server never calls it.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/llm"
)

const defaultModel = "gemini-3.5-flash-lite"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "rate":
		err = runRate(os.Args[2:])
	case "calibrate":
		err = runCalibrate(os.Args[2:])
	case "pairs":
		err = runPairs(os.Args[2:])
	case "compare":
		err = runCompare(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "judge: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  judge rate      -dir DIR [-model M] [-batch N] [-out FILE]
  judge calibrate -history FILE [-n N] [-seed S] [-model M] [-examples K]
  judge pairs     -history FILE [-per-query N] [-seed S] [-model M]
  judge compare   -a SNAPSHOT -b SNAPSHOT [-a-variant V] [-b-variant V]
                  [-top N] [-per-query N] [-model M]
`)
	os.Exit(2)
}

// rateAll rates every item, batch at a time, conc batches in parallel.
func rateAll(ctx context.Context, c *llm.Client, model string, items []item, size, conc int) (map[string]string, float64, error) {
	return rateAllWithSystem(ctx, c, model, systemPrompt, items, size, conc)
}

func rateAllWithSystem(ctx context.Context, c *llm.Client, model, system string, items []item, size, conc int) (map[string]string, float64, error) {
	batches := batch(items, size)
	out := make(map[string]string, len(items))
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		cost     float64
		firstErr error
		done     int
	)
	sem := make(chan struct{}, conc)
	for _, b := range batches {
		wg.Add(1)
		go func(b []item) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			text, usage, err := c.Complete(cctx, system, userMessage(b))

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
			got, err := parseRatings(text)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			for id, r := range got {
				out[id] = r
			}
			fmt.Fprintf(os.Stderr, "  [%d/%d] batches rated\n", done, len(batches))
		}(b)
	}
	wg.Wait()
	return out, cost, firstErr
}

// newClient builds the judge's client. thinking is the Gemini thinkingLevel;
// some models (gemini-3.8-flash) reject "minimal", so it is a flag.
func newClient(model, thinking string) (*llm.Client, error) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY not set")
	}
	c := llm.NewClient(key, model)
	c.Temperature = 0 // a judge should be as repeatable as the API allows
	c.ThinkingLevel = thinking
	return c, nil
}

func runRate(args []string) error {
	fs := flag.NewFlagSet("rate", flag.ExitOnError)
	dir := fs.String("dir", "", "directory holding items.json")
	model := fs.String("model", defaultModel, "model to judge with")
	size := fs.Int("batch", 20, "names per request")
	thinking := fs.String("thinking", "minimal", "Gemini thinkingLevel: minimal, low, medium, high")
	out := fs.String("out", "", "output file (default DIR/judge-ratings.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("-dir is required")
	}
	var items []item
	if err := readJSON(filepath.Join(*dir, "items.json"), &items); err != nil {
		return err
	}
	c, err := newClient(*model, *thinking)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Rating %d names with %s ...\n", len(items), *model)
	got, cost, err := rateAll(context.Background(), c, *model, items, *size, 3)
	if err != nil {
		return err
	}
	rows := make([]rating, 0, len(got))
	for _, it := range items {
		if r, ok := got[it.ID]; ok {
			rows = append(rows, rating{ID: it.ID, Rating: r})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	path := *out
	if path == "" {
		path = filepath.Join(*dir, "judge-ratings.json")
	}
	if err := writeJSON(path, rows); err != nil {
		return err
	}
	fmt.Printf("Rated %d of %d names. %s. Wrote %s\n", len(rows), len(items), costLabel(*model, cost), path)
	return nil
}

// historyEntry is one line of eval-results/ratings/history.json.
type historyEntry struct {
	Query  string `json:"query"`
	Domain string `json:"domain"`
	Rating string `json:"rating"`
	Round  int    `json:"round"`
}

func runCalibrate(args []string) error {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	history := fs.String("history", "eval-results/ratings/history.json", "human ratings to compare against")
	n := fs.Int("n", 0, "how many to sample (0 = all)")
	seed := fs.Int64("seed", 1, "sampling seed")
	model := fs.String("model", defaultModel, "model to judge with")
	size := fs.Int("batch", 20, "names per request")
	outDir := fs.String("out", "", "directory to write judge-ratings.json and items.json into")
	nExamples := fs.Int("examples", 0, "held-out human ratings to show the judge as calibration examples")
	thinking := fs.String("thinking", "minimal", "Gemini thinkingLevel: minimal, low, medium, high")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var entries []historyEntry
	if err := readJSON(*history, &entries); err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Query != entries[j].Query {
			return entries[i].Query < entries[j].Query
		}
		return entries[i].Domain < entries[j].Domain
	})
	if *n > 0 && *n < len(entries) {
		rng := rand.New(rand.NewSource(*seed))
		rng.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
		entries = entries[:*n]
	}
	items := make([]item, len(entries))
	human := make(map[string]string, len(entries))
	for i, e := range entries {
		id := fmt.Sprintf("h%04d", i)
		items[i] = item{ID: id, Query: e.Query, Domain: e.Domain}
		human[id] = e.Rating
	}
	humanAll := make(map[string]string, len(human))
	for k, v := range human {
		humanAll[k] = v
	}
	system := systemPrompt
	if *nExamples > 0 {
		if *nExamples >= len(items) {
			return fmt.Errorf("-examples %d leaves nothing to measure on", *nExamples)
		}
		ex := items[:*nExamples]
		items = items[*nExamples:]
		for _, it := range ex {
			delete(human, it.ID)
		}
		exRatings := map[string]string{}
		for _, it := range ex {
			exRatings[it.ID] = humanAll[it.ID]
		}
		system += examplesBlock(ex, exRatings)
		fmt.Fprintf(os.Stderr, "Showing the judge %d example ratings; measuring on the other %d.\n", len(ex), len(items))
	}
	c, err := newClient(*model, *thinking)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Judging %d already-rated names with %s ...\n", len(items), *model)
	got, cost, err := rateAllWithSystem(context.Background(), c, *model, system, items, *size, 3)
	if err != nil {
		return err
	}
	st := agreement(human, got)
	printStats(st, *model, cost)

	if *outDir != "" {
		if err := os.MkdirAll(*outDir, 0o755); err != nil {
			return err
		}
		rows := make([]rating, 0, len(got))
		for id, r := range got {
			rows = append(rows, rating{ID: id, Rating: r})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
		if err := writeJSON(filepath.Join(*outDir, "items.json"), items); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(*outDir, "judge-ratings.json"), rows); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(*outDir, "human-ratings.json"), human); err != nil {
			return err
		}
		fmt.Printf("Wrote items, judge ratings and human ratings to %s\n", *outDir)
	}
	return nil
}

func printStats(st stats, model string, cost float64) {
	fmt.Printf("\n=== JUDGE AGREEMENT (%s, %d names, %s) ===\n\n", model, st.N, costLabel(model, cost))
	if st.Missing > 0 {
		fmt.Printf("  %d names came back unrated.\n", st.Missing)
	}
	fmt.Printf("  exact verdict      %5.1f%%\n", st.ExactRate()*100)
	fmt.Printf("  good vs not-good   %5.1f%%   (what the eval gates on)\n", st.GoodVsNotRate()*100)
	fmt.Printf("  rated good: human  %5.1f%%   judge %5.1f%%\n", st.HumanGoodShare()*100, st.JudgeGoodShare()*100)
	order := []string{"good", "okay", "bad"}
	fmt.Printf("\n  human \\ judge %8s %8s %8s\n", "good", "okay", "bad")
	for _, h := range order {
		fmt.Printf("  %-13s", h)
		for _, j := range order {
			fmt.Printf(" %8d", st.Confusion[h][j])
		}
		fmt.Println()
	}
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
