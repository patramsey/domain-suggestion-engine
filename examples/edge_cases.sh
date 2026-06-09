#!/usr/bin/env bash
# Edge case inputs — verify graceful handling.
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
JQ="${JQ:-jq}"

hr() { echo; echo "─────────────────────────────────────────"; echo "$1"; echo "─────────────────────────────────────────"; }

ok()   { echo "  ✓ $1"; }
fail() { echo "  ✗ $1"; }

# ── All-stopword input → partial: true ───────────────────────────────────────
hr "All-stopword input (should return partial:true, LLM-only)"
resp=$(curl -sf -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{"input": "the and a", "count": 5}')
echo "$resp" | $JQ '{partial, count: (.suggestions | length)}'
[[ $(echo "$resp" | $JQ -r '.partial') == "true" ]] && ok "partial=true" || fail "expected partial=true"

# ── Cache: same request twice should hit cache ────────────────────────────────
hr "Cache (second identical request should have Age header)"
payload='{"input": "photography studio", "count": 5}'
curl -sf -X POST "$BASE/suggest" -H 'Content-Type: application/json' -d "$payload" > /dev/null
age=$(curl -sI -X POST "$BASE/suggest" -H 'Content-Type: application/json' -d "$payload" \
  | grep -i '^age:' | tr -d '[:space:]')
echo "  Age header: ${age:-'(not present — may need identical repeated call)'}"

# ── Ambiguous TLD filter → 400 ────────────────────────────────────────────────
hr "Ambiguous TLD filter (category + list → 400)"
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{"input":"coffee","tld_filter":{"category":"classic","list":["com"]}}')
echo "  HTTP $status"
[[ "$status" == "400" ]] && ok "got 400" || fail "expected 400, got $status"

# ── Unknown TLD → 422 ────────────────────────────────────────────────────────
hr "Unknown TLD in list (→ 422)"
resp=$(curl -s -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{"input":"coffee","tld_filter":{"list":["fakemadeuptld999"]}}')
echo "$resp" | $JQ '{code, details}'
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{"input":"coffee","tld_filter":{"list":["fakemadeuptld999"]}}')
[[ "$status" == "422" ]] && ok "got 422" || fail "expected 422, got $status"

# ── Input too long → 422 ─────────────────────────────────────────────────────
hr "Input too long (>500 chars → 422)"
long=$(python3 -c "print('a'*501)")
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d "{\"input\":\"$long\"}")
echo "  HTTP $status"
[[ "$status" == "422" ]] && ok "got 422" || fail "expected 422, got $status"

# ── Missing input → 400 ──────────────────────────────────────────────────────
hr "Missing input (→ 400)"
resp=$(curl -s -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' \
  -d '{}')
echo "$resp" | $JQ '{code}'
status=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/suggest" \
  -H 'Content-Type: application/json' -d '{}')
[[ "$status" == "400" ]] && ok "got 400" || fail "expected 400, got $status"

echo
echo "Edge case checks complete."
