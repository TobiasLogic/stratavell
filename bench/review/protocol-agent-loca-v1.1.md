# Agent test v1.1 on LOCA-bench: frozen protocol

Status: frozen. This file is committed and pushed before the first v1.1 scored run. It amends `protocol-agent-loca-v1.md`, which was stopped after two scored runs (see "Why v1 was amended").

## Question

A free model works through LOCA-bench tasks with a context budget smaller than the context the tasks produce. Does Stratavell change task success and token use, compared with LOCA's own context strategies and a component-matched baseline?

The reader tests (v1 to v4.2) could not answer this: they ask questions about a fixed, generated session, and the harness decides when to retrieve.

## Why v1 was amended

v1 ran two scored runs on 2026-11-27, both on `CanvasArrangeExamS2LEnv` seed 42, before it was stopped:

| run | requests (agent + summarizer) | replies LOCA discarded | minutes | largest prompt (provider) | how it ended | success |
|---|---|---|---|---|---|---|
| v1 trim | 92 + 0 | 66: 40 took over 60 s, 26 stopped at 4,096 tokens | 68.6 | 62,928 | other: a text reply with no tool call | 0 |
| v1 compact | 40 + 2 | 0 | 7.1 | 54,049 | claimed done | 0 |

They showed two flaws:

1. **LOCA discarded every long reply.**
   - LOCA's HTTP client gives up on a request after 60 seconds. This endpoint writes about 60 to 90 tokens a second, so a reply longer than about 4,000 tokens never arrives.
   - LOCA also discards a reply that stops at the output cap, which was 4,096 tokens.
   - In both cases LOCA resends the same request without telling the agent, up to 50 times. In the trim run, 66 of 92 replies were discarded: 40 by the timeout and 26 at the cap. So 72% of the run's requests, and most of its hour, were resends.
   - Every strategy had the same limits, but they doubled the schedule's length and filtered out the model's longer replies.
   - A first v1.1 pilot raised only the output cap, to 16,384. The timeout then discarded 26 of 41 replies, all of which took 67 to 155 seconds. The longest reply received was 3,617 tokens. That pilot was stopped after 31 minutes and is not a result.
2. **The budgets were not equal.**
   - v1 said every strategy gets about 57K provider tokens. That figure came from the pilot task, which has 57 tools.
   - Each strategy counts tokens with its own counter, and the two counters err differently on tool definitions:
     - On this task's first request (149 tools), the provider counted 1.23 times LOCA's `cl100k` count and 0.89 times Stratavell's estimate.
     - LOCA's limit of 59,904 `cl100k` tokens reached 62,928 provider tokens in the trim run.
     - Stratavell's capacity of 63,604 estimated tokens comes to about 58.6K provider tokens.
   - The gap, about 7% here, grows with the number of tools, and it was against Stratavell.

v1.1 also changes the provider, for the same model. OpenRouter's free tier allows 1,000 requests a day, so the 60 runs would have taken 5 to 6 days. NVIDIA's API catalog serves the same model free, with no daily cap. See Model.

The two v1 runs are reported here and are not scored. v1.1 restarts from the first state.

## Benchmark

LOCA-bench (`hkust-nlp/LOCA-bench`, MIT license), commit 8b6fac4, with the patch in `bench/loca/loca.patch`. It is v1's patch plus a longer request timeout:
- **Stratavell mode:**
  - Every message is appended to a Stratavell session.
  - Every model request is Stratavell's assembled context plus its 8 memory tools.
  - Memory-tool calls are answered by Stratavell, and every other tool call by the environment, unchanged.
- **Passive mode:**
  - LOCA manages its own context as usual.
  - Stratavell archives every message and answers 4 retrieval tools: `memory_search`, `memory_get_event`, `memory_expand` and `memory_get_artifact`.
