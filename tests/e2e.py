#!/usr/bin/env python3
"""End-to-end test of glesha against real remotes.

For each remote, one device backs up a new set, changes the sources and backs
up again. A second device with no local state, a Docker container holding
only the glesha binary and the credentials file, then discovers the set with
'catalog sync' and restores both snapshots. Each restore must match the
sources as they were when that snapshot was taken. The first device then
moves the set to the trash and restores it, and finally deletes it with
'delete --now'; the second device must see each change after 'catalog sync'.

Usage:
    tests/e2e.py --env FILE --remote b2
    tests/e2e.py --env FILE --remote b2 --remote aws
    tests/e2e.py --env FILE --remote aws --binary ./glesha

The credentials file is passed to 'glesha --env' and never read here. Each
run uploads a few MiB to a new set named e2e-<random> in STANDARD storage. As
soon as the set exists its deletion is queued: before the script exits, pass
or fail, tests/e2ecleanup deletes every stored version of the set's objects,
including the marker 'delete --now' keeps, so nothing stays billed. After a
'delete --now', it must find nothing else. No other object is read or
modified.

Requirements: go, docker.
"""

import argparse
import json
import os
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile
from pathlib import Path

PASSPHRASE = "1234"
DEFAULT_IMAGE = "gcr.io/distroless/static-debian12"
REPOSITORY = Path(__file__).resolve().parent.parent


class Device:
    """Runs glesha with its own empty home directory."""

    def __init__(self, name: str, home: Path, binary: Path, env_file: Path):
        self.name = name
        self.home = home
        self.binary = binary
        self.env_file = env_file
        home.mkdir(parents=True)

    def command(self, args: list[str]) -> tuple[list[str], dict[str, str]]:
        env = {
            "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
            "HOME": str(self.home),
            "GLESHA_CATALOG_PASSPHRASE": PASSPHRASE,
        }
        return [str(self.binary), "--env", str(self.env_file), "--progress", "off", "--color", "no", *args], env

    def path(self, relative: str) -> str:
        """Returns relative as glesha on this device sees it."""
        return str(self.home / relative)

    def glesha(self, *args: str, stdin: str = "") -> str:
        argv, env = self.command(list(args))
        print(f"[{self.name}] glesha {' '.join(args)}", flush=True)
        # run writes the local archive into the working directory
        result = subprocess.run(argv, env=env, input=stdin, capture_output=True, text=True, cwd=self.home)
        if result.returncode != 0:
            raise SystemExit(f"[{self.name}] glesha {' '.join(args)} failed ({result.returncode}):\n{result.stdout}{result.stderr}")
        return result.stdout

    def records(self, kind: str, *args: str) -> list[dict]:
        """Runs glesha with JSON output and returns the data of each record of kind."""
        lines = self.glesha("--format", "json", *args).splitlines()
        return [r["data"] for r in map(json.loads, filter(None, lines)) if r.get("kind") == kind]


