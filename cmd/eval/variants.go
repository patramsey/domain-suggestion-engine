package main

import (
	"fmt"
	"strings"

	"github.com/patlivet/domain-suggestion-engine/internal/llm"
)

// promptVariant is one prompt configuration under test.
type promptVariant struct {
	name             string
	system           string
	temperature      float64
	variantOverrides []string // optional: replaces the 3 creative-variant instructions (must be len 3)
}

// --- 3.5 migration candidates (spec: 2026-09-19-flash-lite-35-migration) ---
// Every instruction here is general: no query-specific text and no word lists.

// tldAdherence targets invented TLDs (3.5 used TLDs outside the allowed list
// 2.5x as often as 3.1).
const tldAdherence = `

TLD rule: copy every TLD exactly from the "Allowed TLDs" list in the request. If the TLD you want is not on that list, choose a different pairing — never output a TLD that is not listed.`

// commonWordGuidance targets likely-taken names (54% of 3.5's top 10 were
// very common words vs 38% for 3.1).
const commonWordGuidance = `

Availability: assume every everyday English word — the kind a newspaper uses daily — is already registered on every TLD. Never return an SLD that is a single everyday word on its own. Prefer compounds of two words, blends, coined words, rare or foreign words, and domain hacks, while keeping every name clean, pronounceable and specific to this concept.`

// separatedBriefs gives the three parallel batches non-overlapping jobs and
// tells each what the others cover (3.5 repeated names across batches 2.6x
// as often as 3.1).
var separatedBriefs = []string{
	"\n\nThis request is one of three batches generated in parallel for the same concept. Each batch owns one kind of name; stay strictly inside yours so the batches do not repeat each other.\nYour batch: single evocative or classical words (Latin, Greek or another language) rooted in THIS concept's specific emotional territory — words that would feel surprising or even slightly wrong for most other businesses. The other two batches cover (1) domain hacks and TLD wordplay and (2) blended or coined words, so produce neither here.",
	"\n\nThis request is one of three batches generated in parallel for the same concept. Each batch owns one kind of name; stay strictly inside yours so the batches do not repeat each other.\nYour batch: domain hacks and TLD wordplay — SLD+TLD pairs that read as one word or phrase, or TLDs whose meaning doubles the concept. The TLD must carry part of the meaning. The other two batches cover (1) single evocative or classical words and (2) blended or coined words, so produce neither here.",
	"\n\nThis request is one of three batches generated in parallel for the same concept. Each batch owns one kind of name; stay strictly inside yours so the batches do not repeat each other.\nYour batch: blended and coined words — portmanteaus of two concept-relevant words, or new words built from relevant roots. Every SLD must be a new word, not one found in a dictionary. The other two batches cover (1) single evocative or classical words and (2) domain hacks and TLD wordplay, so produce neither here.",
}

// craftedBriefR3 replaces separatedBriefs[2] in r3-briefs. r1's version
// ("every SLD must be a new word") produced formulaic coinages that failed
// the blind rating; this one allows real-word compounds and asks for names
// that sound made for the concept.
const craftedBriefR3 = "\n\nThis request is one of three batches generated in parallel for the same concept. Each batch owns one kind of name; stay strictly inside yours so the batches do not repeat each other.\nYour batch: compound and blended names — two concept-relevant words joined or blended so the result still reads and sounds natural, or a familiar word given a fresh twist. Avoid formulaic tech-startup coinages made by gluing a stock prefix or suffix onto a word; every name should sound as if it were made for THIS concept by a person, not generated. The other two batches cover (1) single evocative or classical words and (2) domain hacks and TLD wordplay, so produce neither here."

// r3Briefs is separatedBriefs with the crafted brief replaced.
var r3Briefs = []string{separatedBriefs[0], separatedBriefs[1], craftedBriefR3}

// allVariants lists every prompt variant the eval can run; -variant selects
// which run (default "current"). Candidates for the 3.5 migration are added
// below "current" — see docs/superpowers/specs/2026-09-19-flash-lite-35-migration-design.md.
var allVariants = []promptVariant{
	{name: "current", system: llm.SystemPrompt, temperature: 1.0},
	// Round 1: separated briefs + TLD adherence.
	{name: "r1-briefs", system: llm.SystemPrompt + tldAdherence, temperature: 1.0, variantOverrides: separatedBriefs},
	// Round 2: round 1 + common-word guidance.
	{name: "r2-uncommon", system: llm.SystemPrompt + tldAdherence + commonWordGuidance, temperature: 1.0, variantOverrides: separatedBriefs},
	// Round 3: r1 with the crafted brief rewritten after the blind-rating failure.
	{name: "r3-briefs", system: llm.SystemPrompt + tldAdherence, temperature: 1.0, variantOverrides: r3Briefs},
}

// variants is the selected subset for this invocation (set in main).
var variants = allVariants

// selectVariants returns the variants named in filter (comma-separated), in
// definition order. "all" selects every variant.
func selectVariants(all []promptVariant, filter string) ([]promptVariant, error) {
	if strings.TrimSpace(filter) == "" {
		return nil, fmt.Errorf("-variant: empty")
	}
	if filter == "all" {
		return all, nil
	}
	want := map[string]bool{}
	for _, n := range strings.Split(filter, ",") {
		want[strings.TrimSpace(n)] = true
	}
	var out []promptVariant
	for _, v := range all {
		if want[v.name] {
			out = append(out, v)
			delete(want, v.name)
		}
	}
	for n := range want {
		return nil, fmt.Errorf("-variant: unknown variant %q", n)
	}
	return out, nil
}
