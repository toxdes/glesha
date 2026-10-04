#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# ///
"""Publish glesha AUR recipes through the pinned Yesb toolkit."""

import argparse
import shlex
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "yesb"))

from release_aur import generate_srcinfo, push_aur, release_archives, release_git
from release_lib import check_tools, load_config, load_env_file, project_version, validate_config


def binary_pkgbuild(config, version: str) -> str:
    hosting = config.section("hosting")
    url = hosting["public_base_url"].rstrip("/") + "/" + hosting["release_prefix"]
    values = {
        "PACKAGE": config.section("aur")["binary_package"],
        "VERSION": version,
        "DESCRIPTION": config.project["description"],
        "HOMEPAGE": config.project["homepage"],
    }
    for arch, (archive, digest) in release_archives(config, version).items():
        suffix = "AMD64" if arch == "x86_64" else "ARM64"
        values[f"URL_{suffix}"] = f"{url}/{archive.name}"
        values[f"SHA256_{suffix}"] = digest
    template = Path(__file__).with_name("PKGBUILD.bin").read_text()
    for key, value in values.items():
        template = template.replace(f"@{key}@", shlex.quote(value))
    return template


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env", type=Path)
    parser.add_argument("--type", choices=("git", "bin", "both"), default="both")
    parser.add_argument("--dry-run", action="store_true", help="Validate recipes without publishing")
    args = parser.parse_args()
    if args.env:
        load_env_file(args.env)
    config = load_config(ROOT / "release.toml")
    validate_config(config, "aur")
    version = project_version(config)
    aur = config.section("aur")
    hosting = config.section("hosting")["aur"]
    helper = aur.get("srcinfo_helper_image")
    check_tools("docker" if helper else "makepkg")
    recipes = {}
    if args.type in ("git", "both"):
        recipes[aur["git_package"]] = (ROOT / aur["git_pkgbuild"]).read_text()
    if args.type in ("bin", "both"):
        recipes[aur["binary_package"]] = binary_pkgbuild(config, version)
    # validate both recipes before any remote mutation
    for name, recipe in recipes.items():
        with tempfile.TemporaryDirectory(prefix="glesha-aur-") as directory:
            path = Path(directory)
            (path / "PKGBUILD").write_text(recipe)
            info = generate_srcinfo(path, helper)
            if args.dry_run:
                print(f"{name}:\n{info}")
    if args.dry_run:
        return
    check_tools("git", "ssh")
    options = {
        "aur_host": hosting["host"],
        "aur_user": hosting["ssh_user"],
        "maintainer": hosting["maintainer"],
        "srcinfo_helper_image": helper,
    }
    if args.type in ("git", "both"):
        release_git(config, version, **options)
    if args.type in ("bin", "both"):
        push_aur(aur["binary_package"], recipes[aur["binary_package"]], version=version, **options)


if __name__ == "__main__":
    main()
