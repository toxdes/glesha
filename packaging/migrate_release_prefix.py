#!/usr/bin/env -S uv run
# /// script
# requires-python = ">=3.11"
# dependencies = ["boto3"]
# ///

"""Copy published release objects from the legacy R2 prefix to the shared one.

Existing objects are immutable, so the migration copies them and leaves the
originals in place. Re-running the script is safe: copied keys are skipped.
"""

import argparse
import os
import sys
from dataclasses import dataclass
from pathlib import Path, PurePosixPath

DEFAULT_SOURCE_PREFIX = "glesha/releases"
DEFAULT_TARGET_PREFIX = "releases"
IGNORED_NAMES = {"SHA256SUMS"}


@dataclass(frozen=True)
class Plan:
    copies: list[tuple[str, str, int]]
    existing: list[str]
    conflicts: list[tuple[str, int, int]]
    ignored: list[tuple[str, str]]


def list_objects(client, bucket, prefix):
    """Map object keys below prefix to their size in bytes."""
    normalized = prefix.strip("/") + "/"
    sizes, token = {}, None
    while True:
        request = {"Bucket": bucket, "Prefix": normalized}
        if token:
            request["ContinuationToken"] = token
        page = client.list_objects_v2(**request)
        for item in page.get("Contents", []):
            if item["Key"].endswith("/"):
                continue
            sizes[item["Key"]] = item["Size"]
        token = page.get("NextContinuationToken")
        if not page.get("IsTruncated") or not token:
            return sizes


def ignore_reason(relative):
    path = PurePosixPath(relative)
    if path.name in IGNORED_NAMES:
        return "checksum index is not copied between prefixes"
    if any(part.startswith(".") for part in path.parts):
        return "transient lock or publication marker"
    return None


def build_plan(source, target, source_prefix, target_prefix):
    source_root = source_prefix.strip("/")
    target_root = target_prefix.strip("/")
    if source_root == target_root:
        raise ValueError("source and target prefixes must differ")
    copies, existing, conflicts, ignored = [], [], [], []
    for key, size in sorted(source.items()):
        relative = key[len(source_root) + 1 :]
        if not relative:
            continue
        reason = ignore_reason(relative)
        if reason:
            ignored.append((key, reason))
            continue
        destination = f"{target_root}/{relative}"
        if destination not in target:
            copies.append((key, destination, size))
        elif target[destination] == size:
            existing.append(destination)
        else:
            conflicts.append((destination, target[destination], size))
    return Plan(copies, existing, conflicts, ignored)


def apply_plan(client, bucket, plan, target_prefix):
    for source_key, destination, _ in plan.copies:
        client.copy_object(
            Bucket=bucket,
            Key=destination,
            CopySource={"Bucket": bucket, "Key": source_key},
        )
    verify_copy(client, bucket, plan, target_prefix)


def verify_copy(client, bucket, plan, target_prefix):
    actual = list_objects(client, bucket, target_prefix)
    for _, destination, size in plan.copies:
        if destination not in actual:
            raise ValueError(f"copy did not produce {destination}")
        if actual[destination] != size:
            raise ValueError(
                f"copy changed the size of {destination}: "
                f"{actual[destination]} != {size}"
            )


def load_env_file(path):
    """Load KEY=VALUE entries without replacing existing environment values."""
    env_file = Path(path)
    if not env_file.is_file():
        raise SystemExit(f"{path} not found")
    with env_file.open() as file:
        for line in file:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            os.environ.setdefault(key.strip(), value.strip().strip("'\""))


def r2_client():
    import boto3

    required = [
        "AWS_ENDPOINT_URL",
        "AWS_BUCKET",
        "AWS_ACCESS_KEY_ID",
        "AWS_SECRET_ACCESS_KEY",
    ]
    missing = [name for name in required if name not in os.environ]
    if missing:
        raise SystemExit("Missing env vars: " + ", ".join(missing))
    client = boto3.client(
        "s3",
        endpoint_url=os.environ["AWS_ENDPOINT_URL"],
        aws_access_key_id=os.environ["AWS_ACCESS_KEY_ID"],
        aws_secret_access_key=os.environ["AWS_SECRET_ACCESS_KEY"],
        region_name=os.environ.get("R2_REGION", "auto"),
    )
    return client, os.environ["AWS_BUCKET"]


def report(plan, source_prefix, target_prefix):
    for source_key, reason in plan.ignored:
        print(f"  ignore {source_key} ({reason})")
    for destination, found, expected in plan.conflicts:
        print(
            f"  conflict {destination} exists with {found} bytes, "
            f"source has {expected} bytes",
            file=sys.stderr,
        )
    print(
        f"{len(plan.copies)} object(s) to copy from {source_prefix}/ "
        f"to {target_prefix}/, {len(plan.existing)} already present"
    )
    for source_key, destination, size in plan.copies:
        print(f"  {source_key} -> {destination} ({size} bytes)")


def main():
    parser = argparse.ArgumentParser(
        description=__doc__,
        epilog=(
            "Environment: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, "
            "AWS_ENDPOINT_URL, AWS_BUCKET, R2_REGION (default auto)"
        ),
    )
    parser.add_argument("--env", metavar="PATH", help="Load env vars from file")
    parser.add_argument(
        "--source-prefix", default=DEFAULT_SOURCE_PREFIX, help="legacy prefix"
    )
    parser.add_argument(
        "--target-prefix", default=DEFAULT_TARGET_PREFIX, help="shared prefix"
    )
    parser.add_argument(
        "--apply", action="store_true", help="copy objects; without it, plan only"
    )
    args = parser.parse_args()

    if args.env:
        load_env_file(args.env)

    client, bucket = r2_client()
    source = list_objects(client, bucket, args.source_prefix)
    target = list_objects(client, bucket, args.target_prefix)
    plan = build_plan(source, target, args.source_prefix, args.target_prefix)
    report(plan, args.source_prefix.strip("/"), args.target_prefix.strip("/"))

    if plan.conflicts:
        raise SystemExit("refusing to overwrite existing objects")
    if not args.apply:
        print("Dry run: no objects copied. Pass --apply to copy them.")
        return 0
    if not plan.copies:
        print("Nothing to copy.")
        return 0

    apply_plan(client, bucket, plan, args.target_prefix)
    print(f"Copied and verified {len(plan.copies)} object(s).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
