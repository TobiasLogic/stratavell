import json
import sys

TIMING = {
    "engine_ms_p50", "engine_ms_p95", "wall_seconds", "append_p50_ms", "append_p95_ms",
    "assemble_p50_ms", "assemble_p95_ms", "background_summarizer_ms", "db_bytes",
    "retrieval_p50_ms", "retrieval_p95_ms", "retrieval_p99_ms", "search_p50_ms", "search_p95_ms",
}


def flat(d, prefix=""):
    out = {}
    for k, v in d.items():
        if isinstance(v, dict):
            out.update(flat(v, prefix + k + "."))
        else:
            out[prefix + k] = v
    return out


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: python3 scripts/compare-runs.py <reference runs.jsonl> <new runs.jsonl>")
    ref = {}
    for line in open(sys.argv[1]):
        if line.strip():
            r = json.loads(line)
            ref[(r["strategy"], r["seed"])] = flat(r)
    total = 0
    for line in open(sys.argv[2]):
        if not line.strip():
            continue
        n = json.loads(line)
        key = (n["strategy"], n["seed"])
        if key not in ref:
            sys.exit(f"{key} is not in the reference file")
        a, b = ref[key], flat(n)
        bad = [k for k in sorted(set(a) | set(b)) if k.split(".")[-1] not in TIMING and a.get(k) != b.get(k)]
        checked = len([k for k in a if k.split(".")[-1] not in TIMING])
        total += len(bad)
        print(f"{key[0]:32s} seed {key[1]}: {checked} deterministic fields, {len(bad)} differ")
        for k in bad[:10]:
            print(f"    {k}: {a.get(k)} -> {b.get(k)}")
    if total:
        sys.exit(f"{total} deterministic fields differ")
    print("all deterministic fields match")


main()
