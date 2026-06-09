package scorer

import (
	"fmt"
	"testing"
)

func TestCreativeCoinageScores(t *testing.T) {
	names := []string{
		"breathory", "vinyaspace", "taperstone", "hopsphere",
		"syncopate", "grainflow", "malthaven", "hopsmith",
		"forge", "cedar", "xkqvz",
	}
	for _, w := range names {
		fmt.Printf("%-14s  mem=%.2f  ngram=%.3f  brand=%.3f\n",
			w, memorability(w), ngramBrandability(w), brandability(w))
	}
}
