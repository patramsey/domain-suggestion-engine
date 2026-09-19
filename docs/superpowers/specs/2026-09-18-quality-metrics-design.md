# Deterministic suggestion-quality metrics — design

**Date:** 2026-09-18 · **Status:** approved in discussion, pending spec review
**Branch:** `feat/eval-harness-upgrades` (builds on the eval flags, funnel and snapshot config from commit `061ca41`)

## Problem

We need to move from `gemini-3.1-flash-lite` (shutdown 2027-05-07) to `gemini-3.5-flash-lite`,
and tune prompts so 3.5 is as good as possible. Tuning needs a quality measure that can tell
configs apart. The existing scorer (`internal/scorer`) cannot: every config scores 0.905–0.922,
and it ranks known-bad names at or above good ones:

| Query | Scores |
|---|---|
| craft beer subscription box | `yest.beer` (typo) 0.897 > `ale.house` 0.857 > `hopsmith.shop` 0.853 |
| team project management tool | `tether.works` 0.879 ≈ `catalyst.ventures` 0.878 ≈ `echo.com` 0.863 |

The scorer rates pronounceability, length, relevance and TLD quality per name. It has no
notion of typos, of names that are common words (almost certainly registered), or of names
that would fit any business.

## Goals

- Add deterministic, reproducible metrics that capture those three failure modes.
- Report them per config in the eval and store them in snapshots.
- Check the metrics against the user's own ratings before trusting them.

## Non-goals

- **No change to production ranking.** Metrics are eval-only. Promoting a validated metric
  into `scorer.Rank` is a separate, later change.
- **No availability checks.** A separate service owns availability; "common word" is a
  quality proxy for "plausibly available", not a lookup.
- **No LLM judge** for now. Revisit only if the validated metrics miss what the user cares about.
- **No hand-curated word lists** (project rule). All word data comes from a published dataset.

## Design

### 1. Word data: SCOWL (`internal/wordlist`, `cmd/gen/wordlist`)

- **Source:** classic SCOWL 2020.12.07 (wordlist.aspell.net; tarball from
  `downloads.sourceforge.net/project/wordlist/SCOWL/2020.12.07/scowl-2020.12.07.tar.gz`,
  pinned by SHA-256 `5587667caa20c4891390c2d42dbb4d5c4c3f41bee77af1457ece3ba23fb859cc`).
  License is MIT/X11-style (permissive). Curated for spell checking, so it contains almost no
  typo noise, unlike web frequency lists (`pizzaria` appears in count_1w, wordfreq and
  FrequencyWords).
  - *Why not SCOWL v2 (rel-2026.02.25)?* Verified 2026-09-18: v2 merges levels 10/20 into 35
    (so `late` and `tether` share a level), and abbreviates regular inflections (`balance <v>:
    -, -, balances` omits `balanced`), which would make inflected words look like typos.
    Classic SCOWL lists every inflection and keeps levels 10/20/35/40/50/55/60/70.
- **Generator:** `cmd/gen/wordlist` downloads the pinned tarball, verifies its SHA-256, and
  reads `final/english-words.N` and `final/american-words.N` for N ≤ 70. It keeps lowercase,
  purely alphabetic words (~111.6K) with their lowest level, and writes a gzipped embedded file.
  Wired into the Makefile as `make gen-wordlist`. The generated file is committed.
- **Membership-only words (`wordlist.Unranked`):** early probing found real words flagged as
  typos because they are British/Commonwealth spelling variants (`colour`, `centre`,
  `flavour`, `harbour`, `theatre`, ...) or proper nouns (place names, ...), which classic
  SCOWL keeps in separate regional and proper-name files rather than the ranked
  `english`/`american`-words lists. The generator additionally reads
  `final/{british,british_z,canadian,australian,variant_1}-words.N` and
  `final/{english,american,british,british_z,canadian,australian}-{proper-names,upper}.N`
  (N ≤ 70; abbreviations and contractions files are never read), lowercased and kept only if
  purely `[a-z]+`. These words are real for membership purposes but have no SCOWL commonness
  level, so they are stored at the sentinel level `wordlist.Unranked` (100) — above every
  ranked level, so they never satisfy `typoNeighbourMaxLevel` (50) or `commonMaxLevel` (20). A
  ranked level always wins when a word appears in both a ranked and a membership-only file.
- **API:** `wordlist.Level(word string) (level int, ok bool)`. Lower level = more common
  (10 = most common, 70 = rare); `Unranked` (100) means membership-only, no commonness data.
  Verified levels: `late`/`cold`/`balance` 10; `mint`/`echo`/`balanced` 20;
  `tether`/`hone`/`zenith` 35; `catalyst`/`yest` 40; `pizzeria` 50; `colour`/`centre`/`flavour`
  and proper nouns like place names: `Unranked`.
- **License notice:** SCOWL's copyright notice is committed at `internal/wordlist/data/NOTICE`,
  next to the embedded data, as its license requires.

### 2. Metrics (`internal/quality`)

Operate on the SLD (the label before the TLD). Pure functions, no I/O.

