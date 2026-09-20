package llm

import (
	"fmt"
	"math"
	"strings"
)

// SystemPrompt is the production system prompt. Exported for eval comparisons.
const SystemPrompt = `You are a creative brand and domain name generator. Output ONLY a JSON array of {"sld","tld"} objects — no prose, no fences. Sort best-first: most creative and memorable first.

Before generating, silently identify: (1) the core emotional territory and (2) the primary business concept. Use those to anchor SLD selection. For each SLD you consider, apply this test: would this word appear in suggestions for a yoga studio, a pizza restaurant, AND a software startup equally? If yes, it is too generic — discard it and find something specific to this concept.

SLD constraints:
- 3–14 lowercase letters only (no hyphens, numbers, spaces)
- Must be a complete, pronounceable word or natural-sounding coinage — never a truncated fragment
- Every SLD must be anchored to something specific about this concept — a feeling, material, craft, or structural wordplay. Words expressing generic excellence or aspiration without grounding in the concept are not acceptable.
- No two suggestions may share the same SLD

TLD constraints:
- Choose ONLY from the allowed list — never invent one
- The TLD must earn its place: add meaning, complete a phrase, or exploit a figurative meaning
- Use each TLD at most twice across all suggestions

Creative strategies — use a mix:
1. Domain hack: SLD+TLD reads as a word or phrase ("stud.io", "stre.am")
2. Evocative metaphor: capture the feeling or outcome, not the literal feature
3. Portmanteau: blend two relevant concepts into a coined word
4. Borrowed/classical word: Latin, Greek, or foreign that fits the tone
5. Crisp single word: short, memorable, real dictionary word that fits the theme
6. TLD double-entendre: exploit a word TLD's figurative meaning

Hard rules:
- Do NOT use the exact input keywords as SLDs
- Location context may flavor at most 1–2 SLDs — prefer associative references, never literal place names
- Avoid literal feature/product nouns as SLDs
- Coined words must be complete and pronounceable — never cut a word mid-syllable

Few-shot examples (format and range only — do not reuse these specific SLDs):
[{"sld":"stud","tld":"io"},{"sld":"cedar","tld":"co"},{"sld":"dry","tld":"run"},{"sld":"lexis","tld":"ai"},{"sld":"indie","tld":"software"},{"sld":"get","tld":"domains"},{"sld":"relay","tld":"network"},{"sld":"forge","tld":"build"}]`

// Variant selects a creative focus for a parallel LLM call.
type Variant int

const (
	VariantEvocative Variant = iota // metaphors, classical words, sensation/mood
	VariantWordplay                 // domain hacks, TLD structural cleverness
	VariantCrafted                  // compounds of two ordinary words grounded in the concept
)

// variantInstruction returns a focus instruction appended to the user message.
func variantInstruction(v Variant) string {
	switch v {
	case VariantEvocative:
		return "\n\nCreative focus for this batch: evocative metaphors and classical words specifically rooted in THIS concept's unique emotional territory — not broadly atmospheric words any brand could claim. The test: would a professional naming consultant see this word and immediately understand why it belongs to THIS concept, or would they say 'this could be anything'? If the latter, dig deeper. The word should feel surprising or even slightly wrong when applied to most other businesses."
	case VariantWordplay:
		return "\n\nCreative focus for this batch: domain hacks and TLD wordplay. Look for SLD+TLD pairs that read as a complete word or phrase, or TLDs whose figurative meaning doubles the brand concept. Structural cleverness over thematic fit."
	case VariantCrafted:
		// Compounds are rarely registered, and grounding each half in the
		// concept keeps them specific; see issue #4 in eval-results/README.md.
		return "\n\nCreative focus for this batch: compound names — two short, ordinary English words joined into one name. One word names something THIS concept makes, does or works with; the other names the feeling, image or quality it should evoke. Both words must be instantly recognisable, and the joined name must read naturally aloud as one word. No invented prefixes or suffixes, and no words so general they could attach to any business."
	}
	return ""
}

// VariantInstructions returns the production focus instruction for each
// parallel batch, in batch order. For prompt experiments in cmd/eval.
func VariantInstructions() []string {
	out := make([]string, len(llmVariants))
	for i, v := range llmVariants {
		out[i] = variantInstruction(v)
	}
	return out
}

// overRequestFactor is how many names to ask for per name returned. The
// surplus absorbs names lost to SLD dedup, TLD validation and cross-batch
// duplicates. Measured yield is ~68-71% of requested names, so 2x leaves
// room while keeping output tokens — the main driver of latency — down.
const overRequestFactor = 2

