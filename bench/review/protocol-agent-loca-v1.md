# Agent test v1 on LOCA-bench: frozen protocol

Status: superseded by `protocol-agent-loca-v1.1.md` on 2026-11-27, after two scored runs. They showed that LOCA discarded most replies in one run, because of its 60-second request timeout and the 4,096-token output cap, and that the strategies' budgets were not equal in provider tokens. The text below is unchanged from when it was frozen. The pilot runs at the end tested the harness and are not results.

## Question

A free model works through LOCA-bench tasks with a context budget smaller than the context the tasks produce. Does Stratavell change task success and token use, compared with LOCA's own context strategies and a component-matched baseline?

The reader tests (v1 to v4.2) could not answer this: they ask questions about a fixed, generated session, and the harness decides when to retrieve.

## Benchmark

LOCA-bench (`hkust-nlp/LOCA-bench`, MIT license), commit 8b6fac4, with the patch in `bench/loca/loca.patch`:
- **Stratavell mode:**
  - Every message is appended to a Stratavell session.
  - Every model request is Stratavell's assembled context plus its 8 memory tools.
  - Memory-tool calls are answered by Stratavell, and every other tool call by the environment, unchanged.
- **Passive mode:**
  - LOCA manages its own context as usual.
  - Stratavell archives every message and answers 4 retrieval tools: `memory_search`, `memory_get_event`, `memory_expand` and `memory_get_artifact`.
- **MCP servers** are started from preinstalled environments instead of `uv run` and `uvx`. Unpatched, three servers failed to start inside the sandbox, and LOCA dropped their tools without an error (see Pilot).
- **Shared tool cap:** memory-tool calls count toward LOCA's tool-use cap, so every strategy gets the same number of tool calls. Every strategy's run ends when the cap is reached.

Run environment, as in `bench/loca/README.md`:
- **Dependencies** are pinned in `bench/loca/requirements-*.txt`, resolved as of the upstream commit date, 2026-04-29.
- **Preflight:** before every run, `preflight.py` starts each MCP server and checks that it lists tools.
- **Sandbox:** every run executes under bubblewrap.
  - The home directory is not visible.
  - No API key enters the sandbox. Requests go through `freeproxy.py`, which adds the key, forwards only `:free` model ids and stops at 900 requests per UTC day.
- **Stratavell:** one `stratavell serve` from this commit, with a fresh database. Its summarizer is the agent model, through the same proxy. The policy is the default except for the window.

## States

- **The 15 states** are the first state listed for each of LOCA's 15 task types in `final_64k_set_config.json` (sha256 `67336686488ea40bd4f385b5f89c826168fda5ec5f8fd9dd5454f7b1fb2d75a8`). Each has seed 42 and an environment description length of 64K. They are copied to `bench/loca/config-v1-64k.json`.
- **Why 64K:** 128K was piloted, and no strategy could finish the piloted task with this model. The LOCA paper reports 25.3% to 45.3% success at 64K for open-weight models with their full windows, and 8.0% to 20.0% at 128K.
- **Run order** is fixed in `bench/loca/order-v1.json`:
  - state by state
  - the order of the four strategies rotates by one position from each state to the next, so time of day and endpoint drift do not line up with one strategy

## Budget

- **Size:** about 57K provider-counted prompt tokens for every strategy, with 4,096 output tokens.
- **Tool definitions** take 6.3K to 21K of it, depending on the task type (`cl100k`, measured by the preflight).
- **Calibration:** each strategy enforces the budget with its own counter. The pilot measured both counters against the provider's:
  - The provider counted 0.95 to 1.16 times LOCA's `cl100k` estimate (median 1.07). LOCA's strategies use `--max-context-size 64000`, and at that limit the provider counted about 57K.
  - The provider counted 0.87 to 0.95 times Stratavell's estimate (median 0.90). Stratavell uses a 70,000-token window: a capacity of 63,604 estimated tokens, about 57K provider tokens.
- The provider-reported prompt tokens of every request are recorded, and the largest per strategy is reported.

## Strategies

All strategies use the same model, prompt, tools, budget and tool cap. Common flags: `-s react --max-tool-uses 100 --max-tokens 4096`. The temperature is 1.0, fixed by LOCA.

1. **trim:** `--max-context-size 64000`.
   - LOCA drops the oldest assistant turns and their results to fit.
   - If one tool output is larger than the budget, it removes everything and the run ends.
   - This is the standard control.
2. **compact:** `--context-summary --reset-size 51200 --max-context-size 64000`. The agent model summarizes the history at 80% of the budget.
3. **compact + archive:** strategy 2, with `STRATAVELL_MODE=passive`. This is the component-matched baseline: the same retrieval, with LOCA's context management.
4. **stratavell:** `STRATAVELL_WINDOW=70000`. Stratavell's assembly with the default policy, plus its 8 memory tools.

A run that reaches the tool cap ends and counts as a failure.

## Model

`nvidia/nemotron-3-ultra-550b-a55b:free`, through OpenRouter. Run dates are recorded, because a free endpoint can change behind the same id.

## Metrics

