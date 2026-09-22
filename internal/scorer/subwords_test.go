package scorer

import (
	"slices"
	"testing"
)

// Segmentation rules: cover as many letters as possible, prefer fewer
// segments, and allow a 2-letter segment only when the whole SLD is covered.
func TestSubWordsSegmentation(t *testing.T) {
	cases := map[string][]string{
		"aidrive":  {"ai", "drive"},  // issue #19: not the greedy "aid"
		"gofast":   {"go", "fast"},   // 2-letter prefix, full coverage
		"aicoach":  {"ai", "coach"},  //
		"duskbrew": {"dusk", "brew"}, // unchanged behaviour
		"iodesk":   {"desk"},         // "od"+"es" leaves letters over, so it is rejected
		"qwertyuu": nil,              // no junk 2-letter splits
		"coffee":   {"coffee"},       // a whole word stays whole
	}
	for sld, want := range cases {
		if got := subWords(sld); !slices.Equal(got, want) {
			t.Errorf("subWords(%q) = %v, want %v", sld, got, want)
		}
	}
}

func TestMemorabilityShortSLDs(t *testing.T) {
	// 1-2 letter names are memorable by definition — no lookup, so "io"
	// works without a curated list (issue #19).
	for _, sld := range []string{"ai", "go", "io", "x"} {
		if got := memorability(sld); got != 1.0 {
			t.Errorf("memorability(%q) = %v, want 1.0", sld, got)
		}
	}
}

func TestMemorabilityRejectsGibberish(t *testing.T) {
	for _, sld := range []string{"qwertyuu", "zenithia"} {
		if got := memorability(sld); got > 0.8 {
			t.Errorf("memorability(%q) = %v, want ≤ 0.8 (was inflated by 2-letter fragments)", sld, got)
		}
	}
	if got := memorability("duskbrew"); got != 1.0 {
		t.Errorf("memorability(duskbrew) = %v, want 1.0", got)
	}
}

// Two stacked 2-letter words are not a compound, they are noise.
func TestSubWordsRejectsStackedShortParts(t *testing.T) {
	if got := subWords("asan"); slices.Contains(got, "as") && slices.Contains(got, "an") {
		t.Errorf(`subWords("asan") = %v, want no "as"+"an" split`, got)
	}
	if got := subWords("aidrive"); !slices.Equal(got, []string{"ai", "drive"}) {
		t.Errorf("one short part must still be allowed: %v", got)
	}
}

func TestMemorabilityWholeDictionaryWord(t *testing.T) {
	for _, sld := range []string{"stillness", "solstice", "nightcap", "coffee"} {
		if got := memorability(sld); got != 1.0 {
			t.Errorf("memorability(%q) = %v, want 1.0 — it is a dictionary word", sld, got)
		}
	}
}
