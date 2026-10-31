"""Exercise Yesb's archive builder and AUR recipe with Glesha's assets."""

import hashlib
import os
import runpy
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
from dataclasses import replace
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "yesb"))

import release_aur
from prepare_archive import validate_linux_archive
from release_lib import load_config


class AurTests(unittest.TestCase):
    def test_standard_archive_installs_into_usr(self):
        for arch, carch in (("amd64", "x86_64"), ("arm64", "aarch64")):
            with self.subTest(arch=arch), tempfile.TemporaryDirectory(prefix="glesha aur ") as directory:
                root = Path(directory)
                dist = root / "dist"
                dist.mkdir()
                config = load_config(ROOT / "release.toml")
                shutil.copyfile(ROOT / "release.toml", root / "release.toml")
                for asset in config.section("package")["archive"]["assets"]:
                    destination = root / asset["source"]
                    destination.parent.mkdir(parents=True, exist_ok=True)
                    shutil.copyfile(ROOT / asset["source"], destination)
                (root / "glesha").write_bytes(b"binary")
                environment = {
                    "PACKAGE_ROOT": str(root), "OUTPUT_DIR": str(dist),
                    "TARGETARCH": arch, "VERSION": "0.5.1", "INCLUDE_APPIMAGE": "0",
                }
                with patch.dict(os.environ, environment):
                    package = runpy.run_path(str(ROOT / "yesb/package.py"))

                # yesb normally stages at /pkg-tar inside its build container
                def staging_path(value):
                    return root / "staging" if str(value) == "/pkg-tar" else Path(value)

                with patch.dict(package["build_tar"].__globals__, Path=staging_path):
                    package["build_tar"]()
                archive = dist / f"glesha_0.5.1_{arch}.tar.gz"
                validate_linux_archive(archive)
                with tarfile.open(archive) as bundle:
                    names = {str(Path(member.name)) for member in bundle}
                self.assertTrue(all(name in {".", "usr"} or name.startswith("usr/") for name in names))
                self.assertNotIn("bin", names)

                config = replace(config, path=root / "release.toml")
                digest = hashlib.sha256(archive.read_bytes()).hexdigest()
                archives = {key: (archive, digest) for key in ("x86_64", "aarch64")}
                # render the pinned publisher recipe without uploads or git
                with patch.object(release_aur, "push_aur") as publish:
                    release_aur.release_bin(
                        config, "0.5.1", archives, aur_host="aur.archlinux.org",
                        aur_user="aur", maintainer="maintainer",
                    )
                script = root / "PKGBUILD"
                script.write_text(publish.call_args.args[1])
                source = root / "sources"
                source.mkdir()
                shutil.copyfile(archive, source / f"glesha-0.5.1-{carch}.tar.gz")
                destination = root / "package"
                destination.mkdir()
                subprocess.run([
                    "bash", "-c", 'source "$1"; srcdir=$2; pkgdir=$3; CARCH=$4; package',
                    "bash", str(script), str(source), str(destination), carch,
                ], check=True, capture_output=True)
                self.assertEqual((destination / "usr/bin/glesha").read_bytes(), b"binary")
                self.assertEqual((destination / "usr/bin/glesha").stat().st_mode & 0o777, 0o755)
                self.assertTrue((destination / "usr/share/licenses/glesha/LICENSE").is_file())
                self.assertTrue((destination / "usr/share/man/man1/glesha.1").is_file())
                self.assertFalse((destination / "bin").exists())
                self.assertEqual({path.name for path in destination.iterdir()}, {"usr"})


if __name__ == "__main__":
    unittest.main()
