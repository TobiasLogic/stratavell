#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
work=$(mktemp -d)
port=${STRATAVELL_SMOKE_PORT:-7079}
pid=""
cleanup() {
  [ -n "$pid" ] && kill "$pid" 2>/dev/null && wait "$pid" 2>/dev/null
  rm -rf "$work"
}
trap cleanup EXIT
go build -o "$work/stratavell" ./cmd/stratavell
"$work/stratavell" serve --db "$work/pkg.db" --addr "127.0.0.1:$port" 2>/dev/null &
pid=$!
for _ in $(seq 1 100); do
  curl -sf "http://127.0.0.1:$port/v1/health" >/dev/null && break
  sleep 0.1
done

echo "== python package"
python3 -m venv "$work/build-venv"
"$work/build-venv/bin/pip" install -q build twine
(cd clients/python && rm -rf dist && "$work/build-venv/bin/python" -m build -q --outdir "$work/py-dist")
"$work/build-venv/bin/twine" check "$work"/py-dist/*
python3 -m venv "$work/use-venv"
"$work/use-venv/bin/pip" install -q "$work"/py-dist/*.whl
(cd "$work" && STRATAVELL_SMOKE_PORT=$port "$work/use-venv/bin/python" -I -c '
import os, stratavell
from stratavell import Client, text
a = Client(base_url="http://127.0.0.1:" + os.environ["STRATAVELL_SMOKE_PORT"]).session("pkg-py").agent("main")
a.append(text("user", "Never edit vendor/."))
r = a.assemble(fmt="anthropic", model="m", max_tokens=256)
assert r["tokens"] > 0 and "messages" in r["request"]
assert a.search("vendor")
print("python", stratavell.__version__, "ok")
')

echo "== typescript package"
(cd clients/typescript && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null && npm pack --pack-destination "$work" >/dev/null)
mkdir -p "$work/ts-use"
(cd "$work/ts-use" && npm init -y >/dev/null && npm install --no-audit --no-fund "$work"/stratavell-*.tgz >/dev/null)
cat > "$work/ts-use/t.mjs" <<JS
import { Client, text } from "stratavell";
const agent = await new Client("http://127.0.0.1:$port").agent("pkg-ts", "main");
await agent.append(text("user", "Keep Node 18 support."));
const req = await agent.assemble({ format: "openai", model: "m", max_tokens: 256 });
const { hits } = await agent.search("Node 18");
if (!(req.tokens > 0) || hits.length === 0) throw new Error("unexpected response");
console.log("typescript ok");
JS
(cd "$work/ts-use" && node t.mjs)
echo "all packages passed"
