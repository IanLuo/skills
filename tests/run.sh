#!/usr/bin/env bash
#
# run.sh — test suite for skill-man's scripts.
#
# Runs two things:
#   1. Fixture tests: validate.py over tests/fixtures/, checking each fixture's
#      exit outcome matches its `expected` file (pass/fail).
#   2. Upstream conformance: downloads the official anthropics/skills
#      quick_validate.py (pinned commit) and confirms skill-man's validate.py
#      agrees with it on every fixture, so skill-man cannot silently diverge.
#
# Usage: bash tests/run.sh
# Exit: 0 if all pass, 1 if any fail.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd -P)"
VALIDATE="$REPO_ROOT/skills/skill-man/scripts/validate.py"
FIXTURES="$SCRIPT_DIR/fixtures"

PASS=0
FAIL=0
FAILED_NAMES=()

# ── 1. Fixture tests ──────────────────────────────────────────────────────
echo "── Fixture tests ──────────────────────────────────────────────────────"
for fx in "$FIXTURES"/*/; do
  name="$(basename "$fx")"
  expected="$(cat "$fx/expected")"
  # validate.py exits 0 = pass, non-zero = fail (run on the single fixture dir).
  if python3 "$VALIDATE" "$fx" >/dev/null 2>&1; then
    actual="pass"
  else
    actual="fail"
  fi
  if [ "$actual" = "$expected" ]; then
    printf '  ✓ %-28s (expected %s, got %s)\n' "$name" "$expected" "$actual"
    PASS=$((PASS + 1))
  else
    printf '  ✗ %-28s (expected %s, got %s)\n' "$name" "$expected" "$actual"
    FAIL=$((FAIL + 1))
    FAILED_NAMES+=("$name")
  fi
done

# ── 2. Upstream conformance ──────────────────────────────────────────────
echo ""
echo "── Upstream conformance (vs anthropics/skills quick_validate.py) ───────"
UPSTREAM_PINNED_REF="57546260929473d4e0d1c1bb75297be2fdfa1949"
UPSTREAM_URL="https://raw.githubusercontent.com/anthropics/skills/${UPSTREAM_PINNED_REF}/skills/skill-creator/scripts/quick_validate.py"
UPSTREAM_CACHE="${TMPDIR:-/tmp}/skill-man-quick_validate-${UPSTREAM_PINNED_REF}.py"

if ! command -v curl >/dev/null 2>&1; then
  echo "  ⚠ curl not found — skipping upstream conformance check"
else
  if [ ! -f "$UPSTREAM_CACHE" ]; then
    echo "  fetching upstream quick_validate.py @ ${UPSTREAM_PINNED_REF:0:7}..."
    if ! curl -fsSL "$UPSTREAM_URL" -o "$UPSTREAM_CACHE" 2>/dev/null; then
      echo "  ⚠ could not fetch upstream validator (offline?) — skipping conformance check"
      UPSTREAM_CACHE=""
    fi
  fi
  if [ -n "$UPSTREAM_CACHE" ] && [ -f "$UPSTREAM_CACHE" ]; then
    # skill-man intentionally enforces name==folder-name; upstream quick_validate.py
    # does not. That's the one expected, legitimate divergence — not a regression.
    EXPECTED_DIVERGENCE="name-folder-mismatch"
    CONF_PASS=0
    CONF_FAIL=0
    for fx in "$FIXTURES"/*/; do
      name="$(basename "$fx")"
      # skill-man outcome
      if python3 "$VALIDATE" "$fx" >/dev/null 2>&1; then s_out="pass"; else s_out="fail"; fi
      # upstream outcome (it takes a single skill dir)
      if python3 "$UPSTREAM_CACHE" "$fx" >/dev/null 2>&1; then u_out="pass"; else u_out="fail"; fi
      if [ "$s_out" = "$u_out" ]; then
        printf '  ✓ %-28s skill-man=%s upstream=%s\n' "$name" "$s_out" "$u_out"
        CONF_PASS=$((CONF_PASS + 1))
      elif [ "$name" = "$EXPECTED_DIVERGENCE" ]; then
        printf '  ~ %-28s skill-man=%s upstream=%s  (expected divergence: skill-man enforces name==folder)\n' "$name" "$s_out" "$u_out"
        CONF_PASS=$((CONF_PASS + 1))
      else
        printf '  ✗ %-28s skill-man=%s upstream=%s  (DIVERGENT)\n' "$name" "$s_out" "$u_out"
        CONF_FAIL=$((CONF_FAIL + 1))
        FAIL=$((FAIL + 1))
        FAILED_NAMES+=("conformance:$name")
      fi
    done
    echo "  conformance: $CONF_PASS agree, $CONF_FAIL divergent"
  fi
fi

# ── Summary ───────────────────────────────────────────────────────────────
echo ""
echo "──────────────────────────────────────────────────────────────────────"
echo "Passed: $PASS   Failed: $FAIL"
if [ "$FAIL" -gt 0 ]; then
  echo "Failed: ${FAILED_NAMES[*]}"
  exit 1
fi
echo "All tests passed."
