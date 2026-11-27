import calendar
import glob
import json
import os
import socket
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.environ["LOCA_ROOT"]
MODEL = os.environ.get("LOCA_MODEL", "nvidia/nemotron-3-ultra-550b-a55b:free")
STRATAVELL_URL = os.environ.get("STRATAVELL_URL", "http://127.0.0.1:7077")
CAP = int(os.environ.get("FREEPROXY_DAILY_CAP", "900"))
RESERVE = int(os.environ.get("SCHEDULE_RESERVE", "160"))
TOOL_CAP = "100"
MAX_TOKENS = "16384"
PREFIX = os.environ.get("SCHEDULE_PREFIX", "v1.2")
PRIOR = [p for p in os.environ.get("SCHEDULE_PRIOR", "v1.1").split(",") if p]
UPSTREAM_HOST = os.environ.get("SCHEDULE_UPSTREAM_HOST", "integrate.api.nvidia.com")
HALT_AFTER = 3
API_ERRORS = ("API Error", "Error: Failed to get response after multiple retries.", "Error: Invalid parameter format.", "Error: Request failed with 400 status.")
BUDGET = json.load(open(os.path.join(HERE, "budget-v1.1.json")))


def strategy(name, state):
    b = BUDGET[state]
    if name == "trim":
        return {}, ["--max-context-size", str(b["loca_max_context_size"])]
    if name == "compact":
        return {}, ["--context-summary", "--reset-size", str(b["loca_reset_size"]), "--max-context-size", str(b["loca_max_context_size"])]
    if name == "compact_archive":
        env = {"STRATAVELL_URL": STRATAVELL_URL, "STRATAVELL_MODE": "passive", "STRATAVELL_WINDOW": str(b["stratavell_window"])}
        return env, ["--context-summary", "--reset-size", str(b["passive_reset_size"]), "--max-context-size", str(b["passive_max_context_size"])]
    if name == "stratavell":
        return {"STRATAVELL_URL": STRATAVELL_URL, "STRATAVELL_WINDOW": str(b["stratavell_window"])}, []
    raise KeyError(name)


def today_count():
    log = os.environ.get("FREEPROXY_LOG", os.path.join(ROOT, "logs", "freeproxy.jsonl"))
    day = time.strftime("%Y-%m-%d", time.gmtime())
    n = 0
    if os.path.exists(log):
        for line in open(log):
            try:
                e = json.loads(line)
            except ValueError:
                continue
            if isinstance(e.get("status"), int) and time.strftime("%Y-%m-%d", time.gmtime(e["t"])) == day:
                n += 1
    return n


def stamp():
    return time.strftime("%FT%TZ", time.gmtime())


def wait_for_upstream():
    waited = False
    while True:
        try:
            socket.create_connection((UPSTREAM_HOST, 443), timeout=10).close()
            if waited:
                print(f"{stamp()} {UPSTREAM_HOST} reachable again", flush=True)
            return
        except OSError as e:
            if not waited:
                print(f"{stamp()} {UPSTREAM_HOST} unreachable ({type(e).__name__}), waiting", flush=True)
            waited = True
            time.sleep(60)


def wait_for_quota():
    while today_count() + RESERVE > CAP:
        now = time.time()
        tomorrow = calendar.timegm(time.strptime(time.strftime("%Y-%m-%d", time.gmtime(now)), "%Y-%m-%d")) + 86400 + 120
        print(f"{time.strftime('%FT%TZ', time.gmtime())} quota: {today_count()} used today, waiting {int(tomorrow - now)} s", flush=True)
        time.sleep(max(60, tomorrow - now))


def manifest(path):
    out = {}
    if os.path.exists(path):
        for line in open(path):
            if "=" in line:
                k, v = line.rstrip("\n").split("=", 1)
                out[k] = v
    return out


