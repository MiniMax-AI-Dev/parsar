#!/usr/bin/env python3
"""Export and prepare the pinned private harness build without resolving dependencies."""

import argparse
import hashlib
import json
import subprocess
from pathlib import Path


def sha(data):
    return hashlib.sha256(data).hexdigest()


def private_path(value):
    path = Path(value).expanduser()
    if not path.is_absolute():
        raise ValueError("harness paths must be absolute")
    path = path.resolve()
    root = (Path.home() / ".parsar").resolve()
    if path == root or not path.is_relative_to(root):
        raise ValueError("harness state must be below ~/.parsar")
    return path


def checked_bytes(path, expected):
    data = path.read_bytes()
    if sha(data) != expected:
        raise ValueError("source identity differs: " + str(path))
    return data


def load_manifest():
    package = Path(__file__).resolve().parent
    raw = (package / "source.json").read_bytes()
    manifest = json.loads(raw)
    for key in ("patch", "bounded_read_patch", "build_overlay"):
        checked_bytes(package / manifest[key]["file"], manifest[key]["sha256"])
    sources = sorted((package / manifest["source_directory"]).rglob("*.rs"))
    if not sources or not (package / manifest["source_directory"] / "main.rs").is_file():
        raise ValueError("harness Rust sources are missing")
    return package, raw, manifest, sources


def normalize_lock(original, overlay, version):
    if sha(original) != overlay["original_sha256"]:
        raise ValueError("unexpected upstream Cargo.lock")
    parts = original.split(b"[[package]]")
    changed = 0
    for index, part in enumerate(parts[1:], 1):
        if b"\nsource = " not in part and b'\nversion = "0.0.0"\n' in part:
            parts[index] = part.replace(
                b'\nversion = "0.0.0"\n', ('\nversion = "' + version + '"\n').encode(), 1
            )
            changed += 1
    normalized = b"[[package]]".join(parts)
    if changed != overlay["workspace_packages"] or sha(normalized) != overlay["normalized_sha256"]:
        raise ValueError("workspace-only lock normalization differs")
    return normalized


def prepare(source, output):
    package, raw, manifest, sources = load_manifest()
    source = Path(source).expanduser()
    if not source.is_absolute():
        raise ValueError("native Git source must be absolute")
    output = private_path(output)
    revision = manifest["revision"]
    resolved = subprocess.check_output(
        ["git", "-C", str(source), "rev-parse", revision + "^{commit}"], text=True
    ).strip()
    if resolved != revision:
        raise ValueError("native source revision differs")
    output.mkdir(parents=True, exist_ok=False)
    with subprocess.Popen(
        ["git", "-C", str(source), "archive", "--format=tar", revision], stdout=subprocess.PIPE
    ) as archive:
        try:
            subprocess.run(["tar", "-xf", "-", "-C", str(output)], stdin=archive.stdout, check=True)
        finally:
            archive.stdout.close()
        if archive.wait() != 0:
            raise RuntimeError("native source export failed")
    lock = output / "codex-rs/Cargo.lock"
    lock.write_bytes(normalize_lock(lock.read_bytes(), manifest["cargo_lock"], manifest["native_version"]))
    cargo_manifest = output / "codex-rs/app-server/Cargo.toml"
    checked_bytes(cargo_manifest, manifest["build_overlay"]["original_manifest_sha256"])
    for key in ("patch", "bounded_read_patch", "build_overlay"):
        patch = package / manifest[key]["file"]
        subprocess.run(["git", "apply", "--check", str(patch)], cwd=output, check=True)
        subprocess.run(["git", "apply", str(patch)], cwd=output, check=True)
    checked_bytes(cargo_manifest, manifest["build_overlay"]["prepared_manifest_sha256"])
    source_hashes = {}
    for source_file in sources:
        relative = source_file.relative_to(package / manifest["source_directory"])
        data = source_file.read_bytes()
        target = output / manifest["target_directory"] / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        source_hashes[str(relative)] = sha(data)
    record = {
        "revision": revision,
        "manifest_sha256": sha(raw),
        "patch_sha256": manifest["patch"]["sha256"],
        "bounded_read_patch_sha256": manifest["bounded_read_patch"]["sha256"],
        "build_overlay_sha256": manifest["build_overlay"]["sha256"],
        "prepared_manifest_sha256": sha(cargo_manifest.read_bytes()),
        "prepared_lock_sha256": sha(lock.read_bytes()),
        "rust_toolchain": manifest["rust_toolchain"],
        "sources": source_hashes,
    }
    (output / "preparation.json").write_text(json.dumps(record, indent=2) + "\n")
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, help="existing upstream Git checkout")
    parser.add_argument("--output", type=Path, help="new export below ~/.parsar")
    parser.add_argument("--check", action="store_true", help="verify local manifest and patch identities")
    parser.add_argument("--check-path", action="append", default=[], help="verify an isolated build path")
    args = parser.parse_args()
    for value in args.check_path:
        private_path(value)
    if args.check:
        load_manifest()
    elif args.source is not None and args.output is not None:
        print(prepare(args.source, args.output))
    elif not args.check_path:
        parser.error("provide --source and --output, or --check")


if __name__ == "__main__":
    main()
