# Architecture

This document describes how Stratavell stores a session, turns it into a model request, and keeps that request inside the budget. The decision record is `docs/adr/0001-context-engine-architecture.md`.

## Packages

| package | role |
|---|---|
| `msg` | provider-neutral messages, parts, tool-pairing validation |
| `engine` | the SDK: journal, views, masking, summary DAG, scheduler, assembler, task state, retrieval, memory tools, metrics, deletion, verification |
| `internal/store` | SQLite connections (one writer, a read pool), migrations, content-addressed blobs |
| `internal/textproc` | zero-inference text processing: log digests, previews, symbols, paths, directive sentences |
| `internal/tokens` | conservative token estimator and usage-based calibration |
| `summarize` | `Extractive` (no model), `LLM` (any `Completer`), test helpers |
| `adapters/anthropic`, `adapters/openai`, `adapters/ollama` | render an assembled context into a provider request, parse responses, HTTP clients |
| `agentloop` | a minimal step function for hosts that want one |
| `server` | HTTP API over TCP and Unix sockets |
| `mcp` | MCP server over stdio |
| `cmd/stratavell` | standalone executable |
| `bench`, `cmd/stratavell-bench` | workloads, baselines, cache simulator, reader test |

The engine imports none of the adapters, the server, the MCP server or the benchmark code.

## Data model

A **session** holds **agents**. Each agent owns an append-only stream of **events** with a monotonic sequence number. An event is one message: a user text message, an assistant message (text, thinking blocks, tool calls), or one tool result. Every tool result references the assistant event that issued its call (`parent_id`, `group_id`), so tool groups can be kept whole.

Each event row stores the original parts as JSON. Any text, tool input or opaque provider data larger than 2 KiB moves to a blob, keyed by SHA-256 of the raw bytes and referenced from the part. `content_hash` is SHA-256 of the canonical JSON of the original message, which is how exact recovery is verified.

Derived data, all rebuildable or replaceable:

- `docs` and `docs_fts`: retrieval chunks (FTS5 with porter stemming; an `aux` column carries identifier subwords, paths and tool names; a `sid` column scopes queries to a session)
- `paths`, `symbols`: exact lookup indexes
- `summaries` and `summary_sources`: the summary DAG with ordered source edges
- `task_items`: versioned task state with provenance and supersession
- `views`: generation records (frontier, boundary, mask cutoff)
- `jobs`: compaction jobs and their outcomes

The full schema is in `docs/schema.md`.

## Invariants

1. Committed events are immutable. They disappear only through `DeleteSession` or `Agent.Purge`, which are explicit and logged.
2. A summary node covers a contiguous sequence range of one agent stream. A leaf node's sources are exactly the events in that range, in order. A condensed node's sources are adjacent nodes. `Verify` checks that every frontier node expands to exactly `hi - lo + 1` events of the same stream, in sequence.
3. The frontier covers `[1, boundary]` without gaps. The raw region is `(boundary, head]`.
4. Compaction never splits a tool group. A cut is legal only where no tool call is open and the next event is not a tool result.
5. A background result publishes only if its sources are still where it expects them: a leaf whose first sequence is `boundary + 1`, a pointer it upgrades over the exact same range, or a run of frontier nodes it condenses. Anything else is marked stale.
6. Publication happens inside `Assemble` under the agent lock, so an in-flight request is never changed.
7. Rendering within one generation is a function of the event and of state frozen at the generation commit, so earlier blocks never change between generations.
8. Thinking and redacted-thinking blocks are emitted only for unmodified assistant messages appended in the current generation. They are never edited.
9. The assembled request never exceeds the effective input capacity, after calibration. If the protected content alone cannot fit, the call fails with `ErrProtectedOverflow`.
10. Every query is scoped by session. An agent can see its own events, its descendants' events, events marked shared, and items explicitly granted to it.

## The append path

`Agent.Append`:

1. Validates the message shape and steps a copy of the tool-pairing state. Orphan results, unanswered calls and duplicate call ids are rejected.
2. Infers the content type (file read, file write, command, test, search, retrieval) from metadata and tool names, and pulls the command and paths from the tool call input.
3. Computes derived metadata:
   - a log digest for command and test output (assertion lines first, then other error lines, failing test names, locations, summary line)
   - code symbols for file contents and messages (for command output, the failing test names stand in for symbols)
   - paths from the first 64 KB
   - a file hash for reads
   - a preview if the output is over `max_observation_tokens`, except for results of memory tools
   - for long outputs, a one-line description of their structure when they are JSON or a table
4. Writes blobs, the event, blob references, path and symbol rows, and task items in one transaction, then advances the agent head.
5. Updates in-memory state, queues the event for indexing, and asks the scheduler whether background compaction should start.

