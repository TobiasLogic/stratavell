# Experiment log

Every benchmark and model run made while building palimpsest, in order, including failed and superseded ones. Times are UTC+7 on 2026-11-26. Commit hashes refer to the build each run used. Raw outputs of the runs that the reports cite are in `bench/results/2026-11-26/`. Earlier runs are summarized here and their raw files were not kept.

## Research (no compute cost)

Four research passes read the primary sources and code listed in `docs/research/README.md`. Two of the four research agents stopped on an account rate limit after writing their notes. The notes were checked before use (no instructions or suspicious requests in them), and their key claims were cross-read against the sources they cite.

## Deterministic runs

| time | build | run | outcome |
|---|---|---|---|
| 10:06 | 536c38f | smoke: 120 turns, seed 1, A to G, 64k | worked; G 72% recoverable; exposed two search weaknesses (camelCase identifiers not indexed inside bodies, path hits ranked mentions above reads) |
| 10:07 | 536c38f plus fixes | smoke2, smoke3 | identifier subwords indexed per chunk, path reads ranked first, reliability and recency priors scaled down (they were overriding rank differences); G 100% recoverable |
| 10:15 | 75034b9 | first full suites (64k main, 64k ablation, 200k main) | **failed**: 64k main and the ablation panicked in synchronous compaction (stale chunk indices after the raw region was trimmed, `index out of range`); fixed in e1afa7d with a regression test |
| 10:25 | e1afa7d | full suites rerun | completed; G cost $8.01 at 64k, engine p95 8.0 ± 12.1 ms with spikes on turns with a new user message; superseded by the final runs |
| 10:40 | 8532d05 | search latency profile | p95 search 52 ms at 4.5k chunks before the fix (FTS5 `snippet()` on every candidate); after snippets only for final hits, df-based pruning and AND-first queries: p95 8.8 ms at 3.4k chunks, 33 ms at 13.2k chunks |
| 10:50 | 8532d05 plus changes | smoke5 to smoke7: task state and breakpoint ablations, 300 turns, seeds 1 and 2 | found that the task state block cost 29% of input money in the tail; a snapshot in the cached prefix plus a tail delta cut that, and moving the archive breakpoint onto the last summary cut G from $7.79 to $6.35 |
| 11:02 | 56cd6f8 | final suites: 64k main (5 seeds), 64k ablation (11 variants, 5 seeds), 200k main (3 seeds) | completed |
| 13:11 | 0dc39e9 | `go test -race ./...` | all packages passed, no races reported; the engine package took 1,987 s under the race detector |
| 13:30 | 1479e22 onward | append profiling | a 160 KB log took 73 ms to append with inline indexing; keyword prefilters for digest regexes, no code-symbol scan for command output, prefix checks for pass lines and tighter location hints brought the benchmark-mode append p95 from 71 ms to 25 ms; G's seed-1 results were compared after each change: identical except 24 input tokens out of 7.4 million (auto-injected evidence ranked slightly differently once log symbols were dropped) |
| 13:47 | c6ff120 | **final suites rerun on the release build** | all reported metrics identical to the 56cd6f8 run at two decimals; engine wall-clock p95 within noise; these artifacts are the ones in `bench/results/2026-11-26/` |
| 14:00 | c6ff120 | latency profiles and micro-benchmarks | see `docs/benchmarks.md`, "Performance" |

### After the external review (2026-11-26, afternoon)

| build | run | outcome |
|---|---|---|
| 83737b9 | four suites (main 64k, ablation 64k, main 200k, matched 64k) after the accounting fix | stopped before finishing: superseded by the provenance fix below; partial results showed noa's cost roughly doubling once its in-band calls went through the cache simulator |
| 314ff5d | **final suites**: the four above, with reconciled accounting, evidence-grounded probes, component-matched baselines and retrieval provenance | completed; every run passed the token and cost reconciliation check; these are the tables in `docs/benchmarks.md` |

## Real-model runs

Costs per run are estimates, because runs overlapped and OpenRouter reports one running total per key. The readings are listed below the table.