- **MCP servers** are started from preinstalled environments instead of `uv run` and `uvx`.
- **Shared tool cap:** memory-tool calls count toward LOCA's tool-use cap, and every strategy's run ends when the cap is reached.
- **Request timeout:** new in v1.1. LOCA's HTTP client waits up to 1,800 seconds for a reply instead of 60. The endpoint writes about 39 tokens a second, so a full 16,384-token reply takes about 7 minutes.

Run environment, as in `bench/loca/README.md`:
- **Dependencies** are pinned in `bench/loca/requirements-*.txt`, resolved as of the upstream commit date, 2026-04-29.
- **Preflight:** before every run, `preflight.py` starts each MCP server and checks that it lists tools.
- **Sandbox:** every run executes under bubblewrap.
  - The home directory is not visible.
  - No API key enters the sandbox. Requests go through `freeproxy.py`, which adds the key outside the sandbox and forwards only `:free` model ids.
- **Proxy for NVIDIA:**
  - It asks for the reply as a stream and returns it to LOCA as one reply. In testing, NVIDIA closed a non-streamed 16,384-token request after 5.7 minutes without a reply. Streamed, the same request completed in 7 minutes.
  - It sends requests at least 1.6 seconds apart, under NVIDIA's limit of 40 a minute.
  - It retries a rate-limit refusal (HTTP 429) on NVIDIA up to 40 times, waiting 20 to 120 seconds. It never sends a request to another provider.
- **Stratavell:** one `stratavell serve` from this commit, with a fresh database. Its summarizer is the agent model, through the same proxy. The policy is the default except for the window.
- **Runs** go one at a time, in the fixed order.

## States

- **The 15 states** are those of v1: the first state listed for each of LOCA's 15 task types in `final_64k_set_config.json` (sha256 `67336686488ea40bd4f385b5f89c826168fda5ec5f8fd9dd5454f7b1fb2d75a8`). Each has seed 42 and an environment description length of 64K. They are in `bench/loca/config-v1-64k.json`.
- **Why 64K:** 128K was piloted, and no strategy could finish the piloted task with this model. The LOCA paper reports 25.3% to 45.3% success at 64K for open-weight models with their full windows, and 8.0% to 20.0% at 128K.
- **Run order** is v1's, in `bench/loca/order-v1.json`:
  - state by state
  - the order of the four strategies rotates by one position from each state to the next

## Budget

Every strategy gets the same prompt budget, counted in provider tokens, on every state:
- **Target:** at most 57,000 provider prompt tokens per request.
- **Output cap:** 16,384 tokens for every strategy, with the 1,800-second request timeout.
- **Enforcement:** each strategy keeps enforcing the budget with its own counter, but its limit is set per state so that the limit corresponds to 57,000 provider tokens.

How the per-state limits were set (`bench/loca/dump_tools.py`, `measure_calib.py`, `calibrate.py`):
1. For each state, the first request was built in three forms:
   - LOCA's: the task prompt and the environment's tools
   - passive mode's: the same plus the 4 retrieval tools
   - Stratavell's: its assembled first request, with its 8 memory tools
2. Each form was sent once with a 1-token output cap. The provider's prompt count was recorded next to the strategy's own count.
3. Growth from that first request was converted at the pilot's measured rates:
   - **LOCA's counter:** 0.96 provider tokens per `cl100k` token of messages. The two v1 runs measured 0.964 and 0.955.
   - **Stratavell's counter:** 0.95 provider tokens per estimated token. The 8K and 128K pilots measured 0.968 and 0.931.
4. The resulting limits:
   - **LOCA's input limit:** first-request count + (57,000 − first-request provider count) / 0.96. `--max-context-size` is that limit plus 16,384, because LOCA reserves the output cap from it.
   - **Compact's summary threshold:** `--reset-size` is 85% of the input limit. In v1 it was 51,200 of 59,904, or 85.5%.
   - **Stratavell's capacity:** first-request estimate + (57,000 − first-request provider count) / 0.95. The window is the smallest one whose capacity reaches it, with Stratavell's default safety margin and overhead.

