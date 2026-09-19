# Prompt Evaluation Results

Each eval run compares prompt variants across 16 fixed queries. The key metric is **cross-query generic rate** — percentage of suggestions whose SLD appeared in >25% of query result sets. A generic SLD (e.g. `zenith`, `haven`, `solace`) fits any concept equally well and signals poor anchoring.

Run `make eval` to reproduce. Results may vary slightly between runs due to model temperature — use `-runs N` to measure that noise before trusting a small difference. See the Prompt evaluation section of the top-level README for flags.

Snapshots from 2026-09-18 onward include a `config` block (model, thinking level, temperatures, prompt fingerprint, git commit, dirty flag) and a per-query `funnel`. Earlier snapshots lack these; their thinking level was effectively `minimal`, and their `est_cost_usd` used a placeholder rate ($0.075 / $0.30 per 1M tokens) that does not match any real model price. Snapshots also carry per-suggestion `typo`, `common_word` and `specificity` fields and a per-result `quality` summary; older snapshots can be annotated in place with `go run ./cmd/eval -rescore <file>`.

## Drift tracking

Each `make eval` run saves a `run-<timestamp>.json` snapshot to this directory containing the full suggestion list for every query. Commit these snapshots to track quality drift over time — `git diff` between two snapshots shows exactly which suggestions changed, appeared, or disappeared.

---

## Run 1 — 2026-06-07: old-current vs simplified vs minimal vs temperature

**Context:** The original prompt had accumulated many rules over iterative sessions. This run tested whether a shorter prompt produced better results.

| Variant | Results | TLD Div | Generic% | ms/q | $/query |
|---------|---------|---------|----------|------|---------|
| current (old, ~600 words) | 52 | 29 | 18% | 3160 | $0.000789 |
| **simplified** | 52 | 25 | **8%** | **1833** | **$0.000510** |
| minimal | 49 | 20 | 11% | 1755 | $0.000428 |
| current-t0.7 | 52 | 26 | 23% | 1704 | $0.000784 |
| simplified-t0.7 | 50 | 24 | 13% | 1763 | $0.000509 |

**Winner: `simplified`** — half the generic rate, 42% faster, 35% cheaper.

**Key findings:**
- Lower temperature (0.7) made generic rate WORSE across both prompts. Model falls back to safe familiar words at lower temperature. Stick with 0.9.
- The long TLD double-entendre list and territory system in the old prompt added noise without improving quality.
- Generic SLDs in old current: `arc, aurum, axis, cinder, dusk, ember, glimmer, haven, haze, hearth, lode, mellow, serene, solstice, vesper, vivid`

**Action:** `simplified` promoted to production `SystemPrompt`.

---

## Run 2 — 2026-06-07: new baseline (simplified as current)

**Context:** Confirming new baseline numbers after promoting simplified to production.

| Variant | Results | TLD Div | Generic% | ms/q | $/query |
|---------|---------|---------|----------|------|---------|
| current (simplified) | 50 | 25 | 11% | 1717 | $0.000510 |
| minimal | 48 | 21 | 10% | 1714 | $0.000429 |
| current-t0.7 | 53 | 23 | 11% | 1752 | $0.000510 |

**Key findings:**
- Generic SLDs for new current: `aura, epoch, haven, kindle, mellow, meridian, pulse, repose, solace, umbra, verve, zenith`
- `minimal` is competitive but TLD diversity (21) is noticeably lower
- Persistent offenders across all variants: `zenith`, `haven`, `solace`

---

## Run 3 — 2026-06-07: specificity-check vs anchored-examples vs both

**Context:** Testing two hypotheses for reducing the remaining generic rate.
- `specificity-check`: add "would this fit yoga + pizza + SaaS equally? if yes, discard it" to pre-generation step
- `anchored-examples`: replace generic few-shot examples with concept-specific ones (solon.ai, prana.studio, yeast.run, peel.tools)
- `both`: combine both changes