def void_reason(run_dir):
    m = manifest(os.path.join(run_dir, "RUN"))
    if m.get("preflight") not in ("0", None):
        return "preflight"
    if m.get("exit") != "0":
        return "exit " + str(m.get("exit"))
    reqs = os.path.join(run_dir, "requests.jsonl")
    if os.path.exists(reqs):
        for line in open(reqs):
            if '"status": "capped"' in line:
                return "daily cap"
    states = sorted(glob.glob(os.path.join(run_dir, "loca", "tasks", "*", "state*")))
    if not states:
        return "no task output"
    ev = os.path.join(states[0], "eval.json")
    if not os.path.exists(ev):
        return "no eval"
    e = json.load(open(ev))
    if e.get("status") != "success":
        return "eval status " + str(e.get("status"))
    traj = os.path.join(states[0], "trajectory.json")
    if os.path.exists(traj):
        t = json.load(open(traj))
        hist = (t.get("conversation") or {}).get("full_messages_history") or t.get("messages") or []
        if hist and str(hist[-1].get("content", "")).startswith(API_ERRORS):
            return "api error"
    return None


def replies(run_dir):
    p = os.path.join(run_dir, "requests.jsonl")
    if not os.path.exists(p):
        return 0
    return sum(1 for line in open(p) if '"status": 200' in line)


def history(state, strat):
    runs = os.path.join(ROOT, "work", "runs")
    seen = []
    for p in dict.fromkeys(PRIOR + [PREFIX]):
        base = f"{p}-{state}-{strat}"
        for d in [os.path.join(runs, base)] + glob.glob(os.path.join(runs, base + "-*")):
            m = manifest(os.path.join(d, "RUN"))
            if "exit" in m:
                seen.append((m.get("start", ""), d))
    valid, counted, free = None, 0, 0
    for _, d in sorted(seen):
        reason = void_reason(d)
        if reason is None:
            valid = d
        elif replies(d):
            counted += 1
        else:
            free += 1
    return valid, counted, free


def next_name(state, strat, counted):
    base = f"{PREFIX}-{state}-{strat}" + (f"-r{counted}" if counted else "")
    name, k = base, 0
    while os.path.exists(os.path.join(ROOT, "work", "runs", name)):
        k += 1
        name = f"{base}-z{k}"
    return name


def main():
    order = json.load(open(os.path.join(HERE, "order-v1.json")))
    configs = {c["name"]: c for c in json.load(open(os.path.join(HERE, "config-v1-64k.json")))["configurations"]}
    os.makedirs(os.path.join(ROOT, "work", "configs"), exist_ok=True)
    for item in order:
        state, strat = item["state"], item["strategy"]
        cfg_path = os.path.join(ROOT, "work", "configs", f"{state}.json")
        if not os.path.exists(cfg_path):
            json.dump({"configurations": [configs[state]]}, open(cfg_path, "w"), indent=1)
        env_extra, args = strategy(strat, state)
        streak = 0
        while True:
            valid, counted, free = history(state, strat)
            if valid:
                print(f"{state} {strat}: valid run {os.path.basename(valid)}", flush=True)
                break
            if counted >= 3:
                print(f"{state} {strat}: void after {counted} counted attempts, excluded", flush=True)
                break
            wait_for_upstream()
            wait_for_quota()
            name = next_name(state, strat, counted)
            run_dir = os.path.join(ROOT, "work", "runs", name)
            env = dict(os.environ, **env_extra)
            for k in ("STRATAVELL_URL", "STRATAVELL_MODE", "STRATAVELL_WINDOW"):
                if k not in env_extra:
                    env.pop(k, None)
            cmd = [os.path.join(HERE, "run.sh"), name, "-c", cfg_path, "-s", "react", "-m", MODEL, "--max-tool-uses", TOOL_CAP, "--max-tokens", MAX_TOKENS] + args
            print(f"{stamp()} start {name}", flush=True)
            subprocess.run(cmd, env=env)
            reason = void_reason(run_dir)
            if reason is None:
                print(f"{stamp()} done {name}: valid", flush=True)
                break
            if replies(run_dir):
                streak = 0
                print(f"{stamp()} done {name}: void, {reason}", flush=True)
                continue
            streak += 1
            print(f"{stamp()} done {name}: void, {reason}, no model reply, not counted", flush=True)
            if streak >= HALT_AFTER:
                print(f"{stamp()} halt: {streak} voids in a row with no model reply on {state} {strat}", flush=True)
                return 3
    print("schedule complete", flush=True)


if __name__ == "__main__":
    sys.exit(main())
