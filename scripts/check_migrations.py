#!/usr/bin/env python3.12
"""Read-only drift check: do the objects created by db/NNN_*.sql exist in the running database?  stdlib only.

  python3.12 scripts/check_migrations.py            # needs the stack: uses `docker exec them-postgres psql`

Looks only at `CREATE TABLE` and `ALTER TABLE ... ADD COLUMN` statements, so it reports a migration as
"objects missing" when the object was never created (pending migration) OR was dropped by a later migration
(a false positive — check the later files). It never changes the database.
"""
import collections, glob, os, re, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def psql(sql):
    r = subprocess.run(["docker", "exec", "them-postgres", "psql", "-U", "them", "-d", "them", "-At", "-F", "|", "-c", sql],
                       capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit("psql failed: " + r.stderr.strip())
    return [ln.split("|") for ln in r.stdout.strip("\n").split("\n") if ln]


def main():
    tables = {tuple(r) for r in psql("select table_schema,table_name from information_schema.tables where table_schema in ('them','auth_service')")}
    cols = {tuple(r) for r in psql("select table_schema,table_name,column_name from information_schema.columns where table_schema in ('them','auth_service')")}
    recorded = {r[0].split("_")[0] for r in psql("select version from them.schema_migrations")}
    files = sorted(glob.glob(os.path.join(ROOT, "db", "[0-9]*.sql")))
    names = [os.path.basename(f) for f in files]
    nums = collections.Counter(n.split("_")[0] for n in names)
    print(f"{len(files)} migration files; {sum(1 for n in names if n.split('_')[0] in recorded)} have a schema_migrations row (matched by number)")
    print("duplicate numbers:", ", ".join(sorted(k for k, v in nums.items() if v > 1)) or "none")
    ints = sorted(int(n) for n in nums if n.isdigit())
    print("gaps:", ", ".join(str(n) for n in range(ints[0], ints[-1] + 1) if n not in ints) or "none")
    bad = 0
    for f, name in zip(files, names):
        if "DOWN" in name:
            continue
        text = open(f, encoding="utf-8").read()
        miss = []
        for m in re.finditer(r"CREATE TABLE (?:IF NOT EXISTS )?(?:(\w+)\.)?(\w+)", text, re.I):
            if ((m.group(1) or "them"), m.group(2)) not in tables:
                miss.append(f"table {(m.group(1) or 'them')}.{m.group(2)}")
        for m in re.finditer(r"ALTER TABLE (?:IF EXISTS )?(?:ONLY )?(?:(\w+)\.)?(\w+)\s+ADD COLUMN (?:IF NOT EXISTS )?(\w+)", text, re.I):
            s = m.group(1) or "them"
            if (s, m.group(2)) in tables and (s, m.group(2), m.group(3)) not in cols:
                miss.append(f"column {s}.{m.group(2)}.{m.group(3)}")
        if miss:
            bad += 1
            print(f"MISSING  {name}: {', '.join(miss[:3])}")
    print(f"{bad} migration(s) with objects missing in the live DB (pending, or dropped by a later migration)")


if __name__ == "__main__":
    main()
