import collections
import glob
import json
import math
import os
import random
import shutil
import sqlite3
import statistics
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
os.environ.setdefault("SCHEDULE_PRIOR", "v1.1")
os.environ.setdefault("SCHEDULE_PREFIX", "v1.2")
import schedule
import summarize

ROOT = os.environ["LOCA_ROOT"]
DB = os.environ.get("STRATAVELL_DB", os.path.join(ROOT, "work", "v1.1-stratavell.db"))
PROXY_LOG = os.environ.get("FREEPROXY_LOG", os.path.join(ROOT, "logs", "nvproxy.jsonl"))
STRATS = ("trim", "compact", "compact_archive", "stratavell")
OTHERS = ("trim", "compact", "compact_archive")
TARGET = 57000
OVER = 1.05
OUTPUT_CAP = 16384
TOOL_CAP = 100
PRICE = {
    "source": "OpenRouter's listed price for nvidia/nemotron-3-ultra-550b-a55b (the paid id), read 2026-11-28",
    "prompt_per_million": 0.50,
    "completion_per_million": 2.20,
    "cache_read_per_million": 0.10,
}
BOOT = 10000
SEED = 0
NARROW_STRIP = "agent_workspace/files/"


def load_jsonl(path):
    out = []
    if os.path.exists(path):
        for line in open(path):
            line = line.strip()
            if line:
                try:
                    out.append(json.loads(line))
                except ValueError:
                    pass
    return out


def state_dir(run_dir):
    s = sorted(glob.glob(os.path.join(run_dir, "loca", "tasks", "*", "state*")))
    return s[0] if s else None


def attempts(state, strat):
    runs = os.path.join(ROOT, "work", "runs")
    seen = []
    for p in dict.fromkeys(schedule.PRIOR + [schedule.PREFIX]):
        base = f"{p}-{state}-{strat}"
        for d in [os.path.join(runs, base)] + glob.glob(os.path.join(runs, base + "-*")):
            m = schedule.manifest(os.path.join(d, "RUN"))
            if "exit" in m:
                seen.append((m.get("start", ""), d, m))
    out = []
    for start, d, m in sorted(seen):
        reason = schedule.void_reason(d)
        n = schedule.replies(d)
        out.append({
            "state": state,
            "strategy": strat,
            "run": os.path.basename(d),
            "start": start,
            "end": m.get("end"),
            "preflight": m.get("preflight"),
            "exit": m.get("exit"),
            "void": reason,
            "model_replies": n,
            "counted": reason is None or n > 0,
            "dir": d,
        })
    return out


def stratavell_jobs():
    c = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    rows = c.execute("select id, session_id, kind, level, state, attempts, started_at, finished_at from jobs where started_at is not null and finished_at is not null").fetchall()
    return [{"job": r[0], "session": r[1], "kind": r[2], "level": r[3], "state": r[4], "attempts": r[5], "start": r[6] / 1e9, "end": r[7] / 1e9} for r in rows]


def session_owner(session, tries):
    state, _, ts = session.rpartition("-s0-")
    try:
        ts = int(ts)
    except ValueError:
        return None
    for a in tries:
        if a["state"] == state and a["strategy"] in ("compact_archive", "stratavell") and a["end"]:
            lo = summarize.ts(a["start"])
            hi = summarize.ts(a["end"])
            if lo - 5 <= ts <= hi:
                return a["run"]
    return None


def is_summary_shape(r):
    return not r.get("tools") and r.get("messages") == 2 and r.get("tag") != "ready"


