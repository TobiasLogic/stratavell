# 02. Lossless Context Management (LCM) paper and the Volt implementation

Research note for the compaction engine project. Scope: what the LCM paper claims, what the Volt source code actually does, where the two disagree, and which ideas are worth reimplementing in our Go engine.

Accessed 2026-11-26. Nothing in the Volt repository was built or executed; every finding below comes from reading source text, the paper, and the paper's figures.

## 0. Sources and provenance

| Item | Value |
|---|---|
| Paper (abstract page) | https://arxiv.org/abs/2605.04050 |
| Paper (full text read) | https://arxiv.org/html/2605.04050 (HTML v1, plus the three figure PNGs under https://arxiv.org/html/2605.04050v1/) |
| Code | https://github.com/Martian-Engineering/volt |
| Code revision read (HEAD) | `4f227134c4671fd2d7b0f3b57b1b3ccabcfd3a8e`, 2026-02-27, "chore: generate" |
| Paper-era revision also read | `944c7943c978f9fa0befd631849b8907a9699ee3`, 2026-02-14, "feat: fork from OpenCode with Lossless Context Management (LCM)". This is the first public commit and has the same date as the paper. |
| File history | Queried with the GitHub commits API per path (see section 6). |

All repository paths below are relative to the repo root. Most live under `packages/voltcode/src/`, abbreviated `src/` from here on. Line numbers refer to HEAD unless marked "paper-era".

Number labels used in this note:
- **claimed**: a number stated by the paper or the README and not checked by us.
- **measured (by authors)**: a number the authors report as coming from an experiment. We could not reproduce it.
- **from code**: a default constant read from the source.
- **estimate**: our own arithmetic, derived from code defaults.

## 1. Paper facts

- **Title:** "LCM: Lossless Context Management"
- **Authors:** Clint Ehrlich and Theodore Blackman, both at Voltropy PBC.
- **Date:** v1 submitted 14 Feb 2026 (arXiv submission history: "Sat, 14 Feb 2026 20:46:47 UTC"). The `2605` identifier means the paper was announced in May 2026, about three months after submission.
- **Status:** arXiv preprint, cs.AI, v1 only. There is no journal reference, so as far as the record shows it has not been peer reviewed. The README also links a copy at https://papers.voltropy.com/LCM.
- **Paper license:** CC BY 4.0, as shown on the arXiv HTML page.
- **Size:** short. Six sections plus appendices A (raw scores), B (data model) and C (tool interfaces). There are no tables. All per-length results exist only as images in Figures 1, 6 and 7.

## 2. Architecture as described in the paper

### 2.1 Dual state: immutable store and active context

- **Immutable Store.** Every user message, assistant response and tool result is persisted verbatim and never modified.
- **Active Context.** The window that is actually sent to the LLM each turn. It mixes recent raw messages with "summary nodes".
- **Summaries are a derived cache.** They are described as "materialized views over the immutable history".
- **What "lossless" means.** The paper's definition is that every original message stays retained and reachable through `lcm_grep` or `lcm_expand`. The paper explicitly does not promise that the agent will actually retrieve what it needs.

### 2.2 Hierarchical summary DAG

- **Storage requirements.** A store with transactional writes, foreign-key integrity and indexed search. The reference implementation uses embedded PostgreSQL.
- **Two node kinds:**
  - leaf summaries, which summarize a span of messages;
  - condensed summaries, which summarize other summaries.
- **Provenance.** The paper says provenance edges prevent orphaned summaries.
- **Retrieval method.** The paper argues for regex search over ground truth plus hierarchical drill-down, rather than embeddings. It says no embedding index was implemented (section 2.1).

### 2.3 Control loop (paper Figure 2)

The loop runs once for each new item `h`:

1. Persist `h` to the store and append a pointer to it in the active context `C`.
2. If `Tok(C) > tau_soft`, trigger asynchronous compaction. This does not block the user.
3. While `Tok(C) > tau_hard`, block. Take the oldest block in `C`, run `EscalatedSummary(block)` on it, and replace the block with a pointer to the result.

The paper describes three overhead regimes:
- below `tau_soft`: none;
- between `tau_soft` and `tau_hard`: asynchronous;
- above `tau_hard`: blocking.

The asynchronous result is "atomically swapped" into the context between turns. The paper says this happens within a "~25-second compaction window" (claimed, no distribution reported).

### 2.4 Three-level escalation (paper Figure 3, section 2.3)

1. `LLM-Summarize(X, mode="preserve_details", T)`
2. `LLM-Summarize(X, mode="bullet_points", T/2)`
3. `DeterministicTruncate(X, 512)`, with no LLM involved.

Each level's output is accepted if `Tokens(S) < Tokens(X)`. The paper calls this "guaranteed convergence via Level 3".

### 2.5 Retrievability invariants (section 2.4)

- **Deterministic Retrievability.** The engine itself injects the IDs of summarized content next to each summary, as post-processing that does not depend on model output.
- **Expansion is sub-agent only.** `lcm_expand` can only be called by sub-agents, so the main loop never expands summaries inline.

### 2.6 Large files (section 2.2, Appendix B)

- **Threshold.** Above a token threshold (Appendix B: "default >25k tokens"), the file is stored by path and never loaded into context.
- **What the context gets instead.** An opaque ID, the path, and an "Exploration Summary". A type-aware dispatcher builds the summary:
  - JSON, CSV and SQL get schema and shape extraction;
  - code gets structural analysis;
  - other text gets an LLM summary.
- **Content is not copied.** The file content is deliberately kept out of the store. The paper says file IDs propagate through the DAG so the model "retains awareness" of files.

### 2.7 Operator-level recursion (section 3) and delegation guard (section 3.2)

- **`llm_map`.** One stateless LLM call per JSONL item, a worker pool (default 16), JSON Schema validation, and retry with the validation error fed back. Items move through pending, running, completed and failed states in the DB, and workers claim items with pessimistic locking. The paper calls this "exactly-once execution semantics (modulo retries on error)".
- **`agentic_map`.** The same, but each item gets a full sub-agent session, with a `read_only` flag. Nesting is allowed.
- **Task delegation guard.** A non-root agent that spawns a sub-agent must give `delegated_scope` and `kept_work`. The paper claims this "structural guarantee of termination" makes a depth limit unnecessary.

