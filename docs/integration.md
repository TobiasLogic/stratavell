# Integration guide

There are two ways to use Stratavell, and they control different things.

| mode | what the host does | what the engine manages |
|---|---|---|
| **Full context interception** | routes every model request through `Assemble` and reports every message back | the whole context: compaction, masking, budget, cache layout, task state, retrieval |
| **Passive memory (MCP)** | adds the MCP server to a host that keeps managing its own context | only what the model chooses to archive and retrieve; the host's context is not compacted or bounded by the engine |

Full interception is the only mode in which the engine enforces the token budget and keeps protected constraints in the context. Passive mode is useful for hosts you cannot change, such as a closed-source coding assistant: the model gets a lossless archive and exact retrieval, but compaction is still the host's.

## Full interception in one loop

Every integration, whatever the language, has the same five calls per model turn:

1. **append** the new user message, if any
2. **assemble** the request (optionally already rendered for Anthropic or OpenAI)
3. send it to the model, then **usage** with the provider's token counts
4. **append** the assistant message exactly as returned, including thinking blocks and signatures
5. run the tool calls; for tools named `memory_*` call the engine's **tool** endpoint; **append** each result as a `tool` message with the matching `tool_call_id`

Rules the engine enforces:

- Every tool call needs a result before the next assistant or user message. If the user interrupts, append a synthetic error result such as `"cancelled by user"` first.
- `Assemble` refuses (`409 pending_tool_calls`) while calls are open.
- Do not edit history yourself. Append only; the engine decides what the model sees.
- Pass the assistant message back as you received it. The engine keeps thinking blocks only when nothing before them has changed, and drops them (never edits them) otherwise.

### Go

```go
e, _ := engine.Open(ctx, engine.Options{Path: "memory.db", Summarizer: summarize.Extractive{}})
defer e.Close()
sess, _ := e.OpenSession(ctx, "task-42", engine.SessionConfig{})
agent, _ := sess.Agent(ctx, "main")

agent.Append(ctx, msg.Text(msg.RoleUser, "Fix the flaky test. Never edit vendor/."))
model := anthropic.NewClient("claude-sonnet-5-5")
turns, err := agentloop.Run(ctx, agent, model, myTools, engine.AssembleRequest{System: sys, Tools: toolDefs, IncludeMemoryTools: true}, 50)
```

`agentloop.Step` is about forty lines. Copy it if your framework already has its own loop.

Set `Meta` on tool results when you know it: `Command`, `ExitCode`, `Paths`, `ContentType`. The engine infers these from tool names and inputs, but explicit metadata makes failure tracking and file staleness exact.

### HTTP (any language)

```
stratavell serve --db memory.db --socket $XDG_RUNTIME_DIR/stratavell.sock
```

The same loop over HTTP:

```
POST /v1/sessions                                  {"id": "task-42"}
POST /v1/sessions/task-42/agents                   {"id": "main"}
POST /v1/sessions/task-42/agents/main/events       {"messages": [ ... ]}
POST /v1/sessions/task-42/agents/main/assemble     {"system": "...", "tools": [...], "include_memory_tools": true, "format": "anthropic", "model": "...", "max_tokens": 8192}
     -> {"request": <send this to the provider>, "tokens": ..., "report": {...}}
POST /v1/sessions/task-42/agents/main/usage        {"input_tokens": ..., "output_tokens": ..., "cache_read_input_tokens": ..., "cache_creation_input_tokens": ...}
POST /v1/sessions/task-42/agents/main/tools/memory_search   {"input": {"query": "..."}}
POST /v1/sessions/task-42/close                    {}   (when the run ends: cancels its pending summaries)
```

The complete contract is `docs/openapi.yaml`. Clients:

- `examples/python/stratavell_client.py`: standard library only, TCP or Unix socket
- `examples/typescript/stratavell.ts`: fetch, Node 22 or newer
- `examples/rust/src/main.rs`: `ureq` and `serde_json`

All three examples, and `examples/go`, were run against a live server.

Message JSON is provider-neutral:

