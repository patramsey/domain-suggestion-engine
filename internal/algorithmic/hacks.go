package algorithmic

import (
	"regexp"
	"sort"
	"strings"
)

var sldRe = regexp.MustCompile(`^[a-z]{2,14}$`)

// HacksGenerator finds domain hacks: tokens whose suffix matches a TLD.
// e.g. "coffee" ends with "ee" → "coff.ee"; "studio" ends with "io" → "stud.io"
type HacksGenerator struct{}

func NewHacksGenerator() *HacksGenerator { return &HacksGenerator{} }

func (g *HacksGenerator) Name() string { return "hacks" }

func (g *HacksGenerator) Generate(tokens []string, tlds []string) []Candidate {
	// build suffix index sorted by length descending (longer TLDs match first)
	type entry struct {
		tld string
		len int
	}
	index := make([]entry, 0, len(tlds))
	for _, tld := range tlds {
		index = append(index, entry{tld, len(tld)})
	}
	sort.Slice(index, func(i, j int) bool { return index[i].len > index[j].len })

	candidates := tokens

	// short inflections to try appending
	inflections := []string{"", "s", "ed", "er", "ing"}

	seen := make(map[string]struct{})
	var out []Candidate

	for _, base := range candidates {
		for _, infl := range inflections {
			s := base + infl
			for _, e := range index {
				tld := e.tld
				if strings.HasSuffix(s, tld) {
					sld := s[:len(s)-len(tld)]
					if len(sld) < 2 {
						continue
					}
					if !sldRe.MatchString(sld) {
						continue
					}
					key := sld + "." + tld
					if _, dup := seen[key]; dup {
						continue
					}
					seen[key] = struct{}{}
					out = append(out, Candidate{SLD: sld, TLD: tld})
				}
			}
		}
	}
	return out
}
