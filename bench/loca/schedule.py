import calendar
import glob
import json
import os
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
PREFIX = os.environ.get("SCHEDULE_PREFIX", "v1.1")
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
        if hist and str(hist[-1].get("content", "")).startswith("API Error"):
            return "api error"
    return None


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
        for attempt in range(3):
            name = f"{PREFIX}-{state}-{strat}" + (f"-r{attempt}" if attempt else "")
            run_dir = os.path.join(ROOT, "work", "runs", name)
            if os.path.exists(os.path.join(run_dir, "RUN")) and "exit" in manifest(os.path.join(run_dir, "RUN")):
                reason = void_reason(run_dir)
                if reason is None:
                    break
                print(f"{name} already ran, void: {reason}", flush=True)
                continue
            wait_for_quota()
            env = dict(os.environ, **env_extra)
            for k in ("STRATAVELL_URL", "STRATAVELL_MODE", "STRATAVELL_WINDOW"):
                if k not in env_extra:
                    env.pop(k, None)
            cmd = [os.path.join(HERE, "run.sh"), name, "-c", cfg_path, "-s", "react", "-m", MODEL, "--max-tool-uses", TOOL_CAP, "--max-tokens", MAX_TOKENS] + args
            print(f"{time.strftime('%FT%TZ', time.gmtime())} start {name}", flush=True)
            subprocess.run(cmd, env=env)
            reason = void_reason(run_dir)
            print(f"{time.strftime('%FT%TZ', time.gmtime())} done {name}: {'valid' if reason is None else 'void, ' + reason}", flush=True)
            if reason is None:
                break
    print("schedule complete", flush=True)


if __name__ == "__main__":
    sys.exit(main())
