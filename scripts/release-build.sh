#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version=${1:?usage: scripts/release-build.sh <version without v> [outdir]}
out=${2:-dist}
epoch=$(git log -1 --format=%ct)
rm -rf "$out"
mkdir -p "$out"
./scripts/third-party-notices.sh "$out/THIRD_PARTY_NOTICES.md" >/dev/null
platforms="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
for p in $platforms; do
  goos=${p%/*}
  goarch=${p#*/}
  name="stratavell_${version}_${goos}_${goarch}"
  stage="$out/stage/$name"
  mkdir -p "$stage"
  ext=""
  [ "$goos" = windows ] && ext=".exe"
  for cmd in stratavell stratavell-bench; do
    CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -buildvcs=true \
      -ldflags "-s -w -buildid= -X github.com/TobiasLogic/stratavell/server.Version=${version}" \
      -o "$stage/${cmd}${ext}" "./cmd/${cmd}"
  done
  cp LICENSE NOTICE README.md CHANGELOG.md "$out/THIRD_PARTY_NOTICES.md" "$stage/"
  find "$stage" -exec touch -h -d "@${epoch}" {} +
  if [ "$goos" = windows ]; then
    python3 - "$out/stage" "$name" "$out/${name}.zip" "$epoch" <<'PY'
import os, sys, time, zipfile
root, name, dest, epoch = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
stamp = time.gmtime(epoch)[:6]
with zipfile.ZipFile(dest, "w") as z:
    for f in sorted(os.listdir(os.path.join(root, name))):
        info = zipfile.ZipInfo(name + "/" + f, stamp)
        info.external_attr = (0o755 if f.endswith(".exe") else 0o644) << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        with open(os.path.join(root, name, f), "rb") as fh:
            z.writestr(info, fh.read())
PY
  else
    tar --sort=name --mtime="@${epoch}" --owner=0 --group=0 --numeric-owner -C "$out/stage" -cf - "$name" | gzip -n -9 > "$out/${name}.tar.gz"
  fi
done
rm -rf "$out/stage"
(cd "$out" && sha256sum stratavell_* > SHA256SUMS)
cat "$out/SHA256SUMS"
