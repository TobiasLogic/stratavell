# Storage schema

One SQLite database in WAL mode holds everything. Writes go through a single connection and `BEGIN IMMEDIATE` transactions. Reads use a separate pool. `synchronous=FULL` is the default; `normal` is available for throughput when losing the last few commits on power loss is acceptable (a process crash loses nothing in either mode). Paths are passed to SQLite as escaped `file:` URIs, so `#`, `?` and `%` in directory names are safe.

Migrations live in `internal/store/schema.go`. Each is applied once, in a transaction, and recorded in `schema_migrations`. A database whose schema is newer than the build is refused.

## Tables

| table | contents |
|---|---|
| `sessions` | id, project, creation time, stored policy JSON and its `config_version` |
| `agents` | per session: id, parent, role, head sequence, state (`active`, `done`, `failed`) |
| `events` | the journal: id, session, agent, `seq` (unique per agent), parent and cause ids, timestamp, role, content type, tool name, call id, group id, exit code, error flag, `body` (parts JSON with blob references), `content_hash`, estimated tokens, raw bytes, scope, paths, symbols, derived metadata, protected flag, indexed flag, purged flag |
| `blobs` | content-addressed payloads: SHA-256, raw size, codec (`raw`, `deflate`, or `external` when an `ArtifactStore` holds the bytes), stored size, data |
| `blob_refs` | which events reference which blobs; used for authorization and garbage collection |
| `summaries` | DAG nodes: id, session, agent, level, kind (`semantic`, `pointer`), covered sequence range, text, facts JSON, source and output tokens, content hash, sources hash, model, prompt version, validation state and note, publication state (`pending`, `published`, `superseded`, `stale`), job id |
| `summary_sources` | ordered edges from a node to its sources (`event` or `summary`) |
| `views` | generation records: frontier (ordered node ids), boundary sequence, mask cutoff, reason, policy version |
| `jobs` | compaction jobs: kind (`leaf`, `condense`), level, base generation, range, sources, state (`queued`, `running`, `done`, `published`, `failed`, `abandoned`, `rejected`), attempts, timestamps, error, summary id; a partial unique index prevents duplicate active jobs for one range |
| `task_items` | versioned task state: kind, key, text, status, provenance, scope, owner, source event, refs, version, supersedes and superseded_by, protected |
| `grants` | explicit read grants from one agent to another |
| `paths`, `symbols` | exact lookup indexes with the operation (`read`, `write`, `mention`) and file hash for reads |
| `docs`, `docs_fts` | retrieval chunks and their FTS5 index (columns `body`, `aux`, `sid`) |
| `vectors` | optional embedding vectors per chunk |
| `metrics` | counters and histogram summaries flushed on close |
| `deletions` | an audit record of deletions (hashed session id and counts, no content) |

## Integrity

`stratavell verify` (or `Engine.Verify`) checks:

- every retained event decodes, its blobs exist and match their hashes, and the canonical hash of the reconstructed message equals `content_hash`
- sequence numbers per agent are contiguous and match `head_seq`
- every view's frontier starts at sequence 1, has no gaps, ends at its boundary, and every node expands through the DAG to exactly the events of its range, with no cycles
- SQLite `integrity_check` on a fresh connection, and the FTS5 `integrity-check` command

The integrity check runs on a fresh connection because long-lived pooled connections reported false FTS5 corruption under `integrity_check` while queries on the same connections returned correct results. This was observed with modernc SQLite 3.54.1 and is noted in `docs/limitations.md`.
