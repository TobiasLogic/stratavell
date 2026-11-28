import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
STRATS = ("trim", "compact", "compact_archive", "stratavell")
OTHERS = ("trim", "compact", "compact_archive")
LABEL = {"trim": "trim", "compact": "compact", "compact_archive": "compact + archive", "stratavell": "stratavell"}
ENDINGS = ("claim_done", "tool_cap", "text_reply", "context_lost", "other")
ENDING_LABEL = {"claim_done": "claimed done", "tool_cap": "tool cap", "text_reply": "text reply, no claim", "context_lost": "context lost", "other": "other"}


def short(state):
    return state[: -len("S2LEnv")] if state.endswith("S2LEnv") else state


def num(x):
    if isinstance(x, bool):
        return "yes" if x else "no"
    if isinstance(x, int):
        return f"{x:,}"
    if isinstance(x, float):
        if x.is_integer():
            return f"{int(x):,}"
        if abs(x) >= 100:
            return f"{x:,.1f}"
        return f"{x:.3f}" if abs(x) < 1 else f"{x:.4g}"
    return str(x)


def table(headers, rows, fmt):
    rows = [[num(c) if not isinstance(c, str) else c for c in r] for r in rows]
    if fmt == "md":
        out = ["| " + " | ".join(headers) + " |", "|" + "|".join("---" for _ in headers) + "|"]
        out += ["| " + " | ".join(r) + " |" for r in rows]
        return "\n".join(out)
    w = [max(len(str(x)) for x in col) for col in zip(headers, *rows)]
    line = lambda r: "  ".join(str(c).ljust(w[i]) for i, c in enumerate(r)).rstrip()
    return "\n".join([line(headers), line(["-" * x for x in w])] + [line(r) for r in rows])


def load(res):
    a = json.load(open(os.path.join(res, "analysis.json")))
    runs = [json.loads(l) for l in open(os.path.join(res, "runs.jsonl"))]
    tries = [json.loads(l) for l in open(os.path.join(res, "attempts.jsonl"))]
    return a, runs, tries


