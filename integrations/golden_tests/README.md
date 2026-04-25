# Golden Tests

The mock golden package was retired (it covered the now-removed
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

### Drift gate

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

## Maintainer runbook — keeping the drift gate honest

This section is the operational playbook that turns `baseline_committed.json`
from the empty-placeholder state into a meaningful regression
alarm. **Total maintainer effort: ~3 manual PRs over ~3 months**; everything
else runs on the weekly cron unattended.

### Phase 0 — One-time setup (~15 min, do this first) ⚡

Without an API key configured, the cron runs forever skip and no data is
ever collected. This is the only step that **blocks the whole pipeline**.

- [ ] In GitHub `Settings → Secrets and variables → Actions → Secrets`,
      add **one** of `VV_LLM_API_KEY`, `AI_API_KEY`, `OPENAI_API_KEY`,
      `ANTHROPIC_API_KEY` (whichever matches the LLM you want the cron to
      hit).
- [ ] (Optional) In `Settings → Variables`, add `VV_LLM_MODEL`,
      `VV_LLM_PROVIDER`, `VV_LLM_BASE_URL` if the cron should target a
      non-default model/provider.
- [ ] Confirm `.github/workflows/golden-real-llm.yml` is on `main` —
      it carries the cron schedule already; no edit needed.
- [ ] (Optional sanity) Trigger one manual run via `Actions → golden-real-llm
      → Run workflow` and verify it reaches the "Upload baseline artifact"
      step instead of skipping.

After this the workflow runs every **Monday 03:00 UTC** automatically.

### Phase 1 — Collect P50 baseline (~30 min, do once at week 4–8)

Goal: replace the five `0` placeholders in `baseline_committed.json` with
real per-case medians, so the drift gate starts catching meaningful
regressions instead of being a no-op.

Trigger condition: **4 or more successful weekly runs accumulated** under
`Actions → golden-real-llm → All workflow runs`. With four data points
per case the P50 is stable enough to set a ±50% window around.

Steps:

- [ ] From the Actions UI, download the last 4–8 `golden-real-llm-baseline-*`
      artifacts (each contains one `baseline.json`).
- [ ] Compute the per-case median for `latency_ms` and `total_tokens`.
      Quick way with `jq` against, say, four files:
      ```bash
      for f in baseline-*.json; do
        jq -r '.[] | [.case, .latency_ms, .total_tokens] | @tsv' "$f"
      done | sort -k1,1 \
        | awk '{ lat[$1]=lat[$1]" "$2; tok[$1]=tok[$1]" "$3 }
               END { for (c in lat) print c, lat[c], "|", tok[c] }'
      # then take the middle two values per row → average → P50.
      ```
      For 4 samples, P50 = mean of the 2 middle values; for 8 samples,
      same idea; any spreadsheet with `=MEDIAN()` also works.
- [ ] Edit `vv/integrations/golden_tests/real_llm_tests/baseline_committed.json`:
      - replace each case's `latency_ms_p50: 0` and `total_tokens_p50: 0`
        with the medians from the previous step
      - fill `generated_at` (ISO-8601, e.g. `"2026-05-26T03:00:00Z"`)
      - fill `model` to match the value you actually ran against
      - leave `tolerance_pct: 50` (next phase tightens it).
- [ ] Open a PR; CI should pass because the new baselines are by
      construction within ±50% of themselves.
- [ ] Wait for the next weekly cron run after merge and confirm it stays
      green. If it goes red on the very first run after baseline commit,
      the medians were probably unrepresentative — pull more artifacts
      and recompute.

### Phase 2 — Tighten tolerance to ±30% (~5 min, week 8–10)

Goal: make the drift gate reject ~30% latency / token swings, the
threshold where you actually want to be paged about a regression.

Trigger condition: **≥4 cron runs have stayed green** since the Phase 1
baseline merge (i.e. the variance is genuinely below ±50%).

Steps:

- [ ] Edit `baseline_committed.json` and change `"tolerance_pct": 50` to
      `"tolerance_pct": 30`.
- [ ] Open a PR; merge.
- [ ] Watch the next 1–2 cron runs. If a case starts flapping outside
      ±30% but is clearly normal noise (provider latency varies a lot),
      revert this change and stay at 50% for another month before
      retrying.

### Phase 3 — Tighten tolerance to ±20% (~5 min, week 12+)

Same drill as Phase 2, one more notch.

- [ ] Confirm ≥4 consecutive green cron runs at `tolerance_pct: 30`.
- [ ] Edit `baseline_committed.json` → `"tolerance_pct": 20` → PR → merge.
- [ ] At ±20%, a sustained red run is a real signal: the model upgraded,
      a prompt regressed, or LLM provider degradation. Investigate
      before re-baselining.

### Re-baselining (any time)

Replace baseline values + bump `generated_at` + `model` whenever:

- The default `VV_LLM_MODEL` changes (Anthropic 4.6 → 4.7 etc.).
- A canonical prompt is rewritten and the change is intentional, not
  a regression.
- Provider pricing / quota policy shifts the legitimate latency floor.

Effectively a one-step "reset"; tolerance can stay where it is or bounce
back up to 50% for a few weeks of recollection if the model jump is
large.

### What does NOT need maintainer attention

- **Per-week cron success/failure**: GitHub's default notification routes
  cron failures to repo watchers; you don't need to log in and check.
- **Local script `scripts/run-golden-real-llm.sh`**: only used for one-off
  troubleshooting; the cron is the canonical signal source.
- **Mock golden tests**: retired; nothing to maintain there.

### Reading `baseline.json` (artifact, per-run snapshot)

```jsonc
[
  {
    "case": "Greeting_Hello",
    "prompt": "hello",
    "latency_ms": 612,
    "prompt_tokens": 312,
    "completion_tokens": 18,
    "total_tokens": 330,
    "reply_excerpt": "Hello! How can I help you today?"
  },
  // four more entries
]
```

The artifact is per-run snapshot; `baseline_committed.json` is the
long-lived P50 reference checked into the repo and consulted by the
drift gate.