Indexing runs on a background goroutine by default. Search waits up to 250 ms for pending indexing, which gives read-your-writes behavior for normal use.

## Rendering an event

Within the raw region, each event renders in one of these modes:

| mode | when | what the model sees |
|---|---|---|
| full | default | the original parts |
| fresh | off by default; with `fresh_full_fraction` above 0, an output over `max_observation_tokens` that arrived after the model's last message, when the new outputs fit in that fraction of the capacity and the context stays under the aggressive threshold | the original parts, once |
| preview | output over `max_observation_tokens`, decided at ingestion | header with id, tool, command, path, exit code, size and hash; the structure, for JSON and tables; the digest; head and tail lines; how to fetch the rest |
| unchanged | a re-read of a file whose hash matches an earlier visible read with no write in between | one line pointing at the earlier read |
| duplicate | a non-file observation identical to an earlier visible one | one line pointing at the earlier event |
| masked | observation at or before the mask cutoff | header plus the first failing tests and error lines and the event id |
| input masked | old assistant tool call with a large input | the input replaced by an object that keeps short fields such as `path` and `command` |

Observations that are the latest evidence of an open failure are exempt from masking and render as previews.

## Assembly

`Agent.Assemble` builds the next request:

1. Refuses while tool calls are open.
2. Publishes ready background results (in sequence order) and handles failed jobs.
3. Measures the parts: fixed (system, engine notice, tools, provider overhead), summaries, pinned instructions, task state, history, evidence.
4. Synchronous mode only: summarizes in the foreground once usage reaches the soft threshold.
5. Starts a masking epoch at the aggressive threshold.
6. Runs the emergency ladder at the emergency threshold.
7. Builds the message list and breakpoints, validates tool pairing in the result, records metrics, and schedules background compaction for the next turns.

Layout of the result (default `stable-tail` layout):

```
system:   [host system prompt] [engine notice]                          <- breakpoint
message 1 (user): [summary 1] ... [summary k]                           <- breakpoint
                  [standing instructions] [task state snapshot]
                  [first raw user message, if the raw region starts with one]
messages 2..n:    raw history, rendered                                 <- breakpoint on the last block
                                                                        <- second breakpoint on the previous turn's last block
final user message: [task state changes] [retrieved evidence]
```

Summaries are appended, so a publish keeps the cached prefix up to the previous last summary. The standing instructions and the task state snapshot change only when the frontier changes, which is also when everything after the summaries is rewritten anyway. Task state changes and retrieved evidence sit after the last breakpoint, so they never invalidate cached history.

The `spec` layout from the original brief (task state and evidence before the raw history) is available for comparison. In the benchmarks it costs about 70% more input money for the same tokens, because the earliest cacheable region changes every turn.

## Budget and thresholds

Effective capacity:

```
capacity = context_window - max_output_tokens - reasoning_tokens - max(safety_tokens, safety_fraction * context_window) - provider_overhead
```

Usage is the assembled size divided by capacity, after the calibration ratio learned from provider usage reports (`Agent.ObserveUsage`). The ratio only ever inflates estimates.

| threshold | default | action |
|---|---|---|
| target | 0.40 | how far background compaction tries to bring usage down |
| soft | 0.55 | start background leaf summaries; also started early when predicted time to emergency is under 1.5 times the summarizer latency |
| aggressive | 0.70 | masking epoch, zero inference |
| emergency | 0.85 | deterministic emergency ladder, zero inference |

These are starting values, not tuned optima. Masking also advances at every summary publish, because the region after the summaries is rewritten then anyway.

## Background compaction

Leaf jobs cover the oldest raw events outside the recent window (about 20% of capacity and at least two assistant turns), cut at safe boundaries, about `leaf_chunk_tokens` each. Their summarizer input is the rendered summarizer view: full messages, with large observations reduced to header, digest and a short head and tail. Condense jobs merge the oldest same-level run of frontier nodes when the frontier exceeds `summary_fraction` of capacity or `condense_max_nodes`.

The scheduler keeps a FIFO queue per agent, serves agents round-robin, bounds in-flight jobs per agent and queued jobs overall, deduplicates jobs by range, and applies a per-minute request and token gate that can keep a share of the provider budget for the foreground model. Jobs have timeouts, exponential retry with jitter, and one stricter retry when a summary does not shrink its source to `min_reduction`. A summary that still fails validation becomes a deterministic pointer node. After restart, queued and running jobs are rebuilt from the current view if their range is still eligible, or marked abandoned.