def attribute(tries, jobs):
    owner = {}
    for j in jobs:
        j["run"] = session_owner(j["session"], tries)
    windows = [(summarize.ts(a["start"]), summarize.ts(a["end"]) + 1, a["run"]) for a in tries if a["strategy"] in ("compact_archive", "stratavell") and a["end"]]
    log = [r for r in load_jsonl(PROXY_LOG) if is_summary_shape(r)]
    how = collections.Counter()
    for r in log:
        t0, t1 = r["t"], r["t"] + (r.get("seconds") or 0)
        hits = [j for j in jobs if j["start"] - 1 <= t0 and t1 <= j["end"] + 1]
        runs = {j["run"] for j in hits}
        if len(runs) == 1:
            owner[round(r["t"], 4)] = runs.pop()
            how["job window"] += 1
        elif len(runs) > 1:
            owner[round(r["t"], 4)] = "ambiguous"
            how["ambiguous"] += 1
        else:
            inside = {w[2] for w in windows if w[0] <= t0 <= w[1]}
            if len(inside) == 1:
                owner[round(r["t"], 4)] = inside.pop()
                how["run window, before a retried job's recorded start"] += 1
    return owner, log, how


def ended(hist, calls):
    last = hist[-1] if hist else {}
    text = str(last.get("content", ""))
    if "Context trimming removed all" in text or "Cannot fit messages" in text:
        return "context_lost"
    if calls and calls[-1]["function"]["name"].startswith("claim_done"):
        return "claim_done"
    if len(calls) >= TOOL_CAP:
        return "tool_cap"
    if last.get("role") == "assistant" and not last.get("tool_calls"):
        return "text_reply"
    return "other"


def flags(calls):
    registered, narrow = set(), set()
    for c in calls:
        a = str(c["function"].get("arguments", ""))
        name = c["function"]["name"]
        if any(k in a for k in summarize.FLAG_KEYS):
            registered.add(name)
        b = a.replace(NARROW_STRIP, "")
        if any(k in b for k in summarize.FLAG_KEYS):
            narrow.add(name)
    return sorted(registered), sorted(narrow)


def memory_results(hist):
    res = {m.get("tool_call_id"): str(m.get("content")) for m in hist if m.get("role") == "tool"}
    out = collections.Counter()
    for m in hist:
        for c in m.get("tool_calls") or []:
            if c["function"]["name"] not in summarize.MEMORY:
                continue
            r = res.get(c["id"])
            if r is None:
                out["no_result"] += 1
            elif r.startswith(("[memory_get_event", "[memory_get_artifact", "[memory_expand")):
                out["page"] += 1
            elif "not found" in r[:80]:
                out["not_found"] += 1
            elif r.startswith("this exact"):
                out["repeat_notice"] += 1
            elif r.startswith("[memory_search"):
                out["search_hits"] += 1
            else:
                out["other"] += 1
    return dict(out)


def cost(prompt, completion, cached):
    plain = prompt * PRICE["prompt_per_million"] / 1e6 + completion * PRICE["completion_per_million"] / 1e6
    cached = min(cached, prompt)
    with_cache = (prompt - cached) * PRICE["prompt_per_million"] / 1e6 + cached * PRICE["cache_read_per_million"] / 1e6 + completion * PRICE["completion_per_million"] / 1e6
    return round(plain, 4), round(with_cache, 4)


