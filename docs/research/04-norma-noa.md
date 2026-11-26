# 04. Norma and its "noa" compaction engine

Research note for the asynchronous, losslessly retrievable context engine. Scope: read the Norma source critically, document its agent loop and both of its context managers (the conventional `compaction` package and the model-driven `noa` engine), list defects, and specify how to reimplement each as a baseline.

## 0. Source and method

- Repository: https://github.com/Autumn-27/norma (exists at the given URL). Cloned with `git clone --depth 1` into `/tmp/claude-1000/research-norma/norma-src` on 2026-11-26. HEAD: `d79faab3c2ae4dd27484f7f9934449edd3196bf5` ("Merge pull request #11 from adeljck/adaptive_think", 2026-11-24). GitHub reports 254 stars, created 2026-07-20, license Apache-2.0.
- Nothing in the clone was built, run, installed or tested. Every behavioral claim below comes from reading code. Where a claim is an inference that a test run would settle, it is marked "inferred".
- All file references are repo-relative with line numbers at that commit (for example `noa/compress.go:77-218`).
- Upstream lineage of noa was checked with read-only GitHub API calls (`ranxianglei/acp-kernel` LICENSE and PROVENANCE.md); no upstream code was fetched or executed.

## 1. What Norma is

- **Language and size.** Go module `github.com/Autumn-27/norma` (`go.mod:1`). The `go` directive is `1.26.3` (`go.mod:3`) although the README says "Go 1.23+" (`README.md`, "Module path" line). 223 files, 218 of them `.go`; about 39.6k Go lines, of which about 21.5k are non-test and 18.2k are test. Dependencies: `creack/pty`, `hinshun/vt10x`, `golang.org/x/net`, `gopkg.in/yaml.v3` (`go.mod`).
- **Purpose.** An "Agent Harness SDK": a streaming tool-use loop with Anthropic and OpenAI wire formats, a fail-closed tool system (Read, Write, Edit, MultiEdit, LS, Glob, Grep, Bash, plus opt-in WebFetch, WebSearch, TodoWrite, AskUserQuestion, background tasks, deferred tools), a permission pipeline, hooks, subagents, MCP, plan mode, memory, skills, coordinator mode and JSONL transcripts (`README.md`, "Packages" table). It is heavily modeled on Claude Code: many constants and prompts are annotated "== claude-code ..." (for example `compaction/compaction.go:67-72`, `compaction/summarizer.go:11-17`, `llm/todomsg.go:1-17`).
- **License.** Apache-2.0, "Copyright 2026 Autumn-27" (`LICENSE`, appendix). No NOTICE file. The noa package declares itself "Ported from acp-kernel (MIT, Copyright (c) 2026 ranxianglei)" (`noa/types.go:13-15`), and the compaction prompts are declared verbatim copies of Claude Code text (`compaction/summarizer.go:219-240`). See section 10.
- **Package sizes (non-test / test lines).** `noa` 5,392 / 4,458; `noaadapter` 1,924 / 4,673; `compaction` 766 / 1,514; `harness` 1,407 / 1,544; `llm` 2,608 / 1,935.
- **Docs drift.** The README lists "Snip / MicroCompact / Context Collapse / AutoCompact" for `compaction`, but the package says Snip and Context Collapse are "intentionally absent" (`compaction/compaction.go:14-16`). The README does not mention noa at all. Both specs the code cites, `docs/COMPACTION-SPEC-3.md` (`compaction/compaction.go:2`) and `docs/noa-实施方案.md` (`noa/types.go:14`, `noaadapter/identity.go:5`), are excluded by `.gitignore` (`docs/`) and are not in the repo.

## 2. The agent loop

### 2.1 Message representation

- One vendor-neutral, Anthropic-shaped model: `llm.Message{Role, Content []ContentBlock}` with roles `user` and `assistant` only (`llm/types.go:13-18`, `llm/types.go:97-100`).
- `ContentBlock` is a union keyed by `Type`: `text`, `thinking` (with `Signature`), `tool_use` (`ID`, `Name`, `Input json.RawMessage`, Gemini `ThoughtSignature`), `tool_result` (`ToolUseID`, nested `Content []ContentBlock`, `IsError`) (`llm/types.go:23-81`). A fifth block type, `compact_boundary`, is used only as a position marker (`llm/boundary.go:12`).
- Tool results travel inside a user message; there is no `tool` role in the host model. The OpenAI adapter splits them into `role:"tool"` messages at the wire (`llm/openai.go:146-160`).
- `Usage` carries input, output, cache-read and cache-write counts (`llm/types.go:130-135`). The Anthropic adapter normalizes `InputTokens` to the total prompt size, `input_tokens + cache_read + cache_creation` (`llm/anthropic.go:325-336`); streaming usage is merged with max-of-echo semantics (`llm/types.go:151-162`, `llm/accumulate.go:72-76`).
- Synthetic, self-identifying user messages: skill invocations wrapped in `<system-reminder><invoked-skill name=...>` (`llm/skillmsg.go:13-47`), todo reminders (`llm/todomsg.go:15-36`), and a per-call date reminder that is prepended to every request but never stored (`agentcore/agentcore.go:415-427`, `harness/query.go:784-786`).

### 2.2 How a turn executes

`harness.Query` returns an `iter.Seq2[Event, error]`; the loop body is `loop.run` (`harness/query.go:368-582`). Per iteration:

1. If a compactor is set, `Pre(ctx, messages, lastInputTokens)` may rewrite history; any new boundary is persisted (`harness/query.go:406-410`, `harness/query.go:309-326`).
2. `streamAndExecute` (`harness/query.go:781-874`): maybe append a todo reminder (`harness/query.go:706-725`); build the request array via `requestMessages`, which calls `ContextView.View` when the compactor implements it and `llm.MessagesForAPI` otherwise (`harness/query.go:774-779`); prepend the date reminder; send with `MaxTokens` defaulting to 32,768 (`harness/query.go:358-366`).
3. Tool calls start executing while the response is still streaming: each finished `tool_use` is handed to the executor as soon as the next non-argument event arrives (`harness/query.go:804-857`).
4. On a stream error: if `IsOverflow(err)` and no reactive attempt has been made for this failure, call `Reactive` and retry; otherwise terminate with `prompt_too_long` or `model_error` (`harness/query.go:417-436`).
5. Append the assistant message; set `lastInputTokens = turnInput + turnOutput` (`harness/query.go:438-443`).
6. `max_tokens` with no tool calls: optionally escalate to 64,000 output tokens, then up to 3 "resume directly" nudges (`harness/query.go:632-651`).
7. No tool calls: Stop hooks, then background-task notifications, then token-budget continuation, else `completed` (`harness/query.go:465-506`).
8. Tool calls: append one user message holding all results, then any `Result.Extra` messages (skills), then task notifications; check `MaxTurns` / `MaxDuration` at the turn boundary, with an optional "settlement" wrap-up phase (`harness/query.go:509-581`).

### 2.3 Tool call / result pairing

