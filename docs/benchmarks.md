# Benchmarks

All numbers on this page come from runs in this repository, on 2026-11-26. Raw results, per-turn logs and per-probe logs are in `bench/results/2026-11-26/`. The experiment log, including failed and superseded runs and every API cost, is `docs/experiments.md`.

Read the results with these limits in mind:

1. The coding sessions are generated, not recorded from real agents. The trajectory is fixed in advance, so no strategy can make the agent take a different path. These runs measure what each strategy keeps available and what it costs, not end-to-end task success on real repositories.
2. Prompt-cache costs come from a simulator of Anthropic's documented caching rules, not from provider bills.
3. Summarizer and turn latency are modeled for the deterministic runs. The real-model runs use measured latency.
4. CompactionBench is not released (see `docs/research/01-apec-masking-benchmarks.md`), so it could not be run. OOLONG was not run. The reader test below is a small end-to-end check with a real model, not a substitute for either.

## Workload

`bench.Generate(seed, turns)` produces one long coding session per seed. It is meant to stress compaction in the ways the research notes identified as decisive.

- **Repository:** a synthetic Go codebase of 38 files (2 to 30 KB each), including `pkg/api/config.go` with planted constants and `pkg/journal/writer.go` with a planted fsync-batching function.
- **Turns:** each turn has one to three tool calls:
  - file reads, about 30% of turns
  - file writes that carry the whole new file as tool input, about 20%
  - `go test` runs with 60 to 960 tests and large logs, about 30%; two thirds of them fail with a changing assertion
  - grep commands with 40 to 1,540 matches, about 20%
- **Planted facts,** asked about at twelve probe points per session:
  - two user ground rules in the first message (never modify `vendor/`, keep the public API of package `store` unchanged)
  - a later process rule from the user (run `go vet` before every commit)
  - an architectural decision with its reason, written by the agent
  - a rejected approach with its reason
  - two configuration values read from a file
  - a function name inside a large file
  - the exact assertion message of the currently failing test, which changes during the session
- **Size:** a 300-turn session is 2.9 to 3.2 million tokens of raw content, 54 to 59 times the 64k window's effective capacity. A 400-turn session is 4.1 to 4.5 million tokens, 23 to 25 times the 200k capacity. Tool output volume is deliberately high. SWE-agent, for comparison, truncates outputs, so this workload is a stress test of output handling.

The constraints appear only once, early, and cannot be recovered from the files. That is the property "Benchmarks Are Surprisingly Compaction-Friendly" found missing from most benchmarks.

## Strategies

