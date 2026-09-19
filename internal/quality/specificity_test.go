package quality

import "testing"

func TestSpecificityPositiveForOnTopicName(t *testing.T) {
	coffee := []string{"coffee"}
	others := [][]string{{"yoga"}, {"software"}, {"finance"}}
	s, ok := Specificity("brew", coffee, others)
	if !ok {
		t.Fatal("Specificity(brew) not computable")
	}
	if s <= 0 {
		t.Errorf("Specificity(brew | coffee) = %v, want > 0", s)
	}
	// The same name judged against an unrelated query should score lower.
	s2, ok := Specificity("brew", []string{"yoga"}, [][]string{{"coffee"}, {"software"}, {"finance"}})
	if !ok || s2 >= s {
		t.Errorf("Specificity(brew | yoga) = %v, want < %v", s2, s)
	}
}

func TestSpecificityNotComputable(t *testing.T) {
	if _, ok := Specificity("xqzv", []string{"coffee"}, [][]string{{"yoga"}}); ok {
		t.Error("no recognisable sub-words should be not computable")
	}
	if _, ok := Specificity("brew", []string{"coffee"}, nil); ok {
		t.Error("no other queries should be not computable")
	}
}
