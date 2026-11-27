#!/bin/bash
set -uo pipefail
L="${LOCA_ROOT:?set LOCA_ROOT to the LOCA working directory}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG="$L/logs/freeproxy.jsonl"
name=$1
shift
out="$L/work/runs/$name"
mkdir -p "$out" "$L/logs"
touch "$LOG"
start_line=$(wc -l < "$LOG")
{
  echo "name=$name"
  echo "start=$(date -u +%FT%TZ)"
  echo "load_start=$(cut -d" " -f1-3 /proc/loadavg)"
  echo "stratavell_url=${STRATAVELL_URL:-}"
  echo "stratavell_window=${STRATAVELL_WINDOW:-}"
  echo "stratavell_mode=${STRATAVELL_MODE:-}"
  printf 'args='; printf '%q ' "$@"; echo
} > "$out/RUN"
"$HERE/sandbox.sh" "$L/venv/bin/python" -W ignore "$HERE/preflight.py" --run-config "$@" > "$out/preflight.txt" 2>&1
pre=$?
code=99
if [ "$pre" -eq 0 ]; then
  timeout "${RUN_TIMEOUT:-10800}" "$HERE/sandbox.sh" "$L/venv/bin/loca" run --max-workers 1 -o "$out/loca" "$@" > "$out/stdout.log" 2>&1
  code=$?
fi
end_line=$(wc -l < "$LOG")
sed -n "$((start_line + 1)),${end_line}p" "$LOG" > "$out/requests.jsonl"
{
  echo "end=$(date -u +%FT%TZ)"
  echo "load_end=$(cut -d" " -f1-3 /proc/loadavg)"
  echo "preflight=$pre"
  echo "exit=$code"
} >> "$out/RUN"
echo "$name preflight $pre exit $code, $(wc -l < "$out/requests.jsonl") proxied requests"
