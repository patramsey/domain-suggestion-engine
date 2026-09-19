package quality

import "testing"

// Expected values verified against SCOWL 2020.12.07 on 2026-09-19.
func TestIsCompound(t *testing.T) {
	cases := map[string]bool{
		"duskbrew":   true,  // dusk (35) + brew (35)
		"hopcrate":   true,  // hop + crate
		"questlab":   true,  // quest + lab
		"corebound":  true,  // core + bound
		"coffeeshop": true,  // coffee + shop
		"nightcap":   false, // a dictionary word itself
		"blendora":   false, // "ora" is SCOWL 70, not a common word
		"talentix":   false, // "tix" is not a word
		"medita":     false, // truncated word
		"zenflow":    false, // "zen" is known only as unranked
		"coffee":     false,
		"ab":         false,
	}
	for sld, want := range cases {
		if got := IsCompound(sld); got != want {
			t.Errorf("IsCompound(%q) = %v, want %v", sld, got, want)
		}
	}
}
