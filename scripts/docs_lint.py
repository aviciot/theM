#!/usr/bin/env python3.12
"""Docs lint + index generator + owner lookup.  No third-party deps.

  docs_lint.py check            validate front matter, folders, owns: paths (exit 1 on errors)
  docs_lint.py index            regenerate docs/INDEX.md from front matter
  docs_lint.py staged           warn-only: staged code whose owning doc is not staged (exit 0)
  docs_lint.py links            dead docs/*.md references (errors in docs, CLAUDE.md files and skills; counts in source comments)
  docs_lint.py stale [DAYS]     report-only: docs unverified or last_verified older than DAYS (default 90)
  docs_lint.py stray            report-only: *.md files outside docs/ that are not on the allow-list
  docs_lint.py owners PATH...   print the docs that own each path

Front matter spec: docs/plans/docs-reboot-plan.md section 4.
"""
import datetime, re, subprocess, sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DOCS = ROOT / "docs"
FOLDERS = {"architecture", "reference", "guides", "decisions", "plans", "lessons"}
TYPES = {"architecture", "reference", "guide", "decision", "plan", "lessons"}
STATUSES = {"current", "draft", "superseded"}
SKIP = {"_archive", "_templates"}


def parse(path):
    """Return front-matter dict, or None if the file has none."""
    lines = path.read_text(encoding="utf-8").splitlines()
    if not lines or lines[0].strip() != "---":
        return None
    fm, key = {}, None
    for ln in lines[1:]:
        if ln.strip() == "---":
            return fm
        if ln.lstrip().startswith("- ") and key:
            fm[key].append(ln.strip()[2:].strip())
        elif ":" in ln and not ln.startswith(" "):
            key, val = ln.split(":", 1)
            val = val.split("#")[0].strip()
            fm[key.strip()] = [] if val == "" else val
            key = key.strip()
    return None


def all_docs():
    for p in sorted(DOCS.rglob("*.md")):
        rel = p.relative_to(DOCS)
        if rel.parts[0] in SKIP or p.name == "INDEX.md":
            continue
        yield p, rel


REF = re.compile(r"docs/[A-Za-z0-9_./<>{}-]+\.md")
DOC_SCOPES = ("CLAUDE.md", "go/CLAUDE.md", "README.md")
STRAY_OK = {"CLAUDE.md", "README.md", "INSTALL.md", "go/CLAUDE.md", "go/TEST_INDEX.md",
            "scripts/tests/INDEX.md", "db/README.md", "postgres/README.md", "redis/README.md"}


def tracked():
    out = subprocess.run(["git", "ls-files"], cwd=ROOT, capture_output=True, text=True).stdout.split("\n")
    return [f for f in out if f and not f.startswith((".claude/worktrees", "go/vendor", "frontend/node_modules", "docs/_archive/"))]


def dead_refs():
    """Return (errors, source_count): dead refs inside docs/CLAUDE.md/skills vs inside source comments."""
    errors, src = [], 0
    for f in tracked():
        p = ROOT / f
        in_docs = f.startswith("docs/") or f in DOC_SCOPES or f.startswith(".claude/skills/")
        if f == "docs/INDEX.md" or (in_docs and p.suffix != ".md") or p.suffix in {".png", ".jpg", ".ico", ".woff", ".woff2", ".pdf", ".gz", ".zip", ".sum"}:
            continue
        try:
            text = p.read_text(encoding="utf-8")
        except Exception:
            continue
        for m in sorted(set(REF.findall(text))):
            if any(c in m for c in "<>{}*") or (ROOT / m).exists():
                continue
            if in_docs:
                errors.append(f"{f}: dead doc reference {m}")
            else:
                src += 1
    return errors, src


def links():
    errors, src = dead_refs()
    for e in errors:
        print("ERROR", e)
    print(f"{len(errors)} dead references in docs/CLAUDE.md/skills; {src} in source comments (report-only, backlog F-87)")
    return 1 if errors else 0


def stale(days=90):
    today, n = datetime.date.today(), 0
    for p, rel in all_docs():
        fm = parse(p) or {}
        lv = fm.get("last_verified", "")
        try:
            age = (today - datetime.date.fromisoformat(lv)).days
        except ValueError:
            print(f"UNVERIFIED {rel} ({fm.get('status')})"); n += 1; continue
        if age > days:
            print(f"STALE {rel}: last_verified {lv} ({age} days)"); n += 1
    print(f"{n} doc(s) unverified or older than {days} days")
    return 0