The measurements were made through OpenRouter, before the provider change, and are in `bench/loca/calib-v1.1.json`. The same 45 requests sent to NVIDIA were counted identically for every state and form (`bench/loca/calib-v1.1-nvidia.json`), so the limits in `bench/loca/budget-v1.1.json` apply unchanged:

| state | tools | LOCA first request: `cl100k` / provider | Stratavell first request: estimate / provider | LOCA input limit (`--max-context-size`) | compact `--reset-size` | passive input limit (`--max-context-size`, `--reset-size`) | Stratavell capacity (window) |
|---|---|---|---|---|---|---|---|
| CanvasArrangeExamS2LEnv | 149 | 21,243 / 26,097 | 30,823 / 27,446 | 53,434 (69,818) | 45,419 | 53,272 (69,656, 45,281) | 61,932 (80,616) |
| CanvasListTestS2LEnv | 94 | 13,829 / 16,732 | 20,809 / 18,062 | 55,775 (72,159) | 47,409 | 55,613 (71,997, 47,271) | 61,796 (80,480) |
| CourseAssistantS2LEnv | 75 | 11,541 / 14,200 | 17,436 / 15,530 | 56,124 (72,508) | 47,705 | 55,962 (72,346, 47,568) | 61,089 (79,773) |
| ABTestingS2LEnv | 57 | 7,538 / 9,072 | 11,910 / 10,412 | 57,463 (73,847) | 48,844 | 57,301 (73,685, 48,706) | 60,950 (79,634) |
| AcademicWarningS2LEnv | 57 | 7,446 / 8,983 | 11,787 / 10,315 | 57,464 (73,848) | 48,844 | 57,302 (73,686, 48,707) | 60,929 (79,613) |
| ApplyPhDEmailS2LEnv | 67 | 10,748 / 12,436 | 16,131 / 13,748 | 57,169 (73,553) | 48,594 | 57,006 (73,390, 48,455) | 61,659 (80,343) |
| MachineOperatingS2LEnv | 82 | 11,750 / 14,571 | 17,652 / 15,900 | 55,947 (72,331) | 47,555 | 55,785 (72,169, 47,417) | 60,915 (79,599) |
| SetConfCrDdlS2LEnv | 53 | 7,669 / 9,246 | 12,240 / 10,535 | 57,413 (73,797) | 48,801 | 57,251 (73,635, 48,663) | 61,151 (79,835) |
| ExcelMarketResearchS2LEnv | 45 | 8,560 / 10,538 | 13,437 / 11,862 | 56,958 (73,342) | 48,414 | 56,795 (73,179, 48,276) | 60,951 (79,635) |
| FilterLowSellingProductsS2LEnv | 103 | 14,773 / 17,806 | 22,285 / 19,152 | 55,600 (71,984) | 47,260 | 55,438 (71,822, 47,122) | 62,125 (80,809) |
| UpdateMaterialInventoryS2LEnv | 90 | 14,196 / 17,010 | 21,499 / 18,340 | 55,852 (72,236) | 47,474 | 55,691 (72,075, 47,337) | 62,194 (80,878) |
| WoocommerceNewWelcomeS2LEnv | 142 | 18,251 / 22,127 | 26,857 / 23,457 | 54,577 (70,961) | 46,390 | 54,415 (70,799, 46,253) | 62,165 (80,849) |
| WoocommerceStockAlertS2LEnv | 118 | 16,993 / 20,482 | 25,333 / 21,803 | 55,033 (71,417) | 46,778 | 54,871 (71,255, 46,640) | 62,382 (81,066) |
| NhlB2bAnalysisS2LEnv | 35 | 6,646 / 7,796 | 10,792 / 9,124 | 57,900 (74,284) | 49,215 | 57,738 (74,122, 49,077) | 61,188 (79,872) |
| PayableInvoiceCheckerS2LEnv | 67 | 9,511 / 11,078 | 14,362 / 12,406 | 57,346 (73,730) | 48,744 | 57,184 (73,568, 48,606) | 61,303 (79,987) |

