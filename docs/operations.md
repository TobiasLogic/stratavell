# Operations

## Running

```
palimpsest serve --db /var/lib/palimpsest/memory.db --socket $XDG_RUNTIME_DIR/palimpsest.sock
palimpsest serve --addr 0.0.0.0:7077 --token-file /etc/palimpsest/token   # non-loopback needs a token
palimpsest mcp --db ~/.local/share/palimpsest/memory.db --session my-project
```

Summarizer choices: `--summarizer extractive` (default, no model), `ollama`, `openai` (any compatible endpoint, `--summarizer-url`), `anthropic`, `none`. API keys come from `PALIMPSEST_SUMMARIZER_KEY`, `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`.

## Inspecting

| command | shows |
|---|---|
| `palimpsest stats --session S --agent A` | generation, boundary, frontier size and tokens, archived events and tokens, compression ratio, pending jobs, calibration ratio, growth per turn, summarizer latency |
| `palimpsest stats --engine` | counters, gauges, latency histograms (p50, p95, p99, max), database and WAL size, blob raw and stored bytes, write transactions, heap and goroutines |
| `palimpsest inspect --session S --agent A` | sessions, agents, frontier nodes and task state |
| `palimpsest search --session S --agent A query...` | search hits as the agent would see them |
| `palimpsest show --session S --agent A ev_...` | one event, exactly as stored |
| `palimpsest verify [--session S]` | hash, lineage and SQLite integrity checks; exits non-zero on problems |

The same data is available over HTTP (`/v1/stats`, `.../stats`, `.../verify`) and through the `memory_stats` tool. Metric names include:
- `append_ms`, `assemble_ms`, `retrieval_ms`, `summarize_ms`, `emergency_ms` and `foreground_wait_ms` (histograms)
- `events`, `raw_tokens`, `generations`, `summaries_published`, `summaries_stale`, `pointer_nodes`, `mask_epochs`, `emergencies`, `fallbacks`, `job_failures`, `job_retries`, `queue_rejections`, `rate_deferrals`, `retrievals`, `retrieval_loops_blocked`, `retrieval_budget_blocks`, `exact_recoveries`, `protocol_rejections`, `cache_read_tokens`, `cache_write_tokens`, `summarizer_input_tokens` and `summarizer_output_tokens` (counters)
- `queue_depth`, `workers_busy`, `active_tokens` and `calibration_ratio` (gauges)

Payloads are never logged or exported as metrics.

## Retention and deletion

- `palimpsest delete-session --session S` (or `DELETE /v1/sessions/S`) removes the session's events, summaries, task state, indexes, views, jobs, grants and metrics in one transaction. It then deletes blobs no other session references and records a content-free audit row in `deletions`. The command line version also checkpoints the WAL and vacuums the file so the bytes leave the disk; after an HTTP deletion run `palimpsest vacuum` when the server is idle. With `Store.SecureDelete` set, SQLite overwrites deleted content in place as well.
- `Agent.Purge(ctx, uptoSeq, RetentionPolicy{...})` removes payloads of events already covered by summaries (optionally only events older than a given age). Purged events keep their id, sequence and hash, and report `not found` on retrieval. `Verify` skips them.

## Memory use

The engine keeps each open agent's raw region, frontier and indexes of recent reads in memory. That is bounded by the context window, a few megabytes per agent. Programs that embed the engine and touch many sessions should call `Engine.ReleaseSession(id)` when a session goes idle. It is reloaded from the database on next use, and background results that finish in between are picked up then.

## Backup

The database is one SQLite file plus its WAL. Back up with `sqlite3 memory.db ".backup copy.db"` or by copying after `palimpsest vacuum` while the server is stopped. An external artifact directory (`--artifacts`) must be backed up with it.

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
