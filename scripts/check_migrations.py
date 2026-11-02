#!/usr/bin/env python3.12
"""Migration drift check and lint.  stdlib only.  Read-only: it never changes the database.

  python3.12 scripts/check_migrations.py              # drift: do the objects db/NNN_*.sql create exist in the live DB?
  python3.12 scripts/check_migrations.py --verbose    # also list the objects explained away (dropped later / known drift)
  python3.12 scripts/check_migrations.py --lint       # no DB: every migration >= 127 must record itself in schema_migrations
  python3.12 scripts/check_migrations.py --lint-new db/130_x.sql ...   # same, for the given files (pre-commit hook)

Drift mode needs the stack (`docker exec them-postgres psql`). It looks only at `CREATE TABLE` and
`ALTER TABLE ... ADD COLUMN`. An object is NOT reported when a later migration drops it
(`DROP TABLE`, `DROP COLUMN`) or when it is listed with a reason in `db/known_drift.txt`, so any
report is real: a migration that was committed but never applied (backlog F-135: db/125 was missing and
every gateway usage write failed silently). Exit code 1 on unexplained drift or lint failures.
"""
import collections, glob, os, re, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LINT_FROM = 127  # earlier migrations predate the self-recording convention being enforced

_NAME = r"(?:(\w+)\.)?(\w+)"
CREATE_TABLE = re.compile(r"CREATE TABLE (?:IF NOT EXISTS )?" + _NAME, re.I)
ADD_COLUMN = re.compile(r"ALTER TABLE (?:IF EXISTS )?(?:ONLY )?" + _NAME + r"\s+ADD COLUMN (?:IF NOT EXISTS )?(\w+)", re.I)
DROP_TABLE = re.compile(r"DROP TABLE (?:IF EXISTS )?([\w\.\s,]+?)\s*(?:CASCADE|RESTRICT)?\s*;", re.I)
DROP_COLUMN = re.compile(r"ALTER TABLE (?:IF EXISTS )?(?:ONLY )?" + _NAME + r"\s+((?:[^;]*?))DROP COLUMN (?:IF EXISTS )?(\w+)", re.I | re.S)
SELF_RECORD = re.compile(r"INSERT\s+INTO\s+(?:them\.)?schema_migrations", re.I)


def declared(text):
    """Objects a migration creates: ('table', schema, name) and ('column', schema, table, column)."""
    out = [("table", m.group(1) or "them", m.group(2)) for m in CREATE_TABLE.finditer(text)]
    out += [("column", m.group(1) or "them", m.group(2), m.group(3)) for m in ADD_COLUMN.finditer(text)]
    return out


def dropped(text):
    """Objects a migration drops, in the same shape as declared() (a dropped table also lists its columns implicitly)."""
    out = set()
    for m in DROP_TABLE.finditer(text):
        for part in m.group(1).split(","):
            part = part.strip()
            if part:
                schema, _, name = part.rpartition(".")
                out.add(("table", schema or "them", name))
    for m in DROP_COLUMN.finditer(text):
        out.add(("column", m.group(1) or "them", m.group(2), m.group(4)))
    return out


def is_superseded(obj, index, drops_by_file):
    """True when a LATER migration (higher index in sorted order) drops this object or its table."""
    table = ("table",) + (obj[1:3] if obj[0] == "column" else obj[1:3])
    for later in drops_by_file[index + 1:]:
        if obj in later or table in later:
            return True
    return False


def parse_known_drift(text):
    """db/known_drift.txt lines: `<file> | table schema.name | reason` or `<file> | column schema.table.col | reason`."""
    known = {}
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        fname, obj, reason = [p.strip() for p in line.split("|", 2)]
        kind, _, ref = obj.partition(" ")
        parts = ref.split(".")
        key = ("table", parts[0], parts[1]) if kind == "table" else ("column", parts[0], parts[1], parts[2])
        known[(fname, key)] = reason
    return known


def describe(obj):
    return f"{obj[0]} {'.'.join(obj[1:])}"


