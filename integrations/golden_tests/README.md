# Golden Tests

As of M7 the mock golden package was retired (it covered the now-removed
classical pipeline). Coverage of the unified Primary path lives in the
`dispatches` unit tests and `dispatches_tests` integration suite. The real
LLM golden remains as the long-running performance baseline.

## Real-LLM baseline

`real_llm_tests/` exercises five canonical cases — `Greeting_Hello`,
`SimpleMath_Calc`, `SimpleRead_ExplainFile`, `SimpleEdit_DelegateToCoder`,
`MultiStepRefactor_Plan` — end-to-end through `setup.New` against whatever
LLM the environment is pointed at, then compares the run to a checked-in
P50 baseline.

### Local

```bash
# from vv/
export VV_LLM_API_KEY=sk-...
bash scripts/run-golden-real-llm.sh
# → integrations/golden_tests/real_llm_tests/baseline.json
```

The script is idempotent and Skip-safe: if no API key is set it exits 0
with a clear message instead of failing.

### CI

`.github/workflows/golden-real-llm.yml` runs the suite weekly (Mondays
03:00 UTC) plus on manual `workflow_dispatch`. Configure the secret +
optional repo variables before enabling cron in earnest:

| Source | Name | Required? |
|--------|------|-----------|
| Secret | `VV_LLM_API_KEY` (or `AI_API_KEY` / `OPENAI_API_KEY` / `ANTHROPIC_API_KEY`) | yes — without one of these the run skips |
| Variable | `VV_LLM_MODEL` | optional, default `gpt-4o-mini` |
| Variable | `VV_LLM_PROVIDER` | optional, default `openai` |
| Variable | `VV_LLM_BASE_URL` | optional, set for non-default endpoints |

Each run uploads `baseline.json` (per-case latency_ms + token counts) as
an artifact retained for 90 days. Diff successive runs to spot drift.

### Drift gate (M7)

`real_llm_tests/baseline_committed.json` carries the **P50 reference**
the test compares each run against. Out-of-window cases (default
`±50%` per case, both for `latency_ms` and `total_tokens`) cause the
workflow to fail.

```jsonc
{
  "version": 1,
  "generated_at": "<ISO-8601>",
  "model": "<llm-model-id>",
  "tolerance_pct": 50,
  "cases": {
    "Greeting_Hello":            {"latency_ms_p50": 612, "total_tokens_p50": 330},
    "SimpleMath_Calc":           {"latency_ms_p50": 0,   "total_tokens_p50": 0},
    // ...
  }
}
```

A `latency_ms_p50: 0` or `total_tokens_p50: 0` entry **disables the gate
for that metric on that case** — useful when the baseline is mid-update
or just committed without data yet.

#### Updating `baseline_committed.json`

1. Trigger a clean weekly cron (or run `scripts/run-golden-real-llm.sh`
   locally) and download `baseline.json` from the artifacts.
2. Take the median of at least 4 successful runs per case for both
   `latency_ms` and `total_tokens`. (If only one run is available, set
   `tolerance_pct` to a wider value while collecting more samples.)
3. Write the medians into `baseline_committed.json` with the matching
   `model` field, commit, and verify the next workflow run is green.
4. Recommended timeline for narrowing the window:
   - week 1–2: `tolerance_pct: 50` (current default)
   - week 4+:  `tolerance_pct: 30` once trend is stable
   - month 2+: `tolerance_pct: 20` for a meaningful regression alert.

The artifact `baseline.json` is the per-run snapshot; `baseline_committed.json`
is the long-lived reference checked into the repo.
