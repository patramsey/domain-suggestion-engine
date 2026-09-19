package quality

import "github.com/patlivet/domain-suggestion-engine/internal/wordlist"

// compoundPartMaxLevel is the highest SCOWL level a part may have: common
// enough that a reader recognises it instantly.
const compoundPartMaxLevel = 50

// IsCompound reports whether sld is two real words joined ("duskbrew",
// "hopcrate"): sld is not itself a word, and it splits into two parts of at
// least 3 letters that are both SCOWL words at level ≤ compoundPartMaxLevel.
// Suffix coinages ("blendora", "talentix") are not compounds. In blind
// ratings compounds rated like dictionary words while other coinages rated
// far lower, and compounds are rarely registered.
func IsCompound(sld string) bool {
	if len(sld) < 6 {
		return false
	}
	if _, ok := wordlist.Level(sld); ok {
		return false
	}
	for i := 3; i <= len(sld)-3; i++ {
		if isPart(sld[:i]) && isPart(sld[i:]) {
			return true
		}
	}
	return false
}

func isPart(w string) bool {
	l, ok := wordlist.Level(w)
	return ok && l <= compoundPartMaxLevel
}