class ContainerDevice(Device):
    """A Device in a fresh container: only the binary and credentials are shared."""

    def __init__(self, name: str, home: Path, binary: Path, env_file: Path, image: str):
        super().__init__(name, home, binary, env_file)
        self.image = image

    def command(self, args: list[str]) -> tuple[list[str], dict[str, str]]:
        argv = [
            "docker", "run", "--rm", "-i", "--workdir", "/device",
            "--user", f"{os.getuid()}:{os.getgid()}",
            "--env", "HOME=/device",
            "--env", f"GLESHA_CATALOG_PASSPHRASE={PASSPHRASE}",
            "--volume", f"{self.home}:/device",
            "--volume", f"{self.binary}:/usr/local/bin/glesha:ro",
            "--volume", f"{self.env_file}:/run/glesha/credentials:ro",
            self.image,
            "/usr/local/bin/glesha", "--env", "/run/glesha/credentials", "--progress", "off", "--color", "no", *args,
        ]
        return argv, {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": os.environ.get("HOME", "/")}

    def path(self, relative: str) -> str:
        return "/device/" + relative


def build(directory: Path) -> Path:
    binary = directory / "glesha"
    print("building glesha", flush=True)
    subprocess.run(
        ["go", "build", "-trimpath", "-o", str(binary), "."],
        cwd=REPOSITORY, env={**os.environ, "CGO_ENABLED": "0", "GOOS": "linux"}, check=True,
    )
    return binary


def build_cleanup(directory: Path) -> Path:
    binary = directory / "e2ecleanup"
    subprocess.run(["go", "build", "-o", str(binary), "./tests/e2ecleanup"], cwd=REPOSITORY, check=True)
    return binary


def delete_set(cleanup: Path, device: Device, remote: str, identity: str, uploaded: bool, deleted: bool) -> None:
    """Deletes every stored version of the set's remote objects.

    deleted says that 'glesha delete --now' ran, so only its markers may remain.
    """
    print(f"[cleanup] deleting set {identity} from {remote}", flush=True)
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": str(device.home)}
    result = subprocess.run(
        [str(cleanup), "--env", str(device.env_file), "--remote", remote, "--set", identity],
        env=env, capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise SystemExit(f"[cleanup] set {identity} on {remote} may remain:\n{result.stdout}{result.stderr}")
    counts = {}
    for line in result.stdout.splitlines():
        n, where, prefix = line.split(" ", 2)
        counts[f"{where}:{prefix}"] = int(n)
    print("[cleanup] " + ", ".join(f"{n} versions under {where}" for where, n in counts.items()), flush=True)
    if deleted:
        left = [where for where, n in counts.items() if n and "/tombstones/" not in where]
        if left:
            raise SystemExit(f"[cleanup] glesha delete --now left versions under {left}")
        return
    empty = [where for where, n in counts.items() if n == 0 and "/directory/" not in where]
    if uploaded and empty:
        raise SystemExit(f"[cleanup] nothing found under {empty}; the key layout changed and objects may remain")


def write_sources(root: Path) -> None:
    (root / "notes").mkdir(parents=True)
    (root / "notes" / "todo.txt").write_text("first line\n")
    (root / "notes" / "remove-me.txt").write_text("deleted in the second snapshot\n")
    (root / "empty").mkdir()
    (root / "random.bin").write_bytes(secrets.token_bytes(3 << 20))


def change_sources(root: Path) -> None:
    with open(root / "notes" / "todo.txt", "a") as f:
        f.write("second line\n")
    (root / "notes" / "remove-me.txt").unlink()
    (root / "added" / "deep").mkdir(parents=True)
    (root / "added" / "deep" / "new.txt").write_text("added in the second snapshot\n")
    (root / "random.bin").write_bytes(secrets.token_bytes(1 << 20))


def tree(root: Path) -> dict[str, bytes | None]:
    """Maps each path under root to its content, or None for a directory."""
    out: dict[str, bytes | None] = {}
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root).as_posix()
        out[relative] = None if path.is_dir() else path.read_bytes()
    return out


def compare(expected: Path, restored: Path, label: str) -> None:
    want, got = tree(expected), tree(restored)
    missing = sorted(set(want) - set(got))
    extra = sorted(set(got) - set(want))
    differ = sorted(p for p in set(want) & set(got) if want[p] != got[p])
    if missing or extra or differ:
        raise SystemExit(f"{label} restore differs: missing {missing}, extra {extra}, changed {differ}")
    print(f"{label} restore matches ({len(want)} paths)", flush=True)


def set_id(device: Device, name: str) -> str:
    ids = [s["id"] for s in device.records("set", "list") if s["name"] == name]
    if len(ids) != 1:
        raise SystemExit(f"[{device.name}] expected one set named {name}, found {len(ids)}")
    return ids[0]


def snapshot_ids(device: Device, name: str) -> tuple[str, str]:
    """Returns the IDs of the full and the incremental snapshot of set name."""
    snapshots = [r["snapshot"] for r in device.records("snapshot", "history", name)]
    full = [s["id"] for s in snapshots if s["full"]]
    incremental = [s["id"] for s in snapshots if not s["full"]]
    if len(full) != 1 or len(incremental) != 1:
        raise SystemExit(f"[{device.name}] expected one full and one incremental snapshot, history shows {len(snapshots)}")
    return full[0], incremental[0]


def scenario(remote: str, binary: Path, cleanup: Path, env_file: Path, image: str, work: Path) -> None:
    name = "e2e-" + secrets.token_hex(4)
    print(f"\n== {remote}: set {name}", flush=True)
    first = Device("first", work / "first", binary, env_file)
    sources = first.home / "sources"
    write_sources(sources)
    first.glesha("create", name, str(sources), "--to", remote, "--storage-class", "STANDARD", "--compression", "gzip")
    identity = set_id(first, name)
    uploaded = deleted = False
    try:
        first.glesha("run", name, "--passphrase-file", "-", stdin=PASSPHRASE + "\n")
        uploaded = True
        second = exercise(first, name, identity, remote, binary, env_file, image, work)
        trash(first, second, name, identity, remote)
        first.glesha("delete", name, "--now", "--assume-yes")
        deleted = True
        second.glesha("catalog", "sync", "--from", remote)
        # a sync limited to one remote leaves the rest of the deletion to the next command
        second.glesha("status")
        if identity in [s["id"] for s in second.records("set", "list")]:
            raise SystemExit("[second] still lists the deleted set after catalog sync and status")
    except BaseException:
        # report a failed cleanup without hiding the original failure
        try:
            delete_set(cleanup, first, remote, identity, uploaded, deleted)
        except SystemExit as failure:
            print(failure, file=sys.stderr)
        raise
    delete_set(cleanup, first, remote, identity, uploaded, deleted)


def state(device: Device, identity: str) -> str:
    """Returns the state that 'status' reports for the set."""
    states = [r["condition"]["state"] for r in device.records("status", "status") if r["set"]["id"] == identity]
    if len(states) != 1:
        raise SystemExit(f"[{device.name}] status shows the set {len(states)} times")
    return states[0]


def trash(first: Device, second: "ContainerDevice", name: str, identity: str, remote: str) -> None:
    """Moves the set to the trash and back; the second device follows both."""
    first.glesha("delete", name, "--assume-yes")
    second.glesha("catalog", "sync", "--from", remote)
    if (seen := state(second, identity)) != "TRASH":
        raise SystemExit(f"[second] set is {seen} after delete, want TRASH")
    first.glesha("undelete", name)
    second.glesha("catalog", "sync", "--from", remote)
    if (seen := state(second, identity)) == "TRASH":
        raise SystemExit("[second] set is still in the trash after undelete")


def exercise(first: Device, name: str, identity: str, remote: str, binary: Path, env_file: Path, image: str, work: Path) -> "ContainerDevice":
    """Changes and backs up the sources, then restores both snapshots on a second device."""
    sources = first.home / "sources"
    before = work / "expected-full"
    shutil.copytree(sources, before, symlinks=True)
    change_sources(sources)
    first.glesha("run", name, "--incremental", "--assume-yes", "--passphrase-file", "-", stdin=PASSPHRASE + "\n")

    second = ContainerDevice("second", work / "second", binary, env_file, image)
    second.glesha("catalog", "sync", "--from", remote)
    # encrypted catalogs are known by UUID until unlocked; the name proves decryption
    second.glesha("catalog", "unlock", identity)
    full, incremental = snapshot_ids(second, name)
    for snapshot, expected, label in [(incremental, sources, "latest"), (full, before, "first")]:
        into = "restore-" + label
        second.glesha(
            "restore", name, "--into", second.path(into), "--snapshot", snapshot, "--from", remote,
            "--passphrase-file", "-", stdin=PASSPHRASE + "\n",
        )
        compare(expected, second.home / into / "sources", label)
    return second


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--env", required=True, type=Path, help="credentials file passed to glesha --env")
    parser.add_argument("--remote", required=True, action="append", help="remote to test; repeat for several")
    parser.add_argument("--binary", type=Path, help="statically linked linux glesha binary (default: build one)")
    parser.add_argument("--image", default=DEFAULT_IMAGE, help=f"image for the second device (default: {DEFAULT_IMAGE})")
    parser.add_argument("--keep", action="store_true", help="keep the local working directory")
    args = parser.parse_args()
    if not args.env.is_file():
        raise SystemExit(f"{args.env}: not a file")
    if shutil.which("docker") is None:
        raise SystemExit("docker is required for the second device")
    # SIGTERM unwinds like Ctrl-C so queued deletions still run
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(128 + signal.SIGTERM))

    work = Path(tempfile.mkdtemp(prefix="glesha-e2e-"))
    try:
        binary = args.binary.resolve() if args.binary else build(work)
        cleanup = build_cleanup(work)
        for remote in args.remote:
            scenario(remote, binary, cleanup, args.env.resolve(), args.image, work / remote)
    finally:
        if args.keep:
            print(f"kept {work}", file=sys.stderr)
        else:
            shutil.rmtree(work, ignore_errors=True)
    print("\nall scenarios passed")


if __name__ == "__main__":
    main()