- Results are returned in issue order, one per `tool_use`, with synthetic `"tool execution interrupted"` error results for anything unfinished (`harness/streaming.go:188-229`). Unknown tools, schema failures, permission denials and hook blocks all produce an error `tool_result` rather than a missing one (`harness/tools.go:28-120`).
- Every tool output passes a global cap (`tool.CaptureOnce`, default 30,000 chars, `tool/helpers.go:13`) at one choke point (`harness/tools.go:102-108`).
- On resume, trailing assistant messages with unanswered `tool_use` are dropped (`agentcore/agentcore.go:398-411`).
- Request-time repairs: `llm.PairToolBlocks` drops either half of a pair whose partner is missing (`llm/boundary.go:102-147`); the OpenAI adapter repeats this at the wire and inserts a placeholder `reasoning_content` when a thinking-mode tool call lost its reasoning (`llm/openai.go:166-236`); the Anthropic adapter strips thinking when thinking is off and merges adjacent same-role messages (`llm/anthropic.go:338-433`).
- Malformed tool arguments: in streaming mode both the dispatcher and the accumulator replace unbalanced or invalid JSON with `"{}"` (`harness/query.go:809-813`, `llm/accumulate.go:106-126`). This matters for noa (section 7.1, item 2).

### 2.4 Concurrency model

- A `Session` is not safe for concurrent `Prompt` calls (`agentcore/agentcore.go:243-245`). The loop itself runs on the caller's goroutine.
- Tools run on goroutines bounded by `MaxConcurrency` (default 10, `harness/query.go:205-208`) with FIFO plus exclusivity: concurrency-safe tools run together, a non-safe tool needs exclusive access and blocks everything queued behind it (`harness/streaming.go:10-15`, `harness/streaming.go:64-94`). Tools default to not-safe (`tool/tool.go:145-157`).
- Context management is synchronous and in-band: `Pre`, the summarizer call, `View` and the Compress tool all run on the loop's critical path. There is no background worker anywhere in either context manager.

### 2.5 Subagents and coordinator

- The `Agent` tool runs a fresh `harness.Query` with the definition's system prompt, a filtered tool registry (all parent tools when `Tools` is empty), a seed user message, a depth guard (default 3) and an optional sidechain transcript; only the final text returns to the parent (`subagent/subagent.go:52-158`, `subagent/subagent.go:161-179`).
- The child `QueryInput` sets no `Compactor` (`subagent/subagent.go:124-135`): subagents have no context management of either kind.
- Coordinator mode is either LLM-driven (an Agent tool plus a system segment) or programmatic fan-out with `RunParallel`, bounded at 4 (`coordinator/coordinator.go:79-130`).

## 3. Context-management integration points

- `harness.Compactor`: `Pre`, `Reactive`, `IsOverflow` (`harness/query.go:133-143`).
- `harness.ContextView` (optional): `View(ctx, msgs) []llm.Message`, documented as pure and never written back (`harness/query.go:145-156`). This is the hook noa uses to own request construction.
- `harness.Recorder`: `RecordMessage` for births in order, `RecordBoundary` for observability (`harness/query.go:158-167`).
- `agentcore.Options.Compaction` builds the built-in compactor with the session's provider as summarizer; `Options.Compactor` replaces it, and the two are mutually exclusive (`agentcore/agentcore.go:271-283`).

## 4. Conventional compaction (`compaction` package)

### 4.1 Mechanisms

Three mechanisms, described as aligned 1:1 with Claude Code (`compaction/compaction.go:1-20`): MicroCompact, AutoCompact, Reactive. It is non-destructive in the sense that AutoCompact keeps all prior messages in the array and inserts a boundary; only messages after the last boundary are sent (`llm/boundary.go:74-100`).

### 4.2 Configuration and thresholds

`Config{ContextWindow, MaxOutputTokens, KeepRecent}` with defaults 200,000 / 8,000 / 8 (`compaction/compaction.go:36-57`).

| Quantity | Formula | Default (200k window) | Source |
|---|---|---|---|
| Summary reservation R | `min(MaxOutputTokens, 20000)`, 0 means 20000 | 8,000 | `compaction/compaction.go:92-98` |
| Effective window E | `ContextWindow - R` | 192,000 | `compaction/compaction.go:99` |
| Buffer B | 13,000 if E < 400k; 30,000 if E < 800k; else 50,000 | 13,000 | `compaction/compaction.go:102-111` |
| Auto threshold | `E - B` | 179,000 | `compaction/compaction.go:112` |
| Micro threshold | `E * 6 / 10` | 115,200 | `compaction/compaction.go:113` |
| Max turn growth G | `R + 15000` | 23,000 | `compaction/compaction.go:115-116` |
| Predictive trigger | `tok + G > E` | tok > 169,000 | `compaction/compaction.go:117-119` |

With a 13,000 buffer the predictive check always fires first (it triggers at `E - R - 15000`, below `E - 13000`), so in practice AutoCompact starts at about 169k tokens for a 200k window (confirmed by the table test `compaction/threshold_test.go:99-118`).

### 4.3 Token counting

