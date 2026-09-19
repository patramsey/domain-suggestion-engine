// Package quality computes deterministic quality signals for suggested
// domain labels (SLDs). It is used by the eval harness only; production
// ranking does not depend on it.
package quality

import "github.com/patlivet/domain-suggestion-engine/internal/wordlist"

// Thresholds are initial values from the design spec; change them only on
// evidence from the human rating check.
const (
	// minTypoLen: shorter coinages have too many one-edit neighbours
	// ("lumo" → "limo") to call them typos.
	minTypoLen = 5
	// typoNeighbourMaxLevel: a typo's intended word must be at least this common.
	typoNeighbourMaxLevel = 50
)

const letters = "abcdefghijklmnopqrstuvwxyz"

// IsTypo reports whether sld looks like a misspelling: it is not a word, it is
// at least minTypoLen letters, and a word one edit away is reasonably common.
// Examples: "pizzaria" (pizzeria), "balanc" (balance).
//
// sld must already be lowercase; wordlist lookups are case-sensitive.
func IsTypo(sld string) bool {
	if len(sld) < minTypoLen {
		return false
	}
	if _, ok := wordlist.Level(sld); ok {
		return false
	}
	for _, n := range edits1(sld) {
		// wordlist.Unranked (100) exceeds typoNeighbourMaxLevel, so a
		// membership-only neighbour (British spelling, proper noun) never
		// makes sld look like its typo.
		if l, ok := wordlist.Level(n); ok && l <= typoNeighbourMaxLevel {
			return true
		}
	}
	return false
}

// IsCommonWord reports whether sld is a very common English word.
// The threshold is defined in wordlist.CommonMaxLevel.
//
// sld must already be lowercase; wordlist lookups are case-sensitive.
func IsCommonWord(sld string) bool {
	return wordlist.IsCommon(sld)
}

// edits1 returns every string one deletion, adjacent transposition,
// replacement or insertion away from w (lowercase a–z). Duplicates are kept;
// callers only test membership.
func edits1(w string) []string {
	out := make([]string, 0, 54*len(w)+26)
	for i := 0; i <= len(w); i++ {
		l, r := w[:i], w[i:]
		if len(r) > 0 {
			out = append(out, l+r[1:]) // delete
		}
		if len(r) > 1 {
			out = append(out, l+string(r[1])+string(r[0])+r[2:]) // transpose
		}
		for j := 0; j < len(letters); j++ {
			c := letters[j : j+1]
			if len(r) > 0 {
				out = append(out, l+c+r[1:]) // replace
			}
			out = append(out, l+c+r) // insert
		}
	}
	return out
}
