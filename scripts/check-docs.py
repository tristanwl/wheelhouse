#!/usr/bin/env python3
"""Checks the documentation against the repository (standard library only).

  python3 scripts/check-docs.py

1. Links: every relative Markdown link must point at a file or directory that
   exists, and a #fragment must match a heading of the target file. http(s)
   links are not fetched (CI has no business depending on other sites).
2. File references: a `code span` in a Markdown file that looks like a path into
   this repository (it starts with a top-level directory or is a top-level file
   name, has no placeholder or wildcard) must exist.
3. Environment variables: every WHEELHOUSE_* / service variable the Go code reads
   must be in the README's variable table, and every variable the README table
   names must exist in the code (or be one of the service variables of the
   provider table). Test-only variables are listed in TEST_ONLY.

Exit status 1 with one line per problem.
"""

import os
import re
import sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
SKIP_DIRS = {".git", "node_modules", ".claude", "dist", ".devconfig"}

# Variables only tests and development tooling read; documented in CONTRIBUTING.md
# and web/README.md, not in the operator table.
TEST_ONLY = {
    "UPDATE_PROVIDER_SNAPSHOT",
    "WHEELHOUSE_BIG_FIXTURE",
    "WHEELHOUSE_STORE_CRASH_HELPER",
    "WHEELHOUSE_STORE_CRASH_MODE",
    "WHEELHOUSE_API",
    "WHEELHOUSE_BIN",
    "WHEELHOUSE_CMD",
    "WHEELHOUSE_SMOKE_REQUIRED",
}

# Specs that describe the 0.1.x Next.js code say so in a banner; their file
# references are history, so only their links are checked.
HISTORICAL = {os.path.join("docs", "wheelhouse-spec.md")}

# Paths a build or a run creates (all git-ignored): documented, but absent in a clean checkout.
GENERATED = {"server/.devconfig", "server/wheelhouse", "server/internal/ui/dist", "web/dist"}

problems = []


def md_files():
    for base, dirs, files in os.walk(ROOT):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        for f in files:
            if f.endswith(".md"):
                yield os.path.join(base, f)


def rel(p):
    return os.path.relpath(p, ROOT)


def slug(heading):
    h = heading.strip().lower()
    h = re.sub(r"`", "", h)
    h = re.sub(r"[^\w\- ]", "", h)
    return h.replace(" ", "-")


_anchor_cache = {}


def anchors(path):
    if path not in _anchor_cache:
        found, seen, fence = set(), {}, False
        with open(path, encoding="utf-8") as fh:
            for line in fh:
                if line.lstrip().startswith("```"):
                    fence = not fence
                    continue
                m = None if fence else re.match(r"^#{1,6}\s+(.*?)\s*#*\s*$", line)
                if m:
                    s = slug(re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", m.group(1)))
                    n = seen.get(s, 0)
                    seen[s] = n + 1
                    found.add(s if n == 0 else f"{s}-{n}")
        _anchor_cache[path] = found
    return _anchor_cache[path]


def strip_fences(text):
    return re.sub(r"```.*?```", lambda m: "\n" * m.group(0).count("\n"), text, flags=re.S)


LINK = re.compile(r"(?<!\!)\[[^\]]*\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)")
SPAN = re.compile(r"`([^`\n]+)`")
TOP = {e for e in os.listdir(ROOT) if e not in SKIP_DIRS}
PATH_EXT = re.compile(r"\.(md|ts|tsx|go|sh|yml|yaml|json|mjs|mts|sql|example|py|html|css)$")


def check_links(path, text):
    for n, line in enumerate(text.split("\n"), 1):
        for target in LINK.findall(line):
            if re.match(r"^(https?:|mailto:)", target):
                continue
            file_part, _, frag = target.partition("#")
            dest = path if file_part == "" else os.path.normpath(os.path.join(os.path.dirname(path), file_part))
            if not os.path.exists(dest):
                problems.append(f"{rel(path)}:{n}: broken link {target}")
            elif frag and dest.endswith(".md") and frag not in anchors(dest):
                problems.append(f"{rel(path)}:{n}: no heading #{frag} in {rel(dest)}")


def check_spans(path, text):
    for n, line in enumerate(text.split("\n"), 1):
        for span in SPAN.findall(line):
            s = span.strip()
            if re.search(r"[\s<>*{}$|]|\.\.\.|\.\./", s) or s.startswith(("/", "~", "-", "http")):
                continue
            first = s.split("/")[0]
            if "/" not in s and not (s in TOP and PATH_EXT.search(s)):
                continue
            if first not in TOP:
                continue
            s = s.split(":")[0].rstrip("/")  # file.ts:12 style
            if any(s == g or s.startswith(g + "/") for g in GENERATED):
                continue
            if not os.path.exists(os.path.join(ROOT, s)):
                problems.append(f"{rel(path)}:{n}: `{span}` does not exist")


def go_env_names():
    names = set()
    pat = re.compile(r'"((?:WHEELHOUSE|SONARR|RADARR|SABNZBD|QBITTORRENT|PLEX|JELLYFIN|TMDB)_[A-Z0-9_]+|PORT|GOMEMLIMIT)"')
    for base, dirs, files in os.walk(os.path.join(ROOT, "server")):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        for f in files:
            if f.endswith(".go") and not f.endswith("_test.go"):
                with open(os.path.join(base, f), encoding="utf-8") as fh:
                    text = fh.read()
                # Only identifiers that are used as an environment name.
                if "getenv" not in text.lower() and "Env" not in text:
                    continue
                names.update(pat.findall(text))
    return names


def readme_table_names():
    with open(os.path.join(ROOT, "README.md"), encoding="utf-8") as fh:
        text = fh.read()
    m = re.search(r"^## Configuration\b.*?(?=^## )", text, flags=re.S | re.M)
    section = m.group(0) if m else ""
    names = set()
    for row in section.split("\n"):
        if row.startswith("|"):
            first = row.split("|")[1]
            names.update(n for n in re.findall(r"`([A-Z][A-Z0-9_]*)`", first) if not n.endswith("_"))
    return names, section


def check_env():
    code = go_env_names()
    # Dynamic names (RADARR_<ID>_URL) are documented as a pattern.
    documented, section = readme_table_names()
    for name in sorted(code):
        if name in TEST_ONLY or name in documented:
            continue
        if re.match(r"^(SONARR|RADARR)_[A-Z0-9_]+$", name) and "RADARR_<ID>_URL" in section:
            continue
        problems.append(f"README.md: the code reads {name} but the Configuration table does not list it")
    for name in sorted(documented):
        if name not in code and name != "HOSTNAME":
            problems.append(f"README.md: the Configuration table lists {name} but no Go code reads it")


def main():
    for path in sorted(md_files()):
        with open(path, encoding="utf-8") as fh:
            text = fh.read()
        text = strip_fences(text)
        check_links(path, text)
        if rel(path) not in HISTORICAL:
            check_spans(path, text)
    check_env()
    for p in problems:
        print(p)
    print(f"checked {len(list(md_files()))} Markdown files: {len(problems)} problem(s)")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