| id | strategy | implementation |
|---|---|---|
| A | uncompressed | engine with compaction, masking, externalization, retrieval and task state off; a request over capacity counts as an overflow |
| B | conventional compaction | at 90% of capacity, summarize everything except the last 8 messages into one summary written from the previous summary plus new history (Claude Code and Norma style), synchronous, no retrieval; the kept tail starts at a tool-group boundary |
| C | noa | reimplementation of Norma's noa algorithm from `docs/research/04-norma-noa.md`: nudges at 0.75 and 0.45 of the limit, protected tail of 5 messages and 5,000 tokens, tiered blocks (5 tier-1 make a tier-2, 10 tier-2 make a tier-3), truncation of old large outputs at 0.95, compression written in-band by the primary model (counted as extra primary calls) |
| D | observation masking only | engine rolling mask keeping the last 10 observations, nothing else |
| E | synchronous progressive summarization | engine DAG summaries at the soft threshold, blocking, nothing else (closest to APEC's PEC baseline) |
| F | asynchronous progressive summarization | as E, in background jobs published between requests (closest to APEC) |
| G | palimpsest default | everything on |

All strategies use the same summarizer within a run: `summarize.Extractive` (deterministic, no model) for the main runs, or Claude Haiku 5.5 for the real-summarizer run. All strategies get the same host system prompt and tool definitions, and the same cache simulator with breakpoints on the system block and on recent history. G additionally places a breakpoint after its last summary, which is part of its layout.

## Metrics

- **input Mtok:** total input tokens sent over the session, including extra primary-model calls (C).
- **input cost:** cache-simulated input cost at Sonnet 5.5 list prices from the caching documentation: $2.00 per Mtok uncached, $2.50 for 5-minute cache writes, $0.10 for cache reads. Output tokens are not priced, because they are identical across strategies apart from summarization. Overflowed turns are not sent and cost nothing, so A, C and D look cheap for that reason.
- **cache hit:** cache-read tokens divided by input tokens.
- **overflow turns:** requests that did not fit the window. For a real agent this is a failure.
- **fg wait:** foreground time spent waiting for summarization, modeled as 0.8 s + input/4,000 tokens per second + output/60 tokens per second for extractive runs, measured for real-model runs.
- **probes visible:** planted facts present in the assembled context at probe points.
- **probes recoverable:** visible, or found by a fixed retrieval policy: `memory_search` with a natural query, then `memory_get_event` on the top three hits, within about 6k tokens. Only G and its ablations have retrieval tools. A, B, C, D, E and F lose whatever is not visible.
- **standing rules:** fraction of turns on which all three user rules are visible.
- **tool Mtok sent:** tool-result tokens sent over the session.
- **engine p95:** time spent inside `Assemble` per turn (wall clock, real).
- **exact recovery:** events that decode and hash-match their stored content hash at the end of the run.

Values are mean ± sample standard deviation over seeds. The hardware was an 8-core, 16-thread x86-64 laptop CPU with 16 GB RAM and an NVMe SSD, running Go 1.27.4 and modernc SQLite 3.54.1 (pure Go).

## Main comparison, 64k window

300 turns, 5 seeds, effective capacity 53,700 tokens.

| strategy | input Mtok | input cost $ | cache hit % | max ctx k | overflow turns | fg wait s | probes visible % | probes recoverable % | standing rules % | tool Mtok sent | engine p95 ms | exact recovery % |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| A uncompressed | 0.12 ± 0.06 | 0.12 | 57.3 | 44.2 | 294 ± 2 | 0 | 0.0 | 0.0 | 1.4 | 0.12 | 0.22 | 100 |
| B conventional | 8.61 ± 0.37 | 11.42 ± 0.67 | 48.9 | 53.3 | 56 ± 10 | 824.9 ± 32.2 | 26.4 ± 6.8 | 26.4 ± 6.8 | 61.5 | 7.44 | n/a | n/a |
| C noa | 7.32 ± 1.55 | 5.67 ± 0.75 | 22.1 | 53.5 | 194 ± 21 | 555.5 ± 133.9 | 10.3 ± 5.0 | 10.3 ± 5.0 | 29.0 | 2.64 | n/a | n/a |
| D masking only | 0.30 ± 0.16 | 0.56 | 36.2 | 48.8 | 291 ± 3 | 0 | 0.7 | 0.7 | 2.4 | 0.24 | 1.36 | 100 |
| E sync summary | 8.75 ± 0.07 | 11.41 ± 0.49 | 49.9 | 45.6 | 0 | 322.5 ± 23.5 | 12.1 ± 3.0 | 12.1 ± 3.0 | 21.1 | 7.34 | 11.13 | 100 |
| F async progressive | 9.18 ± 0.05 | 13.04 ± 0.43 | 45.0 | 45.5 | 0 | 0 | 12.9 ± 2.8 | 12.9 ± 2.8 | 22.2 | 7.71 | 7.49 | 100 |
| G palimpsest | 7.45 ± 0.03 | 6.43 ± 0.21 | 68.1 ± 1.2 | 36.1 | 0 | 0 | 49.5 ± 1.7 | 100.0 | 100.0 | 3.52 | 2.66 ± 0.32 | 100 |

Recovery by fact kind, recoverable over asked, summed over seeds:

| strategy | constraint | decision | rejected | value | symbol | current error |
|---|---|---|---|---|---|---|
| B conventional | 82/140 | 0/45 | 1/45 | 4/90 | 3/45 | 15/31 |
| C noa | 27/140 | 5/45 | 2/45 | 2/90 | 1/45 | 4/31 |
| E sync summary | 24/140 | 0/45 | 1/45 | 2/90 | 6/45 | 15/31 |
| F async progressive | 26/140 | 0/45 | 2/45 | 2/90 | 5/45 | 16/31 |
| G palimpsest | 140/140 | 45/45 | 45/45 | 90/90 | 45/45 | 31/31 |

Observations:

- Among strategies that never overflowed, G used 44% less input money than E, 51% less than F and 44% less than B, which overflowed on 56 turns.
- A and D first overflow at turns 4 to 7 and stay over the limit from then on. Large tool outputs fill the window regardless of masking, because masking alone does not shrink the newest observations.
- C overflows because its protected tail (last 5 messages and 5,000 tokens) cannot shrink when a single turn carries several large outputs. This matches the failure mode in the Norma code review.
- F costs more than E. The asynchronous strategy keeps sending the larger context while its summaries are in flight. APEC's own table shows the same pattern (PEC cheaper than APEC); asynchrony buys foreground latency, not tokens.
- In G, half the planted facts are visible in context at probe time; the other half are recovered through retrieval. Without retrieval tools (ablation below) G recovers only what is visible.

## Main comparison, 200k window

400 turns, 3 seeds, effective capacity 179,700 tokens.

| strategy | input Mtok | input cost $ | cache hit % | max ctx k | overflow turns | fg wait s | probes visible % | probes recoverable % | standing rules % | tool Mtok sent | engine p95 ms |
|---|---|---|---|---|---|---|---|---|---|---|---|
| A uncompressed | 1.91 ± 0.62 | 0.60 | 90.3 | 173.5 | 379 ± 4 | 0 | 0.0 | 0.0 | 3.9 | 1.76 | 0.28 |
| B conventional | 38.73 ± 1.19 | 16.46 ± 1.10 | 86.5 | 161.4 | 0 | 298.3 ± 34.7 | 36.0 | 36.0 | 75.9 | 35.97 | n/a |
| C noa | 43.98 ± 2.62 | 30.90 ± 2.79 | 60.2 | 176.1 | 6 ± 2 | 726.3 ± 120.5 | 36.3 | 36.3 | 79.8 | 30.91 | n/a |
| D masking only | 17.82 ± 2.42 | 44.02 ± 6.29 | 1.3 | 179.1 | 253 ± 23 | 0 | 11.0 | 11.0 | 30.5 | 11.39 | 1.75 |
| E sync summary | 32.77 ± 0.61 | 17.82 ± 0.35 | 81.5 | 150.9 | 0 | 382.9 ± 23.0 | 41.9 ± 7.5 | 41.9 ± 7.5 | 79.5 | 25.84 | 16.45 |
| F async progressive | 39.54 ± 0.55 | 27.62 ± 0.80 | 75.0 | 152.5 | 0 | 0 | 47.4 ± 6.3 | 47.4 ± 6.3 | 78.1 | 32.43 | 4.47 |
| G palimpsest | 30.59 ± 0.80 | 10.44 ± 0.32 | 89.9 ± 0.2 | 124.2 | 0 | 0 | 56.8 ± 2.2 | 100.0 | 100.0 | 18.84 | 3.04 ± 0.51 |

With a larger window every summarizing strategy keeps more facts visible. B stops overflowing and C overflows on only 6 turns. A first overflows at turns 16 to 24 and D at turns 57 to 80. G is still 37% cheaper than B, 41% cheaper than E and 62% cheaper than F. It recovers every planted fact and keeps every standing rule. D's rolling mask moves every turn, which drops its cache hit rate to 1.3% and makes it the most expensive strategy here.

## Ablations of G, 64k window

300 turns, 5 seeds. Each row turns one component off.

| variant | input cost $ | cache hit % | probes recoverable % | standing rules % | tool Mtok sent | fg wait s | engine p95 ms | what changes |
|---|---|---|---|---|---|---|---|---|
| G | 6.43 ± 0.21 | 68.1 | 100.0 | 100.0 | 3.52 | 0 | 2.46 | |
| no masking | 7.22 ± 0.18 | 64.8 | 100.0 | 100.0 | 3.79 | 0 | 2.74 | +12% cost |
| no externalization | 13.12 ± 0.54 | 41.4 | 100.0 | 100.0 | 6.58 | 0 | 8.57 | +104% cost, +87% tool tokens |
| no dedupe or unchanged-read stubs | 6.61 ± 0.28 | 67.4 | 100.0 | 100.0 | 3.57 | 0 | 2.68 | +3% cost |
| no task state block | 5.96 ± 0.22 | 69.7 | 98.8 | 100.0 | 3.55 | 0 | 2.07 | −7% cost, misses 5 of 31 current-error probes |
| no pinned directives | 6.26 ± 0.21 | 68.8 | 100.0 | 77.0 | 3.53 | 0 | 2.65 | −3% cost, standing rules drop to 77% |
| no task state, no pins | 5.80 ± 0.21 | 70.4 | 98.8 | 80.9 | 3.56 | 0 | 1.91 | −10% cost, both losses |
| no retrieval tools | 6.30 ± 0.33 | 67.7 | 49.7 | 100.0 | 3.57 | 0 | 2.20 | −2% cost, recoverable facts halve |
| rolling masking | 9.58 ± 0.38 | 51.2 | 100.0 | 100.0 | 3.63 | 0 | 2.64 | +49% cost from cache breaks |
| layout from the brief (task state before history) | 11.05 ± 0.27 | 42.2 | 100.0 | 100.0 | 3.50 | 0 | 2.63 | +72% cost from cache breaks |
| synchronous compaction | 5.37 ± 0.16 | 74.2 | 100.0 | 100.0 | 3.40 | 427.6 ± 18.4 | 13.90 | −16% cost, 7 minutes of blocking per session |

Every component except deduplication earns its cost on this workload. Externalizing oversized outputs at ingestion is the largest single saving. Masking in epochs instead of a rolling window, and keeping dynamic blocks out of the cached prefix, together account for most of the remaining cost difference. Synchronous compaction is cheaper and blocks the foreground; the asynchronous default trades about 16% more input cost for zero summarization wait. Hosts that are cost-bound rather than latency-bound can set `compaction.async` to false.

## End-to-end reader test (real model)

At four probe points per session, the reader is asked each planted question against the exact context the strategy assembled at that point. The reader is Claude Haiku 5.5 via OpenRouter, temperature 0, 600 output tokens. For G, when a fact is not visible, the results of the retrieval policy above are appended before the question. This emulates an agent that searches with a reasonable query; it does not test whether an agent would decide to search.

Grading checks the answer against each fact's key (substring, or for long keys all numbers in order of appearance, or for `Name = Value` keys the value as a whole token).

### With extractive compaction

64k window, 300 turns, seeds 1 and 2, 64 questions per strategy.

| strategy | correct | accuracy % | overflowed requests | correct when the fact was visible | correct when not visible |
|---|---|---|---|---|---|
| A uncompressed | 0 | 0.0 | 64 | 0/0 | 0/0 |
| B conventional | 15 | 23.4 | 23 | 15/15 | 0/26 |
| C noa | 9 | 14.1 | 48 | 9/9 | 0/7 |
| D masking only | 0 | 0.0 | 64 | 0/0 | 0/0 |
| E sync summary | 5 | 7.8 | 0 | 5/6 | 0/58 |
| F async progressive | 5 | 7.8 | 0 | 5/7 | 0/57 |
| G palimpsest | 64 | 100.0 | 0 | 31/31 | 33/33 |

### With Claude Haiku 5.5 as the summarizer

The same reader, with E, F and G compacting through real Haiku 5.5 summaries. 64k window, 300 turns, seed 1, 33 questions per strategy.

| strategy | correct | accuracy % | correct when visible | correct when not visible |
|---|---|---|---|---|
| E sync summary | 6 | 18.2 | 6/8 | 0/25 |
| F async progressive | 6 | 18.2 | 6/8 | 0/25 |
| G palimpsest | 33 | 100.0 | 21/21 | 12/12 |

Real summaries keep somewhat more than extractive ones (E and F go from 8% to 18%), but most planted facts still disappear once their raw turns are summarized and condensed. G's result does not depend on summary quality, because its pinned instructions and its retrieval path read the original events.

A first reader run used a 120-token answer cap and a short-answer prompt. 56 answers came back empty because the reader spent the whole cap on hidden reasoning, and the prompt discouraged giving the reason that the decision question asked for. The run is kept in `bench/results/2026-11-26/reader-v1-superseded/` and is graded there with the final keys: G 75.0%, B 21.9%, C 12.5%, E 7.8%, F 7.8%, A and D 0%. The prompt, the cap and two answer keys were changed after inspecting that run. The changes were applied identically to every strategy, and both runs are published.

## Acceptance targets

| target | result | evidence |
|---|---|---|
| 100% exact recovery of committed, retained events | met | `Verify` after every engine run: 100% of events, no lineage problems; recovery tests after repeated compaction and after SIGKILL |
| 100% preservation of designated protected constraints | met | standing rules visible on 100% of turns in every run with pins enabled; `TestConstraintsSurviveEveryAssembly`, `TestUserDirectivesSurviveCompaction` |
| zero silent tool-call protocol corruption | met | every assembly validates pairing; adapters validate again; no violations in any run; rejected inputs are explicit errors |
| zero cross-agent or cross-session leakage | met in tests | `TestCrossSessionIsolation`, `TestSubagentIsolationAndHandoff`, `TestHTTPFlow`; the benchmarks are single-agent |
| zero known context-overflow requests in controlled tests | met | 0 overflow turns in all 63 runs of G and its ablations; capacity checked in property tests across masking modes and sync and async compaction |
| no synchronous summarization in normal operation | met | G foreground wait 0 s in every run; sync mode exists only when configured |
| under 20 ms p95 incremental overhead for small events | met | see performance below |
| under 20 ms p95 lexical retrieval on a representative corpus | met at 3.4k chunks, missed at 13k chunks | see performance below |
| at least 50% fewer unnecessary tool-output tokens | met at 64k, partly at 200k | tool tokens sent: G 3.52 M vs E 7.34 M (−52%) and B 7.44 M (−53%) at 64k; at 200k G 18.84 M vs E 25.84 M (−27%) and B 35.97 M (−48%) |
| lower foreground compaction latency than existing compaction | met | G 0 s vs B 825 s (64k) and 298 s (200k), modeled |
| no meaningful degradation in task success vs the strongest baseline | met on this workload | reader accuracy G 100% vs B 23%; this is not a measurement of task success on real repositories |

## Performance

PERFORMANCE_SECTION

## Reproducing

```
go build -o bin/palbench ./cmd/palbench
./bin/palbench -window 64000 -turns 300 -seeds 1,2,3,4,5 -strategies main -out out/main64k
./bin/palbench -window 64000 -turns 300 -seeds 1,2,3,4,5 -strategies ablation -out out/ablation64k
./bin/palbench -window 200000 -max-output 16000 -turns 400 -seeds 1,2,3 -strategies main -out out/main200k
OPENROUTER_API_KEY=... ./bin/palbench -window 64000 -turns 300 -seeds 1,2 -strategies G-hybrid -openrouter-reader anthropic/claude-haiku-5.5 -reader-points 4 -out out/reader-G
go test ./engine -run '^$' -bench . -benchtime 300x
go test ./engine -run TestSearchLatencyProfile -v
```

The deterministic runs give identical results on the same build. The workload, ids and clock are seeded, and the summarizer is deterministic. Engine latency varies with the machine.