| Variant | Results | TLD Div | Generic% | ms/q | $/query |
|---------|---------|---------|----------|------|---------|
| current | 51 | 26 | 7% | 1769 | $0.000508 |
| **specificity-check** | 53 | 25 | **5%** | **1723** | $0.000521 |
| anchored-examples | 53 | 23 | 8% | 1774 | $0.000512 |
| both | 51 | 24 | **3%** | 1959 | $0.000523 |

**Winner: `specificity-check`** — 5% generic rate (vs 7%), actually faster than current (1723ms vs 1769ms). `both` wins on pure generic rate (3%) but costs 190ms more latency with marginal quality gain.

**Key findings:**
- `anchored-examples` WORSENED generic rate (8% vs 7%). Concept-specific examples don't teach anchoring — the model treats all few-shot entries as format demos regardless of content.
- `specificity-check` works because it forces **active evaluation** ("would this fit yoga + pizza + SaaS?") rather than a passive rule to acknowledge.
- `zenith` appears in every variant's generic list — the most persistent offender.
- Notable quality improvements in `specificity-check`: `liminal.life` (yoga), `levain.works` (pizza/sourdough), `argot.solutions` (legal jargon).
- `both` produced unexpected clustering in pizza query: `patina/pater/patio/patois/patron` — the specificity test anchored heavily on "pat" from "patspizza.com".

**Action:** `specificity-check` promoted to production `SystemPrompt`.

---

## Persistent issues across all runs

- **`zenith`**: appears in generic list for every variant tested. Immune to both rule-based and example-based approaches.
- **`haven`, `solace`, `repose`**: peace/refuge words that the model defaults to for any calm/wellness adjacent query.
- Examples noted: the few-shot example `ember` bled into suggestions for unrelated queries (yoga, games, pizza) in early runs. Mitigated by changing the examples section label to "do not reuse these specific SLDs."

---

## Run 4 — 2026-06-07: temp-1.0, no-examples, tld-hints

**Context:** Testing three new hypotheses against the specificity-check baseline.

| Variant | Results | TLD Div | Generic% | ms/q | $/query |
|---------|---------|---------|----------|------|---------|
| current | 51 | 23 | 4% | 1955 | $0.000519 |
| no-examples | 53 | 24 | 4% | 1933 | $0.000578 |
| tld-hints | 54 | 23 | 5% | **1672** | $0.000533 |
| **temp-1.0** | 53 | **26** | **3%** | 2152 | **$0.000515** |

**Winner: `temp-1.0`** — best generic rate (3%), highest TLD diversity (26), nearly identical cost (+$0.000004/query). Costs 197ms more latency.

**Key findings:**
- Higher temperature (1.0) improves TLD diversity meaningfully (26 vs 23). Higher randomness reduces the model's tendency to default to the same TLD clusters.
- `tld-hints` worsened generic rate (5%) — confirmed across two tests now. Adding TLD examples to the prompt introduces noise, not focus.
- `no-examples` ties current on generic rate but costs more — without format anchoring, the model generates more raw candidates (higher output tokens).
- Notable quality improvements at temp-1.0: `hopvine.co` (craft beer), `relique.market` (French for relic, vintage), `juris.ai` (Latin for law), `alpen.pub` (Denver altitude), `draught.works`
- `mellow` appears in EVERY variant's generic list across all runs — the most persistent offender
- `zenith` appears in 3 of 4 this run — second most persistent

**Action:** `temp-1.0` promoted to production (change `client.Temperature` default to 1.0).

---

---

## Run 5 — 2026-06-07: 16-query baseline + fixed threshold

**Context:** Expanded from 8 to 16 queries (adding tech SaaS, local services, finance, photography, outdoor gear) and switched from a fixed `>=3` threshold to `>25% of queries` so the metric scales with query set size.

| Variant | Results | TLD Div | Generic% | ms/q | $/query |
|---------|---------|---------|----------|------|---------|
| **current-t1.0** | 51 | 23 | **7%** | 1717 | $0.000513 |
| current-t0.9 | 52 | 24 | 8% | 1709 | $0.000516 |
| no-ex-t1.0 | 52 | 24 | 9% | 2024 | $0.000601 |

