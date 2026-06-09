#!/usr/bin/env bash
# Explore the TLD registry — no API key needed, these are purely in-memory endpoints.
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
JQ="${JQ:-jq}"

hr() { echo; echo "─────────────────────────────────────────"; echo "$1"; echo "─────────────────────────────────────────"; }

# ── Categories overview ───────────────────────────────────────────────────────
hr "All TLD categories (name + count)"
curl -sf "$BASE/tlds/categories" \
  | $JQ '.categories[] | "\(.name): \(.count) TLDs"'

# ── Browse a specific category ────────────────────────────────────────────────
hr "Classic TLDs"
curl -sf "$BASE/tlds/categories" \
  | $JQ '.categories[] | select(.name=="classic") | .tlds'

hr "Tech TLDs"
curl -sf "$BASE/tlds/categories" \
  | $JQ '.categories[] | select(.name=="tech") | .tlds'

hr "First 30 Identity Digital TLDs"
curl -sf "$BASE/tlds/categories" \
  | $JQ '.categories[] | select(.name=="identity_digital") | .tlds[:30]'

# ── Health and config ─────────────────────────────────────────────────────────
hr "Health"
curl -sf "$BASE/health" | $JQ .

hr "Config (LLM + algo)"
curl -sf "$BASE/config" | $JQ '{llm, algo: {enabled: .algo.enabled, active: .algo.active_generators}}'

echo
echo "Done."
