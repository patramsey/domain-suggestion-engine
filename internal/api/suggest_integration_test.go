//go:build integration

package api

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/cache"
	"github.com/patlivet/domain-suggestion-engine/internal/llm"
	"github.com/patlivet/domain-suggestion-engine/internal/parser"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
	"github.com/patlivet/domain-suggestion-engine/internal/tlds"
)

// apiSem limits concurrent Gemini API calls to avoid rate limiting (~3 req/s on free tier).
var apiSem = make(chan struct{}, 3)

// integrationSuggest runs the full suggest pipeline against the real LLM.
func integrationSuggest(t *testing.T, input string, count int) []Suggestion {
	t.Helper()
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}

	allGen := algorithmic.DefaultGenerators(nil, nil)
	engine := algorithmic.NewEngine(allGen, []string{"hacks"})
	llmClient := llm.NewClient(key, "gemini-3.5-flash-lite")
	c, _ := cache.New(10, cache.DefaultTTL)
	icannSet := tlds.DefaultRegistry.ICANNSet()

	resolvedTLDs, _ := tlds.Resolve(tlds.Filter{})
	tldSet := make(map[string]struct{}, len(resolvedTLDs))
	for _, tld := range resolvedTLDs {
		tldSet[tld] = struct{}{}
	}

	tokens := parser.Parse(input, icannSet)

	apiSem <- struct{}{}
	defer func() { <-apiSem }()

	algoCands := engine.Run(tokens, resolvedTLDs)

	// Retry once if LLM returns very few candidates — handles transient API variance.
	var llmCands []algorithmic.Candidate
	for attempt := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		llmCands, _, _ = llmClient.Generate(ctx, input, tokens, resolvedTLDs, tldSet, count, nil, nil)
		cancel()
		if len(llmCands) >= 3 || attempt == 1 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	merged := merge(llmCands, algoCands)
	scored := scorer.Rank(merged, tokens)
	scored = diversityCap(scored, count, len(resolvedTLDs))
	final := tierBalance(scored, count, 0.60)
	final = sldDedup(final)
	sort.Slice(final, func(i, j int) bool { return final[i].Score > final[j].Score })
	final = reserveCommonWords(final, scored, count, 2, func(c algorithmic.Candidate) float64 {
		return scorer.ScoreWithoutCommonWordPenalty(c, tokens)
	})

	_ = c
	suggestions := make([]Suggestion, len(final))
	for i, sc := range final {
		suggestions[i] = Suggestion{
			Name:   sc.Name(),
			SLD:    sc.SLD,
			TLD:    sc.TLD,
			Score:  sc.Score,
			Source: sc.Source,
		}
	}
	return suggestions
}

// assertSuggestionQuality validates structural quality properties of a result set.
func assertSuggestionQuality(t *testing.T, label string, suggestions []Suggestion, minCount int) {
	t.Helper()

	if len(suggestions) < minCount {
		t.Errorf("[%s] want >= %d suggestions, got %d", label, minCount, len(suggestions))
	}

	// Only check SLDs that are unambiguously bad — generic product/modifier nouns
	// that should never anchor a domain. Short function words (the, and, for)
	// are excluded here because they can form valid domain hacks (the.fm, for.est).
	badSLDs := map[string]struct{}{
		"tool": {}, "platform": {}, "service": {}, "powered": {}, "based": {},
		"small": {}, "large": {}, "document": {}, "review": {},
	}

	seenSLD := map[string]int{}
	prevScore := 2.0

	for i, s := range suggestions {
		// scores in range
		if s.Score < 0 || s.Score > 1 {
			t.Errorf("[%s] suggestion %d %q score %.3f out of [0,1]", label, i, s.Name, s.Score)
		}
		// sorted descending within each block of 10 (common-word slots are
		// capped per block, so a new block may start higher)
		if i%resultBlock == 0 {
			prevScore = 2.0
		}
		if s.Score > prevScore+0.001 {
			t.Errorf("[%s] suggestions not sorted: [%d]=%.3f > [%d]=%.3f", label, i, s.Score, i-1, prevScore)
		}
		prevScore = s.Score

		// no duplicate SLDs
		seenSLD[s.SLD]++
		if seenSLD[s.SLD] > 1 {
			t.Errorf("[%s] duplicate SLD %q at position %d", label, s.SLD, i)
		}

		// SLD is lowercase letters only
		for _, r := range s.SLD {
			if r < 'a' || r > 'z' {
				t.Errorf("[%s] SLD %q contains non-lowercase-letter char %q", label, s.SLD, r)
				break
			}
		}

		// SLD length in range (algorithmic hacks allow 2-char SLDs like te.am)
		minSLDLen := 3
		if s.Source == "algorithmic" {
			minSLDLen = 2
		}
		if len(s.SLD) < minSLDLen || len(s.SLD) > 14 {
			t.Errorf("[%s] SLD %q length %d out of [%d,14]", label, s.SLD, len(s.SLD), minSLDLen)
		}

		// no generic product/modifier nouns as SLDs
		if _, bad := badSLDs[s.SLD]; bad {
			t.Errorf("[%s] bad SLD %q used in %q", label, s.SLD, s.Name)
		}

		// TLD is non-empty and lowercase
		if s.TLD == "" {
			t.Errorf("[%s] empty TLD in suggestion %q", label, s.Name)
		}
		if strings.ToLower(s.TLD) != s.TLD {
			t.Errorf("[%s] TLD %q is not lowercase", label, s.TLD)
		}

		// name = sld.tld
		if s.Name != s.SLD+"."+s.TLD {
			t.Errorf("[%s] name %q does not match sld.tld", label, s.Name)
		}

		// source is one of known values
		if s.Source != "llm" && s.Source != "algorithmic" {
			t.Errorf("[%s] unknown source %q in %q", label, s.Source, s.Name)
		}
	}
}

