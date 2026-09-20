# GEMINI.md

This file provides workspace instructions and guidelines for Google Antigravity / Gemini agents working in the `domain-suggestion-engine` repository.

## Commands

```bash
make build          # build bin/server and bin/score
make test           # run all unit and integration tests
go test -race ./... # verify no data races
make eval           # run 16-query prompt evaluation (requires GEMINI_API_KEY)
make eval ARGS="-model gemini-3.5-flash-lite -runs 3 -queries all -label test"

# Run a single test
go test ./internal/scorer/... -run TestBrandability

# Run the server locally
GEMINI_API_KEY=your-key ./bin/server

# Score competitor domains against a query
./bin/score --query "coffee shop" --domains "duskbrew.cafe,roast.coffee,perkbrew.pub"
./bin/score --query "coffee shop" --domains "duskbrew.cafe,roast.coffee" --json
```

## Architecture

The suggestion pipeline:
```
Input → parser → [LLM tier ‖ algorithmic tier] → scorer.Rank → tier balance + TLD diversity cap → top N
```

- **LLM tier** (`internal/llm/`): Parallel Gemini calls with configurable creative briefs (`evocative`, `wordplay`, `crafted`). Configurable globally via `LLM_VARIANTS` or per-request via `"variants": [...]`. Includes retry logic for 429s and partial JSON recovery.
- **Algorithmic tier** (`internal/algorithmic/`): Pluggable `Generator` interface with four implementations: `hacks.go`, `exact.go`, `compounds.go`, and `affixes.go`. Configurable via `GENERATORS` env var.
- **Scorer** (`internal/scorer/`):
  - `brandability` (40%): n-gram phonotactics + sub-word memorability.
  - `conceptRelevance` (30%): FastText subword quantized embeddings (`fasttext.bin`) with GloVe 50d fallback (`glove.bin`).
  - `tldPremium` (15%): per-TLD score + semantic match bonus.
  - `lengthScore` (15%): peaks at 3–8 characters.
  - Availability penalty: demotes common dictionary words (SCOWL ≤ 20: −0.20, SCOWL 35: −0.05) and crowded TLDs.
- **Availability checking**: When requested via `"check_availability": true` or `CHECK_AVAILABILITY=true` env var, DNS NS lookups run concurrently (`internal/dnscheck`) to annotate `is_available`. Real-time streaming is supported via `POST /suggest/stream` (SSE).

## Embedded Data Assets (`//go:embed`)

All static datasets are baked into the binary:
- `internal/scorer/data/fasttext.bin` — int8-quantised FastText subword vectors (~2.3MB)
- `internal/scorer/data/glove.bin` — int8-quantised GloVe 50d vectors, top-20K vocab (~1.1MB)
- `internal/scorer/ngrams_gen.go` — 26×26 bigram log-probability table
- `internal/scorer/tld_scores_gen.go` — per-TLD float64 scores for ~1,287 TLDs
- `internal/scorer/tld_crowding_gen.go` — per-TLD free rate from DNS delegation
- `internal/wordlist/data/words.txt.gz` — SCOWL 2020.12.07 word levels
- `data/tlds/` — PSL + hand-maintained category files

## Git & Workflow Rules

1. **Feature Branch Workflow**: Always create a feature branch (e.g., `feat/...` or `fix/...`) for code modifications. Never commit directly to `main`.
2. **Quality Verification**: Always run `go test -race ./...` and `make build` before proposing or finalizing PRs.
3. **Documentation Integrity**: Preserve existing comments and docstrings. Keep `openapi.yaml`, `README.md`, and `CLAUDE.md` in sync when modifying endpoints or options.
4. **Link Formatting**: Provide clickable markdown links using GitHub / file URI conventions for files and symbols in responses.
