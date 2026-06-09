package parser

import (
	"strings"
	"unicode"
)

var stopwords = map[string]struct{}{
	// articles, conjunctions, short prepositions
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "but": {},
	"of": {}, "in": {}, "on": {}, "at": {}, "by": {}, "to": {},
	"up": {}, "as": {}, "if": {}, "so": {}, "no": {}, "vs": {},
	// pronouns
	"i": {}, "we": {}, "you": {}, "he": {}, "she": {}, "it": {},
	"my": {}, "our": {}, "its": {}, "him": {}, "her": {}, "who": {},
	"them": {}, "they": {},
	// auxiliary verbs
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {},
	"has": {}, "had": {}, "have": {}, "can": {}, "may": {}, "do": {},
	// common connector / filler words
	"for": {}, "with": {}, "from": {}, "that": {}, "this": {}, "than": {},
	"then": {}, "when": {}, "where": {}, "how": {}, "what": {}, "which": {},
	"not": {}, "any": {}, "all": {}, "few": {}, "per": {}, "via": {},
	"use": {}, "get": {}, "make": {},
	// participial adjectives — appear in descriptions but make poor SLDs
	"powered": {}, "based": {}, "driven": {}, "focused": {}, "built": {},
	"made": {}, "used": {}, "designed": {}, "enabled": {}, "oriented": {},
	// standalone word fragments from common compound splits
	"end": {}, // "weekend" → "week"+"end"; "end" alone is useless
	// generic product/service nouns — useful LLM context but terrible SLDs
	"tool": {}, "tools": {}, "platform": {}, "service": {}, "services": {},
	"solution": {}, "solutions": {}, "software": {}, "system": {}, "systems": {},
	"product": {}, "products": {}, "feature": {}, "features": {},
	"document": {}, "documents": {}, "review": {}, "reviews": {},
	"workflow": {}, "dashboard": {}, "interface": {},
	// size/generic qualifiers that almost never make good SLDs
	"small": {}, "large": {}, "big": {}, "new": {}, "old": {},
}

// stemSuffixes maps removable suffixes to minimum stem length after removal.
var stemSuffixes = []struct {
	suffix    string
	minRemain int
}{
	{"ing", 3},
	{"ers", 3},
	{"er", 3},
	{"tion", 3},
	{"ness", 3},
	{"ment", 3},
	{"ly", 3},
	{"es", 3},
	{"s", 3},
}

// commonWords is a minimal set used for camelCase splitting and stemming validation.
// A word must appear here to be considered a valid stem result.
var commonWords = buildCommonWords()

// Parse converts any input form into a normalized token list.
// Handles: SLD strings ("patspizza.com"), camelCase, keywords, descriptions.
func Parse(input string, icannTLDs map[string]struct{}) []string {
	s := strings.TrimSpace(input)
	if s == "" {
		return nil
	}

	// strip known TLD suffixes from the end using lowercased form
	sLow := strings.ToLower(s)
	stripped := stripTLD(sLow, icannTLDs)

	// if TLD was stripped, use the stripped lowercase form; otherwise preserve
	// original case so camelCase splitting works
	var base string
	if stripped != sLow {
		base = stripped // already lowercase
	} else {
		base = s // preserve case for camelCase detection
	}

	// split on whitespace, hyphens, underscores, dots
	words := splitOnDelimiters(base)

	// split concatenated/camelCase words, then lowercase
	var expanded []string
	for _, w := range words {
		parts := splitCamel(w)
		for _, p := range parts {
			expanded = append(expanded, strings.ToLower(p))
		}
	}

	// remove stopwords, short tokens, non-alpha
	var filtered []string
	for _, w := range expanded {
		w = keepAlpha(w)
		if len(w) < 2 {
			continue
		}
		if _, stop := stopwords[w]; stop {
			continue
		}
		filtered = append(filtered, w)
	}

	// light stemming
	stemmed := make([]string, len(filtered))
	for i, w := range filtered {
		stemmed[i] = stem(w)
	}

	// deduplicate while preserving order
	return dedupe(stemmed)
}