def stray():
    n = 0
    for f in tracked():
        if f.endswith(".md") and not f.startswith(("docs/", ".claude/")) and f not in STRAY_OK and "/node_modules/" not in f:
            print("STRAY", f); n += 1
    print(f"{n} markdown file(s) outside docs/ and not on the allow-list (triage: move into docs/<folder>/ or archive)")
    return 0


def check():
    errors, legacy = [], []
    for p, rel in all_docs():
        fm = parse(p)
        if fm is None:
            legacy.append(str(rel))
            continue
        if len(rel.parts) > 1 and rel.parts[0] not in FOLDERS:
            errors.append(f"{rel}: folder not one of {sorted(FOLDERS)}")
        for k in ("title", "type", "status", "last_verified"):
            if not fm.get(k):
                errors.append(f"{rel}: missing '{k}'")
        if fm.get("type") and fm["type"] not in TYPES:
            errors.append(f"{rel}: bad type '{fm['type']}'")
        if fm.get("status") and fm["status"] not in STATUSES:
            errors.append(f"{rel}: bad status '{fm['status']}'")
        if fm.get("type") == "architecture" and not fm.get("owns"):
            errors.append(f"{rel}: architecture docs need 'owns:'")
        for o in fm.get("owns", []):
            if not (ROOT / o).exists():
                errors.append(f"{rel}: owns path does not exist: {o}")
    link_errors, src = dead_refs()
    errors += link_errors
    for e in errors:
        print("ERROR", e)
    for l in legacy:
        print("legacy (no front matter):", l)
    print(f"{len(errors)} errors, {len(legacy)} legacy docs, {src} dead refs in source comments (report-only)")
    return 1 if errors else 0


def index():
    rows, legacy = [], []
    for p, rel in all_docs():
        fm = parse(p)
        (legacy if fm is None else rows).append((rel, fm))
    out = ["# Documentation Index — the-M", "",
           "<!-- GENERATED by scripts/docs_lint.py index — do not edit by hand -->", "",
           "Code beats docs. Find the doc that `owns` the code you touch.", "",
           "| Doc | Type | Status | Verified | Owns |", "|---|---|---|---|---|"]
    for rel, fm in sorted(rows, key=lambda r: (r[1].get("type", ""), str(r[0]))):
        owns = ", ".join(f"`{o}`" for o in fm.get("owns", []))
        out.append(f"| [{fm.get('title', rel)}]({rel}) | {fm.get('type')} | {fm.get('status')} "
                   f"| {fm.get('last_verified')} | {owns} |")
    if legacy:
        out += ["", "## Legacy (no front matter yet — verify before trusting)", ""]
        out += [f"- [{rel}]({rel})" for rel, _ in legacy]
    out += ["", "Retired docs: `_archive/` (not maintained; use only as hints)."]
    (DOCS / "INDEX.md").write_text("\n".join(out) + "\n", encoding="utf-8")
    print(f"INDEX.md: {len(rows)} docs, {len(legacy)} legacy")


def owner_map():
    m = {}
    for p, rel in all_docs():
        fm = parse(p) or {}
        for o in fm.get("owns", []):
            m.setdefault(o, str(Path("docs") / rel))
    return m


def owners_of(path, m):
    return sorted({d for o, d in m.items() if path == o.rstrip("/") or path.startswith(o.rstrip("/") + "/")})


def staged():
    files = subprocess.run(["git", "diff", "--cached", "--name-only"], cwd=ROOT,
                           capture_output=True, text=True).stdout.split()
    m, missing = owner_map(), {}
    for f in files:
        if f.startswith("docs/"):
            continue
        for d in owners_of(f, m):
            if d not in files:
                missing.setdefault(d, []).append(f)
    for d, fs in missing.items():
        print(f"DOCS WARNING: {len(fs)} staged file(s) are owned by {d}, which is not staged "
              f"(e.g. {fs[0]}). Update it and bump last_verified, or confirm no doc change is needed.")
    return 0


if __name__ == "__main__":
    cmd = sys.argv[1] if len(sys.argv) > 1 else "check"
    if cmd == "check": sys.exit(check())
    elif cmd == "index": index()
    elif cmd == "staged": sys.exit(staged())
    elif cmd == "links": sys.exit(links())
    elif cmd == "stale": sys.exit(stale(int(sys.argv[2]) if len(sys.argv) > 2 else 90))
    elif cmd == "stray": sys.exit(stray())
    elif cmd == "owners":
        m = owner_map()
        for a in sys.argv[2:]:
            print(a, "->", ", ".join(owners_of(a, m)) or "(no owning doc)")
    else:
        sys.exit(__doc__)