**Key findings:**
- **Temperature 1.0 vs 0.9 is a wash.** The earlier 3% vs 4% difference (run 4) was noise from only 8 queries. With 16 queries: 7% vs 8%, within noise. Keep t1.0 (already deployed), but it's not a meaningful gain.
- **`no-examples` is consistently worse** across all 16-query runs. Examples are earning their place. This experiment is closed.
- **7-8% generic rate is likely near the practical floor** for prompt-only approaches. Some flagged words (`tempo`, `hearth`) are legitimately appropriate for multiple related queries — the cross-query metric can't distinguish "appropriate for a semantic cluster" from "truly generic."
- **Persistent generics**: `haven`, `tempo`, `tether`, `zenith`, `vivid`, `fathom` — appearing in 4+ of 16 diverse queries. `zenith` is the clearest offender (appears in legal, tech, outdoor gear — completely unrelated domains).

**Action:** No change to production. Baseline confirmed at 7% with 16 queries.

---

## Summary of promoted changes (in order)

| Date | Change | Generic% Before | Generic% After | Note |
|------|--------|----------------|----------------|------|
| 2026-06-07 | old→simplified prompt | 18% | 8% | 8-query eval |
| 2026-06-07 | +specificity-check | 8% | ~5-7% | 8-query eval |
| 2026-06-07 | temperature 0.9→1.0 | 4% | 3% | noise, 8-query eval |
| 2026-06-07 | 16-query rebaseline | — | 7% | stable baseline |

---

## Persistent issues

- **`zenith`**: appears in generic list for every run across all variants. Immune to prompt and temperature changes. Most clearly generic word — appears for legal, tech, outdoor gear, yoga.
- **`mellow`, `haven`, `tempo`**: appear across 4+ queries but may be appropriate for semantic clusters (calm/wellness, shelter/warmth, rhythm/pace).
- **`vivid`**: consistently generic — fits photography, games, fashion, and many others equally.
- **TLD diversity**: stable at 23-25 per query. Prompt-only approaches haven't moved this significantly.

---

## Run 6 — 2026-06-07: sharper-evocative, ten-industries, complete-sentence

**Context:** Three targeted experiments against the 7% baseline.
- `sharper-evocative`: replace evocative variant instruction with one requiring concept-specific justification ("feel surprising or wrong for most other businesses")
- `ten-industries`: replace "yoga+pizza+SaaS" test with a 10-industry checklist in system prompt
- `complete-sentence`: replace binary test with "complete the sentence: [SLD] fits because ___"

| Variant | Results | TLD Div | Generic% | ms/q | $/query |
|---------|---------|---------|----------|------|---------|
| current | 51 | 25 | 4% | 1786 | $0.000517 |
| **sharper-evocative** | 48 | 25 | **1%** | 2042 | $0.000518 |
| ten-industries | 53 | 25 | 5% | 1744 | $0.000521 |
| complete-sentence | 52 | **27** | 5% | 1813 | $0.000520 |