### 2.8 Tool interfaces (Appendix C, as the paper states them)

- `lcm_grep(pattern, summary_id?)`
- `lcm_describe(id)`
- `lcm_expand(summary_id)` (sub-agents only)
- `llm_map(input_path, prompt, output_schema, ...)`
- `agentic_map(input_path, prompt, output_schema, read_only, ...)`
- `Task(prompt, subagent_type, delegated_scope, kept_work, ...)`
- `Tasks(tasks[])`

## 3. Evaluation as reported in the paper

### 3.1 Setup

- **Benchmark:** OOLONG, `trec_coarse` split. Figure 1's caption calls it "OOLONG-synth".
- **Context lengths:** 8K, 16K, 32K, 65K, 131K, 256K, 512K and 1M.
- **Models:** Opus 4.6 as the primary model for both systems. Both also had "access to" Claude Haiku 4.5 as an auxiliary model.
- **Baselines:**
  - Claude Code v2.1.4;
  - "raw Opus 4.6" with no harness.
- **Decontamination.** The authors dropped tasks where a reasoning trace showed the model recognised the TREC data. The raw-model baseline could not be decontaminated because it has no traces.

### 3.2 Results (measured by authors)

The figure labels give exact per-length gains over raw Opus. The raw Opus scores are read off the plot, so they are approximate.

| Context | Raw Opus 4.6 (approx, from plot) | Claude Code gain | Volt gain | Volt minus CC |
|---|---|---|---|---|
| 8K | ~83 | +11.1 | +11.2 | +0.1 |
| 16K | ~59 | +26.3 | +25.0 | -1.3 |
| 32K | ~39 | +25.8 | +29.4 | +3.6 |
| 65K | ~48 | +26.8 | +27.6 | +0.8 |
| 131K | ~42 | +22.0 | +28.3 | +6.3 |
| 256K | ~48 | +8.5 | +18.5 | +10.0 |
| 512K | ~28 | +29.8 | +42.4 | +12.6 |
| 1M | ~18 | +47.0 | +51.3 | +4.3 |
| Mean | ~45.6 | +24.7 (abs 70.3) | +29.2 (abs 74.8) | +4.5 |

The figure labels are internally consistent: the per-length gains average to 24.7 and 29.2. Before decontamination (Appendix A, Figure 7), the mean absolute scores were Claude Code 72.0 and Volt 74.3.

### 3.3 Problems with the reported results

1. **No sample sizes, variance, run counts or confidence intervals.** The paper reports none of these, and never says how many tasks were dropped by decontamination. The released harness (section 5.10) suggests 50 tasks per length, built on only two distinct context windows.
2. **The text contradicts its own figure at 8K.** Section 4.2 says "Claude Code held a slight edge at 8K (+13.1 vs. +11.2)". Figure 1 shows +11.1 for Claude Code against +11.2 for Volt.
3. **The raw scores contradict "unchanged".** Appendix A says the overall finding is unchanged before decontamination ("Volt outperforms Claude Code across context lengths"). Figure 7 appears to show Claude Code at or slightly above Volt at 16K and at 1M.
4. **Decontamination can be asymmetric.** Memorization was detected from Opus reasoning traces. Volt's classification ran in `llm_map` calls that return JSON from a smaller model, so its traces had fewer chances to reveal memorization. Claude Code read the items in its own context. Removing contaminated tasks moved the gap from +2.3 to +4.5. The paper does not say whether exclusions were applied per task across both systems or per system.
5. **One benchmark family, one split, one primary model, one baseline version.**

## 4. Paper claims that are not backed by measurement

| Claim | Where | Status |
|---|---|---|
| "Zero-cost continuity": no overhead below `tau_soft` | 2.4, conclusion | No latency or cost data. Also not true of the default HEAD mode (section 6). |
| Compaction window is "~25 seconds"; added latency is "generally imperceptible" | 2.4 and footnote 1 | No distribution reported. The footnote's KV-cache argument is technically wrong (section 7.3). |
| "Guaranteed convergence" | 2.3 | Only means each summary is smaller than its input. It does not mean the context ends up under the hard limit, and it does not cover summarizer errors (section 7.1). |
| Lossless retrievability of "any message" | 2.4, abstract | A design claim with no tests. The code has exceptions (section 7.2). |
| Scope-reduction invariant gives "structural termination" | 3.2 | Only checks that two strings are non-empty (section 5.8). "Observed no instances of runaway delegation" is anecdotal, with no counts. |
| Recursive `agentic_map` "works seamlessly" | 3.1 | Anecdotal. |
| Regex plus DAG traversal "has proven sufficient for our evaluation workloads" | 2.1 | OOLONG as run never exercises history retrieval (section 5.10). |
| LCM "partially insulates against contamination" | 5 | Speculative. |
| "Reliability and cost advantages" | conclusion | No cost numbers are reported, even though the harness records `total_cost_usd` for Claude Code. |
| LCM outperforms Claude Code because "LCM's recursive architecture is fundamentally more powerful" | 1 | The measured gain is attributed to `llm_map` plus a prescribed workflow. The memory DAG is not what was measured (section 5.10). |

## 5. Volt implementation

### 5.1 Repository basics

- **Origin.** A fork of OpenCode (TypeScript, Bun). LCM replaces OpenCode's session context assembly.
- **License.** MIT, `LICENSE`, "Copyright (c) 2025 voltcode". `packages/docs/LICENSE` is MIT (Mintlify). `packages/voltcode/package.json` declares MIT.
- **Store.** Embedded PostgreSQL 17.7 on 127.0.0.1:54329, database `voltcode_lcm` (`src/session/lcm/config.ts:4-9`). An external Postgres or RDS can be used instead (`config.ts:21-48`).
- **Two runtime modes appeared after the paper:** `dolt` and `upward`. `upward` became the default on 2026-02-27 (`config.ts:121,171`). Code comments repeatedly say "lossless-claw parity" (for example `config.ts:352`, `strategy.ts:186`, `prompt.ts:1012`), which suggests a sibling implementation called lossless-claw. We did not inspect it.

### 5.2 Storage schema

The schema is created inline by `LcmDb.migrate()` in `src/session/lcm/db.ts:569-1089`. Idempotent `DO $$` blocks perform the migrations. The SQL files under `src/worker/migrations/` belong to an unrelated worker.

