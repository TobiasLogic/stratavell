# stratavell (TypeScript client)

A small, dependency-free TypeScript client for the [Stratavell](https://github.com/TobiasLogic/stratavell) HTTP API. Stratavell is a context management engine for long-running LLM agents, with asynchronous compaction, losslessly retrievable history, cache-aware context assembly and persistent task state.

This package is only the client. It talks to a running `stratavell serve` over HTTP, using the global `fetch` (Node 18 or newer, Deno, Bun, or browsers).

## Install

```bash
npm install stratavell
```

Start the engine with a [release binary](https://github.com/TobiasLogic/stratavell/releases), the Docker image, or `go install github.com/TobiasLogic/stratavell/cmd/stratavell@latest`:

```bash
stratavell serve --db memory.db
```

## Use

```ts
import { Client, text } from "stratavell";

const agent = await new Client("http://127.0.0.1:7077").agent("task-42", "main");

await agent.append(text("user", "Upgrade the HTTP client. Keep Node 18 support; do not add dependencies."));

const req = await agent.assemble({ system: "You are a coding agent.", format: "anthropic", model: "claude-sonnet-5-5", max_tokens: 4096 });
console.log(req.tokens, "tokens");

const { hits } = await agent.search("Node 18");
for (const h of hits) console.log(h.id, h.snippet);
```

Each turn follows the same loop:
1. Send `req.request` to your provider.
2. Report the provider's token counts with `agent.usage(...)`.
3. Append the assistant's reply and the tool results.
4. Execute `memory_*` tool calls with `agent.tool(name, input)`.

Pass a token as the second constructor argument, or set `STRATAVELL_TOKEN` in Node, when the server requires a bearer token.

The full loop is in the [integration guide](https://github.com/TobiasLogic/stratavell/blob/main/docs/integration.md). The HTTP API is specified in [openapi.yaml](https://github.com/TobiasLogic/stratavell/blob/main/docs/openapi.yaml).

## License

Apache-2.0
