#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_root="${PARSAR_HOME:-$HOME/.parsar}"
output_dir="${AGENTS_API_RELEASE_DIR:-$runtime_root/build/agents-api-release}"
export GOCACHE="${GOCACHE:-$runtime_root/cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$runtime_root/cache/go-mod}"
python3 - "$HOME/.parsar" "$runtime_root" "$output_dir" "$GOCACHE" "$GOMODCACHE" <<'PY'
import pathlib
import sys

if sys.version_info < (3, 9):
    sys.exit("Agents API releases require Python 3.9 or newer")
base = pathlib.Path(sys.argv[1]).resolve()
for value in sys.argv[2:]:
    path = pathlib.Path(value)
    if not path.is_absolute() or not path.resolve().is_relative_to(base):
        sys.exit("Agents API release directories must be absolute and under ~/.parsar")
PY

require_clean_source() {
  local source_status
  source_status="$(git -C "$repo_root" status --porcelain --untracked-files=all)"
  if [[ -n "$source_status" ]]; then
    printf 'Agents API releases require a clean, committed source tree\n' >&2
    exit 1
  fi
}
require_clean_source
source_revision="$(git -C "$repo_root" rev-parse HEAD)"
source_tree="$(git -C "$repo_root" rev-parse "$source_revision^{tree}")"
source_epoch="$(git -C "$repo_root" show -s --format=%ct "$source_revision")"
archive_name="agents-api-$source_revision-linux-amd64.tar.gz"

mkdir -p "$output_dir"
release_context="$(mktemp -d "$output_dir/.staging.XXXXXX")"
trap 'rm -rf "$release_context"' EXIT
mkdir -p "$release_context/source" "$release_context/package/bin" "$release_context/go-tmp"
export GOTMPDIR="$release_context/go-tmp"
# Build committed bytes so ignored files or concurrent edits cannot change provenance.
git -C "$repo_root" archive "$source_revision" | tar -C "$release_context/source" -xf -
export GOOS=linux GOARCH=amd64 GOAMD64=v1 GOTOOLCHAIN=local
go_version="$(go env GOVERSION)"
required_go="$(awk '$1 == "go" { print "go" $2; exit }' "$release_context/source/go.mod")"
if [[ "$go_version" != "$required_go" ]]; then
  printf 'Agents API release requires %s; found %s\n' "$required_go" "$go_version" >&2
  exit 1
fi
AGENTS_API_BUILD_DIR="$release_context/package/bin" \
  "$release_context/source/scripts/build-agents-api.sh"
require_clean_source
if [[ "$(git -C "$repo_root" rev-parse HEAD)" != "$source_revision" ]]; then
  printf 'Agents API source changed during release build\n' >&2
  exit 1
fi

python3 - "$release_context" "$source_revision" "$source_tree" "$source_epoch" "$go_version" "$archive_name" <<'PY'
import gzip
import hashlib
import json
import pathlib
import sys
import tarfile

root = pathlib.Path(sys.argv[1])
revision, tree, epoch, go_version, archive_name = sys.argv[2:]
source, package = root / "source", root / "package"
binaries = ["agents-api", "agents-api-migrate", "agents-api-device", "agents-api-environment-key"]


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


readme = (source / "services/agents-api/RELEASE.md").read_text(encoding="utf-8")
if "@SOURCE_REVISION@" not in readme:
    sys.exit("Agents API RELEASE.md must link to @SOURCE_REVISION@")
(package / "README.md").write_text(readme.replace("@SOURCE_REVISION@", revision), encoding="utf-8")
(package / "LICENSE").write_bytes((source / "LICENSE").read_bytes())
manifest = {
    "artifact": "agents-api",
    "source": {"commit": revision, "tree": tree, "commit_timestamp": int(epoch)},
    "platform": {"os": "linux", "architecture": "amd64", "goamd64": "v1"},
    "go_version": go_version,
    "upstream_protocol": json.loads((source / "contracts/agents-api/upstream.json").read_text(encoding="utf-8")),
    "binaries": {"bin/" + name: {"sha256": sha256(package / "bin" / name)} for name in binaries},
}
(package / "manifest.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
members = sorted(["bin/" + name for name in binaries] + ["LICENSE", "README.md", "manifest.json"])
(package / "SHA256SUMS").write_text("".join(sha256(package / name) + "  " + name + "\n" for name in members), encoding="utf-8")
members = sorted(members + ["SHA256SUMS"])
prefix = archive_name.removesuffix(".tar.gz")
archive = root / archive_name
with archive.open("wb") as output:
    with gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=0, compresslevel=9) as compressed:
        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as bundle:
            for name in members:
                path = package / name
                info = tarfile.TarInfo(prefix + "/" + name)
                info.size = path.stat().st_size
                info.mode = 0o755 if name.startswith("bin/") else 0o644
                info.mtime = int(epoch)
                with path.open("rb") as contents:
                    bundle.addfile(info, contents)
(root / (archive_name + ".sha256")).write_text(sha256(archive) + "  " + archive_name + "\n", encoding="utf-8")
PY

mv -f "$release_context/$archive_name" "$release_context/$archive_name.sha256" "$output_dir/"
printf 'Standalone Agents API release: %s/%s\n' "$output_dir" "$archive_name"
