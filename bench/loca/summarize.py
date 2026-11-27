import calendar
import collections
import glob
import json
import os
import sys
import time

FLAG_KEYS = ("groundtruth_workspace", "local_db", "/files/")
MEMORY = ("memory_search", "memory_expand", "memory_get_event", "memory_get_artifact", "memory_get_task_state", "memory_get_summary_lineage", "memory_stats", "memory_update_task")


def manifest(path):
    out = {}
    for line in open(path):
        if "=" in line:
            k, v = line.rstrip("\n").split("=", 1)
            out[k] = v
    return out


def ts(s):
    return calendar.timegm(time.strptime(s, "%Y-%m-%dT%H:%M:%SZ"))


def run_row(d):
    m = manifest(os.path.join(d, "RUN"))
    row = {"run": os.path.basename(d), "exit": m.get("exit"), "preflight": m.get("preflight", "")}
    if "start" in m and "end" in m:
        row["minutes"] = round((ts(m["end"]) - ts(m["start"])) / 60, 1)
    reqs = [json.loads(l) for l in open(os.path.join(d, "requests.jsonl"))] if os.path.exists(os.path.join(d, "requests.jsonl")) else []
    ok = [r for r in reqs if r.get("status") == 200]
    agent = [r for r in ok if (r.get("tools") or 0) > 0]
    summ = [r for r in ok if not r.get("tools")]
    row.update({
        "agent_requests": len(agent),
        "summarizer_requests": len(summ),
        "failed_requests": len([r for r in reqs if r.get("status") != 200]),
        "prompt_tokens": sum(r.get("prompt_tokens") or 0 for r in ok),
        "completion_tokens": sum(r.get("completion_tokens") or 0 for r in ok),
        "max_agent_prompt": max([r.get("prompt_tokens") or 0 for r in agent], default=0),
    })
    states = sorted(glob.glob(os.path.join(d, "loca", "tasks", "*", "state*")))
    for s in states[:1]:
        e = json.load(open(os.path.join(s, "eval.json"))) if os.path.exists(os.path.join(s, "eval.json")) else {}
        row["success"] = e.get("accuracy")
        row["steps"] = e.get("steps")
        t = json.load(open(os.path.join(s, "trajectory.json"))) if os.path.exists(os.path.join(s, "trajectory.json")) else {}
        ev = t.get("events") or {}
        row["loca_events"] = {k: len(v) for k, v in ev.items() if v}
        conv = t.get("conversation") or {}
        history = conv.get("full_messages_history") or t.get("messages", [])
        calls = [c for msg in history if msg.get("tool_calls") for c in msg["tool_calls"]]
        last = history[-1] if history else {}
        row["ended"] = "context_lost" if "Context trimming removed all" in str(last.get("content", "")) else ("claim_done" if any(c["function"]["name"].startswith("claim_done") for c in calls[-1:]) else "other")
        names = collections.Counter(c["function"]["name"] for c in calls)
        row["tool_calls"] = sum(names.values())
        row["memory_calls"] = sum(v for k, v in names.items() if k in MEMORY)
        row["flagged"] = sorted({c["function"]["name"] for c in calls if any(k in str(c["function"].get("arguments", "")) for k in FLAG_KEYS)})
        al = os.path.join(s, "stratavell_assembly.jsonl")
        if os.path.exists(al):
            a = [json.loads(l) for l in open(al)]
            row["stratavell"] = {
                "max_estimated": max((x.get("tokens") or 0 for x in a), default=0),
                "capacity": a[0].get("capacity") if a else None,
                "states": dict(collections.Counter(x.get("state") for x in a)),
                "with_summaries": sum(1 for x in a if (x.get("summary_tokens") or 0) > 0),
                "with_masking": sum(1 for x in a if (x.get("masked") or 0) > 0),
                "max_wait_ms": max((x.get("wait_ms") or 0 for x in a), default=0),
            }
    return row


if __name__ == "__main__":
    root = os.environ.get("LOCA_ROOT", ".")
    pattern = sys.argv[1] if len(sys.argv) > 1 else "*"
    for d in sorted(glob.glob(os.path.join(root, "work", "runs", pattern))):
        if os.path.exists(os.path.join(d, "RUN")):
            print(json.dumps(run_row(d)))
