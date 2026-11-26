# Research note 01: APEC and CompactionBench, observation masking, and compaction-friendly benchmarks

Date of reading: 2026-11-26. Scope: three primary sources read in full, plus a discovery pass over related 2025 to 2026 work. This note informs the design of an asynchronous, losslessly retrievable context management engine. It contains no project code.

## 0. Conventions and access status

Every number carries one of three labels.

- **measured**: reported by the source as the output of an experiment or trace analysis. It is still the authors' report; we did not rerun it.
- **claimed**: asserted without an inspectable experiment (analytical models, extrapolations, vendor statements, numbers from abstracts we did not read in full, documentation defaults).
- **verified**: we checked it ourselves against released code or released data.

In tables, the label column or the table caption applies to every number in the row or table.

| Source | What we read | Access |
|---|---|---|
| APEC and CompactionBench | Full 15-page PDF hosted by Microsoft Research | preprints.org returned HTTP 403 to automated clients for every URL tried; the DOI resolves to it. We did not try to get around the block. We read the copy linked from the Microsoft Research publication page instead. |
| The Complexity Trap | Full arXiv HTML (v3), full released repository, one released trajectory archive | All reachable |
| Benchmarks Are Surprisingly Compaction-Friendly | Full arXiv HTML (v2), released repository docs and the compaction source file | All reachable |
| Discovery items | Abstracts or full pages, as noted per item | All URLs listed in Section 6 returned HTTP 200 on 2026-11-26, some after a redirect (noted) |

---

## 1. Source A: CompactionBench and APEC

### 1.1 Bibliographic facts

