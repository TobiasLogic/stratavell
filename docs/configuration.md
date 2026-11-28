# Configuration reference

A policy is JSON. `stratavell config` prints the defaults. Pass a file with `--config` on the command line, `Options.Policy` in Go, or the `policy` field when creating a session over HTTP. A session stores the policy it was created with; reopening the session uses the stored policy unless a new one is given.

Unknown fields are ignored. Missing fields keep their defaults, so a policy file only needs the values you change. Durations are strings such as `"90s"` (integers are read as nanoseconds).

## Versioning and migration

`config_version` is currently `2`. Files without a version, or with version `1`, are read as the legacy flat format (`soft_threshold`, `aggressive_threshold`, `emergency_threshold`, `context_window`, `max_output_tokens`, `workers`, `keep_recent_fraction`) and converted. A version newer than the build is rejected. The database schema has its own version in `schema_migrations`; see `docs/operations.md`.

## thresholds

Fractions of the effective input capacity. They must satisfy `0 < target < soft <= aggressive <= emergency < 1`.

| key | default | meaning |
|---|---|---|
| `soft` | 0.55 | start background summarization |
| `aggressive` | 0.70 | masking epoch (no model call) |
| `emergency` | 0.85 | deterministic emergency ladder (no model call) |
| `target` | 0.40 | level background compaction aims for |

APEC used 0.50 and 0.90 on one model; these defaults are hypotheses that the benchmarks exercise, not tuned values.

## budget

| key | default | meaning |
|---|---|---|
| `context_window` | 200000 | provider context window in tokens |
| `max_output_tokens` | 16000 | reserved for the response |
| `reasoning_tokens` | 0 | extra reservation for thinking, if the provider counts it against the window |
| `safety_tokens` | 2000 | fixed margin |
| `safety_fraction` | 0.02 | proportional margin; the larger of the two is used |
| `provider_overhead` | 300 | per-request framing not visible to the estimator |
| `recent_fraction` | 0.20 | raw history kept unmasked and unsummarized, at least `min_recent_turns` |
| `retrieval_fraction` | 0.08 | cap for automatically injected evidence |
| `task_state_fraction` | 0.04 | cap for the task state block |
| `protected_fraction` | 0.15 | cap for pinned instructions before auto-detected directives are shortened (explicit constraints are never shortened) |
| `summary_fraction` | 0.25 | summary region size that triggers condensation |
| `min_recent_turns` | 2 | assistant turns always kept raw |
| `max_observation_tokens` | 6000 | outputs above this are externalized to a preview at ingestion |

Effective capacity = `context_window - max_output_tokens - reasoning_tokens - max(safety_tokens, safety_fraction * context_window) - provider_overhead`.

## masking

| key | default | meaning |
|---|---|---|
| `mode` | `epoch` | `epoch` moves the mask cutoff only at publishes and thresholds; `rolling` keeps the last `rolling_keep` observations (Complexity Trap style, cache-hostile); `off` |
| `rolling_keep` | 10 | observations kept in rolling mode |
| `dedupe` | true | replace exact duplicate observations with a reference |
| `unchanged_reads` | true | replace re-reads of unchanged files with a reference |
| `externalize` | true | preview outputs above `max_observation_tokens`; results of memory tools are never externalized |
| `fresh_full_fraction` | 0.20 | an oversized output the model has not answered yet is shown in full once, if the new outputs together fit in this fraction of the capacity and the context stays under the aggressive threshold; 0 previews them at once, as before 0.2 |
| `mask_tool_inputs` | true | shrink large tool-call inputs of masked assistant turns |
| `input_mask_min_tokens` | 400 | input size worth masking |
| `preview_head_lines`, `preview_tail_lines` | 40, 30 | preview shape |
| `preview_line_max_bytes` | 240 | per-line clip in previews |
| `digest_max_errors` | 12 | error lines kept in a log digest |

## compaction

