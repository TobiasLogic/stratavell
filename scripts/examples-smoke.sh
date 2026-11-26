#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
sock=$(mktemp -u /tmp/stratavell-smoke-XXXXXX.sock)
port=${STRATAVELL_SMOKE_PORT:-7078}
pid=""
cleanup() {
  [ -n "$pid" ] && kill "$pid" 2>/dev/null && wait "$pid" 2>/dev/null
  rm -rf "$work"
}
trap cleanup EXIT
go build -o "$work/stratavell" ./cmd/stratavell
"$work/stratavell" serve --db "$work/demo.db" --addr "127.0.0.1:$port" --socket "$sock" &
pid=$!
for _ in $(seq 1 100); do
  curl -sf "http://127.0.0.1:$port/v1/health" >/dev/null && break
  sleep 0.1
done
echo "== go (in-process SDK)"
go run ./examples/go
echo "== python (Unix socket)"
python3 examples/python/example.py "$sock"
echo "== typescript (TCP)"
(cd examples/typescript && STRATAVELL_URL="http://127.0.0.1:$port" node --experimental-strip-types example.ts)
echo "== rust (TCP)"
(cd examples/rust && STRATAVELL_URL="http://127.0.0.1:$port" cargo run --quiet)
echo "all examples passed"
