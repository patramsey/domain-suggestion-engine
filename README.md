# Domain Suggestion Engine

A Go HTTP API that generates ranked domain name suggestions from flexible input. Domains may be taken or available.

## How it works

```
Input (keywords, description, or existing domain like "patspizza.com")
  │
  ▼
Parser — tokenises, strips stopwords, extracts SLD from existing domains
  │
  ├─── LLM tier ─────────────────────────────────────────── ~1.9s, $0.0005/query
  │    Three parallel Gemini 3.1 Flash-Lite calls, each with a different
  │    creative brief to maximise variety:
  │
  │    ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
  │    │  Evocative   │  │  Wordplay    │  │  Crafted     │
  │    │  metaphors,  │  │  domain      │  │  portmanteaus│
  │    │  classical   │  │  hacks, TLD  │  │  coined      │
  │    │  words       │  │  cleverness  │  │  words       │
  │    └──────┬───────┘  └──────┬───────┘  └──────┬───────┘
  │           └─────────────────┴─────────────────┘
  │                      merge + SLD dedup
  │
  ├─── Algorithmic tier ────────────────────────────────── <5ms, deterministic
  │    Domain hacks: suffix-matches tokens against the TLD list
  │    e.g. "coffee" → coff.ee,  "studio" → stud.io
  │
  ▼
Score + rank (4 signals, see below)
  │
  ▼
Apply tier balance (default 60% LLM / 40% algo) + 25% per-TLD diversity cap
  │
  ▼
Return top N results
```

**Why two tiers?** The LLM excels at creative, concept-specific names but can't reliably discover domain hacks — it doesn't know which suffixes are valid TLDs. The algorithmic tier finds those instantly and deterministically. Each tier covers the other's blind spot.

**Why three LLM variants?** A single call converges on one creative direction. Three parallel calls with different briefs (evocative metaphors, structural wordplay, portmanteaus) produces breadth without extra latency — they all run simultaneously.

### Scoring

Each candidate is scored on four data-driven signals before ranking:

| Signal | Weight | How it works |
|--------|--------|--------------|
| Brandability | 40% | Phonotactic quality (70%) + sub-word recognizability (30%) |
| Concept relevance | 30% | Semantic similarity between the domain name and the query |
| TLD quality | 15% | Per-TLD score based on real-world adoption data, word-likeness, and semantic match |
| Length | 15% | SLD length curve peaking at 3–8 chars — short names are desirable, not penalised |

**Why these signals?** Brandability catches what sounds like a real word vs. a random consonant cluster — `forge` scores high, `xkqvz` scores near zero. Concept relevance catches whether the name actually relates to the query — `dental.clinic` beats `random.clinic` for a dentistry query. TLD quality reflects real-world recognition and meaning rather than treating all TLDs equally. Length reflects empirical domain popularity distributions.

LLM candidates also receive a position bonus: the LLM sorts its output best-first, so earlier suggestions get a small boost (~0.03–0.07) over later ones.

**How the signals work under the hood:**

- **Phonotactic quality** — A character n-gram model (a lookup table of which two-letter sequences appear frequently in English) scores how naturally a name rolls off the tongue. `coffee` has common English letter pairs; `xkqvz` doesn't appear in any English word and scores near zero. The model was trained on a large English word corpus and stored as a compact 26×26 probability table.

- **Sub-word recognizability** — The SLD is greedily split into the longest recognizable sub-words (e.g. `duskbrew` → `dusk` + `brew`). Names built from real words are more memorable; this acts as a bonus rather than a penalty so creative coinages aren't unfairly punished.

