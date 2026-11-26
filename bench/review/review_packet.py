import json, statistics as st, collections, sys, os, re

HEDGE = re.compile(r"(?i)\b(i )?(don't|do not) know\b|not (stated|shown|present|mentioned)")

root = sys.argv[1]
res = os.path.join(root, "bench/results/2026-11-26")
out = os.path.join(root, "REVIEW_PACKET.txt")


def load(path):
    return [json.loads(l) for l in open(path) if l.strip()]


def ms(xs, prec=2):
    if len(xs) == 1:
        return f"{xs[0]:.{prec}f}"
    return f"{st.mean(xs):.{prec}f} +/- {st.stdev(xs):.{prec}f}"


def table(headers, rows):
    w = [max(len(str(h)), *(len(str(r[i])) for r in rows)) for i, h in enumerate(headers)]
    line = "  ".join(str(h).ljust(w[i]) for i, h in enumerate(headers))
    sep = "  ".join("-" * w[i] for i in range(len(headers)))
    body = ["  ".join(str(r[i]).ljust(w[i]) for i in range(len(headers))) for r in rows]
    return "\n".join([line, sep] + body)


def suite(name):
    runs = load(os.path.join(res, name, "runs.jsonl"))
    by = collections.OrderedDict()
    for r in runs:
        by.setdefault(r["strategy"], []).append(r)
    return by


def main_table(by):
    rows = []
    for s, rs in by.items():
        g = lambda k: [r[k] for r in rs]
        rows.append([
            s,
            len(rs),
            ms([x / 1e6 for x in g("input_tokens")]),
            ms(g("input_cost_usd")),
            ms([x * 100 for x in g("cache_hit_rate")], 1),
            ms([x / 1000 for x in g("max_context")], 1),
            ms(g("overflow_turns"), 0),
            ms([x / 1000 for x in g("foreground_wait_ms")], 1),
            ms([x * 100 for x in g("probe_visible")], 1),
            ms([x * 100 for x in g("probe_recoverable")], 1),
            ms([x * 100 for x in g("standing_retention")], 1),
            ms([x / 1e6 for x in g("tool_tokens_sent")]),
            ms([(r["extra"].get("summarizer_input_tokens", 0) + r["extra"].get("summarizer_output_tokens", 0)) / 1e6 for r in rs]),
            ms(g("extra_primary_calls"), 0),
            ms(g("engine_ms_p95")),
            "n/a" if rs[0]["exact_recovery"] < 0 else ms([x * 100 for x in g("exact_recovery")], 1),
        ])
    return table(["strategy", "seeds", "input_Mtok", "input_cost_usd", "cache_hit_%", "max_ctx_k", "overflow_turns", "fg_wait_s", "visible_%", "recoverable_%", "standing_rules_%", "tool_Mtok", "summarizer_Mtok", "extra_primary_calls", "engine_p95_ms", "exact_recovery_%"], rows)


def kind_table(by):
    kinds = ["constraint", "decision", "rejected", "value", "symbol", "error"]
    rows = []
    for s, rs in by.items():
        tot = collections.defaultdict(lambda: [0, 0])
        for r in rs:
            for k, v in r["probe_by_kind"].items():
                tot[k][0] += v[0]
                tot[k][1] += v[1]
        rows.append([s] + [f"{tot[k][0]}/{tot[k][1]}" for k in kinds])
    return table(["strategy"] + kinds, rows)


def per_seed(by, keys):
    rows = []
    seeds = sorted({r["seed"] for rs in by.values() for r in rs})
    for s, rs in by.items():
        m = {r["seed"]: r for r in rs}
        rows.append([s] + [f"{m[x]['input_cost_usd']:.2f}" if x in m else "-" for x in seeds])
    return table(["strategy"] + [f"seed{x}_cost_usd" for x in seeds], rows)