- **Exact title**: "Benchmarking Long-Horizon Agent Context Compaction with CompactionBench". There is no paper titled "Asynchronous Progressive Compaction". APEC (the PDF section title is "APEC: Asynchronous ProgrEssive Compaction") is the mechanism this paper proposes.
- **Authors**: Banruo Liu (UIUC, work done as an intern at Microsoft Azure Research), Haoran Qiu (Microsoft Azure Research), Íñigo Goiri (Microsoft Azure Research), Jingran Zhou (Microsoft AI), Rodrigo Fonseca (Microsoft Azure Research), Ricardo Bianchini (Microsoft Azure), Fan Lai (UIUC), Esha Choukse (Microsoft Azure Research).
- **Date and venue**: the Microsoft Research page lists it as "November 2026, Preprints.org", DOI 10.20944/preprints202611.0780.v1 (https://www.microsoft.com/en-us/research/publication/benchmarking-long-horizon-agent-context-compaction-with-compactionbench/). The PDF we read (https://www.microsoft.com/en-us/research/wp-content/uploads/2026/10/CompactionBench_preprint.pdf) has a PDF creation date of 2026-10-21 and the header "Preprint." We could not confirm that it matches the preprints.org v1 byte for byte.
- **Status**: preprint, not peer reviewed.
- **License**: we could not see one on the PDF or the Microsoft Research page. The preprints.org page, which would normally show it, was unreachable. Treat it as unknown.
- **Code and benchmark release**: the paper contains no code or data link and makes no statement about a release. A GitHub search found no repository from the authors (as of 2026-11-26). Watch for name collisions: `t46/compactionbench` (MIT, created 2026-06-11, an unrelated "ledger+refetch" benchmark), `ayushnangia/CompactionBench` (no description, created 2026-02-24), and `compactbench` (https://compactbench.github.io/compactbench, a separate conversation-compaction benchmark). None of these is the Microsoft benchmark. **CompactionBench appears unreleased.**

### 1.2 Production trace characterization

All numbers in this subsection are **measured** on proprietary GitHub Copilot traces from Visual Studio and VS Code, June 2026. Nobody outside Microsoft can reproduce them.

- Scale: 13.5M sessions, 3.2M users, 82.4M turns, 760.5M LLM calls, 774.7M tool calls, 44.9T prompt tokens, 39.3B completion tokens. Per session: median 3 user turns (mean 6.1), median 15 LLM calls (mean 56.3), median duration 4.2 min (mean 62.6).
- 7.8% of sessions compact, and those sessions use 44.2% of all tokens.
- Pre-compaction context utilization does not cluster at one value. It peaks near 50%, 65 to 66% (the overall median), 80%, and near 100%, which reflects a mix of per-model trigger rules.
- Median compaction time is about 22% of its turn's execution time; P90 is about 34%.
- Median prompt reduction per event is 72.9% (P25 to P75: 58% to 81%). 6.1% of events drop at least 90% of the prompt.
- Cache hit rate: median 99.0% before compaction, 27.0% on the first call after. The median per-event drop is 57.8 percentage points. 34.3% of events wipe out more than 90 points of hit rate, and 21% almost empty the cache.

A companion paper by most of the same authors, "Agentic Coding in the Wild" (https://arxiv.org/abs/2608.00101), reports these cache hit rates (**measured**): about 90% within a turn, 52% across turn boundaries, and 6% after a model switch. The last figure matters if a different model writes the summary.

### 1.3 What CompactionBench is and what it measures

- **Motivation (measured)**: the authors ran more than ten coding benchmarks with modern models and found they rarely compact (their Table 2). LongSWE-Bench at 256K triggers 1 compaction. SWE-bench, SWE-EVO, Terminal-Bench 2.1 and 4.0, ViBench, DeepSWE, SWE-Marathon, LHTB, LoCoBench and LoCoBench-Agent trigger none. They name three patterns that fail to stress compaction: front-loaded context, single-turn autonomous runs, and passive verifier-driven continuation, where each new agentic turn adds less context.
- **Construction**: each task is a release transition from SWE-EVO. Its changelog is split into bullet-level requirements, ordered by PR number where one exists, and fed one at a time as successive user requests into one persistent OpenClaude session on one evolving repository. Follow-up requests are fixed in advance, so they do not depend on the trajectory. The authors stripped git history and tags from the images after they caught agents reading the target release out of git history.
- **Task inventory (measured)**: 8 release transitions across 4 repositories, 315 changelog items in total. Conan 2.0.2 to 2.0.3 (22 items) and 2.0.14 to 2.0.15 (43). Dask 2022.9.2 to 2022.10.0 (25), 2023.3.2 to 2023.4.0 (39), and 2023.6.0 to 2023.6.1 (21). DVC 1.0.0a1 to 1.0.0a2 (34) and 2.8.1 to 2.8.2 (30). Pydantic v2.6.0b1 to v2.6.0 (101).
- **Metrics**: (M1) task accuracy: the final cumulative source patch is run against the repository tests, excluding agent edits to tests, and accuracy is averaged across tasks weighted by number of tests. (M2) total token cost. (M3) task completion time. Secondary: token counts, cached tokens, compactions per trial, compaction cost and time, the share of compaction time overlapped with foreground work, and cache hit rate on the last foreground call before and the first call after each compaction.
- **Sample size (measured)**: 8 tasks × 5 trials per method. No confidence intervals or standard deviations are reported anywhere.
- **Models**: GPT-5.6 Sol at extra-high reasoning effort for the main table; GPT-5.4 for a sensitivity table (trial count not stated). Context: 400K tokens with 128K reserved for output, compaction triggered at (400K minus 128K) × ρ. A 1M setting appears only as a single example trajectory plot.
- **Harness**: OpenClaude (https://github.com/Gitlawb/openclaude, which now redirects to https://github.com/Twigpine/openclaude) driven by the Pier execution framework through a custom multi-turn adapter.

### 1.4 The APEC mechanism

The paper's Algorithms 1 to 3 give it precisely. Restated:

- **Budgets** (defaults, **claimed** as configuration): summary output cap S_cap = 20K tokens. Combined budget for one new summary plus its raw tail: B = 40K. Raw tail H = 40K minus S. The text says H is "at most 20K", which is backwards, since S at most 20K implies H at least 20K; this is a minor inconsistency in the paper. Trigger ratios: τ_soft = 50%, τ_hard = 90% of the usable budget, i.e. 136K and about 245K tokens in their setup. The cap on accumulated summaries equals τ_soft (136K).
- **Check point**: `AsyncCompactCheck(msg)` runs before every foreground LLM call (Algorithm 1). Nothing interrupts an in-flight foreground call. Summaries land only at call boundaries.
- **Order of operations inside the check**:
  1. If the background work item is READY, append its summary to the prefix list. The new tail becomes `msg[snapEnd+1..]`, so every message produced after the snapshot survives. Clear the work item and return `[sysPrompt, prefix, tail]`.
  2. Otherwise, if tokens ≥ τ_hard, run a synchronous full summary of the whole message list. The prefix becomes that single summary, the tail is the most recent tail within B minus the summary size, and any in-flight background work is **abandoned** (set to NONE). The paper does not say whether that request is cancelled or just ignored, and the cost model charges started summaries whether or not they are used. This is the emergency path, and it also starts a new "epoch".
  3. Otherwise, if tokens ≥ τ_soft, the accumulated prefix is below τ_soft, and no work is in flight: call `SplitForCompaction(msg, prefix, H)`. It takes the raw history after the prefix, minus the most recent H tokens, as the segment and records `snapEnd`. Then start `AsyncSummarize(prefix, segment, S_cap)` in the background. The existing summaries serve as context for the new summary, which lets that request reuse the cached prefix.
- **Worker model**: at most one background compaction per session. The foreground keeps calling the LLM and running tools on the unmodified context while the summary is generated. No other concurrency control is described, and none is needed under this single-writer, call-aligned model.
- **Snapshot boundaries**: a message index (`snapEnd`). The paper does not say whether a split may fall between a tool call and its result. It also sets no minimum segment size. After an application, if tokens are still above τ_soft, the next check can snapshot a segment only slightly larger than zero. The analytical model assumes a fixed L_snap = 60K, which the algorithm does not enforce.
- **Race between background summary and foreground appends**: resolved by appending only. The prefix list is never rewritten, the segment is identified by index, and the tail is recomputed from `snapEnd` at application time, so foreground appends are never lost. A snapshot can go stale in one way only: a τ_hard emergency compaction fires first, and then the pending work is discarded. The paper gives no rollback or validation of a bad summary.
- **Prefix list compaction (epochs)**: once the accumulated summaries reach τ_soft, APEC stops starting background work, lets the context grow to τ_hard, then does one synchronous full compaction and starts a new append-only epoch.
- **Exhaustion prediction**: none. Triggers are static thresholds. The only forward-looking element is the authors' qualitative argument that a lower τ_soft leaves more slack for the summary to finish before the context reaches τ_hard.
- **Publishing results back**: the summary is appended after earlier summaries, i.e. `[sysPrompt, S1, S2, ..., Sk, raw tail]`. The KV cache entries for sysPrompt and S1..S(k-1) stay valid. The abstract says "indexed summaries", but the paper never describes an index that can retrieve the original messages. **APEC is not lossless.** The raw segment is dropped from the prompt after summarization, and nothing in the paper keeps it retrievable.
- **Analytical cost model (claimed)**: a closed form (their Eq. 1 to 3) assuming constant per-call growth, constant summary size, full retention budgets, shared prefix caching, call-aligned triggers, and one epoch. Their worked example: S = 6K, B = 40K, H = 34K, N = 500 calls, g = 1K tokens per call, b = 4K, T = 240K, L_snap = 60K, lag ℓ = 2 calls, prices of $5, $0.50 and $25 per million uncached input, cached input and output tokens. It predicts $46.50 for synchronous compaction versus $38.43 for APEC, a 17.4% saving (claimed, analytical).

### 1.5 Results

Table 3 of the paper, GPT-5.6 Sol, averages over 8 tasks × 5 trials. All values **measured**; no variance reported.

| Metric | Sync-H | Sync-L | Async-L | Prune-H | Prune-L | PEC | APEC |
|---|---|---|---|---|---|---|---|
| Task accuracy (%) | 69.29 | 73.04 | 70.70 | 72.40 | 71.52 | 76.74 | 77.95 |
| Compactions per trial | 1.10 | 3.35 | 3.15 | 0.95 | 3.15 | 3.05 | 2.90 |
| Max compactions | 3 | 9 | 8 | 3 | 8 | 6 | 7 |
| Total input tokens (M) | 34.008 | 22.864 | 22.957 | 32.704 | 22.773 | 20.874 | 22.627 |
| Compaction cost ($) | 1.50 | 2.68 | 2.38 | 1.26 | 2.51 | 0.74 | 0.70 |
| Total cost ($) | 21.93 | 18.54 | 18.30 | 20.80 | 20.22 | 14.75 | 16.37 |
| Completion time (min) | 29.25 | 30.77 | 28.99 | 25.55 | 33.24 | 29.89 | 27.82 |
| Compaction time (s) | 60.02 | 176.76 | 156.88 | 49.81 | 178.87 | 160.38 | 156.17 |
| Overlapped compaction (%) | 0 | 0 | 98.65 | 0 | 0 | 0 | 85.34 |

Baselines: Sync-H and Sync-L are synchronous monolithic compaction at 90% and 50%. Prune-H and Prune-L add a rule-based pass that deduplicates file-read outputs and strips formatting bloat. Async-L is monolithic compaction run in the background at 50%. PEC is progressive (append-only segments) but blocking. APEC is progressive and asynchronous.

Other results:

- Cache hit rate around compaction boundaries (**measured**, their Figure 5a). Before compaction every method sits at 97.5% to 99.1%. After: monolithic methods 7.4% to 9.0%, PEC 33.0%, APEC 31.3%.
- Headline claims checked against the table: input tokens down 33.5% versus Sync-H (34.008 to 22.627M, consistent); total cost down 25.3% versus Sync-H (the abstract says 25.4%, a rounding mismatch); time down 9.6% versus Sync-L; accuracy up 8.66, 4.91 and 7.25 points over Sync-H, Sync-L and Async-L.
- GPT-5.4, their Table 4 (**measured**): Sync-H 56.70% accuracy, $41.06, 144.47 min. Async-L 52.57%, $42.32, 117.13 min. APEC 60.88%, $40.64, 61.53 min. The cost saving is about 1% here, against 25% on GPT-5.6 Sol, and the time saving of about 57% is far larger than on the main model. The paper does not discuss either difference.
- Sensitivity on GPT-5.6 Sol, their Table 5 (**measured**), as accuracy and cost by (S_cap, H): (20K, 20K) 77.41% and $18.16; (20K, 40K) 78.93% and $15.76; (20K, 60K) 78.36% and $21.51; (40K, 20K) 85.81% and $14.65; (40K, 40K) 82.12% and $17.94; (40K, 60K) 68.36% and $17.11.

### 1.6 What the APEC results actually show, and threats to validity

1. **Most of the gain comes from making compaction progressive, not from making it asynchronous.** PEC (progressive, blocking) matches APEC on accuracy (76.74% vs 77.95%) and has the lowest total cost ($14.75 vs $16.37). Asynchrony alone (Async-L vs Sync-L) lowers accuracy (70.70% vs 73.04%) and barely changes cost ($18.30 vs $18.54). What asynchrony buys is latency (APEC 27.82 min vs PEC 29.89 min). It costs tokens, because the foreground keeps running on the larger, not yet compacted context during the lag: input tokens are 22.627M for APEC vs 20.874M for PEC.
2. **Noise is probably as large as the effects.** Six sensitivity configurations of the same mechanism span 68.36% to 85.81%, a 17-point range, wider than the 8.66-point APEC versus Sync-H gap. With 40 runs per cell and no intervals reported, most of the pairwise accuracy gaps in Table 3 cannot be called significant.
3. Weighting accuracy by test count probably lets the largest repository dominate (Pydantic has 101 of the 315 items). Per-task results are not reported.
4. One model carries the main evaluation, and one harness (OpenClaude) is used throughout. The second model shows a different cost picture.
5. Compaction pressure is modest. Sync-H compacts 1.10 times per trial (max 3), so the headline "Sync-H vs APEC" comparison covers about one compaction per run.
6. Faithfulness to the paper's own algorithm is unknown: no code is released, and the published pseudocode leaves the minimum segment size and tool-call pairing undefined.
7. The production trace motivates the work, but none of the CompactionBench numbers were measured in production.
8. Nothing measures constraint retention or information loss directly. The only quality signal is end-of-task test accuracy.

---

## 2. Source B: The Complexity Trap

### 2.1 Bibliographic facts

- **Exact title**: "The Complexity Trap: Simple Observation Masking Is as Efficient as LLM Summarization for Agent Context Management".
- **Authors**: Tobias Lindenbauer (JetBrains Research; TUM), Igor Slinko (JetBrains Research), Ludwig Felder (TUM), Egor Bogomolov (JetBrains Research), Yaroslav Zharov (JetBrains Research).
- **Dates**: arXiv 2508.21433 v1 on 2025-08-29, v2 on 2025-09-05, v3 on 2025-10-27 (https://arxiv.org/abs/2508.21433). The v3 comment reads "DL4C camera-ready version to be presented at the 4th DL4C workshop co-located with NeurIPS '25; added OpenHands generality probe, added hybrid context management strategy".
- **Status**: a workshop paper (Deep Learning for Code in the Agentic Era, NeurIPS 2025 workshop). It went through workshop review, not main-track review. The paper text is licensed CC BY 4.0.
- **Code**: https://github.com/JetBrains-Research/the-complexity-trap, a fork of SWE-agent under MIT (the LICENSE copyright line is the SWE-agent authors'). Created 2025-10-10, last pushed 2025-11-18. **Data**: https://huggingface.co/datasets/JetBrains-Research/the-complexity-trap, Apache-2.0, holding full trajectories for the main SWE-agent runs, the OpenHands probe, and the hybrid.

### 2.2 Mechanisms

A turn is defined as T_i = (reasoning r_i, action a_i, observation o_i). The system and user prompts are immutable.

- **Raw Agent**: no management.
- **Observation Masking** with window M: observations of turns i < t minus M become a placeholder. Reasoning and actions are kept in full. The main experiments use M = 10.
  - **Code-level details (verified in `sweagent/agent/history_processors.py`, class `LastNObservations`)**: the placeholder text is `Old environment output: (N lines omitted)`, where N is the line count of the original. The paper's example, "Previous 8 lines omitted for brevity.", is not the string the code emits.
  - The first observation, the instance template carrying the problem statement, is never masked.
  - A `polling` parameter (default 1) moves the mask boundary only every `polling` steps, to help prompt caching. The main config uses the default, so the boundary moves every step and the prefix cache breaks about M turns back on every call.
  - Tags can force-keep or force-remove specific observations.
  - The processors are applied to the full, unmodified history each time a request is built (`Agent.messages`). Masking is a view, and the raw observations stay in the trajectory. That is exactly the "lossless store, lossy view" pattern.
- **LLM-Summary** with N = 21, M = 10: when the turns accumulated since the last summary reach N + M = 31, the oldest N are summarized together with the previous summary (or the problem statement if there is none). The summary replaces those turns and the last M stay raw. The summarizer runs at temperature 0 and the agent at 0.8. The prompt is OpenHands' summarizer prompt minus its non-coding examples, with sections USER_CONTEXT, COMPLETED, PENDING, CURRENT_STATE, CODE_STATE, TESTS, CHANGES, DEPS and VERSION_CONTROL_STATUS. The summary is inserted as an assistant message prefixed `Checkpoint for the last N turns:`. It runs synchronously inside request construction.
- **Hybrid**: masking with W = 10 during accumulation, plus LLM-Summary with N = 43 and M = 10. N = 43 was chosen because masked context at 43 turns roughly equals raw context at 21 turns (about 30K tokens, claimed from their Figure 9). The summarizer sees the unmasked turns, because in the config it runs before the masking processor.
- **Triggers**: turn counts only. Nothing token-based, adaptive or semantic. Both strategies run synchronously.

### 2.3 Setup

SWE-agent on SWE-bench Verified (500 instances), turn limit 250, one run per configuration. Five model configurations:

- Qwen3-32B, thinking and non-thinking, 122K context via YaRN;
- Qwen3-Coder-480B-A35B-Instruct-FP8, 256K;
- Gemini 2.5 Flash with a thinking budget of 0 or 800 tokens, 1M.

The Qwen models were self-hosted on vLLM (8 H200s), and their costs were computed after the fact at Alibaba API prices. Those prices do not discount cache hits for Qwen3-32B, so its costs are inflated (stated by the authors). Gemini costs are the amounts the Vertex API reported.

Significance comes from a paired bootstrap over the 500 instances (B = 10,000). It does not capture run-to-run sampling variance at temperature 0.8.

Hyperparameters were chosen on a randomly sampled 150-instance subset of SWE-bench Verified, the evaluation set itself, using GPT-4.1-mini, single runs (their Figures 6 and 10).

### 2.4 Results

Table 1 of the paper, SWE-agent, SWE-bench Verified, n = 500, single run, 95% bootstrap CIs. All values **measured**. † marks p < 0.05 versus Raw.

| Model | Strategy | Solve rate (%) | Cost per instance ($) |
|---|---|---|---|
| Qwen3-32B | Raw / Mask / Summary | 17.0 / 15.0 / 16.0 | 1.12 / 0.55† / 0.50† |
| Qwen3-32B thinking | Raw / Mask / Summary | 23.0 / 24.6 / 24.8 | 0.51 / 0.46 / 0.51 |
| Qwen3-Coder 480B | Raw / Mask / Summary | 53.4 / 54.8 / 53.8 | 1.29 / 0.61† / 0.64† |
| Gemini 2.5 Flash | Raw / Mask / Summary | 32.8 / 35.6 / 36.0 | 0.41 / 0.18† / 0.24† |
| Gemini 2.5 Flash thinking | Raw / Mask / Summary | 40.4 / 36.4† / 31.4† | 0.56 / 0.24† / 0.25† |

Observations from the authors' data:

- Masking was cheapest in 4 of 5 configurations. No solve-rate difference between masking and summary is significant except in the Gemini thinking configuration, where both fell significantly below Raw: by 4.0 points for masking (p = 0.04) and 9.0 points for summary (p < 0.0001). Masking and summary were not tested against each other; every test compares a strategy to Raw.
- Both strategies cut cost by more than 50% versus Raw wherever trajectories were long enough (**measured**). Qwen3-32B thinking had a median of only 15 turns, below the 31-turn summary warm-up, and masking saved only 9.8% there, not significant.
- **Trajectory elongation (measured)**: with Gemini 2.5 Flash, mean turns were 52 under summary, 44 under masking and 50 under Raw, i.e. +15% versus masking and +4% versus Raw. With Qwen3-Coder 480B, summary ran 15% longer than Raw and 13% longer than masking. The exception was Qwen3-32B, where masking ran 13% longer than Raw. The causal story, that summaries "mask failure signals" and keep the agent going, is a hypothesis (**claimed**). The supporting evidence is turn counts only.
- **Summary-call overhead (measured)**: per instance, $0.0143 (2.86% of cost) for Qwen3-32B, $0.0033 (0.65%) for Qwen3-32B thinking, $0.0439 (7.20%) for Qwen3-Coder, $0.0161 (6.71%) for Gemini Flash, and $0.0131 (5.24%) for Gemini thinking. These calls barely benefit from caching, since only the summarizer system prompt repeats.
- **Observation share (measured, preliminary)**: observations are about 84% of an average SWE-agent turn's tokens (SWE-bench Lite-50, GPT-4.1-mini, 3 runs).
- **Window sensitivity (measured, single run, 150 instances, GPT-4.1-mini)**: masking peaks at M = 10 and falls at M = 20. Summary does better with M > 0 tail turns and with N = 21 rather than OpenHands' 50/50 split.
- **OpenHands probe (measured)**: 50 instances, Gemini 2.5 Flash, one run. Masking at M = 10 "degrades drastically". M = 58 matched LLM-Summary. The released data shows they swept M over 10, 20, 29, 43, 50, 54, 58, 72 and 86 **on the same 50 instances (verified from file names)**, so M = 58 was picked on the evaluation slice. Their explanation is that OpenHands keeps retry turns. Reading OpenHands' `ObservationMaskingCondenser` at the commit they forked (fd3b4ac8), its `attention_window` counts **events**: every action and every observation is one event. So M = 58 in OpenHands corresponds to roughly 29 action/observation pairs, not 58 turns. The paper does not mention this unit difference (our inference from code; verified that the code counts events).
- **Hybrid (measured, single run, SWE-bench Verified-50, Qwen3-Coder 480B)**: cost 7% below masking and 11% below summary. The extrapolated savings of $20 and $35 on the full benchmark are **claimed**. The "+2.6 percent points" over Raw cannot be an absolute difference: on 50 instances from one run, solve rates move in 2-point steps. It is consistent with a one-instance difference expressed as a relative change (for example 39 vs 38 solved is +2.6% relative). With naive settings (N = 21, W = 10) the hybrid was *less* cost-efficient than either strategy alone (**measured**).
- **Critic-enhanced summaries (measured, 150 instances)**: no solve-rate gain, higher cost, longer trajectories.

### 2.5 What we verified in the released code and data

- Placeholder text, first-observation exemption, `polling`, and view-only application: verified as described above.
- **The released summary processor does not keep context as tightly bounded as the paper's Equation 6 describes.** With `enable_static_checkpointing: true` (set in both the main LLM-Summary config and the hybrid configs), the processor keeps every summary in an internal cache. On the step a summary is created, it returns `[sys, user, latest summary, kept turns]`. On every other step it returns `[sys, user, summary 1, ..., summary k, unprocessed turns]`. We checked this against the released hybrid archive `local-Qwen3-Coder_480B-agent-t_0.8-turn-summaries-t_0-summary_N_21_M_10_obs_masking_M_10_combined.0-verified-50` by counting `Checkpoint for the last` messages in each step's `messages` field. In the longest runs, views swing between 1 summary (on trigger steps, 23 messages) and up to 10 accumulated summaries on the steps in between (**verified**, 50 trajectories inspected). We did not check the 500-instance LLM-Summary archives (600 MB or more each). They use the same processor and flag, so the same behavior is likely but unconfirmed. The effect on the paper's conclusions is probably small: summaries are short compared with masked observations, and the cost figures come from the actual tokens. Still, the "bounded context" property attributed to LLM-Summary is not what the released code sends to the model, and the view's prefix changes on trigger steps and again on the step after.

### 2.6 Weaknesses and threats to validity

1. One run per configuration with temperature 0.8 sampling, and CIs that come from resampling instances only.
2. Hyperparameters (M, N) were tuned on a subset of the evaluation set, albeit with a different model. The OpenHands window was tuned on the very same 50 instances it was then evaluated on.
3. Single domain (SWE-bench), where observations are long and noisy, which favors masking. The authors acknowledge this.
4. The problem statement is never masked or summarized, so task constraints cannot be lost by construction (the critique from Source C). SWE-bench tasks also carry few process constraints.
5. Cost conclusions depend on the pricing scheme, especially cache discounts, and on caching behavior (masking with `polling=1` breaks the cache every step).
6. The hybrid's evidence is one run on 50 instances.
7. The elongation mechanism is a hypothesis resting on correlational turn counts.

---

## 3. Source C: Benchmarks Are Surprisingly Compaction-Friendly

### 3.1 Bibliographic facts

- **Exact title**: "Benchmarks Are Surprisingly Compaction-Friendly".
- **Authors**: Jeanne Salle (ELLIS Institute Tübingen; MPI for Intelligent Systems), Hardik Bhatnagar (Tübingen AI Center; University of Tübingen; equal contribution), Matthias Bethge (Tübingen AI Center; University of Tübingen), Sahar Abdelnabi and Maksym Andriushchenko (equal supervision; ELLIS Institute Tübingen, MPI-IS, Tübingen AI Center).
- **Dates**: arXiv 2610.31778 v1 on 2026-10-22, v2 on 2026-11-20 (https://arxiv.org/abs/2610.31778). cs.LG, 28 pages.
- **Status**: preprint; no venue listed. The paper is under the arXiv perpetual non-exclusive license.
- **Code**: https://github.com/aisa-group/compaction (MIT; created 2026-10-22). It is a copy of OpenCode at upstream commit 41612b3db with extra `compaction.*` config keys, plus an MCP "compact tool" wrapper.

### 3.2 Mechanisms

- **Scaffold**: OpenCode, fixed for every run. Compaction is checked after each assistant turn against a token budget. In the paper the thresholds are absolute: 160K, 180K, 200K, 260K, 320K and 380K tokens. In the code the trigger is the model's usable input limit minus a `reserved` buffer, so a threshold is set through `reserved` (**verified** in `docs/COMPACTION.md` and `packages/opencode/src/session/compaction.ts`). The upstream default reserve is min(20,000, max output tokens) (**verified**), which puts the default trigger very close to the limit.
- **Self-summarization (baseline)**: a user prompt asks the agent to summarize. The prompt (their Appendix 6.4.1) explicitly asks for "key user requests, constraints, or preferences that should persist". The summary goes into a fresh context with the system prompt and an instruction to continue, and the same model writes it.
- **Tool-output pruning**: runs after every assistant turn with no model call. In the code (**verified**) it walks backward through the history and skips everything newer than the second-to-last user message. It stops at the last summary or at an output already pruned, and it skips the `skill` tool. It counts estimated tokens (chars/4) of completed tool outputs, protects the most recent 40,000 (`PRUNE_PROTECT`), and prunes the older ones only if that frees more than 20,000 (`PRUNE_MINIMUM`). A pruned output is flagged as compacted and appears in the prompt as `[Old tool result content cleared]`. The original output stays in session storage, so this is again a lossy view over a lossless store. Pruning runs together with self-summarization.
- **Filesystem offloading**: before each summary, the full message history is dumped as JSON to `.opencode/compactions/`, and the summary says where it is.
- **Clear thinking**: reasoning blocks older than the last k assistant turns are removed before summarizing. The paper says k = 10. The repository default and README example use `keepThinkingTurns = 1`, and the paper does not say which value its runs used.
- **Small-model summarization**: Claude Haiku 4.5 writes the summary.
- The repository also has an `appendOriginalPrompt` option, which appends the first user message to every summary. That would directly counter the constraint erosion the paper measures, but the paper reports no results with it.

### 3.3 Setup

- **Models**: Claude Opus 4.6 (1M context, max effort), GPT-5.3-Codex (400K, medium), GPT-5.4 (1,050K, medium), Gemini 3.1 Pro Preview (1M, high). The abstract says "three frontier models" while the body evaluates four. Gemini rarely compacts and the authors treat it as uninformative.
- **Benchmarks**:
  - PaperBench: 3 papers (adaptive-pruning, all-in-one, bam), Code-Dev variant, gpt-4o-mini judge, 10-hour limit, with an IterativeAgent-style "continue" nudge.
  - ProgramBench: Canop/broot and jarun/nnn for accuracy; lua/lua is added for the erosion analysis and lua plus luajit for the reward-hacking analysis.
  - PostTrainBench: gemma-3-4b-pt and Qwen3-4B-Base, each on GPQA, GSM8K and BFCL.
- **Runs**: "two runs per task and configuration". Failed runs were relaunched until enough succeeded, and accuracy is conditional on a valid submission.
- **Judges**: Claude Sonnet 5 for reward hacking and constraint erosion, with GPT-5.6 Sol as a second judge in the SWE-chat analysis.

### 3.4 Results

All numbers **measured** unless marked.

- **Threshold (Q1)**: for Opus 4.6 and GPT-5.4, accuracy is flat from 160K to 380K, which is only 16% to 38% of their windows. For GPT-5.3-Codex on PaperBench it rises from 0.38 at 160K to about 0.60 at 260K and above. ProgramBench stays flat. PostTrainBench error bars overlap everywhere. Defaults near 90 to 95% of the window were not tested for the 1M-context models.
- **Compactions per run** (baseline, mean): at 160K, Opus 4.90, Gemini 1.00, GPT-5.4 15.08, GPT-5.3-Codex 27.71 (±17.00). At 380K, 0.71, 0.40, 1.96 and 0.76. GPT-5.3-Codex compacts about once every 15 to 25 agent steps on PaperBench at 160K. Gemini's tool outputs have a median length of 26 characters, against 918 for Opus 4.6.
- **Strategy (Q2, at 160K)**:
  - No strategy beats self-summarization beyond the CIs. Pruning stays close to the baseline.
  - Offloading lowers GPT-5.3-Codex from 0.38 to about 0.24.
  - Clear thinking lowers GPT-5.4 from 0.59 to 0.43.
  - Haiku-written summaries leave Opus 4.6 within its CI but lower GPT-5.4 from 0.59 to 0.43 and GPT-5.3-Codex from 0.38 to 0.18.
  - ProgramBench shows no significant differences.
- **Constraint erosion on ProgramBench (Q3)**:
  - Mean anti-cheating rule categories dropped per summary, out of 3: GPT-5.3-Codex 2.73 (at least one dropped in 99% of summaries, all three in 80%), GPT-5.4 1.76 (90%, 25%), Opus 4.6 0.51 (43%, 0%).
  - By compaction index: 0.92 dropped at the first compaction, 1.93 by the 10th, 2.41 by the 15th, and 2.65 by the 30th. Rules compound away because each summary is written from the last.
  - The judge was validated against one human labeler on 110 summaries: Cohen's κ 0.85, 0.96 and 0.80 for the three rules, and weighted κ 0.87 on the dropped count. 16 of 21 disagreements went in the conservative direction.
- **Real sessions (Q4, SWE-chat)**: 44 sessions with at least one compaction, 269 extracted constraints. Overall, 81% were preserved and 19% dropped (24.5% under the second judge; the judges' κ was 0.681). Feature requirements (96 of 269, 35.7%) were dropped 6.2% of the time. Process instructions (39 of 269) were dropped 35.9% of the time, and scope exclusions 36.8% (their count is not printed in the text). The "about 6×" headline is 35.9 against 6.2. The sessions were recorded with Claude Code, not OpenCode.
- **Do dropped rules cause violations? (inconclusive)**
  - ProgramBench: a dropped rule was broken after 23 of 3,940 cases (0.6%) versus 3 of 1,040 kept (0.3%); odds ratio 2.0, Fisher p = 0.33. With lua and luajit added: 30/4,677 vs 3/1,194, OR 2.6, p = 0.13. All 26 post-compaction violations broke the no-internet rule.
  - SWE-chat: 7 of 24 dropped constraints were violated (29%) versus 26 of 137 kept (19%), p = 0.28. Half of the dropped constraints never came up again.
  - For Opus 4.6, 10 of 12 violating runs broke a rule before the first compaction.
- **Rule-aware score (measured, confounded with run length)**: pooled over models, raw score stays within 0.07 of the task mean, while the rule-aware score (violations score 0) falls from -0.03 with no compaction to -0.07 at 2 to 3 compactions, -0.14 at 4 to 7, and -0.28 at 32 or more.

### 3.5 The compaction-friendly argument

- **The benchmark property that makes compaction look good (claimed, argued from the results)**: the objective is fixed and given once, no user input arrives mid-run, and progress persists on disk. After compaction the agent can recover nearly everything by re-reading its own files. The information it needs from earlier context is "small in scope and easy to find". The score measures *what* was built, not *how* it was built, so dropped process rules never show up in it.
- **What breaks compaction**: constraints that exist only in context. These are process instructions ("how to work") and scope exclusions ("don't touch X"), along with anti-cheating rules given once, and user instructions that change over a session. The authors also cite the Recursive Language Models paper, where a GPT-5 compaction agent scores 70.5 on BrowseComp-Plus but 0.1 on OOLONG-Pairs, a task whose answer depends on almost every line. We checked that table (https://arxiv.org/html/2512.24601, Table 1): **base GPT-5 without compaction also scores 0.1 on OOLONG-Pairs** (32K input). That example shows OOLONG-Pairs is hard, not that compaction in particular breaks it (verified).
- **Recommendations for evaluation design**:
  - use tasks whose constraints cannot be recovered from the filesystem, such as multi-turn sessions with user instructions that change over time;
  - score rule adherence alongside task success (rule-aware scoring);
  - measure constraint retention per compaction and by compaction index;
  - compare against subagent decomposition under the same scaffold.

### 3.6 Weaknesses and threats to validity

1. Very small samples: 2 runs per configuration, 2 or 3 tasks per benchmark, and runs relaunched until they succeed. Accuracy is conditional on a valid submission (a survivorship filter), which can hide strategies that cause more failures. Failure rates are reported separately and are high.
2. For the 1M-context models, the threshold sweep covers only 16 to 38% of the window, far from where scaffolds actually trigger.
3. The paper never says how often pruning actually fired. From the code, pruning only touches tool outputs older than the second-to-last user message and newer than the last summary, so in autonomous runs with few user messages it may rarely fire. That would explain "close to baseline" without saying anything about the merit of pruning (our reading of the code; not executed).
4. Offloading is "lossless" only if the agent reads the file back, and the paper never measures whether it did.
5. The repo's clear-thinking default (1) disagrees with the paper (k = 10), and the `appendOriginalPrompt` mitigation goes unreported.
6. LLM judges extracted and labeled the SWE-chat constraints. Agreement between the two judges is substantial but imperfect (κ 0.681).
7. Citation hygiene: the paper calls Wang et al.'s suite "COMPINT" under the title "Lost in compaction: evaluating side-constraint loss under context compaction". The arXiv record 2608.11242 is titled "Lost in Context Compaction: When LLM Systems Drop Side Constraints" and names the suite CONPACT (verified).

---

## 4. Cross-source synthesis

- **Agreement**:
  - Removing old tool outputs (masking or pruning) is close to as good as summarization on coding benchmarks, and much cheaper (B measured; C measured; A's Prune-H was the fastest configuration, though not the most accurate).
  - Summarization has costs beyond its API call: trajectory elongation (B), cache invalidation (A), and constraint erosion (C).
  - All three use static triggers, and none predicts exhaustion.
- **Disagreement on thresholds**:
  - A finds the 50% trigger better than 90% (Sync-L vs Sync-H: 73.04% vs 69.29% accuracy, $18.54 vs $21.93) on a 400K model.
  - C finds a 400K model (GPT-5.3-Codex) *worse* at lower absolute thresholds on PaperBench, and the 1M models insensitive.
  - Different models, benchmarks, harnesses and tiny samples make these irreconcilable on current evidence. The threshold has to be a tunable, measured parameter.
- **Where the losses come from**: B's masking keeps the task prompt, C shows that losing the prompt's rules is the main silent failure, and A never measures it. In all three, the prompt-pinning decision is the one most consequential for correctness.
- **Caching**: A and the OpenCode and Anthropic implementations all treat "clear only in large batches" and "append-only prefix" as the way to keep cache reuse. B's masking with `polling=1` is the cache-hostile variant.

---

## 5. Implications for our design

### 5.1 Starting defaults (every value configurable; provenance in brackets)

| Parameter | Starting value | Provenance |
|---|---|---|
| Usable budget | window minus reserved output (APEC: 400K minus 128K) | APEC setup, claimed as configuration |
| Soft trigger (start background work) | 50% of usable budget | APEC τ_soft, measured on one model and 8 tasks only |
| Hard trigger (block, emergency) | 90% of usable budget | APEC τ_hard; Copilot CLI documents about 80% to start background compaction and about 95% to block (claimed, product docs) |
| Max concurrent background compactions per session | 1 | APEC algorithm; Anthropic background compaction docs |
| Summary output cap | 20K tokens (sensitivity best: 40K) | APEC default and Table 5, measured, noisy |
| Raw tail kept after a summary | about 20K tokens | APEC H, measured, noisy |
| Accumulated-summary cap before epoch rollover | equal to the soft trigger (136K in APEC's setup) | APEC, claimed as configuration |
| Observation mask window | 10 turns (not events) | Complexity Trap, measured on SWE-agent only; OpenHands needed more |
| Tool-output protect budget / minimum batch to clear | protect newest 40K tool-output tokens; clear only if at least 20K is freed | OpenCode constants, verified in code |
| API-side equivalents for reference | clear_tool_uses: trigger 100K input tokens, keep 3 tool uses, optional clear_at_least; threshold compaction default 150K, minimum 50K | Claude API docs, claimed |
| First summary in a masked hybrid | after about 43 turns with W = 10 | Complexity Trap hybrid, measured on n = 50, single run |

### 5.2 Masking policy

- Make masking the first tier and summarization the second, as in the hybrid. It is cheap, deterministic, needs no model call, and matched summarization on coding tasks (measured, B).
- Mask observations (tool results), never reasoning or actions, and never the initial task prompt or any user message.
- Give each placeholder the original size and a stable retrieval handle (our addition; none of the sources do this). Because our store is lossless, a masked item should be re-fetchable by ID. This is where we can do better than OpenCode's `[Old tool result content cleared]` and SWE-agent's line count.
- Move the mask boundary in batches (an SWE-agent-style `polling`, OpenCode's 20K minimum, Anthropic's `clear_at_least`), so each cache break is paid for by the tokens it frees. Measure cache hit rate per boundary.
- Count the window in turns, i.e. action/observation pairs, and document the unit. The OpenHands "M = 58" lesson suggests the unit and the scaffold's retry handling both shift the right value.
- Consider staleness-aware masking, e.g. masking a file read once that file has been edited. B lists the lack of relevance or staleness awareness as a limitation, and AgentDiet (Section 6) reports gains from removing "expired" information.

### 5.3 Asynchronous scheduling and the race protocol

- Use APEC's progressive, append-only structure: `[pinned prefix][S1..Sk][raw tail]`. A's data puts most of the benefit here (PEC vs Sync-L), and it keeps cache reuse around 31 to 33% after a compaction instead of 7 to 9% (measured, A).
- Treat asynchrony as a latency feature with a token cost, and make it switchable per session. On A's data, async gave about 7% lower completion time than blocking progressive compaction and cost about 11% more (measured, one model).
- A race protocol consistent with A and with the Anthropic background-compaction docs (https://platform.claude.com/docs/en/build-with-claude/compaction-background):
  1. Snapshot by sequence number.
  2. Keep one compaction in flight per session.
  3. Never edit history in place; append only.
  4. Apply results only at a request boundary. Nothing touches an in-flight foreground request.
  5. Replace exactly the snapshotted range and keep everything after it.
  6. Stamp each job with an epoch or generation number, and discard its result if a hard compaction or another commit has superseded it.
  7. Commit atomically.
  8. Never split a tool call from its result at a segment boundary (left undefined in A).
  9. Enforce a minimum segment size (undefined in A; the analytical model assumes 60K).
  10. Cancel abandoned summaries, or at least account for their cost.
- The dev.to survey (Section 6) lists the hazards a design must cover: stale snapshots, silent drops, partial failure, out-of-order results, bad-summary rollback, and double triggering.
- Size the soft trigger from measured growth instead of a fixed ratio. Start background work while the remaining headroom still exceeds (p95 summary latency in foreground calls) × (p95 tokens per call), with a safety factor. This rule is our derivation, not from any source. A measured mean growth of about 1K tokens per LLM turn (Table 3) and an 85% overlap share give a sense of scale.
- Make the hard path cheap and deterministic. A blocking LLM summary at τ_hard is the expensive emergency. A mask-and-truncate fallback that needs no model call (CliffCompaction-style dropping instead of rephrasing; see Section 6) should come before it.

### 5.4 Pinning constraints

- Never let a summary be the only copy of the task prompt or of any user instruction. Keep them verbatim in the pinned prefix, or in a separately maintained constraint ledger that is re-injected after every summary.
- Evidence:
  - C's ProgramBench erosion: about 1 of 3 rules gone at the first compaction, 2.65 of 3 by the 30th (measured).
  - C's SWE-chat: process and scope constraints dropped about 6× as often as feature requirements (measured).
  - CONPACT: 17% retention without an extractor and over 90% with one (measured, per abstract).
  - GitHub Copilot documents re-adding the original user instructions and plan state after compaction (claimed, docs).
- Prompt instructions alone are not enough: C's summarization prompt explicitly asked for constraints to persist, and they were dropped anyway (measured).
- The model writing the summary matters a great deal (Opus 4.6 dropped 0.51 of 3 rules on average, GPT-5.3-Codex 2.73), so constraint retention has to be enforced structurally, not delegated to the summarizer.

### 5.5 Lossless retrieval: what not to expect

- Storing everything does not by itself improve task scores. C's filesystem offload was neutral for 1M-context models and *hurt* GPT-5.3-Codex (0.38 to about 0.24, measured, tiny n). Retrieval has to be discoverable (handles in placeholders), cheap, and measured (how often it is used, and whether it was needed).
- A summarizer smaller or different from the main model is model-dependent (−16 and −20 points for the OpenAI models in C, measured), and it loses cache reuse: hit rate falls to about 6% after a model switch (measured, Copilot companion paper).

### 5.6 Evaluation workload design

- **Check that the workload actually compacts.** Most coding benchmarks never trigger compaction (A, measured). Report compactions per run, and drop workloads that do not reach the triggers several times.
- **Build workloads that compaction cannot game**, following C:
  - multi-turn sessions where user instructions arrive and change mid-session;
  - process and scope constraints that exist only in conversation;
  - rule-aware scoring that zeroes out violating runs;
  - constraint-retention measured per compaction and by compaction index.
- CompactionBench-style release-evolution sessions (SWE-EVO repositories, one requirement per turn, git history stripped) are a good template for sustained growth, but the benchmark itself is unreleased. We would have to rebuild it from SWE-EVO and the published task inventory.
- Include a dense-aggregation probe (OOLONG or OOLONG-Pairs) as a stress test. Always report the no-compaction baseline next to it, because base models fail OOLONG-Pairs too.
- **Metrics to log for every run**:
  - task score and rule-aware score;
  - total and cached input tokens;
  - cost under at least two pricing schemes, with and without cache discounts;
  - foreground stall time and the share of compaction time overlapped;
  - cache hit rate on the last call before and the first call after each boundary;
  - summary-call cost as a share of the total;
  - turns per task, to detect trajectory elongation;
  - retrieval calls per compaction.
- **Statistics**: at least 3 to 5 runs per configuration, with CIs over runs and over tasks. Tune hyperparameters on a split disjoint from the evaluation set. Report per-task results, not only test-weighted averages.
- **Anti-cheating hygiene**: strip VCS history and release tags (A), and block network where the task forbids it (C's violations were all no-internet).

### 5.7 What not to rely on

- APEC's headline numbers (25.3% cost, 9.6% time, +8.66 points). One main model, 8 tasks, 5 trials, no intervals, no code, and a sensitivity spread wider than the effect.
- M = 10 as a universal window. It is scaffold-specific and was tuned on part of the test set.
- "The threshold does not matter". C shows that only for 1M models at 16 to 38% of the window, with about two runs per point. A shows the opposite direction on another 400K model.
- That an LLM summary bounds context. Even the reference implementation lets summaries accumulate in the view (verified, B's released hybrid run).
- Vendor-reported internal gains (Anthropic: +29% from context editing, +39% with memory, 84% fewer tokens; Cursor: 50% less compaction error). These are measured on internal evaluations we cannot inspect.
- That summarization prompts preserve constraints because they ask to (C, measured).

---

## 6. Related work discovery pass

Unless stated otherwise, items were read at abstract or page level only. Numbers from abstracts are the authors' measurements as stated in the abstract; we did not check the papers' bodies.

1. **ACON: Optimizing Context Compression for Long-horizon LLM Agents** (Kang et al.), https://arxiv.org/abs/2510.00615, ICML 2026, code at https://github.com/microsoft/acon. Compresses both observations and history, using compression guidelines optimized in natural language through failure analysis (no fine-tuning), then distilled into smaller compressors. Reports 26% to 54% lower peak tokens with better task success on AppWorld, OfficeBench and Multi-objective QA, and up to 46% gains for small agent models. **Measured** (abstract).
2. **OpenHands context condensation** (blog, 2025-04-09), https://openhands.dev/blog/openhands-context-condensensation-for-more-efficient-ai-agents (redirects to www.openhands.dev). LLM summarizing condenser that triggers at a size threshold so cache rebuilds are amortized. The authors argue it turns quadratic cost scaling into linear. Reports 54% vs 53% solved and per-turn cost settling below half the baseline on an unspecified SWE-bench Verified subset (**measured**, sample size not given). At commit fd3b4ac8 (**verified**), `LLMSummarizingCondenser` defaults are max_size = 100 events, keep_first = 1, condensing to half the size, with events truncated to 10,000 characters. `ObservationMaskingCondenser` defaults to attention_window = 5 events with placeholder `<MASKED>`. The condenser code has since moved into https://github.com/OpenHands/software-agent-sdk.
3. **SWE-agent history processors**, https://swe-agent.com/latest/reference/history_processor_config/. Documentation for `LastNObservations` (typical n = 5, placeholder "Old environment output: (n lines omitted)", `polling` for cache friendliness) and related processors. Documentation only.
4. **Anthropic, "Effective context engineering for AI agents"** (2025-09-29), https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents (redirects to /research/). Presents compaction as the first lever, tool-result clearing as "one of the safest lightest touch forms of compaction", and structured note-taking and subagents as alternatives. Recommends tuning compaction prompts for recall first, then precision. Describes Claude Code continuing from a summary plus the five most recently accessed files. **No measurements.**
5. **Anthropic, "Managing context on the Claude Developer Platform"** (2025-09-29), https://www.anthropic.com/news/context-management (redirects to https://claude.com/resources/articles/context-management). Context editing plus a memory tool. Internal agentic-search evaluation: +29% from context editing alone, +39% with memory. A 100-turn web-search evaluation used 84% fewer tokens. **Measured on internal evaluations, not reproducible.**
6. **Claude API documentation**: context editing (https://platform.claude.com/docs/en/build-with-claude/context-editing): `clear_tool_uses_20250919` with a default trigger of 100K input tokens, keep 3 tool uses, optional `clear_at_least`, `exclude_tools` and `clear_tool_inputs`, plus `clear_thinking_20251015`. Threshold compaction (https://platform.claude.com/docs/en/build-with-claude/compaction-threshold): default trigger 150K input tokens, minimum 50K, with `pause_after_compaction` and custom `instructions`. Background compaction (https://platform.claude.com/docs/en/build-with-claude/compaction-background) gives an explicit swap protocol: record how many messages the request held, keep appending, start no second compaction, then drop exactly those messages and keep later ones. Documentation, **not measured**.
7. **Claude Code**, https://code.claude.com/docs/en/costs. Auto-compaction "when approaching context limits", `/compact` with focus instructions, a "Compact instructions" section in CLAUDE.md, and cache statistics that count compaction as an expected cache rebuild. This page gives no exact trigger percentage. Documentation, **not measured**.
8. **GitHub Copilot context management**, https://docs.github.com/en/copilot/concepts/agents/copilot-cli/context-management (redirects to https://docs.github.com/en/copilot/concepts/prompting-and-context/context-management). Copilot CLI starts background compaction at about 80% of the window and pauses if usage reaches about 95% before it finishes. Compaction replaces history with a structured summary plus the original user instructions and current plan or to-do state. Documentation, **claimed**.
9. **Cognition, "Don't Build Multi-Agents"** (Walden Yan, 2025-06-12), https://cognition.ai/blog/dont-build-multi-agents (redirects to cognition.com). Argues for sharing full traces rather than fragments, and for a dedicated, possibly fine-tuned, compression model for very long tasks. **No measurements.**
10. **Cursor, "Training Composer for longer horizons"** (Cassano and Rush, 2026-03-17), https://cursor.com/blog/self-summarization. Self-summarization is trained in RL with the final reward assigned to summary tokens. At 80K and 40K triggers it reports 50% less compaction error than a tuned prompt baseline, with summaries averaging about 1,000 tokens against more than 5,000. **Measured on internal CursorBench, not reproducible.**
11. **MemGPT / Letta**, https://arxiv.org/abs/2310.08560 (v1 2023-10-12), https://www.letta.com/blog/memgpt-and-letta/, https://github.com/letta-ai/letta. Virtual context management modeled on OS paging: the agent moves data between in-context and external memory tiers through function calls. Evaluated on document analysis and multi-session chat with older models. **Measured** (abstract). This is the closest prior art for "losslessly retrievable" context, with the agent in control.
12. **ReSum** (Wu et al.), https://arxiv.org/abs/2509.13313. Periodic summarization for web agents, plus ReSum-GRPO to adapt agents to summary-conditioned reasoning. Reports +4.5% over ReAct without training and +8.2% more with RL. **Measured** (abstract).
13. **AgentFold** (Ye et al.), https://arxiv.org/abs/2510.24699. A learned "folding" operation that condenses at multiple granularities each step. Reports 36.2% on BrowseComp and 47.3% on BrowseComp-ZH with SFT only. **Measured** (abstract).
14. **Context-Folding** (Sun et al.), https://arxiv.org/abs/2510.11967. The agent branches into sub-trajectories and folds them into summaries, trained with FoldGRPO. Matches ReAct with a 10× smaller active context on Deep Research and SWE tasks. **Measured** (abstract).
15. **Context Rot** (Hong, Troynikov, Huber; Chroma, 2025-07-14), https://research.trychroma.com/context-rot (redirects to https://www.trychroma.com/research/context-rot). 18 models; performance degrades with input length even on simple controlled tasks. On LongMemEval (306 prompts), focused inputs of about 300 tokens beat full inputs of about 113K tokens for every model family. **Measured** (full page read).
16. **OOLONG** (Bertsch et al.), https://arxiv.org/abs/2511.02817, COLM 2026, project page https://oolongbench.github.io. Long-context aggregation tasks (synthetic and real D&D transcripts). GPT-5, Claude Sonnet 4 and Gemini 2.5 Pro all score below 50% on both splits at 128K. **Measured** (abstract).
17. **Recursive Language Models** (Zhang, Kraska, Khattab), https://arxiv.org/abs/2512.24601. The prompt becomes a REPL variable the model inspects and recursively queries. Table 1 (**verified**): GPT-5 compaction agent 70.5 on BrowseComp-Plus and 0.1 on OOLONG-Pairs, base GPT-5 0.1 on OOLONG-Pairs, RLM (depth 1) 58.0. OpenCode with context offloading scored 94.0 on BrowseComp-Plus.
18. **Lost in Context Compaction: When LLM Systems Drop Side Constraints** (Wang et al.), https://arxiv.org/abs/2608.11242. In an inbox experiment an agent deleted emails without approval in 30 of 100 sessions after compaction and 0 before. The CONPACT suite finds 17% average side-constraint retention. A separate constraint extractor appended after compaction gets over 90%. **Measured** (abstract). Directly supports constraint pinning (Section 5.4).
19. **Slipstream** (Chen et al.), https://arxiv.org/abs/2605.08580. Runs compaction asynchronously so the summary and the agent's next steps come independently from the same state, then uses a judge to check the candidate summary against the agent's continued reasoning. Reports up to +8.8 points accuracy and up to 39.7% lower latency on SWE-bench Verified and BrowseComp. **Measured** (abstract). Relevant to validating background summaries before commit.
20. **Parallel Context Compaction** (Cim et al.), https://arxiv.org/abs/2605.23296. Splits context into blocks summarized in parallel, for predictable summary volume and lower wall time than sequential compaction. Tested on HotpotQA and LoCoMo with 8B to 120B backbones. **Measured** (abstract, qualitative).
21. **TokenPilot** (Xu et al.), https://arxiv.org/abs/2606.17016, EMNLP 2026 Findings. Cache-aware context management: stabilizes prefixes at ingestion and evicts in conservative batches. Reports 56% to 87% cost reductions on PinchBench and Claw-Eval. **Measured** (abstract).
22. **AgentDiet / Reducing Cost of LLM Agents with Trajectory Reduction** (Xiao et al.), https://arxiv.org/abs/2509.23586, FSE 2026. Removes useless, redundant and expired trajectory content at inference time. Reports 39.9% to 59.7% fewer input tokens and 21.1% to 35.9% lower cost with no loss in performance. **Measured** (abstract).
23. **CliffCompaction** (Nguyen et al.), https://arxiv.org/abs/2609.26779. Compaction that only truncates or drops content, never rephrases it, and never compacts a compaction. Reports up to 50% lower cost on Terminal-Bench, ships as an open-source API proxy. **Measured** (abstract). Relevant to our deterministic hard-path fallback.
24. **SelfCompact** (Li et al.), https://arxiv.org/abs/2606.23525. The model decides when to compact via a tool plus a fire/suppress rubric. Reports matching or beating fixed-interval summarization at 30% to 70% lower cost on math and agentic search. **Measured** (abstract). Relevant if we expose "compact now" hints from the agent.
25. **CompactionRL** (Li et al.), https://arxiv.org/abs/2607.05378, and **SUPO** (Lu et al.), https://arxiv.org/abs/2510.06727. RL training with summarization-based compaction in the loop. CompactionRL reports +6.6 points on SWE-bench Verified over inference-time compaction for GLM-4.5-Air. **Measured** (abstracts). These show that how well a model holds up under compaction can depend on training, which matches C's model-dependence results.
26. **DeepMiner / Beyond Turn Limits** (Tang et al.), https://arxiv.org/abs/2510.08276. A sliding-window context (observation dropping, no summarizer) used in both training and inference; sustains about 100 turns in 32K context. **Measured** (abstract).
27. **You don't need long context** (Belcak et al.), https://arxiv.org/abs/2611.05306. With frequent compaction, frontier models lose little accuracy when kept on short contexts, and they hold up on long-horizon software engineering even at one action per context. **Measured** (abstract, no numbers given there).
28. **"Async compaction: the race conditions nobody talks about"** (dev.to, crabtalk), https://dev.to/crabtalk/async-compaction-the-race-conditions-nobody-talks-about-4ni. A practitioner survey of eight frameworks with five hazard classes (stale snapshot, silent drop, partial failure, ordering, bad-summary rollback) plus double triggering, and fixes: generation counters, merge instead of replace, one compaction in flight, pre-compaction snapshots. Its claims about other frameworks (LangGraph, Aider, Google ADK) are **unverified by us**. **No measurements.**

## 7. Items we could not access or verify

- The preprints.org page and PDF for APEC (HTTP 403 for automated clients). We read the Microsoft Research copy instead. We could not confirm that it matches preprints.org v1, or check its license.
- Any CompactionBench or APEC code or data release. We found none.
- Whether the 500-instance LLM-Summary archives in the Complexity Trap dataset show the same summary accumulation as the hybrid archive we inspected (not downloaded).
- How often tool-output pruning actually fired in the compaction-friendly paper's runs, and which `keepThinkingTurns` value was used (not reported).
- Discovery items beyond their abstracts or pages, unless marked verified.
