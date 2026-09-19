package main

import (
	"context"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/patlivet/domain-suggestion-engine/internal/dnscheck"
	"github.com/patlivet/domain-suggestion-engine/internal/wordlist"
)

func TestProbeLabelsDeterministic(t *testing.T) {
	a, b := probeLabels(60), probeLabels(60)
	if len(a) != 60 {
		t.Fatalf("got %d labels, want 60", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("probe labels must be deterministic")
		}
		l, ok := wordlist.Level(a[i])
		if !ok || l < 35 || l > 70 || len(a[i]) < 4 || len(a[i]) > 8 {
			t.Errorf("label %q out of range (level %d, len %d)", a[i], l, len(a[i]))
		}
	}
}

func TestMeasureCountsOutcomes(t *testing.T) {
	fake := func(_ context.Context, name string) dnscheck.Outcome {
		switch {
		case strings.HasPrefix(name, "x"):
			return dnscheck.Unknown
		case strings.HasSuffix(name, ".crowded"):
			return dnscheck.Delegated
		default:
			return dnscheck.Free
		}
	}
	c := measure(context.Background(), []string{"crowded", "roomy"}, []string{"alpha", "beta", "xray"}, fake, 2)
	if c["crowded"] != [2]int{0, 2} { // xray is unknown for both
		t.Errorf("crowded = %v, want [0 2]", c["crowded"])
	}
	if c["roomy"] != [2]int{2, 2} {
		t.Errorf("roomy = %v, want [2 2]", c["roomy"])
	}
}

func TestFreeRatesSmoothingAndCoverage(t *testing.T) {
	r := freeRates(map[string][2]int{"a": {8, 8}, "b": {0, 8}, "c": {1, 2}}, 5)
	if r["a"] != 0.9 || r["b"] != 0.1 { // (8+1)/(8+2), (0+1)/(8+2)
		t.Errorf("rates = %v", r)
	}
	if _, ok := r["c"]; ok {
		t.Error("TLDs with fewer than minKnown answers must be omitted")
	}
}

func TestRenderParses(t *testing.T) {
	src, err := render(map[string]float64{"io": 0.19, "bar": 0.9}, "test header")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0); err != nil {
		t.Fatalf("rendered file does not parse: %v\n%s", err, src)
	}
	s := string(src)
	if !strings.Contains(s, "package scorer") || !strings.Contains(s, "var tldFreeRate") ||
		strings.Index(s, `"bar"`) > strings.Index(s, `"io"`) {
		t.Errorf("unexpected render:\n%s", s)
	}
}
