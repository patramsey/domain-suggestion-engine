package algorithmic

import (
	"slices"
	"testing"
)

func TestCompoundGeneratorPairsTokens(t *testing.T) {
	g := NewCompoundGenerator()
	got := g.Generate([]string{"denver", "coffee"}, []string{"com", "co"})
	names := candidateNames(got)

	if !slices.Contains(names, "denvercoffee.com") {
		t.Errorf("expected denvercoffee.com in candidates, got %v", names)
	}
	if !slices.Contains(names, "coffeedenver.com") {
		t.Errorf("expected coffeedenver.com in candidates, got %v", names)
	}
}

func TestCompoundGeneratorTooShortOrLong(t *testing.T) {
	g := NewCompoundGenerator()
	// Single token cannot pair
	if got := g.Generate([]string{"coffee"}, []string{"com"}); len(got) != 0 {
		t.Errorf("expected no candidates for single token, got %v", candidateNames(got))
	}
}
