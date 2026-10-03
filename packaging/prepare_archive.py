#!/usr/bin/env python3
"""Normalize Yesb's Linux tarball to a relocatable installation prefix."""

import gzip
import os
import re
import tarfile
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


if __name__ == "__main__":
    main()
