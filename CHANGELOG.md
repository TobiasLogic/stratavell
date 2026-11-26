# Changelog

All notable changes are listed here. The project follows [Semantic Versioning](https://semver.org/). Until 1.0, minor releases may change the Go API, the HTTP API, the MCP tool set or the database schema; every such change is listed here with migration steps.

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