| Table | Key columns and constraints | Lines |
|---|---|---|
| `conversations` | identity PK; `model_name`; `model_ctx_max_tokens`; `ctx_cutoff_threshold numeric(5,4) default 0.6`; `parent_conversation_id` FK (links a sub-agent conversation to its parent) | db.ts:603-617 |
| `messages` | identity PK; `conversation_id` FK; `seq` with `UNIQUE(conversation_id, seq)`; `role` enum (system, user, assistant, tool); `content text` (the flattened message); `token_count int`; generated `content_tsv` with a GIN index | db.ts:620-640 |
| `message_parts` | `part_id` PK; `message_id` FK; `part_type` enum (text, reasoning, tool, patch, file, subtask, compaction, step_start, step_finish, snapshot, agent, retry); `ordinal` with `UNIQUE(message_id, ordinal)`; typed columns per part kind (`tool_call_id`, `tool_name`, `tool_input jsonb`, `tool_output`, `tool_error`, `patch_hash`, `file_url`, `step_tokens_in`, ...); `metadata jsonb` | db.ts:942-996 |
| `summaries` | `summary_id text` PK (global, not scoped to a conversation); `kind` enum (sprig, bindle); `summary_level` CHECK `^d[1-9][0-9]*$`; `condensation_order int >= 1`; `summary_type` CHECK in (sprig, bindle, archive_stub); `content`; `token_count`; `file_ids jsonb`; `is_off_context bool`; `qmd_doc_id` (unique when set) and `qmd_doc_version`; generated `content_tsv` with a GIN index | db.ts:643-760 |
| `summary_messages` | leaf edges: `(summary_id, message_id, ord)`; PK `(summary_id, ord)`; `UNIQUE(summary_id, message_id)`; `message_id` FK is `ON DELETE RESTRICT`; index on `message_id` | db.ts:780-788 |
| `summary_parents` | condensed edges: `(summary_id, parent_summary_id, ord)`. The naming is inverted: `parent_summary_id` is the child, lower-order input. `RESTRICT` on the input | db.ts:791-799 |
| `summary_lineage_pointers` | `(summary_id, points_to_summary_id, pointer_kind, ord)`; kind in (archive_stub, archive_full, lineage_parent); no self-pointers | db.ts:763-777 |
| `context_items` | the active-context projection: PK `(conversation_id, position)`; `item_type` (message or summary); CHECK that exactly one of `message_id` / `summary_id` is set; both FKs `RESTRICT` | db.ts:802-822 |
| `large_files` | `file_id` PK; `storage_kind` in (path, inline_text, inline_binary) with a shape CHECK; `original_path`; `content`; `binary_content bytea`; `token_count bigint`; `exploration_summary`; `explorer_used` | db.ts:826-1006 |
| `llm_map_runs` / `llm_map_items`, `agentic_map_runs` / `agentic_map_items` | run row (prompt, schema, concurrency, timeout, max_attempts, input/output path and LCM id); item rows `(map_id, item_index)` with status, attempts, result jsonb, error | db.ts:1009-1085 |

What these tables actually enforce:

- **Messages are append-only by convention.** We found no `UPDATE messages` or `DELETE FROM messages` anywhere under `src/session/lcm`. `appendMessage` locks the conversation row (`FOR UPDATE`, db.ts:1151-1155) and assigns `seq` and `position` as max+1 inside the transaction.
- **Summaries are mutable.** `markSummaryAsArchiveStub` and `setSummariesOffContext` update rows in place (db.ts:2774-2822). Only the original messages behave as immutable, not the whole store.
- **The level invariant is enforced on insert.** A d2 node may only have d1 sprigs as inputs, and a dN node (N > 2) only d(N-1) bindles (db.ts:1758-1783).
- **IDs:**
  - summary ID = `"sum_" + sha256(content + Date.now())[0:16]` (`src/session/lcm/summary.ts:222-229`). Because of the timestamp, this is not content-addressed, even though the model-facing prompt says "the same content always produces the same ID" (`src/session/prompt/lcm.txt:120`).
  - file ID = `"file_" + sha256(conversationId:path:size:mtime)[0:16]` (db.ts:3007-3016). The README calls this "content-addressed"; it is not.

### 5.3 Summary DAG representation and expansion

- **Node levels.** d1 ("sprig") summarizes messages. d2 ("bindle") summarizes sprigs. d3 and above are bindles over bindles of exactly one level lower. `archive_stub` nodes exist only in Dolt mode.
- **Expansion walk.** `expandSummaryToMessages` is a recursive CTE that walks `summary_parents` and `summary_lineage_pointers` down to `summary_messages` and returns all reachable messages ordered by `seq` (db.ts:2222-2261). Expansion always goes all the way to raw messages. There is no "one level down" option.
- **ID injection.** `getCurrentContext` prefixes every summary with `[Summary ID: sum_x]`, and bindles also get `[Parent Summaries: ...]` listing their immediate inputs (db.ts:1307-1371, 1465-1474). Token accounting includes this header overhead (db.ts:1486-1540). This is the engine-side "deterministic retrievability" the paper describes. Note that it only names the summary and its immediate inputs, not the covered message IDs.
- **Lane reordering.** After every replacement, context order is normalized to bindles, then other summaries, then sprigs, then raw messages (`normalizeContextLaneOrder`, db.ts:1836-1917). Summaries are therefore always grouped at the front.

### 5.4 Per-turn context assembly

`buildLcmModelMessages` (`src/session/prompt.ts:876-1493`) runs on every agent step:

1. **Sync.** Sync new OpenCode session messages into LCM (`syncSessionMessagesToLcm`, prompt.ts:483-591). Each message is flattened by `formatMessageForLcm` (prompt.ts:291-461) into a `content` string, with tool calls serialized as `<tool name="x">\nInput: {json}\nOutput: ...\n</tool>`, and is also stored as structured `message_parts`.
2. **Budget.** Compute the budget, then `isOverThreshold` (section 5.5).
3. **Compaction.**
   - Over the hard limit: block in `compactUntilUnderHardLimit`.
   - Otherwise, if over soft, or always in `upward` mode: schedule async compaction (prompt.ts:954-1133).