**Winner: `sharper-evocative`** — 1 generic SLD across 16 queries (was 6). A 75% reduction. Only `nexus` remained (and it's genuinely appropriate for hub/community concepts).

**Key findings:**
- **The evocative variant was the main source of generics.** Changing the system prompt wasn't the fix — changing the creative variant instruction that gets appended to the user message was. The evocative variant was generating `haven`, `tempo`, `vivid`, `mellow` etc. by telling the model to "draw from sensation, time, natural material, and mood" — those territories produce broadly atmospheric words.
- **Framing the test differently in the system prompt doesn't help.** Both `ten-industries` (5%) and `complete-sentence` (5%) performed the same as current. The system prompt tests are already working; the generic words come from the variant instructions.
- **Quality is dramatically better** with `sharper-evocative`: yoga gets `drishti.vision`, `mudra.academy`, `yantra.design`; craft beer gets `wort.beer`, `gyle.bar`; barbershop gets `razor.bar`, `fringe.salon`, `shear.community`.
- **`complete-sentence` has highest TLD diversity (27)** — the justification step may encourage more varied TLD thinking.
- Tradeoff: `sharper-evocative` occasionally produces very niche results (`asynchronous.me` for project management, `syncopate.studio`) that are creative but may be too obscure for some users. 256ms slower.

**Action:** `sharper-evocative` instruction promoted to production `VariantEvocative` in `prompt.go`.

---

## Summary of promoted changes (in order)

| Date | Change | Generic% Before | Generic% After | Note |
|------|--------|----------------|----------------|------|
| 2026-06-07 | old→simplified prompt | 18% | 8% | 8-query eval |
| 2026-06-07 | +specificity-check in system prompt | 8% | ~5-7% | 8-query eval |
| 2026-06-07 | temperature 0.9→1.0 | 4% | 3% | noise, 8-query eval |
| 2026-06-07 | 16-query rebaseline | — | 7% | stable baseline established |
| 2026-06-07 | sharper evocative variant instruction | 4% | 1% | 16-query eval |

---

## Persistent issues

- **`nexus`**: The last remaining generic — appears for hub/community/connection concepts. Arguably concept-appropriate in those cases. Accepted.
- **TLD diversity**: stable at 23-27. `complete-sentence` variant pushes it to 27 but wasn't better on generics.

## Status

Prompt iteration complete. Subsequent runs at 0–1% generic rate confirmed stable. Work shifted to scorer improvements (n-gram brandability, GloVe concept relevance, data-driven TLD scores) — see main README for current scorer architecture.

---

## Metric validation — 2026-09-19: blind human rating of 150 names

**Setup:** 150 names drawn blind from the 3.1 and 3.5 smoke snapshots (`cmd/ratings sample -n 150 -seed 1`), stratified to include flagged names; rated good / okay / bad by one rater (the project owner). Result: 105 good, 41 okay, 4 bad.

| Metric | Comparison (share rated **good**) | Verdict |
|---|---|---|
| Typo | flagged 36% (n=25) vs unflagged 77% (n=125), p < 0.001 | **Agrees** — valid quality signal |
| Common word | flagged 93% (n=29) vs unflagged 65% (n=121), p = 0.003 | Raters *prefer* common words when availability is assumed — use only as an **availability** proxy, never a quality penalty |
| Specificity | Spearman ρ = +0.19 vs rating, n=141, p = 0.02 | **Agrees**, weakly |

**Model comparison (same ratings):** names only 3.5 produced were rated good 88% (n=49) vs 60% for 3.1-only names (n=97); among names with neither flag, 94% (n=33) vs 59% (n=61). One rater, one run per model — strong evidence, not proof.

**Tooling note:** `cmd/ratings analyze` at this point compared the share rated *bad*; with only 4 bad ratings that test had no power and wrongly reported typo and common-word as "does not agree". The figures above use the share rated good (the tool is being changed to match).

**Decision:** keep typo and specificity as quality metrics; treat common-word as an availability proxy. Next: migrate to `gemini-3.5-flash-lite` optimising for usable names — see `docs/superpowers/specs/2026-09-19-flash-lite-35-migration-design.md`.

---

## Run — 2026-09-19: gemini-3.5-flash-lite migration baseline

**Setup:** current production prompt, `-runs 3 -queries all` (24 queries × 3 runs), thinking `minimal`, temperature 1.0. Snapshots `run-2026-09-19T081006.867-base-31.json` and `run-2026-09-19T081046.433-base-35.json` (not committed).

| Top 10 per query | 3.1 (targets) | 3.5 (start) | 3.5 noise band |
|---|---|---|---|
| Typo | 8.8% | 3.3% | 0.8 pts |
| Common word | 36.5% | **49.6%** | 5.4 pts |
| Mean specificity | 0.104 | 0.101 | 0.011 |
| Names kept / query | 48 | 35 | 2.6 |
| Median latency | 1619 ms | 1641 ms | — |
| Cost / query | $0.00221 | $0.00381 | — |

**Funnel (3.5):** keeps 57% of returned names (3.1: 77%) — losses: cross-brief duplicates 906 (3.1: 418), unknown TLD 577 (269), truncation filter 390 (266).

**Gap:** common-word rate is the only failing target (+13 pts). Typo, kept names (≥ 20), specificity (within band) and cost (≤ 2× = $0.00442) already pass. Tuning rounds target common words and the yield leaks.

### Tuning rounds — 2026-09-19 (3.5, `-runs 3 -queries all`, snapshot `run-2026-09-19T081251.813-r1r2.json`)

| Top 10 per query | Target (3.1) | 3.5 baseline | r1-briefs | r2-uncommon |
|---|---|---|---|---|
| Common word | ≤ 36.5% | 49.6% | **30.1%** | 25.7% |
| Typo | ≤ 8.8% | 3.3% | 6.4% | 8.5% |
| Mean specificity | ≥ 0.104 (± band) | 0.101 | 0.106 | 0.105 |
| Names kept / query | ≥ 20 | 35 | 43.8 | 45.1 |
| Median latency | ~1619 ms | 1641 | 1676 | 1678 |
| Cost / query | ≤ $0.00442 | $0.00381 | $0.00396 | $0.00416 |

- **r1-briefs** (separated creative briefs + TLD rule): meets every target. Cross-brief duplicates fell 906 → 93 and yield rose to 70%, but invented TLDs rose 577 → 863 — the TLD rule did not help; the gain is from de-duplication. **Kept — winner.**
- **r2-uncommon** (+ common-word guidance): common words only 4.4 pts below r1 (inside the 5.4-pt noise band) while typos rose 2.1 pts. **Dropped.**
- Temperature sweep skipped: targets met (spec: stop when all targets are met).

### Final gate 1 — blind rating round 2, 2026-09-19: **FAILED**

100 names (50 per model, each query's top 10, `cmd/ratings sample -n 100 -seed 2 -top 10 -by-model`) from the 3.1 baseline and the tuned 3.5 (`r1-briefs`), rated blind by one rater.

| | 3.1 | tuned 3.5 (r1-briefs) |
|---|---|---|
| Rated good | **82%** (41/50) | **56%** (28/50) — p = 0.008 |
| Common-word names rated good | 100% (15/15) | 87% (13/15) |
| Other names rated good | 77% (23/30) | 48% (15/31) |

The tuned prompt's third brief ("every SLD must be a new word, not one found in a dictionary") pushed 3.5 toward formulaic startup coinages (`zenpulse`, `talentix`, `blendora`, `fundza`, `verdictiq`) that the rater marked okay/bad. None of the deterministic metrics detect this: typo and common-word looked better than 3.1, and specificity did not correlate with ratings (ρ = −0.09, n = 100). The switch was not merged.

### Blind rating round 3 — 2026-09-19: three-way

150 names (50 per arm, each query's top 10, `-top 10 -by-model`, seed 3) rated blind by one rater.

| Arm | Rated good |
|---|---|
| 3.1, current prompt | 74% (37/50) |
| **3.5, current prompt (untuned)** | **84% (42/50)** — +10 pts vs 3.1, p = 0.32 |
| 3.5, `r3-briefs` (rewritten coined-words brief) | 68% (34/50) — −6 pts, p = 0.66 |

Untuned 3.5 matched or beat 3.1 in every rating round (round 1: 88% vs 60%); both tuned prompts rated below untuned. Common-word names were rated good 88% of the time (n = 60), so the prompt changes that reduced common words also removed names the rater liked. Untuned 3.5's trade-off: top-10 common-word rate ~50% vs 3.1's 37% (likely-taken names).

### Availability check — 2026-09-19 (registrar availability API, read-only)

Run 1 top 10 per query (24 queries), 469 unique names.

| | 3.1 | 3.5 (untuned) |
|---|---|---|
| Available at standard price | 38% (3.8 per query) | **21% (2.1 per query)** |
| Premium | 4% | 6% |
| Taken | 58% | 73% |
| Queries with no available name in the top 10 | 0 / 24 | 3 / 24 |
| Available — common-word names | 13% | 8% |
| Available — other names | 54% | 34% |

Two-proportion z = 4.0. Untuned 3.5 wins the blind rating but roughly halves registrable names, because more of its top 10 are very common words (almost all taken). Its non-common names were rated good 83% of the time in round 3 (3.1: 59%), which makes demoting very common words in ranking a promising next step.

### Availability-aware ranking — offline tuning, 2026-09-19

TLD crowding table from DNS delegation of 60 SCOWL probe words per TLD (`make gen-tld-crowding`, resolver 1.1.1.1). Offline re-rank of the 3.5 baseline run 1 with penalty = word penalty + `w × (1 − free rate)`:

| Setup | Top-10 available | Per query | Queries with none |
|---|---|---|---|
| 3.1 today | 38% | 3.8 | 0 |
| 3.5, no penalty | 21% | 2.1 | 3 |
| 3.5, common-word penalty only (0.15) | 30% | 3.0 | 2 |
| **3.5, common 0.20 + SCOWL-35 0.05 + crowding 0.15** | **35.7%** | **3.5** | **1** |
| 3.5, best in grid (0.20 / 0.10 / 0.30) | 36.7% | 3.6 | 1 |

Chosen: smallest penalties within 1 point of the best. The table's per-TLD rank correlation with registrar samples was 0.71 (below the planned 0.8); the outcome check below was used instead.

### Availability-aware ranking — real-output check, 2026-09-19

Top-10 availability from a registrar availability API, per run:

| | Run 1 | Run 2 | Run 3 | Final build (new run) | Mean |
|---|---|---|---|---|---|
| 3.1, current ranking | 37.9% | 36.8% | 41.2% | — | 38.6% (≈3.9 / query) |
| 3.5 + penalty (0.20 / 0.05 / 0.15) | 35.7%* | 34.0% | 27.1% | 28.7% | 31.4% (≈3.1 / query) |

\*Tuning run. Untuned 3.5 was 21% (2.1 / query). Run-to-run spread is about ±5 points, so single-run estimates are unreliable. The penalty removes nearly all common words from the top 10 (2.5% vs ~50%), but 3.5's candidate pool limits availability to ~31% on average — below the 33% gate.

### Blind rating round 4 and pipeline gates — 2026-09-19

Round 4 (seed 4): 50 top-10 names from 3.1 vs 50 from the final 3.5 build (penalty on), 5 prefilled from history.

| | Good | Okay | Bad |
|---|---|---|---|
| gemini-3.1-flash-lite | 40 (80%) | 8 | 2 |
| gemini-3.5-flash-lite + penalty | 40 (80%) | 10 | 0 |

Typo flag agrees with ratings again (44% vs 84% good, p=0.013, n=9 flagged). Quality is at parity: the penalty did not cost rated quality.

Pipeline and load (`cmd/suggestcheck`, CACHE_SIZE=0, all 32 requests; load n=200 c=5):

| | Errors / LLM failures | Top-10 typo | Top-10 common | p50 / p95 / p99 |
|---|---|---|---|---|
| 3.1 | 0 / 0 | 6.9% | 34.4% | 1702 / 2112 / 2457 ms |
| 3.5 final build | 0 / 0 | 7.5% | 5.0% | 1617 / 1789 / 1939 ms |

Both pass. Open trade-off for the merge decision: rated quality equal, latency better, top-10 availability ~31% vs 38.6% (≈0.8 fewer registrable names per query).

### Availability — top 10 and top 20, 2026-09-19

Standard-price availability from a registrar availability API (read-only; premium-priced names count as unavailable). 3.1 and untuned 3.5 are the three baseline runs; "3.5 + penalty" is those three 3.5 runs re-ranked with the penalty plus one fresh run of the final build. 20 is the API's default `count`.

| | Top 10 available | Per query | Top 20 available | Per query |
|---|---|---|---|---|
| 3.1 (runs: 37.9 / 36.8 / 41.0%; top 20: 43.4 / 43.5 / 46.3%) | **38.6%** | 3.9 | **44.4%** | 8.9 |
| 3.5 untuned | 20.7% | 1.8 | 24.2% | 4.1 |
| 3.5 + penalty (runs: 35.4 / 33.3 / 26.7 / 28.7%; top 20: 29.6 / 31.9 / 26.0 / 26.2%) | **31.0%** | 3.1 | **28.4%** | 5.7 |

The gap is wider in the top 20 (−16 pts, ≈3.2 fewer registrable names per query) than in the top 10 (−7.6 pts). The penalty helps less further down the list because 3.5's deeper candidates are also mostly dictionary words.

**Why 3.5 is less available.** TLDs are not the cause: both models put 2–3% of their top 10 on crowded TLDs (free rate < 0.4). The difference is the kind of name (top 10, SCOWL level of the SLD):

| Kind of name | 3.1 share | 3.1 available | 3.5 + penalty share | 3.5 + penalty available |
|---|---|---|---|---|
| Not a dictionary word | 29% | 75% | ~10–30%* | 57% |
| Rarer word (SCOWL 40–70) | 12% | 38% | 21% | 24% |
| SCOWL 35 | 23% | 38% | 47% | 32% |
| Very common (SCOWL ≤ 20) | 37% | 22% | 2% | 0% |

\*20% of 3.5's top-10 SLDs were outside the cached level lookup; the share is between 10% and 30%.

The penalty removed very common words as designed, but 3.5 fills those slots with SCOWL-35 words, which are also mostly taken. 3.1 coins more names, and coined names are usually free. Across all rating rounds, names made of two real words (`duskbrew`, `nightcap`) were rated good 93% of the time (14/15), dictionary words 88% (278/315), and other coinages 38% (23/61) — which is why both earlier "coin new words" prompts lowered ratings. Follow-up: steer 3.5 toward two-word compounds without suffix coinages (tracked in issue #4).

### DNS availability metric (`make eval -avail`) — calibration, 2026-09-19

Issue #4, step 1. `-avail` checks each query's top 20 names for an NS delegation (`internal/dnscheck`, resolver 1.1.1.1) and reports the share with none. Calibrated with `-rescore -avail` on three snapshots that already have registrar results:

| Snapshot | DNS free, top 10 / top 20 | Registrar standard-price available, top 10 / top 20 | Offset |
|---|---|---|---|
| 3.1 baseline (3 runs) | 47.2% / 52.8% | 38.6% / 44.4% | +8.6 / +8.4 |
| 3.5 untuned (3 runs) | 33.4% / 36.2% | 20.7% / 24.2% | +12.7 / +12.0 |
| 3.5 final build (1 run) | 40.8% / 38.8% | 28.7% / 26.2% | +12.1 / +12.6 |

Per name, DNS agreed with the registrar on 91.6% (3.1), 86.1% and 87.5% (3.5) of names. Every disagreement was DNS saying "free" for a name that is premium-priced or registered without nameservers; DNS never called an available name taken. 3.5's names are premium more often (7.3% vs 3.2%, mostly dictionary words on newer TLDs), so DNS flatters 3.5 by about 4 points relative to 3.1.

**Use:** screen prompt variants with `-avail`, and treat a 3.5 variant as a candidate only if it clears 3.1's DNS figures by that margin — about **≥ 51% top 10 and ≥ 57% top 20** (3.1's registrar numbers plus 3.5's offset). Confirm candidates with a registrar check; if a variant produces fewer dictionary words, its offset should shrink toward 3.1's, which the registrar check will show.