def run_metrics(a, owner, summary_log):
    d = a["dir"]
    m = schedule.manifest(os.path.join(d, "RUN"))
    base = summarize.run_row(d)
    reqs = [r for r in load_jsonl(os.path.join(d, "requests.jsonl")) if r.get("tag") != "ready"]
    mine = a["run"]
    agent, loca_summ, spilled_in, failed = [], [], [], collections.Counter()
    for r in reqs:
        if r.get("tools"):
            if r.get("status") == 200:
                agent.append(r)
            else:
                failed["agent " + str(r.get("status"))] += 1
            continue
        if is_summary_shape(r):
            o = owner.get(round(r["t"], 4))
            if o and o != mine:
                spilled_in.append(r)
                continue
            if o == mine:
                continue
        if r.get("status") == 200:
            loca_summ.append(r)
        else:
            failed["summary " + str(r.get("status"))] += 1
    strat_summ_all = [r for r in summary_log if owner.get(round(r["t"], 4)) == mine]
    strat_summ = [r for r in strat_summ_all if r.get("status") == 200]
    for r in strat_summ_all:
        if r.get("status") != 200:
            failed["stratavell summary " + str(r.get("status"))] += 1
    in_slice = {round(r["t"], 4) for r in reqs}
    outside = [r for r in strat_summ if round(r["t"], 4) not in in_slice]
    counted = agent + loca_summ + strat_summ
    P = lambda rs: sum(r.get("prompt_tokens") or 0 for r in rs)
    C = lambda rs: sum(r.get("completion_tokens") or 0 for r in rs)
    K = lambda rs: sum(r.get("cached_tokens") or 0 for r in rs)
    prompt, completion, cached = P(counted), C(counted), K(counted)
    plain, with_cache = cost(prompt, completion, cached)
    sd = state_dir(d)
    t = json.load(open(os.path.join(sd, "trajectory.json"))) if sd and os.path.exists(os.path.join(sd, "trajectory.json")) else {}
    hist = (t.get("conversation") or {}).get("full_messages_history") or t.get("messages", [])
    calls = [c for msg in hist if msg.get("tool_calls") for c in msg["tool_calls"]]
    reg, narrow = flags(calls)
    success = base.get("success")
    agent_prompts = [r.get("prompt_tokens") or 0 for r in agent]
    reasoning_known = [r for r in counted if r.get("reasoning_tokens") is not None]
    finishes = collections.Counter(str(r.get("finish")) for r in counted if "finish" in r)
    return {
        "state": a["state"],
        "strategy": a["strategy"],
        "run": mine,
        "protocol": mine.split("-", 1)[0],
        "start": m.get("start"),
        "end": m.get("end"),
        "minutes": base.get("minutes"),
        "load_start": float(m.get("load_start", "0").split()[0]),
        "load_end": float(m.get("load_end", "0").split()[0]),
        "success": success,
        "flagged_registered": reg,
        "flagged_narrow": narrow,
        "success_primary": 1 if success == 1.0 and not reg else 0,
        "success_narrow": 1 if success == 1.0 and not narrow else 0,
        "ended": ended(hist, calls),
        "steps": base.get("steps"),
        "tool_calls": len(calls),
        "memory_calls": base.get("memory_calls"),
        "memory_calls_by_tool": dict(collections.Counter(c["function"]["name"] for c in calls if c["function"]["name"] in summarize.MEMORY)),
        "memory_results": memory_results(hist),
        "agent_requests": len(agent),
        "agent_prompt_mean": round(statistics.mean(agent_prompts), 1) if agent_prompts else 0,
        "agent_prompt_median": statistics.median(agent_prompts) if agent_prompts else 0,
        "agent_prompt_max": max(agent_prompts, default=0),
        "agent_prompt_sum": sum(agent_prompts),
        "agent_completion_sum": C(agent),
        "loca_summary_requests": len(loca_summ),
        "loca_summary_prompt_sum": P(loca_summ),
        "loca_summary_completion_sum": C(loca_summ),
        "loca_summary_prompt_max": max((r.get("prompt_tokens") or 0 for r in loca_summ), default=0),
        "stratavell_summary_requests": len(strat_summ),
        "stratavell_summary_prompt_sum": P(strat_summ),
        "stratavell_summary_completion_sum": C(strat_summ),
        "stratavell_summary_outside_run_window": len(outside),
        "spilled_in_excluded": len(spilled_in),
        "failed_requests": dict(failed),
        "capped_agent_replies": sum(1 for r in agent if (r.get("completion_tokens") or 0) >= OUTPUT_CAP),
        "capped_summary_replies": sum(1 for r in loca_summ + strat_summ if (r.get("completion_tokens") or 0) >= OUTPUT_CAP),
        "prompt_tokens": prompt,
        "completion_tokens": completion,
        "cached_tokens": cached,
        "requests_with_reasoning_count": len(reasoning_known),
        "reasoning_tokens_where_reported": sum(r["reasoning_tokens"] or 0 for r in reasoning_known),
        "finish_reasons_where_logged": dict(finishes),
        "cost_estimate_usd": plain,
        "cost_estimate_with_cache_usd": with_cache,
        "loca_events": base.get("loca_events"),
        "stratavell_assembly": base.get("stratavell"),
    }


