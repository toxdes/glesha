"""Check AUR installation paths and immutable download references."""

import hashlib
import subprocess
import tempfile
import unittest
from dataclasses import replace
from pathlib import Path

from publish_aur import ROOT, binary_pkgbuild, load_config


class AurTests(unittest.TestCase):
    def test_binary_recipe_installs_into_usr(self):
        with tempfile.TemporaryDirectory(prefix="glesha aur ") as directory:
            root = Path(directory)
            dist = root / "dist"
            dist.mkdir()
            for arch in ("amd64", "arm64"):
                (dist / f"glesha_0.5.1_{arch}.tar.gz").write_bytes(arch.encode())
            config = replace(load_config(ROOT / "release.toml"), path=root / "release.toml")
            recipe = binary_pkgbuild(config, "0.5.1")
            self.assertNotRegex(recipe, r"@[A-Z0-9_]+@")
            self.assertIn("https://packages.toxdes.com/glesha/releases/glesha_0.5.1_amd64.tar.gz", recipe)
            self.assertIn(hashlib.sha256(b"amd64").hexdigest(), recipe)
            script = root / "PKGBUILD"
            script.write_text(recipe)
            source = root / "sources"
            files = {
                "bin/glesha": b"binary",
                "LICENSE": b"MIT",
                "share/doc/glesha/config-sample.toml": b"[archive]\n",
                "share/man/man1/glesha.1": b".TH GLESHA 1\n",
            }
            for name, data in files.items():
                path = source / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
            package = root / "package"
            subprocess.run([
                "bash", "-c", 'source "$1"; srcdir=$2; pkgdir=$3; package',
                "bash", str(script), str(source), str(package),
            ], check=True, capture_output=True)
            self.assertEqual((package / "usr/bin/glesha").read_bytes(), b"binary")
            self.assertTrue((package / "usr/share/licenses/glesha-bin/LICENSE").is_file())
            self.assertTrue((package / "usr/share/man/man1/glesha.1").is_file())
            self.assertFalse((package / "bin").exists())
            self.assertFalse((package / "share").exists())

    def test_missing_archive_fails_before_publication(self):
        with tempfile.TemporaryDirectory() as directory:
            config = replace(load_config(ROOT / "release.toml"), path=Path(directory) / "release.toml")
            with self.assertRaises(SystemExit):
                binary_pkgbuild(config, "0.5.1")


if __name__ == "__main__":
    unittest.main()