// stripTLD removes a trailing TLD suffix from an SLD-style input like "patspizza.com".
// Tries longest match first to correctly handle multi-level suffixes (co.uk).
func stripTLD(s string, icann map[string]struct{}) string {
	// collect dot positions to try multi-level and single-level suffixes
	// try stripping from the last dot backward
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			candidate := s[i+1:]
			if _, ok := icann[candidate]; ok {
				return s[:i]
			}
		}
	}
	return s
}

func splitOnDelimiters(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '.' || r == '/' || r == '\t'
	})
}

// splitCamel splits a concatenated string into dictionary words using greedy longest-match.
// e.g. "patspizza" → ["pat", "pizza"], "coffeeshop" → ["coffee", "shop"]
func splitCamel(s string) []string {
	// First try splitting on actual camelCase boundaries.
	if parts := splitOnCase(s); len(parts) > 1 {
		return parts
	}
	// Then try greedy dictionary split.
	if parts := greedySplit(s, commonWords); len(parts) > 1 {
		return parts
	}
	return []string{s}
}

func splitOnCase(s string) []string {
	var parts []string
	start := 0
	runes := []rune(s)
	for i := 1; i < len(runes); i++ {
		if unicode.IsUpper(runes[i]) && unicode.IsLower(runes[i-1]) {
			parts = append(parts, strings.ToLower(string(runes[start:i])))
			start = i
		}
	}
	parts = append(parts, strings.ToLower(string(runes[start:])))
	if len(parts) == 1 {
		return nil
	}
	return parts
}

// greedySplit attempts a greedy longest-match dictionary split of s.
func greedySplit(s string, dict map[string]struct{}) []string {
	return greedySplitAt(s, dict)
}

func greedySplitAt(s string, dict map[string]struct{}) []string {
	if s == "" {
		return nil
	}
	// try lengths from longest to shortest
	for end := len(s); end >= 2; end-- {
		word := s[:end]
		if _, ok := dict[word]; ok {
			rest := s[end:]
			// direct continuation
			split := greedySplitAt(rest, dict)
			if rest == "" || len(split) > 0 {
				return append([]string{word}, split...)
			}
			// 's' junction: "patspizza" → "pat" + skip 's' → "pizza"
			if len(rest) > 1 && rest[0] == 's' {
				split = greedySplitAt(rest[1:], dict)
				if len(split) > 0 {
					return append([]string{word}, split...)
				}
			}
		}
	}
	return nil
}

