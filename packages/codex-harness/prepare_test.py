#!/usr/bin/env python3

import json
import tempfile
import unittest
from pathlib import Path

from prepare import checked_bytes, normalize_lock, private_path, sha


class PreparationTests(unittest.TestCase):
    def test_only_workspace_versions_change(self):
        original = (
            b'[[package]]\nname = "native"\nversion = "0.0.0"\n'
            b'[[package]]\nname = "external"\nversion = "0.0.0"\nsource = "registry+example"\n'
        )
        expected = original.replace(b'version = "0.0.0"', b'version = "0.153.4"', 1)
        overlay = {
            "original_sha256": sha(original),
            "normalized_sha256": sha(expected),
            "workspace_packages": 1,
        }
        self.assertEqual(normalize_lock(original, overlay, "0.153.4"), expected)
        for field, value in (("original_sha256", "wrong"), ("normalized_sha256", "wrong"), ("workspace_packages", 2)):
            with self.subTest(field=field), self.assertRaises(ValueError):
                normalize_lock(original, {**overlay, field: value}, "0.153.4")

    def test_private_build_paths_reject_escape(self):
        root = Path.home() / ".parsar"
        root.mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(prefix="harness-path-test-", dir=root) as directory:
            base = Path(directory)
            self.assertEqual(private_path(base / "output"), base.resolve() / "output")
            (base / "escape").symlink_to(root.parent, target_is_directory=True)
            for value in ("relative", root, root / ".." / "outside", base / "escape" / "outside"):
                with self.subTest(value=value), self.assertRaises(ValueError):
                    private_path(value)

    def test_bounded_read_patch_has_separate_identity_and_native_scope(self):
        package = Path(__file__).resolve().parent
        manifest = json.loads((package / "source.json").read_text())
        patch = manifest["bounded_read_patch"]
        data = checked_bytes(package / patch["file"], patch["sha256"])
        with self.assertRaises(ValueError):
            checked_bytes(package / patch["file"], manifest["patch"]["sha256"])
        paths = [line.split()[2][2:] for line in data.decode().splitlines() if line.startswith("diff --git ")]
        self.assertEqual(set(paths), {
            "codex-rs/exec-server/src/bounded_file_read.rs",
            "codex-rs/exec-server/src/bounded_file_read_tests.rs",
            "codex-rs/exec-server/src/client.rs",
            "codex-rs/exec-server/src/environment.rs",
            "codex-rs/exec-server/src/lib.rs",
            "codex-rs/exec-server/src/remote_file_system.rs",
        })

    def test_shared_patch_identity_and_fixture_references(self):
        package = Path(__file__).resolve().parent
        root = package.parents[1]
        manifest = json.loads((package / "source.json").read_text())
        canonical = package / manifest["patch"]["file"]
        checked_bytes(canonical, manifest["patch"]["sha256"])
        with self.assertRaises(ValueError):
            checked_bytes(canonical, "wrong")
        for name in ("raw_manager", "raw_files", "retirement"):
            fixture = root / "services/agents-api/tests/native" / name / "source.json"
            reference = json.loads(fixture.read_text())["patch"]
            self.assertEqual((fixture.parent / reference["file"]).resolve(), canonical.resolve())
            self.assertEqual(reference["sha256"], manifest["patch"]["sha256"])


if __name__ == "__main__":
    unittest.main()
