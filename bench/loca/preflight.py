import json
import os
import sys
import tempfile
from pathlib import Path

ROOT = os.environ.get("LOCA_ROOT", ".")
sys.path.insert(0, os.path.join(ROOT, "upstream"))

import tiktoken
from inference.run_react import dynamic_import_class, setup_mcp_servers, suppress_all_output
from gem.tools.mcp_tool import MCPTool
from gem.tools.tool_env_wrapper import ToolEnvWrapperOpenAI

ENC = tiktoken.get_encoding("cl100k_base")


def check(cfg):
    with tempfile.TemporaryDirectory(dir=os.path.join(ROOT, "work")) as d:
        task_workspace = Path(d)
        agent_workspace = task_workspace / "agent_workspace"
        (task_workspace / "local_db").mkdir(parents=True)
        (agent_workspace / "memory").mkdir(parents=True)
        params = dict(cfg["env_params"])
        params.setdefault("task_dir", str(task_workspace))
        with suppress_all_output():
            env = dynamic_import_class(cfg["env_class"])(**params)
        mcp_config = setup_mcp_servers(json.loads(json.dumps(cfg["mcp_servers"])), task_workspace, agent_workspace)
        tool = MCPTool(mcp_config, validate_on_init=False, execution_timeout=120.0, fix_schema_for_openai=False)
        env = ToolEnvWrapperOpenAI(env, tools=[tool], max_tool_uses=10)
        with suppress_all_output():
            _, _, prompt, tools = env.reset()
        defs = tools[0] if tools else []
        names = [t["function"]["name"] for t in defs]
        enabled = [k for k, v in cfg["mcp_servers"].items() if v.get("enabled", True)]
        missing = [s for s in enabled if not any(n.startswith(s + "_") or n == s for n in names)]
        try:
            tool.close()
        except Exception:
            pass
        return {
            "name": cfg["name"],
            "seed": cfg["env_params"].get("seed"),
            "tools": len(names),
            "tool_tokens": len(ENC.encode(json.dumps(defs, ensure_ascii=False), disallowed_special=())),
            "servers": enabled,
            "missing": missing,
            "prompt_chars": len(prompt or ""),
        }


def config_path(args):
    if args and args[0] == "--run-config":
        rest = args[1:]
        for i, a in enumerate(rest):
            if a in ("-c", "--config-file") and i + 1 < len(rest):
                return rest[i + 1]
            if a.startswith("--config-file="):
                return a.split("=", 1)[1]
        raise SystemExit("preflight: no -c or --config-file in the run arguments")
    return args[0]


if __name__ == "__main__":
    cfgs = json.load(open(config_path(sys.argv[1:])))["configurations"]
    bad = 0
    for cfg in cfgs:
        r = check(cfg)
        bad += bool(r["missing"])
        print(json.dumps(r), flush=True)
    sys.exit(1 if bad else 0)
