import json
import os
import sys
import tempfile
from pathlib import Path

ROOT = os.environ["LOCA_ROOT"]
sys.path.insert(0, os.path.join(ROOT, "upstream"))

from inference.run_react import dynamic_import_class, setup_mcp_servers, suppress_all_output
from gem.tools.mcp_tool import MCPTool
from gem.tools.tool_env_wrapper import ToolEnvWrapperOpenAI


def first_request(cfg):
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
        try:
            tool.close()
        except Exception:
            pass
        return {"prompt": prompt, "tools": tools[0] if tools else []}


if __name__ == "__main__":
    out = {}
    for cfg in json.load(open(sys.argv[1]))["configurations"]:
        out[cfg["name"]] = first_request(cfg)
        print(cfg["name"], len(out[cfg["name"]]["tools"]), flush=True)
    json.dump(out, open(sys.argv[2], "w"))
