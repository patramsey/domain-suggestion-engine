package llm

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
)

// Funnel counts how raw LLM output is whittled down to candidates. Every
// returned pair ends in exactly one of BadFormat, Truncated, UnknownTLD,
// DupInVariant, DupAcrossVariants or Kept, so those six sum to Returned.
//
// EvalGenerate (which produces this Funnel) does not run production
// Generate's retry-once-if->50%-invalid step, so eval yield can be lower
// than production yield for the same prompt and inputs.
type Funnel struct {
	Requested int `json:"requested"` // names asked for across all variant calls
	Returned  int `json:"returned"`  // pairs parsed from successful responses

	BadFormat         int `json:"bad_format"`          // SLD fails the 3–14 lowercase letters rule
	Truncated         int `json:"truncated"`           // SLD looks like a mid-word fragment
	UnknownTLD        int `json:"unknown_tld"`         // TLD not in the allowed set
	DupInVariant      int `json:"dup_in_variant"`      // SLD repeated within one variant's response
	DupAcrossVariants int `json:"dup_across_variants"` // SLD already kept from an earlier variant
	Kept              int `json:"kept"`

	FailedCalls   int `json:"failed_calls"`    // variant calls that errored after retries
	PartialParses int `json:"partial_parses"`  // responses recovered from malformed/truncated JSON
	MaxTokenStops int `json:"max_token_stops"` // responses that stopped at the output token limit
}

// Add returns the field-wise sum of two funnels.
func (a Funnel) Add(b Funnel) Funnel {
	return Funnel{
		Requested:         a.Requested + b.Requested,
		Returned:          a.Returned + b.Returned,
		BadFormat:         a.BadFormat + b.BadFormat,
		Truncated:         a.Truncated + b.Truncated,
		UnknownTLD:        a.UnknownTLD + b.UnknownTLD,
		DupInVariant:      a.DupInVariant + b.DupInVariant,
		DupAcrossVariants: a.DupAcrossVariants + b.DupAcrossVariants,
		Kept:              a.Kept + b.Kept,
		FailedCalls:       a.FailedCalls + b.FailedCalls,
		PartialParses:     a.PartialParses + b.PartialParses,
		MaxTokenStops:     a.MaxTokenStops + b.MaxTokenStops,
	}
}

type rejectReason int

const (
	reasonNone rejectReason = iota
	reasonBadFormat
	reasonTruncated
	reasonUnknownTLD
)

// validatePair applies the per-pair checks used by parseAndValidate.
func validatePair(p rawPair, tldSet map[string]struct{}) rejectReason {
	if !sldRe.MatchString(p.SLD) {
		return reasonBadFormat
	}
	if looksLikeTruncation(p.SLD) {
		return reasonTruncated
	}
	if _, ok := tldSet[p.TLD]; !ok {
		return reasonUnknownTLD
	}
	return reasonNone
}

// rankedCandidatesWithFunnel is rankedCandidates plus a count of why each
// returned pair was kept or dropped.
func rankedCandidatesWithFunnel(perVariant [][]rawPair, tldSet map[string]struct{}) ([]algorithmic.Candidate, Funnel) {
	var f Funnel
	seenSLD := make(map[string]struct{})
	var out []algorithmic.Candidate
	for _, pairs := range perVariant {
		f.Returned += len(pairs)
		valid, invalid := parseAndValidate(pairs, tldSet)
		for _, p := range invalid {
			switch validatePair(p, tldSet) {
			case reasonBadFormat:
				f.BadFormat++
			case reasonTruncated:
				f.Truncated++
			case reasonUnknownTLD:
				f.UnknownTLD++
			}
		}
		f.DupInVariant += len(pairs) - len(valid) - len(invalid)
		n := len(valid)
		for j, p := range valid {
			if _, dup := seenSLD[p.SLD]; dup {
				f.DupAcrossVariants++
				continue
			}
			seenSLD[p.SLD] = struct{}{}
			rank := 1.0 - float64(j)/float64(n) // [1/n, 1.0], never 0
			out = append(out, algorithmic.Candidate{
				SLD:     p.SLD,
				TLD:     p.TLD,
				Source:  "llm",
				LLMRank: rank,
			})
		}
	}
	f.Kept = len(out)
	return out, f
}

// overRequest mirrors BuildRequest's over-request factor (3×), so the eval can
// report how many names were asked for. TestOverRequestMatchesBuildRequest
// guards against the two drifting apart.
func overRequest(count int) int {
	return count * 3
}

// PromptFingerprint returns a short hash identifying the full prompt a
// request is built from: the system prompt, the user-message template and the
// per-variant instructions (or the overrides that replace them). Eval
// snapshots record it so results can be tied to the exact prompt.
func PromptFingerprint(system string, variantOverrides []string) string {
	h := sha256.New()
	write := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	write(system)
	_, user := BuildRequest("<input>", []string{"<token>"}, []string{"<tld>"}, 1, nil, nil)
	write(user)
	if len(variantOverrides) == len(llmVariants) {
		write("overrides")
		for _, o := range variantOverrides {
			write(o)
		}
	} else {
		for _, v := range llmVariants {
			write(variantInstruction(v))
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
