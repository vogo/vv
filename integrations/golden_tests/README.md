# Golden Tests

Two adjacent packages, two different jobs:

| Package | Runs on | LLM | Purpose |
|---------|---------|-----|---------|
| `golden_tests/` | every CI / `make test` | mock | Contract regression — pins dispatcher shape (LLM call counts, sub-agent invocation order) for the five canonical cases. Cheap, deterministic. |
| `real_llm_tests/` | weekly cron + manual | real | Performance baseline — records latency and token usage against a real model. Skipped when no API key is set. |

## Real-LLM baseline (M6 G5)

The real-LLM suite exercises the same five cases (`Greeting_Hello`,
`SimpleMath_Calc`, `SimpleRead_ExplainFile`, `SimpleEdit_DelegateToCoder`,
`MultiStepRefactor_Plan`) end-to-end through `setup.New` against whatever
LLM the environment is pointed at.

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

### Reading the baseline

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
  // ...
]
```

The mock golden suite still owns "did this regress". The real-LLM suite
owns "is the model getting slower / more expensive over time".