4. **Assemble.** `strategy.assembleContext(conversationId)` reads `context_items` with joins (prompt.ts:1136).
5. **Retrieval cues.** Optionally run pre-response retrieval and inject a `<memory-cues>` user message before the last user message (prompt.ts:1160-1195, 840-874). This only does anything in Dolt mode.
6. **Rebuild provider messages.**
   - Structured path (interleaved-reasoning models): rebuilt from `message_parts`.
   - Default path: rebuilt by regex-parsing the `<tool>` XML back out of `content` (`parseToolTagsFromLcm`, prompt.ts:609-719). Tool-call IDs are synthesized as `lcm_${contextIndex}_${j}` (prompt.ts:1450-1466).

Two details matter for design:
- **Summaries are sent as assistant messages.** Summary rows have role `summary`, and `mapLcmRoleToModel` turns everything that is not user or system into `assistant` (db.ts:1327; prompt.ts:593-596). The model therefore sees summaries as its own prior turns.
- **Reasoning attachment bug.** In the interleaved path, every assistant message gets the reasoning of the first assistant message, because `uiMessages.find(... indexOf(u) <= idx)` always returns the first match (prompt.ts:1388-1392). This shows how fragile the text round trip is.

### 5.5 Compaction triggers and thresholds

**Budget** (`src/session/token-budget.ts:179-243`):
- `reserve = min(output_reserve or 20000, model.limit.output, 0.25 * window)`
- `hardLimit = window - (systemPromptTokens + toolTokens) - reserve`
- `softThreshold = 0.6 * window - overhead` (from code)

All token counts use `ceil` or `round` of `chars / 4` (`src/util/token.ts:1-7`; `large-file-threshold.ts:53-55`). No provider tokenizer is used.

**Paper-era (944c7943), from code:**
- soft cutoff 0.6 of the window;
- `TARGET_FREE_PERCENTAGE` 0.25;
- `MIN_MESSAGES_TO_SUMMARIZE` 3;
- `CRITICAL_THRESHOLD_MULTIPLIER` 1.2;
- `MAX_COMPACTION_ROUNDS` 10 (paper-era `context.ts:117-142`).

This version matches the paper's two-threshold loop.

**HEAD `upward` mode, the default** (`config.ts:171-203`, `strategy.ts:68-104`):
- **Global trigger:** `currentTokens > 0.75 * (window - overhead - reserve)`.
- **Leaf trigger:** raw tokens outside the fresh tail >= `leafChunkTokens` = 20,000. The fresh tail is the last `freshTailCount` = 32 raw messages.
- **Either trigger starts the async sweep `compactForcedRecursive`** (`context.ts:1308-1484`):
  1. **Leaf pass.** Repeatedly take the oldest contiguous run of raw messages outside the tail, capped at 20k tokens (the first message is always taken even if larger). Summarize it to a d1 node, together with the two preceding summaries as "continuity context" (context.ts:1344-1393, 544-574).
  2. **Condensation pass.** Repeatedly pick the shallowest order that has a contiguous run of at least the fanout:
     - 8 when condensing d1 into d2;
     - 4 deeper;
     - 2 in hard-trigger mode.

     The run must total at least `max(2000, 0.1 * 20000)` tokens and is capped at 20k input tokens. Condense it to order+1 (context.ts:1413-1469, 1566-1639; config.ts:355-358).
  3. **Stop condition.** Stop when a pass no longer reduces the token count.
  4. **Bindles are never evicted** in this mode.
- **Blocking path** (`compactUntilUnderHardLimit`, `strategy.ts:171-249`): up to `maxCompactionRounds` = 10 forced sweeps. It returns `success: false` after 10 rounds or when a round makes no progress. The caller logs "hard-limit compaction failed, proceeding anyway" and sends the request regardless (prompt.ts:991-1000).
- **Async guard.** One in-flight async job per conversation. Errors are swallowed and logged (strategy.ts:138-166).

**HEAD `dolt` mode** (`config.ts:182-195`; `token-budget.ts:254-402`):
- **Per-lane bands** with hysteresis. A lane starts compacting at `tokens > soft + delta` and continues until `tokens <= target`. Hard-limit risk bypasses the band.
  - leaves: soft 50,000 / delta 5,000 / target 50,000 / cap 50,000, fresh tail 4;
  - sprigs: 10,000 / 2,000 / 10,000;
  - bindles: 10,000 / 2,000 / 10,000;
  - minimum fanout 2.
- **Eviction.** Overflowing bindles are removed from `context_items` and marked off-context (context.ts:684-796). Optional "ghost cue" archive stubs (220 output tokens max) are written to `summaries`, but are not put back into the active context (context.ts:769-780).
- **Recall.** Off-context recall shells out to the external `qmd` CLI for embedding search (`src/session/lcm/retrieval.ts:130-180, 405-432`). This embedding pathway contradicts the paper's statement that no embedding index was implemented. It was added on 2026-02-18, after the paper.

**Summarizer model.** Compaction calls the session's primary model (`summarize.ts:153-155`, with `model: input.model` passed through from `prompt.ts`). In the paper's benchmark that would be Opus 4.6.

### 5.6 Summarization prompts and the escalation path

**Prompts:**
- HEAD prompts are plain text files under `src/session/lcm/prompts/{upward,dolt}/{summarize,condense}/d*.txt`.
- The d1 prompt (`upward/summarize/d1.txt`) asks for a chronological narrative that covers:
  - intent and constraints;
  - decisions and their rationale;
  - concrete code changes (files, functions, APIs);
  - tool findings that changed direction;
  - the end state.

  Target length is "1800-2000 tokens". It tells the summarizer not to add metadata headers or IDs.
- The d2 and d3+ prompts are near copies with "merge chronologically, resolve repetition" wording and the same 1800-2000 token target.
- The summarizer request wraps the source in `<messages>` or `<source_summaries>`. It optionally prepends `<preceding_summaries>` and ends with "Do not continue or answer the source conversation directly" (`summarize.ts:21-74`, `condense.ts:21-74`).
- **Partial input in the paper-era/Dolt path.** `formatMessagesForSummary` truncates each tool output to 1,000 characters before summarization (summarize.ts:290-295). In upward mode the summarizer instead receives the flattened `content` text (context.ts:1744-1771), so this cut does not apply there.

