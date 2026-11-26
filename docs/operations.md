# Operations

## Running

```
stratavell serve --db /var/lib/stratavell/memory.db --socket $XDG_RUNTIME_DIR/stratavell.sock
stratavell serve --addr 0.0.0.0:7077 --token-file /etc/stratavell/token   # non-loopback needs a token
stratavell mcp --db ~/.local/share/stratavell/memory.db --session my-project
```

Summarizer choices: `--summarizer extractive` (default, no model), `ollama`, `openai` (any compatible endpoint, `--summarizer-url`), `anthropic`, `none`. API keys come from `STRATAVELL_SUMMARIZER_KEY`, `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`.

Without `--db`, commands use `stratavell.db` in the working directory. `serve` and `mcp` create the file if it is missing. The other commands refuse to create a missing database, session or agent.

## Environment variables

| variable | used by | meaning |
|---|---|---|
| `STRATAVELL_DB` | every command | database path when `--db` is not given |
| `STRATAVELL_TOKEN` | `serve` | bearer token required on every request except `/v1/health`; `--token-file` takes precedence |
| `STRATAVELL_SUMMARIZER` | `serve`, `mcp` | `extractive`, `ollama`, `openai`, `anthropic` or `none` |
| `STRATAVELL_SUMMARIZER_URL`, `STRATAVELL_SUMMARIZER_MODEL` | `serve`, `mcp` | endpoint and model of a model-backed summarizer |
| `STRATAVELL_SUMMARIZER_KEY` | `serve`, `mcp` | API key for the summarizer; falls back to `OPENAI_API_KEY` or `ANTHROPIC_API_KEY` |
| `STRATAVELL_SESSION`, `STRATAVELL_AGENT` | `mcp` | the session and agent the MCP server is bound to |
| `STRATAVELL_URL` | TypeScript and Rust examples | server address |
| `STRATAVELL_TOKEN` | Python and TypeScript example clients | bearer token sent with each request |
| `OPENROUTER_API_KEY` | `stratavell-bench` | only for the optional real-model runs; otherwise read from `stratavell/openrouter.env` under the user config directory |

Each `STRATAVELL_*` variable is also read under its pre-0.1.0 name, `PALIMPSEST_*`, with a deprecation warning. The new name wins when both are set.

## Inspecting

| command | shows |
|---|---|
| `stratavell stats --session S --agent A` | generation, boundary, frontier size and tokens, archived events and tokens, compression ratio, pending jobs, calibration ratio, growth per turn, summarizer latency |
| `stratavell stats --engine` | counters, gauges, latency histograms (p50, p95, p99, max), database and WAL size, blob raw and stored bytes, write transactions, heap and goroutines |
| `stratavell inspect --session S --agent A` | sessions, agents, frontier nodes and task state |
| `stratavell search --session S --agent A query...` | search hits as the agent would see them |
| `stratavell show --session S --agent A ev_...` | one event, exactly as stored |
| `stratavell verify [--session S]` | hash, lineage and SQLite integrity checks; exits non-zero on problems |

The same data is available over HTTP (`/v1/stats`, `.../stats`, `.../verify`) and through the `memory_stats` tool. Metric names include:
- `append_ms`, `assemble_ms`, `retrieval_ms`, `summarize_ms`, `emergency_ms` and `foreground_wait_ms` (histograms)
- `events`, `raw_tokens`, `generations`, `summaries_published`, `summaries_stale`, `pointer_nodes`, `mask_epochs`, `emergencies`, `fallbacks`, `job_failures`, `job_retries`, `queue_rejections`, `rate_deferrals`, `retrievals`, `retrieval_loops_blocked`, `retrieval_budget_blocks`, `exact_recoveries`, `protocol_rejections`, `cache_read_tokens`, `cache_write_tokens`, `summarizer_input_tokens` and `summarizer_output_tokens` (counters)
- `queue_depth`, `workers_busy`, `active_tokens` and `calibration_ratio` (gauges)

Payloads are never logged or exported as metrics.

## Retention and deletion

