package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/algorithmic"
	"github.com/patlivet/domain-suggestion-engine/internal/cache"
	"github.com/patlivet/domain-suggestion-engine/internal/dnscheck"
	"github.com/patlivet/domain-suggestion-engine/internal/llm"
	"github.com/patlivet/domain-suggestion-engine/internal/parser"
	"github.com/patlivet/domain-suggestion-engine/internal/scorer"
	"github.com/patlivet/domain-suggestion-engine/internal/tlds"
)

const maxInputLen = 500
const defaultCount = 20
const maxCount = 100
const maxUnavailable = 40
const maxInspireFrom = 5

// Config holds all runtime configuration for the handler.
type Config struct {
	GeminiAPIKey      string
	GeminiModel       string
	CacheSize         int
	LLMShare          float64
	CommonWordSlots   int // results per 10 reserved for very common single words; 0 disables
	AlgoEnabled       bool
	ActiveGenerators  []string
	AllGenerators     []string
	LLMVariants       []string // creative variants to run, e.g. ["evocative", "wordplay", "crafted"] or ["1"]
	CheckAvailability bool          // check DNS availability for returned names; default false
	DNSResolverAddr   string
	DNSCacheSize      int           // max entries in DNS LRU cache; 0 disables, default 5000
	DNSCacheTTL       time.Duration // TTL for cached DNS availability outcomes; default 1h
	Version           string
	BuiltAt           string
}

// Handler handles all API routes.
type Handler struct {
	cfg          Config
	engine       *algorithmic.Engine
	llm          *llm.Client
	cache        *cache.Cache
	icannSet     map[string]struct{} // cached once at init, safe for concurrent read
	dnsLookup    dnscheck.Lookup
	variantNames []string
}

// NewHandler wires all components together from the given config.
func NewHandler(cfg Config) (*Handler, error) {
	allGen := algorithmic.DefaultGenerators(nil, nil)
	engine := algorithmic.NewEngine(allGen, cfg.ActiveGenerators)

	llmClient := llm.NewClient(cfg.GeminiAPIKey, cfg.GeminiModel)
	var vars []llm.Variant
	if len(cfg.LLMVariants) > 0 {
		var err error
		vars, err = llm.ParseVariants(strings.Join(cfg.LLMVariants, ","))
		if err != nil {
			return nil, fmt.Errorf("LLMVariants: %w", err)
		}
	} else {
		vars = []llm.Variant{llm.VariantEvocative, llm.VariantWordplay, llm.VariantCrafted}
	}
	llmClient.Variants = vars
	varNames := make([]string, len(vars))
	for i, v := range vars {
		varNames[i] = v.String()
	}

	if cfg.CommonWordSlots < 0 || cfg.CommonWordSlots > 10 {
		return nil, fmt.Errorf("CommonWordSlots %d: want 0–10", cfg.CommonWordSlots)
	}

	// CacheSize 0 disables the response cache (used by pipeline checks that
	// need every request to reach the model).
	var c *cache.Cache
	if cfg.CacheSize > 0 {
		var err error
		c, err = cache.New(cfg.CacheSize, cache.DefaultTTL)
		if err != nil {
			return nil, err
		}
	}

	resolverAddr := cfg.DNSResolverAddr
	if resolverAddr == "" {
		resolverAddr = "1.1.1.1:53"
	}

	var dnsLook dnscheck.Lookup
	if cfg.DNSCacheSize > 0 {
		ttl := cfg.DNSCacheTTL
		if ttl <= 0 {
			ttl = dnscheck.DefaultCacheTTL
		}
		dnsCache, err := dnscheck.NewCache(cfg.DNSCacheSize, ttl)
		if err != nil {
			return nil, fmt.Errorf("DNSCache: %w", err)
		}
		dnsLook = dnscheck.CachedResolver(resolverAddr, dnsCache)
	} else {
		dnsLook = dnscheck.Resolver(resolverAddr)
	}

	return &Handler{
		cfg:          cfg,
		engine:       engine,
		llm:          llmClient,
		cache:        c,
		icannSet:     tlds.DefaultRegistry.ICANNSet(),
		dnsLookup:    dnsLook,
		variantNames: varNames,
	}, nil
}

