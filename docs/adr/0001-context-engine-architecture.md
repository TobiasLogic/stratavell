# ADR 0001: A standalone, lossless, asynchronous context engine

Status: accepted (2026-11-26)

## Context

The target directory was empty. There was no host agent to extend, so there was no existing inference loop, compaction mechanism, token counter, provider adapter or storage layer to reuse. The only agent on the machine was an Artex binary release (AGPL-3.0, Go, by the author of Norma) with no source code, so nothing could be integrated into it. The project is meant to be released publicly as a universal engine, not as a feature of one framework.

The research notes in `docs/research/` shaped the decision. The points that mattered most:

1. APEC (CompactionBench, Liu et al., Microsoft, preprint) measured that most of its accuracy gain comes from making compaction progressive (append summaries instead of rewriting one big summary), while asynchrony mainly buys latency and costs extra input tokens during the lag. APEC is not lossless: the summarized raw segment is dropped.
2. The Complexity Trap (Lindenbauer et al.) measured that simple observation masking matches LLM summarization on cost and solve rate in four of five SWE-agent configurations, and that its released masking code moves the mask boundary every step, which breaks prompt caching about ten turns back on every call.
3. Benchmarks Are Surprisingly Compaction-Friendly measured that summaries drop process instructions and scope exclusions in about 36% of cases and that rules compound away across repeated compactions because each summary is written from the previous one.
4. LCM and Volt (Ehrlich and Blackman) propose an immutable store, a summary DAG and exact expansion. Reading the Volt source showed gaps between the paper and the code: an unlocked delete-and-reinsert of the context table that can race with appends, a fallback that only guarantees one token of reduction, truncated tool outputs, and no prompt-cache awareness.
5. Norma's noa compaction is model-driven and synchronous; its kept tail can split a tool call from its result, and content can end up in neither the summary nor the view.
6. Anthropic's documentation states that cache entries are written only at breakpoints (at most four), that lookback checks at most 20 earlier positions, and that on preserved-thinking models a thinking block's signature fingerprints everything before it, so a client that rewrites earlier turns must drop the affected thinking blocks instead of resending them.

## Decision

Build palimpsest as a standalone Go engine that owns persistent session state and produces the model's context as a materialized view of that state.

Core choices:

- **Canonical state is an immutable event journal** in SQLite (WAL mode, `synchronous=FULL` by default). Large payloads go to a content-addressed blob store in the same database, so journal, blobs, indexes and summary lineage commit in one transaction. A pluggable `ArtifactStore` can move blob bytes to the filesystem or elsewhere; the metadata stays transactional in SQLite.
- **The context is a view** defined by a generation record: an ordered frontier of summary nodes covering a prefix of the stream, a raw region after it, and a mask cutoff. Views change only at generation commits. Within a generation, rendering is append-only, which keeps the cached prefix stable.
- **Zero-inference reduction runs first.** Oversized tool outputs are externalized at ingestion (before the model first sees them) to a bounded preview with a structured digest and an archive reference. Exact duplicates and unchanged file re-reads are replaced by references. Old observations are masked in epochs, not on a rolling window. Active failure evidence is exempt from masking.
- **Semantic compaction is progressive and asynchronous by default**, in the APEC style, but lossless: summary nodes form a DAG with ordered, exact source edges, and every summarized event stays retrievable by id. Jobs snapshot completed, tool-group-closed ranges and are committed with a compare-and-swap check against the current frontier. Results publish only inside `Assemble`, between inference requests.
- **A deterministic emergency ladder guarantees the budget** without any model call: mask, archive old ranges as deterministic pointer nodes, condense summaries into pointer nodes, mask down to the last turn, archive recent ranges, then shorten the latest turn's observations. If protected content alone cannot fit, `Assemble` returns `ErrProtectedOverflow` instead of dropping it.
- **Protected task state is separate from summaries.** User messages are pinned verbatim once they leave the raw region. Explicit constraints are always pinned. Failures, tests and modified files are extracted from tool results deterministically. Agent-written decisions carry model provenance. Summaries never create user-provenance items.
- **Retrieval is local and bounded**: SQLite FTS5 (porter tokenizer, identifier subwords in an auxiliary column), symbol and path indexes, exact id lookup, reciprocal rank fusion with small reliability, staleness and recency priors. Embeddings are an optional interface and are off by default.
- **The cache-aware layout puts dynamic content at the tail.** Summaries come first and are append-only. Pinned instructions and a task state snapshot follow and change only when the frontier changes. Raw history follows. Task state changes and retrieved evidence go into the final user message, after the last breakpoint. Breakpoints go on the system block, the last summary, the last history block and the previous turn's history block.
- **Integration is layered**: the Go SDK (`engine`, `msg`), an HTTP API with optional Unix socket (`server`, `docs/openapi.yaml`), an MCP server (`mcp`), provider adapters (`adapters/anthropic`, `adapters/openai`, `adapters/ollama`) and a small loop helper (`agentloop`). None of the core packages import an adapter, the server or any agent host.

## Integration boundaries

- The engine never calls the primary model. The host sends the assembled request and reports usage.
- The engine may call a summarizer, through the `Summarizer` interface. The default `summarize.Extractive` needs no model. `summarize.LLM` works with any `Completer` (Anthropic, OpenAI-compatible, Ollama).
- Tool execution belongs to the host. Memory tools are executed by the engine through `Agent.HandleTool`, HTTP `POST .../tools/{name}`, or MCP `tools/call`.
- Full compaction requires the host to route its context assembly through `Assemble`. A host that cannot do that can still use the MCP server in passive mode for archiving and retrieval, but its own context is not managed.

## Consequences

Positive:

- Committed events are never destroyed by compaction; exact recovery is tested after repeated compaction and after SIGKILL.
- No synchronous model call happens on the foreground path in the default configuration.
- The engine runs fully locally: SQLite, an extractive summarizer, lexical retrieval.

Negative or open:

- The metadata store is SQLite only. Custom storage backends are limited to artifact bytes.
- Pure-Go SQLite (modernc) keeps builds free of cgo but makes BM25 scoring slower than C SQLite. Lexical search p95 at about 13k indexed chunks misses the 20 ms target (see `docs/benchmarks.md`).
- The extractive summarizer is deterministic and free but weaker than an LLM summarizer. The benchmarks report both.
- Rewriting earlier turns (masking, summarizing) means thinking blocks of affected turns are dropped, never altered. Hosts that need server-side context editing instead can still use the engine's journal and retrieval, but that path is not implemented here.

## Rejected alternatives

- **A single rolling summary (conventional compaction).** Measured to lose constraints and decisions, and it blocks the foreground.
- **Rolling-window observation masking.** Equivalent token savings, but measurably worse cache reuse (see the `G-rolling-mask` ablation).
- **Model-driven compression (noa).** Puts compaction latency and cost on the primary model. Its protected tail cannot shrink, which caused overflows with large tool outputs.
- **Embedding-first retrieval.** Needs a service or a local model, and no measurement here showed a gain over lexical, symbol and path retrieval for exact recovery.
- **A filesystem-only journal.** Atomicity across journal, blobs, indexes and lineage would need a custom write-ahead protocol. SQLite already provides it.
