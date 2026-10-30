#!/usr/bin/env python3
"""Fold changelog fragments into CHANGELOG.md.

A change describes itself in changes/unreleased/<slug>.<section>.md instead of editing
CHANGELOG.md, so two branches never conflict on it. <section> is added, changed, deprecated,
removed, fixed or security; the body is one or more `- ` list items.

    python3 scripts/changelog.py check                  # every fragment is well-formed (CI)
    python3 scripts/changelog.py release 0.1.0          # fold the fragments into a dated
                                                        # "## 0.1.0 - YYYY-MM-DD" section, delete them
    python3 scripts/changelog.py release 0.1.0 --date 2026-11-03
    python3 scripts/changelog.py release-notes 0.1.0    # that section's body, for the GitHub release

Keep a Changelog section order; SOURCE_DATE_EPOCH, when set, dates the release.
"""

from __future__ import annotations

import argparse
import datetime as dt
import os
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
CHANGELOG = ROOT / "CHANGELOG.md"
FRAGMENTS = ROOT / "changes" / "unreleased"
SECTIONS = ["Added", "Changed", "Deprecated", "Removed", "Fixed", "Security"]
FRAGMENT_NAME = re.compile(r"^(?P<slug>[A-Za-z0-9][A-Za-z0-9._-]*)\.(?P<section>[a-z]+)\.md$")
VERSION = re.compile(r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$")
HEADER = """# Changelog

All notable changes to SysPI are recorded here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
form; SysPI follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html) and each release's tag
equals its version, as `.project.json` requires of the packages it installs.
"""


def fail(message: str) -> None:
    print(f"changelog.py: {message}", file=sys.stderr)
    sys.exit(1)


def fragments() -> list[tuple[pathlib.Path, str, str]]:
    """Every fragment as (path, section heading, body), validated."""
    found = []
    for path in sorted(FRAGMENTS.glob("*.md")) if FRAGMENTS.is_dir() else []:
        if path.name == "README.md":
            continue
        m = FRAGMENT_NAME.match(path.name)
        if not m or m["section"] not in (s.lower() for s in SECTIONS):
            fail(f"{path.relative_to(ROOT)}: name must be <slug>.<section>.md with section one of "
                 + ", ".join(s.lower() for s in SECTIONS))
        body = path.read_text(encoding="utf-8").strip()
        if not body.startswith("- "):
            fail(f"{path.relative_to(ROOT)}: body must be list items starting with '- '")
        found.append((path, m["section"].capitalize(), body))
    return found


def render_section(version: str, date: str, parts: list[tuple[pathlib.Path, str, str]]) -> str:
    out = [f"## {version} - {date}", ""]
    for section in SECTIONS:
        items = [body for _, s, body in parts if s == section]
        if items:
            out += [f"### {section}", ""] + items + [""]
    return "\n".join(out)


def section_of(version: str) -> str:
    if not CHANGELOG.is_file():
        fail(f"{CHANGELOG.name} does not exist")
    text = CHANGELOG.read_text(encoding="utf-8")
    m = re.search(rf"^## {re.escape(version)} - \d{{4}}-\d{{2}}-\d{{2}}\n(?P<body>.*?)(?=^## |\Z)", text, re.M | re.S)
    if not m:
        fail(f"{CHANGELOG.name} has no '## {version} - <date>' section; run 'changelog.py release {version}' first")
    return m["body"].strip() + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("check")
    release = sub.add_parser("release")
    release.add_argument("version")
    release.add_argument("--date")
    notes = sub.add_parser("release-notes")
    notes.add_argument("version")
    args = parser.parse_args()

    if args.command == "check":
        found = fragments()
        print(f"ok: {len(found)} fragment(s) well-formed")
        return

    if not VERSION.match(args.version):
        fail(f"'{args.version}' is not a semantic version (no leading v)")

    if args.command == "release-notes":
        sys.stdout.write(section_of(args.version))
        return

    parts = fragments()
    if not parts:
        fail("no fragments under changes/unreleased/ to release")
    if args.date:
        date = args.date
    else:
        epoch = os.environ.get("SOURCE_DATE_EPOCH")
        when = dt.datetime.fromtimestamp(int(epoch), dt.timezone.utc) if epoch else dt.datetime.now(dt.timezone.utc)
        date = when.date().isoformat()
    existing = CHANGELOG.read_text(encoding="utf-8") if CHANGELOG.is_file() else HEADER
    if re.search(rf"^## {re.escape(args.version)} - ", existing, re.M):
        fail(f"{CHANGELOG.name} already has a {args.version} section")
    head, sep, tail = existing.partition("\n## ")
    new = head.rstrip("\n") + "\n\n" + render_section(args.version, date, parts)
    if sep:
        new += "\n## " + tail
    CHANGELOG.write_text(new, encoding="utf-8")
    for path, _, _ in parts:
        path.unlink()
    print(f"released {args.version} ({date}): {len(parts)} fragment(s) folded into {CHANGELOG.name}")


if __name__ == "__main__":
    main()