// cacheGet and cacheSet are no-ops when the cache is disabled (CacheSize 0).
func (h *Handler) cacheGet(key string) ([]byte, bool) {
	if h.cache == nil {
		return nil, false
	}
	return h.cache.Get(key)
}

func (h *Handler) cacheSet(key string, value []byte) {
	if h.cache != nil {
		h.cache.Set(key, value)
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && (r.URL.Path == "/suggest" || r.URL.Path == "/suggest/stream"):
		h.handleSuggest(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/tlds/categories":
		h.handleCategories(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		h.handleHealth(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/config":
		h.handleConfig(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleSuggest implements POST /suggest.
func (h *Handler) handleSuggest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// 1. Decode + validate
	var req SuggestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, CodeMissingInput, "invalid request body")
		return
	}
	if req.Input == "" {
		WriteError(w, http.StatusBadRequest, CodeMissingInput, "input is required")
		return
	}
	if len(req.Input) > maxInputLen {
		WriteError(w, http.StatusUnprocessableEntity, CodeInputTooLong, "input must be 500 characters or fewer")
		return
	}
	if req.TLDFilter != nil && req.TLDFilter.Category != "" && len(req.TLDFilter.List) > 0 {
		WriteError(w, http.StatusBadRequest, CodeAmbiguousTLDFilter, "tld_filter.category and tld_filter.list are mutually exclusive")
		return
	}
	if req.Count <= 0 {
		req.Count = defaultCount
	}
	if req.Count > maxCount {
		req.Count = maxCount
	}
	if len(req.UnavailableDomains) > maxUnavailable {
		WriteError(w, http.StatusUnprocessableEntity, CodeTooManyUnavailable,
			fmt.Sprintf("unavailable_domains must contain %d or fewer entries", maxUnavailable))
		return
	}
	if len(req.InspireFrom) > maxInspireFrom {
		WriteError(w, http.StatusUnprocessableEntity, CodeTooManyInspireFrom,
			fmt.Sprintf("inspire_from must contain %d or fewer entries", maxInspireFrom))
		return
	}
	debug := r.URL.Query().Get("debug") == "true"

	// 2. Resolve TLD filter
	var f tlds.Filter
	if req.TLDFilter != nil {
		f.Category = req.TLDFilter.Category
		f.List = req.TLDFilter.List
	}
	resolvedTLDs, err := tlds.Resolve(f)
	if err != nil {
		if ute, ok := err.(*tlds.UnknownTLDError); ok {
			WriteError(w, http.StatusUnprocessableEntity, CodeUnknownTLD,
				"one or more TLDs not recognized", ute.TLDs...)
			return
		}
		WriteError(w, http.StatusBadRequest, CodeUnknownCategory, err.Error())
		return
	}

	// 3. Normalize unavailable/inspire lists and check cache
	unavailable := normalizeUnavailable(req.UnavailableDomains)
	inspireFrom := normalizeUnavailable(req.InspireFrom) // same normalization: lowercase + dedupe
	checkAvail := req.CheckAvailability || h.cfg.CheckAvailability
	isStream := r.URL.Path == "/suggest/stream" || r.Header.Get("Accept") == "text/event-stream"

	activeVariants := h.llm.Variants
	if len(activeVariants) == 0 {
		activeVariants = []llm.Variant{llm.VariantEvocative, llm.VariantWordplay, llm.VariantCrafted}
	}
	variantNames := h.variantNames
	if len(req.Variants) > 0 {
		var err error
		activeVariants, err = llm.ParseVariants(strings.Join(req.Variants, ","))
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_variants", err.Error())
			return
		}
		variantNames = make([]string, len(activeVariants))
		for i, v := range activeVariants {
			variantNames[i] = v.String()
		}
	}

	cacheKey := cache.Key(req.Input, req.Count, debug, checkAvail, resolvedTLDs, unavailable, inspireFrom, variantNames)
	if cached, ok := h.cacheGet(cacheKey); ok {
		if isStream {
			var resp SuggestResponse
			if err := json.Unmarshal(cached, &resp); err == nil {
				h.streamSuggest(w, r, resp.Suggestions, false, resp.Partial, resp.TLDsUsed, debug)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Age", "1")
		w.Write(cached)
		return
	}

	// 4. Parse tokens
	tokens := parser.Parse(req.Input, h.icannSet)
	partial := false
	algoEnabled := h.cfg.AlgoEnabled && len(h.engine.Active()) > 0

	if len(tokens) == 0 {
		// all-stopword input: skip algorithmic tier
		algoEnabled = false
		partial = true
	}

	// build TLD set for O(1) LLM validation
	tldSet := make(map[string]struct{}, len(resolvedTLDs))
	for _, tld := range resolvedTLDs {
		tldSet[tld] = struct{}{}
	}

	// 5-6. Launch tiers in parallel
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	type algoTierResult struct {
		candidates []algorithmic.Candidate
		dur        time.Duration
	}
	type llmTierResult struct {
		candidates []algorithmic.Candidate
		usage      llm.TokenUsage
		dur        time.Duration
		err        error
	}

	algoCh := make(chan algoTierResult, 1)
	llmCh := make(chan llmTierResult, 1)

	if algoEnabled {
		go func() {
			t0 := time.Now()
			cands := h.engine.Run(tokens, resolvedTLDs)
			algoCh <- algoTierResult{candidates: cands, dur: time.Since(t0)}
		}()
	} else {
		algoCh <- algoTierResult{}
	}

	go func() {
		t0 := time.Now()
		cands, usage, err := h.llm.Generate(ctx, req.Input, tokens, resolvedTLDs, tldSet, req.Count, unavailable, inspireFrom, activeVariants...)
		llmCh <- llmTierResult{candidates: cands, usage: usage, dur: time.Since(t0), err: err}
	}()

	// 7. Collect results
	algoResult := <-algoCh
	llmResult := <-llmCh

	if llmResult.err != nil {
		slog.Warn("llm tier failed", "err", llmResult.err, "duration_ms", llmResult.dur.Milliseconds())
		if len(algoResult.candidates) == 0 {
			WriteError(w, http.StatusInternalServerError, CodeBothTiersFailed,
				"both generation tiers failed")
			return
		}
		partial = true
	}

	// 8. Merge + deduplicate (prefer LLM on duplicate)
	merged := merge(llmResult.candidates, algoResult.candidates)
	afterMerge := len(merged)

	// 9. Score
	ranked := scorer.Rank(merged, tokens)

	// 10. TLD diversity cap applied to the full pool before tier-selection,
	//     so tierBalance can find non-homogeneous candidates.
	scored := diversityCap(ranked, req.Count, len(resolvedTLDs))
	afterDiversityCap := len(scored)

	// 11. Tier balance: 60/40 LLM/algo split
	effectiveLLMShare := h.cfg.LLMShare
	if !algoEnabled {
		effectiveLLMShare = 1.0
	}
	final := tierBalance(scored, req.Count, effectiveLLMShare)
	afterTierBalance := len(final)

	// 12. SLD dedup (no repeated SLD across suggestions)
	final = sldDedup(final)
	afterSLDDedup := len(final)
	sort.Slice(final, func(i, j int) bool { return final[i].Score > final[j].Score })

	// 13. Filter unavailable domains
	if len(unavailable) > 0 {
		ranked = filterUnavailable(ranked, unavailable)
		final = filterUnavailable(final, unavailable)
	}

	// 13b. Top up from the pre-cap pool if the cap left the result short.
	final = backfillToCount(final, ranked, req.Count)

	// 14. Reserve slots for very common single words: great names that are
	//     usually taken, ranked by quality instead of being demoted out.
	if h.cfg.CommonWordSlots > 0 {
		pool := scored
		if len(unavailable) > 0 {
			pool = filterUnavailable(scored, unavailable)
		}
		final = reserveCommonWords(final, pool, req.Count, h.cfg.CommonWordSlots, func(c algorithmic.Candidate) float64 {
			return scorer.ScoreWithoutCommonWordPenalty(c, tokens)
		})
	}

	// 15. Log pipeline metrics
	algoUnique := countSource("algorithmic", merged)
	estimatedCostUSD, costKnown := llm.EstimateCost(h.cfg.GeminiModel, llmResult.usage)
	slog.Info("suggest",
		"input_length", len(req.Input),
		"token_count", len(tokens),
		"tld_count", len(resolvedTLDs),
		"llm_raw", len(llmResult.candidates),
		"algo_raw", len(algoResult.candidates),
		"algo_unique", algoUnique,
		"after_merge", afterMerge,
		"after_diversity_cap", afterDiversityCap,
		"after_tier_balance", afterTierBalance,
		"after_sld_dedup", afterSLDDedup,
		"returned", len(final),
		"target", req.Count,
		"cache_hit", false,
		"llm_duration_ms", llmResult.dur.Milliseconds(),
		"algo_duration_ms", algoResult.dur.Milliseconds(),
		"total_duration_ms", time.Since(start).Milliseconds(),
		"partial", partial,
		"llm_prompt_tokens", llmResult.usage.PromptTokens,
		"llm_candidate_tokens", llmResult.usage.CandidateTokens,
		"llm_total_tokens", llmResult.usage.TotalTokens,
		"estimated_cost_usd", estimatedCostUSD,
		"cost_known", costKnown,
	)

	// 13. Build response
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

	// set generation source header
	src := generationSource(llmResult.err == nil, algoEnabled && len(algoResult.candidates) > 0)
	w.Header().Set("X-Generation-Source", src)

	if isStream {
		h.streamSuggest(w, r, suggestions, checkAvail, partial, resolvedTLDs, debug)
		return
	}

	if checkAvail && len(suggestions) > 0 {
		names := make([]string, len(suggestions))
		for i, s := range suggestions {
			names[i] = s.Name
		}
		outcomes := dnscheck.CheckAll(r.Context(), names, h.dnsLookup, 10)
		for i, s := range suggestions {
			if o, ok := outcomes[s.Name]; ok {
				switch o {
				case dnscheck.Free:
					avail := true
					suggestions[i].Available = &avail
				case dnscheck.Delegated:
					avail := false
					suggestions[i].Available = &avail
				}
			}
		}
	}
	resp := SuggestResponse{
		Suggestions: suggestions,
		Partial:     partial,
	}
	if debug {
		resp.TLDsUsed = resolvedTLDs
		resp.ActiveGenerators = h.engine.Active()
	}

	w.Header().Set("Content-Type", "application/json")

	encoded, _ := json.Marshal(resp)
	h.cacheSet(cacheKey, encoded)
	w.Write(encoded)
}

func (h *Handler) streamSuggest(w http.ResponseWriter, r *http.Request, suggestions []Suggestion, checkAvail bool, partial bool, resolvedTLDs []string, debug bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	resp := SuggestResponse{
		Suggestions: suggestions,
		Partial:     partial,
	}
	if debug {
		resp.TLDsUsed = resolvedTLDs
		resp.ActiveGenerators = h.engine.Active()
	}

	initData, _ := json.Marshal(resp)
	fmt.Fprintf(w, "event: suggestions\ndata: %s\n\n", initData)
	flusher.Flush()

	// As configured: live availability lookups only execute when DNS checking is enabled!
	if !checkAvail || len(suggestions) == 0 {
		fmt.Fprintf(w, "event: done\ndata: {}\n\n")
		flusher.Flush()
		return
	}

	names := make([]string, len(suggestions))
	for i, s := range suggestions {
		names[i] = s.Name
	}

	type availEvent struct {
		Name      string `json:"name"`
		Available bool   `json:"available"`
	}

	var mu sync.Mutex
	dnscheck.CheckAllStream(r.Context(), names, h.dnsLookup, 10, func(name string, o dnscheck.Outcome) {
		if o == dnscheck.Unknown {
			return
		}
		avail := (o == dnscheck.Free)
		data, err := json.Marshal(availEvent{Name: name, Available: avail})
		if err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, "event: availability\ndata: %s\n\n", data)
		flusher.Flush()
	})

	mu.Lock()
	fmt.Fprintf(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
	mu.Unlock()
}

// handleCategories implements GET /tlds/categories.
func (h *Handler) handleCategories(w http.ResponseWriter, _ *http.Request) {
	names := tlds.DefaultRegistry.CategoryNames()
	categories := make([]Category, 0, len(names))
	for _, name := range names {
		tldList, _ := tlds.DefaultRegistry.Category(name)
		categories = append(categories, Category{
			Name:  name,
			Count: len(tldList),
			TLDs:  tldList,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	json.NewEncoder(w).Encode(CategoriesResponse{Categories: categories})
}

// handleHealth implements GET /health.
func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	checks := map[string]CheckResult{}

	// tld_registry: default category resolves non-empty
	if tldList, err := tlds.Resolve(tlds.Filter{}); err != nil || len(tldList) == 0 {
		msg := "TLD registry failed"
		if err != nil {
			msg = err.Error()
		}
		checks["tld_registry"] = CheckResult{OK: false, Message: msg}
	} else {
		checks["tld_registry"] = CheckResult{OK: true}
	}

	// llm_key: API key present in config
	if h.cfg.GeminiAPIKey == "" {
		checks["llm_key"] = CheckResult{OK: false, Message: "GEMINI_API_KEY not set"}
	} else {
		checks["llm_key"] = CheckResult{OK: true}
	}

	// cache: always ok if we got here (initialized in NewHandler)
	checks["cache"] = CheckResult{OK: true}

	// generators: algo disabled is valid; if enabled, must have at least one active
	if !h.cfg.AlgoEnabled || len(h.engine.Active()) > 0 {
		checks["generators"] = CheckResult{OK: true}
	} else {
		checks["generators"] = CheckResult{OK: false, Message: "ALGO_ENABLED=true but no generators active"}
	}

	allOK := true
	for _, c := range checks {
		if !c.OK {
			allOK = false
			break
		}
	}

	status := "ok"
	httpStatus := http.StatusOK
	if !allOK {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(HealthResponse{Status: status, Checks: checks})
}

// handleConfig implements GET /config.
func (h *Handler) handleConfig(w http.ResponseWriter, _ *http.Request) {
	resp := ConfigResponse{
		LLM: LLMConfig{
			Model:     h.cfg.GeminiModel,
			TimeoutMs: 8000,
			LLMShare:  h.cfg.LLMShare,
			APIKeySet: h.cfg.GeminiAPIKey != "",
			Variants:  h.variantNames,
		},
		Algo: AlgoConfig{
			Enabled:          h.cfg.AlgoEnabled,
			ActiveGenerators: h.engine.Active(),
			AllGenerators:    h.cfg.AllGenerators,
		},
		Ranking: RankingConfig{CommonWordSlots: h.cfg.CommonWordSlots},
		Cache: CacheConfig{
			Enabled:    h.cache != nil,
			MaxSize:    h.cfg.CacheSize,
			TTLSeconds: int(cache.DefaultTTL.Seconds()),
		},
		TLDRegistry: TLDRegistryConfig{
			PSLDate:         tlds.DefaultRegistry.PSLDate(),
			ICANNTLDCount:   tlds.DefaultRegistry.ICANNCount(),
			Categories:      tlds.DefaultRegistry.CategoryNames(),
			DefaultCategory: "default",
		},
		Build: BuildInfo{
			Version: h.cfg.Version,
			BuiltAt: h.cfg.BuiltAt,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- helpers ---

// merge combines LLM and algo candidates, deduplicating on (sld,tld). LLM wins on conflict.
func merge(llmCands, algoCands []algorithmic.Candidate) []algorithmic.Candidate {
	seen := make(map[string]struct{}, len(llmCands)+len(algoCands))
	out := make([]algorithmic.Candidate, 0, len(llmCands)+len(algoCands))
	for _, c := range llmCands {
		key := c.SLD + "." + c.TLD
		seen[key] = struct{}{}
		out = append(out, c)
	}
	for _, c := range algoCands {
		key := c.SLD + "." + c.TLD
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}

// tierBalance selects top candidates respecting the llmShare split.
func tierBalance(scored []scorer.ScoredCandidate, count int, llmShare float64) []scorer.ScoredCandidate {
	llmTarget := int(math.Ceil(float64(count) * llmShare))
	algoTarget := count - llmTarget

	var llmPool, algoPool []scorer.ScoredCandidate
	for _, sc := range scored {
		if sc.Source == "llm" {
			llmPool = append(llmPool, sc)
		} else {
			algoPool = append(algoPool, sc)
		}
	}

	var out []scorer.ScoredCandidate
	llmTaken := min(llmTarget, len(llmPool))
	out = append(out, llmPool[:llmTaken]...)

	algoTaken := min(algoTarget, len(algoPool))
	out = append(out, algoPool[:algoTaken]...)

	// backfill if a tier came up short
	needed := count - len(out)
	if needed > 0 {
		// take from whichever pool has leftovers
		for _, pool := range [][]scorer.ScoredCandidate{llmPool[llmTaken:], algoPool[algoTaken:]} {
			for _, sc := range pool {
				if needed <= 0 {
					break
				}
				out = append(out, sc)
				needed--
			}
		}
	}

	// re-sort by score
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	return out
}

// diversityCap filters the candidate pool so no single TLD appears more than 25%
// of the requested count (when there are at least 4 TLDs available). It scales
// the per-TLD cap when fewer TLDs are available so the candidate pool is not starved.
func diversityCap(scored []scorer.ScoredCandidate, count int, numTLDs int) []scorer.ScoredCandidate {
	if numTLDs <= 0 {
		seenTLD := make(map[string]struct{})
		for _, sc := range scored {
			seenTLD[sc.TLD] = struct{}{}
		}
		numTLDs = len(seenTLD)
	}
	if numTLDs <= 1 {
		return scored
	}

	var maxShare float64
	switch numTLDs {
	case 2:
		maxShare = 0.75
	case 3:
		maxShare = 0.50
	default:
		maxShare = 0.25
	}

	tldCap := max(1, int(math.Ceil(float64(count)*maxShare)))
	tldCount := make(map[string]int)
	out := make([]scorer.ScoredCandidate, 0, len(scored))
	for _, sc := range scored {
		if tldCount[sc.TLD] < tldCap {
			tldCount[sc.TLD]++
			out = append(out, sc)
		}
	}
	return out
}

// sldDedup removes lower-scored duplicates sharing the same SLD.
func sldDedup(scored []scorer.ScoredCandidate) []scorer.ScoredCandidate {
	seen := make(map[string]struct{}, len(scored))
	out := make([]scorer.ScoredCandidate, 0, len(scored))
	for _, sc := range scored {
		if _, dup := seen[sc.SLD]; dup {
			continue
		}
		seen[sc.SLD] = struct{}{}
		out = append(out, sc)
	}
	return out
}

func countSource(source string, merged []algorithmic.Candidate) int {
	n := 0
	for _, c := range merged {
		if c.Source == source {
			n++
		}
	}
	return n
}

func generationSource(llmOK, algoContributed bool) string {
	if llmOK && algoContributed {
		return "both"
	}
	if llmOK {
		return "llm"
	}
	return "algorithmic"
}

// normalizeUnavailable lowercases and deduplicates the unavailable domains list.
func normalizeUnavailable(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// filterUnavailable removes candidates whose full name is in the unavailable set.
func filterUnavailable(scored []scorer.ScoredCandidate, unavailable []string) []scorer.ScoredCandidate {
	unavailSet := make(map[string]struct{}, len(unavailable))
	for _, d := range unavailable {
		unavailSet[d] = struct{}{}
	}
	out := make([]scorer.ScoredCandidate, 0, len(scored))
	for _, sc := range scored {
		if _, skip := unavailSet[sc.Name()]; !skip {
			out = append(out, sc)
		}
	}
	return out
}
