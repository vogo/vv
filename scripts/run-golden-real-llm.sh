#!/usr/bin/env bash
#
# Local driver for the M6 G5 real-LLM golden baseline.
#
# What it does:
#   1. Validates that one of the supported API key env vars is set.
#   2. Runs vv/integrations/golden_tests/real_llm_tests/ against the real
#      configured LLM.
#   3. Writes a JSON artifact to baseline.json next to the test package
#      so historical runs can be diffed offline.
#
# Defaults can be overridden via env:
#   VV_LLM_API_KEY            (or AI_API_KEY / OPENAI_API_KEY / ANTHROPIC_API_KEY)
#   VV_LLM_PROVIDER           default openai
#   VV_LLM_MODEL              default gpt-4o-mini
#   VV_LLM_BASE_URL           optional (custom endpoint)
#   VV_GOLDEN_BASELINE_OUT    output JSON path; default real_llm_tests/baseline.json
#
# Exit 0 on Skip (no key) so this is safe to wire into a `make` target without
# breaking developers who don't have an API key configured.

set -euo pipefail

# Resolve repo paths regardless of where the script is invoked from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VV_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TEST_PKG="./integrations/golden_tests/real_llm_tests/"

# Default output location lives inside the package so it's easy to find.
DEFAULT_OUT="$VV_DIR/integrations/golden_tests/real_llm_tests/baseline.json"
export VV_GOLDEN_BASELINE_OUT="${VV_GOLDEN_BASELINE_OUT:-$DEFAULT_OUT}"

# Friendly key-presence check. Exit 0 + clear message keeps `make`
# integration honest without surprising newcomers with a confusing failure.
have_key=false
for k in VV_LLM_API_KEY AI_API_KEY OPENAI_API_KEY ANTHROPIC_API_KEY; do
    if [[ -n "${!k:-}" ]]; then
        have_key=true
        break
    fi
done

if [[ "$have_key" != "true" ]]; then
    echo "[real-llm-golden] no API key set; skipping (set VV_LLM_API_KEY or one of AI_API_KEY/OPENAI_API_KEY/ANTHROPIC_API_KEY)"
    exit 0
fi

echo "[real-llm-golden] running against ${VV_LLM_MODEL:-gpt-4o-mini} via ${VV_LLM_PROVIDER:-openai}"
echo "[real-llm-golden] baseline output: $VV_GOLDEN_BASELINE_OUT"

cd "$VV_DIR"

# -count=1 disables Go's test cache so a re-run actually contacts the LLM.
# -v keeps the per-case t.Logf lines visible — they are the headline output.
go test "$TEST_PKG" -count=1 -v -timeout 5m

echo "[real-llm-golden] done; artifact written to $VV_GOLDEN_BASELINE_OUT"
