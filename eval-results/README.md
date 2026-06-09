# Prompt Evaluation Results

Each eval run compares prompt variants across 16 fixed queries. The key metric is **cross-query generic rate** — percentage of suggestions whose SLD appeared in >25% of query result sets. A generic SLD (e.g. `zenith`, `haven`, `solace`) fits any concept equally well and signals poor anchoring.

Run `make eval` to reproduce. Results may vary slightly between runs due to model temperature.

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
