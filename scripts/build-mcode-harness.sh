#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
package="$repo_root/packages/mcode-harness"
source="${MCODE_NATIVE_SOURCE:?Set MCODE_NATIVE_SOURCE to the pinned upstream checkout}"
if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  printf 'Build the MiniMax Code Runtime artifact on Linux x86_64\n' >&2
  exit 1
fi
root="$HOME/.parsar/build"
mkdir -p "$root"
context="$(mktemp -d "$root/mcode-build.XXXXXX")"
trap 'rm -rf "$context"' EXIT
revision="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["revision"])' "$package/source.json")"
if [[ "$(git -C "$source" rev-parse HEAD)" != "$revision" ]]; then
  printf 'MiniMax Code source revision does not match source.json\n' >&2
  exit 1
fi
mkdir "$context/upstream"
git -C "$source" archive "$revision" | tar -x -C "$context/upstream"
printf '%s\n' "$revision" > "$context/upstream/.parsar-source-revision"
cp "$package/"*.mjs "$package/"*.ts "$package/"*.json "$context/"
(
  cd "$context"
  npm ci --no-audit --no-fund
  MCODE_SOURCE="$context/upstream" node build.mjs
  MCODE_SOURCE="$context/upstream" node build-sandbox.mjs
  node dist/worker.mjs /workspace --describe > dist/tools.json
  npm prune --omit=dev --no-audit --no-fund
)
artifact="$context/artifact"
mkdir "$artifact"
cp -R "$context/dist" "$context/node_modules" "$artifact/"
cp "$package/launch.mjs" "$package/bridge.mjs" "$package/check.mjs" "$package/tool-executor.mjs" "$package/source.json" "$artifact/"
cp "$context/upstream/LICENSE" "$artifact/UPSTREAM_LICENSE"
cp "$context/upstream/third_party/sandbox-runtime/LICENSE" "$artifact/SANDBOX_LICENSE"
cp "$context/upstream/third_party/pi-mono/LICENSE" "$artifact/PI_LICENSE"
python3 - "$artifact" "$package" <<'PY'
import hashlib,json,pathlib,sys
artifact,package=map(pathlib.Path,sys.argv[1:])
pin=json.loads((package/'source.json').read_text())
pin['files']={str(p.relative_to(artifact)):hashlib.sha256(p.read_bytes()).hexdigest()
              for p in sorted(artifact.rglob('*')) if p.is_file()}
(artifact/'provenance.json').write_text(json.dumps(pin,indent=2)+'\n')
PY
destination="$root/mcode-harness-$(date +%Y%m%d%H%M%S)"
mv "$artifact" "$destination"
printf 'MiniMax Code Runtime artifact: %s\n' "$destination"