def mcnemar(b, c):
    n = b + c
    if n == 0:
        return 1.0
    k = min(b, c)
    return min(1.0, 2 * sum(math.comb(n, i) for i in range(k + 1)) / 2 ** n)


def boot_ci(diffs):
    rng = random.Random(SEED)
    n = len(diffs)
    means = sorted(sum(diffs[rng.randrange(n)] for _ in range(n)) / n for _ in range(BOOT))
    return means[int(0.025 * BOOT)], means[math.ceil(0.975 * BOOT) - 1]


def sign_test(diffs):
    neg = sum(1 for d in diffs if d < 0)
    pos = sum(1 for d in diffs if d > 0)
    return {"stratavell_lower": neg, "stratavell_higher": pos, "ties": len(diffs) - neg - pos, "p": mcnemar(neg, pos)}


def pair(rows, other, key):
    by = {(r["state"], r["strategy"]): r for r in rows}
    states = sorted({r["state"] for r in rows})
    s = [by[(st, "stratavell")][key] for st in states]
    o = [by[(st, other)][key] for st in states]
    both = sum(1 for x, y in zip(s, o) if x and y)
    s_only = sum(1 for x, y in zip(s, o) if x and not y)
    o_only = sum(1 for x, y in zip(s, o) if y and not x)
    neither = len(states) - both - s_only - o_only
    diffs = [x - y for x, y in zip(s, o)]
    lo, hi = boot_ci(diffs)
    return {
        "other": other,
        "both": both,
        "stratavell_only": s_only,
        "other_only": o_only,
        "neither": neither,
        "difference": round(sum(diffs) / len(diffs), 4),
        "bootstrap_95": [round(lo, 4), round(hi, 4)],
        "mcnemar_p": round(mcnemar(s_only, o_only), 4),
    }


def token_pair(rows, other, key):
    by = {(r["state"], r["strategy"]): r for r in rows}
    states = sorted({r["state"] for r in rows})
    diffs = [by[(st, "stratavell")][key] - by[(st, other)][key] for st in states]
    return {"other": other, "median_difference": statistics.median(diffs), "differences": dict(zip(states, diffs)), "sign_test": sign_test(diffs)}


