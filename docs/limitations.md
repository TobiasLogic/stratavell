# Known limitations

Measured problems and design limits, most significant first.

## Evaluation

- The benchmark sessions are generated with fixed trajectories. They measure what each strategy keeps available and what it costs, not whether an agent solves a real task. No run used real repositories, SWE-bench, CompactionBench (unreleased) or OOLONG.
- The reader test appends retrieval results for G when a fact is not visible. It measures recoverability with a reasonable query, not whether a model would choose to search or pick that query.
- Cache costs come from a simulator of the documented Anthropic rules (breakpoints, a 20-position lookback, minimum cacheable prefix, 5-minute TTL). Provider behavior can differ in ways the documentation does not describe.
- Background summarizer latency in the deterministic runs is a model (0.8 s plus prefill at 4,000 tokens per second plus decode at 60 tokens per second), not a measurement.
- **Standing-rule visibility is matched on the user's own sentence.** Before 2026-11-27, short keys also matched unrelated session text and overstated the baselines' standing-rule numbers; this is fixed and all suites were rerun (see `docs/benchmarks.md`). Exact sentence matching has its own limit: a model-written summary that paraphrases a rule correctly would count as losing it. The deterministic suites use the extractive summarizer, which keeps sentences verbatim, so they are not affected. The reader tests ran with the old keys. A dry run with the corrected keys changes one question per reader in v3 and v4 (F at turn 151), which was asked again under protocol v4.2; in v1 and v2 it changes only the evidence bookkeeping of 3 questions that had no retrieval. The Haiku-summary run cannot be checked without paid calls.
- The real-model summarizer runs used Claude Haiku 5.5 on one seed. The reader runs used Haiku (v1 to v3) and three free models from different labs (v4), on one or two seeds. The variance of those numbers is not known.
- Every reader run reuses the same generated questions: 33 on seed 1 (v3, v4), or 64 on seeds 1 and 2 (v1, v2). The free-model runs therefore test new readers, not new questions.
- Strict reader grading checks for the expected value and no hedging. It does not check that an answer cites its evidence or that its other details are right.
- The thresholds (0.55, 0.70, 0.85) were not tuned. No threshold sweep was run.
- Once the baselines get the same retrieval, Stratavell shows no question-answering advantage in the pre-registered run (28, 27 and 26 of 33 strict-correct answers). Its measured advantages are cost, standing-rule retention, current-failure recall and not blocking. General fact recovery comes from retrieval itself.
- The file-provenance fix in retrieval outputs, found by that run, was re-measured in v4 with three free readers: no config-value hedges remained, and all three strategies were at or near 100% strict. The test is now at its ceiling and no longer separates the strategies.
- Task success on real coding tasks has not been measured. That needs an agent that actually solves tasks with each strategy, with independently executed tests and cost per resolved task.

## Engine

