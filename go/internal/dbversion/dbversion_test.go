package dbversion

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func logger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestVerify_CurrentSchema(t *testing.T) {
	var buf bytes.Buffer
	err := Verify(context.Background(), func(_ context.Context, v string) (bool, error) { return v == Expected, nil }, logger(&buf))
	if err != nil || !strings.Contains(buf.String(), "database schema is current") {
		t.Errorf("err=%v log=%s", err, buf.String())
	}
}

// The db/125 situation: the code is newer than the database.
func TestVerify_BehindIsAnErrorLogAndErrBehind(t *testing.T) {
	var buf bytes.Buffer
	err := Verify(context.Background(), func(context.Context, string) (bool, error) { return false, nil }, logger(&buf))
	if !errors.Is(err, ErrBehind) {
		t.Fatalf("err = %v, want ErrBehind", err)
	}
	out := buf.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "BEHIND") || !strings.Contains(out, Expected) {
		t.Errorf("must log an ERROR naming the missing migration:\n%s", out)
	}
}

// A broken check (missing table, no permission, connection) is only a warning and never an error.
func TestVerify_CheckFailureNeverBlocks(t *testing.T) {
	var buf bytes.Buffer
	err := Verify(context.Background(), func(context.Context, string) (bool, error) { return false, errors.New("permission denied") }, logger(&buf))
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("expected a warning:\n%s", buf.String())
	}
	_ = io.Discard
}

// A stale database only logs by default; THE_M_REQUIRE_MIGRATIONS=true turns it into a startup failure.
func TestGate_RequireMigrationsSwitch(t *testing.T) {
	behind := errors.New("behind")
	t.Setenv("THE_M_REQUIRE_MIGRATIONS", "")
	if gate(behind) != nil {
		t.Error("default: a behind database must not stop the service")
	}
	t.Setenv("THE_M_REQUIRE_MIGRATIONS", "true")
	if !errors.Is(gate(behind), behind) {
		t.Error("THE_M_REQUIRE_MIGRATIONS=true: the service must refuse to start")
	}
	if gate(nil) != nil {
		t.Error("a current database never fails")
	}
}

// Expected must name the newest migration file (run from a full checkout; skipped when db/ is not around,
// e.g. inside the Docker build context).
func TestExpectedMatchesNewestMigration(t *testing.T) {
	dir, _ := os.Getwd()
	var db string
	for i := 0; i < 6; i++ {
		if st, err := os.Stat(filepath.Join(dir, "db")); err == nil && st.IsDir() {
			if m, _ := filepath.Glob(filepath.Join(dir, "db", "[0-9]*.sql")); len(m) > 0 {
				db = filepath.Join(dir, "db")
				break
			}
		}
		dir = filepath.Dir(dir)
	}
	if db == "" {
		t.Skip("db/ not found (not a full checkout)")
	}
	files, _ := filepath.Glob(filepath.Join(db, "[0-9]*.sql"))
	var names []string
	for _, f := range files {
		if n := filepath.Base(f); !strings.Contains(n, "DOWN") {
			names = append(names, strings.TrimSuffix(n, ".sql"))
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.SplitN(names[i], "_", 2)[0])
		b, _ := strconv.Atoi(strings.SplitN(names[j], "_", 2)[0])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	if newest := names[len(names)-1]; Expected != newest {
		t.Errorf("dbversion.Expected = %q but the newest migration is %q — bump Expected in internal/dbversion/dbversion.go", Expected, newest)
	}
}
