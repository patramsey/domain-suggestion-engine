package algorithmic

import (
	"slices"
	"testing"
)

func TestExactTLDChicagoPlumbing(t *testing.T) {
	g := NewExactTLDGenerator()
	got := g.Generate([]string{"chicago", "plumbing"}, []string{"com", "plumbing", "net"})
	names := candidateNames(got)

	if !slices.Contains(names, "chicago.plumbing") {
		t.Errorf("expected chicago.plumbing in candidates, got %v", names)
	}
}

func TestExactTLDDenverCoffee(t *testing.T) {
	g := NewExactTLDGenerator()
	got := g.Generate([]string{"denver", "coffee"}, []string{"com", "coffee"})
	names := candidateNames(got)

	if !slices.Contains(names, "denver.coffee") {
		t.Errorf("expected denver.coffee in candidates, got %v", names)
	}
}

func TestExactTLDPrefixMatch(t *testing.T) {
	g := NewExactTLDGenerator()
	// "developer" prefix matches .dev
	got := g.Generate([]string{"react", "developer"}, []string{"com", "dev"})
	names := candidateNames(got)

	if !slices.Contains(names, "react.dev") {
		t.Errorf("expected react.dev in candidates, got %v", names)
	}
}

func TestExactTLDInflectionMatch(t *testing.T) {
	g := NewExactTLDGenerator()
	// "plumbers" inflects to .plumbing
	got := g.Generate([]string{"chicago", "plumbers"}, []string{"com", "plumbing"})
	names := candidateNames(got)

	if !slices.Contains(names, "chicago.plumbing") {
		t.Errorf("expected chicago.plumbing in candidates, got %v", names)
	}
}

func TestExactTLDMultipleTokens(t *testing.T) {
	g := NewExactTLDGenerator()
	got := g.Generate([]string{"best", "chicago", "plumbing"}, []string{"com", "plumbing"})
	names := candidateNames(got)

	if !slices.Contains(names, "bestchicago.plumbing") {
		t.Errorf("expected bestchicago.plumbing in candidates, got %v", names)
	}
	if !slices.Contains(names, "chicago.plumbing") {
		t.Errorf("expected chicago.plumbing in candidates, got %v", names)
	}
}

func TestExactTLDNoMatch(t *testing.T) {
	g := NewExactTLDGenerator()
	got := g.Generate([]string{"chicago", "plumbing"}, []string{"com", "net", "io"})
	if len(got) != 0 {
		t.Errorf("expected no candidates when no TLD matches tokens, got %v", candidateNames(got))
	}
}

func TestExactTLDAvoidsStutter(t *testing.T) {
	g := NewExactTLDGenerator()
	got := g.Generate([]string{"plumbing"}, []string{"plumbing"})
	if len(got) != 0 {
		t.Errorf("expected no candidates for single token matching TLD, got %v", candidateNames(got))
	}
}
