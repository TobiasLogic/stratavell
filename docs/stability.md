# Interface stability

Stratavell follows [Semantic Versioning](https://semver.org/). Before 1.0, any minor release may change the interfaces below. Every change to a stable interface is listed in `CHANGELOG.md` with migration steps, and the intent is to change them rarely.

## Intended to stay stable

These are the interfaces that integrations should depend on.

| interface | scope |
|---|---|
| HTTP API `/v1` | routes, request and response fields in `docs/openapi.yaml`. New fields may appear in responses; removing or changing a field means `/v2`. |
| MCP tool names and arguments | `memory_search`, `memory_query`, `memory_expand`, `memory_get_event`, `memory_get_artifact`, `memory_get_task_state`, `memory_get_summary_lineage`, `memory_stats`, `memory_update_task`, and `memory_archive` in passive mode |
| `msg` package | `Message`, `Part`, `Meta` and their JSON form, which is also the HTTP wire format |
| `engine` core | `Open`, `Options`, `Engine.OpenSession`, `LookupSession`, `Session.Agent`, `LookupAgent`, `Agent.Append`, `Assemble`, `ObserveUsage`, `HandleTool`, `Search`, `GetEvent`, `TaskState`, `Engine.Verify`, `CloseSession`, `DeleteSession`, `Close` |
| CLI commands | `serve`, `mcp`, `stats`, `inspect`, `search`, `show`, `verify`, `delete-session`, `vacuum`, `migrate`, `config`, `version` and their documented flags |
| policy JSON | fields in `docs/configuration.md`; older `config_version` values are migrated on read |
| database files | migrations only add; a database is never rewritten in a way an older 0.1.x build cannot detect, and a build refuses a schema newer than it knows |

## Experimental

These may change in any release without a deprecation period.

| interface | why |
|---|---|
| `engine` extension points: `Summarizer`, `Embedder`, `Ranker`, `Trigger`, `Tokenizer`, `ArtifactStore`, `Clock`, `IDSource` | shapes may change as more implementations are written |
| multi-agent calls: `Session.Spawn` with a `Handoff`, `Agent.Complete`, `Agent.Grant` | covered by tests but not by an external user yet |
| `engine.Engine.DB()` | exposes the internal store for tests and maintenance; use `Checkpoint`, `Vacuum` and `Backup` instead |
| `adapters/*` | provider APIs change, and only the OpenAI-compatible adapter has been used live |
| `agentloop` | a reference loop, meant to be copied and adapted |
| `clients/python`, `clients/typescript` | thin wrappers over HTTP API v1; their method names may change, the wire format is the stable part |
| `summarize` | prompts and output format may change, which changes summary text |
| metric names in `stats` output | may be renamed as observability is refined |
| `bench`, `cmd/stratavell-bench` | a research harness, not a library; its output format follows the experiments |

Packages under `internal/` are not importable and carry no compatibility promise.

## Behavior

Assembly output is deterministic for a given database, policy, clock and summarizer. The exact text and token layout of assembled requests is not a stable interface: it can change in any release to improve compaction. Behavior changes that show up in `TestDeterministicSmoke` are listed in `CHANGELOG.md`.
