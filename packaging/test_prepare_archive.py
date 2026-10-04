"""Check release layouts and platform-specific installation assets."""

import contextlib
import io
import tarfile
import tempfile
import unittest
import zipfile
from pathlib import Path

from prepare_archive import prepare_archive, prepare_platform_archive


class ArchiveTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.source = self.root / "glesha_0.5.1_amd64.tar.gz"
        files = {
            "usr/bin/glesha": b"linux executable",
            "usr/share/man/man1/glesha.1": b".TH GLESHA 1\n",
            "usr/share/doc/glesha/config-sample.toml": b"[archive]\n",
            "install.txt": b"Linux instructions",
            "LICENSE": b"MIT",
        }
        with tarfile.open(self.source, "w:gz") as bundle:
            for name, data in files.items():
                member = tarfile.TarInfo(name)
                member.size = len(data)
                member.mode = 0o755 if name.endswith("/glesha") else 0o644
                bundle.addfile(member, io.BytesIO(data))
        prepare_archive(self.source)
        (self.root / "glesha-macos").write_bytes(b"macos executable")
        (self.root / "glesha-windows.exe").write_bytes(b"windows executable")

    def test_platform_bundles_have_correct_binary_and_assets(self):
        for platform, arch in (("macos", "amd64"), ("macos", "arm64"), ("windows", "amd64")):
            with self.subTest(platform=platform, arch=arch), contextlib.chdir(self.root):
                archive = prepare_platform_archive(self.source, platform, arch, "0.5.1")
                if platform == "windows":
                    with zipfile.ZipFile(archive) as bundle:
                        self.assertNotIn("bin/glesha", bundle.namelist())
                        self.assertEqual(bundle.read("bin/glesha.exe"), b"windows executable")
                        self.assertIn(b"Windows x64", bundle.read("install.txt"))
                        self.assertEqual(bundle.read("LICENSE"), b"MIT")
                        self.assertIn("share/man/man1/glesha.1", bundle.namelist())
                else:
                    with tarfile.open(archive) as bundle:
                        self.assertEqual(bundle.extractfile("bin/glesha").read(), b"macos executable")
                        self.assertEqual(bundle.getmember("bin/glesha").mode, 0o755)
                        self.assertIn(b"macOS", bundle.extractfile("install.txt").read())
                        self.assertIn("share/man/man1/glesha.1", bundle.getnames())
                with tarfile.open(self.source) as bundle:
                    self.assertEqual(bundle.extractfile("bin/glesha").read(), b"linux executable")

    def test_failed_packaging_preserves_existing_archive(self):
        archive = self.root / "glesha_0.5.1_macos_amd64.tar.gz"
        archive.write_bytes(b"previous build")
        (self.root / "glesha-macos").unlink()
        with contextlib.chdir(self.root), self.assertRaises(FileNotFoundError):
            prepare_platform_archive(self.source, "macos", "amd64", "0.5.1")
        self.assertEqual(archive.read_bytes(), b"previous build")
        self.assertFalse(archive.with_name(archive.name + ".tmp").exists())

    def test_traversal_is_rejected(self):
        with tarfile.open(self.source, "w:gz") as bundle:
            member = tarfile.TarInfo("../escaped")
            member.size = 1
            bundle.addfile(member, io.BytesIO(b"x"))
        with contextlib.chdir(self.root), self.assertRaises(ValueError):
            prepare_platform_archive(self.source, "macos", "amd64", "0.5.1")
        self.assertFalse((self.root.parent / "escaped").exists())


if __name__ == "__main__":
    unittest.main()