def find_drift(files, live_tables, live_cols, known):
    """files: [(name, text)] sorted. Returns (unexplained, explained) as {name: [(obj, why)]}."""
    drops = [dropped(t) for _, t in files]
    unexplained, explained = collections.defaultdict(list), collections.defaultdict(list)
    for i, (name, text) in enumerate(files):
        if "DOWN" in name:
            continue
        for obj in declared(text):
            if obj[0] == "table":
                present = (obj[1], obj[2]) in live_tables
            else:  # a column of a table the live DB lacks is reported via the table, not twice
                present = (obj[1], obj[2]) not in live_tables or (obj[1], obj[2], obj[3]) in live_cols
            if present:
                continue
            if is_superseded(obj, i, drops):
                explained[name].append((obj, "dropped by a later migration"))
            elif (name, obj) in known:
                explained[name].append((obj, "known drift: " + known[(name, obj)]))
            else:
                unexplained[name].append((obj, "missing"))
    return unexplained, explained


def lint(files):
    """Migrations numbered >= LINT_FROM must record themselves so schema_migrations stays trustworthy."""
    problems = []
    for name, text in files:
        num = os.path.basename(name).split("_")[0]
        if not num.isdigit() or int(num) < LINT_FROM or "DOWN" in name:
            continue
        if not SELF_RECORD.search(text):
            problems.append(f"{name}: must end with INSERT INTO them.schema_migrations (version, description, applied_at) ...")
    return problems


def _text(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def _read(paths):
    return [(os.path.basename(p), _text(p)) for p in paths]


def _all_migrations():
    return _read(sorted(glob.glob(os.path.join(ROOT, "db", "[0-9]*.sql"))))


def psql(sql):
    r = subprocess.run(["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them", "-At", "-F", "|", "-c", sql],
                       capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit("psql failed: " + r.stderr.strip())
    return [ln.split("|") for ln in r.stdout.strip("\n").split("\n") if ln]


def main(argv):
    verbose = "--verbose" in argv
    if "--lint" in argv or "--lint-new" in argv:
        if "--lint-new" in argv:
            files = _read([a for a in argv[argv.index("--lint-new") + 1:] if a.endswith(".sql")])
        else:
            files = _all_migrations()
        problems = lint(files)
        for p in problems:
            print("LINT  " + p)
        print(f"{len(problems)} migration lint problem(s)")
        return 1 if problems else 0

    files = _all_migrations()
    names = [n for n, _ in files]
    nums = collections.Counter(n.split("_")[0] for n in names)
    known_path = os.path.join(ROOT, "db", "known_drift.txt")
    known = parse_known_drift(_text(known_path)) if os.path.exists(known_path) else {}
    live_tables = {tuple(r) for r in psql("select table_schema,table_name from information_schema.tables where table_schema in ('them','auth_service')")}
    live_cols = {tuple(r) for r in psql("select table_schema,table_name,column_name from information_schema.columns where table_schema in ('them','auth_service')")}
    recorded = {r[0].split("_")[0] for r in psql("select version from them.schema_migrations")}
    print(f"{len(files)} migration files; {sum(1 for n in names if n.split('_')[0] in recorded)} have a schema_migrations row (matched by number)")
    print("duplicate numbers:", ", ".join(sorted(k for k, v in nums.items() if v > 1)) or "none")
    ints = sorted(int(n) for n in nums if n.isdigit())
    print("gaps:", ", ".join(str(n) for n in range(ints[0], ints[-1] + 1) if n not in ints) or "none")

    unexplained, explained = find_drift(files, live_tables, live_cols, known)
    for name, items in unexplained.items():
        print(f"MISSING  {name}: " + ", ".join(describe(o) for o, _ in items[:4]))
    if verbose:
        for name, items in explained.items():
            for obj, why in items:
                print(f"ok       {name}: {describe(obj)} ({why})")
    print(f"{len(unexplained)} migration(s) with unexplained missing objects; {sum(len(v) for v in explained.values())} object(s) explained "
          "(dropped later or listed in db/known_drift.txt; --verbose shows them)")
    return 1 if unexplained else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