// --- test cases ---

var integrationCases = []struct {
	label    string
	input    string
	minCount int
}{
	// single keywords — food & drink
	{"single: pizza", "pizza", 4},
	{"single: coffee", "coffee", 4},
	{"single: sushi", "sushi", 3},
	{"single: vegan", "vegan", 4},
	{"single: bakery", "bakery", 4},
	{"single: whiskey", "whiskey", 4},

	// single keywords — lifestyle & fitness
	{"single: yoga", "yoga", 4},
	{"single: surf", "surf", 4},
	{"single: hiking", "hiking", 4},
	{"single: cycling", "cycling", 4},
	{"single: wellness", "wellness", 3},

	// single keywords — creative & media
	{"single: photography", "photography", 4},
	{"single: podcast", "podcast", 4},
	{"single: animation", "animation", 4},
	{"single: tattoo", "tattoo", 4},
	{"single: music", "music", 4},

	// single keywords — tech
	{"single: crypto", "crypto", 4},
	{"single: drone", "drone", 4},
	{"single: robotics", "robotics", 4},
	{"single: cybersecurity", "cybersecurity", 4},

	// single keywords — evocative coinables (LLM anchors on these as SLDs, fewer diverse results)
	{"single: bloom", "bloom", 2},
	{"single: forge", "forge", 2},
	{"single: pulse", "pulse", 2},
	{"single: echo", "echo", 3},
	{"single: spark", "spark", 3},
	{"single: craft", "craft", 2},
	{"single: glow", "glow", 3},
	{"single: flux", "flux", 3},

	// two-word phrases
	{"two words: photo studio", "photo studio", 4},
	{"two words: coffee shop", "coffee shop", 4},
	{"two words: law firm", "law firm", 4},
	{"two words: music festival", "music festival", 3},
	{"two words: pet care", "pet care", 4},
	{"two words: book club", "book club", 3},
	{"two words: wine bar", "wine bar", 4},
	{"two words: food truck", "food truck", 4},
	{"two words: art gallery", "art gallery", 4},
	{"two words: game studio", "game studio", 4},
	{"two words: urban farm", "urban farm", 4},
	{"two words: flower shop", "flower shop", 4},
	{"two words: fitness club", "fitness club", 4},
	{"two words: record label", "record label", 4},
	{"two words: travel blog", "travel blog", 4},
	{"two words: podcast network", "podcast network", 4},
	{"two words: coding bootcamp", "coding bootcamp", 4},
	{"two words: hair salon", "hair salon", 4},
	{"two words: dog walker", "dog walking", 4},
	{"two words: web design", "web design", 4},

	// full descriptions — SaaS / apps
	{"desc: AI legal tool", "AI-powered legal document review tool for small law firms", 4},
	{"desc: meditation app", "meditation and mindfulness app for anxiety", 4},
	{"desc: team project management", "team project management", 4},
	{"desc: kids coding platform", "learn to code platform for kids", 4},
	{"desc: fintech budget app", "personal budget tracking and savings app", 4},
	{"desc: AI resume builder", "AI resume builder and job application tracker", 4},
	{"desc: remote work tool", "async communication tool for remote teams", 4},
	{"desc: invoice tracker", "freelance invoice and payment tracker for contractors", 4},
	{"desc: mental health journaling", "mental health journaling and mood tracking app", 4},
	{"desc: language learning", "language learning app for travelers", 4},
	{"desc: meal prep app", "meal prep and nutrition planning app", 3},
	{"desc: ev charging finder", "electric vehicle charging station locator app", 4},

	// full descriptions — physical businesses
	{"desc: brooklyn bakery", "brooklyn artisan bakery sourdough bread", 3},
	{"desc: coffee subscription", "small batch specialty coffee roastery and subscription", 4},
	{"desc: craft beer box", "craft beer subscription box monthly delivery", 4},
	{"desc: flower delivery", "same-day local flower delivery and arrangement studio", 4},
	{"desc: tattoo studio", "custom tattoo and piercing studio booking", 4},

	// full descriptions — marketplaces & communities
	{"desc: vintage clothing resale", "vintage clothing resale marketplace", 4},
	{"desc: ecommerce marketplace", "sustainable eco-friendly product marketplace", 4},
	{"desc: sustainable fashion", "sustainable fashion resale and rental marketplace", 4},
	{"desc: indie game community", "indie game developer community and showcase", 4},
	{"desc: wedding planning", "wedding planning and vendor coordination", 4},
	{"desc: farmers market app", "farmers market vendor and buyer management app", 4},

	// full descriptions — creative agencies & studios
	{"desc: creative agency", "creative branding and design studio", 4},
	{"desc: outdoor adventure community", "weekend hiking and outdoor adventure community", 4},
	{"desc: photography courses", "online course platform for professional photographers", 4},
	{"desc: architecture portfolio", "architecture and interior design portfolio platform", 4},

	// existing domain as input
	{"domain input: patspizza.com", "patspizza.com", 3},
	{"domain input: mybrand.io", "mybrand.io", 1},
	{"domain input: techstartup.io", "techstartup.io", 2},
	{"domain input: mystore.shop", "mystore.shop", 2},
	{"domain input: getfit.app", "getfit.app", 3},

	// abstract / evocative concepts
	{"abstract: flow state productivity", "flow state deep work productivity", 4},
	{"abstract: social audio", "social audio conversations", 4},
	{"abstract: second brain", "second brain knowledge management", 4},
	{"abstract: ambient focus", "ambient music generative focus", 4},
	{"abstract: slow living", "slow living intentional minimalist lifestyle", 4},
	{"abstract: dark mode tech", "dark mode aesthetic developer tools", 4},
	{"abstract: creator economy", "creator economy monetisation tools", 4},

	// input format variety
	{"format: all caps", "COFFEE SHOP", 4},
	{"format: hyphenated", "eco-friendly products", 4},
	{"format: mixed case", "PhotoStudio", 4},
	{"format: underscore", "photo_studio_nyc", 4},
	{"format: sentence", "I make handmade soy candles for gifts", 4},
	{"format: question", "where can I find good sushi near me", 3},

	// edge cases
	{"edge: all stopwords", "the and a for", 1},
	{"edge: numbers only", "12345", 1},
	{"edge: very short", "ai", 3},
	{"edge: single stopword", "the", 1},
	{"edge: long description", "comprehensive enterprise resource planning and inventory management for mid-market manufacturing companies streamline supply chain operations", 4},
}

