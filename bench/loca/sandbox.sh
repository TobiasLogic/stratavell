#!/bin/bash
set -euo pipefail
L="${LOCA_ROOT:?set LOCA_ROOT to the LOCA working directory}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PY="${UV_PYTHON_DIR:-$HOME/.local/share/uv/python}"
mkdir -p "$L/home" "$L/work"
touch "$L/home/excel-mcp.log" "$L/venv/lib/python3.11/excel-mcp.log"
EXTRA=()
for v in STRATAVELL_URL STRATAVELL_WINDOW STRATAVELL_MODE; do
  if [ -n "${!v:-}" ]; then EXTRA+=(--setenv "$v" "${!v}"); fi
done
exec bwrap \
  --ro-bind /usr /usr --ro-bind /etc /etc \
  --symlink usr/bin /bin --symlink usr/bin /sbin --symlink usr/lib /lib --symlink usr/lib /lib64 \
  --ro-bind "$PY" "$PY" \
  --bind "$L" "$L" \
  --ro-bind "$L/venv" "$L/venv" --ro-bind "$L/xvenv" "$L/xvenv" \
  --bind "$L/home/excel-mcp.log" "$L/venv/lib/python3.11/excel-mcp.log" \
  --ro-bind "$HERE" "$HERE" \
  --proc /proc --dev /dev --tmpfs /tmp --tmpfs /run \
  --unshare-pid --unshare-ipc --unshare-uts --die-with-parent --new-session \
  --clearenv \
  --setenv HOME "$L/home" \
  --setenv PATH "$L/venv/bin:/usr/bin" \
  --setenv VIRTUAL_ENV "$L/venv" \
  --setenv UV_CACHE_DIR "$L/uv-cache" \
  --setenv UV_PROJECT_ENVIRONMENT "$L/venv" --setenv UV_NO_SYNC 1 --setenv UV_PYTHON_DOWNLOADS never \
  --setenv LOCA_UV_AS_PYTHON 1 --setenv LOCA_XVENV "$L/xvenv" --setenv LOCA_ROOT "$L" \
  --setenv npm_config_cache "$L/home/.npm" \
  --setenv LANG C.UTF-8 \
  --setenv LOCA_OPENAI_API_KEY sandbox-no-key \
  --setenv LOCA_OPENAI_BASE_URL "http://127.0.0.1:${FREEPROXY_PORT:-18080}/v1" \
  "${EXTRA[@]}" \
  --chdir "$L/upstream" \
  "$@"
