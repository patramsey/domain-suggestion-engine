package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeServer answers /suggest with fixed names; requests whose input starts
// with "fail" get algorithmic-only responses (an LLM-tier failure).
func fakeServer(t *testing.T, kinds *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req suggestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case len(req.UnavailableDomains) > 0:
			*kinds = append(*kinds, "unavailable")
		case req.TLDFilter != nil:
			*kinds = append(*kinds, "tld-filter")
		default:
			*kinds = append(*kinds, "plain")
		}
		src := "both"
		if len(req.Input) >= 4 && req.Input[:4] == "fail" {
			src = "algorithmic"
		}
		w.Header().Set("X-Generation-Source", src)
		json.NewEncoder(w).Encode(suggestResponse{Suggestions: []suggestion{
			{Name: "pizzaria.pizza", SLD: "pizzaria"}, {Name: "late.cafe", SLD: "late"}, {Name: "faderoom.shop", SLD: "faderoom"},
		}})
	}))
}

func TestRunQualityRequestKinds(t *testing.T) {
	var kinds []string
	srv := fakeServer(t, &kinds)
	defer srv.Close()
	res := runQuality(context.Background(), srv.Client(), srv.URL, []string{"q1", "q2", "q3", "q4", "q5", "q6"}, 4)
	if len(res) != 6+4+4 {
		t.Fatalf("want 14 results, got %d", len(res))
	}
	count := map[string]int{}
	for _, k := range kinds {
		count[k]++
	}
	if count["plain"] != 6 || count["unavailable"] != 4 || count["tld-filter"] != 4 {
		t.Errorf("request kinds = %v", count)
	}
}

func TestSummarizeQuality(t *testing.T) {
	res := []result{
		// verified 2026-09-19: pizzaria = typo; late = common (SCOWL 10); faderoom and hopsmith = neither
		{Query: "craft beer delivery", Kind: "plain", Status: 200, Source: "both", SLDs: []string{"pizzaria", "late", "faderoom"}},
		{Query: "yoga studio", Kind: "plain", Status: 200, Source: "algorithmic", SLDs: []string{"hopsmith"}},
		{Query: "yoga studio", Kind: "plain", Status: 500},
	}
	s := summarizeQuality(res, []string{"craft beer delivery", "yoga studio"})
	if s.Requests != 3 || s.Errors != 1 || s.LLMFailed != 1 {
		t.Errorf("counts = %+v", s)
	}
	if s.Names != 4 || math.Abs(s.TypoRate-0.25) > 1e-9 || math.Abs(s.CommonWordRate-0.25) > 1e-9 {
		t.Errorf("rates = %+v", s)
	}
	if math.Abs(s.NamesPerRequest-2.0) > 1e-9 {
		t.Errorf("NamesPerRequest = %v", s.NamesPerRequest)
	}
}

func TestRunLoadAndSummarize(t *testing.T) {
	var kinds []string
	srv := fakeServer(t, &kinds)
	defer srv.Close()
	res := runLoad(context.Background(), srv.Client(), srv.URL, []string{"ok one", "fail two"}, 10, 3)
	if len(res) != 10 {
		t.Fatalf("want 10 results, got %d", len(res))
	}
	s := summarizeLoad(res)
	if s.N != 10 || s.Errors != 0 || s.LLMFailed != 5 {
		t.Errorf("load summary = %+v", s)
	}
	if math.Abs(s.FailureRate-0.5) > 1e-9 {
		t.Errorf("FailureRate = %v", s.FailureRate)
	}
}

func TestPercentile(t *testing.T) {
	v := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	if p := percentile(v, 0.50); p != 50 {
		t.Errorf("p50 = %d", p)
	}
	if p := percentile(v, 0.95); p != 100 {
		t.Errorf("p95 = %d", p)
	}
	if p := percentile(nil, 0.5); p != 0 {
		t.Errorf("empty = %d", p)
	}
}
