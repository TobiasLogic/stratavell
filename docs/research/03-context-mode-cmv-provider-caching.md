# 03. Context Mode, Contextual Memory Virtualisation, and provider caching rules

Research note for the asynchronous, losslessly retrievable context engine. Sources were read on 2026-11-26.

## 0. Scope, method, labels

Sources examined:

| Source | Version read | How |
|---|---|---|
| Context Mode, https://github.com/mksglu/context-mode | v2.2.4, commit `cd8c0d23` (2026-11-25) | `git clone --depth 1`, source read only, nothing executed or installed |
| CMV paper, https://arxiv.org/abs/2602.22402 | v2, 2026-10-22 (v1 2026-02-25) | abstract page plus full HTML text at https://arxiv.org/html/2602.22402 |
| CMV code, https://github.com/IngressionLabs/contextual-memory-virtualisation | commit `59d699da` (2026-10-22), package `cmv` 2.2.1 | `git clone --depth 1`, source read only, nothing executed |
| Anthropic docs | Markdown variants (`<page>.md`) of the platform.claude.com pages cited below | `curl` |
| OpenAI prompt caching | https://developers.openai.com/api/docs/guides/prompt-caching (platform.openai.com URL served the same page) | `curl` |
| Gemini context caching | https://ai.google.dev/gemini-api/docs/caching and the `?generate-content=true` variant | `curl` |
| SQLite FTS5 | https://www.sqlite.org/fts5.html | `curl` |

File paths below are relative to each repository root. Line numbers refer to the commits listed above.

Number labels:

- **measured**: the source reports a measurement and the data or harness is in the repo or paper.
- **claimed**: stated by the source without a measurement I could trace, or headline marketing figures.
- **documented**: provider documentation states it as a rule or price.
- **derived**: my own arithmetic from documented or measured inputs; not a measurement.

## 1. Context Mode

### 1.1 What it is, and the license

Context Mode is a plugin for 11 coding agents (Claude Code, Codex, Gemini CLI, Qwen, Copilot CLI, VS Code, Kiro, ZCode, Pi, OpenCode, OMP; adapter rows in `src/adapters/table.ts:30-125`). It combines host hooks (PreToolUse, PostToolUse, UserPromptSubmit, SessionStart, PreCompact) with an MCP server that exposes nine tools (`TOOL_ORDER`, `src/core/contracts/constants.ts:5`). Storage is one SQLite file per project at `~/.context-mode/projects/<projectKey>/context.db` (`constants.ts:165-171`), opened through `node:sqlite`, WAL mode, `busy_timeout = 5000` (`src/core/store/schema.ts:12-15`).

License: **Elastic License 2.0** (`LICENSE`, `package.json` `"license": "Elastic-2.0"`). It is source-available, not open source: it forbids providing the software "to third parties as a hosted or managed service". The README also advertises a separate paid hosted product, "Context Mode Gateway" (`README.md:9`). Consequence for us: ideas are free to reuse, code is not something to copy into a product that might be offered as a service.

### 1.2 How it intercepts tool output

Two paths, chosen per host (`src/adapters/table.ts:129-148`):

1. **Deny and redirect (PreToolUse).** A `Read` with no `offset`/`limit` on a file larger than `OUTPUT_CAP = 16384` bytes is denied with a message naming the real byte count and three alternatives (`docs/algorithms.md:24-41`, `constants.ts:15`). WebFetch, curl/wget and build tools are redirected to `ctx_execute` or `ctx_index` + `ctx_search` on hosts without a replace channel (deny texts at `constants.ts:281-295`). A shell parser (`src/core/policy/lexer.ts`, `parse.ts`, `unwrap.ts`) walks `bash -c`, heredocs and pipes before deciding.
2. **Run, then replace (PostToolUse).** On hosts with a replace channel (`REPLACE_ROWS`, `table.ts:135`: claude-code, gemini, copilot-cli, pi, opencode, omp) the tool runs normally and its result is swapped before the model sees it. On Claude Code the swap is the hook field `updatedToolOutput`, rebuilt in the host's own output shape (`src/adapters/claude-code.ts:188-213`).

The replace path is `postReplace` in `src/core/hooks/dispatch.ts:478-571`:

- Pass through whole if the output is at most `REPLACE_TRIGGER_BYTES = 4096` (`dispatch.ts:486`, `constants.ts:33`), if it is a ranged Read (`boundedRead`, `dispatch.ts:612-614`), a skill load, one of the agent's own instruction files, or an MCP result with non-text blocks (`dispatch.ts:482-490`).
- Otherwise the whole output is chunked into line-aligned chunks of at most `REPLACE_CHUNK_BYTES = 48` bytes (`dispatch.ts:521`, `constants.ts:46`), stored as a source with chunks and "line units", and the model receives at most `REPLACE_RETURN_BYTES = 96` bytes of selected chunks (`constants.ts:43`) plus one handle line of the form `ctx_search({queries:["<intent>"], source:"s<base36 id>"}) searches the whole output (<N> B).` (`constants.ts:52-55`).
- The ranking query is the tool "intent" (first non-empty of `description`, `command`, `file_path`, `pattern`, `url`, tool name; `dispatch.ts:517-518`) plus the words of the session's last user prompt in any-word mode (`dispatch.ts:546-549`).
- A structural digest is tried first: if the output parses as a JSON array or a uniform log, rare rows or lines named by the query are returned whole within `SHAPE_RETURN_BYTES = 256` (`dispatch.ts:567-568`, section 1.6).
- Writes up to `SYNC_INDEX_MAX_BYTES = 65536` happen inside the hook; above that only the source row is reserved synchronously (so the handle exists) and chunks are written detached after the hook returns (`dispatch.ts:534-539`, `constants.ts:25`). Under lock contention writes go to a spool directory (`src/core/hooks/spool.ts`).
- "Lost bet": if a full Read was replaced and the model later does a ranged Read of the same file, further outputs with that key pass whole up to `OUTPUT_CAP` for the rest of the session; a compaction clears the marks (`dispatch.ts:528-531`, `588-598`; `constants.ts:418-422`).

The rationale for the constants is documented as an offline replay sweep: trigger 4096 saves 41.99% of bytes, 6144 saves 30.89%, 8192 saves 23.34% on 602 files and 28,390 calls (`constants.ts:29-32`) (measured, offline byte count, no model in the loop). The return budget went 1024 -> 512 -> 96 and the chunk size 256 -> 48 through further sweeps (`constants.ts:43-46`).

### 1.3 The "sandbox"

`ctx_execute` (`src/core/tools/execute.ts`) writes the code to a temp dir (or passes it with `-c` for shells), spawns the runtime from an argv list with `shell: false`, in its own process group (`src/core/sandbox/spawn.ts:50-52`), with the parent environment minus `NODE_OPTIONS`, `BUN_OPTIONS`, `DENO_OPTIONS` and other preload variables (`src/core/sandbox/env.ts:7-43`). Timeout defaults to and caps at 600,000 ms; the whole tree is killed (`constants.ts:125`, `docs/algorithms.md:176-195`).

It is **not an isolation boundary**. The README says so directly: "Code from `ctx_execute` runs on your host as your user, with your files, your network and your environment except the preload variables ... Here "sandbox" means the output stays in the sandbox and out of the context." (`README.md:600`).

Output handling (`execute.ts:139-169`): if there is no `intent` and the whole reply fits `OUTPUT_CAP`, the output is returned as is. Otherwise the full output is indexed and whole chunks are selected against `intent`, within `OUTPUT_CAP` if it would have fit, else within `EXEC_PREVIEW_BYTES = 4096` (`constants.ts:18`), ending with a `ctx_search(...)` pointer.

### 1.4 What it stores: schema and tokenizers

Schema version 7, built by ordered migrations in `src/core/store/schema.ts`:

