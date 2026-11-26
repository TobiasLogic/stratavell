# Known limitations

Measured problems and design limits, most significant first.

## Evaluation

- The benchmark sessions are generated with fixed trajectories. They measure what each strategy keeps available and what it costs, not whether an agent solves a real task. No run used real repositories, SWE-bench, CompactionBench (unreleased) or OOLONG.
- The reader test appends retrieval results for G when a fact is not visible. It measures recoverability with a reasonable query, not whether a model would choose to search or pick that query.
- Cache costs come from a simulator of the documented Anthropic rules (breakpoints, a 20-position lookback, minimum cacheable prefix, 5-minute TTL). Provider behavior can differ in ways the documentation does not describe.
- Background summarizer latency in the deterministic runs is a model (0.8 s plus prefill at 4,000 tokens per second plus decode at 60 tokens per second), not a measurement.
- The real-model runs used one reader and summarizer (Claude Haiku 5.5) and one or two seeds; the variance of those numbers is not known.
- The thresholds (0.55, 0.70, 0.85) were not tuned. No threshold sweep was run.

## Engine

- **Lexical search slows with corpus size.** On the pure-Go SQLite build, p95 is 8.8 ms at 3.4k chunks and 33 ms at 13.2k chunks. The cost is BM25 scoring on the OR fallback. A cgo SQLite build or a chunk-recency cap would help; neither is implemented.
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
