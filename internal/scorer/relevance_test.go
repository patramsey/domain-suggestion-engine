package scorer

import "testing"

func TestConceptRelevanceNotComputable(t *testing.T) {
	cases := []struct {
		sld    string
		tokens []string
	}{
		{"brew", nil},                // no query tokens
		{"xqzv", []string{"coffee"}}, // no recognisable sub-words
	}
	for _, c := range cases {
		if _, ok := ConceptRelevance(c.sld, c.tokens); ok {
			t.Errorf("ConceptRelevance(%q, %v) ok = true, want false", c.sld, c.tokens)
		}
		if got := conceptRelevance(c.sld, c.tokens); got != 0.5 {
			t.Errorf("conceptRelevance(%q, %v) = %v, want neutral 0.5", c.sld, c.tokens, got)
		}
	}
}

func TestConceptRelevanceWrapperMatches(t *testing.T) {
	for _, sld := range []string{"brew", "roast", "yoga", "ale"} {
		r, ok := ConceptRelevance(sld, []string{"coffee"})
		if !ok {
			t.Fatalf("ConceptRelevance(%q) not computable", sld)
		}
		if r < 0.1 || r > 1.0 {
			t.Errorf("ConceptRelevance(%q) = %v, want within [0.1, 1.0]", sld, r)
		}
		if got := conceptRelevance(sld, []string{"coffee"}); got != r {
			t.Errorf("wrapper = %v, exported = %v for %q", got, r, sld)
		}
	}
}