| time | build | provider | run | estimated cost | outcome |
|---|---|---|---|---|---|
| 10:27 | e1afa7d | Ollama, qwen2.5-coder 1.5B Q4, local | one smoke call with `num_ctx` 32768 | $0 | worked; not used further, because the user asked to use OpenRouter instead |
| 10:31 | e1afa7d | OpenRouter, Claude Haiku 5.5 | key check | under $0.001 | worked; the key has a $5 limit |
| 10:32 | e1afa7d | OpenRouter, Haiku 5.5 reader | reader test v1, extractive compaction, A to G, seeds 1 and 2 | about $1.35 | **superseded**: 120-token cap; 56 answers came back empty because the model spent the cap on hidden reasoning; short-answer prompt discouraged the "why" part; kept in `reader-v1-superseded` |
| 10:32 | e1afa7d | OpenRouter, Haiku 5.5 summarizer and reader | reader test v1 with real summaries, B, C, E, F, G, seed 1 | about $1.2 | **superseded** for the same reasons; B (conventional) was stopped at 16 of 33 answers after about 2.5 hours because every compaction blocked on a 27k-token summary call |
| 11:10 | cd900f7 | OpenRouter, Haiku 5.5 reader | **reader test v2**, extractive compaction, A to G, seeds 1 and 2; 600-token cap, prompt asks for the reason when the question asks why | about $1.07 | completed; G 100%, B 23.4%, C 14.1%, E and F 7.8%, A and D 0% (all overflowed) |
| 11:20 | 09ba4b4 | OpenRouter, Haiku 5.5 summarizer and reader | **real-summary reader test**, E, F, G, seed 1 | about $0.82 | completed; G 100%, E 18.2%, F 18.2% |

| 15:10 | 2343c4b | OpenRouter, Haiku 5.5 reader | **reader test v3, frozen protocol** (`bench/review/protocol-reader-v3.md`), G versus F + retrieval versus B + passive archive, seed 1 | about $0.42 | completed; strict 28/33 (G), 27/33 (B + archive), 26/33 (F + retrieval): parity under matched retrieval; most strict misses were hedges caused by retrieval outputs lacking file paths, fixed afterwards in 314ff5d and not re-measured |

Running total reported by the OpenRouter key endpoint: $0.000004 after the key check, $1.35 when reader v1 finished (real-summary v1 still running), $2.03 and $2.24 during the real-summary v1 run, $3.30 when reader v2 finished, $4.12 after the real-summary run, and **$4.54 after reader v3**, against the key's $5 limit. The remaining credit was left unspent.

## Grading changes, all applied to stored answers offline

1. The failing-test question is graded on the assertion message without its `file:line` prefix. Readers that quoted only the message were being marked wrong.
2. The API-constraint question is graded on `store` instead of `package store`.
3. `Name = Value` facts are graded on the value as a whole token. The earlier rule accepted any answer that contained the name and the digits anywhere. One E answer ("I do not know ... RetryLimit ...") had been counted correct by accident; it is now wrong.

4. While writing `REVIEW_PACKET.txt`, a sample answer showed that lenient grading credits hedged answers ("I don't know. The session shows `flushLedgerBatch` ... but nothing identifies it as performing fsync batching"). A strict count is now reported next to the lenient one. Only G is affected: 98.4% and 87.9% strict, against 100% lenient in both runs.

Changes 1 and 2 were made after reading the v1 answers. All three apply to every strategy, and both the v1 and v2 runs are published with the final grader.

5. After the external review, strict grading (no hedged answers) became the headline. "Evidence available" was added as a third dimension, and v3 reports all three under a protocol frozen before the run.

## Bugs found by tests and runs

| found by | bug | fix |
|---|---|---|
| fuzz seed corpus | database paths containing `#` were passed raw into SQLite's `file:` URI, so everything after `#` was a fragment: separate databases collapsed into one shared file and the WAL and foreign-key pragmas were dropped | 75034b9: URI escaping, `TestPathsWithURICharacters` |
| first full suites | stale chunk indices in synchronous compaction | e1afa7d |
| `TestStaleAndUnchangedFileReads` | generic deduplication ran before the unchanged-read rule, so a re-read after a write could be shown as a duplicate | dedupe no longer applies to file reads |
| `TestPromptInjectionStaysData` | the engine's own tag names inside tool output reached the model unescaped through previews | escaping in all rendered stubs and previews |
| `TestDeterministicReplayAfterRestart` | queued jobs were abandoned on restart, so the restored context differed | jobs are rebuilt from the current view |
| integrity check | long-lived pooled reader connections reported false FTS5 corruption under `PRAGMA integrity_check`, while queries on the same connections returned correct results | checks run on a fresh connection plus the FTS5 `integrity-check` command |
| Go example | the failure digest picked bare `FAIL` lines over the assertion message | assertion-first digests |
| examples | Unix socket paths over about 104 bytes fail with `bind: invalid argument` | the server explains the limit |
| `TestGrade` | value grading accepted answers by accident | token-level matching |
| external review | noa's in-band compression calls were priced with an approximation and left out of the cache-hit numerator, so its cost and hit rate disagreed | every request goes through the cache simulator; runs fail unless tokens and cost reconcile |
| reader v3 | search hits and event pages did not show which file an archived observation came from, so a reader could not confirm where a retrieved value lives | paths in search hits, event pages and injected evidence; `TestRetrievalOutputsCarryProvenance` |
