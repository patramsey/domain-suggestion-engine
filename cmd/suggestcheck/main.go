// Command suggestcheck exercises a running /suggest server with the eval
// query sets. quality mode scores the names users would see (top 10 per
// response) and exercises the unavailable_domains and TLD-filter paths;
// load mode measures latency and failures under concurrency. Run the server
// with CACHE_SIZE=0 so every request reaches the model.
//
// Usage:
//
//	go run ./cmd/suggestcheck quality -url http://localhost:8080 -queries all -out quality.json
//	go run ./cmd/suggestcheck load -url http://localhost:8080 -queries all -n 200 -c 5 -out load.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/evalset"
)

func main() {
	if len(os.Args) < 2 || (os.Args[1] != "quality" && os.Args[1] != "load") {
		fmt.Fprintln(os.Stderr, "usage: suggestcheck quality|load [flags]")
		os.Exit(2)
	}
	mode := os.Args[1]
	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	url := fs.String("url", "http://localhost:8080", "base URL of the running server")
	set := fs.String("queries", "all", "query set: core, hard or all")
	out := fs.String("out", "", "write raw results and summary as JSON to this file")
	extra := fs.Int("extra", 4, "quality: queries that also get unavailable_domains and TLD-filter requests")
	n := fs.Int("n", 200, "load: total requests")
	c := fs.Int("c", 5, "load: concurrent workers")
	fs.Parse(os.Args[2:])

	queries, err := evalset.Queries(*set)
	if err != nil {
		fmt.Fprintf(os.Stderr, "suggestcheck: %v\n", err)
		os.Exit(2)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	ctx := context.Background()

	var report any
	if mode == "quality" {
		res := runQuality(ctx, client, *url, queries, *extra)
		s := summarizeQuality(res, queries)
		fmt.Printf("requests=%d errors=%d llm_failed=%d names/request=%.1f\n", s.Requests, s.Errors, s.LLMFailed, s.NamesPerRequest)
		fmt.Printf("top-10: names=%d typo=%.1f%% common=%.1f%% mean_spec=%.3f\n", s.Names, s.TypoRate*100, s.CommonWordRate*100, s.MeanSpecificity)
		report = map[string]any{"summary": s, "results": res}
	} else {
		res := runLoad(ctx, client, *url, queries, *n, *c)
		s := summarizeLoad(res)
		fmt.Printf("n=%d errors=%d llm_failed=%d failure_rate=%.2f%% p50=%dms p95=%dms p99=%dms\n",
			s.N, s.Errors, s.LLMFailed, s.FailureRate*100, s.P50Ms, s.P95Ms, s.P99Ms)
		report = map[string]any{"summary": s, "results": res}
	}
	if *out != "" {
		data, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(*out, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "suggestcheck: write %s: %v\n", *out, err)
			os.Exit(1)
		}
		fmt.Printf("Saved: %s\n", *out)
	}
}
