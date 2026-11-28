# Experiment log

Every benchmark and model run made while building Stratavell (named palimpsest until the v0.1.0 rename; the benchmark tool was then called `palbench`), in order, including failed and superseded ones. Times are UTC+7 on 2026-11-26. Commit hashes refer to the build each run used. Raw outputs of the runs that the reports cite are in `bench/results/2026-11-26/`, and the deterministic suites rerun after the standing-rule key fix are in `bench/results/2026-11-27/`. Earlier runs are summarized here and their raw files were not kept.

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

### Release check for v0.1.0 (2026-11-26, evening)

| build | run | outcome |
|---|---|---|
| rename working tree before 983d247, then again from a clean clone of df46f66 | seed 1 of the 64k main, matched and ablation suites, compared with `bench/results/2026-11-26/` by `scripts/compare-runs.py` | both times: 26 strategy runs, every deterministic field identical, only timing fields differ. Not stored separately, because the outputs equal the published ones |
| 983d247 | `TestDeterministicSmoke` golden file created (120 turns, 32k window, five strategies) | identical across three repeats and at GOMAXPROCS 1, 2 and 16 |
| 983d247 | `scripts/release-build.sh 0.1.0`, run twice | six platform archives, byte-identical SHA256SUMS across the two builds |

No real-model run was made for the release, and no API credit was spent.

### Search latency investigation (2026-11-26, night)

The test behind the 33.7 ms p95 at 13.2k chunks was a 1,200-turn session. Each idea was measured on a saved copy of that corpus, with the engine's exact queries, on the same machine. No API credit was spent.

| experiment | result |
|---|---|
| per-query breakdown | three queries made the tail. `pkg/mod1/file1.go` took 36 ms: 17 ms full-text, plus 19 ms in the stale-read check, one or two queries per candidate. `value_42 compute` took 27 ms and `HandleRequest compute value` 21 ms, almost all full-text. The other five took 1 to 4 ms. |
| CPU profile of `Search` | BM25 scoring was about 42% of search time, the per-candidate stale check about 16%, and document-size lookups inside BM25 about 15% |
| native C SQLite through cgo (mattn/go-sqlite3, the same SQLite 3.54.1) against pure-Go modernc, on the same SQL | 1.85x faster: the sum over 8 queries dropped from 66.8 to 36.2 ms, and the slowest query from 27.1 to 15.0 ms. Not adopted, because it gives up the no-cgo build. |
| `PRAGMA mmap_size=256MB` | no effect: 66.8 to 65.9 ms |
| FTS5 `optimize` (segment merge) | sum 66.8 to 59.2 ms, slowest query unchanged. Adopted as part of `Vacuum`. |
| session token removed from the MATCH expression | sum 66.8 to 52.1 ms. Not adopted: the token is what keeps multi-session databases isolated inside FTS5. |
| prototype in-memory inverted index in pure Go | sum 12.0 ms, slowest 4.8 ms, 11.5 MB heap, 0.6 s to build. Not adopted: the fixes below reached similar latency without a second index to keep crash-consistent. |
| term frequencies of the slow queries | `value` (38.6% of chunks), `compute` (58.8%) and `go` (74.2%) were still in the queries, despite the 5% common-term rule. Two bugs: (1) the refill to three terms took the rarest remaining terms however common they were; (2) document frequency ignored two-letter words, so `go` looked like a 0% term. |
| stale check as one EXISTS query against one JOIN plus DISTINCT | 0.61 ms against 13.8 ms for 40 candidates, with identical results |

The fixes are dddc848:
- term pruning refills only from terms in at most 20% of chunks, and keeps the two rarest only when nothing else is left
- two-letter words are counted in document frequency
- a query that is a single path skips full-text search when the exact path index has hits
- the stale check is one EXISTS query per search
- `Vacuum` merges FTS5 segments first

`TestSearchLatencyProfile`:
- 13.2k chunks: p95 35.8 ms (33.7 ms in the published run) became 6.2 to 6.5 ms
- 3.4k chunks: p95 10.6 ms became 2.8 to 3.1 ms

On a freshly reopened copy of the corpus, p95 over the eight queries went from 76.5 to 14.6 ms.

Seed 1 of the 64k main, matched and ablation suites (26 strategy runs) on dddc848 is identical to `bench/results/2026-11-26/` in every deterministic field, including recoverability per fact kind and injected evidence. The golden smoke run is unchanged.

### Standing-rule key fix, 2026-11-27 (UTC+7)

