package quality

import (
	"slices"
	"testing"
)

func TestEdits1(t *testing.T) {
	got := edits1("ab")
	for _, want := range []string{
		"b", "a", // deletes
		"ba",       // transpose
		"xb", "ay", // replaces
		"cab", "acb", "abc", // inserts
	} {
		if !slices.Contains(got, want) {
			t.Errorf("edits1(\"ab\") missing %q", want)
		}
	}
	// n deletes + (n-1) transposes + 26n replaces + 26(n+1) inserts, duplicates kept
	if len(got) != 2+1+52+78 {
		t.Errorf("len(edits1(\"ab\")) = %d, want 133", len(got))
	}
}

// Expected values verified against SCOWL 2020.12.07 on 2026-09-18.
func TestIsTypo(t *testing.T) {
	for sld, want := range map[string]bool{
		"pizzaria": true, // pizzeria, level 50
		"balanc":   true, // balance, level 10
		"percolat": true, // percolate, level 35
		"fermenta": true, // ferment — borderline coinage; the human check decides

		"pizzeria":   false, // real word
		"tether":     false,
		"brownstone": false,
		"hopsmith":   false, // coinage with no one-edit neighbour
		"evertread":  false,
		"faderoom":   false,
		"vessquest":  false,
		"lumo":       false, // shorter than minTypoLen
		"yest":       false, // known miss: a real (archaic) SCOWL word, and short

		"colour":  false, // real word: British spelling, membership-only (Unranked)
		"centre":  false, // real word: British spelling, membership-only (Unranked)
		"flavour": false, // real word: British spelling, membership-only (Unranked)
		"harbour": false, // real word: British spelling, membership-only (Unranked)
		"theatre": false, // real word: British spelling, membership-only (Unranked)
	} {
		if got := IsTypo(sld); got != want {
			t.Errorf("IsTypo(%q) = %v, want %v", sld, got, want)
		}
	}
}

func TestIsCommonWord(t *testing.T) {
	for sld, want := range map[string]bool{
		"late": true, "cold": true, "balance": true, // level 10
		"mint": true, "echo": true, "balanced": true, // level 20
		"tether": false, "zenith": false, // level 35
		"catalyst": false, // level 40
		"pizzeria": false, // level 50
		"hopsmith": false, // not a word
		"colour":   false, // Unranked (membership-only) is never common
	} {
		if got := IsCommonWord(sld); got != want {
			t.Errorf("IsCommonWord(%q) = %v, want %v", sld, got, want)
		}
	}
}
