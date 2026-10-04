#!/usr/bin/env python3
"""Prepare relocatable Linux, macOS and Windows release bundles."""

import gzip
import os
import re
import shutil
import tarfile
import tempfile
import zipfile
from pathlib import Path, PurePosixPath


def prepare_archive(archive: Path) -> None:
    temporary = archive.with_name(archive.name + ".tmp")
    try:
        with tarfile.open(archive, "r:gz") as source:
            with temporary.open("wb") as output:
                with gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=0) as compressed:
                    with tarfile.open(fileobj=compressed, mode="w|") as target:
                        names = set()
                        for member in source:
                            path = PurePosixPath(member.name)
                            if path.is_absolute() or ".." in path.parts or not (member.isfile() or member.isdir()):
                                raise ValueError(f"Unexpected archive member: {member.name}")
                            parts = path.parts
                            if parts and parts[0] == "usr":
                                parts = parts[1:]
                            if not parts:
                                continue
                            member.name = str(PurePosixPath(*parts))
                            if member.name in names:
                                raise ValueError(f"Duplicate archive member: {member.name}")
                            names.add(member.name)
                            member.uid = member.gid = 0
                            member.uname = member.gname = "root"
                            member.mtime = 0
                            body = source.extractfile(member) if member.isfile() else None
                            try:
                                target.addfile(member, body)
                            finally:
                                if body is not None:
                                    body.close()
                        required = {"bin/glesha", "share/man/man1/glesha.1", "install.txt", "LICENSE"}
                        if not required.issubset(names):
                            raise ValueError("Release archive is missing installation assets")
        temporary.replace(archive)
    finally:
        temporary.unlink(missing_ok=True)


def main() -> None:
    architecture = os.environ.get("TARGETARCH", "")
    version = os.environ.get("VERSION", "")
    if architecture not in {"amd64", "arm64"} or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.+_-]*", version):
        raise SystemExit("Expected TARGETARCH=amd64|arm64 and a valid VERSION")
    archive = Path(os.environ.get("OUTPUT_DIR", "/output")) / f"glesha_{version}_{architecture}.tar.gz"
    prepare_archive(archive)
    print(f"Prepared relocatable archive: {archive.name}")
    prepare_platform_archive(archive, "macos", architecture, version)
    if architecture == "amd64":
        prepare_platform_archive(archive, "windows", architecture, version)


def prepare_platform_archive(source: Path, platform: str, architecture: str, version: str) -> Path:
    suffix = "zip" if platform == "windows" else "tar.gz"
    archive = source.parent / f"glesha_{version}_{platform}_{architecture}.{suffix}"
    temporary = archive.with_name(archive.name + ".tmp")
    try:
        with tempfile.TemporaryDirectory(prefix="glesha-package-") as directory:
            root = Path(directory)
            with tarfile.open(source, "r:gz") as bundle:
                for member in bundle:
                    path = PurePosixPath(member.name)
                    if path.is_absolute() or ".." in path.parts or not (member.isfile() or member.isdir()):
                        raise ValueError(f"Unexpected archive member: {member.name}")
                    target = root.joinpath(*path.parts)
                    if member.isdir():
                        target.mkdir(parents=True, exist_ok=True)
                    else:
                        target.parent.mkdir(parents=True, exist_ok=True)
                        with bundle.extractfile(member) as body, target.open("wb") as output:
                            shutil.copyfileobj(body, output)
                        target.chmod(member.mode)
            (root / "bin/glesha").unlink()
            binary = "glesha-windows.exe" if platform == "windows" else "glesha-macos"
            destination = root / "bin" / ("glesha.exe" if platform == "windows" else "glesha")
            shutil.copyfile(Path.cwd() / binary, destination)
            destination.chmod(0o755)
            shutil.copyfile(Path(__file__).with_name(f"install-{platform}.txt"), root / "install.txt")
            files = sorted(path for path in root.rglob("*") if path.is_file())
            if platform == "windows":
                with zipfile.ZipFile(temporary, "w", compression=zipfile.ZIP_DEFLATED) as bundle:
                    for path in files:
                        member = zipfile.ZipInfo(path.relative_to(root).as_posix(), (1980, 1, 1, 0, 0, 0))
                        member.compress_type = zipfile.ZIP_DEFLATED
                        member.external_attr = (0o100000 | path.stat().st_mode & 0o777) << 16
                        with path.open("rb") as body, bundle.open(member, "w") as output:
                            shutil.copyfileobj(body, output)
            else:
                with temporary.open("wb") as output:
                    with gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=0) as compressed:
                        with tarfile.open(fileobj=compressed, mode="w|") as bundle:
                            for path in files:
                                member = bundle.gettarinfo(str(path), arcname=path.relative_to(root).as_posix())
                                member.uid = member.gid = 0
                                member.uname = member.gname = "root"
                                member.mtime = 0
                                with path.open("rb") as body:
                                    bundle.addfile(member, body)
        temporary.replace(archive)
    finally:
        temporary.unlink(missing_ok=True)
    print(f"Prepared {platform} archive: {archive.name}")
    return archive


if __name__ == "__main__":
    main()