def analysis(rows, tries):
    per = {}
    for s in STRATS:
        rs = [r for r in rows if r["strategy"] == s]
        per[s] = {
            "runs": len(rs),
            "success_raw": sum(1 for r in rs if r["success"] == 1.0),
            "success_primary": sum(r["success_primary"] for r in rs),
            "success_narrow": sum(r["success_narrow"] for r in rs),
            "ended": dict(collections.Counter(r["ended"] for r in rs)),
            "median_agent_prompt_mean": statistics.median(r["agent_prompt_mean"] for r in rs),
            "median_prompt_tokens_per_run": statistics.median(r["prompt_tokens"] for r in rs),
            "prompt_tokens": sum(r["prompt_tokens"] for r in rs),
            "completion_tokens": sum(r["completion_tokens"] for r in rs),
            "cached_tokens": sum(r["cached_tokens"] for r in rs),
            "agent_requests": sum(r["agent_requests"] for r in rs),
            "loca_summary_requests": sum(r["loca_summary_requests"] for r in rs),
            "stratavell_summary_requests": sum(r["stratavell_summary_requests"] for r in rs),
            "memory_calls": sum(r["memory_calls"] or 0 for r in rs),
            "memory_calls_by_tool": dict(sum((collections.Counter(r["memory_calls_by_tool"]) for r in rs), collections.Counter())),
            "memory_calls_in_tool_cap_runs": [r["memory_calls"] for r in rs if r["ended"] == "tool_cap"],
            "tool_cap_runs": sum(1 for r in rs if r["ended"] == "tool_cap"),
            "memory_calls_in_tool_cap_runs_min": min((r["memory_calls"] or 0 for r in rs if r["ended"] == "tool_cap"), default=0),
            "memory_calls_in_tool_cap_runs_max": max((r["memory_calls"] or 0 for r in rs if r["ended"] == "tool_cap"), default=0),
            "memory_results": dict(sum((collections.Counter(r["memory_results"]) for r in rs), collections.Counter())),
            "tool_calls": sum(r["tool_calls"] for r in rs),
            "minutes": round(sum(r["minutes"] or 0 for r in rs), 1),
            "capped_replies": sum(r["capped_agent_replies"] + r["capped_summary_replies"] for r in rs),
            "failed_requests": sum(sum(r["failed_requests"].values()) for r in rs),
            "cost_estimate_usd": round(sum(r["cost_estimate_usd"] for r in rs), 2),
            "cost_estimate_with_cache_usd": round(sum(r["cost_estimate_with_cache_usd"] for r in rs), 2),
            "over_budget_states": sorted(r["state"] for r in rs if r["agent_prompt_max"] > TARGET * OVER),
            "max_agent_prompt": max(r["agent_prompt_max"] for r in rs),
        }
    succ = {k: {o: pair(rows, o, k) for o in OTHERS} for k in ("success_primary", "success_narrow")}
    tok = {
        "agent_prompt_mean": {o: token_pair(rows, o, "agent_prompt_mean") for o in OTHERS},
        "prompt_tokens": {o: token_pair(rows, o, "prompt_tokens") for o in OTHERS},
    }
    exp = {}
    for k in ("success_primary", "success_narrow"):
        n = {s: per[s][k] for s in STRATS}
        exp[k] = {
            "E1": n["stratavell"] >= n["trim"] - 1 and n["stratavell"] >= n["compact"] - 1,
            "E3": n["stratavell"] >= n["compact_archive"] - 1,
        }
    med = {s: per[s]["median_agent_prompt_mean"] for s in STRATS}
    exp["E2"] = med["stratavell"] < med["compact"] and med["stratavell"] < med["compact_archive"]
    exp["E4"] = any(r["strategy"] == "trim" and r["ended"] == "context_lost" for r in rows)
    voids = [a for a in tries if a["void"]]
    return {
        "target": TARGET,
        "over_budget_threshold": TARGET * OVER,
        "price": PRICE,
        "bootstrap": {"resamples": BOOT, "seed": SEED, "method": "percentile, states resampled with replacement"},
        "difference_sign": "stratavell minus the other strategy",
        "per_strategy": per,
        "success_pairs": succ,
        "token_pairs": tok,
        "expectations": exp,
        "voids": {
            "total": len(voids),
            "counted": sum(1 for a in voids if a["counted"]),
            "no_model_reply": sum(1 for a in voids if not a["counted"]),
            "by_reason": dict(collections.Counter(a["void"] for a in voids)),
            "counted_runs": [{"run": a["run"], "void": a["void"], "model_replies": a["model_replies"]} for a in voids if a["counted"]],
        },
        "excluded_slots": [],
        "scored_runs": len(rows),
        "runs_v11": sum(1 for r in rows if r["protocol"] == "v1.1"),
        "runs_v12": sum(1 for r in rows if r["protocol"] == "v1.2"),
        "first_start": min(r["start"] for r in rows),
        "last_end": max(r["end"] for r in rows),
        "cost_estimate_total_usd": round(sum(per[s]["cost_estimate_usd"] for s in STRATS), 2),
        "cost_estimate_with_cache_total_usd": round(sum(per[s]["cost_estimate_with_cache_usd"] for s in STRATS), 2),
        "requests_with_reasoning_reported": sum(r["requests_with_reasoning_count"] for r in rows),
        "requests_counted": sum(r["agent_requests"] + r["loca_summary_requests"] + r["stratavell_summary_requests"] for r in rows),
        "attempts": len(tries),
        "states": len({r["state"] for r in rows}),
        "states_with_any_success": sorted({r["state"] for r in rows if r["success"] == 1.0}),
        "states_with_any_success_count": len({r["state"] for r in rows if r["success"] == 1.0}),
        "tool_cap_runs": sum(1 for r in rows if r["ended"] == "tool_cap"),
        "success_raw_total": sum(1 for r in rows if r["success"] == 1.0),
        "dates": sorted({r["start"][:10] for r in rows}),
        "protocols": dict(collections.Counter(r["protocol"] for r in rows)),
        "load_1min": [min(min(r["load_start"], r["load_end"]) for r in rows), max(max(r["load_start"], r["load_end"]) for r in rows)],
        "flagged_registered": [{"run": r["run"], "tools": r["flagged_registered"], "success": r["success"]} for r in rows if r["flagged_registered"]],
        "flagged_narrow": [{"run": r["run"], "tools": r["flagged_narrow"], "success": r["success"]} for r in rows if r["flagged_narrow"]],
    }