**Escalation at HEAD** (`src/session/lcm/compaction-escalation.ts`, used by `summarize.ts:179-195` and `condense.ts:214-229`):
1. **Normal.** The prompt with `maxOutputTokens` = 2,200 (from code).
2. **Aggressive.** The same prompt plus an appended "Aggressive Compression Override" directive, with `maxOutputTokens = max(128, 0.6 * 2200)` = 1,320 (compaction-escalation.ts:16-25; summarize.ts:79-83).
3. **Fallback.**
   - Acceptance rule for levels 1 and 2: the output must be non-empty and `chars/4 < input tokens` (compaction-escalation.ts:32-37).
   - The fallback then binary-searches the longest prefix of the **source text** whose estimate is strictly below the **input token count**, and appends `[... truncated from N tokens]` (compaction-escalation.ts:43-91).

So the HEAD fallback guarantees only a one-token reduction. A 20k-token chunk can come back as a ~19,999-token "summary".

**Paper-era escalation (944c7943):**
- Level 3 truncated the failed aggressive **summary** to 512 tokens (2,048 chars) and appended `[LCM File IDs: ...]` (paper-era `summarize.ts:378-460`). This matches the paper's `DeterministicTruncate(X, 512)`, except that it truncates the summary rather than `X`.
- Every level persisted its own summary row, so rejected attempts stayed in the DB as orphan summaries linked to the same messages (paper-era `summarize.ts:141, 353, 443`; `context.ts:422-462`).
- The `compaction-escalation.ts` module was added on 2026-02-27 by a commit titled "restore three-pass LCM compaction escalation".

**What escalation does not cover: summarizer errors.** `generateText` is not wrapped in try/catch in `summarize.ts` or `condense.ts`. A provider error, timeout or rate limit therefore propagates:
- async compaction: logged and dropped;
- blocking compaction: the whole turn fails (prompt.ts:1486-1491).

The deterministic fallback only handles output that is not shorter, not the failures most likely to happen in production.

### 5.7 Model-facing tools (HEAD)

| Tool | Parameters | Bounds and behavior | Source |
|---|---|---|---|
| `lcm_grep` | `pattern: string`, `conversation_id: number` (required), `summary_id?: string`, `page?: number` | PostgreSQL POSIX regex `content ~ pattern`. This is not the tsvector index, so each conversation's messages are scanned sequentially. 50 matches per page, ~40 KB per page. Each hit is shown as `seq`, `role` and the **first 200 characters of the message** (not the matched region), grouped by the d1 sprig that covers it, not by the node currently in context. | src/tool/lcm-grep.ts:10-17, 34-53, 140, 172-176; db.ts:2908-2983 |
| `lcm_describe` | `id: string` (`file_` or `sum_`) | Metadata only: level, type, lineage, exploration summary for files. | src/tool/lcm-describe.ts:10-12 |
| `lcm_expand` | `summary_id: string` | Sub-agent only: fails if `session.parentID` is unset. Returns every reachable original message. No LCM-specific bound; output passes through OpenCode's generic `Truncate` (2,000 lines or 50 KB, the rest spilled to a file kept for 7 days). | src/tool/lcm-expand.ts:11-13, 36-54, 133; src/tool/tool.ts:68-82; src/tool/truncation.ts:10-14, 92-99 |
| `lcm_read` | `file_id: string`, `max_bytes?: number` (1 to 100,000,000; default 100,000) | Sub-agent only. Sets `metadata.truncated`, which bypasses the generic truncation, so a sub-agent can pull up to 100 MB into its own context. | src/tool/lcm-read.ts:13-23, 38-58, 145-151 |
| `lcm_expand_query` | `prompt: string`, `summary_ids?: string[]`, `query?: string`, `conversation_id?: number`, `max_tokens?: int` (default 2,000) | Resolves candidates (up to 24) through the mode adapter, then spawns an `explore` sub-agent that calls `lcm_expand` and must reply with JSON `{answer, cited_summary_ids, expanded_summary_count, truncated}`. The answer length is only a prompt target and is not enforced. Not in the paper. | src/tool/lcm-expand-query.ts:13-25, 97-111, 175-197 |
| `llm_map` | `input_path`, `output_path`, `prompt`, `output_schema` (JSON Schema object), `model?` ("small" default, "default" = parent, or `provider/model`), `concurrency?` (16), `timeout_seconds?` (120), `max_attempts?` (3) | Items are claimed with `SELECT ... FOR UPDATE SKIP LOCKED LIMIT 1` and set to RUNNING. Each response is validated against the schema; on failure the original message, the error and the prior response are resent. Output is JSONL `{item_index, ok, result or error}`. The output file is registered in LCM by path. Returns counts and IDs only. | src/tool/llm-map.ts:24-38, 149-151, 253-273, 444-484 |
| `agentic_map` | same, plus `read_only: boolean`; timeout default 900 s; concurrency fixed at 16 | Each item runs as a child session. `read_only` denies edit, write, bash, task and todowrite. | src/tool/agentic-map.ts:27-45, 72-74, 195-229 |
| `task` / `tasks` | `description`, `prompt`, `subagent_type`, `delegated_scope?`, `kept_work?`, `session_id?`, `run_in_background?` | The guard only rejects a non-explore sub-agent call when either string is empty after trimming. | src/tool/task.ts:17-32, 52-64 |

How the model learns its numeric `conversation_id` for `lcm_grep`:
- We could not find any place where it is surfaced to the model.
- The tool trusts whatever integer it receives, and `regexSearchMessages` does not check that the ID belongs to the caller's lineage (db.ts:2963-2975). In single-tenant embedded mode, any conversation in the database is readable.
- Multi-tenant mode isolates users by Postgres schema (`user-context.ts`).

### 5.8 Large file and tool-output handling

