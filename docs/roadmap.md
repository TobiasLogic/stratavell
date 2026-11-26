# Roadmap

v0.1.0 is a research preview. Its benchmarks measure what each strategy keeps available, at what simulated cost, on generated sessions with a fixed trajectory. The next phase is about whether that translates into better agents. Nothing below has been run yet, and none of it is part of the v0.1.0 release.

## Engineering

- **Real-world integrations.** Integrate with at least two open agent loops through full interception and publish the integration code. Today the only live loops are the examples and the benchmark harness.
- **Concurrency at scale.** The current load test is four agents with 150 turns each. Test tens of concurrent agents on one database, long-running servers, and many idle sessions with `ReleaseSession`.
- **Search latency.** Lexical search p95 is 33.7 ms at 13k indexed chunks. Options: a dedicated index per agent, cheaper OR-fallback scoring, or a cgo SQLite build as an option.
- **Live provider validation.** Exercise the Anthropic adapter against the live Messages API, including prompt-cache breakpoints and thinking-block preservation. Add tests for the Ollama adapter.

## Evaluation

The goal is cost per successfully completed task, measured on real agent trajectories with real provider bills.

### Benchmarks

| benchmark | what it adds | notes |
|---|---|---|
| [LOCA-bench](https://github.com/hkust-nlp/LOCA-bench) (Zeng, Huang and He, ICML 2026) | agents whose context grows under control while task semantics stay fixed. It treats the context-management strategy as part of the scaffold, which is the comparison Stratavell needs. | open source |
| [OOLONG](https://oolongbench.github.io) (Bertsch et al.) | dense aggregation over long contexts, a stress test for summaries and retrieval | report the no-compaction baseline next to it; base models score poorly on it too |
| [SWE-bench Pro](https://scale.com/leaderboard/swe_bench_pro_public) (Scale AI), or another executable coding benchmark | real repositories, executed tests, task success | Epoch AI's review rates the public set as flawed, with a large share of broken tasks. Use a verified subset, or report broken-task handling explicitly. |
| CompactionBench (Microsoft, with APEC) | multi-session coding with repeated compaction and executable checks | not released as of this writing; run it if it becomes available |
| [CompactBench](https://compactbench.github.io/compactbench) | repeated compact-and-continue cycles scored on locked decisions, forbidden behaviors and immutable facts | a candidate for the constraint-retention question |

No benchmark named "CompAct-Bench" could be found. The closest are CompactionBench and CompactBench above, and a benchmark proposed but not released in arXiv 2607.08032.

### Method

- **Strongly matched baselines.** Every baseline gets the same retrieval tools, the same summarizer model and the same prompts. Add model-written summaries for the conventional baseline, since the extractive summarizer used so far penalizes summary-only strategies.
- **Original implementations.** Where licenses and interfaces allow, compare against the original systems rather than reimplementations.
- **Real trajectories.** The agent chooses its own actions, so a strategy can change what happens next.
- **Real billing.** Report provider-reported cache reads, cache writes and uncached tokens next to the simulator's prediction, and calibrate the simulator from the difference.
- **Low-cost and cross-provider runs.** Run a low-cost model family, such as DeepSeek, for breadth and at least one other provider family for validation. Prompt caching rules differ by provider, so cost results must be reported per provider.
- **Statistics.** Pre-register protocols as was done for the v3 reader run, use enough tasks and seeds for confidence intervals, and report the intervals.
- **Headline metric.** Cost per successfully completed task, with success rate, token use and wall-clock time next to it.

### Infrastructure

- [Harbor](https://github.com/harbor-framework/harbor), the harness behind Terminal-Bench, runs agents such as Claude Code, OpenHands and Codex CLI in containerized environments. It can fan out to hosted sandboxes, which makes it a candidate runner for the executable benchmarks.
- [Daytona](https://www.daytona.io) and Modal provide hosted sandboxes that Harbor can use for parallel environments.
- Model-based evaluation spends API credits, so it starts only with an approved budget.