| key | default | meaning |
|---|---|---|
| `enabled` | true | semantic compaction on or off |
| `async` | true | background jobs; `false` summarizes in the foreground at the soft threshold |
| `emergency` | true | allow the deterministic emergency ladder; with `false` an oversized context returns `ErrContextOverflow` |
| `workers` | 2 | background workers per engine |
| `max_queued` | 64 | queue bound; beyond it scheduling is refused and masking or the emergency ladder take over |
| `max_inflight_per_agent` | 2 | queued plus running leaf jobs per agent |
| `leaf_chunk_tokens` | 16000 | target size of a leaf job's input |
| `min_chunk_tokens` | 1500 | smallest leaf worth summarizing |
| `summarizer_observation_max_tokens` | 1500 | observations above this enter the summarizer as header, digest and short preview |
| `job_timeout` | `"1m30s"` | per summarizer call |
| `max_attempts` | 3 | retries, with exponential backoff and jitter |
| `retry_backoff` | `"2s"` | first backoff |
| `min_reduction` | 0.5 | a summary must be at most this fraction of its source, else it is retried once with a stricter prompt and then replaced by a pointer node |
| `condense_fanin` | 6 | nodes merged per condensation |
| `condense_max_nodes` | 12 | frontier size that triggers condensation |
| `predict_safety` | 1.5 | start compaction early when turns to emergency fall below this times summarizer latency divided by turn interval |
| `emergency_wait` | `"0s"` | how long an emergency waits for in-flight jobs before using pointers |
| `upgrade_pointers` | true | replace an emergency pointer with the semantic summary of the same range when it arrives |
| `prompt_version` | `leaf-v1` | recorded on every summary |
| `summary_max_tokens` | 1200 | cap on one summary |
| `summary_ratio` | 0.15 | requested summary size relative to the source |
| `rate_per_minute` | 30 | summarizer requests per minute |
| `tokens_per_minute` | 400000 | summarizer plus foreground tokens per minute when they share a provider budget |
| `foreground_reserve` | 0 | share of `tokens_per_minute` kept for the foreground model |
| `passive_summaries` | false | summarize agents that never call `assemble` (passive hosts that keep their own context); off by default, since nothing shows those summaries to the model |

## retrieval

| key | default | meaning |
|---|---|---|
| `enabled` | true | memory tools and search |
| `auto_inject` | true | on a new user message, add up to `auto_inject_hits` archived hits that are not already in context |
| `auto_inject_hits` | 3 | |
| `max_calls_per_turn` | 8 | memory tool calls per turn before refusal |
| `dedup_window_turns` | 6 | identical memory calls within this window are answered with a pointer to the earlier result |
| `chunk_bytes` | 1600 | retrieval chunk size |
| `index_max_bytes` | 2097152 | bytes of one event that are indexed; the digest of the rest is indexed |
| `max_result_tokens` | 6000 | bound on one tool result, and the default `memory_get_event` page size in tokens; `max_tokens` asks for up to 4 times this, limited by the room left under the aggressive threshold |
| `default_limit`, `max_limit` | 8, 25 | search result counts |
| `artifact_max_length` | 32768 | bytes per `memory_get_artifact` call, also bounded by `max_result_tokens` |

## cache

| key | default | meaning |
|---|---|---|
| `layout` | `stable-tail` | dynamic blocks after the last breakpoint; `spec` places task state and evidence before raw history |
| `breakpoints` | 4 | maximum cache breakpoints emitted |

## top level

| key | default | meaning |
|---|---|---|
| `task_state` | true | render the task state block |
| `pin_user_directives` | true | pin user messages verbatim once they leave the raw region |
| `pin_max_tokens` | 800 | user messages longer than this are pinned as extracted directive sentences plus a reference |

## Engine options (Go only)

`engine.Options` also takes: `Store` (durability `full` or `normal`, read connections, `secure_delete`), `Blob` (compression threshold and ratio), `Summarizer`, `Embedder`, `Tokenizer`, `Artifacts`, `Ranker`, `Trigger`, `Clock`, `IDs`, `Manual` (no background workers; call `RunJobs`) and `SyncIndex`.
