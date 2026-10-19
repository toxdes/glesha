#!/usr/bin/env python3
"""Bundle the canonical CLI reference with the portable glesha skill."""

import argparse
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="fail if the bundled reference is stale")
    args = parser.parse_args()

    root = Path(__file__).resolve().parents[1]
    source = root / "cli_reference.md"
    target = root / "skills/glesha/references/cli_reference.md"
    content = source.read_text(encoding="utf-8")
    for path in ("man/", "compatibility.md", "pricing/README.md", "man/glesha-version.1"):
        kind = "tree" if path.endswith("/") else "blob"
        content = content.replace(f"]({path})", f"](https://github.com/toxdes/glesha/{kind}/main/{path})")
    bundled = (
        "<!-- Generated from cli_reference.md by scripts/sync_skill_reference.py; do not edit. -->\n\n"
        + content
    )
    if args.check:
        if not target.is_file() or target.read_text(encoding="utf-8") != bundled:
            print("Skill reference is stale. Run python3 scripts/sync_skill_reference.py")
            return 1
        print("Skill reference is current.")
        return 0

    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(bundled, encoding="utf-8")
    print(f"Updated {target.relative_to(root)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