- **Read tool.** Files over **7.5 KB (about 2.5k tokens)** read without an explicit offset or limit go to LCM instead of into context. So do reads that would push the context over the soft threshold, or parallel reads above 100k tokens (`src/tool/read.ts:36, 110-117, 253-309`). This 7.5 KB threshold is the same in the paper-era code. The paper's "default >25k tokens" corresponds only to an unused helper constant (`large-file-threshold.ts:21,28`).
- **What is stored for files.** A path reference only (`insertLargeFileFromPath`, db.ts:3117-3155), plus an exploration summary from about 35 type-specific explorers (`src/session/lcm/explore/*`). `lcm_read` reads the **current** file from disk, capped at 100 MB (db.ts:3259-3341). If the file has changed, `lcm_read` returns the new content; if it was deleted, it returns "could not be read".
- **Large tool outputs.** Outputs over **10,000 estimated tokens** are stored as `inline_text` in `large_files` and replaced by a reference, a 2,000-character preview and instructions (`src/session/large-tool-output.ts:11, 84-115`; `src/session/processor.ts:539-571`).
- **The ordering problem.** The generic `Truncate` wrapper (50 KB or 2,000 lines) runs inside every tool's `execute` first (`tool.ts:68-82`). For tools such as `bash`, which do not truncate themselves, LCM therefore receives the already truncated text plus the path of a spill file. The full output lives only in `~/.local/share/.../tool-output/`, which is cleaned after 7 days (`truncation.ts:14, 33-42`).
- **Pasted user text.** Large pasted text is stored inline (`prompt.ts:1520-1600`).
- **File IDs in summaries.** File IDs are extracted from summarized text with regex patterns such as `[Large File Stored: file_x]` and kept in the `summaries.file_ids` column. They are not put into the summary text (summarize.ts:199, 330-354; condense.ts:233-236).

### 5.9 Prompt caching

- **No LCM-specific cache handling.** Caching is OpenCode's generic `applyCaching`: ephemeral breakpoints on the first two system messages and the last two non-system messages (`src/provider/transform.ts:187-224`), plus an optional `promptCacheKey` set to the session ID for OpenAI-style APIs (transform.ts:629-663).
- **Every compaction changes the prefix.** Each async completion rewrites `context_items` near the front (summary lane plus the first raw messages), so the next request misses the cache for everything after the first changed item.
- **Volatile serialization.** Synthetic tool-call IDs depend on position (prompt.ts:1450-1466), so a message's serialized bytes are not stable across compactions.
- **Dolt cue block.** The memory-cue block inserted before the last user message varies from turn to turn.

### 5.10 The OOLONG harness (`packages/voltcode/evals/oolong/`)

This is the reproducibility artifact. It is present at the paper-era commit, and the prompt functions below are identical at HEAD. The only diff is a timeout change.

- **Data source.** `oolongbench/oolong-synth`, `trec_coarse`, validation split, from the Hugging Face datasets-server (`dataset.ts`).
- **Task structure.** The header comment and setup code say: "Only 2 context windows exist, each reused by 25 tasks" (index.ts:16, 796-797). That gives 50 tasks at 131K. Counts at other lengths are not stated.
- **Data never enters context.** Each context window is written to a file. The model works on the file through tools, so the dataset is never placed in the model context.
- **Prompts differ by system:**
  - **Volt** (`buildVoltCodePrompt`, index.ts:505-610) prescribes a full expert method:
    - "Do NOT write a regex or keyword-based classifier";
    - "REQUIRED APPROACH: use the llm_map tool (NOT agentic_map)";
    - batches of 10 items per call, with an exact output JSON Schema, concurrency 16 and a 300 s timeout;
    - then load into SQLite with indexes and answer with SQL.
  - **Claude Code** (`buildClaudeCodePrompt`, index.ts:621-654) gets the data description, label definitions and question, but no method. It is not told to avoid regex classifiers.

  The runner's doc comment says "Same prompt as VoltCode but stripped of VoltCode/LCM-specific references" (index.ts:1215). The code shows much more was removed.
- **Labels are cached per context window.** Both prompts tell the model to cache labels per window and reuse them (index.ts:587-594, 640-644; cache verification at 1120-1140). So each system classifies each window once, and the 25 tasks on that window are aggregate queries over the same labels. At 131K the effective number of independent classification trials is therefore 2 per system, not 50.
- **Unified-prompt history.** The header comment records earlier scores with a shared prompt: "v3 (unified, 'implement classifier'): VoltCode 32-36%, Claude Code 50%" at 512K, and Volt 46% against Claude Code 42% at 256K (index.ts:35-38). These numbers come from a code comment and are not in the paper. They suggest that much of the published gap appeared when the prompts were split and Volt's prompt prescribed the `llm_map` batch workflow.
- **What the benchmark exercises.** Because the data stays in a file and the context stays small, the run exercises `llm_map` plus instruction quality. It barely touches the summary DAG, compaction or `lcm_grep`/`lcm_expand`.
- **Contradictory shipped prompt.** `src/session/prompt/symbolic-recursion.txt:9-12` (present at the paper-era commit) tells the model that for 300+ items, writing its own batch LLM-API script is "almost always better than `llm_map` or `agentic_map`". That is the RLM-style symbolic recursion the paper argues against. `src/session/system.ts:107-129, 222-223` includes this prompt and substitutes the user's API endpoint and **API key** into it.

## 6. Where the code disagrees with the paper

| Topic | Paper | Code | Source |
|---|---|---|---|
| Level-3 fallback | `DeterministicTruncate(X, 512)` | Paper-era: truncate the aggressive *summary* to 512 tokens. HEAD: truncate the *source* to (input tokens - 1). | paper-era summarize.ts:378-460; compaction-escalation.ts:43-91 |
| Level-2 target | bullet points, `T/2` | Same prompt plus an "aggressive" directive; output cap 0.6x | compaction-escalation.ts:16-25; summarize.ts:79-83 |
| Soft-threshold gating ("zero-cost continuity") | No work below `tau_soft` | HEAD default `upward` schedules a sweep every step and compacts once 20k raw tokens sit outside the 32-message tail, whatever the soft threshold | prompt.ts:1010-1014; strategy.ts:68-96 |
| Hard-limit loop | `while Tok(C) > tau_hard` compact | At most 10 rounds; the fresh tail (32 messages in upward) is protected even under the hard trigger; on failure the request is sent anyway | strategy.ts:171-249; context.ts:1339; prompt.ts:991-1000 |
| Large-file threshold | >25k tokens | Read tool uses 7.5 KB (~2.5k tokens) | read.ts:117 |
| "Every tool result persisted verbatim" | yes | Outputs over 50 KB / 2,000 lines are truncated before LCM sees them; the full text sits in a 7-day spill file | tool.ts:68-82; truncation.ts:14 |
| Embedding retrieval | "we have not implemented this" | `qmd` vector recall plus per-turn memory cues (Dolt, added 2026-02-18) | retrieval.ts:130-180 |
| Retrievability of all compacted content via in-context IDs | always | Dolt evicts bindles from active context; stubs are not placed back in context | context.ts:684-796 |
| `lcm_grep` signature | `(pattern, summary_id?)` | Also requires `conversation_id`; adds `page` | lcm-grep.ts:12-17 |
| Grep grouping | "summary node that currently covers them" | The d1 sprig containing the message | db.ts:2963-2975 |
| Indexed FTS backs `lcm_grep` | yes | `lcm_grep` uses unindexed `~` regex; the tsvector indexes serve other functions | db.ts:2949, 2971 vs 2266-2301 |
| Delegation termination | structural guarantee | Non-empty string check only | task.ts:52-64 |
| "exactly-once" map semantics | yes | In-process claim; no recovery for items left RUNNING after a crash | llm-map.ts:253-273 |
| Operator-level recursion replaces model-written loops | thesis of section 3 | The shipped system prompt recommends model-written batch scripts for N >= 300 | symbolic-recursion.txt:9-12 |
| IDs | "stable identifiers", README "content-addressed" | Time-salted summary IDs; file IDs from path, size and mtime | summary.ts:222-229; db.ts:3007-3016 |