func TestIntegrationSuggestionQuality(t *testing.T) {
	for _, tc := range integrationCases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			suggestions := integrationSuggest(t, tc.input, 8)
			assertSuggestionQuality(t, tc.label, suggestions, tc.minCount)
		})
	}
}

// TestIntegrationUnavailableDomains verifies that confirmed-taken domains are
// excluded from results and used as quality calibration for new suggestions.
//
// Unavailability verified with a registrar availability check on 2026-07-03.
func TestIntegrationUnavailableDomains(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}

	const input = "craft beer subscription box monthly delivery"

	// Confirmed taken via a registrar availability check on 2026-07-03.
	unavailable := []string{"vessel.beer", "hopology.beer", "craft.beer", "brew.beer"}

	llmClient := llm.NewClient(key, "gemini-3.5-flash-lite")
	icannSet := tlds.DefaultRegistry.ICANNSet()
	resolvedTLDs, _ := tlds.Resolve(tlds.Filter{})
	tldSet := make(map[string]struct{}, len(resolvedTLDs))
	for _, tld := range resolvedTLDs {
		tldSet[tld] = struct{}{}
	}
	tokens := parser.Parse(input, icannSet)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cands, _, err := llmClient.Generate(ctx, input, tokens, resolvedTLDs, tldSet, 10, unavailable, nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if len(cands) < 3 {
		t.Fatalf("too few candidates returned: %d", len(cands))
	}

	unavailableSet := make(map[string]struct{}, len(unavailable))
	for _, d := range unavailable {
		unavailableSet[d] = struct{}{}
	}

	for _, c := range cands {
		name := c.SLD + "." + c.TLD
		if _, taken := unavailableSet[name]; taken {
			t.Errorf("unavailable domain %q appeared in suggestions", name)
		}
	}
}
