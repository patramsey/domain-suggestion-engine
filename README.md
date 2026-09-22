# Domain Suggestion Engine

A Go HTTP API that turns a few keywords, a business description or an existing domain into a ranked list of brandable domain names.

```bash
curl -s -X POST localhost:8080/suggest -d '{"input": "denver coffee shop that serves beer at night"}'
# → brewwave.beer, solstice.live, nightcap.coffee, alpenglow.pub, vesper.coffee, coff.ee, …
```

- **Hybrid LLM and algorithmic tiers, unified ranking.** Gemini proposes creative, concept-specific names; deterministic algorithmic generators find domain hacks (`coff.ee`), exact matches (`coffee.bar`), compounds (`brewcraft.coffee`), and affixes (`getcoffee.com`). Both tiers are scored by the same data-driven ranker.
- **Leans toward registrable names, with optional live verification.** The engine demotes names that are almost certainly taken (common dictionary words, crowded TLDs), but keeps 2 of every 10 results for the best very common single words (`mint.cafe`). When live check is desired, the engine runs concurrent DNS delegation checks or streams real-time availability via SSE.
- **Iterative.** Pass back names the user liked (`inspire_from`) and names that turned out to be taken (`unavailable_domains`) to steer the next batch.
- **One self-contained binary.** Quantized FastText and GloVe embeddings, n-gram tables, TLD data and word lists are embedded; the only external runtime dependency is the Gemini API.

## Contents

