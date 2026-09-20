package algorithmic

// AffixGenerator generates candidates by applying high-converting brand prefixes
// and suffixes to query tokens (e.g. "coffee" → "getcoffee.com", "coffeehq.io").
type AffixGenerator struct{}

// NewAffixGenerator creates a new AffixGenerator.
func NewAffixGenerator() *AffixGenerator {
	return &AffixGenerator{}
}

func (g *AffixGenerator) Name() string { return "affixes" }

var (
	brandPrefixes = []string{"get", "the", "go", "my", "try", "join"}
	brandSuffixes = []string{"hq", "app", "lab", "hub", "co", "box", "space", "base"}
)

func (g *AffixGenerator) Generate(tokens []string, tlds []string) []Candidate {
	if len(tokens) == 0 || len(tlds) == 0 {
		return nil
	}

	targetTLDs := selectTargetTLDs(tlds, 5)

	seen := make(map[string]struct{})
	var out []Candidate

	add := func(sld, tld string) {
		if len(sld) < 4 || len(sld) > 18 {
			return
		}
		if !sldRe.MatchString(sld) {
			return
		}
		key := sld + "." + tld
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, Candidate{SLD: sld, TLD: tld})
	}

	for _, tok := range tokens {
		if len(tok) < 3 || len(tok) > 12 {
			continue
		}

		// Try prefixes
		for _, pre := range brandPrefixes {
			sld := pre + tok
			for _, tld := range targetTLDs {
				add(sld, tld)
			}
		}

		// Try suffixes
		for _, suf := range brandSuffixes {
			sld := tok + suf
			for _, tld := range targetTLDs {
				add(sld, tld)
			}
		}
	}

	return out
}
