import json, statistics as st, collections, sys, os, re

root = sys.argv[1]
res = os.path.join(root, "bench/results/2026-11-26")
here = os.path.join(root, "bench/review")
HEDGE = re.compile(r"(?i)\b(i )?(don't|do not) know\b|not (stated|shown|present|mentioned)|cannot (confirm|determine)|can't (confirm|determine)")
LABEL = {
    "A-uncompressed": "A uncompressed",
    "B-conventional": "B conventional compaction (proxy)",
    "C-noa": "C noa (reimplementation)",
    "D-masking": "D observation masking only",
    "E-sync-summary": "E sync progressive summaries",
    "F-async-progressive": "F async progressive summaries",
    "G-hybrid": "G palimpsest",
    "B-conventional+pins": "B + pinned user messages",
    "B-conventional+archive": "B + passive archive with retrieval",
    "E-sync-summary+retrieval": "E + retrieval",
    "F-async-progressive+retrieval": "F + retrieval",
}


def load(path):
    return [json.loads(l) for l in open(path) if l.strip()]


def ms(xs, prec=2, sep=" ± "):
    if len(xs) == 1:
        return f"{xs[0]:.{prec}f}"
    return f"{st.mean(xs):.{prec}f}{sep}{st.stdev(xs):.{prec}f}"


def md(headers, rows):
    out = ["| " + " | ".join(headers) + " |", "|" + "---|" * len(headers)]
    out += ["| " + " | ".join(str(c) for c in r) + " |" for r in rows]
    return "\n".join(out)


def txt(headers, rows):
    w = [max(len(str(h)), *(len(str(r[i])) for r in rows)) for i, h in enumerate(headers)]
    lines = ["  ".join(str(h).ljust(w[i]) for i, h in enumerate(headers)), "  ".join("-" * x for x in w)]
    lines += ["  ".join(str(r[i]).ljust(w[i]) for i in range(len(headers))) for r in rows]
    return "\n".join(lines)


def suite(name):
    path = os.path.join(res, name, "runs.jsonl")
    if not os.path.exists(path):
        return None
    by = collections.OrderedDict()
    for r in load(path):
        by.setdefault(r["strategy"], []).append(r)
    return by


def main_rows(by, sep, label):
    rows = []
    for s, rs in by.items():
        g = lambda k: [r[k] for r in rs]
        rows.append([
            LABEL.get(s, s) if label else s,
            ms([x * 100 for x in g("completion_rate")], 1, sep),
            ms(g("input_cost_usd"), 2, sep),
            ms(g("total_cost_usd"), 2, sep),
            ms([x / 1e6 for x in g("input_tokens")], 2, sep),
            ms([x * 100 for x in g("cache_hit_rate")], 1, sep),
            ms(g("overflow_turns"), 0, sep),
            ms([x / 1000 for x in g("foreground_wait_ms")], 1, sep),
            ms([x * 100 for x in g("probe_visible")], 1, sep),
            ms([x * 100 for x in g("probe_grounded")], 1, sep),
            ms([x * 100 for x in g("probe_recoverable")], 1, sep),
            ms([x * 100 for x in g("standing_retention")], 1, sep),
            ms([x / 1e6 for x in g("tool_tokens_sent")], 2, sep),
            ms(g("extra_primary_calls"), 0, sep),
            ms(g("engine_ms_p95"), 2, sep),
        ])
    return rows


MAIN_H = ["strategy", "completion %", "input cost $", "total cost $", "input Mtok", "cache hit %", "overflow turns", "fg wait s", "visible %", "grounded %", "recoverable %", "standing rules %", "tool Mtok", "extra primary calls", "engine p95 ms"]


def acct_rows(by, label):
    rows = []
    for s, rs in by.items():
        r0 = lambda k: st.mean([r[k] for r in rs])
        rows.append([LABEL.get(s, s) if label else s, f"{r0('cache_read_tokens')/1e6:.2f}", f"{r0('cache_write_tokens')/1e6:.2f}", f"{r0('uncached_tokens')/1e6:.2f}", f"{r0('input_tokens')/1e6:.2f}", f"{r0('extra_primary_input_tokens')/1e6:.2f}", f"{r0('extra_primary_output_tokens')/1e3:.1f}", f"{r0('extra_output_cost_usd'):.2f}", f"{r0('summarizer_cost_usd_at_haiku'):.3f}"])
    return rows


ACCT_H = ["strategy", "cache read Mtok", "cache write Mtok", "uncached Mtok", "input Mtok", "of which in-band calls Mtok", "in-band output ktok", "in-band output cost $", "summarizer cost $ at Haiku 5.5"]


def kind_rows(by, label):
    kinds = ["constraint", "decision", "rejected", "value", "symbol", "error"]
    rows = []
    for s, rs in by.items():
        tot = collections.defaultdict(lambda: [0, 0])
        for r in rs:
            for k, v in r["probe_by_kind"].items():
                tot[k][0] += v[0]
                tot[k][1] += v[1]
        rows.append([LABEL.get(s, s) if label else s] + [f"{tot[k][0]}/{tot[k][1]}" for k in kinds])
    return rows


