package wordlist

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestSourceIsPinnedRelease(t *testing.T) {
	want := "source=scowl-2020.12.07 sha256=5587667caa20c4891390c2d42dbb4d5c4c3f41bee77af1457ece3ba23fb859cc max_level=70 "
	if !strings.HasPrefix(Source(), want) {
		t.Errorf("Source() = %q, want prefix %q", Source(), want)
	}
	if !strings.Contains(Source(), fmt.Sprintf("words=%d", Len())) {
		t.Errorf("header word count does not match decoded Len() = %d: %q", Len(), Source())
	}
	if Len() < 100_000 {
		t.Errorf("Len() = %d, expected ~111.6K words", Len())
	}
}

// Levels verified against the SCOWL 2020.12.07 final/ files on 2026-09-18.
func TestLevelKnownWords(t *testing.T) {
	for word, want := range map[string]int{
		"late": 10, "cold": 10, "balance": 10,
		"mint": 20, "echo": 20, "balanced": 20, // inflections are listed
		"tether": 35, "honed": 35, "zenith": 35,
		"catalyst": 40, "yest": 40,
		"pizzeria": 50,
	} {
		got, ok := Level(word)
		if !ok || got != want {
			t.Errorf("Level(%q) = %d, %v; want %d, true", word, got, ok, want)
		}
	}
}

func TestLevelAbsentWords(t *testing.T) {
	for _, word := range []string{"pizzaria", "balanc", "hopsmith", "", "Late"} {
		if _, ok := Level(word); ok {
			t.Errorf("Level(%q) found; want absent", word)
		}
	}
}

// British spellings and proper nouns are known to SCOWL only as regional
// variants (final/british*, final/canadian*, final/australian*, final/variant_1
// words files) or proper names/upper entries, so they carry no ranked
// commonness level; they are membership-only words at the Unranked sentinel.
// Verified against SCOWL 2020.12.07 on 2026-09-18: "casper" (a US place name)
// appears only in final/american-proper-names.50; it is not in any
// english/american ranked words file at any level.
func TestLevelUnrankedWords(t *testing.T) {
	for _, word := range []string{"colour", "centre", "flavour", "casper"} {
		got, ok := Level(word)
		if !ok || got != Unranked {
			t.Errorf("Level(%q) = %d, %v; want %d, true", word, got, ok, Unranked)
		}
	}
}

func TestIsCommon(t *testing.T) {
	for w, want := range map[string]bool{
		"late": true, "balance": true, "mint": true, "balanced": true, // levels 10–20
		"tether": false, "catalyst": false, "pizzeria": false, // 35, 40, 50
		"colour":   false, // Unranked
		"hopsmith": false, "": false,
	} {
		if got := IsCommon(w); got != want {
			t.Errorf("IsCommon(%q) = %v, want %v", w, got, want)
		}
	}
}

func TestWordsRangeSortedRankedOnly(t *testing.T) {
	ws := Words(35, 35)
	if len(ws) < 1000 {
		t.Fatalf("Words(35,35) returned %d words, expected thousands", len(ws))
	}
	if !sort.StringsAreSorted(ws) {
		t.Error("Words must be sorted")
	}
	for _, w := range ws[:50] {
		if l, _ := Level(w); l != 35 {
			t.Errorf("Words(35,35) returned %q at level %d", w, l)
		}
	}
	for _, w := range Words(10, 100) {
		if l, _ := Level(w); l == Unranked {
			t.Fatalf("Words must never return Unranked words; got %q", w)
		}
	}
}