- **`IsTypo(sld) bool`** — true when all of:
  - `sld` is not in the word list,
  - `len(sld) ≥ 5` (short coinages have many one-edit neighbours: `lumo` → `limo`),
  - some word within edit distance 1 (deletion, insertion, substitution, adjacent
    transposition) is in the word list at level ≤ `typoNeighbourMaxLevel` (initially 50).
  - Examples: `pizzaria` → `pizzeria` ✓ flagged; `balanc` → `balance` ✓; `hone`, `tether`,
    `lather` not flagged (real words); `hopsmith` not flagged (no neighbour).
  - Known miss: `yest` is a real (archaic) word in SCOWL and only 4 letters, so it is not
    flagged. Accepted.
  - Borderline: `fermenta` → `ferment` is flagged. The human check decides whether such
    coinages should count as typos.
- **`IsCommonWord(sld) bool`** — `sld` is in the word list at level ≤ `commonMaxLevel`
  (initially 20): `late`, `cold`, `quiet`, `balance`, `mint`, `echo`. Not `tether` or
  `zenith` (35). This is a proxy for "plausibly already registered", used only to judge
  suggestion quality; the human ratings check it as a name-quality signal only (do very
  common words read as generic/less desirable?), not as a proxy for actual availability —
  that would need a real availability lookup, out of scope here (see Non-goals). Whether
  common words are in fact more likely to be taken is not tested by the ratings, and is not
  grounds for dropping the metric.
- **`Specificity(sld, queryTokens, otherQueriesTokens) (float64, bool)`** — relevance to this
  query minus mean relevance to the other queries in the eval set, using GloVe relevance.
  Quantifies the prompt's own test ("would this fit a yoga studio, a pizza place and a
  software startup equally?"). Requires exporting
  `scorer.ConceptRelevance(sld, tokens) (float64, bool)`, where `ok` is false when the scorer
  would fall back to its neutral 0.5 (no recognisable sub-words). Names with `ok == false`
  are excluded from specificity averages and counted separately, rather than scored 0.
  Scoring behaviour is unchanged; the existing unexported function becomes a wrapper.

Thresholds (`typoNeighbourMaxLevel`, `commonMaxLevel`, minimum typo length) are named
constants, set initially as above and adjusted only from the human-check results.

### 3. Eval integration (`cmd/eval`)

- For each config, the report adds: typo rate, common-word rate, mean specificity
  (and count of names with unknown specificity), next to the existing cross-query generic rate.
- Each rate is computed over **all kept names** and over **each query's top 10 by score**
  (what users see).
- Snapshots store per-suggestion flags (`typo`, `common_word`, `specificity`) and per-result
  aggregates, so any two snapshots can be compared after the fact.
- `go run ./cmd/eval -rescore <snapshot.json>` recomputes the metrics for an existing snapshot
  and writes them back into it, so older snapshots (including today's 3.1 vs 3.5 runs) can be
  scored without new API calls. Older snapshots without a `config` block are supported.

### 4. Human check

- **Sample:** ~150 names drawn from recent 3.1 and 3.5 snapshots, stratified so that flagged
  typos, common words, and high- and low-specificity names are all represented alongside
  random unflagged names. Duplicate names across configs are rated once.
- **Blind:** the page shows only the query and the domain. It never shows model, config or
  metric flags. Order is shuffled.
- **Page:** a private Artifact. One name at a time, rated good / okay / bad with keys 1 / 2 / 3,
  with undo and progress. Ratings persist in the Artifact's database so they can be read back.
- **Analysis:** for each metric, compare ratings of flagged vs unflagged names (e.g. share
  rated "bad" among flagged typos vs overall), and rank correlation between specificity and
  rating. The report states sample sizes and says plainly when a metric does not agree with
  the ratings.
- **Outcome:** metrics that agree are used to judge prompt tuning for 3.5. Metrics that don't
  are adjusted (thresholds) or dropped. For common-word specifically, the ratings validate it
  only as a name-quality signal (do raters find very common words worse names?) — its role as
  an availability proxy is a separate, untested claim and is not grounds for dropping the
  metric.

## Expansion points (only if the human check shows a need)

- **Finer word frequency:** add Google Books Ngram v3 1-grams (CC-BY 3.0, attribution notice
  required; ~8–10 GB one-off download, collapsed offline to a small table) if SCOWL's levels
  are too coarse — e.g. to catch typos of words that are themselves in SCOWL (`yest`).
- **Non-English queries:** Spanish membership/frequency data (sources and licenses not yet
  checked) if the `hard` query set shows non-English inputs matter.
- **LLM judge:** only if validated deterministic metrics still miss clear quality differences.

## Testing

- `internal/wordlist`: decodes the embedded file; known words present at plausible levels;
  a regeneration check that the committed file matches the pinned release.
- `internal/quality`: table tests with flagged and unflagged examples for each metric,
  including the known miss (`yest`) documented as expected behaviour; edit-distance-1
  neighbour generation tested directly.
- `internal/scorer`: `ConceptRelevance` matches the existing unexported function; existing
  scorer tests still pass unchanged.
- `cmd/eval`: metric aggregation over a fixed fake result set; recompute-from-snapshot
  round-trip.

## Out of scope for this spec

Sub-project D (production latency load test) and sub-project E (tuning the prompt for
`gemini-3.5-flash-lite`, informed by the funnel findings: cross-brief duplicates, invented
TLDs, shorter SLDs) are designed separately.