Only agents that have called `assemble` at least once are summarized in the background, since a host that keeps its own context never shows the summaries to its model; `passive_summaries` changes this. `CloseSession` cancels a session's queued and running jobs and marks them cancelled, so a run that has ended does not keep paying for summaries. Each summarizer call carries its job id and attempt in the context, and the bundled adapters send them as `X-Stratavell-Job` and `X-Stratavell-Attempt` headers.

## Emergency ladder

All steps are deterministic and monotonic:

1. Advance the mask cutoff to the start of the recent window.
2. Archive old raw ranges as pointer nodes. A pointer lists the event ids, tools, files, failures and a sampled index of the range. A semantic summary for the same range that arrives later replaces the pointer at the next publish.
3. Condense the oldest summaries into a pointer node.
4. Advance the mask cutoff to just before the last assistant turn.
5. Archive everything before the last assistant turn.
6. Shorten the latest turn's observations and tool inputs for this request only.

If the request still does not fit, `Assemble` returns `ErrContextOverflow` with a breakdown of where the tokens are.

## Task state and protected content

Task items come from four sources, each with its own provenance:

- **user**: every user message becomes a directive; the first one is also a goal; messages flagged `constraint` and `AddConstraint` create explicit constraints (shared across the session's agents)
- **tool**: modified files, test results per command, open and resolved failures per command
- **model**: plan steps, decisions, rejected approaches, findings and notes written through `memory_update_task`
- **system**: subagent assignments and completions

Items are versioned by kind and key. A newer item supersedes the current one. A lower-provenance write to a key held by a higher-provenance item, or a write with a stale `expected_version`, is stored as a conflict instead of overwriting it. Summaries can never create user items, and tool output can never create constraints.

Pinned instructions are directives and constraints whose source event is no longer verbatim in the raw region. Explicit constraints are never shortened. Auto-detected directives are shortened first if the protected budget (`protected_fraction`) is exceeded.

## Retrieval

`memory_search` combines four candidate lists:

- **FTS5:** session-scoped, AND of the query terms first and OR when that finds fewer than 12 chunks. Terms that appear in more than 5% of chunks are dropped, using a document-frequency estimate maintained in memory.
- **Symbol index:** exact matches on identifiers.
- **Path index:** reads and writes ahead of mentions.
- **Exact ids.**

Reciprocal rank fusion (k = 60) merges the lists. Small priors adjust for source reliability, files changed since they were read, and recency. Results are bounded snippets with provenance labels and an "already in context" flag.

Expansion and retrieval tools page their output under a token budget. `memory_get_event` pages are sized in tokens, and `max_tokens` asks for a larger page within the room left under the aggressive threshold. Results of memory tools are never externalized, so a page the model asked for stays in its context.

`memory_query` reads one archived output as records instead of pages: the largest list in JSON (or the one named by `path`), rows of a CSV, TSV or Markdown table, or lines of text. It filters with `where`, keeps `fields`, sorts, and greps, and returns compact JSON records within the result budget.

`memory_get_artifact`, `memory_get_event` and `memory_query` accept an event id, a full sha256 hash, or a hash prefix of at least 8 hex characters, resolved only among outputs the caller can see.

Every tool result is labeled as archived data.

Loop control:
- the same memory call repeated within six turns is answered with a pointer to the earlier result; arguments are compared after parsing, so default values spelled out do not make a new request
- a `memory_get_event` call for an output that the last context included in full is answered with a notice
- each turn has a call budget

## Multi-agent model

`Session.Spawn` creates a child agent with its own stream and view. The handoff is a short protected brief (task, constraints, granted event ids), not the parent transcript. The parent can read its descendants' events; children see only their own events, shared items and explicit grants; siblings are isolated. `Agent.Complete` records shared findings, grants the parent the evidence refs the child can see, and returns a handoff text for the parent's tool result. Each agent has its own lock, budgets and scheduler queue; SQLite serializes writes.

## Security notes

- Archived content, summaries and retrieved evidence are wrapped in labeled blocks and described as data in the engine notice. The engine's own tag names are escaped when they appear inside tool output, summaries or user text placed in those blocks.
- The HTTP server binds to loopback by default and refuses a non-loopback address without a bearer token. It compares tokens in constant time and limits request bodies to 64 MiB. Without a token it answers TCP requests only when they are addressed to a loopback host and carry no foreign `Origin`, which blocks DNS rebinding and cross-site requests. Unix sockets are created with mode 0600, and an existing path is replaced only when it is a stale socket. The full threat model is in `SECURITY.md`.
- Metrics and logs never contain payloads.
- Blob access requires a reference from an event the agent may see, so content-addressed deduplication across sessions does not expose content across sessions.
