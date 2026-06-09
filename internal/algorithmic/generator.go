package algorithmic

import "fmt"

// Candidate is a scored domain name suggestion produced by any generation tier.
type Candidate struct {
	SLD     string
	TLD     string
	Source  string  // "algorithmic" or "llm"
	LLMRank float64 // [0,1] normalised position in the LLM's sorted output; 0 for non-LLM
}

// Name returns the full domain string.
func (c Candidate) Name() string {
	return fmt.Sprintf("%s.%s", c.SLD, c.TLD)
}

// Generator produces domain name candidates from tokens and a resolved TLD set.
type Generator interface {
	// Name returns the identifier used in the GENERATORS env var.
	Name() string
	// Generate produces candidates. Tokens are already expanded with synonyms.
	Generate(tokens []string, tlds []string) []Candidate
}