| Table | Definition | Purpose |
|---|---|---|
| `events` | rowid `seq`, `session_id`, `worktree`, `agent`, `kind` in (`prompt`,`edit`,`cmd`,`toolerr`,`index`,`cost`,`decision`), `tool`, `key`, `path`, `ok`, `exit_code`, `bytes`, `body`, `turn`, byte counters, timestamps (`schema.ts:29-49`) | session journal |
| `sources` | `id` AUTOINCREMENT, session/worktree/agent, `kind` in (`path`,`content`,`url`), `label`, `bytes_total`, `chunk_count`, timestamps, `unit_lo`/`unit_hi` (`schema.ts:51-64`, `145-152`) | one row per stored output or indexed file |
| `chunks` | FTS5, metadata columns `UNINDEXED`, indexed `body` and `title`, `tokenize = 'porter unicode61 remove_diacritics 2'` (`schema.ts:21`, `91-106`) | word search; `title` is the markdown heading path |
| `chunks_tri` | FTS5 `tokenize = 'trigram'`, `content = ''` (contentless), same rowid as `chunks` (`schema.ts:112-121`) | substring search for identifiers, paths, partial words |
| `chunks_vocab` | `fts5vocab(chunks, 'row')` (`schema.ts:120`) | vocabulary for typo correction |
| `line_units` | FTS5 over `body` with the porter tokenizer, `source_id`/`session_id`/`ord` unindexed (`schema.ts:125-133`) | per-line units for post-compaction recall |
| `meta` | key/value | once-markers, seen lists, compaction boundaries |

The porter tokenizer replaced plain `unicode61 remove_diacritics 2` in schema v3; FTS5 has no ALTER for tokenizers, so the table is rebuilt and every row copied (`schema.ts:89-106`). The trigram table is contentless, so deletes must re-supply the indexed text through the FTS5 `'delete'` command (`src/core/store/fts.ts:93-95`, `182-196`).

Retention: rows idle for 30 days are deleted, then oldest-accessed sources until the project is under 500 MB; whole dead projects after 14 days (`constants.ts:167-169`, `src/core/store/cleanup.ts:33-60`). "Lossless" therefore only holds inside the retention window: a handle in an old transcript can dangle.

There is **no content addressing and no staleness tracking**. `storeText` (`src/core/index/text.ts:12-27`) always inserts a new `sources` row. Re-indexing a file or replacing a second run of the same command creates another source with its own id; earlier copies stay searchable and only lose ties to newer ones ("newest source first", `fts.ts:54-55`). Nothing links an `edit` event on a path to earlier read outputs of that path.

### 1.5 Chunking

`src/core/index/chunk.ts`:

- **Markdown** (`mdChunks`, `chunk.ts:39-71`): chunks never cross an ATX heading; each chunk carries the heading path `A > B > C` as `title`. Blocks of one section merge greedily up to `MAX_CHUNK_BYTES = 4096` (`constants.ts:81`). A fenced block that fits is never split; a larger one is split on its blank lines and every piece repeats the opener and closer lines so it stays a valid fence (`fencePieces`, `chunk.ts:75-83`). A run of table lines is its own block (`lineBlocks`, `chunk.ts:94-151`).
- **HTML** (`htmlBlocks`, `chunk.ts:205-250`): split after a closing tag that returns element depth to 0; comments and `script`/`style` bodies are skipped whole. URLs indexed through `ctx_index` are converted to readable Markdown and the raw HTML is kept as a separate `"<label> (raw)"` source excluded from default search (`src/core/tools/index.ts:136-155`, `fts.ts:40-41`).
- **Plain text** (`merge`, `chunk.ts:332-397`): every blank-line block is its own chunk (no merging). With a small cap (the 48-byte replace path) a long block is cut after its last whole line that fits; a single line longer than the cap is kept whole up to 4096 bytes, and only a longer line is cut on a code point.
- Markdown structure is auto-detected for inline content (`hasMdStructure`, `chunk.ts:154-164`).
- **Line units** (`src/core/index/units.ts:25-49`): each non-blank line becomes a unit made of its indentation ancestors plus the line, so a JSON value keeps its parent keys and a TAP diagnostic keeps its `not ok` line. Bounded per unit by `RECALL_BUDGET_BYTES = 2048` and per output by 8x its bytes of ancestor copies (`constants.ts:414`, `424`).

### 1.6 Structured extraction ("shape digest")

`src/core/index/shape.ts` reduces an output to its "rare units" when a parser finds structure:

- **JSON arrays** of at least `SHAPE_MIN_ROWS = 20` objects (`shape.ts:21`). For each scalar key, a key is "skewed" when one value holds at least 90% of rows and the others hold at most 2 rows; the rows with the minority values are rare (`rareOfSkewed`, `shape.ts:76-89`; `jsonShape`, `shape.ts:110-143`). A categorical key with no dominant value but one-off values also qualifies (`shape.ts:83-88`). If no key is skewed but the query names a numeric key, the min and max rows are returned (`jsonExtremes`, `shape.ts:180-207`). Rows that do not fit are rendered with whole fields only, skewed keys first (`fitRow`, `shape.ts:152-169`).
- **Logs** (`lineShape`, `shape.ts:224-271`): a Drain-style template miner. Tokens containing a digit become `<*>`; lines are grouped by token count and first token; a line joins the first cluster whose template matches at least 50% of positions. More than 64 clusters, more than 4 common templates, or a common template with no literal token means "not a log". Rare = clusters of at most 2 lines plus lines with a rare token at a skewed position. More than 9 rare units means "no strong structure" and the code falls back to BM25 chunks.
- The digest header is a count line such as `N lines, K rare:` or `N objects {keys}, K rare:`; rare units are ranked by BM25 against the query and only units the query matches are returned.

These thresholds (`shape.ts:21-29`) are heuristics; the only evidence in the repo is fixture tasks in the live A/B and the held-out set notes (for example the `mcp-1` blocked-ticket case in `docs/architecture/modules/DEVIATIONS.md:553`).

### 1.7 Search ranking

`ftsQuery` in `src/core/store/fts.ts:364-417`:

1. Each whitespace-separated query word becomes a quoted FTS5 string with a prefix star, so user text can never inject FTS5 operators (`toMatch`, `fts.ts:205-219`). A fixed English stopword list is dropped unless the query is only stopwords (`fts.ts:229-243`).
2. **Porter list**: one `MATCH` with the terms joined by OR; rows that match the AND form ("full") rank before partial matches. BM25 column weights: body 1.0, title 1.0 (`TITLE_WEIGHT`, `fts.ts:34`; title weights 2, 5, 10 were measured and lost "whole fences", `DEVIATIONS.md:532`).
3. **Typo fallback**, only when the porter list is empty: each query stem of 5+ characters absent from `chunks_vocab` is replaced by the nearest vocabulary term within Levenshtein distance 2 (ties: higher document count), then the porter pass reruns (`typoTerms`, `fts.ts:313-349`).
4. **Trigram list**: every non-stopword query word of 3+ characters as a quoted substring, plus the whole query when it has several words, joined by OR, ranked by trigram BM25 (`trigramMatch`, `fts.ts:252-259`; `TRI_RANK`, `fts.ts:77-86`).
5. **Fusion**: Reciprocal Rank Fusion with `RRF_K = 60` (`constants.ts:119`) over the porter and trigram lists (`fts.ts:394-402`). Final order: current session first, then current worktree, then AND matches, then fused score, then porter rank, newest source, chunk order (`fts.ts:403-407`). For `scope: "project"` the porter SQL itself also fuses a BM25 rank with a recency rank by RRF (`PORTER_RANK_PROJECT`, `fts.ts:56-65`).
6. Replies hold 5 hits (`SEARCH_REPLY_HITS`, `constants.ts:85`). A snippet is one contiguous slice of stored text chosen by a per-block BM25 score (k1 1.2, b 0.75), grown to whole blocks, at most 800 bytes for prose and 2048 for a fenced code block (`src/core/store/snippet.ts:1-16`, `constants.ts:86-87`). Query identifiers are matched as whole symbols (double weight) and as camelCase/snake/path parts.

