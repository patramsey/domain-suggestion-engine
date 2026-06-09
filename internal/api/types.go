package api

// TLDFilter selects which TLDs to use. Exactly one field must be set when present.
type TLDFilter struct {
	Category string   `json:"category,omitempty"`
	List     []string `json:"list,omitempty"`
}

type SuggestRequest struct {
	Input              string     `json:"input"`
	Count              int        `json:"count,omitempty"`
	TLDFilter          *TLDFilter `json:"tld_filter,omitempty"`
	UnavailableDomains []string   `json:"unavailable_domains,omitempty"`
	InspireFrom        []string   `json:"inspire_from,omitempty"`
}

type Suggestion struct {
	Name   string  `json:"name"`
	SLD    string  `json:"sld"`
	TLD    string  `json:"tld"`
	Score  float64 `json:"score"`
	Source string  `json:"source"`
}

type SuggestResponse struct {
	Suggestions      []Suggestion `json:"suggestions"`
	Partial          bool         `json:"partial"`
	TLDsUsed         []string     `json:"tlds_used,omitempty"`
	ActiveGenerators []string     `json:"active_generators,omitempty"`
}

type Category struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	TLDs  []string `json:"tlds"`
}

type CategoriesResponse struct {
	Categories []Category `json:"categories"`
}

// Health types

type CheckResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type HealthResponse struct {
	Status string                 `json:"status"`
	Checks map[string]CheckResult `json:"checks"`
}

// Config types

type LLMConfig struct {
	Model      string  `json:"model"`
	TimeoutMs  int     `json:"timeout_ms"`
	LLMShare   float64 `json:"llm_share"`
	APIKeySet  bool    `json:"api_key_set"`
}

type AlgoConfig struct {
	Enabled          bool     `json:"enabled"`
	ActiveGenerators []string `json:"active_generators"`
	AllGenerators    []string `json:"all_generators"`
}

type CacheConfig struct {
	Enabled    bool `json:"enabled"`
	MaxSize    int  `json:"max_size"`
	TTLSeconds int  `json:"ttl_seconds"`
}

type TLDRegistryConfig struct {
	PSLDate        string   `json:"psl_date"`
	ICANNTLDCount  int      `json:"icann_tld_count"`
	Categories     []string `json:"categories"`
	DefaultCategory string  `json:"default_category"`
}

type BuildInfo struct {
	Version string `json:"version"`
	BuiltAt string `json:"built_at"`
}

type ConfigResponse struct {
	LLM         LLMConfig         `json:"llm"`
	Algo        AlgoConfig        `json:"algo"`
	Cache       CacheConfig       `json:"cache"`
	TLDRegistry TLDRegistryConfig `json:"tld_registry"`
	Build       BuildInfo         `json:"build"`
}