| time | build | run | outcome |
|---|---|---|---|
| 01:44 | 314ff5d tree, one key changed, scratch copy | seed 1, 64k main and matched, `package store` key replaced by the rule's sentence | E 18.1% to 6.5% and F 20.1% to 7.0% standing rules; prompted the fix |
| 01:47 | none (a throwaway test) | key audit: every fact key over seeds 1 to 5, listing where each key occurs | only the three standing-rule keys match unrelated text |
| 01:51 to 02:01 | 635fcb5 | all four suites rerun in full: 64k main, matched and ablation (5 seeds), 200k main (3 seeds) | 151 strategy runs, 7,029 deterministic fields compared with `bench/results/2026-11-26/`; only rule-visibility fields changed, for B, E, F, their variants and the G ablation without pins; 200k unchanged; stored in `bench/results/2026-11-27/` |

The first attempt at the rerun started only the 64k main suite. The other three suites never ran, because of a shell error in the launch command, and were started again at 01:55.

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

### Free-model reader runs, 2026-11-27 (UTC+7)

All on OpenRouter free endpoints, $0. Build 1b0788f for every run.

| time | run | outcome |
|---|---|---|
| 00:10 to 00:29 | v4 pilot: 120 turns, G only, 9 questions per model; Nemotron 3 Ultra, Gemma 4 31B, ERNIE 5.5 Flash, Ling 3.2 Flash | Gemma returned HTTP 429 on every request and was dropped; the other three were kept; pilot answers are not results |
| 00:31 to 00:43 | **reader test v4** (`bench/review/protocol-reader-v4.md`), NVIDIA Nemotron 3 Ultra | completed; strict 33, 32 and 33 of 33 (G, F + retrieval, B + archive); 0 unanswered |
| 00:43 to 00:50 | reader test v4, Baidu ERNIE 5.5 Flash | completed; strict 33, 32 and 33; 0 unanswered |
| 00:50 to about 00:59 | reader test v4, inclusionAI Ling 3.2 Flash | **stopped by hand** after 1 question, which was unanswered after 10 attempts and 371 s of upstream 429s; reported as incomplete |
| 01:01:53 | protocol v4.1 committed (1fced24): replace the third reader, selection rule fixed | committed 29 s after the candidate pilot started and before any pilot answer had come back |
| 01:01 to 01:33 | v4.1 pilot: Gemma 4 26B, Particle-1, Flint, LFM-3 1.7B | Gemma 2 of 2 and Particle 2 of 4 unanswered (both hit the 15-minute timeout); Flint 9 of 9 strict; LFM 6 of 9 (three answers were tool calls); Flint chosen by the rule |
| 01:33 to 01:42 | **reader test v4.1**, Recursive Flint | completed; strict 33, 32 and 33; 0 unanswered |

**Expectations:** E1 (at most 1 config-value hedge per strategy) and E2 (strict counts within 3 of G) were met for all three complete readers.

**Stored errors:** the OpenRouter account's `user_id`, which appears in the provider's error bodies, is replaced with `user_redacted` in the stored answers. Nothing else in those files was changed.

### Reader correction after the second review, 2026-11-27 (UTC+7)

An outside review of revision 2 pointed out that the v4 evidence column still credited F with evidence at the question the key error had broken. All $0.

| time | step | outcome |
|---|---|---|
| 07:20 to 07:24 | dry runs with the corrected keys and no model calls, for the v3/v4 configuration and the v1/v2 configuration (`bench/results/2026-11-27/reader-v4-dry-run/`, `reader-v2-dry-run/`) | against the stored records: in v3, v4 and v4.1, one slot of 99 per reader changes (F at turn 151, `constraint_api`, now gets retrieval); in v1 and v2, 3 slots for E and F without retrieval change visibility only. 2 other v1/v2 slots differ because of harness changes made in v3, not the keys |
| 07:27 | protocol v4.2 committed (e18960f) and pushed at 07:29 | asks only the changed slot again, with the three readers that completed v4 and v4.1 |
| 07:29 to 07:30 | **reader test v4.2** | all three readers answered strictly correctly with retrieval appended; E3 met; F with the corrected keys is 33 of 33 for every reader |

The reader tables now show evidence as recorded and with the corrected keys. The strict grade is labeled "strict correct", because it checks for the value and no hedging, not cited evidence.

**The shared miss:** every reader missed the same question, F at turn 151 on the public-API rule. A local reproduction showed that the harness skipped retrieval there because of the visibility-key bug listed below.

### Agent test on LOCA-bench, 2026-11-27 and 2026-11-28 (UTC+7)