def reader(path):
    recs = load(path)
    by = collections.OrderedDict()
    for r in recs:
        by.setdefault(r["strategy"], []).append(r)
    rows = []
    for s, rs in by.items():
        n = len(rs)
        c = sum(r["correct"] for r in rs)
        ov = sum(r["overflow"] for r in rs)
        vis = [r for r in rs if r["visible"]]
        hid = [r for r in rs if not r["visible"] and not r["overflow"]]
        empty = sum(1 for r in rs if not r["overflow"] and r["answer"] == "")
        strict = sum(1 for r in rs if r["correct"] and not HEDGE.search(r["answer"]))
        rows.append([s, n, c, f"{100 * c / n:.1f}", strict, f"{100 * strict / n:.1f}", ov, f"{sum(r['correct'] for r in vis)}/{len(vis)}", f"{sum(r['correct'] for r in hid)}/{len(hid)}", empty])
    kinds = collections.defaultdict(lambda: collections.defaultdict(lambda: [0, 0]))
    for r in recs:
        kinds[r["strategy"]][r["kind"]][1] += 1
        kinds[r["strategy"]][r["kind"]][0] += r["correct"]
    krows = []
    ks = ["constraint", "decision", "rejected", "value", "symbol", "error"]
    for s in by:
        krows.append([s] + [f"{kinds[s][k][0]}/{kinds[s][k][1]}" for k in ks])
    return table(["strategy", "questions", "correct", "accuracy_%", "correct_strict", "accuracy_strict_%", "overflowed", "correct_if_visible", "correct_if_not_visible", "empty_answers"], rows), table(["strategy"] + ks, krows)


def sample_answers(path, strategies, n=3):
    recs = load(path)
    lines = []
    for s in strategies:
        picked = [r for r in recs if r["strategy"] == s and not r["overflow"]][:n]
        for r in picked:
            ans = r["answer"].replace("\n", " ")
            if len(ans) > 160:
                ans = ans[:160] + "..."
            lines.append(f"  [{s}] turn {r['turn']} {r['fact']} visible={r['visible']} retrieval_added={r['retrieval_added']} correct={r['correct']}: {ans}")
    return "\n".join(lines)


m64 = suite("main64k")
m200 = suite("main200k")
abl = suite("ablation64k")
rx_main, rx_kind = reader(os.path.join(res, "reader-extractive/reader.regraded.jsonl"))
rh_main, rh_kind = reader(os.path.join(res, "reader-haiku-summaries/reader.regraded.jsonl"))
rv_main, rv_kind = reader(os.path.join(res, "reader-v1-superseded/reader.regraded.jsonl"))
raw64 = [r["raw_session_tokens"] for r in m64["G-hybrid"]]
raw200 = [r["raw_session_tokens"] for r in m200["G-hybrid"]]

text = open(sys.argv[2]).read()
text = text.replace("{{MAIN64}}", main_table(m64)).replace("{{KIND64}}", kind_table(m64)).replace("{{SEED64}}", per_seed(m64, None))
text = text.replace("{{MAIN200}}", main_table(m200)).replace("{{KIND200}}", kind_table(m200))
text = text.replace("{{ABL}}", main_table(abl)).replace("{{ABLKIND}}", kind_table(abl))
text = text.replace("{{RX}}", rx_main).replace("{{RXK}}", rx_kind).replace("{{RH}}", rh_main).replace("{{RHK}}", rh_kind).replace("{{RV}}", rv_main)
hedged = []
for name in ["reader-extractive", "reader-haiku-summaries"]:
    for r in load(os.path.join(res, name, "reader.regraded.jsonl")):
        if r["correct"] and HEDGE.search(r["answer"]):
            a = r["answer"].replace("\n", " ")
            hedged.append(f"  [{name} {r['strategy']}] turn {r['turn']} {r['fact']} visible={r['visible']}: {a[:240]}")
text = text.replace("{{HEDGED}}", "\n".join(hedged))
text = text.replace("{{SAMPLES}}", sample_answers(os.path.join(res, "reader-extractive/reader.regraded.jsonl"), ["B-conventional", "E-sync-summary", "G-hybrid"]))
text = text.replace("{{RAW64}}", f"{min(raw64)/1e6:.2f} to {max(raw64)/1e6:.2f}").replace("{{RAW200}}", f"{min(raw200)/1e6:.2f} to {max(raw200)/1e6:.2f}")
open(out, "w").write(text)
print(out, len(text))
