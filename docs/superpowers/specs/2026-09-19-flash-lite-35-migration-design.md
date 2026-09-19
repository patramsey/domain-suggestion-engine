# Migrating the LLM tier to gemini-3.5-flash-lite — design

**Date:** 2026-09-19 · **Status:** approved in discussion, pending spec review
**Builds on:** eval harness upgrades and quality metrics (`docs/superpowers/specs/2026-09-18-quality-metrics-design.md`)

## Why

`gemini-3.1-flash-lite` shuts down on 2027-05-07; Google names `gemini-3.5-flash-lite` as its
replacement. Both accept `thinkingLevel: "minimal"` (committed), so 3.5 already runs.

## What we know (2026-09-18/19 measurements)

- **Name quality is not the gap.** In a blind rating of 150 names by the user (105 good /
  41 okay / 4 bad), names only 3.5 produced were rated good 88% of the time (n=49) vs 60% for
  names only 3.1 produced (n=97); among names with no typo or common-word flag, 94% (n=33) vs
  59% (n=61). One rater and one run per model, so strong evidence rather than proof.
- **Metric validation from the same ratings** (good vs not-good): typo-flagged names rated good
  36% vs 77% (p < 0.001) — typo is a valid quality signal. Common-word names rated good 93% vs
  65% — raters like them when availability is assumed, so common-word is an **availability**
  proxy only, never a quality penalty. Specificity correlates weakly (ρ = 0.19, p = 0.02).
- **The gaps are usability and yield** (core queries × 2 runs):
  - 3.5 top-10 common-word rate 54% vs 38% for 3.1 — likely-taken names crowd the results.
  - 3.5 keeps 56% of returned names vs 80%: cross-brief duplicates 438 vs 171, invented TLDs
    207 vs 82, truncation filter 191 vs 131. No MAX_TOKENS stops or broken JSON.
  - 3.5 is ~10% faster at the median and ~76% more expensive per query.
  - Raising the thinking level gave no quality gain on either model.

## Goal

Make 3.5 produce **usable** names: keep its rated quality while matching 3.1 on likely
availability and fixing the yield leaks, then switch production.

## Success criteria

Measured on each query's top 10 by score, `-runs 3 -queries all`, against a same-period 3.1
baseline:

| Measure | Target |
|---|---|
| Common-word rate | ≤ 3.1's |
| Typo rate | ≤ 3.1's |
| Mean specificity | ≥ 3.1's, within measured run-to-run noise |
| Names kept per query | ≥ 20 (the API's default `count`), so a default response can be filled |
| Median latency | ≤ 3.1's |
| Cost per query | ≤ 2× 3.1's (real per-model prices, thinking tokens included) |

**Final gates** (all must pass before the switch merges):

1. **Blind rating:** ~100 names drawn from 3.1's and tuned 3.5's top 10s; passes when tuned 3.5
   is rated good at least as often as 3.1.
2. **Full pipeline:** the same targets hold for names returned by the real `/suggest` endpoint
   (LLM + algorithmic tiers, 60/40 tier balance, TLD diversity cap, >50%-invalid retry), not
   just the eval's LLM-only output — see §5.
3. **Load:** under the §5 load test, tuned 3.5's p95 latency is ≤ 3.1's p95 measured the same
   way, and fewer than 1% of requests fail (including 429s that exhaust retries).

**Noise rule** (used to keep or drop a change): the noise band for a metric is the range
(max − min) of that metric's per-run aggregate across the 3 baseline runs of 3.5. A change is
kept only if it improves the metric by more than that band and does not worsen any other
target by more than its own band.

## Non-goals

- No change to production until the switch commit (approach "develop as eval variant, switch
  together").
- No scorer/ranking change (demoting common words in `scorer.Rank`, "approach B") in this
  project; it is the fallback if the prompt alone cannot meet the common-word target.
- No query-specific prompt instructions and no word lists (project rules): every change must
  generalise across queries; the `hard` query set guards against overfitting to `core`.
- No availability lookups (a separate service owns availability).

## Design

### 1. TLD constraint (`internal/llm`) — DROPPED after feasibility check

**Result (2026-09-19):** `gemini-3.5-flash-lite` accepts `responseSchema` and small TLD enums,
but rejects any enum above 88 values with `400 INVALID_ARGUMENT` (binary search; also fails with
`format: "enum"` and via `responseJsonSchema`). The default allowed list has 154 TLDs, so the
constraint cannot cover normal requests. Per the failure mode below, the lever is dropped;
invented TLDs are instead addressed by a prompt round (TLD adherence) and the existing filter.
The original design is kept below for reference.

- New client option `Client.ConstrainTLDs bool` (default false). When on, `call` sends
  `generationConfig.responseSchema`: an array of objects `{sld: string, tld: string enum}`
  whose enum is exactly the allowed TLD list passed to the request (154 TLDs by default). No
  hardcoded values.
- Off by default, so production is unchanged until the switch.
- First step is a feasibility check on 3.5: the schema is accepted, output honours the enum,
  and median latency does not rise noticeably. If it fails, this lever is dropped and the
  existing TLD filter remains.

### 2. Candidate prompts as eval variants (`cmd/eval`)

- Variants move to a new `cmd/eval/variants.go`. Each variant sets the system prompt, its
  three creative-brief texts (the existing `variantOverrides` mechanism), a temperature and
  the TLD-constraint setting.
- New flag `-variant <name>` runs only the named variant(s), so each experiment's API cost is
  bounded. The snapshot `config` block records the TLD-constraint setting per variant.
- Prompt levers, one per round:
  - **Brief separation:** the three briefs get non-overlapping jobs, and each is told what
    the other two cover.
  - **Common-word guidance:** a general instruction that single everyday dictionary words are
    almost always already registered, so prefer compounds, blends, coinages and domain hacks —
    while keeping the qualities the ratings rewarded (clean, specific, no truncations).
  - **Temperature:** sweep 0.8 / 1.0 / 1.2 on the best prompt.

### 3. Measurement fixes (`cmd/ratings`)

- `analyze` compares the share of names rated **good** (flagged vs unflagged) instead of the
  share rated bad; with only 4 bad ratings the bad-share test had no power.
- `sample` gains `-top N`: draw only from each result's top N by score, so the final blind
  check rates what users would see.

### 4. Procedure

1. **Baseline:** 3.1 and 3.5 with the current prompt, `-runs 3 -queries all`. Establishes
   3.1's targets, 3.5's start point and run-to-run noise.
2. **Rounds**, one lever each, same settings: brief separation + TLD adherence →
   common-word guidance → temperature sweep. (The TLD-constraint round is dropped; see §1.) Keep a change only if it beats the previous best
   by more than the noise band (see Success criteria). Each round is ~216 Gemini calls
   (~$0.30).
3. **Stop** when all targets are met, or after 6 rounds; in the latter case report the best
   variant against every target and let the user decide (relax a target, switch anyway, or
   add approach B).
4. **Final gate 1:** blind rating of 3.1 vs tuned 3.5 top 10s (≈100 names, `-top 10`).
5. **Final gates 2–3:** full-pipeline and load checks on the switch build (§5).

### 5. Switch

The switch commit is prepared on its own branch, checked end to end, and merged only if the
full-pipeline and load gates pass. Before the switch the candidate prompt exists only in the
eval, so these checks must run against a server built from the switch commit.

**Pipeline and load checker (`cmd/suggestcheck`).** A small tool that talks to a running
`/suggest` server:

- `quality` mode: sends each eval query (core + hard) sequentially, plus variants of a subset
  with `unavailable_domains` and with a TLD filter narrowed to 1–3 TLDs (exercising the enum's
  small-list edge case and those prompt branches); saves the responses; reports top-10 typo,
  common-word and specificity rates and names returned per request.
- `load` mode: sends the eval queries at a fixed concurrency (default 5) for a fixed number of
  requests (default 200); reports p50/p95/p99 end-to-end latency, error rate and 429 count.
- Both run once against a server built from the pre-switch commit (3.1) and once against the
  switch build (3.5), same machine, same day, with `CACHE_SIZE=0` so every request reaches the
  model.
- **Cache switch-off:** today `CACHE_SIZE=0` fails at startup (the LRU library rejects size 0).
  The handler is changed so `CACHE_SIZE=0` means no cache (skip get/set). The default stays
  500, so production behaviour is unchanged. This change lands before the checks, on the
  pre-switch side too, so both builds run uncached.

**The switch commit:**

1. The winning prompt text moves into `internal/llm/prompt.go` (system prompt and creative
   briefs).
2. The default model becomes `gemini-3.5-flash-lite` in `cmd/server/main.go` and `cmd/eval`.
3. The TLD constraint is enabled if it won its round; the winning temperature becomes the
   default.
4. The baseline is re-run just before switching; a final eval on the committed code must
   reproduce the tuned numbers.

The checks run against this commit on its branch; it merges only when gates 2 and 3 pass.

**Rollback:** revert the switch commit (restores 3.1, its prompt and settings).

**The user's uncommitted `internal/llm/prompt.go` edits** (unavailable-domains wording) are
decided by the user before the switch commit — commit, discard, or fold in. They only affect
requests with `unavailable_domains`, which the eval does not exercise, so tuning proceeds
meanwhile.

## Failure modes

- **TLD enum rejected, ignored or slow:** drop the lever; keep the existing filter.
- **Targets not met in 6 rounds:** stop and report; user decides.
- **Model drift before the switch:** baseline re-run just before switching; final comparison
  uses same-period runs.
- **Pipeline gate fails while eval passed:** the difference comes from production-only steps
  (tier balance, diversity cap, retry, unavailable/inspire branches); investigate that step
  before any further prompt tuning, and report to the user.
- **Load gate fails** (latency or 429s): try without the TLD constraint first; if still
  failing, report — rate limits may need a quota increase rather than a code change.
- **Cost above 2× 3.1's:** the variant fails the target regardless of quality; prefer the
  cheaper candidate or trim the prompt.

## Testing

- Unit tests: response-schema builder (enum equals the given TLDs; valid JSON shape; option off
  sends no schema; a 1-TLD list), `-variant` filter, `cmd/ratings sample -top`, good-share
  analysis, and `cmd/suggestcheck` request building and percentile/metric aggregation against
  an `httptest` server (no live API); the handler with `CacheSize: 0` serves requests without
  caching and still starts.
- The live API is called only by eval runs. Production tests stay green throughout; after the
  switch, existing integration tests run against 3.5.

---

## Part 2 — availability-aware ranking (added 2026-09-19 after the gates)

### What changed since Part 1

- The tuned prompts failed the blind rating (r1-briefs 56% good vs 3.1 82%; r3-briefs 68% vs 74%). **3.5 with the current prompt** won the three-way rating (84% vs 3.1's 74%) and passes the pipeline and load gates. The user chose to switch with the current prompt.
- A registrar availability check (read-only) showed untuned 3.5's top 10 is 21% registrable vs 38% for 3.1 (2.1 vs 3.8 names per query; 3/24 queries with none). Causes: more real words (116 coinages vs 509 in run 1) and more crowded TLDs (.ai/.io/.co/.app ≈ 6% free).
- Offline re-ranking showed word-rarity demotion alone plateaus at ~30–32%; adding a crowded-TLD penalty reaches ~33–35% (≈3.4 names/query). Re-ranking cannot reach 38% — the remainder is in 3.5's candidate pool. The user accepted this and chose rarity + crowded-TLD demotion.

### Design

1. **Word levels** (`internal/wordlist`): `CommonMaxLevel = 20`, `IsCommon(word)` (moved from `internal/quality`, which delegates), and `Words(minLevel, maxLevel) []string` (sorted) for probe-label selection.
2. **TLD crowding table** (`cmd/gen/tld-crowding` → `internal/scorer/tld_crowding_gen.go`): for every default-allowed TLD, the Laplace-smoothed share of a fixed probe-label set with no DNS delegation (NXDOMAIN on an NS lookup). Probe labels: a deterministic stride sample of ~60 SCOWL words at levels 35–70, 4–8 letters, identical for every TLD. Lookups go to a configurable public resolver (default `1.1.1.1:53`), low concurrency, retries. Vendor-neutral: no registrar dependency; the engine only reads the committed table and never makes network or availability calls at request time. TLDs missing from the table use a neutral free rate of 0.5.
   - Validation (2026-09-19 spike): DNS vs registrar availability on 1,957 names agreed 94.2% per name and ranked 45 TLDs with Spearman 0.96. The generated table must rank the 45 TLDs measured via the registrar check with Spearman ≥ 0.8 before it is used.
   - **Result (2026-09-19):** the generated table (probe labels SCOWL 35–70) ranked the 45 TLDs with Spearman 0.71; common-word probes (SCOWL 10–20) gave 0.73. The comparison is limited by small per-TLD registrar samples (15–73 names each) of a different word mix. **User decision:** the binding check is the outcome — top-10 availability with the table — which reaches 35.7–36.7% on the 3.5 baseline (3.1: 38%), above the 33% gate. Tuned penalties: common word 0.20, SCOWL-35 0.05, crowding weight 0.15.
3. **Ranking penalties** (`scorer.Score`, all candidates): subtract `0.15` for SCOWL ≤ 20 words, `0.05` for SCOWL-35 words, and `w × (1 − freeRate(tld))` with `w = 0.15`; clamp to [0, 1]. Starting values come from the feasibility sweep; final values are re-tuned offline against the real table (smallest penalties within 1 point of the best achievable top-10 availability).
4. **Gates before switching** (all must pass): offline top-10 availability ≥ 33% on the 3.5 baseline snapshot; a short blind rating of the new top-10 names (history reused) rated good at least as often as 3.1 (74%); pipeline and load checks on the final build.
5. **Shipping:** word-level API and generator/table land on `feat/flash-lite-35` (no behaviour change — nothing reads the table yet). The penalties ship with the model change on the switch branch, one revertible commit set.

### Testing

Unit tests for `wordlist.IsCommon`/`Words`; generator pure functions (probe selection determinism, counting with a fake lookup, smoothing/min-coverage, rendered file parses); scorer penalty (common-word score drops by exactly the penalty; crowded vs roomy TLD ordering; unknown TLD neutral). Existing scorer tests whose orderings conflict with the intended demotion are updated deliberately, each listed with its reason.
