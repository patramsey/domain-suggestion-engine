package algorithmic

// Engine runs the hacks generator and deduplicates results.
type Engine struct {
	generators []Generator
}

// NewEngine builds an engine containing only the generators named in active.
func NewEngine(all []Generator, active []string) *Engine {
	activeSet := make(map[string]struct{}, len(active))
	for _, name := range active {
		activeSet[name] = struct{}{}
	}
	var gen []Generator
	for _, g := range all {
		if _, ok := activeSet[g.Name()]; ok {
			gen = append(gen, g)
		}
	}
	return &Engine{generators: gen}
}

// Run executes all active generators and returns deduplicated candidates.
func (e *Engine) Run(tokens []string, tlds []string) []Candidate {
	seen := make(map[string]struct{})
	var out []Candidate
	for _, g := range e.generators {
		for _, c := range g.Generate(tokens, tlds) {
			key := c.SLD + "." + c.TLD
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			c.Source = "algorithmic"
			out = append(out, c)
		}
	}
	return out
}

// Active returns the names of currently active generators.
func (e *Engine) Active() []string {
	names := make([]string, len(e.generators))
	for i, g := range e.generators {
		names[i] = g.Name()
	}
	return names
}

// DefaultGenerators returns all built-in generators.
func DefaultGenerators(idTLDs []string, isIDTLD func(string) bool) []Generator {
	return []Generator{
		NewHacksGenerator(),
		NewExactTLDGenerator(),
	}
}