- [Quick start](#quick-start)
- [How it works](#how-it-works)
- [Ranking](#ranking)
- [API](#api)
- [Configuration](#configuration)
- [Scoring your own list](#scoring-your-own-list)
- [Development](#development)
- [Project structure](#project-structure)

---

## Quick start

**Prerequisites:** Go 1.26+, a [Gemini API key](https://aistudio.google.com/apikey), and `jq` for the example commands.

```bash
make build
GEMINI_API_KEY=your-key ./bin/server

curl -s -X POST localhost:8080/suggest \
  -H 'Content-Type: application/json' \
  -d '{"input": "denver coffee shop that serves beer at night", "count": 8}' | jq .
```

Real output (scores rounded; LLM output varies between calls):

```json
{
  "suggestions": [
    { "name": "brewwave.beer",   "sld": "brewwave",  "tld": "beer",   "score": 0.86, "source": "llm" },
    { "name": "solstice.live",   "sld": "solstice",  "tld": "live",   "score": 0.86, "source": "llm" },
    { "name": "nightcap.coffee", "sld": "nightcap",  "tld": "coffee", "score": 0.83, "source": "llm" },
    { "name": "alpenglow.pub",   "sld": "alpenglow", "tld": "pub",    "score": 0.81, "source": "llm" },
    { "name": "vesper.coffee",   "sld": "vesper",    "tld": "coffee", "score": 0.80, "source": "llm" },
    { "name": "velvet.bar",      "sld": "velvet",    "tld": "bar",    "score": 0.80, "source": "llm" },
    { "name": "afterglow.bar",   "sld": "afterglow", "tld": "bar",    "score": 0.79, "source": "llm" },
    { "name": "coff.ee",         "sld": "coff",      "tld": "ee",     "score": 0.78, "source": "algorithmic" }
  ],
  "partial": false
}
```

More examples are in [`examples/`](./examples) (`basic.sh`, `edge_cases.sh`, `explore_tlds.sh`).

### Refining with `inspire_from` and `unavailable_domains`

Suggestions work best as a conversation. Once the user has picked favourites — and found some of them taken — send them back:

```bash
# First batch
curl -s -X POST localhost:8080/suggest \
  -H 'Content-Type: application/json' \
  -d '{"input": "sustainable outdoor gear brand", "count": 10}'

# The user likes perennial.clothing and rootbound.life, but both are taken
curl -s -X POST localhost:8080/suggest \
  -H 'Content-Type: application/json' \
  -d '{
    "input": "sustainable outdoor gear brand",
    "count": 10,
    "inspire_from":        ["perennial.clothing", "rootbound.life"],
    "unavailable_domains": ["perennial.clothing", "rootbound.life"]
  }'
```

The LLM sees both lists: `inspire_from` sets the creative direction, and `unavailable_domains` keeps it from spending output on names already ruled out. Unavailable names are also removed from the results.

---

## How it works

```
Input (keywords, description, or existing domain like "patspizza.com")
  │
  ▼
Parser — tokenises, strips stopwords, extracts the SLD from existing domains
  │
  ├─── LLM tier ──────────────────────────────────────────── ~1.25s
  │    Three parallel Gemini 3.5 Flash-Lite calls, each with a different
  │    creative brief to maximise variety:
  │
  │    ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
  │    │  Evocative   │  │  Wordplay    │  │  Compounds   │
  │    │  metaphors,  │  │  domain      │  │  two real    │
  │    │  classical   │  │  hacks, TLD  │  │  words that  │
  │    │  words       │  │  cleverness  │  │  fit concept │
  │    └──────┬───────┘  └──────┬───────┘  └──────┬───────┘
  │           └─────────────────┴─────────────────┘
  │                      merge + SLD dedup
  │
  ├─── Algorithmic tier ──────────────────────────────────── <5ms, deterministic
  │    Pluggable generators: hacks (coff.ee), exact (coffee.bar),
  │    compounds (brewcraft.coffee), and affixes (getcoffee.com)
  │
  ▼
Score + rank (four quality signals, minus an availability penalty)
  │
  ▼
Tier balance (default 60% LLM / 40% algorithmic) + 25% per-TLD diversity cap
  │
  ▼
Remove unavailable_domains, top up if the cap left the list short
  │
  ▼
Reserve 2 of every 10 results for very common single words, return the top N
```

**Why two tiers?** The LLM is good at creative, concept-specific names but can't reliably find domain hacks, because it doesn't know which suffixes are real TLDs. The algorithmic tier finds them instantly and deterministically. Each covers the other's blind spot.

**Why three LLM calls?** A single call converges on one creative direction. Three parallel calls with different briefs give breadth without adding latency. The third brief asks for compounds of two ordinary words — one for what the business makes or does, one for the feeling it should evoke (`darkroast`, `lenscraft`) — because such names are rarely registered yet read like real brands.

**What if a tier fails?** The response still returns whatever the other tier produced, with `"partial": true`. Only when both fail does the request return an error.

### Performance

| | |
|---|---|
| Latency | ~1.25s median, ~1.45s p95 (measured at 5 concurrent requests) |
| Cost per request | ~$0.003 at Gemini paid-tier prices |
| LLM model | `gemini-3.5-flash-lite` (override with `GEMINI_MODEL`) |
| Throughput | Bounded by your Gemini rate limits |

Identical requests are served from an in-memory LRU cache (500 entries, 5-minute TTL by default).

---

## Ranking

Every candidate gets a score in `[0, 1]`:

```
score = 0.40 × brandability + 0.30 × concept relevance + 0.15 × TLD quality + 0.15 × length
        + LLM position bonus
        − availability penalty
```

| Signal | Weight | What it measures |
|--------|--------|------------------|
| Brandability | 40% | How natural the name sounds (80%) and how recognisable its parts are (20%) |
| Concept relevance | 30% | Semantic similarity between the name and the query |
| TLD quality | 15% | Real-world adoption of the TLD, whether it's a word, and whether it fits the query |
| Length | 15% | Peaks at 3–8 characters |

Brandability separates `forge` from `xkqvz`. Concept relevance makes `dental.clinic` beat `random.clinic` for a dentistry query. TLD quality reflects real recognition and meaning instead of treating all TLDs alike. Numbers score 0 for brandability and hyphens 0.15.

**LLM position bonus.** The LLM lists its best ideas first, so earlier suggestions get a small boost (about 0.03–0.07).

### Availability penalty

The engine never checks whether a domain is registered, but some names are almost certainly taken. Showing them first wastes the user's time, so they are demoted:

| Penalty | Amount | Why |
|---------|--------|-----|
| The name is a very common English word (`mint`, `late`) | −0.20 | Common words are registered on nearly every popular TLD |
| The name is a moderately common word | −0.05 | Registered often, but less reliably |
| TLD crowding | −0.15 × share of ordinary words already registered on that TLD | `.com` is about 94% taken for ordinary words; `.io` about 55% |

### Common-word slots

Very common single words (`mint`, `forge`, `case`) are some of the best names there are, but about 7 in 8 are already registered. Demoting them all would hide them completely, so **2 of every 10 results** (`COMMON_WORD_SLOTS`) are kept for the best of them, scored without the common-word penalty (the TLD crowding penalty still applies). Each block of 10 results holds at most 2 such words and is sorted by score, so every page of 10 shows 2 alongside 8 names that are mostly registrable. When one turns out to be taken, pass it back in `unavailable_domains` on the next request.

Word commonness comes from [SCOWL](http://wordlist.aspell.net/) frequency levels (very common: level ≤ 20; moderately common: level 35). TLD crowding comes from a table generated offline by checking which of a fixed set of probe words have DNS delegations on each TLD (`make gen-tld-crowding`). Both are embedded, so ranking makes no network calls.

Together with the compound brief, this makes about half of the LLM's top 20 registrable at standard price (51% in evaluation, against 26% before the compound brief and 44% for the previous model), with 82% of top-10 names rated good in blind review. The common-word slots then trade about 2 of those registrable names per 20 results for strong common words, bringing a full response to roughly 42–46%. The experiment history is in [`eval-results/README.md`](./eval-results/README.md).

### How the signals work

- **Phonotactic quality** — a character bigram model (a 26×26 table of how often each letter pair appears in English) scores how naturally a name reads aloud. `coffee` is made of common pairs; `xkqvz` isn't.
- **Sub-word recognisability** — the name is split into known words, preferring a split that covers every letter (`duskbrew` → `dusk` + `brew`, `aidrive` → `ai` + `drive`) and falling back to the best partial cover. A whole dictionary word, or any name of 1–2 letters, counts as fully recognisable. Names built from real words are more memorable. This is a bonus, not a penalty, so coined words aren't punished.
- **Semantic similarity** — quantized [FastText](https://fasttext.cc/) subword embeddings (`fasttext.bin`) with [GloVe](https://nlp.stanford.edu/projects/glove/) 50d fallback (`glove.bin`) place each word and morpheme as a dense vector, with related words close together (`coffee` near `espresso`, far from `skateboard`). Subword vector representations allow scoring novel coined compounds and affixes even when unseen in vocabulary. The score is the cosine similarity between the name's sub-words and the query's keywords, so `roast.coffee` scores well for "coffee shop".
- **TLD quality** — derived from the [Majestic Million](https://majestic.com/reports/majestic-million) and the [IANA root zone database](https://www.iana.org/domains/root/db): TLDs used by many popular sites rank higher, and TLDs that are real words (`.studio`, `.pizza`) get a bonus over letter-soup ones.

---

## API

The full OpenAPI 3.1 spec is in [`openapi.yaml`](./openapi.yaml); paste it into [editor.swagger.io](https://editor.swagger.io) to browse it.

### `POST /suggest`

**Request**

```json
{
  "input": "denver coffee shop that serves beer",
  "count": 20,
  "tld_filter": { "category": "default" },
  "unavailable_domains": ["ember.coffee"],
  "inspire_from": ["roast.pub"]
}
```

| Field | Type | Description |
|---|---|---|
| `input` | string | **Required.** Keywords, a description, or an existing domain (`patspizza.com`). Max 500 characters. |
| `count` | integer | Number of suggestions. Default `20`, max `100`. |
| `tld_filter.category` | string | A named TLD preset (see [`GET /tlds/categories`](#get-tldscategories)). Can't be combined with `list`. |
| `tld_filter.list` | []string | An explicit TLD list without dots, e.g. `["com", "io", "pizza"]`. |
| `unavailable_domains` | []string | Domains known to be taken: removed from results and shown to the LLM. Max 40. |
| `inspire_from` | []string | Domains the user liked; the LLM steers toward them. Max 5. |
| `check_availability` | boolean | When `true`, runs concurrent DNS delegation checks and populates `is_available`. Default `false`. |
| `variants` | []string | Creative LLM prompts to run (`evocative`, `wordplay`, `crafted`). Default: all three. |

Add `?debug=true` to include `tlds_used` and `active_generators` in the response.

**Response**

```json
{
  "suggestions": [
    { "name": "nightcap.coffee", "sld": "nightcap", "tld": "coffee", "score": 0.83, "source": "llm", "is_available": true },
    { "name": "coff.ee",         "sld": "coff",     "tld": "ee",     "score": 0.78, "source": "algorithmic", "is_available": false }
  ],
  "partial": false
}
```

`source` is `llm` or `algorithmic`. `is_available` is populated when `check_availability: true` was requested (or server has `CHECK_AVAILABILITY=true`), or `null` otherwise. A response holds `count` names whenever the tiers produce enough; the per-TLD diversity cap never shortens it (with a narrow `tld_filter`, results concentrate in the TLDs you asked for). Results come in blocks of 10, each sorted by score; each block holds at most 2 very common single words (see [Common-word slots](#common-word-slots)). `partial: true` means only one tier contributed (for example, the LLM call failed, or the input was all stopwords).

### `POST /suggest/stream`

Streams domain suggestions and real-time DNS availability updates over Server-Sent Events (SSE). Accepts the exact same request body as `POST /suggest`.

1. `event: suggestions` — Emits the full candidate list immediately.
2. `event: avail` — Emits individual DNS results as concurrent workers resolve them (`{"name": "...", "available": true/false}`).
3. `event: done` — Signals stream completion (`{}`).

**Errors** are returned as `{"error": "...", "code": "..."}`:

| HTTP | `code` | Meaning |
|---|---|---|
| 400 | `missing_input` | `input` is missing or empty |
| 400 | `ambiguous_tld_filter` | Both `category` and `list` are set |
| 422 | `input_too_long` | `input` is over 500 characters |
| 422 | `unknown_tld` | `list` contains a TLD that isn't in the Public Suffix List |
| 422 | `unknown_category` | `category` isn't a known preset |
| 422 | `too_many_unavailable_domains` | More than 40 `unavailable_domains` |
| 422 | `too_many_inspire_from` | More than 5 `inspire_from` |
| 500 | `both_tiers_failed` | The LLM failed and the algorithmic tier produced nothing |

### `GET /tlds/categories`

Lists the TLD presets and their contents.

```bash
curl -s localhost:8080/tlds/categories | jq '.categories[] | {name, count}'
```

| Name | Contents |
|---|---|
| `default` | 154 curated TLDs — used when no filter is given |
| `all` | The full ICANN section of the Public Suffix List (6,925 entries) |
| `identity_digital` | 160 Identity Digital registry TLDs |
| `classic` | `com`, `net`, `org`, `info`, `biz` |
| `tech` | `io`, `ai`, `dev`, `app`, `cloud`, `software`, `codes` |
| `country` | All 247 two-letter country-code TLDs |

### `GET /health`

Returns `200` when the server is fully configured and `503` otherwise.

```json
{
  "status": "ok",
  "checks": {
    "tld_registry": { "ok": true },
    "llm_key":      { "ok": true },
    "cache":        { "ok": true },
    "generators":   { "ok": true }
  }
}
```

### `GET /config`

A snapshot of the running configuration: model, LLM timeout and share, generators, common-word slots, cache settings, TLD registry date and build version. The API key is reported only as `api_key_set: true/false`.

---

## Configuration

All configuration is through environment variables.

| Variable | Default | Description |
|---|---|---|
| `GEMINI_API_KEY` | — | **Required.** Gemini API key. |
| `GEMINI_MODEL` | `gemini-3.5-flash-lite` | Gemini model ID. |
| `PORT` | `8080` | HTTP listen port. |
| `CACHE_SIZE` | `500` | Response cache entries. `0` disables the cache. |
| `LLM_SHARE` | `0.60` | Share of result slots reserved for LLM suggestions. |
| `ALGO_ENABLED` | `true` | `false` turns off the algorithmic tier (LLM-only results). |
| `COMMON_WORD_SLOTS` | `2` | Results per 10 kept for very common single words, ranked by quality (0–10). `0` ranks them with the full availability penalty, which pushes nearly all of them out. |
| `GENERATORS` | `hacks,exact,compounds,affixes` | Comma-separated list of active algorithmic generators (`hacks`, `exact`, `compounds`, `affixes`). |
| `LLM_VARIANTS` | `evocative,wordplay,crafted` | Comma-separated creative briefs to run concurrently. |
| `CHECK_AVAILABILITY` | `false` | When `true`, enables live DNS availability check by default on all `/suggest` requests. |
| `DNS_RESOLVER` | `1.1.1.1:53` | Upstream DNS resolver host:port for live availability lookups. |

---

## Scoring your own list

`bin/score` runs the same ranking over any list of domains, with no server or API key. Use it to compare competitors' names or check a shortlist.

```bash
./bin/score --query "coffee shop denver" --domains "duskbrew.cafe,perkbrew.pub,roast.coffee,brew.coffee,coffee.com"
./bin/score --query "coffee shop denver" --file competitors.csv   # first column is used
./bin/score --query "coffee shop denver" --domains "duskbrew.cafe,roast.coffee" --json
```

```
domain                          score
----------------------------------------
perkbrew.pub                    0.753
brew.coffee                     0.745
roast.coffee                    0.740
duskbrew.cafe                   0.701
coffee.com                      0.498
```

`coffee.com` ranks last despite being an excellent name: it is a very common word on the most crowded TLD, so the availability penalty assumes it is taken.

---

## Development

```bash
make build          # build bin/server and bin/score
make test           # run all tests
make eval           # run the prompt evaluation (reads the key from ~/.gemini-api-key)
```

Integration tests that call Gemini are behind a build tag and skip without a key:

```bash
GEMINI_API_KEY=your-key go test -tags integration ./internal/api/
```

### Embedded data

The server has no runtime file dependencies: everything below is compiled in with [`//go:embed`](https://pkg.go.dev/embed) or generated Go code, and committed to the repo. Regenerate only when refreshing the source data.

| Command | Regenerates | Source |
|---|---|---|
| `make update-psl` | `data/tlds/public_suffix_list.dat` | [Public Suffix List](https://publicsuffix.org/) |
| `make gen-tld-scores` | `internal/scorer/tld_scores_gen.go` | IANA root zone + Majestic Million |
| `make gen-ngrams` | `internal/scorer/ngrams_gen.go` | English word corpus |
| `make gen-glove` | `internal/scorer/data/glove.bin` | GloVe 6B (~860 MB download) |
| `go run ./cmd/gen/fasttext` | `internal/scorer/data/fasttext.bin` | Quantized FastText English subwords model |
| `make gen-wordlist` | `internal/wordlist/data/words.txt.gz` | SCOWL 2020.12.07 (see `internal/wordlist/data/NOTICE`) |
| `make gen-tld-crowding` | `internal/scorer/tld_crowding_gen.go` | Live DNS lookups of probe words on each TLD |

### Adding an algorithmic generator

Generators implement one interface (`internal/algorithmic/generator.go`):

```go
type Generator interface {
    Name() string // used in the GENERATORS env var
    Generate(tokens []string, tlds []string) []Candidate
}
```

1. Add a file in `internal/algorithmic/` that implements it.
2. Register it in `DefaultGenerators()` in `engine.go`.
3. Enable it by adding its name to `GENERATORS`.

Candidates are deduplicated across generators, so they can overlap safely.

### Evaluating changes

Run an evaluation after any change to `internal/llm/prompt.go`, `internal/scorer/`, or the model. The experiment history and methodology live in [`eval-results/README.md`](./eval-results/README.md).

**`make eval`** runs a query set through the LLM tier and scorer and saves a JSON snapshot to `eval-results/`. Each snapshot records its full configuration (model, thinking level, temperatures, prompt fingerprint, git commit), token usage and cost, and a yield funnel showing where names were dropped.

```bash
make eval ARGS="-runs 3 -queries all -label baseline"
make eval ARGS="-model gemini-3.1-flash-lite -thinking low"
```

| Flag | Default | Purpose |
|---|---|---|
| `-model` | `$GEMINI_MODEL`, else `gemini-3.5-flash-lite` | Model under test |
| `-thinking` | `minimal` | Gemini `thinkingLevel`: `minimal`, `low`, `medium`, `high` |
| `-temperature` | per variant | Override the generation temperature (0–2) |
| `-runs` | `1` | Repeat each query; with 2+, the report shows per-run numbers and noise bands |
| `-queries` | `core` | `core` (16 queries), `hard` (short, vague, non-English, long, niche) or `all` |
| `-variant` | `current` | Prompt variants to run, comma-separated, or `all` |
| `-label` | — | Appended to the snapshot filename |
| `-rescore` | — | Re-annotate an existing snapshot with quality metrics, without API calls |
| `-avail` | off | DNS-check each query's top 20 names and report the likely-registrable share for the top 10 and top 20 (also works with `-rescore`) |
| `-resolver` | `1.1.1.1:53` | DNS resolver for `-avail` |

The report flags three deterministic quality signals, for all kept names and for each query's top 10:

- **Typo** — not a word, 5+ letters, and one edit away from a common word (`pizzaria`). Validated against blind human ratings: flagged names are rated good far less often.
- **Common word** — SCOWL level ≤ 20 (`late`, `mint`). An availability proxy, not a quality problem: people like these names, they just can't register them.
- **Specificity** — relevance to its own query minus average relevance to the other queries. Only comparable between snapshots run on the same query set.

**Likely registrable** (`-avail`) — the share of each query's top 10 and top 20 with no DNS delegation, using the same lookups as `make gen-tld-crowding`. It is a fast screen, not a registrar check: it never marks an available name as taken, but it counts premium-priced and reserved names as free, so it reads 8–13 points above a registrar's standard-price availability. Confirm winners with a registrar check. The eval makes these lookups; the engine never does.

**Blind human ratings** (`cmd/ratings`) check the metrics — and compare models — against human judgement:

```bash
go run ./cmd/ratings sample -n 100 -seed 1 -out ratings/ -history eval-results/ratings/history.json run-A.json run-B.json
# rate ratings/items.json as good / okay / bad, save ratings/ratings.json
go run ./cmd/ratings analyze -dir ratings/
go run ./cmd/ratings history-add -dir ratings/ -history eval-results/ratings/history.json -round 5
```

`ratings.json` is an array of `{"id": "r001", "rating": "good"}`. The history file stores past ratings so the same name is never rated twice, and a round never contains two TLD variants of one name.

**End-to-end checks** (`cmd/suggestcheck`) exercise a running server: both tiers, tier balance, the TLD diversity cap, retries, and the `unavailable_domains` and TLD-filter paths, plus a load test. Disable the cache so every request reaches the model:

```bash
CACHE_SIZE=0 GEMINI_API_KEY=your-key ./bin/server &
go run ./cmd/suggestcheck quality -url http://localhost:8080 -out quality.json
go run ./cmd/suggestcheck load -url http://localhost:8080 -n 200 -c 5 -out load.json
```

---

## Project structure

```
cmd/
  server/            HTTP server
  score/             bin/score: rank any list of domains offline
  eval/              Prompt evaluation harness (make eval)
  ratings/           Blind human-rating samples, analysis and history
  suggestcheck/      End-to-end quality and load checks against a running server
  gen/               Generators for the embedded data (see Development)
internal/
  api/               Routing, validation and the full suggest pipeline
  parser/            Input tokeniser (SLD stripping, camelCase split, stopwords)
  llm/               Gemini client, prompts and the three creative briefs, response validation
  algorithmic/       Generator interface and the domain-hack generator
  scorer/            Ranking signals, availability penalty and their embedded data
  wordlist/          Embedded SCOWL word levels
  quality/           Eval-only quality metrics (typo, common word, specificity)
  evalset/           Evaluation query sets
  tlds/              Public Suffix List parser and TLD categories
  cache/             In-process LRU cache with TTL
data/tlds/           Vendored Public Suffix List and hand-maintained category files
examples/            Shell scripts exercising the API
eval-results/        Experiment history, methodology, rating history and selected snapshots
RESEARCH.md          Background research: naming theory, scoring signals, data sources
openapi.yaml         OpenAPI 3.1 spec
```

## License

[MIT](./LICENSE)