In-memory ranking for the replace path uses the same tokenizer and BM25 through a private `:memory:` contentless FTS5 table (`rankTexts`, `fts.ts:609-636`), so hook-time selection and stored search score identically.

Retrieval quality (measured, by the project on its own held-out set `e2e/fixtures/search/heldout.json`: 50 queries, kinds natural 14, identifier 8, typo 8, code 7, morph 6, phrase 5, flag 2; sources are mostly library docs such as Context7 Supabase pages), from `docs/architecture/modules/DEVIATIONS.md:531-533`:

| Change | Recall@5 | MRR@10 |
|---|---|---|
| implicit AND (baseline) | 0.40 | 0.347 |
| stopwords out, OR with AND-first | 0.78 | 0.678 |
| porter stemming | 0.80 | 0.697 |
| trigram list + typo fallback + RRF | 0.86 | 0.720 |
| (rejected) `tokenchars '_-'` | 0.76 instead of 0.78 | |

Search p95 went from 5.7 to 12.1 ms with 1 MB extra indexed after adding trigram (measured, `DEVIATIONS.md:533`). The trigram index size question is recorded as an open RFC (#962) in the same row. MMR on top of RRF was measured and dropped: no gain, about 2x p50 latency (`docs/algorithms.md:77-79`).

### 1.8 Intent-driven filtering

"Intent" appears in three places: the explicit `intent` parameter of `ctx_execute` (`execute.ts:37`, `144-158`), the derived intent of a replaced host tool call (`dispatch.ts:517-518`), and the last user prompt added to the ranking query (`dispatch.ts:546-549`). Filtering is selection of whole chunks or whole units ranked by BM25; nothing is cut mid-chunk and the full output stays indexed (`src/core/index/select.ts:37-61`; the "no lossy cut" rule with a per-call-site audit table in `docs/architecture/NO-LOSSY-CUT.md`). No regex and no LLM call is used anywhere in selection (stated as an owner rule, for example `chunk.ts:2`, `shape.ts:5-6`).

### 1.9 Session continuity and resume

- **Journal**: hooks append `events` rows for prompts, edits, shell commands (keyed by a canonical command key), tool errors (keyed by tool name plus sorted-key JSON of the input, `dispatch.ts:405-414`), index operations and decisions (answers to AskUserQuestion) (`dispatch.ts:437-469`).
- **State block after compaction**: on `SessionStart` with `source = compact` (Claude Code) or a pending flag (other hosts), a state block of at most `STATE_BUDGET = 2048` bytes is injected (`dispatch.ts:157-182`, `constants.ts:19`). Candidates: the last request (pinned), open errors (latest failure per key with no later success), changed files (last write per path), decisions (last per key), earlier requests, indexed sources (`src/core/compaction/candidates.ts:54-135`). Score = `WEIGHT[kind] x decay(age) x (1 + floor(log2(repeat)))`, weights toolerr 900, decision 700, prompt 500, edit 400, index 200, decay `0.25 + 0.75 x 2^(-age/4)` in turns (`src/core/compaction/score.ts:15-24`, `constants.ts:99-117`). Rows are never cut; a row that does not fit becomes a pointer.
- **Turn-boundary recall** ("push, not pull"): after a compaction, every `UserPromptSubmit` runs one BM25 query of the current prompt (first 64 distinct non-stopword words) over this session's line units and journal rows written before the compaction boundary, keeps rows scoring at least 5, picks by MMR (lambda 0.7) within 2048 bytes, and injects each row at most once per session (`dispatch.ts:249-269`, `src/core/compaction/recall.ts:38-60`, `constants.ts:414-429`). The code comment gives the motivation: "a model does not call a tool for state it does not know it lost (0 of 50 clean T-CMP sessions)" (`recall.ts:1-3`) (claimed; the run data is not in the clone).
- **Tools**: `ctx_resume({seq?, session?})` returns up to 2 KB of state for the session and pages large rows by `seq`; `ctx_search(sort: "timeline")` lists journal rows, stored sources and memory files oldest-first with an epoch-ms cursor (`fts.ts:476-601`).

Recall evaluation (measured, tiny n): shipped policy 10/10 correct on Sonnet 5.5, 4/5 on Opus 5.5, baseline 10/10 (`bench/RESULTS.md`, section "Compaction push recall"). The bench notes that this run "does not separate" the arms.

### 1.10 Tools and parameters

| Tool | Parameters (zod schema) | Source |
|---|---|---|
| `ctx_execute` | `language`, `code` (required); `file`, `intent`, `timeoutMs` (max 600000), `background`, `summary` | `src/core/tools/execute.ts:33-41` |
| `ctx_batch` | `commands: string[]`, `queries: string[]` (both required) | `src/core/tools/batch.ts:25-28` |
| `ctx_index` | exactly one of `path`, `content`, `url`; optional `source` label | `src/core/tools/index.ts:26-31` |
| `ctx_search` | `queries: string[]`; `scope` (`session`/`worktree`/`project`), `source`, `offset`, `sort` (`relevance`/`timeline`), `after` | `src/core/tools/search.ts:23-29` |
| `ctx_resume` | `session`, `seq` | `src/core/tools/resume.ts:24-26` |
| `ctx_purge` | one of `session`, `source`, `project` | `src/core/tools/purge.ts:24-27` |
| `ctx_stats`, `ctx_doctor`, `ctx_upgrade` | none | `src/prompt/tools/*.md` |

Tool descriptions are build-time literals (`src/prompt/tools/*.md`, `src/prompt/instructions.md`).

### 1.11 Prompt-cache handling

ADR-0021 (`docs/architecture/adr/0021-prompt-cache-safety.md`) fixes the rules: `tools/list` and `initialize` are byte-identical across starts, the server never sends `list_changed`, tool and instruction text contain no dynamic tokens (checked in CI by `build/check-prompt-static.mjs`), hook text goes through append channels (tool result or user turn), and a compaction in one session never changes another session's payload. The measured cost of the plugin itself: about 1,200 extra prefix tokens from its 9 tools (36 vs 27 tools in init), paid as a cache write once per build or description change (`bench/RESULTS.md:154-162`) (measured, 2 sessions per arm, "direction only").

The replace text is computed once at PostToolUse, before the result is first sent, so the transcript is never rewritten afterwards. That is the right shape for both prompt caching and Anthropic's preserved-thinking prefix check (section 3.7).

### 1.12 Token-savings claims versus measurements

| Figure | Label | Source |
|---|---|---|
| "98%" reduction (Hacker News title) | claimed | `README.md:40` |
| 99.4% median output bytes saved over 36 bench rows with at least 10 KB raw output | measured per call, scripted bench rows, bytes not tokens | `README.md:29`, `398` |
| Full scripted session 281,261 B raw to 3,266 B in context (98.8%), "about 70K to 817 tokens" at 4 B/token | measured bytes, tokens estimated | `README.md:13`, `379` |
| Search replies save 77.2% | measured bytes | `README.md:409` |
| Offline replay of 820 real sessions, 23,175 calls: corpus byte saving 1.1% (old policy), 6.8% with the later replace model | measured bytes, no model | `bench/RESULTS.md:41-49`, `61-86` |
| Live A/B, 20 tasks x 3 runs x 2 arms (120 sessions): session input median -6.7% (plugin worse), fresh input -18.6% (worse); both arms 60/60 correct | measured | `bench/RESULTS.md:90-108` |
| Final A/B 2: small tasks session tokens 38,210 vs 36,184 off (+5.7%); large tasks 60,390 vs 55,897 (median over tasks "-19.6%"); cost on large tasks $0.391 vs $0.607 | measured, 45 and 36 sessions | `bench/RESULTS.md:11-16`, `466-469` |
| Final A/B 3: large tasks -3.8% tokens vs off, $0.345 vs $0.612; small tasks +5.6% | measured, 36 and 45 sessions | `bench/RESULTS.md:472-490` |
| Hosted Gateway: -37.6% dollars per long session | claimed here (3 pairs, data on vendor site, not in repo) | `README.md:660`, `676` |

The authors' own reading (`bench/RESULTS.md:15-18`): the plugin cuts bytes the model reads on large outputs (734.5 vs 4,694.5 median tool_result bytes) but "does not yet cut session tokens"; on small tasks it costs a fixed ~2,000 tokens of tool schemas and hooks; on some large tasks the model spends extra turns calling `ctx_*` tools (multi-1 +29%, big-json +74%). The repo is unusually honest about this, and the headline README numbers should be read as per-output byte reductions, not end-to-end token or cost savings.

Hook latency (measured, `docs/performance.md:7-20`): the Claude Code hook p95 is 41 ms against a 40 ms budget (FAIL), a 64 KB PostToolUse 62 ms against 60 ms (FAIL), a 1 MiB index about 2x over budget (114 to 134 ms).

## 2. Contextual Memory Virtualisation (CMV)

### 2.1 Bibliographic facts

- Title: "Contextual Memory Virtualisation: DAG-Based State Management and Structurally Lossless Trimming for LLM Agents".
- Author: Cosmo Santoni (Imperial College London; Ingression Labs). Single author.
- arXiv:2602.22402, v1 2026-02-25, v2 2026-10-22. Subjects cs.SE (primary), cs.AI, cs.HC, cs.OS. 11 pages, 6 figures, CC BY 4.0. The abstract page shows no journal reference; it is a preprint.
- Code: https://github.com/IngressionLabs/contextual-memory-virtualisation, **Apache-2.0**, TypeScript CLI and TUI for Claude Code session JSONL files (`src/`, about 7,700 lines).

### 2.2 The virtual-memory analogy, as specified

The paper frames the context window as RAM and saved session state as pages that "the user [can] page ... in and out of active context as needed" (paper section 1). In the specification and the code, this is a metaphor rather than a mechanism:

- There is **no working set, no page fault, no eviction policy and no automatic page-in**. "Paging in" means the user branches a new session from a stored snapshot.
- **Snapshot** (section 2.2): an immutable copy of a session JSONL plus metadata (name, timestamp, source session, estimated tokens, tags). **Branch(v, trim)**: a new session with a fresh UUID from snapshot v, trimmed by default, optionally with an orientation message as the first user line. **Trim**: snapshot plus branch in one step. **Tree**: ASCII rendering of lineage.
- The "DAG" is a tree: the paper says so ("a directed tree (a strict subclass of Directed Acyclic Graphs)"), keeping the DAG term for future merges (section 2.1). No merge exists.

### 2.3 The three-pass trimming algorithm

Paper Algorithm 1 and `src/core/trimmer.ts`:

1. **Pass 1** (`trimmer.ts:248-276`): find the last compaction boundary with a cheap `String.includes` check, parsing only candidate lines (`system/compact_boundary`, or an old-style bare `summary`; `isCompactionBoundary`, `trimmer.ts:32-35`). The code, unlike the paper, keeps pre-boundary content when the boundary records a preserved segment (partial compaction, `trimmer.ts:42-45`, `276`).
2. **Pass 2** (`trimmer.ts:278-306`): collect `tool_use` ids from the skipped pre-boundary lines.
3. **Pass 3** (`trimmer.ts:308-429`): skip pre-boundary lines; drop `file-history-snapshot` and `queue-operation` records; strip image blocks inside tool results; replace any tool result over the threshold with `[Trimmed tool result: ~N chars]`; stub large non-whitelisted string inputs of tool calls; remove all `thinking` blocks; drop `tool_result` blocks whose `tool_use` was skipped (orphans); delete `usage` metadata; drop rows left empty and re-link their children's `parentUuid` to the nearest kept ancestor (`trimmer.ts:390-416`).

Threshold: 500 characters default, minimum 50 (`trimmer.ts:8`, `227`). Never-stubbed input fields: `file_path`, `notebook_path`, `command`, `description`, `prompt`, `pattern`, `path`, `url`, `skill`, `args`, and a few notebook fields (`trimmer.ts:61-76`).

Divergence between paper and code: the paper's Algorithm 1 stubs write-tool inputs over the threshold. The current code does **not** by default, because "these fields are the file content the model may re-apply on resume, so stubbing them silently corrupts files" (`trimmer.ts:12-18`, `228`; README "Write/Edit payloads are never stubbed by default"). That is a real failure mode the author found after publication.

Auto-trim (not evaluated in the paper): `npm install` runs a postinstall script that registers PreCompact and PostToolUse hooks in Claude Code settings (`package.json` `postinstall`, `src/postinstall.ts`). The PostToolUse hook rewrites the session JSONL in place, via a temp file and atomic rename, when the file exceeds 600 KB and has grown 64 KB since the last trim, after saving one of 5 rotating backups (`src/commands/auto-trim.ts:11-18`, `121-151`). How a running host reacts to its transcript being rewritten is not covered by the paper and I did not verify it.

### 2.4 Evaluation and results

Method (paper section 4.2): 76 sessions from one user's Claude Code install (Claude Opus 4.6) over three months, excluding subagent sessions and sessions with fewer than 10 messages or 5,000 tokens. Tokens are estimated from bytes with a chars/4 heuristic plus a fixed system overhead (`SYSTEM_OVERHEAD_TOKENS = 20_000`, `src/core/analyzer.ts:13`, `236-242`). Cache hit rate is assumed, not measured: h = 0.9. Break-even is capped at 60 turns.

Cost model (section 4.1): `C(T,h) = T/1e6 x (h x P_read + (1-h) x P_write)`; cold turn `C_cold(T) = T/1e6 x P_write`; penalty `C_cold(T_post) - C(T_pre,h)`; savings `C(T_pre,h) - C(T_post,h)`; break-even `n* = ceil(penalty/savings) + 1`.

Results (paper Tables 2 and 3; reductions are measured on JSONL bytes and converted to tokens by the heuristic, so I label them "measured bytes, estimated tokens"):

| Group | n | Mean reduction | Median reduction | Mean break-even |
|---|---|---|---|---|
| All | 76 | 20% (max 86%, min 0%) | 12% | 35 turns (median 38) |
| Mixed (tool results at least 15% of JSONL bytes) | 12 | 39% | 33% | 10 turns |
| Conversational (under 15%) | 64 | 17% | 10% | 40 turns |

Mean cache-miss penalty $0.30 (median $0.32) at Opus 4.6 prices. The committed stats file `data/cmv_benchmark_stats.json` labels its run "Sonnet 4" with mean penalty $0.1843; multiplying by 5/3 (the Opus 4.6 to Sonnet 4 price ratio) gives the paper's figure, and break-even turns are invariant under a uniform price scale (derived), so the two are consistent, but the paper's figures are a rescaling rather than a separate run.

No answer-quality evaluation exists. The paper says: "We have not yet quantified the impact on downstream reasoning accuracy in a controlled setting" (section 5). The "context rebuilding" cost that is presented as the main value ("10-20 user turns and 15-30 minutes", section 4.4) is anecdotal and explicitly unquantified.

### 2.5 Critical assessment

1. **"Structurally lossless" is lossy for tool data.** User and assistant text survive verbatim; tool outputs are replaced by a size stub with no pointer back. The source JSONL survives as a snapshot or rotating backup, but the model has no handle to retrieve the removed bytes. The stated recovery is "the model simply re-reads the file" (section 3), which is wrong for anything non-reproducible (command output, test runs, web pages, API responses, files edited since) and returns current content for files, which may differ from what the assistant's surviving summary was based on.
2. **The reduction metric includes bytes that are never sent to the API.** `analyzeCacheImpactWithRealTrim` applies the whole-file JSONL byte reduction ratio to the estimated prompt tokens (`src/core/cache-analyzer.ts:211-218`). The file ratio includes pre-compaction lines, file-history snapshots, queue operations and usage metadata, which a host does not resend as prompt after a compaction (inference from the boundary semantics the trimmer itself relies on). The paper attributes the 60 to 86% tail "primarily" to pre-compaction skipping (section 4.3). Those sessions' reductions are therefore mostly not prompt-token reductions.
3. **The cost model overstates savings for append-only conversations.** It bills `(1-h)` of the whole context at the write rate on every steady-state turn. In an append-only conversation with a breakpoint at the tail, the steady-state cost is the cached prefix at the read rate plus only the new suffix at the write rate. Under that model, trimming saves `(T_pre - T_post) x P_read` per turn. Worked example (derived), `T_pre = 100k`, `T_post = 60k`:
   - Opus 4.6 prices ($6.25 write, $0.50 read): CMV model gives break-even at 8 turns; append-only model gives 18 turns.
   - Opus 5.5 prices ($5 write, $0.20 read, section 3.1): CMV model 10 turns; append-only model 36 turns.
   Cheaper cache reads on newer models make retroactive trimming pay back more slowly, not faster.
4. **Thinking removal.** Removing all thinking blocks is allowed by Anthropic's rules (omit "the oldest, the newest, or all of them, but none in between", section 3.7), so the branched session does not 400 on preserved-thinking models, but all prior reasoning is lost. The paper calls signatures "non-portable across sessions"; the docs say signatures are compatible across platforms and that the prefix check compares content, so "the same content in a new request or a fork passes it". Thinking becomes invalid in CMV branches because the history before it changed (trimmed), not because signatures are session-bound per se (some models are additionally account-bound).
5. **Orphan handling** is a real API constraint and the code handles it carefully (orphan stripping and `parentUuid` re-linking). Worth copying as an invariant.
6. **Single user, one model, assumed hit rate, byte heuristics, no variance, no quality measurement**, and the auto-trim mode that most users get by default (installed by postinstall) is not evaluated at all.

### 2.6 Ideas worth taking from CMV

- Immutable session snapshots with lineage (parent snapshot, source session, tags) as a first-class object: a cheap way to fork a warmed context into several tasks.
- Treat any history rewrite as an explicit "cold-cache event" and compute its break-even before doing it, but with an append-only cost model and measured `usage` fields, not h = 0.9 and chars/4.
- A whitelist of identifying fields that must survive any stubbing (`file_path`, `command`, `url`, `pattern`, `description`): the model keeps knowing what it did even when it lost the output.
- Never stub write payloads (Edit/Write inputs): the author found this corrupts files on re-application.
- Tool-call pairing invariants (every `tool_result` has its `tool_use` in the preceding assistant turn) enforced mechanically during any transformation.

## 3. Anthropic documentation (what our cache-aware assembler must obey)

All fetched 2026-11-26 from the Markdown variants of the URLs given.

### 3.1 Prompt caching

URL: https://platform.claude.com/docs/en/build-with-claude/prompt-caching

- **Two modes.** Automatic caching: one top-level `cache_control: {"type": "ephemeral"}`; the breakpoint is applied to the last cacheable block and moves forward as the conversation grows. Explicit breakpoints: `cache_control` on individual blocks. They combine; the automatic breakpoint uses one of the 4 slots. Edge cases (documented): a no-op if the last block already has an explicit breakpoint with the same TTL; a 400 if it has one with a different TTL; a 400 if 4 explicit breakpoints already exist; if the last block is not eligible, the system silently walks backward to the nearest eligible block, else caching is skipped. Automatic caching is unavailable on Amazon Bedrock `bedrock-runtime` (400 for top-level `cache_control`).
- **Prefix order**: `tools`, then `system`, then `messages`, "up to and including the block designated with `cache_control`". Changes at one level invalidate that level and all later levels.
- **Breakpoints**: up to **4** per request (documented). Breakpoints are free; only writes, reads and uncached input are billed.
- **Lookback** (documented, "How automatic prefix checking works"): writes occur only at breakpoints (one entry, a cumulative hash of the prefix ending at that block). Reads check the breakpoint and then walk backward one block at a time, at most **20 positions per breakpoint** (the breakpoint itself counts as the first), looking for entries that earlier requests wrote. "On the Claude API, a run of consecutive `tool_use` blocks counts as one position, and so does a run of consecutive `tool_result` blocks." If a turn adds 20 or more blocks past the last write, add a second breakpoint. The lookback does not find stable content behind a changing breakpoint; put the breakpoint on the last block that is identical across requests.
- **Minimum cacheable length** (documented; Claude API, Claude Platform on AWS, Google Cloud Google-managed, Foundry); shorter prompts are processed uncached with no error, and both cache usage fields are 0:
  - 512 tokens: Claude Fable 5.2, Mythos 5.2, Fable 5.1, Mythos 5.1, Opus 5.5, Opus 5, Sonnet 5.5, Fable 5, Mythos 5, Haiku 5.5.
  - 1,024: Opus 4.8, Sonnet 5, Sonnet 4.6, Sonnet 4.5, Opus 4.1, Opus 4, Sonnet 4.
  - 2,048: Mythos Preview, Opus 4.7.
  - 4,096: Opus 4.6, Opus 4.5, Haiku 4.5.
  - Bedrock (`bedrock-mantle`) has its own minimums and field names (AWS docs).
- **TTL and lifetime**: default 5 minutes, refreshed at no cost on each use; optional `"ttl": "1h"`. The lifetime runs from the **start** of the request that writes or reads the entry, so generation time counts against it. Mixed TTLs are allowed if longer-TTL entries come before shorter ones; billing uses positions A (last hit), B (last 1h breakpoint after A), C (last breakpoint): reads for A, 1h writes for B - A, 5m writes for C - B. A cache entry becomes available only after the first response starts, so parallel requests should wait for the first response.
- **Pricing multipliers** (documented): 5-minute writes 1.25x base input; 1-hour writes 2x; reads 0.1x, except reads at **0.025x** on Fable 5.2, Mythos 5.2, Fable 5.1, Mythos 5.1 and **0.05x** on Opus 5.5 and Sonnet 5.5. Examples: Opus 5.5 $4 base, $5 5m write, $8 1h write, $0.20 read; Sonnet 5.5 $2 / $2.50 / $4 / $0.10; Haiku 5.5 (up to 100k-token prompts) $0.10 / $0.125 / $0.20 / $0.01. Multipliers stack with Batch discounts, long-context pricing and data residency.
- **What invalidates** (documented table): tool definitions invalidate everything; the web search toggle, citations toggle and `speed` setting invalidate system and messages; `tool_choice` and adding/removing images anywhere invalidate messages; thinking configuration and effort always invalidate messages and, on some models, tools and system (an effort change carried as a `role: "system"` message on models with per-message effort keeps the prefix); non-tool-result user content with thinking is model-specific (cache kept on models that keep prior thinking: Opus 4.5+, Sonnet 4.6+, Haiku 5.5, all Fable/Mythos); a dropped thinking block changes the prefix from its position on. Also: tool_use key order must be stable ("some languages (for example, Swift, Go) randomize key order during JSON conversion"). Mid-conversation `role: "system"` messages (Fable/Mythos 5.x, Opus 5.5, Opus 5, Opus 4.8, Sonnet 5.5, Haiku 5.5; not Sonnet 5) and the `inline-tools-2026-09-15` beta (`tool_addition` blocks in a system message) let instructions and tools change without touching the cached prefix.
- **Usage fields** (documented): `usage.cache_creation_input_tokens`, `usage.cache_read_input_tokens`, `usage.input_tokens` (only tokens after the last breakpoint), and `usage.cache_creation.{ephemeral_5m_input_tokens, ephemeral_1h_input_tokens}` whose sum equals `cache_creation_input_tokens`. Total input = read + creation + input. In streaming they arrive on `message_start`. Responses may also carry a per-iteration `usage.iterations` array (shown in the pre-warm example).
- **Pre-warming**: `max_tokens: 0` writes the cache at the breakpoints and returns an empty `content` with `stop_reason: "max_tokens"`; rejected with `stream: true`, manual extended thinking, structured outputs, forced `tool_choice`, and inside Message Batches. Use explicit breakpoints (automatic caching would key on the placeholder message) and the same thinking and effort settings as the real traffic.
- **Isolation**: caches are per organization, and per workspace on the Claude API, Claude Platform on AWS and Foundry. Exact matching is required. KV representations are held in memory only (ZDR eligible). There is no manual cache clear.
- **Thinking blocks** cannot carry `cache_control` but are cached as part of earlier assistant turns and count as input when read.
- **Cache diagnostics** (https://platform.claude.com/docs/en/build-with-claude/cache-diagnostics): send `diagnostics: {"previous_message_id": <prior response id or null>}` on every turn; the response's `diagnostics.cache_miss_reason.type` names the first divergence: `model_changed`, `system_changed`, `tools_changed`, `messages_changed` (each with `cache_missed_input_tokens`), `previous_message_not_found`, or `unavailable` (which also covers differences in `tool_choice`, `thinking`, `context_management`, `output_config` or the set of beta headers). The page lists the feature as stable on the Claude API and Google Cloud (Anthropic managed); its SDK examples use the `client.beta.messages` namespace, and the fetched page does not name a beta header (the bundled Claude API skill reference names `cache-diagnosis-2026-04-07`).

### 3.2 Context editing

URL: https://platform.claude.com/docs/en/build-with-claude/context-editing

- **Beta header**: `context-management-2025-06-27`. Request field `context_management: {"edits": [...]}`. Status beta on all platforms and all supported models. The page opens with a note that server-side compaction is "the primary strategy" for most long conversations; context editing is for finer control.
- **Server-side**: applied before the prompt reaches Claude; the client keeps and resends its full unmodified history.
- **`clear_tool_uses_20250919`** parameters (documented):

| Parameter | Default | Meaning |
|---|---|---|
| `trigger` | 100,000 input tokens | activate once the prompt exceeds it; `{"type": "input_tokens", "value": N}` or `{"type": "tool_uses", "value": N}` |
| `keep` | 3 tool uses | `{"type": "tool_uses", "value": N}` most recent pairs kept; oldest cleared first |
| `clear_at_least` | none | `{"type": "input_tokens", "value": N}`; if that much cannot be cleared, the strategy is not applied ("whether context clearing is worth breaking your prompt cache") |
| `exclude_tools` | none | tool names never cleared |
| `clear_tool_inputs` | `false` | also clear the tool call parameters |

  Cleared results are replaced with placeholder text telling Claude they were removed.
- **`clear_thinking_20251015`**: `keep` is `{"type": "thinking_turns", "value": N}` (N > 0) or `"all"`; default when the strategy is present is `{thinking_turns: 1}` on every model; when absent, the per-model default applies (keep all prior thinking on Opus 4.5+, Sonnet 4.6+, Haiku 5.5+, all Fable/Mythos; keep only the turn in progress on older models). N counts the turn in progress; a user message of only `tool_result` blocks continues a turn. When both strategies are used, `clear_thinking_20251015` must be listed first.
- **Caching interaction** (documented): tool result clearing invalidates the cached prefix at the clear point, so use `clear_at_least`; you pay a cache write each time content is cleared. Keeping thinking preserves the cache; clearing it invalidates at the clearing point.
- **Preserved thinking**: "server-side context management never invalidates thinking blocks" on preserved-thinking models; client-side edits to earlier turns can.
- **Response**: `context_management.applied_edits[]` with `{"type": "clear_tool_uses_20250919", "cleared_tool_uses": N, "cleared_input_tokens": N}` and `{"type": "clear_thinking_20251015", "cleared_thinking_turns": N, "cleared_input_tokens": N}`; in streaming it is in the final `message_delta` event.
- **Token counting** accepts `context_management` and returns `input_tokens` (after editing) plus `context_management.original_input_tokens`.
- **Memory tool**: with context editing enabled, "Claude receives an automatic warning" as context approaches the clearing threshold so it can save to memory first.
- Client-side SDK compaction (`compaction_control` in tool runners) is deprecated in TypeScript and Ruby and removed in Python 1.0; it miscounts usage with server tools because `cache_read_input_tokens` accumulates across internal calls (documented warning).

Memory tool (https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool): tool type `memory_20250818`, entirely client-side (your handler executes `view`, `create`, `str_replace`, `insert`, `delete`, `rename` against storage you control under a `/memories` prefix; path traversal protection is the implementer's job). No beta header for the tool itself.

Compaction, briefly (https://platform.claude.com/docs/en/build-with-claude/compaction and `.../compaction-threshold`): threshold compaction uses edit `compact_20260112` with beta `compact-2026-01-12`, default trigger 150,000 input tokens, minimum 50,000, optional `pause_after_compaction`; on-demand compaction uses beta `compact-2026-09-04` and a top-level `compaction` parameter and is recommended "wherever it is available". Kept turns keep their thinking only with on-demand compaction under stated conditions.

### 3.3 Token counting

URL: https://platform.claude.com/docs/en/build-with-claude/token-counting

- `POST /v1/messages/count_tokens` takes the same inputs as message creation and returns `{"input_tokens": N}`. Free, with separate RPM limits (Start 5,000, Build 10,000, Scale 20,000) (documented).
- The count is an estimate; it may include system-added tokens that are not billed. It does not use caching logic.
- Errors for server tools other than the advisor tool, the MCP connector, and `image`/`document` blocks with `url` or `file` sources (send base64 to count).
- Prior-turn thinking counts on keep-all models; current-turn thinking counts.
- Claude Opus 4.7 and later and all Fable/Mythos 5.x use a newer tokenizer: about 30% more tokens for the same text than earlier models (documented). Never reuse counts across tokenizer generations, and never use chars/4.

### 3.4 Thinking blocks across tool use, and preserved thinking

URLs: https://platform.claude.com/docs/en/build-with-claude/extended-thinking (manual mode), https://platform.claude.com/docs/en/build-with-claude/thinking (shared rules), https://platform.claude.com/docs/en/build-with-claude/preserved-thinking and `.../preserved-thinking/prefix-checks`, `.../preserved-thinking/change-tools-or-instructions`.

Rules for every thinking model (documented):

- A tool-use loop is one assistant turn. Within it, pass every `thinking` block back complete and unmodified alongside its `tool_use` ("Required"). Across turns, passing everything back is "Recommended"; outside tool use you may omit prior turns' thinking, "but not a block between two that you keep".
- Within the latest assistant message the consecutive thinking blocks must match the original generation exactly; modified thinking blocks get a 400. `redacted_thinking` blocks (encrypted `data` field) must be passed back too; filtering on `type == "thinking"` alone breaks the protocol.
- The `signature` field is opaque, carries the encrypted full thinking, arrives as a `signature_delta` before `content_block_stop` when streaming, and is compatible across platforms; blocks from Fable 5.2, Mythos 5.2, Sonnet 5.5 and Haiku 5.5 only work in the producing (or linked) account.
- Thinking cannot be toggled mid-turn; doing so silently disables thinking for that request. Changing thinking mode, `budget_tokens` or effort invalidates cache breakpoints.
- Manual mode (`type: "enabled"`, `budget_tokens`) is rejected on newer models; on preserved-thinking models `"enabled"` is a 400 and adaptive is the default.

Preserved thinking (prefix check), on Fable 5.2, Mythos 5.2, Fable 5.1, Mythos 5.1, Opus 5.5, Sonnet 5.5, Haiku 5.5 (documented):

- Each thinking block's signature records a fingerprint of its prefix (`system`, `tools`, all earlier messages). If a request changes anything before a block, that block fails the check. Result with no setting: **400** on Fable 5.2, Mythos 5.2, Mythos 5.1; "possible 400" on Fable 5.1, Opus 5.5, Sonnet 5.5, Haiku 5.5 (otherwise the stale reasoning silently reaches the model and is billed).
- Beta `thinking-binding-controls-2026-08-01` adds `thinking.block_binding.prefix_mismatch_behavior`: `"error"` or `"drop_block"`, and a response `input_transformations` array.
- What does not count as a change: `model`, `effort`, `max_tokens`, `tool_choice`, `metadata`, `thinking.display`; JSON formatting and key order (parsed JSON is compared); `cache_control` markers; unreferenced `defer_loading` tools; **server-side context editing and threshold compaction** ("change only what Claude reads").
- The three habits of an append-only integration: send each assistant turn back exactly as returned (including empty thinking blocks); build `system` and `tools` once per conversation; put every update in a new message at the end.
- Directly relevant to us: "Shorten or downscale a tool result or image before you first send it. If your code clears, shortens, or re-encodes something it already sent, the thinking after it becomes invalid. To clear old tool results later ... use context editing." And: if your own code summarizes or cuts older turns, remove the kept turns' thinking or send `"drop_block"`; "cut rarely, in large steps".
- For gateways and proxies (our engine is one): pass `anthropic-beta` and `thinking.block_binding` through, return empty thinking blocks and `input_transformations` to the caller, never move a `role: "system"` message into `system`, do not hide the 400.

## 4. OpenAI and Gemini, for comparison

### 4.1 OpenAI prompt caching

URL: https://developers.openai.com/api/docs/guides/prompt-caching (documented, as fetched):

- Caching is implicit on all models. Earlier models (before GPT-5.6) are implicit-only: breakpoints at model-dependent intervals (2,048-token spacing on GPT-5.5), no cache-write surcharge, `prompt_cache_retention` of `in_memory` (about 5 to 10 minutes inactive, up to an hour) or `24h`, minimum cacheable length "varies by request settings", and reported `cached_tokens` excludes hidden system tokens and rounds down to a multiple of 128. A stable `prompt_cache_key` helps routing (about 15 requests per minute per key).
- GPT-5.6 and later: minimum 1,024 visible tokens; implicit mode places a breakpoint at the end of the latest eligible message; explicit mode (`prompt_cache_options.mode: "explicit"`, `prompt_cache_breakpoint` on content blocks) allows up to four cache writes per request; writes cost 1.25x for the default 30-minute lifetime or 2.5x for a 12-hour lifetime (`prompt_cache_options.ttl`), reads 0.1x (0.05x on some GPT-6.x models). Lookup boundaries: the first 2 and latest 50 explicit breakpoints, the implicit breakpoint, and up to 20 earlier eligible message endings.
- Usage: `usage.input_tokens_details.cached_tokens` and `cache_write_tokens` (Responses API), with `cache_write_tokens_details.{ttl_30m_tokens, ttl_12h_tokens}` on GPT-5.6+.
- Caches live on individual machines; traffic above about 15 requests per minute per prefix can overflow to other machines.

### 4.2 Gemini context caching

URLs: https://ai.google.dev/gemini-api/docs/caching and https://ai.google.dev/gemini-api/docs/caching?generate-content=true (documented):

- Implicit caching is on by default for Gemini 2.5 and newer, with no guaranteed saving. Minimum tokens: 4,096 for the Gemini 4 models, 3.8 Flash, 3.6 Flash, 3.1 Pro Preview; 2,048 for 2.5 Flash and 2.5 Pro. On Gemini 4 multi-turn loops, the previous request is cached only if the new request adds at least 2,050 tokens; otherwise its last 2,050 tokens are reprocessed at full price.
- Explicit caching (`v1beta/cachedContents`, beta) exists for Gemini 3 and 2.5 through `generateContent` only, not for Gemini 4 or the Interactions API. Default TTL 1 hour, no minimum or maximum TTL; billed on cached token count at a reduced rate plus storage time. Implicit caching does not apply on requests that use an explicit cache.
- Cached tokens are reported in `usage_metadata` (generateContent) or `usage.total_cached_tokens` (Interactions API, Python/JS).

Cross-provider takeaway: all three now converge on prefix caching with at most about four writes per request, a lookback of about 20 positions, and cheap reads (0.025x to 0.1x). Anthropic is the only one whose reasoning blocks are cryptographically bound to the exact prior prefix, which makes "never rewrite what was sent" a correctness rule, not only a cost rule.

## 5. Cross-cutting weaknesses and failure modes

- **BM25 for code and logs.** Lexical ranking misses paraphrase ("connection refused" vs `ECONNREFUSED`), and log IDF is dominated by unique junk tokens (request ids, hex addresses, timestamps). Context Mode's replace path ranks 48-byte chunks: BM25 length normalization is nearly meaningless at that size, and a rare error not named by the intent or the last prompt is not selected (its shape digest exists to catch exactly that). Their retrieval eval is 50 queries over mostly documentation sources, built by the same team; it says little about logs or code.
- **Prompt-dependent previews.** Selection uses the last user prompt, so the same output gives different previews in different sessions. That is fine for caching (computed once, then frozen), but it must be stored, not recomputed, or a replay would change bytes.
- **Opaque previews cost turns.** A 96-byte preview forces the model to call search; the project's own A/B shows extra turns erasing token savings on some tasks (section 1.12). A preview has to carry enough to decide whether to fetch more.
- **Index size.** Text is stored in `chunks`, indexed again by the contentless trigram table (trigram postings are large; Context Mode leaves RFC #962 open), and copied again into `line_units` with up to 8x ancestor copies. Per SQLite docs, a trigram table with `detail=none` or `detail=column` is smaller but then full-text queries may not contain tokens longer than 3 characters (LIKE/GLOB still work); substrings under 3 characters never match; `case_sensitive 1` allows indexed GLOB but not LIKE (https://www.sqlite.org/fts5.html, section 4.3.4).
- **Staleness.** Neither system ties a stored read to later edits of that file. Context Mode keeps every copy searchable; CMV deletes the output and tells the model to re-read, which gives current content, not the content the conversation reasoned about.
- **Retention breaks losslessness.** Context Mode deletes after 30 days idle or past 500 MB; CMV keeps 5 rotating backups per session for auto-trim.
- **Retroactive rewrites.** CMV's in-place auto-trim and any client-side "compaction" of earlier turns invalidate the prompt cache from the edit point and, on preserved-thinking models, the thinking after it (section 3.4).
- **Hook latency** is on the critical path of every tool call; Context Mode misses its own 40 to 60 ms budgets at the p95 and at 1 MiB (section 1.12).
- **Security surface.** Context Mode's "sandbox" runs as the user; CMV's postinstall edits Claude Code settings on `npm install`.

## 6. Recommendations for our engine

### 6.1 Content-addressed artifact externalization

- Store every externalized output as a blob keyed by a cryptographic digest of its exact bytes (SHA-256 or BLAKE3). The handle shown to the model is a short, stable prefix of the digest with a type tag. Because the key is known before any database write, there is no need for Context Mode's "reserve the row synchronously, write chunks detached" dance (`dispatch.ts:534-539`): hash, emit the handle, index asynchronously.
- Dedup is automatic: re-running `git log` or re-reading an unchanged file yields the same handle, and search returns one copy.
- Keep a separate occurrence table (artifact digest, session, turn, tool, call id, path, mtime, size) so provenance and staleness are queryable without duplicating bytes.
- Staleness: when a later edit or write touches a path, mark earlier read-occurrences of that path as superseded. Superseded artifacts stay retrievable by handle but are demoted in search and annotated with the turn at which the file changed.
- Retention must never silently break a handle that appears in a live transcript: pin blobs referenced by any retained session; evict only unreferenced blobs; when a handle cannot be resolved, say so explicitly instead of returning an empty result.

### 6.2 Bounded previews

- Decide the preview at insertion time, before the result is first sent, and persist the exact rendered bytes with the occurrence. Never re-render or shrink a preview that was already sent (cache and preserved-thinking rule, section 3.4).
- Make previews informative rather than minimal: a header with exact byte and line counts, exit code, and the handle; the head and tail lines; a structural digest when one is found (section 6.3); and a retrieval hint. A budget around 1 to 2 KB is a starting point to evaluate against Context Mode's 96 B, whose own data shows extra turns.
- Keep previews deterministic given the artifact and a recorded query. If intent-aware selection is used (Context Mode style), record the query used alongside the preview.
- Pass through without externalization: outputs under a threshold, ranged reads the model asked for explicitly, skill or instruction loads, and Edit/Write payloads (CMV's corruption lesson).

### 6.3 Structured log extraction

- Adopt a Drain-style template miner for logs (Context Mode `shape.ts:224-271` is a compact reference design): mask tokens with digits (extend to hex, UUIDs, ISO timestamps, durations), cluster by length and first token, report template counts and rare lines.
- Add explicit severity and outcome extractors that do not depend on rarity: lines with error/warn/fatal/panic markers, stack frames, test failure blocks (go test, pytest, jest), compiler diagnostics in `file:line:col` form, and the exit code.
- For JSON arrays, Context Mode's skewed-key rule (dominant value at least 90%, minority at most 2 rows) and numeric extremes are cheap and deterministic.
- Keep indentation-ancestor context for line units (Context Mode `units.ts`): a matching YAML or JSON line returned with its parents is far more useful than the bare line.
- Store the masked template as its own FTS column so queries like "all occurrences of this message shape" work.

### 6.4 FTS5 schema and tokenizers for code and log content

- Keep raw bytes in the blob store and use **external-content or contentless FTS5 tables** keyed by chunk rowid so text is not stored twice inside SQLite. If contentless, keep the text needed for the FTS5 `'delete'` command available (Context Mode's contentless trigram table deletes by reinserting text from `chunks`, `fts.ts:93-95`), or use `contentless_delete=1` (SQLite 3.43+).
- Word channel: `unicode61 remove_diacritics 2` (it already splits on `_`, `.`, `/`, `-`, so `ctx_search` indexes as `ctx` and `search`). Wrap with `porter` for prose-like content (docs, commit messages, chat text); Context Mode measured a small gain from porter on documentation queries (recall@5 0.78 to 0.80). Whether porter helps or hurts on logs and code needs our own eval.
- Substring channel: a `trigram` table for identifiers, paths, hashes and partial words, with the 3-character minimum and index-size cost in mind. Measure `detail=column` or `detail=none` if we only need LIKE/GLOB there.
- Consider a third "exact symbol" channel (for example `unicode61` with `tokenchars` covering `_.:/-`) fused by RRF rather than replacing the word channel; Context Mode found `tokenchars '_-'` slightly worse as a replacement (0.78 to 0.76) on docs queries, which is not the same experiment.
- Unit size: index lines or records with ancestor context (roughly 200 to 800 bytes) rather than 48-byte fragments, and markdown by heading with the heading path as a separately weighted column.
- Escape every user query term as a quoted FTS5 string (Context Mode `toMatch`), and split on NUL (their D-SEARCH-6 crash).

### 6.5 Retrieval fusion

- Use RRF (k = 60, Cormack et al. default; Context Mode `constants.ts:119`) across independent rank lists: word BM25, trigram BM25, exact symbol, recency, and "same artifact or same session" locality. RRF needs no score normalization.
- Rank documents matching all terms before partial matches inside each tier (Context Mode's biggest single gain, recall@5 0.40 to 0.78).
- Typo correction from `fts5vocab` (Levenshtein at most 2 on stems of 5+ characters) only when a pass returns nothing, so it never perturbs good results.
- Deterministic tie-breaking (artifact id, chunk order) so repeated identical queries return identical bytes.
- Build our own held-out set dominated by logs, test output, stack traces, JSON API responses and code, with identifier, typo and paraphrase queries, and report recall@k and MRR per kind, as Context Mode does.

### 6.6 Cache-friendly assembler rules

1. Build `tools` and `system` once per conversation and serialize deterministically. Go's `encoding/json` sorts map keys, but any hand-rolled map iteration or struct reshaping must not reorder fields between turns.
2. Append only. Every new fact (retrieved snippets, recall blocks, date changes, mode changes) goes in a new message at the end; operator instructions go in mid-conversation `role: "system"` messages on models that accept them; tool changes go through `tool_addition` blocks under `inline-tools-2026-09-15`.
3. Externalize at first send; never edit a sent tool result. When old content must go, prefer server-side `clear_tool_uses_20250919` with `clear_at_least` sized so the invalidation pays back, or API compaction. If the engine itself must rewrite history, do it rarely and in large steps, drop or remove the kept turns' thinking (or send `"drop_block"` and surface `input_transformations`), and treat it as a new cold-cache epoch.
4. Breakpoints: one at the end of the stable `tools` + `system` prefix, one at the conversation tail (automatic caching or explicit), and an intermediate one when a turn can add 20 or more lookback positions (remember that runs of `tool_use` or `tool_result` blocks count as one position on the Claude API). Never exceed 4. Never put a breakpoint on a block that changes every request.
5. Respect per-model minimum cacheable lengths (512 to 4,096 tokens) and the 1h TTL trade-off (2x write) when gaps exceed 5 minutes, including long generations, since the lifetime runs from request start.
6. For fan-out, send the first request alone and wait until its response starts before sending siblings that share the prefix.
7. Decide rewrites with an append-only cost model fed by measured `usage` (section 2.5 item 3), not with an assumed hit rate.
8. Measure every request: log `cache_read_input_tokens`, `cache_creation_input_tokens`, `cache_creation.ephemeral_5m/1h_input_tokens`, `input_tokens`, and `context_management.applied_edits`; thread `diagnostics.previous_message_id` to attribute misses (`messages_changed` means the engine edited history).
9. Use `count_tokens` (free, RPM-limited, an estimate, aware of `context_management`) for budget decisions, with the model actually targeted; never chars/4.
10. Store assistant content verbatim, including empty `thinking` blocks and `redacted_thinking`; for a fork that changes history, start a new conversation without the old thinking (CMV's approach is valid there).

## 7. What I could not access or verify

- Context Mode run data referenced in comments or docs but not committed (for example the "0 of 50 clean T-CMP sessions" figure in `recall.ts`, raw A/B rows "stay private", and the `DECISIONS-r*` and `tickets/` files that live at an older commit URL). I did not fetch the vendor site (contextmode.ai) or its Gateway benchmark JSON files.
- No code from either repository was executed, so no number in sections 1 and 2 is reproduced by me.
- I did not verify how Claude Code reacts to CMV rewriting a live session JSONL, or whether it resends pre-compaction lines; section 2.5 item 2 is an inference from the boundary semantics in CMV's own trimmer.
- The Vertex AI context-cache page was fetched but not used; Gemini facts come from ai.google.dev only.
- The cache diagnostics page as fetched shows no beta header; the header name given comes from the bundled Claude API skill reference, not the page.