```json
{"role": "assistant", "parts": [
  {"type": "thinking", "text": "...", "signature": "...", "provider": "anthropic"},
  {"type": "text", "text": "Running the tests."},
  {"type": "tool_call", "tool_call_id": "toolu_1", "tool_name": "bash", "input": {"command": "go test ./..."}}
]}
{"role": "tool", "meta": {"command": "go test ./...", "exit_code": 1},
 "parts": [{"type": "tool_result", "tool_call_id": "toolu_1", "tool_name": "bash", "text": "--- FAIL: ...", "is_error": true}]}
```

### Provider formats

`format: "anthropic"` returns a Messages API body:
- consecutive tool results are merged into one user message
- tail text goes after the `tool_result` blocks
- `cache_control` is set on up to four blocks, never on thinking blocks
- thinking, redacted thinking and unknown provider blocks are emitted only when the engine keeps them
- the request is validated for alternation, pairing and breakpoint count before it is returned

`format: "openai"` returns a Chat Completions body:
- system blocks become one system message
- tool calls go in `tool_calls`, results in `tool` messages
- thinking is never sent

### Subagents

```go
worker, _ := sess.Spawn(ctx, lead, "worker-1", engine.Handoff{Task: "Find why TestReplay fails", Constraints: []string{"read only"}, Refs: []string{evID}})
... run the worker's loop ...
text, _ := worker.Complete(ctx, engine.Completion{Summary: "...", Findings: []string{"..."}, Refs: []string{...}})
```

`text` is meant to be the tool result of the lead's spawn call. Over HTTP, create the agent with `parent` and `handoff`, and finish with `POST .../complete`.

### Custom components

| interface | used for | default |
|---|---|---|
| `engine.Summarizer` | semantic summaries | `summarize.Extractive` (no model) |
| `summarize.Completer` | the LLM behind `summarize.LLM` | Anthropic, OpenAI-compatible and Ollama clients in `adapters/` |
| `engine.Tokenizer` | token counts | a conservative local estimator, calibrated by usage reports |
| `engine.ArtifactStore` | where blob bytes live | inside the database; `engine.NewFileArtifactStore(dir)` is provided |
| `engine.Ranker` | fusing retrieval lists | reciprocal rank fusion with small priors |
| `engine.Trigger` | when background compaction starts | thresholds plus exhaustion prediction |
| `engine.Embedder` | optional semantic retrieval list | none |

## Passive memory with MCP

```
stratavell mcp --db ~/.local/share/stratavell/memory.db --session my-project --agent assistant
```

Register it as a stdio MCP server in the host. With Docker, the same server is `docker run -i --rm -v stratavell-data:/data -e STRATAVELL_SESSION=my-project ghcr.io/tobiaslogic/stratavell`. The image's default command is `mcp --db /data/memory.db`, and the named volume keeps the archive between runs. To serve HTTP from the container instead, pass a token, since the server refuses a non-loopback address without one: `docker run -p 127.0.0.1:7077:7077 -e STRATAVELL_TOKEN=... ghcr.io/tobiaslogic/stratavell serve --addr 0.0.0.0:7077 --db /data/memory.db`.

Tools exposed:

- `memory_archive`: store notes, findings or large outputs
- `memory_search`
- `memory_query`: select records, fields or matching lines from one archived JSON, table or text output
- `memory_expand`
- `memory_get_event`
- `memory_get_artifact`
- `memory_get_task_state`
- `memory_get_summary_lineage`
- `memory_stats`
- `memory_update_task`

The server negotiates MCP protocol versions 2025-11-25, 2025-06-18, 2025-03-26 and 2024-11-05.

In passive mode nothing is compacted. Only archived content is searchable, and the host still decides what stays in its own context. If the host also offers hooks, posting every message to the HTTP API (`.../events`) gives a complete journal and lossless retrieval, but compaction still needs the host to use `assemble`.

## Fully local operation

No step needs a network service, an API key or an embedding model:
- the default summarizer is extractive
- retrieval is lexical
- the tokenizer is an estimator

`--summarizer ollama --summarizer-model qwen2.5-coder:7b` uses a local Ollama model for summaries through Ollama's native API with `num_ctx` set to 16384. Avoid pointing `--summarizer openai` at Ollama's OpenAI-compatible endpoint: it does not let the client set `num_ctx` and silently truncates long prompts to the server default.
