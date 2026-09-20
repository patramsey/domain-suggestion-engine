package algorithmic

import (
	"slices"
	"testing"
)

func TestAffixGeneratorPrefixesAndSuffixes(t *testing.T) {
	g := NewAffixGenerator()
	got := g.Generate([]string{"coffee"}, []string{"com", "io"})
	names := candidateNames(got)

	// Prefixes
	if !slices.Contains(names, "getcoffee.com") {
		t.Errorf("expected getcoffee.com in candidates, got %v", names)
	}
	// Suffixes
	if !slices.Contains(names, "coffeehq.com") {
		t.Errorf("expected coffeehq.com in candidates, got %v", names)
	}
	if !slices.Contains(names, "coffeelab.io") {
		t.Errorf("expected coffeelab.io in candidates, got %v", names)
	}
}

func TestAffixGeneratorShortTokenIgnored(t *testing.T) {
	g := NewAffixGenerator()
	// Token < 3 letters ignored
	if got := g.Generate([]string{"a"}, []string{"com"}); len(got) != 0 {
		t.Errorf("expected no candidates for 1-char token, got %v", candidateNames(got))
	}
}