def tables(a, runs, tries, fmt):
    by = {(r["state"], r["strategy"]): r for r in runs}
    states = list(dict.fromkeys(r["state"] for r in runs))
    per = a["per_strategy"]
    T = {}

    def cell(r):
        mark = "1" if r["success"] == 1.0 else "0"
        if r["flagged_registered"]:
            mark += " (flagged)"
        return mark

    T["success_grid"] = table(["state"] + [LABEL[s] for s in STRATS], [[short(st)] + [cell(by[(st, s)]) for s in STRATS] for st in states], fmt)
    T["success_counts"] = table(
        ["strategy", "checker success", "primary (registered flag)", "narrow flag reading"],
        [[LABEL[s], per[s]["success_raw"], per[s]["success_primary"], per[s]["success_narrow"]] for s in STRATS], fmt)
    for key, name in (("success_primary", "pairs_primary"), ("success_narrow", "pairs_narrow")):
        rows = []
        for o in OTHERS:
            p = a["success_pairs"][key][o]
            rows.append([f"stratavell vs {LABEL[o]}", p["both"], p["stratavell_only"], p["other_only"], p["neither"], f"{p['difference']:+.3f}", f"{p['bootstrap_95'][0]:+.3f} to {p['bootstrap_95'][1]:+.3f}", f"{p['mcnemar_p']:.3f}", f"{p['mcnemar_p_holm']:.3f}"])
        T[name] = table(["pair", "both", "stratavell only", "other only", "neither", "difference", "bootstrap 95%", "McNemar p", "Holm p"], rows, fmt)
    e = a["expectations"]
    met = lambda x: "met" if x else "not met"
    T["expectations"] = table(["expectation", "primary (registered flag)", "narrow flag reading"], [
        ["E1: stratavell >= trim - 1 and >= compact - 1", met(e["success_primary"]["E1"]), met(e["success_narrow"]["E1"])],
        ["E2: stratavell's median prompt per agent request below compact's and compact + archive's", met(e["E2"]), "(same)"],
        ["E3: stratavell >= compact + archive - 1", met(e["success_primary"]["E3"]), met(e["success_narrow"]["E3"])],
        ["E4: at least one trim run ends with context lost", met(e["E4"]), "(same)"],
    ], fmt)
    T["tokens"] = table(
        ["strategy", "median of per-run mean prompt per agent request", "median prompt tokens per run", "prompt tokens, 15 runs", "completion tokens, 15 runs", "cached prompt tokens, 15 runs"],
        [[LABEL[s], per[s]["median_agent_prompt_mean"], per[s]["median_prompt_tokens_per_run"], per[s]["prompt_tokens"], per[s]["completion_tokens"], per[s]["cached_tokens"]] for s in STRATS], fmt)
    for fam, name in (("token_pairs", "token_pairs"), ("token_pairs_without_over_budget_states", "token_pairs_sensitivity")):
        rows = []
        for key, label in (("agent_prompt_mean", "mean prompt per agent request"), ("prompt_tokens", "prompt tokens per run")):
            for o in OTHERS:
                p = a[fam][key][o]
                st = p["sign_test"]
                rows.append([label, f"stratavell vs {LABEL[o]}", f"{p['median_difference']:+,.0f}", st["stratavell_lower"], st["stratavell_higher"], st["ties"], f"{st['p']:.3f}", f"{st['p_holm']:.3f}"])
        T[name] = table(["measure", "pair", "median difference", "states stratavell lower", "states stratavell higher", "ties", "sign test p", "Holm p"], rows, fmt)
    T["ended"] = table(
        ["strategy"] + [ENDING_LABEL[x] for x in ENDINGS] + ["tool calls", "memory calls"],
        [[LABEL[s]] + [per[s]["ended"].get(x, 0) for x in ENDINGS] + [per[s]["tool_calls"], per[s]["memory_calls"]] for s in STRATS], fmt)
    mem_tools = sorted({t for s in STRATS for t in per[s]["memory_calls_by_tool"]})
    T["memory"] = table(["strategy"] + mem_tools + ["memory calls in each tool-cap run"], [[LABEL[s]] + [per[s]["memory_calls_by_tool"].get(t, 0) for t in mem_tools] + [", ".join(str(x) for x in per[s]["memory_calls_in_tool_cap_runs"]) or "(none)"] for s in ("compact_archive", "stratavell")], fmt)
    kinds = [("page", "a page of an archived original"), ("search_hits", "search hits"), ("not_found", "not found"), ("repeat_notice", "repeat notice"), ("no_result", "no result"), ("other", "other")]
    T["memory_results"] = table(["strategy"] + [k[1] for k in kinds], [[LABEL[s]] + [per[s]["memory_results"].get(k[0], 0) for k in kinds] for s in ("compact_archive", "stratavell")], fmt)
    lim = a["over_budget_threshold"]
    mark = lambda v: f"{v:,}" + (" (over 5%)" if v > lim else " (over target)" if v > a["target"] else "")
    T["budget"] = table(["state"] + [LABEL[s] for s in STRATS], [[short(st)] + [mark(by[(st, s)]["agent_prompt_max"]) for s in STRATS] for st in states] + [["largest"] + [mark(per[s]["max_agent_prompt"]) for s in STRATS]], fmt)
    T["requests"] = table(
        ["strategy", "agent requests", "LOCA summary requests", "Stratavell summary requests", "of which sent after the run ended", "replies at the 16,384 cap", "failed requests", "minutes, 15 runs"],
        [[LABEL[s], per[s]["agent_requests"], per[s]["loca_summary_requests"], per[s]["stratavell_summary_requests"], per[s]["stratavell_summary_requests_after_run"], per[s]["capped_replies"], per[s]["failed_requests"], per[s]["minutes"]] for s in STRATS], fmt)
    T["cost"] = table(
        ["strategy", "prompt tokens", "of which cached", "completion tokens", "estimate at list price, USD", "estimate with cached tokens at the cache price, USD"],
        [[LABEL[s], per[s]["prompt_tokens"], per[s]["cached_tokens"], per[s]["completion_tokens"], f"{per[s]['cost_estimate_usd']:.2f}", f"{per[s]['cost_estimate_with_cache_usd']:.2f}"] for s in STRATS]
        + [["all", a["prompt_tokens_total"], a["cached_tokens_total"], a["completion_tokens_total"], f"{a['cost_estimate_total_usd']:.2f}", f"{a['cost_estimate_with_cache_total_usd']:.2f}"]], fmt)
    T["overshoot"] = table(
        ["run", "tool calls the model made", "counted by LOCA", "before its last reply", "in its last reply", "calls to a tool that does not exist", "checker success"],
        [[x["run"], x["tool_calls"], x["calls_counted_by_loca"], x["calls_before_last_reply"], x["last_reply_calls"], x["calls_to_unknown_tools"], "1" if x["success"] == 1.0 else "0"] for x in a["tool_cap_overshoot"]], fmt) if a["tool_cap_overshoot"] else "(none)"
    T["summary_budget"] = table(
        ["strategy", "largest LOCA summary request", "largest Stratavell summary request", "states over 5%"],
        [[LABEL[s], per[s]["max_loca_summary_prompt"] or "(none)", per[s]["max_stratavell_summary_prompt"] or "(none)", ", ".join(short(x) for x in per[s]["summary_over_budget_states"]) or "(none)"] for s in STRATS], fmt)
    ip = a.get("isolation_probe")
    if ip:
        fails = ip["failures_by_probe"]
        T["isolation_probe"] = table(["probe, from each session", "probes", "failed"], [[k, n, fails.get(k, 0)] for k, n in ip["probes"].items()], fmt)
        cfail = {"own event read": fails.get("control: own event read refused", 0), "own summary expanded": fails.get("control: own summary expand refused", 0), "own unique term found by search": ip["controls"].get("own unique term not in search reply", 0)}
        T["isolation_controls"] = table(["control, in the session's own memory", "probes", "failed"], [[k, n, cfail.get(k, 0)] for k, n in ip["controls"].items() if k in cfail], fmt)
    def assembly(r, k):
        x = r["stratavell_assembly"]
        return x["states"].get(k, 0) if x else "-"
    T["runs_requests"] = table(
        ["state", "strategy", "steps", "completion tokens", "completion per agent request", "LOCA summary requests", "Stratavell summary requests", "largest summary request", "agent replies at the cap", "summary replies at the cap"],
        [[short(r["state"]), LABEL[r["strategy"]], r["steps"], r["completion_tokens"], r["agent_completion_mean"], r["loca_summary_requests"], r["stratavell_summary_requests"], max(r["loca_summary_prompt_max"], r["stratavell_summary_prompt_max"]), r["capped_agent_replies"], r["capped_summary_replies"]] for r in runs], fmt)
    T["runs_compaction"] = table(
        ["state", "strategy", "LOCA trim events", "LOCA summary events", "Stratavell assemblies normal / soft / aggressive", "with summaries", "with masking", "load at start", "load at end"],
        [[short(r["state"]), LABEL[r["strategy"]], (r["loca_events"] or {}).get("trim", 0), (r["loca_events"] or {}).get("summary", 0),
          " / ".join(str(assembly(r, k)) for k in ("normal", "soft", "aggressive")) if r["stratavell_assembly"] else "-", r["stratavell_assembly"]["with_summaries"] if r["stratavell_assembly"] else "-", r["stratavell_assembly"]["with_masking"] if r["stratavell_assembly"] else "-", f"{r['load_start']:.2f}", f"{r['load_end']:.2f}"] for r in runs], fmt)
    v = a["voids"]
    rows = [[x, n] for x, n in sorted(v["by_reason"].items())]
    T["voids"] = table(["void reason", "attempts"], rows, fmt)
    T["counted_voids"] = table(["run", "reason", "model replies"], [[c["run"], c["void"], c["model_replies"]] for c in v["counted_runs"]], fmt) if v["counted_runs"] else "(none)"
    T["flags"] = table(["run", "tools whose arguments matched", "checker success"], [[f["run"], ", ".join(f["tools"]), f["success"]] for f in a["flagged_registered"]], fmt) if a["flagged_registered"] else "(none)"
    T["runs"] = table(
        ["state", "strategy", "run", "success", "ended", "tool calls", "memory calls", "agent requests", "mean prompt per request", "largest prompt", "prompt tokens", "minutes"],
        [[short(r["state"]), LABEL[r["strategy"]], r["run"], cell(r), ENDING_LABEL[r["ended"]], r["tool_calls"], r["memory_calls"] or 0, r["agent_requests"], r["agent_prompt_mean"], r["agent_prompt_max"], r["prompt_tokens"], r["minutes"]] for r in runs], fmt)
    return T


