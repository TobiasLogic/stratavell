# LOCA-bench with Stratavell

Runs [LOCA-bench](https://github.com/hkust-nlp/LOCA-bench) (MIT license) with Stratavell as the agent's context manager. The protocol is `bench/review/protocol-agent-loca-v1.2.md`, which amends `protocol-agent-loca-v1.1.md` and, through it, `protocol-agent-loca-v1.md`.

## What the patch changes

`loca.patch` applies to LOCA-bench commit 8b6fac4. It changes four things.

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

**4. Request timeout** (`inference/run_react.py`). LOCA's HTTP client waits up to 1,800 seconds for a reply instead of 60.
- Unpatched, a reply that took longer than 60 seconds was dropped and the same request was sent again, up to 50 times, with nothing shown to the agent.
- On a free endpoint that writes 60 to 90 tokens a second, that dropped every reply longer than about 4,000 tokens. At 39 tokens a second, a full 16,384-token reply takes about 7 minutes.

## Setup

Needs `git`, `uv`, `bwrap` (bubblewrap) and Node.js on Linux.

```
export LOCA_ROOT=/path/to/an/empty/directory
bench/loca/setup.sh
```

This clones LOCA-bench at 8b6fac4, applies the patch and builds three virtual environments:
- the main one, from `requirements-lock.txt`
- one for each of the two `uvx` tools

It also downloads tiktoken's `cl100k_base` file once, into `$LOCA_ROOT/tiktoken-cache`. Preflight and LOCA load it from there, so a run does not need the network to start.

The lock files were resolved as of the upstream commit date, 2026-04-29. Unpinned, current `mcp` and `fastmcp` releases break LOCA's servers.

## Running

1. **Proxy.** Start it outside the sandbox. The protocol uses NVIDIA's API catalog:

   ```
   FREEPROXY_UPSTREAM=nvidia FREEPROXY_MIN_GAP=1.6 FREEPROXY_DAILY_CAP=20000 \
     FREEPROXY_LOG=$LOCA_ROOT/logs/nvproxy.jsonl python3 -I bench/loca/freeproxy.py 18081
   ```

   - `FREEPROXY_UPSTREAM` is `openrouter` (the default) or `nvidia`. The key comes from `OPENROUTER_API_KEY` or `NVIDIA_API_KEY`, or from `FREEPROXY_KEY_FILE`.
   - It forwards only model ids that end in `:free` and refuses anything else. For NVIDIA it removes the suffix.
   - For NVIDIA it requests the reply as a stream and returns it as one reply, because in testing NVIDIA closed a non-streamed 16,384-token request after 5.7 minutes without a reply. It retries a rate-limit refusal (HTTP 429) up to 40 times, waiting 20 to 120 seconds.
   - For NVIDIA it also retries network errors and HTTP 500, 502, 503 and 504 every 30 seconds, for up to `FREEPROXY_RETRY_NET_SECONDS` (900 by default), so a short outage does not end a run.
   - It spaces requests at least `FREEPROXY_MIN_GAP` seconds apart and stops at `FREEPROXY_DAILY_CAP` requests per UTC day.
   - It logs every request with its token counts, and removes the account id from error bodies.
   - `GET /ready?model=ID` sends that model a one-line request, without retries, and returns 200 if it answers. The request is logged with the tag `ready`.

2. **Stratavell.** For the Stratavell strategies, start a server, with the agent's model as summarizer:

   ```
   stratavell serve -addr 127.0.0.1:7077 -db $LOCA_ROOT/work/stratavell.db \
     -summarizer openai -summarizer-url http://127.0.0.1:18081/v1 -summarizer-model MODEL
   ```

3. **Runs.** `bench/loca/run.sh NAME LOCA_ARGS...` runs one configuration file. `FREEPROXY_PORT` and `FREEPROXY_LOG` name the proxy (defaults 18080 and `$LOCA_ROOT/logs/freeproxy.jsonl`):
   - First it runs `preflight.py` in the sandbox. That starts every MCP server and checks that each one lists tools.
   - If the preflight passes, it runs `loca run` in the sandbox.
   - It writes `RUN`, `preflight.txt`, `requests.jsonl` and LOCA's own outputs to `$LOCA_ROOT/work/runs/NAME/`.

4. **Budget calibration.** Each strategy counts tokens its own way, so the protocol sets per-state limits that all correspond to the same number of provider tokens:
   - `dump_tools.py`, run in the sandbox, saves each state's first prompt and tool definitions.
   - `measure_calib.py` sends each state's first request in LOCA's, passive mode's and Stratavell's form, with a 1-token output cap. It needs the proxy (`CALIB_PROXY`) and a scratch `stratavell serve` (`CALIB_STRATAVELL`, default port 7078). `calib-v1.1.json` was measured through OpenRouter and `calib-v1.1-nvidia.json` through NVIDIA. The counts are identical.
   - `calibrate.py` turns those counts into the limits in `budget-v1.1.json`.

5. **The protocol's runs.** `bench/loca/schedule.py`, with `LOCA_ROOT` set, runs the 60 runs of `protocol-agent-loca-v1.2.md` in the order of `order-v1.json`, with the limits in `budget-v1.1.json`:

   ```
   LOCA_ROOT=... FREEPROXY_PORT=18081 FREEPROXY_LOG=$LOCA_ROOT/logs/nvproxy.jsonl FREEPROXY_DAILY_CAP=20000 \
     STRATAVELL_URL=http://127.0.0.1:7077 python3 -I bench/loca/schedule.py
   ```

   - It resumes after an interruption. Runs named with an earlier prefix in `SCHEDULE_PRIOR` (`v1.1` by default) count, so the valid v1.1 runs are not repeated.
   - Before every run, it calls the proxy's `/ready`, which sends the model a one-line request. While the model does not answer within `FREEPROXY_READY_TIMEOUT` seconds (120), it waits, and asks again every 5 minutes.
   - It starts a run only when at least 160 of the day's requests remain under `FREEPROXY_DAILY_CAP`.
   - It reruns a void run at most twice. A void run in which the model returned no reply does not count, and is retried with a `-zN` name. After 3 of these in a row on the same run, it stops with exit code 3.

## Sandbox

`sandbox.sh` runs LOCA under bubblewrap:
- The home directory is replaced, and only `LOCA_ROOT` is writable. The virtual environments are read-only.
- The environment is cleared. No API key enters the sandbox, because requests go to the local proxy.
- The network is shared: the proxy and the Stratavell server listen on localhost, and the model's Python code can still reach the internet.

LOCA's Python tool can read files outside the agent's workspace, including the ground truth. The protocol flags any run whose tool calls name those paths.