- **Headline:** task success from LOCA's programmatic checker (1 or 0), per strategy, paired by state.
- **Per run:**
  - agent and summarizer requests
  - provider prompt and completion tokens, as a sum, per agent request, and as the largest single request
  - compaction events: LOCA's trim and summary events, and Stratavell's assembly reports
  - memory-tool calls, steps and wall time
  - how the run ended: claimed done, tool cap, context lost or other
  - machine load
- **Estimated cost:** tokens priced at the paid endpoint's listed price for the same model on the run date. It is labeled as an estimate, because free endpoints do not bill and report no cache use.

## Validity rules

- **Void runs:** a run is void when:
  - the preflight fails
  - the run exits nonzero or times out after 3 hours
  - LOCA's evaluation does not complete
  - an API error ends the run
  - the proxy's daily cap is reached during the run

  `schedule.py` reruns a void run at most twice. Void runs are kept and their counts are reported. A state whose run stays void after two reruns is reported, and excluded from the paired comparisons for that strategy pair.
- **Daily quota:** the scheduler starts a run only when at least 160 of the day's 900 requests remain. Otherwise it waits for the next UTC day.
- **Contamination:** a run is flagged when any tool call's arguments name `groundtruth_workspace`, `local_db` or the task's `files/` directory. LOCA's Python tool can read those paths. Flagged runs count as failures in the primary analysis, and are listed.

## Statistics

With 15 states and one run each at temperature 1, only large differences can be detected. Results are reported mainly descriptively:
- **Success:**
  - success counts per strategy, with the 2 x 2 paired table against stratavell
  - McNemar's exact p-value for each pair
  - a paired bootstrap 95% interval of the success difference
- **Tokens:**
  - provider prompt tokens per agent request and per run
  - paired differences against stratavell, with the median and a sign test
- **Wording:** a difference that is not significant is reported as inconclusive, not as parity.

## Expectations

Stated before the first scored run, and reported as met or not met:
- **E1:** stratavell succeeds on at least as many states as trim, and at least as many as compact, minus 1 in each case.
- **E2:** stratavell's median provider prompt tokens per agent request are lower than compact's and lower than compact + archive's. This is the median over states of each state's per-request mean, with paired differences.
- **E3:** stratavell succeeds on at least as many states as compact + archive, minus 1.
- **E4:** at least one trim run ends with context lost: an output larger than the budget removes the whole history. This happened in the 128K pilot.

## Scale and budget

- **Size:** 15 states x 4 strategies = 60 scored runs.
- **Pilot requests per run** at 128K, agent plus summarizer, mostly bounded by the tool cap:
  - trim: 50
  - compact: 124
  - compact + archive: 153
  - stratavell: 238
- **Estimate:** the cap is now 100 and the tasks are smaller, so about 80 requests per run. That makes about 4,800 requests, or 5 to 6 days at 900 a day.
- **Cost:** $0, on free models only.
- **What this is:** a first measurement, not a definitive comparison. A definitive one would need all 75 states and repeated runs, which means a paid model and an approved budget.

## Pilot (not results)

All runs were on 2026-11-27: `ABTestingS2LEnv` seed 42, Nemotron 3 Ultra free, one run each at temperature 1.0, $0. They tested the harness.

| run | env | requests (agent + summarizer) | minutes | largest prompt (provider) | how it ended | success |
|---|---|---|---|---|---|---|
| pilot 1, trim | 8K | 31 + 0 | 13 | 14.6K | void: 3 MCP servers had not started | (0) |
| p1b, trim | 8K | 7 + 0 | 1.4 | 13.8K | claimed done, empty overall row | 0 |
| p2, stratavell | 8K | 14 + 0 | 3.9 | 15.2K | claimed done | 1 |
| p3, trim | 128K | 50 + 0 | 20.8 | 56.9K | a 1,995-row listing overflowed the budget, and trimming removed the whole history | 0 |
| p3, stratavell | 128K | 187 + 51 | 47.4 | 35.0K | void: integration error at step 187 (see below); 36 memory-tool calls; it queried tables one at a time | (0) |
| p3, compact | 128K | 92 + 32 | 32.0 | 11.8K | claimed done with 5 of 2,000 scenarios | 0 |
| p3, compact + archive | 128K | 119 + 34 | 38.4 | 20.4K | claimed done with an empty file; no archive tool was used | 0 |

The pilot ran with a tool cap of 150.

The p3 stratavell run is void:
- Memory-tool calls did not yet count toward the cap. After 151 environment calls, LOCA's environment stopped executing tools, but the episode went on.
- One call was left without a result, and Stratavell refused to assemble the next request (HTTP 409), which ended the run with an error.

What the pilot changed:
- **MCP server launch** was patched.
- **Memory-tool calls** now count toward the shared tool cap.
- **Ending at the cap:** for every strategy, a run now ends when the cap is reached. Unpatched, LOCA kept going with unanswered tool calls once its cap was reached, which LOCA's default cap of 500 rarely triggers. A test with a cap of 3 ended cleanly with trim and with stratavell.
- **Unanswered calls:** any call left unanswered is closed with an error result before Stratavell assembles.
- **Stratavell's window** is calibrated to the same provider-token budget as LOCA's strategies.
- **The environment size** moved from 128K to 64K, and the tool cap from 150 to 100.