`count` returns `lastInputTokens` when positive (the previous response's total input plus its output, `harness/query.go:442`), otherwise `EstimateTokens(MessagesForAPI(msgs)) * 4 / 3`, where `EstimateTokens` is total characters / 4 over text, thinking, tool name plus input, and nested tool-result text (`compaction/compaction.go:121-167`). No tokenizer. System prompt and tool schemas are not in the estimate.

### 4.4 Algorithm (`Pre`, `compaction/compaction.go:173-195`)

1. If `tok <= microThreshold`, return.
2. MicroCompact (`compaction/compaction.go:244-291`): collect `tool_use` ids of the compactable set {Read, Bash, Grep, Glob, WebFetch, WebSearch, Edit, Write} (`compaction/compaction.go:62-65`) in history order; keep the last 5 (`microKeepRecent`); replace the content of every other matching `tool_result` with `"[Old tool result content cleared]"`. This edits the history array the loop keeps, not just the request. If anything changed, re-estimate locally (provider figure discarded).
3. If `tok >= autoThreshold` or predictive, AutoCompact with trigger `"auto"`.

AutoCompact (`compaction/compaction.go:295-339`):

1. Skip if no summarizer or the circuit breaker tripped (3 consecutive failures, `compaction/compaction.go:71`, `compaction/compaction.go:307-315`).
2. `start = lastBoundary + 1`; `tailStart = max(index of the 8th-from-last non-marker message, start)` (`compaction/compaction.go:227-242`). The tail is a message count, not a token budget, and is not aligned to tool groups.
3. Summarize `msgs[start:tailStart]` minus markers. This range includes the previous summary and re-injected skills, so summaries are recursive.
4. Re-inject skills: latest invocation per skill name found before `tailStart`, each clipped to 20,000 chars, newest first while the total stays within 25,000 tokens, then restored to chronological order (`compaction/compaction.go:346-421`).
5. Splice: `msgs[:tailStart] + boundaryMarker + summaryMessage + skills + msgs[tailStart:]`. The marker is a user message whose only block is `compact_boundary` carrying `{trigger, pre_tokens, messages_summarized, active_skills}` (`llm/boundary.go:14-38`). The summary message is a user text message: a fixed "session is being continued" preamble plus the digest (`compaction/compaction.go:328-344`).

Summarizer (`compaction/summarizer.go:24-102`): replays the real head messages (dangling tool blocks dropped by `pairForReplay`, `compaction/summarizer.go:104-143`) followed by one user message containing a no-tools preamble, a nine-section summary template, and a no-tools trailer (`compaction/summarizer.go:219-335`). System prompt is a one-line summarizer role (`compaction/summarizer.go:22`); thinking is forced off; no tools are sent; max output is `min(20000, host MaxTokens)` (`compaction/summarizer.go:30-34`). Two attempts on transient errors (`compaction/summarizer.go:71-101`). If the summary request itself overflows, drop `floor(20%)` (at least 1, keeping at least 1) of the oldest API-round groups (a group starts at each assistant message), prepend a retry marker when the slice would start with an assistant message, and retry up to 3 times (`compaction/summarizer.go:145-199`). Output post-processing strips `<analysis>`, unwraps `<summary>` into `"Summary:\n"`, and collapses blank lines (`compaction/summarizer.go:201-217`).

Reactive (`compaction/compaction.go:197-206`): one forced AutoCompact with trigger `"reactive"`; success means the local estimate shrank, not that the view fits. The harness allows one reactive attempt per failure (`harness/query.go:423-433`). `IsOverflow` is substring matching on "too long", "context length", "context_length", "maximum context", "status 413" (`compaction/compaction.go:211-220`).

### 4.5 Persistence and caching

- When a boundary appears, the harness writes a `boundary` record and then re-appends the marker, summary, skills and the entire kept tail to the transcript (`harness/query.go:301-326`). A resumed session therefore contains the tail twice; only the post-boundary copy is sent.
- MicroCompact edits are not recorded, so the transcript keeps the full tool output (`harness/query.go:86-91`, `transcript/transcript.go:8-18`).
- Caching: the summarizer request uses its own system prompt and no tools, so it shares no cache prefix with the main conversation. The Anthropic adapter only ever places `cache_control` on the system prompt (`llm/anthropic.go:87-107`); conversation messages never get a breakpoint on that wire.

## 5. noa: model-driven compression

### 5.1 What it is

noa is "a model-driven context-compression engine: a pure core with no host dependency, no I/O, and no model calls" (`noa/types.go:1-16`). The model itself writes the summaries, as arguments to a `Compress` tool; noa decides when to ask, validates and applies the request, archives the originals to disk, and projects a pruned view each turn. It is a Go port of `acp-kernel` (TypeScript, MIT plus an attribution term), which itself descends from the OpenCode "DCP" plugin lineage according to acp-kernel's PROVENANCE.md (section 10). `noaadapter` binds the pure core to Norma.

`noaadapter.Enable` is the only switch and attaches three things together (`noaadapter/noa.go:9-50`): the `Compactor` (which implements `ContextView`), the `Compress` tool, and a resident system-prompt section. It also overwrites `DynamicBoundary` so the whole system prompt, including the noa section, sits in the cacheable prefix (`noaadapter/noa.go:46-48`). Default window when the host gives none: 200,000 (`noaadapter/noa.go:52-55`). It is not used by the CLI or by any example.

### 5.2 Projection, identity and refs

- **Flattening.** Each host content block becomes one `CoreMessage{ID, Role, ContentType, Text, ToolName, ToolCallID}` (`noa/types.go:45-52`): user text, assistant text, reasoning, tool-call (text = raw JSON arguments), tool-result (role `tool`, text = joined text blocks, tool name recovered from its call) (`noaadapter/project.go:84-129`). If the history carries a built-in `compact_boundary`, only messages after the last one are projected (`noaadapter/project.go:38-48`). A sidecar keeps thinking signatures, origins and error flags for reassembly (`noaadapter/project.go:18-36`).
- **Identity.** `id = "h_" + hex(sha256(role|type|toolCallID|toolName|text))[:16]`, computed after stripping the trailing ref tag; identical bodies get `_1`, `_2` suffixes by occurrence order within one projection pass (`noaadapter/identity.go:25-56`). Identity is therefore content-addressed and depends on history being append-only.
- **Refs.** Every core receives a ref `m00001..m99999` the first time it is seen, never reassigned (`noa/refs.go:72-113`, `noa/const.go:6-8`). Hard-protected cores (tool name `Compress`, or a configured `ProtectedTools` pattern, exact or trailing `*`, case-insensitive, or a host predicate) are recorded as `BLOCKED` and get no number (`noa/protected.go:13-44`, `noa/const.go:51-54`). Summary and nudge messages are skipped (`noa/processturn.go:4-26`).
- **Tags.** On reassembly, user text and tool-result bodies with a ref get a trailing `\n<noa-ref id="mNNNNN" tokens="X" [src="Tool"]/>`; assistant content is never tagged (`noaadapter/project.go:221-279`, `noaadapter/tags.go:36-119`). The `tokens` figure is frozen at first render in `TokenSnapshot` so the bytes never change (`noaadapter/tags.go:96-107`).

### 5.3 Per-turn pipeline (`noa.ProcessTurn`)

Fixed node order (`noa/processturn.go:77-104`):

1. **assign-refs** (above).
2. **sync-blocks**: a block absorbed by a higher tier becomes inactive; any other block is active iff one of its covered messages (or its summary id) is still present; dead `TokenSnapshot` entries are pruned (`noa/sync.go:9-79`).
3. **prune** (`noa/prune.go:64-175`): `covered` = union of active blocks' effective ids. Repairs first un-cover the reasoning of any turn with a visible act (`noa/prune.go:255-288`) and both halves of a half-covered Compress pair (`noa/prune.go:290-327`). Each active block is anchored at its earliest covered index in the view. Output walks the view, emitting each anchored summary before its anchor index, always keeping the first user message (even when covered), and dropping covered cores. Then strip orphaned tool calls (Compress exempt), orphaned tool results, and reasoning runs not immediately followed by an assistant act (`noa/prune.go:169-253`). The summary message is a user text message: `[Compressed conversation section]`, optional topic after a U+2014 separator, the model's summary, a blank line, and `<noa-archive block="bN" tier="T" range="mA-mB" path="ABS"/>` (`noa/prune.go:23-62`).
4. **hide-compress-calls**: drop Compress call/result pairs whose block is inactive or that never created a block, except the 2 most recent block-less calls; stub each `summary` inside the remaining calls' arguments to 199 runes plus an ellipsis (`noa/hideconsumed.go:8-169`, `noa/const.go:32-37`).
5. **emergency-truncate** (only when the turn's token count is at least `Truncate.Threshold * limit`): see 5.6.
6. **nudge-inject**: build the recommendation, decide, stamp pacing, return a decision (`noa/nudge.go:273-295`).

The adapter's `Session.View` runs this, appends the rendered nudge as a final user core with id `noa_nudge` when allowed, stores the result as `lastView` (the exact array the model's refs will be resolved against), and reassembles host messages (`noaadapter/session.go:171-222`). The view is never written back to history.

### 5.4 Triggers and thresholds (nudging)

Token count for the turn (`noaadapter/session.go:309-351`): the estimate is the size of the pruned view (`noa/processturn.go:106-136`); the provider-reported size from the previous response is applied as a raise-only floor, except when it predates the most recent compression.

Derived quantities (`noa/nudge.go:11-22`, `noa/config.go:89-119`, `noa/config.go:166-171`), with defaults for a 200k window:

| Quantity | Formula | Default |
|---|---|---|
| Adaptive growth unit A | `min(GrowthCap, max(GrowthFloor, round(L*0.05)))` | 50,000 (floor = cap = 50,000, so the ratio never matters) |
| Cadence floor F | `max(20000, int(0.45*A))` | 22,500 |
| Min pressure benefit | `max(5000, round(L*0.01))` | 5,000 |
| Tier-2/3 pending threshold | `round(A*1.5)` | 75,000 |
| Pressure band | usage >= 0.75 | 150,000 tokens |
| Emergency mark | usage >= 0.95 | 190,000 tokens |
| First-sight / count-trigger usage floor | usage >= 0.45 | 90,000 tokens |

Pending amounts (`noa/nudge.go:216-242`): T1 = tokens of recommended ranges whose chars >= 5,000; T2 = summary tokens of active tier-1 blocks (with their count); T3 = the same for tier-2 blocks.

Decision (`noa/nudge.go:56-202`):

- **Pressure band** (usage >= 0.75): pick the tier with the most pending (ties go to the lower tier); inject iff that pending >= min benefit. No growth gate.
- **Below pressure**: require `growth >= F` since the last shown nudge (or since the baseline), or "first sight" (no stamps yet, usage >= 0.45, some pending >= A). Then, in order: tier 1 if T1 >= A; else tier 2 if (at least 5 active tier-1 blocks and usage >= 0.45) or (T2 >= 75,000 and T2 > T1), subject to tier-2 cadence; else tier 3 with the analogous rule (10 active tier-2 blocks).
- **Stamping** (`noa/nudge.go:300-321`): if the count fell more than A below the baseline, re-anchor; initialize the baseline on first sight; on injection record the count globally and per tier. A successful compression zeroes all stamps (`noa/compress.go:209-214`).
- **Adapter failure ladder** (`noaadapter/session.go:224-307`, `noaadapter/session.go:419-426`): a failed or empty Compress call, a parse failure, a dead-range refusal, or a nudge followed by an assistant turn with no Compress call each count as one failure. After 3 (`MaxCompressAttempts`), nudges are suppressed until the count grows by F (22,500) or a "real user turn" arrives, which resets the counter.

The nudge text (`noa/nudgetext.go:147-272`) comes in three voices: gentle (efficiency note, philosophy, breakdown, when to and when not to compress, tool usage, how-to rules with an archive addendum, a merged range table, an 8-entry block map), emergency (used for usage >= 0.75 as well as >= 0.95), and tier (tier-2 or tier-3 rules plus target blocks). Only three sections are resident in the system prompt: tag semantics, how to read summaries (historical, not live instructions), and how to use the archive (`noa/prompts_system.go:7-124`).

### 5.5 What is protected and what is summarized

- **Hard-protected, never compressed:** Compress calls and results, configured `ProtectedTools` and the other half of their exchanges (`noa/protected.go:56-79`, `noa/compress.go:370-378`). User text can never be hard-protected; the check only looks at tool names.
- **Always kept verbatim in the view:** the first user message, even if a block claims it (`noa/prune.go:97-103`, `noa/prune.go:151-156`).
- **Soft-protected zone** (`noa/recommend.go:8-72`): the last 5 visible messages, plus visible messages from the end until about 5,000 tokens accumulate, plus the most recent user message. Read, Grep, Glob, Bash and LS outputs do not occupy slots (`noa/const.go:56-62`). Requests that hit the zone are trimmed with a warning, or rejected if nothing is left (`noa/compress.go:380-401`).
- **Summarized:** whatever ranges the model chooses, by inclusive ref span, subject to the gates in 5.7. The recommendation shown to the model groups contiguous compressible messages, breaks at gaps and at a user message once a group holds 3 messages, merges neighbors until 5,000 chars, and drops groups under 200 tokens (`noa/recommend.go:92-277`).
- Skill invocation messages and todo reminders get no special treatment: they are ordinary user text and are compressible (no reference to them anywhere in `noa/` or `noaadapter/`). The conventional compactor, by contrast, re-injects skills (`compaction/compaction.go:317-321`).

### 5.6 Mechanical last resort: emergency truncation

`noa/truncate.go:35-131`: at `count >= 0.95 * limit`, aim for `0.9 * threshold`; candidates are tool results outside the last 5 cores, not already truncated, of at least 1,000 tokens and more than 4,000 runes; largest first, each replaced by its first 2,000 runes, a marker beginning `[truncated for context space` with the original token estimate, and its last 2,000 runes. It applies only to the sent view, so it lifts by itself once usage drops.

### 5.7 The Compress tool and `ApplyCompression`

Tool wiring (`noaadapter/tool_compress.go:15-92`): `RawInput: true` (skips schema validation so the lenient parser runs), not read-only, not concurrency-safe, never prompts for permission. Schema: `{topic?, content: [{topic?, startId, endId, summary}], summaryMaxChars?}` where `content` deliberately has no `type` so stringified arrays pass (`noa/schema.go:10-64`). The description tells the model not to call it unprompted (`noa/schema.go:3-12`).

Parsing (`noa/parseinput.go:65-441`): accepts a JSON string wrapper, markdown fences, trailing commas, raw newlines in strings, a stringified `content`, a missing final brace, and brace-matching salvage of truncated arrays; field aliases `startRef` / `messageId` and `endRef`.

Before applying, a range set that has already failed twice (order-independent signature) is refused (`noaadapter/deadrange.go:29-81`, `noa/const.go:41-43`).

`ApplyCompression` (`noa/compress.go:67-218`), always against `lastView`:

- **A. Resolve** each endpoint (`noa/boundaries.go:155-254`). `mNNNNN` maps to a raw id and then to its index in the view; if absent but absorbed into an active higher block, snap to that owner's anchor with a warning; otherwise "consumed". `bN` maps to an active block's anchor; an inactive absorbed block snaps to its owner. Reversed endpoints are swapped. A range is block-kind if either endpoint is a block id.
- **B. In-batch overlap**: sort by start; skip a range that starts at or before the furthest accepted end.
- **C. Atomic size gate**: total runes of message-kind ranges must be at least 5,000 unless the batch contains a block-kind range; otherwise reject the whole batch with a diagnostic tailored to unknown, consumed or too-small refs (`noa/compress.go:220-280`).
- **D. Build each block in memory** (`noa/compress.go:308-492`): widen message ranges so reasoning runs stay with their acts and tool calls stay with their results (contiguous neighbors only, at most 20 positions from the original endpoints, Compress excluded, at most 2 passes; `noa/toolpairs.go:12-102`); decide the tier (`noa/tier.go:20-84`); compute effective coverage (range ids plus absorbed blocks' coverage) and direct coverage (effective minus what was already covered); remove hard-protected and soft-protected ids; withdraw any thinking turn that would leave a visible tool call without its reasoning (`noa/turnintegrity.go:75-115`); reject "no new content", terminal rewrites, and invalid summaries (trimmed length 50 to 20,000 runes, or up to `summaryMaxChars`; `noa/compress.go:494-518`); compute `CompressedTokens`, the ref span of the actual coverage, the next block id from one global counter (`noa/state.go:34-42`), and the archive markdown.
- **E. Persist** every archive first, atomically; if any write fails, remove those written and create nothing (`noa/compress.go:176-193`).
- **F. Commit**: append blocks, deactivate absorbed ones, update stats, zero the nudge stamps.

The tool returns a panel: a headline with before, after and reclaimed tokens, one line per block `bN(Tt)=mA-mB[*] -> path` (`*` when the span contains refs the block did not take), and notes, warnings and errors (`noa/panel.go:38-136`). On success the adapter resets the failure counter and dead-range tracker and saves `state.json` (`noaadapter/session.go:365-398`).

### 5.8 Hierarchy (tiers)

Tiers 1 to 3, fixed at block creation (`noa/types.go:54-56`). A message-only range yields tier 1. A range with a block endpoint consumes only the blocks at the lowest tier present among the active blocks anchored inside it and produces `min(MaxTier, lowest + 1)`; higher-tier blocks inside the range survive and are reported (`noa/tier.go:20-69`). Tier 3 is terminal: re-summarizing tier-3 blocks with no new messages is rejected (`noa/tier.go:71-84`). Loose raw messages between block endpoints are absorbed into the higher-tier block (`noa/nudgetext.go:97-99`, `noa/compress.go:520-548`). Tier-2 and tier-3 nudges carry their own size targets (50 to 150 tokens per source block for tier 2, 30 to 60 for tier 3; `noa/prompts_rules.go:166-242`).

### 5.9 Persistence, rebuild, materialize, inherit

- **Archive files**: `<ArchiveBaseDir>/<SessionID>/tier<T>/<bN>_<startRef>-<endRef>.md`, YAML-like front matter (block, tier, session, created, range, counts, original tokens, topic), the summary as a blockquote, then entries in view order; tier 2 and 3 archives list absorbed blocks with relative and absolute paths to their own archives (`noa/archive.go:54-209`). Headings are partly Chinese (`noa/archive.go:97-99`, `noa/archive.go:140-143`). Writes are temp file plus rename (`noaadapter/archive.go:21-60`). `ArchiveBaseDir` is mandatory (`noaadapter/session.go:39-40`).
- **State**: `state.json` next to the archives, versioned (v1), written atomically, saved only after a successful compression (`noaadapter/state_store.go:13-180`, `noaadapter/session.go:388`). A corrupt file falls back to an empty state with a warning (`noaadapter/session.go:131-137`).
- **Rebuild**: because the summaries are tool-call arguments in the transcript, `RebuildStateFromLog` replays every Compress call whose recorded panel shows at least one block, against the projection of history up to that call, adopting existing archives (`noa/rebuild.go:45-90`, `noaadapter/materialize.go:55-93`, `noaadapter/archive.go:73-99`).
- **Materialize**: applies the block ledger without tags so a session can continue with noa switched off (`noaadapter/materialize.go:10-53`).
- **Inherit**: a session without blocks can copy a parent session's ledger (blocks, refs, snapshot, next id; nudge stamps and stats reset), walking at most 8 ancestors (`noaadapter/inherit.go:9-84`). Not wired into the subagent package.

### 5.10 Token counting

`DefaultCountTokens` = one per CJK rune (U+4E00-U+9FFF, U+3040-U+30FF, U+AC00-U+D7AF) plus `ceil(other runes / 4)` (`noa/tokenize.go:9-53`). Only core text is counted; tags, the nudge, system prompt and tool schemas are not (the provider floor covers them after the first response). `estimateMessages` (bytes / 4) is used for overflow comparisons and the "sent" size (`noaadapter/compactor.go:77-91`).

### 5.11 Synchronous or asynchronous

Fully synchronous and in-band. The engine runs inside `View` before each request and inside the Compress tool during the model's turn. Compression costs the main model an extra turn of output tokens (the summaries) and happens only when the model complies. Nothing runs in the background.

### 5.12 Overflow recovery

`Compactor.Reactive` (`noaadapter/compactor.go:44-75`) cannot involve the model, so it arms a ceiling of `0.95 * configured limit` (`noaadapter/overflow.go:23-47`), rebuilds the view with `ModelContextLimit` set to that ceiling (so emergency truncation fires lower, `noaadapter/session.go:188-193`), and returns success only if the rebuilt view is smaller than the last sent one and fits the ceiling.

### 5.13 Interaction with prompt caching

Design choices aimed at a stable byte prefix: frozen ref tags (`noaadapter/tags.go:78-107`), refs never renumbered, summaries anchored at the earliest covered position with stable ordering (`noa/prune.go:105-142`), nudges appended only at the tail and never stored (`noaadapter/session.go:205-217`), and the resident prompt placed before the cache boundary (`noaadapter/noa.go:46-48`). A compression necessarily invalidates the prefix from its earliest covered message. Emergency truncation toggles bytes mid-prefix on and off. On the Anthropic wire the effort buys little, because only the system prompt carries `cache_control` (`llm/anthropic.go:87-107`); it pays off on providers with automatic prefix caching (OpenAI-compatible endpoints). The daily date reminder prepended to every request (`harness/query.go:784-786`) changes the first message once per day.

## 6. Other infrastructure

- **Provider adapters**: Anthropic Messages (`llm/anthropic.go`), OpenAI Chat Completions (`llm/openai.go`), OpenAI Responses (`llm/openai_responses.go`), selected by `Config.Format`; keys and base URLs default from `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`, `OPENAI_API_KEY`, `OPENAI_BASE_URL` (`llm/provider.go:185-227`). Retries on connection setup (default 3, exponential backoff capped at 8 s), empty-response retries (default 2, OpenAI only), an optional shared rate limiter, proxy support, thinking type and reasoning effort switches (`llm/provider.go:72-170`). Gemini thought signatures are carried through the OpenAI path and stripped on the Anthropic path (`llm/types.go:49-75`).
- **Token counting implementations**: three heuristics (compaction chars/4 times 4/3; noa CJK-aware runes/4; noaadapter bytes/4) plus provider usage. No real tokenizer anywhere.
- **Session persistence**: append-only JSONL, one file per session plus sidechain files per subagent (`transcript/transcript.go:1-249`). Each append opens, writes and closes the file, with no fsync (`transcript/transcript.go:69-99`). A single unparsable line makes `Load` fail for the whole session (`transcript/transcript.go:120-123`). `Session.Resume` rebuilds messages and usage (`agentcore/agentcore.go:379-396`).
- **Configuration**: `agentcore.Options` (`agentcore/agentcore.go:26-219`), `compaction.Config` (three knobs), `noa.Config` via `DefaultConfig` with `ValidateConfig` warnings (`noa/config.go:63-171`), `noaadapter.Options` (`noaadapter/session.go:14-37`). Env switches `AGENT_CORE_DISABLE_BACKGROUND_TASKS`, `AGENT_CORE_TASK_MEM_CAP` (`agentcore/agentcore.go:321-332`).
- **Tests**: 715 `Test` functions, 5 fuzz targets (all in `noa/fuzz_test.go`: Compress argument parsing, boundary parsing, truncation, ref assignment, stub summary), no benchmarks. Providers are exercised over `httptest` SSE; the loop with scripted providers; noa with a simulated agent (`noaadapter/longsession_test.go`, `noaadapter/chaos_test.go`). One test is written to fail until a defect is fixed: `TestOverflowLearningIsWiredIn` asserts behavior the code does not have (`noaadapter/overflow_test.go:84-122`); by reading, it should fail on this commit (not run). There is no quality evaluation of summaries against a real model.

## 7. Weaknesses and bugs

### 7.1 noa

1. **Archives are not lossless under emergency truncation (high).** `ApplyCompression` reads message text from `lastView` (`noaadapter/session.go:215-217`, `noaadapter/session.go:370-380`), and `lastView` is produced after the emergency-truncate node (`noa/processturn.go:87-89`). The archive is rendered from those texts (`noa/compress.go:520-548`), so a range compressed while truncation is active archives the truncated body, despite "Content is written verbatim, no truncation, ever" (`noa/archive.go:54-59`). The emergency nudge explicitly tells the model to compress instead of re-running tools (`noa/nudgetext.go:125-138`), which is exactly this case. The archive test probes characters 100 to 200 of each body (`noaadapter/resilience_test.go:282-288`), inside the 2,000-rune head that truncation keeps, so it cannot catch this.
2. **Lenient parsing is mostly unreachable, and repaired calls cannot be rebuilt (high).** In streaming mode the harness replaces unbalanced arguments with `"{}"` before dispatch (`harness/query.go:809-813`), so the truncated-array salvage never sees its input. The accumulator stores `"{}"` for any argument string that is not strictly valid JSON (`llm/accumulate.go:115-118`), so a call with trailing commas or raw newlines reaches the tool intact but is recorded as `"{}"`. `RebuildStateFromLog` then parses no ranges and skips it (`noa/rebuild.go:66-69`): state rebuilt from the transcript silently loses those blocks while their archives exist. The tool-gate test calls `ParseCompressArgs` and the schema gate directly and does not cover the streaming path (`noaadapter/toolgate_test.go:59-88`).
3. **Overflow learning is dead code, and the ceiling never lifts (medium).** `ParseOverflowWindow` and `overflowState.learn` have no callers (`noaadapter/overflow.go:49-78`); `IsOverflow` discards the error (`noaadapter/compactor.go:37-42`). The ceiling is always 95% of the configured limit, and after a successful reactive recovery `emergencyFloor` is never reset (only the failure branch clears it, `noaadapter/compactor.go:64-74`), so every later view in the session runs against a lowered limit.
4. **No upper bound on summary size (medium).** `summaryMaxChars` is model-controlled and accepted if positive (`noa/compress.go:506-509`, `noa/parseinput.go:429-441`). Nothing checks that a summary is smaller than the content it replaces, so a "compression" can grow the context.
5. **Compression depends on model compliance (structural).** Below the pressure band nudges are advisory; an uncooperative model leaves only emergency truncation, which cannot reduce many medium-sized messages (the repo's own test name `TestTruncateCannotHelpWithManyMediumMessages`, `noaadapter/longsession_test.go`). The final outcome is a `prompt_too_long` termination.
6. **User constraints have weak protection (medium).** Only the first user message is guaranteed verbatim. Later user messages, including ones that add constraints, are compressible at the model's discretion, guarded only by prompt rules; the last user message is protected only by the sliding zone. Skill instructions are not re-injected (section 5.5).
7. **"Real user turn" detection is imprecise (low).** Any user text message without a tool result resets the failure ladder (`noaadapter/session.go:276-290`). The harness injects many of those: todo reminders right before each request (`harness/query.go:782`), task notifications, max-token and budget nudges, Stop-hook messages, settlement prompts.
8. **Reactive's measuring `View` has side effects (low, inferred).** `Reactive` calls `View` to measure (`noaadapter/compactor.go:62`), which runs `notePriorTurnOutcome`, re-stamps nudges and replaces `lastView`; the retry then calls `View` again. If the measuring call injected a nudge, the second call can count a spurious ignored nudge (`noaadapter/session.go:259-274`).
9. **Suppressed nudges are stamped as shown (low).** `stampNudge` runs in the pure pipeline whether or not the adapter's ladder allows injection (`noa/nudge.go:280-295`, `noaadapter/session.go:207-214`), delaying the next growth-band nudge.
10. **Possible double coverage after widening (low, inferred).** The overlap check uses pre-widening indices (`noa/compress.go:109-133`) while widening happens later per range (`noa/compress.go:318-336`), and `preExisting` excludes blocks built earlier in the same batch (`noa/compress.go:142-143`). Two adjacent ranges can then both claim a widened tool result. No test covers it.
11. **Bounded ref space and O(history) per turn (medium for long sessions).** Refs stop at 99,999 cores (one core per content block); after that new messages are unaddressable (`noa/refs.go:103-106`). The ref maps grow with every core ever seen, are deep-copied by several pipeline nodes per turn (`noa/state.go:93-115`), and the whole `state.json` is rewritten on each compression. Every turn re-hashes the full history (`noaadapter/project.go:64-80`). `findMessage` is a linear scan inside loops (`noa/compress.go:299-306`).
12. **Durability (low).** Archive and state writes use rename without fsync (`noaadapter/archive.go:21-60`, `noaadapter/state_store.go:147-180`): atomic against crashes, not durable against power loss. State is saved only on successful compression; refs assigned since then are re-derived deterministically on resume.
13. **Subagents (medium, wiring risk).** Children have no compactor (`subagent/subagent.go:124-135`). With an empty `Tools` list they inherit the parent registry (`subagent/subagent.go:165-167`); if the host built that registry after `Enable`, the child sees a Compress tool bound to the parent's session and `lastView`.
14. **Prompt and code disagree (low).** The resident prompt says tier-2/3 archives "do not contain raw messages" (`noa/prompts_system.go:86-88`), but loose messages are archived in higher-tier archives (`noa/compress.go:520-548`, `noa/archive.go:109-115`).
15. **Estimate gaps (low).** On the first turn after resume there is no provider floor, so the estimate omits system prompt, tool schemas and tags (`noaadapter/session.go:327-341`).

### 7.2 Conventional compaction

1. **MicroCompact is destructive in memory and cache-hostile (medium).** It overwrites retained history (`compaction/compaction.go:267-286`, assigned back at `harness/query.go:408`); only the transcript keeps originals. Above 60% of the effective window it runs before every call, so each new compactable result clears the next-oldest one and changes bytes mid-prefix on most turns.
2. **Tail cut ignores tool groups (medium).** `KeepRecent` counts messages (`compaction/compaction.go:227-242`). When the cut separates a `tool_use` from its result, the summarizer drops the call (`compaction/summarizer.go:104-143`) and the API view drops the result (`llm/boundary.go:102-147`): that exchange disappears from the model's view without being summarized.
3. **Gate undercounts (medium).** `count` prefers the previous response's usage (`compaction/compaction.go:126-131`), which excludes the tool results appended since that response; only the fixed 15,000-token predictive margin compensates.
4. **Inconsistent reservation (low).** The reservation defaults to 8,000 (`compaction/compaction.go:47-57`) while the summarizer may emit up to 20,000 (`compaction/summarizer.go:30-34`, wired with `opts.MaxTokens`, default 0, at `agentcore/agentcore.go:282`).
5. **Lossy overflow retry (medium).** When the summary request overflows, the oldest 20% of groups are dropped from the summarizer input (`compaction/summarizer.go:145-174`), yet the boundary still advances past them (`compaction/compaction.go:333-338`): their content is in neither summary nor view.
6. **Recursive summarization, no hierarchy (structural).** Each AutoCompact re-summarizes the previous summary together with new content; detail decays with every pass and only the transcript holds originals, with no retrieval path for the model.
7. **Duplicated tail on resume (low, inferred).** The transcript re-appends the kept tail at every boundary (`harness/query.go:321-323`). After resume, MicroCompact keys on `tool_use` ids in encounter order (`compaction/compaction.go:251-265`), so a tool id in the recent window whose earlier duplicate is older lands in the clear set and both copies are cleared.
8. **Reactive success criterion (low).** Any decrease in the estimate counts as success, even if the view still does not fit (`compaction/compaction.go:200-206`).
9. **Summarizer request shape (unverified risk).** It replays `tool_use` / `tool_result` blocks with no tool definitions (`compaction/summarizer.go:64-69`). Whether every provider accepts that was not verified; it also forfeits any cache sharing with the main conversation.

### 7.3 General

- The Anthropic adapter never caches conversation messages (`llm/anthropic.go:87-107`).
- The transcript has no fsync, and one bad line blocks resume (`transcript/transcript.go:69-127`).
- Docs drift: README Go version and compaction mechanisms (section 1); specs excluded from the repo.

## 8. Reimplementing noa compaction as a baseline

Behavioral spec at reproduction-level detail. Defaults in brackets. Suggested deviations for our baseline (to fix the defects above without changing the algorithm's character) are marked "Baseline fix"; leave them off for a faithful replica.

**Data model.**

1. Flatten history into cores, one per content block: user text, assistant text, reasoning, tool call (text = raw argument JSON, plus tool name and call id), tool result (role tool, text = joined text parts, tool name looked up from its call). Ignore other block kinds. If the history contains an older compaction boundary, project only after it.
2. Core id = first 16 hex chars of SHA-256 over `role|type|callID|toolName|text` (tags stripped), with occurrence suffixes `_1`, `_2`, ... for duplicates within one pass. Any stable per-message id works if the host has one.
3. State: blocks `{id bN, tier, topic, summary, directIDs, effectiveIDs, absorbedBlockIDs, archivePath, compressedTokens, startRef, endRef, createdAt, active, compressCallID}`; ref map `rawID <-> mNNNNN` [5 digits, 1..99,999] with BLOCKED entries; frozen token snapshot per ref; nudge stamps `{baseline, lastShown, lastShownByTier}`; stats; next block id [1].

**Per-request projection** (pure; never written back):

4. Assign refs in order to cores without one, skipping synthetic messages. Hard-protected cores (the compress tool itself and configured protected tool patterns) get BLOCKED.
5. Sync: blocks absorbed by another block become inactive; any other block is active iff at least one covered id is present.
6. Prune: covered = union of active blocks' effective ids. Un-cover the reasoning of any turn (reasoning run + consecutive assistant acts + their immediately following results) that keeps a visible act; un-cover both halves of a half-covered compress exchange. Anchor each active block at its earliest covered index (sort by anchor, then block number). Emit summaries before their anchors, always keep the first user message, drop covered cores. Then remove tool calls without results (except compress calls), results without calls, and reasoning runs not followed by an assistant act. A summary message is a user message: header line with optional topic, summary body, blank line, archive pointer tag `{block, tier, range, path}`.
7. Hide compress calls: drop compress call+result pairs whose block is inactive or that never made a block, except the 2 most recent [KeepLastOrphaned 2]; stub `summary` fields in surviving calls to 200 runes [SummaryStubChars].
8. Size: estimate = sum over cores of (CJK runes + ceil(other runes / 4)). Count = max(estimate, previous response's total input tokens) unless that figure predates the last compression.
9. Emergency truncation if count >= 0.95 x L: target 0.9 x threshold; tool results outside the last 5 cores, >= 1,000 tokens and > 4,000 runes, largest first, keep the first and last 2,000 runes around a marker that states the original size; stop when under target. View only. Baseline fix: never let a truncated text reach the archiver (archive from the stored history, not the sent view).
10. Recommendation: soft zone = last 5 visible cores [PreserveRecentMessages] + cores from the end until 5,000 tokens [PreserveRecentTokens] + last user core; Read/Grep/Glob/Bash/LS outputs do not use slots. Scan addressable cores: covered or summary -> gap; hard-protected -> gap; soft-protected -> gap; else compressible. Group contiguous compressible cores, also breaking at a user core when the group has >= 3 members; merge neighbors until >= 5,000 chars [MinCompressRange]; drop groups < 200 tokens.
11. Nudge decision with A = 50,000 [min(cap 50,000, max(floor 50,000, round(0.05 L)))], F = max(20,000, 0.45 A) = 22,500, min benefit = max(5,000, round(0.01 L)), T1/T2/T3 pending as in 5.4:
    - usage >= 0.75: inject the tier with the largest pending if it is >= min benefit.
    - else require growth since last shown (or baseline) >= F, or first sight (no stamps, usage >= 0.45, max pending >= A); then T1 if pending >= A; else T2 if (>= 5 active tier-1 blocks and usage >= 0.45) or (T2 pending >= 1.5 A and > T1 pending), with per-tier cadence F; else T3 analogously (>= 10 active tier-2 blocks).
    - stamp: re-anchor the baseline if count < baseline - A; initialize the baseline once; on injection record count globally and per tier.
12. Failure ladder: a failed or empty compress call, a parse failure, a dead-range refusal, or an injected nudge followed by an assistant turn without a compress call each count as a failure; after 3 [MaxCompressAttempts] stop injecting until count grows by F, or a real user message arrives (reset). Baseline fix: only real user input resets.
13. Render the nudge (gentle below 0.75, emergency at or above it, tier-specific for T2/T3) with: our own wording of the rules (section 10), a compressible/protected range table (ref span, message count, tokens, tool/text share, user-message count), up to 8 active block spans, and the tool call shape. Append it as the last user message of this request only.
14. Tag user-text and tool-result bodies with a trailing ref tag including the frozen token figure. Do not tag assistant content.

**Compress tool** (model-called, exclusive, no permission prompt):

15. Parse leniently (fences, string-wrapped JSON, trailing commas, raw newlines, stringified `content`, missing closing brace, brace-matching salvage). Baseline fix: keep the raw argument string in history even when it is not valid JSON, so the call can be replayed.
16. Refuse a range set (order-independent) that has failed >= 2 times since the last success [DeadRepeatReject 2].
17. Resolve each endpoint against the exact view the model saw (store it from step 13). Message ref -> index; absorbed -> owner block anchor; else error "consumed" or "unknown". Block id -> active block anchor; inactive absorbed -> owner. Swap reversed endpoints; block-kind if either endpoint is a block.
18. Skip ranges that start within an earlier accepted range (sorted by start). Baseline fix: re-check overlap after widening.
19. Reject the whole batch if the rune total of message-kind ranges is < 5,000 and the batch has no block-kind range.
20. Per range: widen message ranges for reasoning pairs and contiguous tool pairs (<= 20 positions from the original endpoints, compress calls excluded, up to 2 passes); tier: message range -> 1; block range -> consume only the lowest-tier active blocks anchored inside, output min(3, lowest + 1); effective = range ids plus consumed coverage; direct = effective minus already covered; drop hard-protected ids and their pair halves, then soft-zone ids (reject if nothing remains and nothing is consumed); withdraw split thinking turns; reject message ranges with no new content; reject tier-3-into-tier-3 with no direct ids; validate summary length 50..20,000 runes (raisable per call). Baseline fix: cap the raise and require summary tokens < compressed tokens.
21. Allocate `bN` from one global counter; ref span from actual coverage; compressed tokens = direct tokens + consumed summaries' tokens; render the archive in view order (tier-1: raw messages; higher tiers: absorbed summaries with links plus loose messages).
22. Write all archives atomically before touching state; on any failure delete the ones written and change nothing. Then commit: add blocks, deactivate consumed blocks, update stats, zero nudge stamps, persist state, reset failure ladder and dead-range tracker.
23. Return a panel with before/after tokens and one line per block (id, tier, actual span, archive path), plus warnings and errors that tell the model what to change.

**Recovery and lifecycle.**

24. Provider overflow: rebuild the view with the limit set to 0.95 x configured and accept only if it shrank and fits; one attempt per failure. Baseline fix: parse the real window from the error and clear the ceiling after success.
25. Rebuild from history: replay every compress call whose recorded result reported at least one block, against the projection of history up to that call, adopting existing archives.
26. System prompt resident text: explain tags, that summaries are historical records and not live instructions, and how to read or grep the archive.

## 9. Reimplementing Norma's conventional compaction as a baseline

Defaults: window W = 200,000, `MaxOutputTokens` = 8,000, `KeepRecent` = 8.

1. R = min(MaxOutputTokens, 20,000), with 0 meaning 20,000 [8,000]. E = W - R [192,000]. B = 13,000 if E < 400,000; 30,000 if E < 800,000; else 50,000. Auto = E - B [179,000]. Micro = 0.6 E [115,200]. G = R + 15,000 [23,000].
2. tok = previous response's total input (including cache read and write) plus its output when available; otherwise (characters / 4 over the API view) x 4/3. API view = messages after the last boundary, markers removed, unpaired tool blocks dropped.
3. Before every model call: if tok <= Micro, stop. Otherwise MicroCompact: over the whole history, take `tool_use` ids for Read, Bash, Grep, Glob, WebFetch, WebSearch, Edit, Write in order; keep the last 5; replace the contents of all other matching results with a fixed cleared placeholder; if changed, re-estimate locally.
4. If tok >= Auto or tok + G > E [tok > 169,000], AutoCompact:
   - if no summarizer, or 3 consecutive failures have tripped the breaker, stop;
   - start = last boundary + 1; tailStart = max(index of the 8th-from-last non-marker message, start); head = messages in [start, tailStart) minus markers; empty head -> stop;
   - summarizer request: head with unpaired tool blocks dropped, then one user message with our own summary instructions (no tools; sections for intent, concepts, files and code, errors, problem solving, all user messages, pending tasks, current work, next step; an analysis scratchpad followed by a summary block); a short summarizer system prompt; thinking off; no tools; max output min(20,000, host output cap); 2 attempts on transient errors;
   - on overflow of the summary request: group head by API round (new group at each assistant message), drop floor(20%) of groups (at least 1, keep at least 1), prepend a retry marker if the result starts with an assistant message, retry up to 3 times;
   - post-process: remove the analysis block, unwrap the summary block under a "Summary:" header, collapse blank lines; failure increments the breaker, success resets it;
   - skills: for each skill name, the latest invocation before tailStart; clip each to 20,000 chars; take newest first while total <= 25,000 tokens; restore chronological order;
   - splice: history[:tailStart] + boundary marker {trigger, preTokens, messagesSummarized, activeSkills} + user summary message (continuation preamble + summary) + skills + history[tailStart:].
5. Reactive: on a provider overflow, run one AutoCompact with trigger "reactive"; retry if the estimate shrank, else terminate with prompt-too-long.
6. Persistence: append every new message; on a new boundary also append the boundary record and re-append marker, summary, skills and tail so a cold resume sees the compacted view.

For a fair baseline, keep these behaviors including the known weaknesses (message-count tail, in-place MicroCompact, recursive summaries); otherwise the comparison measures our fixes rather than Norma's design.

## 10. License implications

- **Norma (Apache-2.0).** Obligations (license copy, NOTICE, change notices) attach to copies and derivative works of the code. Reimplementing the algorithms from this note in our own code and words copies no protected expression; algorithms, thresholds and data-structure ideas are not copyrightable. Apache-2.0's patent grant only covers people who use the licensed work, so a clean reimplementation gets no grant; the risk of a patent on these mechanics is low but nonzero. Crediting Norma as the source of the design in our docs costs nothing and is good practice.
- **noa's upstream.** noa says it is ported from acp-kernel under MIT (`noa/types.go:13-15`) and that the four rule prompts are copied verbatim (`noa/prompts_rules.go:3-30`). The acp-kernel LICENSE (read via the GitHub API) is MIT plus an "Additional Term, Attribution on Product Surfaces": any product that "uses or incorporates the Software" must show visible attribution with links on its home page, documentation or credits page. Norma ships neither the MIT notice nor that term (its repo has only the Apache LICENSE), which looks like a compliance gap on Norma's side. acp-kernel's PROVENANCE.md says it descends from `opencode-acp`, a fork of the AGPL-licensed DCP plugin, and that DCP-derived files were reimplemented "in fresh expression"; it also lists `prompts/*` among the DCP-derived files. From what was read here, the provenance of the rule text Norma copied (`src/compression-rules.ts`) is not established.
- **Claude Code text inside Norma's conventional compaction.** The summary prompt, no-tools preamble and trailer are marked verbatim copies of Claude Code's `prompt.ts` (`compaction/summarizer.go:219-240`); many constants are marked "== claude-code". Claude Code is distributed under Anthropic's proprietary terms, not an open-source license, and Norma's Apache license cannot relicense that text.
- **What we should do.** (1) Write all code ourselves from this specification; do not copy Norma files. (2) Write our own prompt text for nudges, rules, resident instructions and the summarizer; do not copy noa's rule prompts (MIT plus attribution term, with possible AGPL lineage) or the Claude Code prompts. (3) Reusing numeric parameters (thresholds, ratios, caps) is fine; they are facts about a configuration, not expression. (4) Pick our own literal markers and tag names instead of `[Compressed conversation section]`, `<noa-ref>` and `<noa-archive>`; short functional strings are unlikely to be protected, but this avoids any question. (5) If we ever decide to vendor any acp-kernel-derived text or code, comply with MIT plus the attribution term, and first audit for DCP lineage.

## 11. Takeaways for our engine

- noa's strongest ideas: immutable, content-addressed message ids with stable refs that the model can name; a pure projection that never mutates history; non-destructive blocks with on-disk originals and a pointer in every summary; tiered consolidation that absorbs one tier at a time; atomic batch validation with corrective errors; and rebuildability from the transcript because summaries are recorded tool arguments.
- Its weak points are the ones our design targets: everything is synchronous and in-band (the main model pays a turn to compress, and only if it complies); retrieval is "read a markdown file", not an indexed lossless store; the archive is fed from the already-truncated view; and user constraints rely on prompt discipline. An asynchronous engine can compute candidate summaries off the critical path, keep originals in a content-addressed store keyed by the same ids, and apply blocks at turn boundaries with the same pairing and turn-integrity rules described in sections 5.3 and 5.7.