- **Search latency is measured only up to 13k chunks.** With pure-Go SQLite, p95 is 3.1 ms at 3.4k chunks and 6.5 ms at 13.2k chunks (v0.1.1; v0.1.0 had 9.4 and 33.7 ms). Queries made only of very common words still score many rows, and larger corpora have not been measured. A native C SQLite build was 1.85x faster on the same SQL, but it is not used, to keep the build free of cgo. The latency profile uses eight queries on one generated session. The regression check for the v0.1.1 search change used seed 1 of three suites. Neither is a broad evaluation of search quality or latency.
- **Large tool outputs take tens of milliseconds to ingest.** A 160 KB test log takes about 36 ms to append, mostly regex work for the digest and path extraction. Small events take under 1 ms.
- **The metadata store is SQLite only.** `ArtifactStore` moves blob bytes elsewhere, but events, indexes and lineage stay in SQLite. With an external artifact store, a blob written just before a rolled-back transaction is left behind as an orphan file, because garbage collection only knows about blobs that have database rows.
- **Token counting is an estimate.** Usage reports from the provider calibrate it upward only. Anthropic documents about 30% more tokens per text on its newer tokenizer, and the default estimator is conservative for code and logs but has not been checked against every tokenizer. `Tokenizer` is pluggable.
- **Thinking blocks are dropped from rewritten turns.** When masking or summarization changes anything before an assistant turn, that turn's thinking blocks are no longer sent. This follows the provider rules, but the model loses its earlier reasoning for those turns. Server-side context editing, which keeps thinking valid, is not used.
- **Pinned user directives grow with the number of user messages.** Long sessions with many user messages approach `protected_fraction`; auto-detected directives are then shortened to 60 tokens with a reference. Explicit constraints are never shortened, and overflowing them is an error.
- **The extractive summarizer is weak.** It keeps decision, rejection, failure and constraint lines and the first line of each event; it does not reason. It exists so the engine runs with no model. In the benchmarks, strategies that depend on summaries alone lose most planted facts with either summarizer.
- **Directive detection is pattern-based.** "Never", "must", "do not" and similar words mark user sentences as directives. Paraphrased or implicit rules in very long user messages may be missed; short user messages are pinned whole, so this only affects messages over `pin_max_tokens`.
- **Embeddings are not evaluated.** The `Embedder` interface works and its failures degrade to lexical search, but no experiment showed a benefit, so it is off by default and the vector search is a brute-force scan.
- **Retention purge keeps derived text.** `Agent.Purge` removes event payloads and their index rows, but published summaries derived from them remain unless `KeepSummaries` is false, which only clears unpublished summaries. Full removal of a session's content is `DeleteSession` followed by a vacuum.
- **The FTS5 integrity false positive.** Long-lived pooled connections reported FTS5 corruption under `PRAGMA integrity_check` while queries on them returned correct results (modernc SQLite 3.54.1). Verification uses a fresh connection. The root cause was not investigated further.
- **One process per database.** Two engines opening the same file at once are not supported. Use the HTTP API to share one engine.

## Integration

- Passive MCP mode cannot compact the host's context. It only archives what the model chooses to archive and serves retrieval.
- The Anthropic adapter was validated against the documented request shape and tested offline. No live Anthropic API call was made, because no Anthropic key was available. The OpenAI-compatible adapter was exercised live through OpenRouter.
- There is no streaming adapter. Hosts that stream should assemble the final assistant message, including thinking signatures, before calling `Append`.
- The code has no doc comments, by the author's style choice; the API is documented in `docs/`.
- The Ollama adapter has no automated tests; it was used for one live local call.

## Security and deployment

- A bearer token gives access to every session in the database. There are no per-session credentials, roles or audit of reads. Use separate servers and databases for separate trust domains.
- The HTTP server has no TLS. Remote access needs a TLS-terminating proxy.
- The database is not encrypted at rest.
- Labels on archived content ("data, not instructions") reduce prompt-injection risk from stored tool outputs, but do not remove it.
- See `SECURITY.md` for the full threat model.

## Platforms

- Tested locally on Linux amd64 with Go 1.26.11 (the minimum in `go.mod`) and 1.27.4, including the full race-detector run.
- CI runs the test suite on GitHub-hosted Linux, macOS and Windows runners. The v0.1.0 run passed on Linux and macOS. On Windows every package passed except one store test that creates a directory named `c?d`, which Windows does not allow. That test now skips the name on Windows (after the tag).
- The crash-recovery test is skipped in `-short` mode, so on macOS and Windows it has not run yet.
- Release binaries are cross-compiled for Linux, macOS and Windows on amd64 and arm64. CI test runs cover Linux amd64, Windows amd64 and macOS arm64; the Linux arm64, Windows arm64 and macOS amd64 builds are not tested.

## Project

- The API is pre-1.0 and may change; see `docs/stability.md`.
- The project was renamed from palimpsest for v0.1.0. Legacy environment variables and the legacy default database name still work, with warnings, and will be removed in a later release.
