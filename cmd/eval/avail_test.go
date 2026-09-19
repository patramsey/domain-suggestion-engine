package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/dnscheck"
)

func TestParseConfigAvail(t *testing.T) {
	cfg, err := parseConfig(nil, env(nil))
	if err != nil || cfg.Avail || cfg.Resolver != "1.1.1.1:53" {
		t.Errorf("defaults: cfg=%+v err=%v", cfg, err)
	}
	cfg, err = parseConfig([]string{"-avail", "-resolver", "9.9.9.9:53"}, env(nil))
	if err != nil || !cfg.Avail || cfg.Resolver != "9.9.9.9:53" {
		t.Errorf("flags: cfg=%+v err=%v", cfg, err)
	}
}

// 25 suggestions saved out of score order; only the top 20 by score are
// looked up, and errored results are skipped.
func TestAnnotateDNSChecksTopTwentyByScore(t *testing.T) {
	var sugs []savedSuggestion
	for i := range 25 {
		// scores 0.01..0.25, saved in a shuffled-looking order
		score := float64((i*7)%25+1) / 100
		sugs = append(sugs, savedSuggestion{Name: fmt.Sprintf("n%02d.com", int(score*100)), Score: score})
	}
	run := savedRun{Results: []savedQueryResult{
		{Query: "q", Suggestions: sugs},
		{Query: "bad", Error: "boom", Suggestions: []savedSuggestion{{Name: "err.com", Score: 1}}},
	}}
	var looked []string
	fake := func(_ context.Context, name string) dnscheck.Outcome {
		looked = append(looked, name)
		if strings.HasPrefix(name, "n2") { // n20..n25 delegated
			return dnscheck.Delegated
		}
		return dnscheck.Free
	}
	annotateDNS(context.Background(), &run, fake, 1)

	if len(looked) != 20 {
		t.Fatalf("looked up %d names, want 20", len(looked))
	}
	for _, s := range run.Results[0].Suggestions {
		inTop := s.Score >= 0.06 // top 20 of 0.01..0.25
		switch {
		case !inTop && s.DNS != "":
			t.Errorf("%s (score %.2f) outside top 20 but DNS=%q", s.Name, s.Score, s.DNS)
		case inTop && s.Score >= 0.20 && s.DNS != "delegated":
			t.Errorf("%s: DNS=%q, want delegated", s.Name, s.DNS)
		case inTop && s.Score < 0.20 && s.DNS != "free":
			t.Errorf("%s: DNS=%q, want free", s.Name, s.DNS)
		}
	}
	if run.Results[1].Suggestions[0].DNS != "" {
		t.Error("errored result should not be checked")
	}
}

func TestAvailSummary(t *testing.T) {
	mk := func(dns ...string) []savedSuggestion {
		var out []savedSuggestion
		for i, d := range dns {
			out = append(out, savedSuggestion{Name: fmt.Sprintf("s%d.com", i), Score: float64(100 - i), DNS: d})
		}
		return out
	}
	results := []savedQueryResult{
		// top 2: one free, one delegated; ranks 3–4 free
		{Query: "a", Suggestions: mk("free", "delegated", "free", "free")},
		// top 2: unknown + delegated → no free name; rank 3 unchecked
		{Query: "b", Suggestions: mk("", "delegated", "")},
	}
	got := availSummary(results, 2)
	want := availStats{Free: 1, Known: 3, Unknown: 1, Queries: 2, QueriesNone: 1}
	if got != want {
		t.Errorf("top 2: got %+v, want %+v", got, want)
	}
	if r := got.Rate(); math.Abs(r-1.0/3) > 1e-9 {
		t.Errorf("Rate = %v, want 1/3", r)
	}
	if p := got.PerQuery(); p != 0.5 {
		t.Errorf("PerQuery = %v, want 0.5", p)
	}
	got = availSummary(results, 4)
	want = availStats{Free: 3, Known: 5, Unknown: 2, Queries: 2, QueriesNone: 1}
	if got != want {
		t.Errorf("top 4: got %+v, want %+v", got, want)
	}
}

func TestHasDNS(t *testing.T) {
	run := savedRun{Results: []savedQueryResult{{Suggestions: []savedSuggestion{{Name: "a.com"}}}}}
	if hasDNS(run) {
		t.Error("no DNS data yet")
	}
	run.Results[0].Suggestions[0].DNS = "free"
	if !hasDNS(run) {
		t.Error("DNS data present")
	}
}