## 7. Critical evaluation

### 7.1 Convergence is narrower than claimed

- **What the ladder proves.** Only that each produced summary is shorter than its input (in chars/4 units). At HEAD, "shorter" can mean one token.
- **What it does not prove.** That the active context gets under the hard limit:
  - the upward fresh tail (32 raw messages, each of which may carry several tool outputs of up to ~10k estimated tokens) is never compacted;
  - condensation can stall on fanout or token floors;
  - after 10 rounds the request is sent anyway.
- **Errors are outside the ladder.** Summarizer exceptions bypass the fallback entirely.
- **The estimate is biased low.** chars/4 undercounts code and JSON-heavy text, so "under the hard limit" by estimate can still overflow the provider's real limit. The 20k output reserve is the only margin.
- **For us:** a real convergence guarantee needs:
  - a terminal step that always fits, for example truncating or referencing any item including tail items, down to a fixed absolute budget;
  - a fallback that also fires on summarizer error or timeout;
  - token counts from a provider tokenizer, or a calibrated estimator with an explicit safety margin.

### 7.2 "Lossless" has holes

Retrievability of originals fails or degrades in these cases:

1. **Tool outputs over 50 KB.** Only a truncated copy plus a 7-day spill path reaches the store (section 5.8).
2. **Files.** Stored by path only, so later edits or deletion silently change or lose what `lcm_read` returns. No content hash is recorded to detect drift.
3. **Reverted turns.** OpenCode reverts are not mirrored into LCM. The sync only warns when LCM has more messages than the session (prompt.ts:565-568), and the reverted turns stay in the active context. We did not test this.
4. **Lossy round trip.** The canonical `messages.content` is a lossy, regex-parsed XML serialization. Structured `message_parts` exist alongside it, but the default (non-interleaved) request path rebuilds tool calls by parsing text (prompt.ts:1416-1473). The shipped prompt's instruction "NEVER ... Generate fake `<tool>` tags" (symbolic-recursion.txt:59-61) suggests, though it does not prove, that this format leaked into model behavior.
5. **Dolt eviction.** Evicted bindles drop out of active context, so the paper's in-context-ID guarantee no longer holds.

### 7.3 Asynchronous compaction and cache invalidation

**Concurrency.**
- `replacePositionsWithSummary`, `removeContextPositions` and `normalizeContextLaneOrder` each read all items, `DELETE` every `context_items` row for the conversation and reinsert them.
- They run in separate transactions at the default isolation level and take no conversation lock. Only `appendMessage` locks (db.ts:1151, 1999-2094, 1836-1917).
- An append that commits during an async replacement can be deleted from `context_items` (the message survives in `messages` but silently leaves the context) or can cause a position primary-key conflict. We found this by reading the code; we did not reproduce it.
- The replacement is also not atomic with the following lane normalization.
- **For us:** one writer per conversation, and compaction results committed by compare-and-swap against a context version, with a rebase if the covered items changed.

**Cache.** The paper's footnote says only the new summary and post-compaction messages need fresh KV. With prefix caching, every token after the first changed position must be re-prefilled. Here that means the summary lane and the whole raw tail. The cost is therefore a near-full uncached prefill, plus fresh cache writes, after every compaction, and HEAD upward mode compacts roughly every 20k new raw tokens. The paper measures none of this.

**Summarizer cost (estimate, from HEAD upward defaults).**
- Each 20k-token raw chunk costs one primary-model call: about 20k source tokens + up to 2 prior summaries (~4k) + the prompt as input, and up to 2.2k output.
- Every 8 to 10 sprigs add one d2 call with ~16 to 20k input and ~2k output.
- Roughly 1.3 to 1.4x the raw volume re-read as input, plus about 0.11x as output, all on Opus-class pricing.
- This is affordable next to the agent's own repeated prefill, but it is not "zero cost". A cheaper summarizer model should be the default.