KIND_H = ["strategy", "constraint", "decision", "rejected", "value", "symbol", "current error"]


def savings(by):
    done = {s: rs for s, rs in by.items() if min(r["completion_rate"] for r in rs) == 1.0}
    if "G-hybrid" not in done:
        return "G did not complete every turn."
    g_in = st.mean([r["input_cost_usd"] for r in done["G-hybrid"]])
    g_tot = st.mean([r["total_cost_usd"] for r in done["G-hybrid"]])
    lines = []
    for s, rs in done.items():
        if s == "G-hybrid":
            continue
        o_in = st.mean([r["input_cost_usd"] for r in rs])
        o_tot = st.mean([r["total_cost_usd"] for r in rs])
        lines.append(f"- versus {LABEL.get(s, s)}: {100 * (1 - g_in / o_in):.1f}% lower input cost, {100 * (1 - g_tot / o_tot):.1f}% lower total cost")
    excluded = [LABEL.get(s, s) for s in by if s not in done]
    if excluded:
        lines.append(f"- not compared (did not complete every turn): {', '.join(excluded)}")
    return "\n".join(lines)


def reader(path, sep_md):
    if not os.path.exists(path):
        return None
    recs = load(path)
    by = collections.OrderedDict()
    for r in recs:
        by.setdefault(r["strategy"], []).append(r)
    rows = []
    for s, rs in by.items():
        n = len(rs)
        lenient = sum(r["correct"] for r in rs)
        strict = sum(1 for r in rs if r["correct"] and not HEDGE.search(r["answer"]))
        ev = sum(1 for r in rs if r.get("evidence_available", r.get("visible") or r.get("retrieval_added")))
        ov = sum(r["overflow"] for r in rs)
        empty = sum(1 for r in rs if not r["overflow"] and r["answer"] == "")
        rows.append([LABEL.get(s, s) if sep_md else s, n, f"{strict} ({100 * strict / n:.1f}%)", f"{lenient} ({100 * lenient / n:.1f}%)", ev, ov, empty])
    return rows


READ_H = ["strategy", "questions", "grounded answer (strict)", "value recovered (lenient)", "evidence available", "overflowed", "empty answers"]


def hedged(paths):
    out = []
    for name, p in paths:
        if not os.path.exists(p):
            continue
        for r in load(p):
            if r["correct"] and HEDGE.search(r["answer"]):
                out.append(f"[{name}, {r['strategy']}, turn {r['turn']}, {r['fact']}] " + r["answer"].replace("\n", " ")[:240])
    return out


def build(template, out, fmt):
    md_mode = fmt == "md"
    T = md if md_mode else txt
    sep = " ± " if md_mode else " +/- "
    text = open(template).read()
    for name in ["main64k", "main200k", "ablation64k", "matched64k"]:
        by = suite(name)
        if by is None:
            continue
        text = text.replace("{{" + name + ".main}}", T(MAIN_H, main_rows(by, sep, md_mode)))
        text = text.replace("{{" + name + ".acct}}", T(ACCT_H, acct_rows(by, md_mode)))
        text = text.replace("{{" + name + ".kind}}", T(KIND_H, kind_rows(by, md_mode)))
        text = text.replace("{{" + name + ".savings}}", savings(by))
        g = by.get("G-hybrid")
        if g:
            raw = [r["raw_session_tokens"] for r in g]
            text = text.replace("{{" + name + ".raw}}", f"{min(raw) / 1e6:.2f} to {max(raw) / 1e6:.2f}")
    for key, rel in [("reader.v2", "reader-extractive/reader.regraded.jsonl"), ("reader.haiku", "reader-haiku-summaries/reader.regraded.jsonl"), ("reader.v1", "reader-v1-superseded/reader.regraded.jsonl"), ("reader.v3", "reader-v3/reader.jsonl")]:
        rows = reader(os.path.join(res, rel), md_mode)
        text = text.replace("{{" + key + "}}", T(READ_H, rows) if rows else "(not run)")
    hz = hedged([("v2 extractive", os.path.join(res, "reader-extractive/reader.regraded.jsonl")), ("Haiku summaries", os.path.join(res, "reader-haiku-summaries/reader.regraded.jsonl")), ("v3", os.path.join(res, "reader-v3/reader.jsonl"))])
    text = text.replace("{{spend}}", sys.argv[2] if len(sys.argv) > 2 else "unknown")
    text = text.replace("{{hedged}}", "\n".join(("- " if md_mode else "  ") + h for h in hz) if hz else "(none)")
    left = re.findall(r"\{\{[^}]+\}\}", text)
    if left:
        raise SystemExit(f"unfilled placeholders in {template}: {sorted(set(left))}")
    open(out, "w").write(text)
    print(out, len(text))


build(os.path.join(here, "benchmarks-template.md"), os.path.join(root, "docs/benchmarks.md"), "md")
build(os.path.join(here, "template.txt"), os.path.join(root, "REVIEW_PACKET.txt"), "txt")
