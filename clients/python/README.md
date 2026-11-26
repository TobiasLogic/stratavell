# stratavell (Python client)

A small, dependency-free Python client for the [Stratavell](https://github.com/TobiasLogic/stratavell) HTTP API. Stratavell is a context management engine for long-running LLM agents, with asynchronous compaction, losslessly retrievable history, cache-aware context assembly and persistent task state.

This package is only the client. It talks to a running `stratavell serve`, over TCP or a Unix socket.

## Install

```bash
pip install stratavell
```

Start the engine with a [release binary](https://github.com/TobiasLogic/stratavell/releases), the Docker image, or `go install github.com/TobiasLogic/stratavell/cmd/stratavell@latest`:

```bash
stratavell serve --db memory.db --socket /tmp/stratavell.sock
```

## Use

```python
from stratavell import Client, text, tool_call, tool_result

client = Client(socket_path="/tmp/stratavell.sock")
agent = client.session("task-42").agent("main")

agent.append(
    text("user", "Find why the build fails. Do not touch the generated/ directory."),
    tool_call("c1", "bash", {"command": "make build"}),
    tool_result("c1", "bash", "main.c:12:5: error: implicit declaration of function 'parse_cfg'", exit_code=2, command="make build"),
)

request = agent.assemble(system="You are a coding agent.", fmt="anthropic", model="claude-sonnet-5-5", max_tokens=4096)
print(request["tokens"], "tokens")

for hit in agent.search("parse_cfg"):
    print(hit["id"], hit["snippet"])
```

Each turn follows the same loop:
1. Send `request["request"]` to your provider.
2. Report the provider's token counts with `agent.usage(...)`.
3. Append the assistant's reply and the tool results.
4. Execute `memory_*` tool calls with `agent.tool(name, arguments)`.

Use `Client(base_url="http://127.0.0.1:7077")` for TCP. Set `STRATAVELL_TOKEN`, or pass `token=`, when the server requires a bearer token.

The full loop is in the [integration guide](https://github.com/TobiasLogic/stratavell/blob/main/docs/integration.md). The HTTP API is specified in [openapi.yaml](https://github.com/TobiasLogic/stratavell/blob/main/docs/openapi.yaml).

## License

Apache-2.0
