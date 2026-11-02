#!/usr/bin/env python3.12
"""
test_46_check_migrations_unit.py — unit tests for scripts/check_migrations.py (NO stack needed).

The key case is migration 125 (backlog F-135): a committed migration whose column is missing in the
live database must be reported, while objects dropped by a later migration or listed in
db/known_drift.txt must not be (so a report always means something real).

Usage: python3.12 scripts/tests/test_46_check_migrations_unit.py     (exit code 0 = pass)
"""
import os, sys, unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
import check_migrations as cm  # noqa: E402

M125 = ("125_gateway_user_id.sql", "ALTER TABLE them.gateway_requests\nADD COLUMN user_id INTEGER;\nCREATE INDEX i ON them.gateway_requests(user_id);")
LIVE_TABLES = {("them", "gateway_requests"), ("them", "runs")}


class Declared(unittest.TestCase):
    def test_create_table_and_add_column(self):
        text = "CREATE TABLE IF NOT EXISTS them.a (id int);\nCREATE TABLE b (id int);\nALTER TABLE ONLY them.a ADD COLUMN IF NOT EXISTS c text;"
        self.assertEqual(cm.declared(text), [("table", "them", "a"), ("table", "them", "b"), ("column", "them", "a", "c")])

    def test_drops(self):
        text = "DROP TABLE IF EXISTS them.x, them.y CASCADE;\nALTER TABLE them.orchestrators\n    DROP COLUMN IF EXISTS a2a_exposed;"
        self.assertEqual(cm.dropped(text), {("table", "them", "x"), ("table", "them", "y"), ("column", "them", "orchestrators", "a2a_exposed")})


class Drift(unittest.TestCase):
    def test_missing_column_is_reported_like_migration_125(self):
        unexplained, explained = cm.find_drift([M125], LIVE_TABLES, {("them", "gateway_requests", "id")}, {})
        self.assertEqual([o for o, _ in unexplained["125_gateway_user_id.sql"]], [("column", "them", "gateway_requests", "user_id")])
        self.assertFalse(explained)

    def test_present_column_is_fine(self):
        unexplained, _ = cm.find_drift([M125], LIVE_TABLES, {("them", "gateway_requests", "user_id")}, {})
        self.assertFalse(unexplained)

    def test_dropped_by_a_later_migration_is_explained(self):
        later = ("130_drop.sql", "ALTER TABLE them.gateway_requests DROP COLUMN user_id;")
        unexplained, explained = cm.find_drift([M125, later], LIVE_TABLES, set(), {})
        self.assertFalse(unexplained)
        self.assertIn("dropped by a later migration", explained["125_gateway_user_id.sql"][0][1])

    def test_dropped_table_explains_its_columns_and_itself(self):
        files = [("016_g.sql", "CREATE TABLE them.app_nodes (id int);"), ("017_d.sql", "DROP TABLE IF EXISTS them.app_nodes CASCADE;")]
        unexplained, _ = cm.find_drift(files, LIVE_TABLES, set(), {})
        self.assertFalse(unexplained)

    def test_a_drop_in_an_EARLIER_migration_does_not_hide_a_later_create(self):
        files = [("010_d.sql", "DROP TABLE IF EXISTS them.t;"), ("020_c.sql", "CREATE TABLE them.t (id int);")]
        unexplained, _ = cm.find_drift(files, LIVE_TABLES, set(), {})
        self.assertIn("020_c.sql", unexplained)

    def test_known_drift_is_explained_with_its_reason(self):
        known = cm.parse_known_drift("# c\n125_gateway_user_id.sql | column them.gateway_requests.user_id | on purpose\n")
        unexplained, explained = cm.find_drift([M125], LIVE_TABLES, set(), known)
        self.assertFalse(unexplained)
        self.assertIn("on purpose", explained["125_gateway_user_id.sql"][0][1])

    def test_missing_table_is_reported_once_not_per_column(self):
        files = [("050_n.sql", "CREATE TABLE them.nope (id int);\nALTER TABLE them.nope ADD COLUMN x int;")]
        unexplained, _ = cm.find_drift(files, LIVE_TABLES, set(), {})
        self.assertEqual([o for o, _ in unexplained["050_n.sql"]], [("table", "them", "nope")])

    def test_down_scripts_are_ignored(self):
        unexplained, _ = cm.find_drift([("110_x_DOWN.sql", "CREATE TABLE them.nope (id int);")], LIVE_TABLES, set(), {})
        self.assertFalse(unexplained)


class Lint(unittest.TestCase):
    def test_new_migration_without_self_record_fails(self):
        self.assertEqual(len(cm.lint([("127_x.sql", "CREATE TABLE them.t (id int);")])), 1)

    def test_new_migration_with_self_record_passes(self):
        text = "CREATE TABLE them.t (id int);\nINSERT INTO them.schema_migrations (version, description, applied_at) VALUES ('127_x','d',NOW());"
        self.assertEqual(cm.lint([("127_x.sql", text)]), [])

    def test_old_migrations_and_down_scripts_are_exempt(self):
        self.assertEqual(cm.lint([("125_gateway_user_id.sql", "ALTER TABLE a ADD COLUMN b int;"), ("200_x_DOWN.sql", "DROP TABLE a;")]), [])

    def test_every_migration_from_127_in_the_repo_records_itself(self):
        self.assertEqual(cm.lint(cm._all_migrations()), [])


if __name__ == "__main__":
    unittest.main(verbosity=1)
