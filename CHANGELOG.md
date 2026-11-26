# Changelog

All notable changes are listed here. The project follows [Semantic Versioning](https://semver.org/). Until 1.0, minor releases may change the Go API, the HTTP API, the MCP tool set or the database schema; every such change is listed here with migration steps.

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
