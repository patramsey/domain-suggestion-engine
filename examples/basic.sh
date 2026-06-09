#!/usr/bin/env bash
# Basic usage examples — assumes server is running on localhost:8080
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
JQ="${JQ:-jq}"

hr() { echo; echo "─────────────────────────────────────────"; echo "$1"; echo "─────────────────────────────────────────"; }

# ── 1. Keywords ──────────────────────────────────────────────────────────────
hr "1. Keywords: 'coffee shop brooklyn'"
curl -sf -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{"input": "coffee shop brooklyn", "count": 10}' \
  | $JQ '.suggestions[] | "\(.score | . * 100 | round / 100)  \(.name)  [\(.source)]"'

# ── 2. Existing domain as input ───────────────────────────────────────────────
hr "2. Existing domain: 'patspizza.com'"
curl -sf -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{"input": "patspizza.com", "count": 10}' \
  | $JQ '.suggestions[] | "\(.score | . * 100 | round / 100)  \(.name)  [\(.source)]"'

# ── 3. Business description ───────────────────────────────────────────────────
hr "3. Description: 'handmade artisan candles'"
curl -sf -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{"input": "handmade artisan candles", "count": 10}' \
  | $JQ '.suggestions[] | "\(.score | . * 100 | round / 100)  \(.name)  [\(.source)]"'

# ── 4. Classic TLDs only ──────────────────────────────────────────────────────
hr "4. Classic TLDs only (.com, .net, .org, .info, .biz)"
curl -sf -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{"input": "coffee", "count": 8, "tld_filter": {"category": "classic"}}' \
  | $JQ '.suggestions[].name'

# ── 5. Identity Digital TLDs ──────────────────────────────────────────────────
hr "5. Identity Digital TLDs (word TLDs like .pizza, .cafe, .studio)"
curl -sf -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{"input": "pizza restaurant", "count": 10, "tld_filter": {"category": "identity_digital"}}' \
  | $JQ '.suggestions[] | "\(.name)  [\(.source)]"'

# ── 6. Explicit TLD list ──────────────────────────────────────────────────────
hr "6. Explicit TLD list: [coffee, cafe, com, io]"
curl -sf -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{"input": "coffee", "count": 8, "tld_filter": {"list": ["coffee", "cafe", "com", "io"]}}' \
  | $JQ '.suggestions[].name'

# ── 7. Debug mode: show which TLDs and generators were used ───────────────────
hr "7. Debug mode"
curl -sf -X POST "$BASE/suggest?debug=true" \
  -H 'Content-Type: application/json' \
  -d '{"input": "studio photography", "count": 5}' \
  | $JQ '{active_generators, tld_count: (.tlds_used | length), top_suggestions: [.suggestions[:3][].name]}'

echo
echo "Done."