// BuildRequest constructs the user message for a suggestion request.
// rawInput is the original user input (for fallback path).
// tokens is the parsed token list (empty on fallback path).
// count is the number of results the caller wants (we request 1.5× to absorb losses).
func BuildRequest(rawInput string, tokens []string, tlds []string, count int, unavailable, inspireFrom []string) (system, user string) {
	system = SystemPrompt

	requestCount := int(math.Ceil(float64(count) * overRequestFactor))
	tldList := strings.Join(tlds, ", ")

	var msg string
	if len(tokens) == 0 {
		// fallback path: no tokens extracted, pass raw input
		msg = fmt.Sprintf(
			"Generate %d domain name suggestions for: %s\n\nAllowed TLDs:\n%s",
			requestCount, rawInput, tldList,
		)
	} else {
		// Lead with full context so the LLM reasons from the concept, then give
		// the parsed keywords as a secondary signal.
		if len(rawInput) > len(strings.Join(tokens, " "))+10 {
			msg = fmt.Sprintf(
				"Generate %d domain name suggestions for this concept: %s\nKey themes: %s",
				requestCount, rawInput, strings.Join(tokens, ", "),
			)
		} else {
			msg = fmt.Sprintf(
				"Generate %d domain name suggestions for: %s",
				requestCount, strings.Join(tokens, ", "),
			)
		}
		msg += fmt.Sprintf("\n\nAllowed TLDs:\n%s", tldList)
	}

	if len(tlds) > 1 {
		minTLDs := min(len(tlds), int(math.Ceil(float64(requestCount)/3)))
		if len(tlds) >= int(math.Ceil(float64(requestCount)/2)) {
			msg += fmt.Sprintf("\n\nUse at least %d distinct TLDs across all suggestions — no TLD more than twice.", minTLDs)
		} else {
			msg += fmt.Sprintf("\n\nUse at least %d distinct TLDs across all suggestions.", minTLDs)
		}
	}

	if len(inspireFrom) > 0 {
		msg += fmt.Sprintf("\n\nThe user liked these domains — generate suggestions in a similar creative direction (same vibe, tone, and style, but different names):\n%s", strings.Join(inspireFrom, ", "))
	}

	if len(unavailable) > 0 {
		if len(inspireFrom) > 0 {
			// inspire_from already sets creative direction; unavailable is exclusion only
			msg += fmt.Sprintf("\n\nDo not suggest these domains (already taken):\n%s", strings.Join(unavailable, ", "))
		} else {
			// No inspire_from: use taken domains as a quality calibration signal
			msg += fmt.Sprintf("\n\nThese domains are already registered — do not suggest them, but study them: they represent the quality bar that real people found compelling enough to claim. Use them to understand the creative territory and caliber, then find alternatives of similar strength that are not on this list:\n%s", strings.Join(unavailable, ", "))
		}
	}

	user = msg
	return
}

// BuildRetryRequest builds a retry user message that calls out hallucinated TLDs
// and preserves unavailable and inspire_from constraints.
func BuildRetryRequest(rawInput string, tokens []string, tlds []string, count int, badTLDs, unavailable, inspireFrom []string) (system, user string) {
	system = SystemPrompt
	requestCount := int(math.Ceil(float64(count) * overRequestFactor))
	tldList := strings.Join(tlds, ", ")

	var context string
	if len(tokens) > 0 {
		context = strings.Join(tokens, ", ")
	} else {
		context = rawInput
	}

	var intro string
	if len(badTLDs) > 0 {
		intro = fmt.Sprintf("Your previous response included TLDs not in the allowed list: %s.\nYou must choose only from: %s\n\n", strings.Join(badTLDs, ", "), tldList)
	} else {
		intro = fmt.Sprintf("Your previous response had invalid format or disallowed names.\nYou must choose only from: %s\n\n", tldList)
	}

	msg := intro + fmt.Sprintf("Regenerate %d suggestions for: %s\n\nReturn a JSON array of objects: [{\"sld\": \"...\", \"tld\": \"...\"}, ...]", requestCount, context)

	if len(inspireFrom) > 0 {
		msg += fmt.Sprintf("\n\nThe user liked these domains — generate suggestions in a similar creative direction:\n%s", strings.Join(inspireFrom, ", "))
	}
	if len(unavailable) > 0 {
		msg += fmt.Sprintf("\n\nDo not suggest these domains (already registered):\n%s", strings.Join(unavailable, ", "))
	}

	user = msg
	return
}
