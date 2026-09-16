#!/usr/bin/env python3
"""Prepare an exact-pin, explicitly patched native qualification source tree."""

import argparse
import hashlib
import json
import subprocess
from pathlib import Path


def sha(data):
    return hashlib.sha256(data).hexdigest()


def prepare(source, output, manifest_file=None):
    manifest_file = manifest_file or Path(__file__).resolve().with_name("source.json")
    here = manifest_file.parent
    manifest_bytes = manifest_file.read_bytes()
    manifest = json.loads(manifest_bytes)
    patch = here / manifest["patch"]["file"]
    patch_bytes = patch.read_bytes()
    if sha(patch_bytes) != manifest["patch"]["sha256"]:
        raise ValueError("native patch does not match source.json")
    patches = [patch]
    fixture_patch = manifest.get("fixture_patch")
    if fixture_patch:
        fixture = here / fixture_patch["file"]
        if sha(fixture.read_bytes()) != fixture_patch["sha256"]:
            raise ValueError("fixture dependency patch does not match source.json")
        patches.append(fixture)
    revision = manifest["revision"]
    resolved = subprocess.check_output(
        ["git", "-C", str(source), "rev-parse", revision + "^{commit}"], text=True
    ).strip()
    if resolved != revision:
        raise ValueError("native source revision differs")
    runtime = (Path.home() / ".parsar").resolve()
    if not output.is_relative_to(runtime) or output == runtime:
        raise ValueError("output must be a new directory below ~/.parsar")
    output.mkdir(parents=True, exist_ok=False)
    # Export the named commit, never the caller's potentially modified checkout.
    with subprocess.Popen(
        ["git", "-C", str(source), "archive", "--format=tar", revision],
        stdout=subprocess.PIPE,
    ) as archive:
        try:
            subprocess.run(["tar", "-xf", "-", "-C", str(output)], stdin=archive.stdout, check=True)
        finally:
            archive.stdout.close()
        if archive.wait() != 0:
            raise RuntimeError("native source export failed")

    lock = output / "codex-rs/Cargo.lock"
    original = lock.read_bytes()
    overlay = manifest["cargo_lock"]
    if sha(original) != overlay["original_sha256"]:
        raise ValueError("unexpected upstream Cargo.lock")
    parts = original.split(b"[[package]]")
    changed = 0
    for index, part in enumerate(parts[1:], 1):
        if b'\nsource = ' not in part and b'\nversion = "0.0.0"\n' in part:
            parts[index] = part.replace(b'\nversion = "0.0.0"\n', b'\nversion = "0.153.4"\n', 1)
            changed += 1
    normalized = b"[[package]]".join(parts)
    if changed != overlay["workspace_packages"] or sha(normalized) != overlay["normalized_sha256"]:
        raise ValueError("workspace-only lock normalization differs")
    lock.write_bytes(normalized)
    for item in patches:
        subprocess.run(["git", "apply", "--check", str(item)], cwd=output, check=True)
        subprocess.run(["git", "apply", str(item)], cwd=output, check=True)
    if fixture_patch:
        for name, expected in fixture_patch["prepared_files"].items():
            if sha((output / name).read_bytes()) != expected:
                raise ValueError("fixture dependency source differs: " + name)

    fixtures = {}
    for item in manifest["fixtures"]:
        data = (here / item["source"]).read_bytes()
        target = output / item["target"]
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        fixtures[item["target"]] = sha(data)
    record = {
        "revision": revision,
        "manifest_sha256": sha(manifest_bytes),
        "patch_sha256": sha(patch_bytes),
        "cargo_lock": overlay,
        "fixtures": fixtures,
    }
    if fixture_patch:
        record["fixture_patch"] = fixture_patch
    (output / "preparation.json").write_text(json.dumps(record, indent=2) + "\n")
    print(output)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, type=Path, help="existing native Git checkout")
    parser.add_argument("--output", required=True, type=Path, help="new directory under ~/.parsar")
    parser.add_argument("--manifest", type=Path, help="explicit native qualification manifest")
    args = parser.parse_args()
    source, output = args.source.expanduser(), args.output.expanduser()
    if not source.is_absolute() or not output.is_absolute():
        parser.error("source and output must be absolute paths")
    manifest_file = args.manifest.expanduser().resolve() if args.manifest else None
    prepare(source.resolve(), output.resolve(), manifest_file)