**Per-step DB overhead.**
- Every agent step: a sync, two token-count queries (N+1 lookups of each bindle's inputs, db.ts:1534), a full context read, a parts fetch, and a best-effort JSON snapshot write per appended message.
- Dolt additionally shells out to `qmd update`, `embed` and `vsearch` on the critical path when off-context bindles exist (retrieval.ts:169-180).

**Operational weight.** Embedded PostgreSQL 17.7 is downloaded and managed by the agent, and is only supported on darwin and linux, x64 and arm64.

### 7.4 Summarizer reliability assumptions

- **Fidelity is never checked.** No check that identifiers, paths, error strings or decisions survive summarization. The only acceptance test is length.
- **Narrative, not structured.** Summaries are free prose capped at ~2k tokens. Facts degrade silently at each level (d1 to d2 to d3), and the only recovery path is the model choosing to expand.
- **Main agent cannot expand.** Recall therefore costs a sub-agent round trip. This is a good context-hygiene choice but adds latency and cost for every precise recall.
- **Weak grep usability.** Snippets are the first 200 characters of each message, not the match, so hits deep inside long tool outputs show no useful context.
- **The model is told to hide memory operations** from the user (`prompt/lcm.txt:5-18`). We should not copy that.

### 7.5 What is genuinely good

- **Normalized schema.** Immutable messages, an ordered leaf-edge table, an ordered condensed-edge table, and an explicit `context_items` projection with an exactly-one CHECK and RESTRICT foreign keys. Clean, and easy to port to SQLite or Postgres.
- **Strict level discipline.** dN is built only from d(N-1), enforced at insert time. Together with fanout minimums and a fixed per-node target, the summary footprint grows roughly logarithmically with session length.
- **Engine-injected IDs.** Summary IDs and input IDs are injected by the engine, independent of model output, and counted in the token budget.
- **Sweep heuristics.** Fresh-tail protection, leaf-chunk triggering by token mass outside the tail, shallowest-first condensation, continuity context (previous 2 summaries) for the summarizer, and hysteresis bands in Dolt to avoid compaction thrash.
- **Bounded recall.** Sub-agent-only expansion, with bounded output and spill to file.
- **`llm_map` design.** File-in/file-out JSONL, DB-tracked item states, `SKIP LOCKED` claims, schema validation with error-feedback retries, and a compact summary returned to the parent.
- **Large-object references.** Large content is replaced by an ID, a preview and a type-aware exploration summary. File IDs are propagated through summaries as structured metadata.
- **Integrity checker.** Position contiguity, broken references, orphans, cycles and token drift (`src/session/lcm/integrity.ts`). It is exposed only as a server route.

## 8. Recommendations for our Go engine

### 8.1 Adopt (reimplement the idea)

1. **Three-layer store:**
   - an append-only message log, with structured parts as the canonical form and rendered text derived from them;
   - derived summary nodes;
   - an ordered active-context projection referencing either a message or a summary.
2. **DAG edges as ordered tables** (`summary_id, ord, child_ref`), level stored as an integer depth, the d(N-1) to dN invariant checked on insert, RESTRICT on originals, and a recursive walk for expansion and scoped search.
3. **Engine-injected stable IDs** in every summary header, counted in the budget.
4. **Upward-style sweep:**
   - leaf trigger by token mass outside a protected tail;
   - shallowest-first condensation with minimum fanout (8 at the first level, 4 above);
   - a per-node output target around 2k tokens;
   - prior-summary continuity context.
5. **Hysteresis**: soft + delta to start, target to stop.
6. **An escalation ladder** with output caps and a deterministic fallback, but with a fixed absolute bound (the paper's 512-token idea), not "input minus one".
7. **Sub-agent-only full expansion**, a hard output cap, and a separate cheap `describe`.
8. **A DB-backed map operator** with SKIP LOCKED claims, schema validation and retry-with-error.
9. **Large-object references** with preview plus type-aware exploration summary, and file-ID propagation as structured metadata.
10. **An integrity checker** run in tests and optionally at startup.

### 8.2 Adapt or fix

1. **Content-addressed blobs.** Store large tool outputs and observed file snapshots by content hash (or at least record the hash at observation time) so "lossless" holds. Never truncate before persisting. Truncate only the in-context rendering.
2. **Real convergence:**
   - fallback on error or timeout as well as on "not shorter";
   - a terminal fit step that may shrink tail items to references;
   - a provider tokenizer or a calibrated estimator with a margin;
   - an explicit failure state instead of "proceed anyway".
3. **Async safety:**
   - a single goroutine or actor (or advisory lock) per conversation;
   - versioned context with compare-and-swap commits;
   - compaction computed on a snapshot and rebased if the covered items changed.
4. **Cache-aware layout:**
   - keep the summary lane append-mostly and place a cache breakpoint at its end;
   - make serialization position-independent (stable tool-call IDs from the log, never from context index);
   - batch compactions to limit invalidations;
   - measure hit rate and prefill tokens per compaction.
5. **Present summaries as clearly labelled memory blocks**, not as assistant turns.
6. **A cheaper summarizer model by default.** Record the model, prompt version and input hash on each summary node, so nodes can be regenerated and audited.
7. **Grep:** return match-centred snippets, and derive the scope from the session, never from a model-supplied ID.
8. **Delegation:** use a real depth or budget limit. A free-text scope declaration can be kept only as a nudge.
9. **Map runs:** add crash recovery for items left in RUNNING (lease timestamps) and resumable runs.

### 8.3 Avoid

- Flattening tool calls into XML text and regex-parsing them back.
- Delete-all-and-reinsert rewrites of the context table without a lock.
- chars/4 as the only basis for hard limits.
- Tool parameters that let the model widen its own read scope (conversation IDs, 100 MB reads).
- Hiding memory operations from the user.
- Injecting API keys into prompts.
- Mutating summary rows in place, which makes "materialized views" non-reproducible.
- Benchmarking with system-specific method prompts. Our evals should give every system the same task prompt, report n, distinct contexts, runs and confidence intervals, and test retrieval of history directly. OOLONG-as-run tests `llm_map`, not memory.

## 9. License and reuse

- **Volt: MIT.** Copyright 2025 voltcode, inherited on top of MIT-licensed OpenCode. MIT would let us copy code with attribution (keep the copyright and permission notice).
- **Our policy.** We will not copy code. Reimplementing the ideas described here (schema shapes, the DAG, thresholds, escalation, tool contracts) in Go is unrestricted: ideas and architectures are not covered by the copyright license.
- **Prompts.** If we later want verbatim prompt text from `src/session/lcm/prompts/`, that would be copying MIT-licensed material and would need the notice. Writing our own prompts avoids the question.
- **The paper: CC BY 4.0.** Quoting or adapting its figures or text requires attribution to Ehrlich and Blackman (Voltropy PBC) with a link to arXiv:2605.04050.

## 10. Limits of this note and open questions

- **Nothing was executed.** We did not build or run Volt, its tests or its eval harness. Behavioral statements (races, revert divergence, tail-protection overflow) come from reading the code and are not reproduced.
- **Evaluated revision unknown.** We could not confirm which commit produced the paper's numbers. The public history starts on the paper's date (944c7943), so anything earlier is not visible. HEAD (2026-02-27) has substantially different compaction code (Dolt and Upward modes, a new escalation module).
- **Figure readings.** The per-length raw-Opus scores in section 3.2 are read from a plot and are approximate. The gain labels are exact as printed.
- **lossless-claw.** Code comments name it as a parity target. We did not locate or inspect it.
- **Task counts.** Not determined for context lengths other than 131K (the harness states 50 tasks over 2 windows there). The paper gives no counts anywhere.
- **Not reviewed:** the exploration-summary explorers beyond the dispatcher list, and the Dolt ghost-cue prompt text.
