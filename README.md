# palimpsest

palimpsest is a context engine for long-running LLM agents. It keeps every message, tool call and tool output in an immutable local journal, and builds each model request as a view of that journal:
- recent work verbatim
- older work as summaries and archived observations
- standing user instructions pinned
- everything else retrievable exactly, by id

Compaction runs in the background and never blocks a turn. The request it produces is shaped so that prompt caches keep working.

It is a Go library with an HTTP API, a Unix socket, an MCP server and a standalone executable. It runs fully locally: SQLite for storage, an extractive summarizer that needs no model, lexical retrieval. Model-backed summarizers (Anthropic, OpenAI-compatible, Ollama) are optional.

## What it does

- **Lossless.** Every committed event is stored with a content hash and can be recovered byte for byte, after any number of compactions and after a crash. Summaries are derived views linked to their exact sources in a DAG.
- **Zero-inference first.** Oversized tool outputs become bounded previews with a log digest (failing tests, assertion lines, locations) before the model first sees them. Duplicates and unchanged file re-reads become references. Old observations are masked in batches, never one turn at a time, so cached prefixes survive. Evidence for an open failure is never masked.
- **Progressive and asynchronous.** Summaries of completed, tool-group-closed ranges are produced by background workers and published between requests with a compare-and-swap check. Late or conflicting results are rejected; events appended meanwhile are untouched.
- **Never over budget.** A deterministic ladder (mask, archive as pointer nodes, condense, shorten) guarantees the request fits without calling a model. If protected content alone cannot fit, you get an explicit error instead of silently dropped instructions.
- **Instructions survive.** User messages are pinned verbatim once they leave the recent window; explicit constraints are always pinned. Task state (open failures, tests, modified files, decisions) is tracked with provenance and kept separate from summaries.
- **Exact retrieval.** The model gets memory tools: `memory_search`, `memory_expand`, `memory_get_event`, `memory_get_artifact`, `memory_get_task_state`, `memory_get_summary_lineage`, `memory_stats` and `memory_update_task`. They search with FTS5, symbol and path indexes and exact ids, fused by reciprocal rank, and are scoped to what the agent may see.
- **Cache-aware layout.** Summaries are append-only at the front. Pinned instructions and a task-state snapshot change only when the summary frontier changes. Per-turn dynamic content sits after the last cache breakpoint. Thinking blocks are passed back unchanged or dropped, never edited.
- **Multi-agent.** Subagents get their own streams and a short provenance-linked handoff instead of the parent transcript. Isolation, grants and shared findings are enforced by the engine.

## Results

On generated long coding sessions (300 turns, 2.9 to 3.2 million raw tokens each, 64k-token window, 5 seeds), against reimplementations of common strategies:

| strategy | requests that fit | input cost per session | planted facts recoverable | standing user rules kept | blocking per session |
|---|---|---|---|---|---|
| conventional compaction (proxy) | 81% | n/a | 26% | 62% | 825 s |
| noa (reimplementation) | 35% | n/a | 10% | 29% | 556 s |
| sync progressive summaries | 100% | $11.41 | 12% | 21% | 323 s |
| async progressive summaries | 100% | $13.04 | 13% | 22% | 0 |
| async summaries + retrieval | 100% | $12.88 | 97% | 22% | 0 |
| **palimpsest** | **100%** | **$6.43** | **100%** | **100%** | **0** |

"n/a": not every request fit the window, so the cost is not comparable. Costs are simulated input costs at Claude Sonnet 5.5 prices, from a model of Anthropic's documented prompt-cache rules.

What the comparisons show:

- **Retrieval drives recovery.** Giving the summary-based baselines the same retrieval tools brings their fact recovery close to palimpsest's.
- **What stays palimpsest's own:**
  - about half the input cost, mostly from keeping oversized tool outputs out of the context and from cache-aware ordering
  - standing user rules kept verbatim
  - the current failure kept in task state
- **Question answering ends in a tie.** In a pre-registered question-answering run with a real model (Claude Haiku 5.5), palimpsest answered 28 of 33 questions with a grounded answer. The two component-matched baselines answered 27 and 26. That run also exposed missing file paths in retrieval output, which is now fixed but not re-measured.
- **Task success on real coding tasks has not been evaluated yet.** The sessions are scripted, and the costs come from a simulator, not provider bills.

Method, every table (200k window, component-matched baselines, eleven ablations, cost accounting), missed targets and an external review with the responses to it are in `docs/benchmarks.md`. `REVIEW_PACKET.txt` is the same material as one text file for outside review. `docs/experiments.md` lists every run, including failed and superseded ones.

## Quick start

Requires Go 1.26 or newer. No cgo.

```
go install github.com/TobiasLogic/palimpsest/cmd/palimpsest@latest
palimpsest serve --db memory.db --socket $XDG_RUNTIME_DIR/palimpsest.sock
```

Go:

```go
e, _ := engine.Open(ctx, engine.Options{Path: "memory.db", Summarizer: summarize.Extractive{}})
defer e.Close()
s, _ := e.OpenSession(ctx, "task-42", engine.SessionConfig{})
a, _ := s.Agent(ctx, "main")

a.Append(ctx, msg.Text(msg.RoleUser, "Fix the flaky test. Never edit vendor/."))
cx, _ := a.Assemble(ctx, engine.AssembleRequest{System: system, Tools: tools, IncludeMemoryTools: true})
req, _ := anthropic.Render(cx, anthropic.Options{Model: "claude-sonnet-5-5", MaxTokens: 8192})
```

Send `req`, report usage with `a.ObserveUsage`, append the assistant message as returned, run its tool calls (memory tools through `a.HandleTool`), append the results, and repeat. `agentloop.Step` does exactly that.

HTTP, from any language:

```
POST /v1/sessions/task-42/agents/main/events     {"messages": [...]}
POST /v1/sessions/task-42/agents/main/assemble   {"format": "anthropic", "model": "...", "include_memory_tools": true}
```

MCP, for hosts whose context you cannot intercept (archiving and retrieval only):

```
palimpsest mcp --db memory.db --session my-project
```

Examples in Go, Python, TypeScript and Rust are in `examples/`.

## Documentation

| document | contents |
|---|---|
| `docs/integration.md` | full interception versus passive MCP, the per-turn loop, provider formats, subagents, custom components |
| `docs/openapi.yaml` | the HTTP API |
| `docs/architecture.md` | data model, invariants, rendering, assembly, emergency ladder, task state, retrieval, multi-agent model, security |
| `docs/adr/0001-context-engine-architecture.md` | why it is built this way |
| `docs/configuration.md` | every policy field and its default |
| `docs/schema.md` | storage schema, migrations, integrity checks |
| `docs/operations.md` | running, metrics, retention and deletion, backup, compatibility and rollback, profiling |
| `docs/benchmarks.md` | method and results |
| `docs/experiments.md` | every run, cost and bug found |
| `docs/limitations.md` | known limitations |
| `docs/research/` | notes on the papers and code this design builds on |

## Status

Version 0.1.0, API v1. Tests cover:
- exact recovery after repeated compaction and after SIGKILL
- tool pairing and capacity under property tests
- concurrency and subagent isolation under the race detector
- prompt injection from stored content
- deletion, configuration migration and deterministic replay
- fuzzing of messages, digests, search queries and append/assemble sequences

The Anthropic adapter is tested offline against the documented request shape; the OpenAI-compatible adapter was used live through OpenRouter. Isolation and protocol guarantees are backed by tests: no failures observed in the tested configurations, which is not a proof.