All runs used free endpoints, $0. Pilots, amendments and their reasons are recorded in the protocols (`bench/review/protocol-agent-loca-v1.md`, `-v1.1.md`, `-v1.2.md`), and the results are in `docs/agent-test.md`.

| time | step | outcome |
|---|---|---|
| 11-27 morning | pilots on OpenRouter's free Nemotron 3 Ultra: 8K, then 128K with all four strategies | the first pilot was invalid (three MCP servers silently missing under `uv run`); no strategy finished the 128K task, so the test uses 64K |
| 11-27 11:33 | protocol v1 pushed (d12f3d5), scored runs started | **stopped** after two runs: LOCA discarded long replies, and the budgets were unequal |
| 11-27 afternoon and evening | v1.1 pilots: one trim run on OpenRouter (stopped after 31.7 minutes, 26 of 41 replies discarded by the 60-second timeout), then all four strategies on NVIDIA's free tier | NVIDIA runs clean; same prompt counts and the same reply word for word as OpenRouter |
| 11-27 20:40 | protocol v1.1 (e22f79d), scored runs started on NVIDIA | 9 runs executed in order, 8 of them valid |
| 11-27 about 22:15 | DNS outage | the ninth run ended with LOCA's API error and was marked valid by mistake; 153 attempts then failed preflight within 4 minutes, with no model request |
| 11-27 23:46 | protocol v1.2 (b6604bc, 0b94971), schedule resumed | 52 runs, all valid, no void |
| 11-28 08:46 | schedule complete | 60 scored runs; analysis in `bench/results/2026-11-28/loca-v1.2/` |
| 11-28 | outside review of the packet, then revision 2 | all its numbers reproduced; it caught a cost total rounded from rounded parts ($52.87, now $52.88) and 4 runs past the 100-call cap (LOCA checks the cap once per reply); added Holm p-values, a sensitivity analysis without the over-budget state, a session isolation check and the protocol text in the packet, all secondary |
| 11-28 | second outside review, then revision 3 | no numerical error found; added the registered per-run metrics, summary requests in the budget check (both compact strategies sent one of over 75,000 tokens on PayableInvoiceChecker), usage outside the 60 runs, and a direct cross-session test that found a `memory_get_summary_lineage` bug (summary structure, no text, for a known id from another session; no run called it) |

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
| reader v4 (2026-11-27) | the three standing-rule keys (`vendor/`, `package store`, `go vet`) also match ordinary session text, so a lost rule could be counted as visible, and the reader test then skipped retrieval. This inflates the baselines' standing-rule numbers; G pins the rules and is unaffected. Over 5 seeds: F + retrieval 21.9% to 7.3%, E + retrieval 22.0% to 7.7% | 635fcb5: each rule keyed on its own sentence; all suites rerun; only rule-visibility fields changed |
| reader v4 (2026-11-27) | OpenRouter error bodies carry the account's `user_id`, and the reader test stored error text verbatim | 4b2bd32: redacted before storing; `TestRedactAccount`; the stored v4 files were redacted by hand |
| second review (2026-11-27) | the reader's evidence-available column used the same old keys, so it showed evidence for F at turn 151 although the reader never saw the rule | evidence recomputed from dry runs with the corrected keys and shown next to the recorded value; the one affected question asked again under protocol v4.2 |
| agent test v1 (2026-11-27) | LOCA's HTTP client gave up on a reply after 60 seconds and resent the request without telling the agent; 66 of 92 replies in one run were discarded | `loca.patch` waits up to 1,800 seconds (b948fa7, e22f79d) |
| agent test v1.1 outage (2026-11-27) | `schedule.py` looked for an API-error message that LOCA never writes, so a run ended by an API error was marked valid | matches LOCA's own error messages (b6604bc) |
| agent test v1.1 outage (2026-11-27) | preflight downloaded the tokenizer file at the start of every run, so every run failed while DNS was down | the file is cached by `setup.sh` (b6604bc) |
| agent test v1.2, before any run (2026-11-27) | the readiness check only tested that NVIDIA's host accepted a connection, which passed while the model did not answer | the proxy's `/ready` asks the model (0b94971) |
| agent test results (2026-11-28) | `memory_get_artifact` answers "not found" for the sha256 prefix that event headers show, and agents tried it 17 times | not fixed yet; reported in `docs/agent-test.md` |
| agent test results (2026-11-28) | the registered contamination check also matches the task's own input files under `agent_workspace/files/` | not changed after the runs; both readings reported |