def value(a, path, places=None):
    x = a
    for k in path.split("."):
        x = x[int(k)] if isinstance(x, list) else x[k]
    return f"{x:,.{int(places)}f}" if places else num(x)


def render(template, a, runs, tries, fmt):
    text = open(template).read()
    keep, drop = ("md", "packet") if fmt == "md" else ("packet", "md")
    text = re.sub(r"<!--" + drop + r"-->.*?<!--/" + drop + r"-->\n?", "", text, flags=re.S)
    text = re.sub(r"<!--/?" + keep + r"-->\n?", "", text)
    T = tables(a, runs, tries, fmt)
    text = re.sub(r"\{\{t:([a-z_]+)\}\}", lambda m: T[m.group(1)], text)
    text = re.sub(r"\{\{v:([A-Za-z0-9_.]+)(?::(\d))?\}\}", lambda m: value(a, m.group(1), m.group(2)), text)
    left = re.findall(r"\{\{[^}]*\}\}", text)
    if left:
        raise SystemExit("unfilled: " + ", ".join(sorted(set(left))))
    proto = os.path.join(os.path.dirname(os.path.dirname(template)), "review", "protocol-agent-loca-v1.2.md")
    return text.replace("<!--protocol-->\n", open(proto).read())


def main():
    repo = sys.argv[1]
    res = os.path.join(repo, "bench", "results", "2026-11-28", "loca-v1.2")
    a, runs, tries = load(res)
    tpl = os.path.join(HERE, "results-template.md")
    open(os.path.join(repo, "docs", "agent-test.md"), "w").write(render(tpl, a, runs, tries, "md"))
    open(os.path.join(repo, "docs", "review", "AGENT_TEST_PACKET.txt"), "w").write(render(tpl, a, runs, tries, "txt"))


if __name__ == "__main__":
    main()
