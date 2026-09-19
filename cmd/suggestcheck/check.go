package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/parser"
	"github.com/patlivet/domain-suggestion-engine/internal/quality"
	"github.com/patlivet/domain-suggestion-engine/internal/tlds"
)

type tldFilter struct {
	List []string `json:"list"`
}

type suggestRequest struct {
	Input              string     `json:"input"`
	TLDFilter          *tldFilter `json:"tld_filter,omitempty"`
	UnavailableDomains []string   `json:"unavailable_domains,omitempty"`
}

type suggestion struct {
	Name   string  `json:"name"`
	SLD    string  `json:"sld"`
	TLD    string  `json:"tld"`
	Score  float64 `json:"score"`
	Source string  `json:"source"`
}

type suggestResponse struct {
	Suggestions []suggestion `json:"suggestions"`
	Partial     bool         `json:"partial"`
}

// result is one /suggest request's outcome.
type result struct {
	Query  string   `json:"query"`
	Kind   string   `json:"kind"` // plain | unavailable | tld-filter
	Status int      `json:"status"`
	DurMs  int64    `json:"dur_ms"`
	Source string   `json:"source"` // X-Generation-Source: both | llm | algorithmic
	Names  []string `json:"names"`
	SLDs   []string `json:"slds"`
	Err    string   `json:"error,omitempty"`
}

// narrowFilter exercises the small-TLD-list edge case and the TLD-filter path.
var narrowFilter = []string{"com", "io"}

func send(ctx context.Context, c *http.Client, base string, kind string, req suggestRequest) result {
	res := result{Query: req.Input, Kind: kind}
	body, _ := json.Marshal(req)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/suggest", bytes.NewReader(body))
	if err != nil {
		res.Err = err.Error()
		return res
	}
	hreq.Header.Set("Content-Type", "application/json")
	t0 := time.Now()
	resp, err := c.Do(hreq)
	res.DurMs = time.Since(t0).Milliseconds()
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	res.Source = resp.Header.Get("X-Generation-Source")
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		res.Err = string(data)
		return res
	}
	var sr suggestResponse
	if err := json.Unmarshal(data, &sr); err != nil {
		res.Err = err.Error()
		return res
	}
	for _, s := range sr.Suggestions {
		res.Names = append(res.Names, s.Name)
		res.SLDs = append(res.SLDs, s.SLD)
	}
	return res
}

// runQuality sends every query once, and for the first `extra` queries also
// an unavailable_domains request (using the first 3 names returned) and a
// narrow TLD-filter request.
func runQuality(ctx context.Context, c *http.Client, base string, queries []string, extra int) []result {
	var out []result
	for i, q := range queries {
		r := send(ctx, c, base, "plain", suggestRequest{Input: q})
		out = append(out, r)
		if i >= extra {
			continue
		}
		taken := r.Names
		if len(taken) > 3 {
			taken = taken[:3]
		}
		if len(taken) == 0 {
			taken = []string{"example.com"}
		}
		out = append(out, send(ctx, c, base, "unavailable", suggestRequest{Input: q, UnavailableDomains: taken}))
		out = append(out, send(ctx, c, base, "tld-filter", suggestRequest{Input: q, TLDFilter: &tldFilter{List: narrowFilter}}))
	}
	return out
}

type qualitySummary struct {
	Requests        int     `json:"requests"`
	Errors          int     `json:"errors"`     // transport errors or non-200
	LLMFailed       int     `json:"llm_failed"` // 200 but the LLM tier did not contribute
	Names           int     `json:"names"`      // top-10 names scored
	TypoRate        float64 `json:"typo_rate"`
	CommonWordRate  float64 `json:"common_word_rate"`
	MeanSpecificity float64 `json:"mean_specificity"`
	NamesPerRequest float64 `json:"names_per_request"`
}

// summarizeQuality scores each successful response's top 10 names (the
// response is already ranked) with the same metrics as the eval.
func summarizeQuality(res []result, queries []string) qualitySummary {
	icann := tlds.DefaultRegistry.ICANNSet()
	tokens := map[string][]string{}
	for _, q := range queries {
		tokens[q] = parser.Parse(q, icann)
	}
	s := qualitySummary{Requests: len(res)}
	var typo, common, known, returned, ok int
	var spec float64
	for _, r := range res {
		if r.Err != "" || r.Status != http.StatusOK {
			s.Errors++
			continue
		}
		ok++
		if r.Source == "algorithmic" {
			s.LLMFailed++
		}
		returned += len(r.SLDs)
		top := r.SLDs
		if len(top) > 10 {
			top = top[:10]
		}
		var others [][]string
		for _, q := range queries {
			if q != r.Query {
				others = append(others, tokens[q])
			}
		}
		for _, sld := range top {
			s.Names++
			if quality.IsTypo(sld) {
				typo++
			}
			if quality.IsCommonWord(sld) {
				common++
			}
			if v, ok := quality.Specificity(sld, tokens[r.Query], others); ok {
				spec += v
				known++
			}
		}
	}
	if s.Names > 0 {
		s.TypoRate = float64(typo) / float64(s.Names)
		s.CommonWordRate = float64(common) / float64(s.Names)
	}
	if known > 0 {
		s.MeanSpecificity = spec / float64(known)
	}
	if ok > 0 {
		s.NamesPerRequest = float64(returned) / float64(ok)
	}
	return s
}

// runLoad sends n plain requests cycling through queries, with c concurrent
// workers.
func runLoad(ctx context.Context, cl *http.Client, base string, queries []string, n, c int) []result {
	out := make([]result, n)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < c; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				out[i] = send(ctx, cl, base, "plain", suggestRequest{Input: queries[i%len(queries)]})
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out
}

type loadSummary struct {
	N           int     `json:"n"`
	Errors      int     `json:"errors"`
	LLMFailed   int     `json:"llm_failed"`
	FailureRate float64 `json:"failure_rate"` // (errors + llm_failed) / n
	P50Ms       int64   `json:"p50_ms"`
	P95Ms       int64   `json:"p95_ms"`
	P99Ms       int64   `json:"p99_ms"`
}

func summarizeLoad(res []result) loadSummary {
	s := loadSummary{N: len(res)}
	var durs []int64
	for _, r := range res {
		if r.Err != "" || r.Status != http.StatusOK {
			s.Errors++
			continue
		}
		if r.Source == "algorithmic" {
			s.LLMFailed++
		}
		durs = append(durs, r.DurMs)
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	s.P50Ms, s.P95Ms, s.P99Ms = percentile(durs, 0.50), percentile(durs, 0.95), percentile(durs, 0.99)
	if s.N > 0 {
		s.FailureRate = float64(s.Errors+s.LLMFailed) / float64(s.N)
	}
	return s
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
