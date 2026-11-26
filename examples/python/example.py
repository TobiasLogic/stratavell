import sys
from palimpsest_client import Client, text, tool_call, tool_result

socket_path = sys.argv[1] if len(sys.argv) > 1 else None
client = Client(socket_path=socket_path)
agent = client.session("py-demo").agent("main")

agent.append(
    text("user", "Find why the build fails. Do not touch the generated/ directory."),
    tool_call("c1", "bash", {"command": "make build"}, "Building first."),
    tool_result("c1", "bash", "gcc -o app main.c\nmain.c:12:5: error: implicit declaration of function 'parse_cfg'\nmake: *** [app] Error 1", exit_code=2, command="make build"),
)

ctx = agent.assemble(system="You are a build engineer.", fmt="anthropic", model="claude-haiku-5-5", max_tokens=2048)
req = ctx["request"]
print("tokens", ctx["tokens"], "of", ctx["capacity"], "| messages", len(req["messages"]), "| state", ctx["report"]["state"])

for hit in agent.search("implicit declaration parse_cfg"):
    print("hit", hit["id"], hit["provenance"], hit["snippet"][:80])

print(agent.tool("memory_get_task_state", {})["text"])