By the same conversion, v1's single limits put LOCA's strategies at 58.9K to 63.2K provider tokens and Stratavell at 58.2K to 59.6K. Stratavell's ceiling was the lower one on 12 of the 15 states, by up to 4.6K (on Canvas). The trim run's largest prompt, 62,928, is close to the predicted 63,212 for Canvas.

The provider-counted prompt tokens of every request are recorded. The largest per run is reported, so the budget each strategy actually reached can be checked.

## Strategies

All strategies use the same model, prompt, tools, budget and tool cap. Common flags: `-s react --max-tool-uses 100 --max-tokens 16384`. The temperature is 1.0, fixed by LOCA. Per-state numbers come from `budget-v1.1.json`.

1. **trim:** `--max-context-size` from the table.
   - LOCA drops the oldest assistant turns and their results to fit.
   - If one tool output is larger than the budget, it removes everything and the run ends.
   - This is the standard control.
2. **compact:** `--context-summary --reset-size R --max-context-size M`. The agent model summarizes the history at 85% of the input limit.
3. **compact + archive:** strategy 2 with `STRATAVELL_MODE=passive`, using the passive-mode limits. This is the component-matched baseline: the same retrieval, with LOCA's context management.
4. **stratavell:** `STRATAVELL_WINDOW` from the table. Stratavell's assembly with the default policy, plus its 8 memory tools.

A run that reaches the tool cap ends and counts as a failure.

## Model

`nvidia/nemotron-3-ultra-550b-a55b` on NVIDIA's API catalog (build.nvidia.com), free tier. LOCA names it with the `:free` suffix, which the proxy removes. v1 used OpenRouter's free endpoint for the same model.

- **Same model on both:** checked before freezing (see Pilot).
  - Prompt counts were identical on all 45 calibration requests.
  - The same first request gave replies of similar length and the same first tool call on both.
  - The same mid-run history gave the same 50-token reply, word for word, on both.
- **One provider:** every scored run uses NVIDIA. The proxy never switches provider. If NVIDIA becomes unavailable, the operator stops the schedule until it is back. Runs voided by an outage are kept, reported and count toward the two reruns. A change of provider would be a new amendment, made before any further scored run.
- Run dates are recorded, because a free endpoint can change behind the same id.

## Metrics

- **Headline:** task success from LOCA's programmatic checker (1 or 0), per strategy, paired by state.
- **Per run:**
  - agent and summarizer requests
  - replies cut off at the output cap, which LOCA resends
  - provider prompt and completion tokens, as a sum, per agent request, and as the largest single request
  - compaction events: LOCA's trim and summary events, and Stratavell's assembly reports
  - memory-tool calls, steps and wall time
  - how the run ended: claimed done, tool cap, context lost or other
  - machine load
- **Estimated cost:** tokens priced at a paid endpoint's listed price for the same model on the run date. It is labeled as an estimate, because the free tier does not bill. The prompt tokens NVIDIA reports as cached are recorded and shown next to it.

## Validity rules

- **Void runs:** a run is void when:
  - the preflight fails
  - the run exits nonzero or times out after 6 hours. v1's limit was 3 hours, but with replies of up to 16,384 tokens a single request can take several minutes.
  - LOCA's evaluation does not complete
  - an API error ends the run
  - the proxy's daily cap is reached during the run, or it returns a rate-limit refusal to LOCA after its 40 retries

  `schedule.py` reruns a void run at most twice. Void runs are kept and their counts are reported. A state whose run stays void after two reruns is reported, and excluded from the paired comparisons for that strategy pair.
- **Daily quota:** NVIDIA has no daily cap. The proxy's own cap is set to 20,000 requests a day, so it should not bind. The scheduler still starts a run only when at least 160 of that day's requests remain.
- **Contamination:** a run is flagged when any tool call's arguments name `groundtruth_workspace`, `local_db` or the task's `files/` directory. LOCA's Python tool can read those paths. Flagged runs count as failures in the primary analysis, and are listed.
- **Budget check:** for each strategy, the largest provider prompt of each run is reported next to the 57,000 target. A strategy whose largest prompt exceeds the target by more than 5% on any state is named in the results, with the states.

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