- `stratavell delete-session --session S` (or `DELETE /v1/sessions/S`) removes the session's events, summaries, task state, indexes, views, jobs, grants and metrics in one transaction. It then deletes blobs no other session references and records a content-free audit row in `deletions`. The command line version also checkpoints the WAL and vacuums the file so the bytes leave the disk; after an HTTP deletion run `stratavell vacuum` when the server is idle. With `Options.Store.SecureDelete` set (Go SDK), SQLite overwrites deleted content in place as well.
- `Agent.Purge(ctx, uptoSeq, RetentionPolicy{...})` removes payloads of events already covered by summaries (optionally only events older than a given age). Purged events keep their id, sequence and hash, and report `not found` on retrieval. `Verify` skips them.

## Memory use

The engine keeps each open agent's raw region, frontier and indexes of recent reads in memory. That is bounded by the context window, a few megabytes per agent. Programs that embed the engine and touch many sessions should call `Engine.ReleaseSession(id)` when a session goes idle. It is reloaded from the database on next use, and background results that finish in between are picked up then.

## Backup

The database is one SQLite file plus its WAL. Back up a live database from Go with `Engine.Backup(ctx, "copy.db")` (`VACUUM INTO`, refuses to overwrite), with `sqlite3 memory.db ".backup copy.db"`, or by copying the file after `stratavell vacuum` while the server is stopped. An external artifact directory (`--artifacts`) must be backed up with it.

## Migrating from palimpsest

Stratavell was called palimpsest before v0.1.0. The database schema did not change, so existing databases open as they are. Nothing is renamed, moved or deleted automatically.

1. **Binary.** Install `stratavell` and replace `palimpsest` in scripts and service files. Replace `palbench` with `stratavell-bench`.
2. **Go imports.** Replace `github.com/TobiasLogic/palimpsest` with `github.com/TobiasLogic/stratavell`.
3. **Environment.** Rename `PALIMPSEST_*` variables to `STRATAVELL_*`. The old names keep working for now and print a warning.
4. **Default database.** If you relied on the default `palimpsest.db` in the working directory, it is still used, with a warning, as long as no `stratavell.db` exists. To switch to the new name:

   ```
   stratavell migrate --dry-run
   stratavell migrate
   ```

   `migrate` copies the database with `VACUUM INTO` and opens the copy. It verifies every session's hashes and lineage, and compares the event count with the source. It refuses to run if the target or its `-wal` or `-shm` file exists. The source file is left untouched: delete it yourself once you are satisfied. Use `--from` and `--to` for other paths, for example `stratavell migrate --from ~/.local/share/palimpsest/memory.db --to ~/.local/share/stratavell/memory.db`. Stop any server using the source database first.
5. **MCP hosts.** Change the configured command from `palimpsest mcp ...` to `stratavell mcp ...`. The server now reports itself as `stratavell`, and the tool names are unchanged.
6. **Benchmark key file.** `stratavell-bench` reads `stratavell/openrouter.env` under the user config directory, and falls back to the old `palimpsest/openrouter.env`.

## Compatibility and rollback

- **Schema.** Migrations only add. A build refuses a database whose schema version is newer than it knows, so rolling back the binary after a schema upgrade fails loudly instead of corrupting data. Keep a copy of the database before upgrading across a schema version.
- **Configuration.** `config_version` 0 and 1 are migrated on read. Policies are stored per session, so an engine upgrade does not change running sessions unless a new policy is passed. Durations accept both strings and integer nanoseconds.
- **API.** The HTTP API is versioned under `/v1`. Additive fields can appear in responses; removals or changes will use `/v2`.
- **Turning the engine off for a host.** Hosts integrate through `Assemble`, so the rollback path is to stop calling it and send the host's own history again; the journal stays intact. Inside the engine, set `compaction.enabled=false`, `masking.mode=off` and `retrieval.enabled=false` to get a plain journal with no rewriting. Each component can be disabled on its own (see `docs/configuration.md`), and the ablations in `docs/benchmarks.md` show the effect of each.

## Profiling

```
go test ./engine -run '^$' -bench . -benchtime 300x -cpuprofile cpu.prof
go tool pprof -top -cum engine.test cpu.prof
go test ./engine -run TestSearchLatencyProfile -v
go test -race ./...
go test ./engine -run '^$' -fuzz FuzzAppendAssemble -fuzztime 60s
```
