#!/usr/bin/env python3
"""Check that relative links in README/docs point at existing files.

External URLs, mailto: and pure anchors are ignored. Anchors on relative links
are stripped before the existence check. Run from the repository root.
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
FILES = [ROOT / "README.md", ROOT / "README.zh-CN.md", ROOT / "CONTRIBUTING.md",
         ROOT / "SECURITY.md", ROOT / "CHANGELOG.md", ROOT / "CODE_OF_CONDUCT.md"]
FILES += sorted((ROOT / "docs").rglob("*.md"))
FILES += sorted((ROOT / "integrations").rglob("*.md"))

LINK = re.compile(r"\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)")
SKIP_PREFIX = ("http://", "https://", "mailto:", "#", "data:")

failures = []
for path in FILES:
    if not path.exists():
        continue
    text = path.read_text(encoding="utf-8")
    for lineno, line in enumerate(text.splitlines(), 1):
        for raw in LINK.findall(line):
            target = raw.split("#", 1)[0]
            if not target or target.startswith(SKIP_PREFIX):
                continue
            if target.startswith("/"):
                resolved = ROOT / target.lstrip("/")
            else:
                resolved = (path.parent / target).resolve()
            if not resolved.exists():
                failures.append(f"{path.relative_to(ROOT)}:{lineno}: {raw}")

if failures:
    print("broken relative links:", file=sys.stderr)
    print("\n".join(failures), file=sys.stderr)
    sys.exit(1)
print(f"check-docs: {len(FILES)} files, all relative links resolve")
