# Changelog

All notable changes are listed here. The project follows [Semantic Versioning](https://semver.org/). Until 1.0, minor releases may change the Go API, the HTTP API, the MCP tool set or the database schema; every such change is listed here with migration steps.

## Unreleased

Retrieval and background-summary changes from the LOCA-bench agent test (`docs/agent-test.md`). In that test, Stratavell's agent spent 291 of its 325 memory calls reading archived outputs page by page, and 17 of its artifact lookups failed. Behavior changes are listed with the setting that restores the old behavior.

### Retrieval

- **Retrieval results stay visible.** In the test, 247 of the 351 memory-tool results were over the 6,000-token observation limit, so Stratavell replaced the page the agent had just asked for with a short preview.
  - Results of memory tools are no longer externalized.
  - `memory_get_event` pages are sized in tokens (`max_result_tokens`, 6,000), not bytes. The old 18,000-byte page of dense JSON came to about 7,700 tokens.
  - A page ends at a line break when one is near the end.
- **`memory_get_event` takes `max_tokens`,** up to 4 times `max_result_tokens`, limited by the room left under the aggressive threshold. The reply says when the limit applied.
- **New tool `memory_query`** selects records from one archived output instead of paging through it:
  - JSON: it finds the largest list of records, or the one named by `path`. A list of rows with a header row, as spreadsheet APIs return it, becomes records keyed by the header. JSON inside a Python-style wrapper such as `result='...'` is read too.
  - CSV, TSV and Markdown tables
  - plain text, by line, with `grep` and `context`
  - `where` with `value`, `~part`, `>n`, `>=n`, `<n`, `<=n` or `!=v` on dotted field names; `fields`; `sort` (`-field` for descending); `offset` and `limit`. Records come back as compact JSON, one per line, within `max_result_tokens`.
  - With only an id, it lists the fields and the first records.
- **Structure hints.** Previews and masked notes of large structured outputs state their shape, for example "JSON, 120 records; fields: id, name, sku, ...", and point to `memory_query`.
- **Hash prefixes resolve.** Event pages and notes show `sha256` and 12 hex characters, but `memory_get_artifact` matched only full blob hashes, and the hash in an event header was never a blob hash. Every such lookup in the test failed.
  - `memory_get_artifact`, `memory_get_event` and `memory_query` now accept a full hash or a prefix of at least 8 hex characters, with or without `sha256:`.
  - A prefix is matched only against outputs the caller can see. When it matches outputs with different content, the reply asks for a longer prefix.
- **New option to show new outputs once in full.** With `masking.fresh_full_fraction` above 0, an oversized output the model has not answered yet goes into the next context in full, if the new outputs together fit in that share of the capacity and the context stays under the aggressive threshold. After the model's next message it is previewed.
  - It is off by default. Turning a full message into a preview on the next turn breaks the prompt cache: at 0.20, G's cost in the 64k suite rose by about 18% on seeds 1 and 2, with the same probe results. Keeping such outputs in full until the next mask epoch cost 25 to 33% more.
  - It is meant for real agents, where seeing an output once may save retrieval calls. That has not been measured yet.
- **Repeated requests.** The repeat check compares parsed arguments, so `{"id": x}` and `{"id": x, "offset": 0}` count as the same request. Asking `memory_get_event` for an output that the last context included in full returns a short notice instead of a second copy.

### Background summaries

- **Agents that never assemble are not summarized.** A host that keeps its own context and only archives into Stratavell never shows a summary to its model, yet in the test such runs made 136 summary requests. They are now skipped. `compaction.passive_summaries: true` restores them.
- **Closing a session cancels its jobs.** `POST /v1/sessions/{sid}/close`, `Engine.CloseSession`, `Session.close()` in Python and `closeSession()` in TypeScript cancel the session's queued and running summary jobs, record them as `cancelled` and unload the session. Its data stays, and the next request reopens it. `DeleteSession` cancels the session's jobs first, too.
- **Summary requests are tagged.** The OpenAI, Anthropic and Ollama adapters send `X-Stratavell-Job` and `X-Stratavell-Attempt` with each summary request. `SummarizeInput` carries `JobID` and `Attempt`, and `engine.JobTagFrom(ctx)` reads them in a custom summarizer.
- **Summary use is counted.** `memory_stats` and `Agent.Stats` report how many summaries were shown in an assembled context, expanded, or returned by search since the agent was loaded.

### Tool definitions

- **`include_memory_tools` sends six core tools:** `memory_search`, `memory_query`, `memory_get_event`, `memory_get_artifact`, `memory_expand` and `memory_update_task`.
  - `memory_get_task_state`, `memory_get_summary_lineage` and `memory_stats` are still answered and listed over MCP. `all_memory_tools` (`AssembleRequest.AllMemoryTools`) sends them too.
  - No agent in the LOCA test called them, and the task state is already in the context.
- **Shorter descriptions and schemas.** Arguments that the schemas no longer list, such as search's `roles` and `seq_from`, are still accepted.
- **Measured with the engine's estimator,** the memory tools and the engine notice take 1,140 tokens per request by default, down from 1,335, including the new tool. With all tools, 1,312.

## v0.1.3, 2026-11-28

Security patch release. No API, MCP tool set, database schema or configuration changes.

### Security

- **`memory_get_summary_lineage` checked access only for event ids.** v0.1.2 and earlier are affected, through the Go SDK, HTTP, the Unix socket and MCP.
  - Given a summary id from another session, or from a sibling agent in the same session, it returned that summary's structure: kind, level, sequence range, number of source events, and the ids of related summaries.
  - It returned no summary text and no event content.
  - It now gives the same "not found" reply as for a summary that does not exist. Its child and covering-summary lists leave out summaries the agent cannot see.
  - Found by a cross-session probe during the LOCA-bench agent test.
- **`memory_expand` refused such a summary with a different message** than a summary that does not exist, so the reply showed that the id exists. Both now get the same reply. It also leaves out child summaries and events the agent cannot see.
- Regression tests cover the engine, the HTTP API and the MCP server.

### Benchmark

- **Fixed standing-rule visibility keys.** The three standing rules were detected by short keys (`vendor/`, `package store`, `go vet`) that also match ordinary session text: the vendor file listing, the first line of Go files in package `store`, and the `go vet ./...` command.
  - A strategy that had lost a rule could therefore still be counted as showing it, and the reader test then skipped retrieval.
  - Each rule is now detected by its own sentence. The graded answers are unchanged.
  - The deterministic suites were rerun. Only rule-visibility fields changed: standing-rule retention, and probe visibility, grounding and recoverability for those rules. Every token, cache and cost field is identical.
  - `bench/testdata/smoke-golden.json` was regenerated for this change.
- **Reader test options.** `-reader-only strategy@turn:fact,...` asks only the listed questions. `-reader-dry-run` runs the reader test with no model and records what each question would see.
- **Reader evidence corrected.** The reader tables show evidence as recorded and with the corrected keys. The only reader question whose input the key fix changes (F at turn 151, the public-API rule) was asked again under protocol v4.2.
- The reader's strict grade is now labeled "strict correct" instead of "grounded answer": it checks for the expected value and no hedging, not cited evidence.

## v0.1.2, 2026-11-26

Packaging release. The engine is unchanged from v0.1.1.

- Docker image `ghcr.io/tobiaslogic/stratavell`, for linux/amd64 and linux/arm64. It is static, runs as a non-root user and is about 25 MB. Its default command is the MCP server over stdio, with the archive in the `/data` volume.
- MCP Registry entry `io.github.TobiasLogic/stratavell` (`server.json`), published from GitHub Actions with OIDC.
- Python client `stratavell` (`clients/python`), standard library only, for Python 3.9 or newer, over TCP or a Unix socket.
- TypeScript client `stratavell` (`clients/typescript`), with no runtime dependencies and with type definitions. It uses the global `fetch` and works in Node 18 or newer, Deno, Bun or browsers. It no longer assumes `process` exists.
- The Python and TypeScript examples use these clients instead of their own copies.
- CI checks that the Go, Python, npm and `server.json` versions agree. It builds and smoke-tests both packages against a live server, builds the image and runs an MCP handshake inside it.
- `docs/releasing.md` describes the release and the one-time registry setup.

## v0.1.1, 2026-11-26

Patch release: search latency. No API, database schema or configuration changes.

### Search

- Fixed term pruning. Before, a query with one or two rare words was refilled with the rarest remaining words even when those appeared in most chunks, so full-text search scored up to 75% of the corpus.
- Two-letter words such as `go` are now counted as common words when they are.
- A query that is a single file path uses the exact path index and skips full-text search when that index has hits.
- The stale-read check runs as one `EXISTS` query per search instead of one or two queries per candidate.
- `Engine.Vacuum`, and so `stratavell vacuum`, merges FTS5 index segments first, so it takes slightly longer.
- Measured on one generated 1,200-turn session with eight queries, with v0.1.0 and v0.1.1 on the same machine in the same session:
  - p95 at 13.2k chunks: 35.8 ms to 6.5 ms, an 81.8% reduction (v0.1.0's documentation reported 33.7 ms from an earlier run)
  - p95 at 3.4k chunks: 10.6 ms to 3.1 ms
  - p95 at 13.2k chunks on a freshly reopened database (new engine and connections): 76.5 ms to 14.6 ms
- Still pure Go, with no new dependencies.

### Behavior change: common-term fallback

- **Before:** when a query's rare words matched nothing, the full-text fallback still searched its common words. `value_42 compute` returned chunks that merely contained "value" or "compute".
- **Now:** words in more than 20% of chunks are dropped whenever a rarer word is present, so a query whose rare words match nothing returns no full-text hits.
- **Unchanged:** a query made only of common words still searches its one or two rarest words. Exact path, symbol and id lookups are unaffected.

### Verification

- Seed 1 of the 64k main, component-matched and ablation suites (26 strategy runs) is identical to the published results in every deterministic field, and `TestDeterministicSmoke` is unchanged.
- New regression tests fail on v0.1.0 and pass on v0.1.1.

## v0.1.0 (Research Preview), 2026-11-26

First public release. The project was developed under the working name palimpsest and renamed to Stratavell for this release.

### Engine

- Append-only SQLite journal (WAL, fsync on every commit) with a SHA-256 per event and content-addressed blob storage.
- The context sent to the model is rebuilt each turn as a view of the journal. Every committed event stays exactly retrievable.
- Zero-model reductions:
  - oversized tool outputs are externalized at ingestion as previews with a log digest
  - duplicate outputs and unchanged re-reads become references
  - old observations are masked in batches (epochs)
- Background progressive compaction into a summary DAG whose leaves point at exact event ranges. Summaries are validated before use, with a pointer node as the fallback. Publication uses a generation compare-and-swap between requests.
- Standing user instructions are pinned verbatim. Provenance-aware task state is versioned with supersession.
- Cache-aware assembly: a stable prefix, a task-state snapshot plus a per-turn delta, and breakpoints placed for Anthropic prompt caching.
- Hybrid retrieval: FTS5 BM25 plus symbol, path and exact-id lookup, fused with reciprocal rank fusion. Retrieval outputs carry event ids, file paths and a data-not-instructions label.
- Deterministic emergency ladder that fits a request without any model call, or fails with `ErrProtectedOverflow` instead of dropping protected instructions.
- Multi-agent sessions: spawn, handoff, completion and grants, with per-agent isolation and a fair scheduler.

### Interfaces

- Go SDK (`engine`, `msg`, `summarize`), HTTP API v1 over TCP and Unix sockets (`docs/openapi.yaml`), MCP server over stdio (full and passive modes) and the `stratavell` CLI.
- Adapters for the Anthropic Messages API, OpenAI-compatible chat APIs and the Ollama native API.
- Examples in Go, Python, TypeScript and Rust.

### Safety and operations

- Without a bearer token the HTTP server answers only requests addressed to a loopback host, and refuses cross-origin browser requests. This protects against DNS rebinding and cross-site requests. A token is required to listen on a non-loopback address.
- The Unix socket is created with mode 0600. An existing path is replaced only if it is a stale socket: a regular file, or a socket another process is listening on, is never removed.
- Read-only CLI commands (`stats`, `inspect`, `search`, `show`, `verify`, `vacuum`, `delete-session`) refuse to create a missing database, session or agent.
- `stratavell verify` checks event hashes, summary lineage and SQLite and FTS5 integrity.
- The minimum Go version is 1.26.11. Earlier 1.26 releases have standard-library vulnerabilities (`net/http`, `crypto/x509`, `crypto/tls`) that the HTTP server can reach, according to `govulncheck`.

### Migrating from palimpsest

- The module path is now `github.com/TobiasLogic/stratavell`. Replace `github.com/TobiasLogic/palimpsest` in imports.
- The CLI is now `stratavell`, and the benchmark tool is `stratavell-bench` (formerly `palbench`).
- Environment variables are now `STRATAVELL_*`. The old `PALIMPSEST_*` names are still read, print a deprecation warning, and will be removed in a later release.
- The default database file is now `stratavell.db`. If only `palimpsest.db` exists in the working directory, it is used with a warning. `stratavell migrate` copies it to the new name, verifies the copy and leaves the original untouched. See `docs/operations.md`.
- MCP hosts should change the configured command from `palimpsest mcp` to `stratavell mcp`. The server now reports itself as `stratavell`.
- The database schema is unchanged. Databases created by palimpsest builds open without conversion.

### Benchmarks

- Results in `bench/results/2026-11-26/` were produced by build 314ff5d, before the rename, and are kept unchanged.
- A seed-1 rerun of the main, component-matched and ablation suites on the release build reproduced every deterministic field exactly. Only timing fields differ. See `docs/benchmarks.md`.
- `TestDeterministicSmoke` pins a short deterministic run in `bench/testdata/smoke-golden.json`, so behavior changes show up in CI.
- Known limits: the workloads are generated with fixed trajectories, the baselines are reconstructions, cache costs are simulated, and real autonomous coding-task success has not been evaluated. See `docs/limitations.md`.