- **Semantic similarity** — Uses [GloVe](https://nlp.stanford.edu/projects/glove/) word embeddings: each word in the vocabulary is represented as a 50-dimensional numeric vector, where similar words cluster together in vector space (`coffee` and `espresso` are close; `coffee` and `skateboard` are not). We compute the average vector for the domain's sub-words and the query's keywords, then measure the cosine similarity between them (1 = same direction / same meaning, 0 = unrelated). A domain like `roast.coffee` will score high for the query "coffee shop" because `roast` and `coffee` are semantically nearby in the embedding space.

- **TLD adoption data** — TLD scores are derived from the [Majestic Million](https://majestic.com/reports/majestic-million) (a ranked list of the top million websites) and the [IANA root zone database](https://www.iana.org/domains/root/db). A TLD used by many popular websites ranks higher than an obscure one. A word-likeness bonus rewards TLDs that form real words (`.studio`, `.pizza`) over letter-soup ones.

### Performance

| Metric | Value |
|--------|-------|
| Latency (p50) | ~1.9s |
| Cost per query | ~$0.0005 |
| LLM model | Gemini 3.1 Flash-Lite |
| Throughput | Limited by Gemini rate limits |

Repeated identical queries are served from an in-memory LRU cache (default 500 entries, 5-minute TTL).

---

## Quick start

**Prerequisites:** Go 1.22+, `jq` (for the example commands)

```bash
# 1. Build
make build

# 2. Run (requires a Gemini API key)
GEMINI_API_KEY=your-key-here ./bin/server

# 3. First request
curl -s -X POST localhost:8080/suggest \
  -H 'Content-Type: application/json' \
  -d '{"input": "denver coffee shop that serves beer at night"}' | jq .
```

**Example response:**
```json
{
  "suggestions": [
    { "name": "duskbrew.cafe",  "sld": "duskbrew",  "tld": "cafe",  "score": 0.91, "source": "llm" },
    { "name": "perkbrew.pub",   "sld": "perkbrew",  "tld": "pub",   "score": 0.89, "source": "llm" },
    { "name": "roast.pub",      "sld": "roast",     "tld": "pub",   "score": 0.86, "source": "llm" },
    { "name": "coff.ee",        "sld": "coff",      "tld": "ee",    "score": 0.76, "source": "algorithmic" }
  ],
  "partial": false
}
```

### Iterative refinement with inspire_from

The real power comes from follow-up calls. Once the user picks names they like, pass them back to steer the next batch:

```bash
# 1. Initial search
curl -s -X POST localhost:8080/suggest \
  -H 'Content-Type: application/json' \
  -d '{"input": "sustainable outdoor gear brand", "count": 10}'

# → User likes "perennial.clothing" and "rootbound.life", but they're taken

# 2. Follow-up: steer toward that vibe, exclude the taken ones
curl -s -X POST localhost:8080/suggest \
  -H 'Content-Type: application/json' \
  -d '{
    "input": "sustainable outdoor gear brand",
    "count": 10,
    "inspire_from":        ["perennial.clothing", "rootbound.life"],
    "unavailable_domains": ["perennial.clothing", "rootbound.life"]
  }'
```

The LLM receives both lists: `inspire_from` tells it the creative direction to pursue; `unavailable_domains` prevents it from wasting output on names you've already checked.

---

### Scoring competitor domains with `bin/score`

`bin/score` runs the same scoring algorithm against any list of domain names — no API key or server required. Useful for benchmarking competitor suggestions or evaluating a hand-crafted shortlist.

```bash
# Score a comma-separated list
./bin/score --query "coffee shop denver" --domains "duskbrew.cafe,perkbrew.pub,roast.coffee"

# Score from a CSV file (first column used)
./bin/score --query "coffee shop denver" --file competitors.csv

# JSON output
./bin/score --query "coffee shop denver" --domains "duskbrew.cafe,roast.coffee" --json
```

```
domain                          score
----------------------------------------
roast.coffee                    0.804
perkbrew.pub                    0.760
duskbrew.cafe                   0.716
```

---

## Configuration

All configuration is via environment variables.

| Variable | Default | Description |
|---|---|---|
| `GEMINI_API_KEY` | — | **Required.** Gemini API key |
| `GEMINI_MODEL` | `gemini-3.5-flash-lite` | Model ID. Override to pin a specific version. |
| `PORT` | `8080` | HTTP listen port |
| `CACHE_SIZE` | `500` | LRU cache entry count |
| `LLM_SHARE` | `0.60` | Fraction of result slots reserved for LLM suggestions |
| `ALGO_ENABLED` | `true` | Set `false` for 100% LLM output |
| `GENERATORS` | `hacks` | Active algorithmic generators (comma-separated). Currently only `hacks` exists. |

---

## API

Full OpenAPI 3.1 spec is in [`openapi.yaml`](./openapi.yaml). You can paste it into [editor.swagger.io](https://editor.swagger.io) to browse it interactively.

### `POST /suggest`

Generate domain name suggestions.

**Query params**

| Param | Type | Description |
|---|---|---|
| `debug` | bool | Include `tlds_used` and `active_generators` in response |

**Request body**

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
| `input` | string | **Required.** Keywords, description, or existing domain (e.g. `patspizza.com`). Max 500 chars. |
| `count` | integer | Suggestions to return. Default `20`, max `100` |
| `tld_filter.category` | string | Named TLD preset — see `GET /tlds/categories`. Mutually exclusive with `list`. |
| `tld_filter.list` | []string | Explicit TLD list without dots, e.g. `["com", "io", "pizza"]` |
| `unavailable_domains` | []string | Domains confirmed taken — excluded from results and passed to the LLM. Max 40. |
| `inspire_from` | []string | Domains the user liked — LLM steers toward that creative direction. Max 5. |

**Response**

```json
{
  "suggestions": [
    { "name": "pats.pizza", "sld": "pats", "tld": "pizza", "score": 0.82, "source": "llm" },
    { "name": "stud.io",    "sld": "stud", "tld": "io",    "score": 0.76, "source": "algorithmic" }
  ],
  "partial": false
}
```

`partial: true` means only one tier contributed (LLM failure or all-stopword input).

**Error codes**

| HTTP | `code` | Meaning |
|---|---|---|
| 400 | `missing_input` | `input` field absent or empty |
| 400 | `ambiguous_tld_filter` | Both `category` and `list` set |
| 422 | `input_too_long` | Input exceeds 500 characters |
| 422 | `unknown_tld` | `list` contains a TLD not in the PSL |
| 422 | `unknown_category` | `category` names a preset that doesn't exist |
| 422 | `too_many_unavailable_domains` | `unavailable_domains` exceeds 40 entries |
| 422 | `too_many_inspire_from` | `inspire_from` exceeds 5 entries |
| 500 | `both_tiers_failed` | LLM error and algorithmic tier produced nothing |

---

### `GET /tlds/categories`

List all available TLD categories with their contents.

```bash
curl -s localhost:8080/tlds/categories | jq '.categories[] | {name, count}'
```

| Name | Contents |
|---|---|
| `default` | ~151 curated high-value TLDs (used when no filter is specified) |
| `all` | Full PSL ICANN section (~1,500 TLDs) |
| `identity_digital` | ~240 Identity Digital registry TLDs |
| `classic` | com, net, org, info, biz |
| `tech` | io, ai, dev, app, cloud, software, codes |
| `country` | All 2-character ICANN ccTLDs |

---

### `GET /health`

Returns `200` when fully configured, `503` when misconfigured.

```bash
curl -s localhost:8080/health | jq .
```

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

Current runtime configuration snapshot. Never exposes the API key value.

---

## Development

```bash
make test           # run all tests
make build          # build bin/server and bin/score
make eval           # run prompt evaluation harness (requires GEMINI_API_KEY)
make update-psl     # refresh the vendored Public Suffix List
make gen-tld-scores # regenerate TLD scores from IANA + Majestic Million
make gen-ngrams     # regenerate character n-gram tables from English word corpus
make gen-glove      # regenerate GloVe word embeddings (~860 MB download)
```

The three `gen-*` targets fetch external datasets and write generated Go/binary files that are committed to the repo. Re-run them when you want to refresh the underlying data (e.g. after a significant Majestic Million update, or to pick up new TLDs from a PSL refresh).

**Self-contained binary** — the n-gram tables, GloVe vectors, TLD scores, and PSL data are all embedded into the compiled binary via Go's [`//go:embed`](https://pkg.go.dev/embed) directive. The server has zero runtime file dependencies; you can copy a single binary anywhere and run it.

### Adding a new algorithmic generator

The algorithmic tier uses a `Generator` interface (`internal/algorithmic/generator.go`):

```go
type Generator interface {
    Name() string  // used in the GENERATORS env var
    Generate(tokens []string, tlds []string) []Candidate
}
```

To add a new generator:
1. Create a new file in `internal/algorithmic/` implementing the interface
2. Add it to the slice in `DefaultGenerators()` in `engine.go`
3. Activate it by including its name in the `GENERATORS` env var (comma-separated)

The engine deduplicates candidates across all active generators, so generators can overlap without producing duplicates.

### Prompt evaluation

`make eval` runs the 16-query `core` set against the current prompt and scorer, saving a timestamped JSON snapshot to `eval-results/`. Commit the snapshot to track quality over time — `git diff eval-results/` between two snapshots shows exactly which suggestions changed.

Pass flags through `ARGS` to compare configurations:

```bash
make eval ARGS="-model gemini-3.5-flash-lite -runs 3 -label m35"
make eval ARGS="-thinking low -temperature 0.7 -queries all"
```

| Flag | Default | Purpose |
|---|---|---|
| `-model` | `$GEMINI_MODEL`, else `gemini-3.5-flash-lite` | Model under test |
| `-thinking` | `minimal` | Gemini `thinkingLevel` (`minimal`, `low`, `medium`, `high`) |
| `-temperature` | each variant's own | Override generation temperature (0–2) |
| `-runs` | `1` | Repeat each query to measure run-to-run noise |
| `-queries` | `core` | `core` (historical 16), `hard` (short, vague, non-English, long, niche inputs) or `all` |
| `-variant` | `current` | Prompt variant(s) to run: comma-separated names, or `all` |
| `-label` | none | Added to the snapshot filename |
| `-rescore` | none | Re-annotate an existing snapshot with quality metrics; no API calls |

With -runs 2 or more, the report adds per-run top-10 metrics and each metric's noise band (max − min across runs).

Each snapshot records its full configuration (model, thinking level, temperatures, prompt fingerprint, git commit and whether code was uncommitted), per-call token usage including thinking tokens, cost at the model's real price (see `cmd/eval/pricing.go`), and a yield funnel showing where returned names were dropped.

Every saved suggestion is also annotated with deterministic quality flags, and the report prints them per config for all kept names and for each query's top 10:

- **Typo** — not a word, 5+ letters, and one edit from a common word (`pizzaria`, `balanc`).
- **Common word** — a very common English word (SCOWL level ≤ 20: `late`, `mint`), almost certainly registered.
- **Specificity** — GloVe relevance to its own query minus mean relevance to the other queries; near zero means the name would fit any business. Specificity is relative to the snapshot's own query set, so only compare specificity between snapshots run on the same `-queries` set.

Word data is classic SCOWL 2020.12.07, embedded via `make gen-wordlist` (see `internal/wordlist/data/NOTICE`). These metrics are eval-only; production ranking does not use them.

Run it after any change to `internal/llm/prompt.go`, `internal/scorer/`, or the Gemini model version. See `eval-results/README.md` for the full experiment history and methodology.

To check the metrics against human judgement, `cmd/ratings` builds a blind sample and analyses ratings:

```bash
go run ./cmd/ratings sample -n 150 -seed 1 -out ratings/ eval-results/run-A.json eval-results/run-B.json
# rate ratings/items.json (good / okay / bad), save as ratings/ratings.json
go run ./cmd/ratings analyze -dir ratings/
```

`ratings.json` is a flat array of `{"id": "r001", "rating": "good"}` objects, one per rated item in `items.json`, where `rating` is one of `good`, `okay` or `bad`.

`cmd/suggestcheck` checks a running server end to end (both tiers, tier balance, TLD diversity cap, retry, unavailable-domains and TLD-filter paths). Start the server with `CACHE_SIZE=0` so every request reaches the model:

```bash
CACHE_SIZE=0 GEMINI_API_KEY=... ./bin/server &
go run ./cmd/suggestcheck quality -url http://localhost:8080 -out quality.json
go run ./cmd/suggestcheck load -url http://localhost:8080 -n 200 -c 5 -out load.json
```

**Disabling the algorithmic tier (LLM only):**

```bash
GEMINI_API_KEY=your-key ALGO_ENABLED=false ./bin/server
```

---

## Project structure

```
cmd/
  server/            HTTP server entrypoint
  eval/              Prompt evaluation harness (make eval)
  gen/
    tld-scores/      Generates internal/scorer/tld_scores_gen.go
    ngrams/          Generates internal/scorer/ngrams_gen.go
    glove/           Generates internal/scorer/data/glove.bin
internal/
  api/               Request handling, types, errors
  tlds/              Public Suffix List parser, TLD category registry
  parser/            Input tokenizer (SLD stripping, camelCase split, stopwords)
  algorithmic/       Generator interface + domain hacks generator
  llm/               Gemini REST client, multi-variant prompt builder, response validation
  scorer/            Domain quality scorer + embedded data (n-gram tables, GloVe vectors, TLD scores)
  cache/             In-process LRU cache with TTL
data/
  tlds/              Vendored PSL + hand-maintained category files
eval-results/        Prompt experiment history, methodology notes, and run snapshots — see eval-results/README.md
RESEARCH.md          Background research: naming theory, scoring signals, data sources
openapi.yaml         Full OpenAPI 3.1 spec (also browseable at editor.swagger.io)
LICENSE              MIT
```