Unchanged from v1, and reported as met or not met:
- **E1:** stratavell succeeds on at least as many states as trim, and at least as many as compact, minus 1 in each case.
- **E2:** stratavell's median provider prompt tokens per agent request are lower than compact's and lower than compact + archive's. This is the median over states of each state's per-request mean, with paired differences.
- **E3:** stratavell succeeds on at least as many states as compact + archive, minus 1.
- **E4:** at least one trim run ends with context lost: an output larger than the budget removes the whole history. This happened in the 128K pilot.

## Scale and budget

- **Size:** 15 states x 4 strategies = 60 scored runs.
- **Estimate:** the four NVIDIA pilot runs took 4 to 8 minutes and 20 to 52 requests each. Other tasks may need more steps, and a run can use up to 100 tool calls. So the 60 runs should take about 6 to 24 hours and 2,000 to 6,000 requests.
- **Cost:** $0, on free models only.
- **What this is:** a first measurement, not a definitive comparison. A definitive one would need all 75 states and repeated runs.

## Pilot (not results)

v1's pilot is in `protocol-agent-loca-v1.md`. The v1.1 pilot ran on 2026-11-27 on `CanvasArrangeExamS2LEnv` seed 123, which is not a scored state. Canvas was chosen because it has the most tools (149), and so the largest counter gap. Each run used this protocol's flags and Canvas limits:

| run | provider | requests (agent + summarizer) | replies LOCA discarded | minutes | largest prompt (provider) | how it ended | success |
|---|---|---|---|---|---|---|---|
| trim, first try | OpenRouter | 41 + 0 | 26, all by the 60-second timeout | 31.7 | 55,535 | stopped by the operator | (none) |
| trim | NVIDIA | 52 + 0 | 0 | 7.7 | 58,385 | claimed done | 0 |
| compact | NVIDIA | 19 + 3 | 0 | 4.8 | 49,281 | claimed done | 0 |
| compact + archive | NVIDIA | 24 + 10 | 0 | 5.3 | 49,993 | claimed done; no archive tool used | 0 |
| stratavell | NVIDIA | 19 + 1 | 0 | 4.0 | 39,824 | claimed done; 6 memory-tool calls | 0 |

- **No reply hit the output cap** in the NVIDIA runs, and no request failed.
- **The trim run's largest prompt was 58,385**, 2.4% over the 57,000 target, within the 5% of the budget check.
- **Stratavell stayed below the budget.** Its largest estimate was 44,223 of its 61,932 capacity, and its largest provider count was 0.90 of that, consistent with the calibration.
- **Compact + archive** used no retrieval tool, as in v1's 128K pilot.

**Endpoint comparison,** done before freezing:
- **First request:** the Canvas first request, sent 3 times to each endpoint. Replies were 122 to 228 tokens on NVIDIA and 130 to 212 on OpenRouter, all calling `canvas_canvas_list_courses`.
- **Mid-run history:** step 10 of the NVIDIA trim pilot (48,207 prompt tokens), sent twice to each. All 4 replies were the same 50 tokens, word for word.
- **Reply lengths:** in v1's trim run, 66 of 92 requests were resends of discarded long replies. So its per-request reply lengths (median 4,096 tokens) mostly measure those resends, not a difference between endpoints. The four NVIDIA pilots' median reply was 53 to 172 tokens.
- **Speed:** a streamed 16,384-token reply took 7 minutes 5 seconds on NVIDIA, about 39 tokens a second. The pilots' agent requests took a median of 4.0 to 5.8 seconds.

The pilot also changed the harness:
- **Proxy:** it gained the NVIDIA mode.
- **Request timeout:** raised from 600 to 1,800 seconds.
- **Run order:** unchanged; runs go one at a time.
