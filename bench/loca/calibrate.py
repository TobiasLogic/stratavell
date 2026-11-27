import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
TARGET = 57000
OUTPUT = 16384
LOCA_SLOPE = 0.96
STRATAVELL_SLOPE = 0.95
RESET_FRACTION = 0.85


def stratavell_capacity(window):
    return window - OUTPUT - max(2000, int(0.02 * window)) - 300


def window_for(capacity):
    w = capacity + OUTPUT + 2300
    while stratavell_capacity(w) < capacity:
        w += 1
    return w


def budget(row):
    loca = round(row["loca_x0"] + (TARGET - row["loca_p0"]) / LOCA_SLOPE)
    passive = round(row["passive_x0"] + (TARGET - row["passive_p0"]) / LOCA_SLOPE)
    cap = round(row["stratavell_e0"] + (TARGET - row["stratavell_p0"]) / STRATAVELL_SLOPE)
    return {
        "loca_limit": loca,
        "loca_max_context_size": loca + OUTPUT,
        "loca_reset_size": round(RESET_FRACTION * loca),
        "passive_limit": passive,
        "passive_max_context_size": passive + OUTPUT,
        "passive_reset_size": round(RESET_FRACTION * passive),
        "stratavell_capacity": cap,
        "stratavell_window": window_for(cap),
    }


if __name__ == "__main__":
    src = sys.argv[1] if len(sys.argv) > 1 else os.path.join(HERE, "calib-v1.1.json")
    dst = sys.argv[2] if len(sys.argv) > 2 else os.path.join(HERE, "budget-v1.1.json")
    rows = json.load(open(src))
    out = {name: budget(row) for name, row in rows.items()}
    json.dump(out, open(dst, "w"), indent=1)
    for name, b in out.items():
        print(f"{name:32s} loca {b['loca_limit']:6d} passive {b['passive_limit']:6d} stratavell capacity {b['stratavell_capacity']:6d} window {b['stratavell_window']:6d}")