func keepAlpha(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func stem(w string) string {
	for _, rule := range stemSuffixes {
		if strings.HasSuffix(w, rule.suffix) {
			stem := w[:len(w)-len(rule.suffix)]
			if len(stem) >= rule.minRemain {
				if _, ok := commonWords[stem]; ok {
					return stem
				}
			}
		}
	}
	return w
}

func dedupe(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

func buildCommonWords() map[string]struct{} {
	words := []string{
		"app", "art", "bar", "bay", "bit", "box", "buy", "car", "cat", "cup",
		"cut", "day", "dog", "dot", "eat", "egg", "end", "eye", "far", "fit",
		"fly", "fun", "get", "got", "guy", "hit", "hop", "hot", "hub", "ice",
		"ink", "jar", "jet", "job", "joy", "key", "kid", "kit", "lab", "law",
		"lay", "led", "leg", "let", "lid", "lip", "log", "lot", "low", "mad",
		"map", "mix", "mob", "mod", "mud", "nap", "net", "new", "nit", "nod",
		"now", "nut", "oak", "oil", "old", "one", "orb", "ore", "owe", "own",
		"pad", "pan", "pat", "pay", "pen", "pet", "pie", "pig", "pin", "pit",
		"pop", "pot", "pro", "pub", "put", "raw", "ray", "red", "rep", "rim",
		"rip", "rod", "row", "run", "rut", "sad", "sat", "saw", "say", "sea",
		"set", "sew", "shy", "ski", "sky", "sly", "son", "sow", "spa", "spy",
		"sum", "sun", "tab", "tag", "tap", "tar", "tax", "tea", "ten", "tie",
		"tip", "top", "tow", "toy", "try", "tub", "tug", "two", "van", "via",
		"vim", "vow", "war", "was", "wax", "web", "win", "wit", "woe", "won",
		"wow", "yak", "yam", "yew", "you", "zen", "zip", "zoo",
		// 4-letter
		"able", "acid", "acre", "aged", "aide", "ally", "also", "arch", "area",
		"army", "atom", "auto", "axis", "baby", "back", "bake", "ball", "band",
		"bank", "barn", "base", "bath", "beam", "bean", "bear", "beat", "beef",
		"been", "bell", "belt", "best", "bike", "bill", "bind", "bird", "bite",
		"blog", "blow", "blue", "boat", "body", "bold", "bolt", "bond", "bone",
		"book", "boom", "boot", "boss", "both", "bowl", "brew", "buck", "buff",
		"bulk", "bull", "burn", "burp", "busy", "byte", "cage", "cake", "call",
		"came", "camp", "cape", "card", "care", "cash", "cast", "cave", "cell",
		"chat", "chef", "chip", "city", "clad", "clam", "clan", "clap", "claw",
		"clay", "clip", "club", "clue", "coal", "coat", "code", "coil", "coin",
		"cold", "comb", "come", "cone", "cook", "cool", "copy", "cord", "core",
		"corn", "cost", "cozy", "crab", "crew", "crop", "crow", "cure", "curl",
		"cute", "dark", "dart", "dash", "data", "date", "deal", "dear", "deck",
		"deed", "deep", "deny", "desk", "dial", "dice", "diet", "dirt", "dish",
		"disk", "dock", "dome", "door", "dose", "down", "draw", "drip", "drop",
		"drum", "dude", "dull", "dump", "dune", "dusk", "dust", "duty", "dwell",
		"each", "earl", "earn", "ease", "east", "easy", "edge", "edit", "emit",
		"epic", "even", "ever", "exam", "exec", "exit", "expo", "face", "fact",
		"fail", "fair", "fake", "fall", "fame", "farm", "fast", "fate", "feed",
		"feel", "feet", "file", "fill", "film", "find", "fine", "fire", "firm",
		"fish", "flag", "flat", "flaw", "flex", "flow", "foam", "fold", "folk",
		"fond", "font", "food", "foot", "fork", "form", "fort", "four", "free",
		"from", "fuel", "full", "fund", "fuse", "gain", "game", "gate", "gear",
		"gift", "give", "glad", "glow", "glue", "goal", "gold", "golf", "good",
		"grab", "gram", "grid", "grit", "grow", "grip", "gulf", "guru", "hack",
		"hair", "half", "hall", "hand", "hard", "harm", "hash", "have", "head",
		"heal", "heap", "heat", "heel", "held", "helm", "help", "herb", "hero",
		"hide", "high", "hill", "hint", "hire", "hold", "hole", "holy", "home",
		"hook", "hope", "horn", "host", "hour", "hunt", "hype", "icon", "idea",
		"idle", "info", "iron", "isle", "item", "join", "jump", "just", "keen",
		"keep", "kick", "kind", "king", "knot", "know", "lack", "lake", "lamp",
		"land", "lane", "last", "late", "lead", "leaf", "lean", "leap", "left",
		"lens", "lift", "like", "lime", "line", "link", "lion", "list", "live",
		"load", "loan", "lock", "loft", "logo", "long", "look", "loop", "loss",
		"loud", "love", "luck", "lure", "lush", "made", "main", "make", "male",
		"mall", "mark", "mars", "mask", "mass", "mast", "math", "mean", "meet",
		"melt", "memo", "menu", "mesh", "meta", "mild", "mile", "milk", "mill",
		"mind", "mine", "mint", "mode", "more", "most", "move", "much", "muse",
		"myth", "nail", "name", "near", "need", "nest", "next", "nice", "node",
		"none", "noon", "norm", "nose", "note", "null", "noun", "only", "open",
		"oral", "oval", "over", "pace", "pack", "page", "pain", "pair", "pale",
		"park", "part", "pass", "path", "peak", "peel", "peer", "pick", "pier",
		"pile", "pine", "pink", "pipe", "plan", "play", "plot", "plug", "plus",
		"poem", "poet", "poll", "polo", "pond", "pool", "poor", "port", "pose",
		"post", "pour", "prep", "prey", "prim", "prod", "prop", "pull", "pump",
		"pure", "push", "quiz", "race", "rack", "rank", "rate", "read", "real",
		"reed", "reef", "reel", "rely", "rent", "rest", "rice", "rich", "ride",
		"ring", "rise", "risk", "road", "rock", "role", "roll", "roof", "room",
		"rope", "rose", "rule", "rust", "safe", "sage", "sale", "salt", "same",
		"sand", "save", "scan", "seal", "seed", "seek", "self", "sell", "semi",
		"send", "shed", "ship", "shop", "shot", "show", "sign", "silk", "site",
		"size", "skin", "slab", "slam", "slap", "slim", "slip", "slow", "slug",
		"snap", "snow", "soak", "soar", "sock", "soft", "soil", "solo", "some",
		"song", "sort", "soul", "soup", "span", "spin", "spot", "spur", "stab",
		"star", "stay", "stem", "step", "stir", "stop", "strap", "stub", "stun",
		"such", "suit", "swap", "swim", "sync", "tail", "talk", "tall", "tank",
		"tape", "task", "team", "tech", "tell", "tend", "tent", "term", "text",
		"than", "that", "them", "then", "they", "thin", "this", "tick", "tide",
		"till", "time", "tiny", "tire", "told", "toll", "tomb", "tone", "took",
		"tool", "town", "trap", "tree", "trim", "trip", "true", "tube", "tune",
		"type", "unit", "upon", "used", "user", "vast", "very", "vest", "view",
		"vine", "visa", "void", "vibe", "volt", "vote", "wade", "wage", "wait",
		"wake", "walk", "wall", "want", "warm", "wash", "wave", "week", "well",
		"went", "what", "when", "wide", "wild", "will", "wind", "wine", "wing",
		"wire", "wise", "wish", "with", "word", "work", "worn", "wrap", "yard",
		"year", "yoga", "your", "zone",
		// 5-letter+
		"about", "above", "added", "after", "again", "agent", "ahead", "algae",
		"along", "alter", "angle", "apart", "apple", "arena", "audio", "audit",
		"awake", "award", "aware", "batch", "beach", "begin", "bench", "berry",
		"black", "blade", "blank", "blast", "blaze", "bleed", "blend", "bless",
		"bliss", "block", "blood", "bloom", "board", "boast", "boost", "booze",
		"bound", "brain", "brand", "brave", "break", "brick", "brief", "bring",
		"broad", "brook", "brown", "brush", "build", "built", "burst", "buyer",
		"cabin", "cable", "carry", "catch", "cause", "chain", "chair", "chalk",
		"chaos", "charm", "chart", "chase", "cheap", "check", "chess", "chest",
		"chief", "child", "chill", "civic", "civil", "claim", "class", "clean",
		"clear", "clerk", "click", "climb", "clock", "clone", "close", "cloud",
		"coach", "coast", "combo", "comic", "comma", "coral", "count", "cover",
		"craft", "crane", "crash", "crazy", "cream", "creative", "crest", "crisp",
		"cross", "crowd", "crown", "crumb", "crush", "curve", "cycle", "daily",
		"dance", "decor", "delta", "dense", "depot", "depth", "derby", "devil",
		"diner", "dodge", "doing", "doubt", "dough", "draft", "drain", "drama",
		"drape", "drawn", "dream", "drink", "drive", "drone", "drove", "drown",
		"dwarf", "eagle", "early", "earth", "eight", "elite", "ember", "empty",
		"enter", "equal", "error", "event", "every", "exact", "extra", "fable",
		"faith", "fancy", "feast", "fetch", "fever", "fiber", "field", "fifth",
		"fifty", "fight", "final", "first", "fixed", "flame", "flash", "fleet",
		"flesh", "float", "flock", "flood", "floor", "flora", "floss", "fluid",
		"flute", "focus", "force", "forge", "found", "frame", "frank", "fraud",
		"fresh", "front", "frost", "froze", "fruit", "funny", "fuzzy", "giant",
		"given", "glade", "glare", "glass", "globe", "gloom", "gloss", "grace",
		"grade", "grain", "grand", "grant", "grape", "grasp", "grass", "grave",
		"great", "green", "greet", "grind", "groan", "groff", "gross", "grove",
		"guard", "guava", "guest", "guide", "guild", "gusto", "gypsy", "happy",
		"haven", "heart", "heavy", "hence", "honey", "house", "human", "humor",
		"hurry", "hustle", "ideal", "image", "indie", "inner", "input", "ivory",
		"jewel", "juicy", "juice", "jumbo", "jungle", "karma", "knack", "known",
		"label", "large", "laser", "later", "laugh", "layer", "learn", "least",
		"lemon", "level", "light", "limit", "local", "lodge", "logic", "loose",
		"lover", "lower", "lunch", "lusty", "magic", "major", "manor", "maple",
		"march", "match", "media", "merit", "metal", "might", "model", "money",
		"month", "moose", "mover", "music", "niche", "night", "noble", "noise",
		"north", "noted", "novel", "nurse", "nymph", "ocean", "offer", "often",
		"order", "organ", "other", "ought", "ozone", "paint", "panel", "party",
		"pasta", "patch", "peace", "pearl", "penny", "perch", "phase", "phone",
		"photo", "pilot", "pinch", "pixel", "pixel", "pizza", "place", "plain",
		"plane", "plant", "plate", "plaza", "pluck", "plumb", "point", "porch",
		"power", "press", "price", "pride", "prime", "print", "prior", "prism",
		"prize", "proof", "prose", "proud", "prove", "proxy", "pulse", "pupil",
		"query", "queue", "quick", "quiet", "quota", "quote", "radar", "radio",
		"rally", "ranch", "range", "rapid", "ratio", "razor", "reach", "ready",
		"realm", "rebel", "refer", "relay", "remix", "repay", "reset", "rider",
		"ridge", "rifle", "right", "risky", "river", "robin", "robin", "robot",
		"rocky", "rouge", "rough", "round", "route", "rowdy", "royal", "rugby",
		"ruler", "rural", "rusty", "salad", "sauce", "scale", "scare", "scene",
		"scent", "scope", "score", "scout", "screw", "scrub", "seedy", "sense",
		"serve", "setup", "seven", "shade", "shaft", "shake", "shall", "shame",
		"shape", "share", "shark", "sharp", "sheet", "shelf", "shell", "shift",
		"shine", "shirt", "short", "shout", "sight", "since", "sixth", "sixty",
		"skill", "slack", "slash", "sleep", "slice", "slide", "slope", "smart",
		"smile", "smoke", "solid", "solve", "sound", "south", "space", "speak",
		"speed", "spend", "spice", "spine", "split", "spoke", "spoon", "spray",
		"squad", "stack", "staff", "stage", "stake", "stale", "stamp", "stand",
		"stark", "start", "state", "stave", "steam", "steel", "steep", "steer",
		"stern", "stick", "stiff", "still", "stock", "storm", "story", "stove",
		"strap", "straw", "strip", "strum", "study", "stuff", "style", "sugar",
		"suite", "super", "surge", "swamp", "swear", "sweep", "sweet", "swift",
		"swipe", "swirl", "table", "taunt", "teach", "tease", "thank", "their",
		"there", "these", "thick", "thing", "think", "those", "three", "threw",
		"throw", "thumb", "tiger", "tight", "timer", "tired", "title", "today",
		"token", "torch", "total", "touch", "tough", "towel", "tower", "toxic",
		"track", "trade", "trail", "train", "trait", "treat", "trend", "trial",
		"tried", "trove", "truck", "truly", "truss", "trust", "truth", "tweak",
		"twice", "twist", "ultra", "under", "union", "until", "urban", "using",
		"utter", "valet", "value", "valve", "vapid", "vault", "verse", "video",
		"vigor", "viral", "visit", "vital", "vivid", "vocal", "voice", "waste",
		"watch", "water", "weave", "weird", "white", "whole", "width", "witch",
		"world", "worry", "worth", "would", "wrath", "write", "wrong", "yacht",
		"yield", "young", "yours",
		// domain-specific
		"brew", "cafe", "craft", "eco", "fresh", "green", "nano", "pixel",
		"tech", "wave", "bolt", "dash", "flux", "glow", "jade", "loft",
		"mint", "nest", "nova", "onyx", "peak", "reef", "roam", "sage",
		"silo", "snap", "trek", "turf", "vibe", "weld", "yolo", "zest",
		"agile", "aglow", "alive", "alpha", "amber", "ample", "ample",
		"blaze", "bliss", "brash", "brave", "brisk", "buddy", "built",
		"chaos", "civic", "clean", "clear", "clever", "coast", "crisp",
		"delta", "dense", "depth", "diner", "disco", "daily",
		"eager", "ember", "envoy", "equal", "ethics",
		"fancy", "fauna", "focal", "forge", "forte", "fresh", "funky",
		"glide", "gleam", "graft", "grasp", "gusto",
		"haven", "haze", "hearth", "helios", "hippo", "humid",
		"indie", "inward", "ivory",
		"jazzy", "joust",
		"karma",
		"lemon", "lunar", "lusty", "lyric",
		"maple", "metro", "micro", "milky", "moxie",
		"nexus", "nifty", "nimble", "nomad", "nymph",
		"oaken", "oasis", "olive", "omega", "onward",
		"panda", "petal", "phase", "pine", "pluck", "plume", "polar",
		"query", "quill", "quota",
		"radar", "rainy", "rally", "raven", "rebel", "remix", "ridge",
		"scout", "scrub", "serum", "servo", "sleek", "solid", "solar",
		"spire", "stark", "steam", "stout", "swift",
		"tango", "tempo", "tenor", "tiger", "tonic", "totem", "tuned",
		"ultra", "unity",
		"valor", "vault", "verge", "vital", "vocal", "voila",
		"waltz", "whisk",
		"xenon",
		"yield",
		"zesty", "zippy",
		// common brand/domain words
		"pizza", "espresso", "brew", "roast", "latte", "mocha", "java",
		"coffee", "studio", "agency", "creative", "digital", "social",
		"media", "online", "store", "shop", "market", "place", "space",
		"works", "labs", "hq", "hub", "base", "camp", "point",
		"solutions", "services", "systems", "network", "cloud", "data",
		// compound words that should stay whole rather than split into weak fragments
		"weekend", "outdoor", "outdoors", "startup", "standout", "standby",
		"insight", "outlook", "intake", "output", "uptime", "downtime",
		"feedback", "framework", "workflow", "backend", "frontend",
		"homepage", "toolbar", "sidebar", "checkbox",
	}
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}
