package algorithmic

import (
	"slices"
	"testing"
)

var allTLDs = []string{
	"com", "io", "ai", "co", "net", "app", "dev", "me",
	"pizza", "coffee", "cafe", "studio", "ee", "am", "gg",
}

// --- Hacks ---

func TestHacksStudio(t *testing.T) {
	g := NewHacksGenerator()
	got := g.Generate([]string{"studio"}, allTLDs)
	names := candidateNames(got)
	if !slices.Contains(names, "stud.io") {
		t.Errorf("hacks missing stud.io; got %v", names)
	}
}

func TestHacksCoffee(t *testing.T) {
	g := NewHacksGenerator()
	got := g.Generate([]string{"coffee"}, allTLDs)
	names := candidateNames(got)
	if !slices.Contains(names, "coff.ee") {
		t.Errorf("hacks missing coff.ee; got %v", names)
	}
}

func TestHacksMinSLDLength(t *testing.T) {
	g := NewHacksGenerator()
	got := g.Generate([]string{"io"}, allTLDs)
	for _, c := range got {
		if len(c.SLD) < 2 {
			t.Errorf("hacks produced SLD shorter than 2 chars: %q", c.SLD)
		}
	}
}

// --- Engine ---

func TestEngineRunsRegisteredGenerators(t *testing.T) {
	all := DefaultGenerators(allTLDs, nil)
	e := NewEngine(all, []string{"hacks"})
	got := e.Run([]string{"coffee"}, allTLDs)
	if len(got) == 0 {
		t.Fatal("engine produced no candidates")
	}
	for _, c := range got {
		if c.Source != "algorithmic" {
			t.Errorf("candidate source should be 'algorithmic', got %q", c.Source)
		}
	}
}

func TestEngineDeduplicates(t *testing.T) {
	all := DefaultGenerators(allTLDs, nil)
	e := NewEngine(all, []string{"hacks"})
	got := e.Run([]string{"studio"}, allTLDs)
	seen := make(map[string]int)
	for _, c := range got {
		seen[c.Name()]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("engine produced duplicate candidate %q (%d times)", name, count)
		}
	}
}

func TestEngineActiveList(t *testing.T) {
	all := DefaultGenerators(allTLDs, nil)
	e := NewEngine(all, []string{"hacks"})
	active := e.Active()
	if len(active) != 1 || active[0] != "hacks" {
		t.Errorf("expected [hacks], got %v", active)
	}
}

func TestNewGeneratorCanBeRegistered(t *testing.T) {
	custom := &trivialGen{}
	all := []Generator{custom}
	e := NewEngine(all, []string{"trivial"})
	got := e.Run([]string{"test"}, []string{"com"})
	if len(got) == 0 {
		t.Fatal("custom generator produced nothing")
	}
}

type trivialGen struct{}

func (g *trivialGen) Name() string { return "trivial" }
func (g *trivialGen) Generate(tokens []string, tlds []string) []Candidate {
	return []Candidate{{SLD: "hello", TLD: "com"}}
}

func candidateNames(cs []Candidate) []string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name()
	}
	return names
}