def copy_raw(rows, out):
    raw = os.path.join(out, "raw")
    os.makedirs(raw, exist_ok=True)
    for r in rows:
        src = os.path.join(ROOT, "work", "runs", r["run"])
        dst = os.path.join(raw, r["run"])
        os.makedirs(dst, exist_ok=True)
        for f in ("RUN", "requests.jsonl"):
            shutil.copy2(os.path.join(src, f), os.path.join(dst, f))
        sd = state_dir(src)
        for f in ("eval.json", "stratavell_assembly.jsonl"):
            if sd and os.path.exists(os.path.join(sd, f)):
                shutil.copy2(os.path.join(sd, f), os.path.join(dst, f))


def main():
    out = sys.argv[1]
    os.makedirs(out, exist_ok=True)
    order = json.load(open(os.path.join(HERE, "order-v1.json")))
    tries = []
    chosen = []
    for item in order:
        a = attempts(item["state"], item["strategy"])
        tries.extend(a)
        valid = [x for x in a if x["void"] is None]
        if not valid:
            chosen.append(None)
            continue
        chosen.append(valid[-1])
    jobs = stratavell_jobs()
    owner, summary_log, how = attribute(tries, jobs)
    rows = [run_metrics(a, owner, summary_log) for a in chosen if a]
    first = min(summarize.ts(a["start"]) for a in tries)
    unowned = [j for j in jobs if j["run"] is None]
    summary_requests = [r for r in summary_log if r["t"] >= first]
    check = {
        "stratavell_jobs": len(jobs),
        "jobs_without_run": len(unowned),
        "summary_shaped_requests_since_first_run": len(summary_requests),
        "matched_to_one_run": sum(1 for r in summary_requests if owner.get(round(r["t"], 4)) not in (None, "ambiguous")),
        "matched_by": dict(how),
        "matched_by_job_window": how["job window"],
        "matched_by_run_window": how["run window, before a retried job's recorded start"],
        "retried_jobs": sum(1 for j in jobs if j["attempts"]),
        "ambiguous": sum(1 for r in summary_requests if owner.get(round(r["t"], 4)) == "ambiguous"),
        "unmatched": sum(1 for r in summary_requests if owner.get(round(r["t"], 4)) is None),
        "trim_runs_with_summary_requests_left": sum(1 for r in rows if r["strategy"] == "trim" and r["loca_summary_requests"]),
        "stratavell_runs_with_loca_summary_requests": sum(1 for r in rows if r["strategy"] == "stratavell" and r["loca_summary_requests"]),
    }
    res = analysis(rows, tries)
    res["attribution_check"] = check
    res["excluded_slots"] = [f"{i['state']} {i['strategy']}" for i, a in zip(order, chosen) if not a]
    with open(os.path.join(out, "runs.jsonl"), "w") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")
    with open(os.path.join(out, "attempts.jsonl"), "w") as f:
        for a in tries:
            f.write(json.dumps({k: v for k, v in a.items() if k != "dir"}) + "\n")
    with open(os.path.join(out, "stratavell-jobs.jsonl"), "w") as f:
        for j in sorted(jobs, key=lambda j: j["start"]):
            f.write(json.dumps(j) + "\n")
    with open(os.path.join(out, "proxy-log.jsonl"), "w") as f:
        for r in load_jsonl(PROXY_LOG):
            if r["t"] >= first - 600:
                f.write(json.dumps(r) + "\n")
    json.dump(res, open(os.path.join(out, "analysis.json"), "w"), indent=1)
    copy_raw(rows, out)
    print(json.dumps(check))


if __name__ == "__main__":
    main()
