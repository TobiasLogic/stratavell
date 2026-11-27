#!/bin/bash
set -euo pipefail
L="${LOCA_ROOT:?set LOCA_ROOT to an empty working directory}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
COMMIT=8b6fac4
mkdir -p "$L/logs" "$L/work" "$L/home" "$L/xvenv"
if [ ! -d "$L/upstream/.git" ]; then
  git clone -q https://github.com/hkust-nlp/LOCA-bench.git "$L/upstream"
fi
git -C "$L/upstream" checkout -q "$COMMIT"
git -C "$L/upstream" apply "$HERE/loca.patch"
export UV_CACHE_DIR="${UV_CACHE_DIR:-$L/uv-cache}"
uv venv -q --python 3.11 "$L/venv"
VIRTUAL_ENV="$L/venv" uv pip install -q -r "$HERE/requirements-lock.txt"
VIRTUAL_ENV="$L/venv" uv pip install -q --no-deps -e "$L/upstream" "$REPO/clients/python"
uv venv -q --python 3.11 "$L/xvenv/cli-mcp-server"
VIRTUAL_ENV="$L/xvenv/cli-mcp-server" uv pip install -q -r "$HERE/requirements-cli-mcp-server.txt"
uv venv -q --python 3.12 "$L/xvenv/pdf-tools-mcp"
VIRTUAL_ENV="$L/xvenv/pdf-tools-mcp" uv pip install -q -r "$HERE/requirements-pdf-tools-mcp.txt"
TIKTOKEN_CACHE_DIR="$L/tiktoken-cache" "$L/venv/bin/python" -c "import tiktoken; tiktoken.get_encoding(\"cl100k_base\")"
echo "LOCA-bench $COMMIT is set up in $L"
