#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
want=${1:-}
want=${want#v}
go_v=$(sed -n 's/^var Version = "\(.*\)"$/\1/p' server/server.go)
py_v=$(sed -n 's/^version = "\(.*\)"$/\1/p' clients/python/pyproject.toml)
py_init=$(sed -n 's/^__version__ = "\(.*\)"$/\1/p' clients/python/src/stratavell/__init__.py)
ts_v=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' clients/typescript/package.json | head -1)
mcp_v=$(python3 -c 'import json; print(json.load(open("server.json"))["version"])')
mcp_img=$(python3 -c 'import json; print(json.load(open("server.json"))["packages"][0]["identifier"].rsplit(":", 1)[1])')
fail=0
for pair in "server/server.go:$go_v" "clients/python/pyproject.toml:$py_v" "clients/python/src/stratavell/__init__.py:$py_init" "clients/typescript/package.json:$ts_v" "server.json version:$mcp_v" "server.json image tag:$mcp_img"; do
  name=${pair%:*}
  v=${pair##*:}
  ref=${want:-$go_v}
  if [ "$v" != "$ref" ]; then
    echo "version mismatch: $name has $v, expected $ref"
    fail=1
  fi
done
[ "$fail" = 0 ] && echo "all versions are ${want:-$go_v}"
exit "$fail"
