import json
import os
import sys
import time
import urllib.request

import tiktoken

sys.path.insert(0, os.path.join(os.environ["LOCA_ROOT"], "upstream"))
from inference.stratavell_ctx import StratavellContext

ENC = tiktoken.get_encoding("cl100k_base")
PROXY = "http://127.0.0.1:18080/v1/chat/completions"
SCRATCH = os.environ.get("CALIB_STRATAVELL", "http://127.0.0.1:7078")
MODEL = "nvidia/nemotron-3-ultra-550b-a55b:free"
OUTPUT = 16384


def cl(x):
    return len(ENC.encode(json.dumps(x, ensure_ascii=False), disallowed_special=()))


def provider(messages, tools, tag):
    body = {"model": MODEL, "messages": messages, "max_tokens": 1, "temperature": 1.0}
    if tools:
        body["tools"] = tools
    for attempt in range(4):
        req = urllib.request.Request(PROXY, data=json.dumps(body).encode(), headers={"Content-Type": "application/json", "Authorization": "Bearer sandbox-no-key", "X-Run-Tag": tag})
        try:
            with urllib.request.urlopen(req, timeout=300) as r:
                u = json.loads(r.read()).get("usage") or {}
                if u.get("prompt_tokens"):
                    return u["prompt_tokens"]
        except Exception as e:
            print("retry", tag, str(e)[:200], file=sys.stderr, flush=True)
        time.sleep(5)
    raise SystemExit("no usage for " + tag)


def main():
    src, out_path = sys.argv[1], sys.argv[2]
    data = json.load(open(src))
    out = json.load(open(out_path)) if os.path.exists(out_path) else {}
    for name, v in data.items():
        if name in out:
            continue
        prompt, tools = v["prompt"], v["tools"]
        first = [{"role": "user", "content": prompt}]
        sctx = StratavellContext(f"calib-{os.path.basename(src)}-{name}-{int(time.time())}", 90000, OUTPUT, url=SCRATCH)
        passive = sctx.passive_tools(tools)
        row = {
            "tools": len(tools),
            "loca_x0": cl(first) + cl(tools),
            "loca_p0": provider(first, tools, "calib-loca"),
            "passive_x0": cl(first) + cl(passive),
            "passive_p0": provider(first, passive, "calib-passive"),
        }
        sctx.append(*first)
        msgs, stools = sctx.assemble(tools, MODEL, OUTPUT)
        row["stratavell_e0"] = sctx.recorder[-1]["tokens"]
        row["stratavell_p0"] = provider(msgs, stools, "calib-stratavell")
        out[name] = row
        json.dump(out, open(out_path, "w"), indent=1)
        print(name, json.dumps(row), flush=True)


if __name__ == "__main__":
    main()
