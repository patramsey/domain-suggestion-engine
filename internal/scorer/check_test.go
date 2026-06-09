package scorer

import (
	"fmt"
	"testing"
)

func TestPrintNgramScores(t *testing.T) {
	words := []string{"forge", "ember", "cedar", "stripe", "coffee", "zymmo", "rhythm", "xkqvz", "zzzzz", "aeiou", "flow", "bloom", "craft"}
	for _, w := range words {
		fmt.Printf("%-12s  ngram=%.3f  brandability=%.3f\n", w, ngramBrandability(w), brandability(w))
	}
}
