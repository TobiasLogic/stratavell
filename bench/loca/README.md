# LOCA-bench with Stratavell

Runs [LOCA-bench](https://github.com/hkust-nlp/LOCA-bench) (MIT license) with Stratavell as the agent's context manager. The protocol is `bench/review/protocol-agent-loca-v1.md`.

## What the patch changes

`loca.patch` applies to LOCA-bench commit 8b6fac4. It changes three things.

**1. A Stratavell mode in the agent loop** (`inference/run_react.py` and `inference/stratavell_ctx.py`). It is off unless `STRATAVELL_URL` is set.
- Every message is appended to a Stratavell session.
- With `STRATAVELL_MODE` unset:
  - each model request is Stratavell's assembled context plus its memory tools
  - memory-tool calls are answered by Stratavell, and every other tool call by the environment, unchanged
- With `STRATAVELL_MODE=passive`:
  - LOCA manages its own context as usual
  - Stratavell archives every message and answers four retrieval tools: `memory_search`, `memory_get_event`, `memory_expand` and `memory_get_artifact`

**2. Tool cap.** Memory-tool calls count toward `--max-tool-uses`, and every strategy's run ends when the cap is reached. Unpatched, the environment stopped executing tools at the cap, but the loop kept sending requests with unanswered tool calls.

**3. MCP server launch** (`gem/tools/mcp_server/config_loader.py`). With `LOCA_UV_AS_PYTHON` and `LOCA_XVENV` set, servers start from preinstalled environments instead of `uv run` and `uvx`.
- Unpatched, inside the sandbox, `uv run` tried to build a new environment, and three servers never started.
- LOCA then ran the task without their tools and reported no error.

## Setup

Needs `git`, `uv`, `bwrap` (bubblewrap) and Node.js on Linux.

```
export LOCA_ROOT=/path/to/an/empty/directory
bench/loca/setup.sh
```

This clones LOCA-bench at 8b6fac4, applies the patch and builds three virtual environments:
- the main one, from `requirements-lock.txt`
- one for each of the two `uvx` tools

The lock files were resolved as of the upstream commit date, 2026-04-29. Unpinned, current `mcp` and `fastmcp` releases break LOCA's servers.

## Running

1. **Proxy.** Start it outside the sandbox:

   ```
   FREEPROXY_LOG=$LOCA_ROOT/logs/freeproxy.jsonl python3 -I bench/loca/freeproxy.py 18080
   ```

   - It reads the OpenRouter key from `OPENROUTER_API_KEY` or `FREEPROXY_KEY_FILE`.
   - It forwards only model ids that end in `:free`, refuses anything else, and stops at `FREEPROXY_DAILY_CAP` requests per UTC day (default 900).
   - It logs every request with its token counts, and removes the account id from error bodies.

2. **Stratavell.** For the Stratavell strategies, start a server, with the agent's model as summarizer:

   ```
   stratavell serve -addr 127.0.0.1:7077 -db $LOCA_ROOT/work/stratavell.db \
     -summarizer openai -summarizer-url http://127.0.0.1:18080/v1 -summarizer-model MODEL
   ```

3. **Runs.** `bench/loca/run.sh NAME LOCA_ARGS...` runs one configuration file:
   - First it runs `preflight.py` in the sandbox. That starts every MCP server and checks that each one lists tools.
   - If the preflight passes, it runs `loca run` in the sandbox.
   - It writes `RUN`, `preflight.txt`, `requests.jsonl` and LOCA's own outputs to `$LOCA_ROOT/work/runs/NAME/`.

4. **The protocol's runs.** `bench/loca/schedule.py`, with `LOCA_ROOT` set, runs the 60 runs of `protocol-agent-loca-v1.md` in the order of `order-v1.json`.
   - It resumes after an interruption.
   - It starts a run only when at least 160 of the day's 900 requests remain.
   - It reruns a void run at most twice.

## Sandbox

`sandbox.sh` runs LOCA under bubblewrap:
- The home directory is replaced, and only `LOCA_ROOT` is writable. The virtual environments are read-only.
- The environment is cleared. No API key enters the sandbox, because requests go to the local proxy.
- The network is shared: the proxy and the Stratavell server listen on localhost, and the model's Python code can still reach the internet.

LOCA's Python tool can read files outside the agent's workspace, including the ground truth. The protocol flags any run whose tool calls name those paths.
