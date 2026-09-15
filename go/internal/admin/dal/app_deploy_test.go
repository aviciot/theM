package dal_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin/dal"
)

var errNoRows = errors.New("no rows")

// ── fake querier ──────────────────────────────────────────────────────────────

type deployFakeQuerier struct {
	agentIDs     []string
	agentRows    []agentRowData
	existsResult map[string]existsData
	execErr      error
	execCalls    []string
}

type existsData struct{ id, hash string }

type agentRowData struct {
	id, kind, ns, name string
	version             int
	implType, hash, slug string
}

func (f *deployFakeQuerier) Query(_ context.Context, _ string, args ...any) (dal.RowScanner, error) {
	// First Query call: agentIDsQ (no args beyond sourceAppID) → return agentIDs
	// Second Query call: agentQ (1 arg: slice) → return agentRows
	if len(args) == 1 {
		// agentQ — return agent rows
		rows := make([][]any, len(f.agentRows))
		for i := range f.agentRows {
			rows[i] = nil // signal to use agentFakeRow via a different path
		}
		return &agentQueryRows{rows: f.agentRows}, nil
	}
	// agentIDsQ — return agent IDs
	rows := make([][]any, len(f.agentIDs))
	for i, id := range f.agentIDs {
		rows[i] = []any{id}
	}
	return newStringRows(rows), nil
}

func (f *deployFakeQuerier) QueryRow(_ context.Context, _ string, args ...any) dal.SingleRowScanner {
	// existsQ: 5 scalar args (kind, ns, name, version, tenantID)
	if len(args) == 5 {
		name, _ := args[2].(string)
		if d, ok := f.existsResult[name]; ok {
			return &existsFakeRow{id: d.id, hash: d.hash}
		}
		return &errFakeRow{err: errNoRows}
	}
	// insertCD RETURNING id — return a fake new UUID
	return &existsFakeRow{id: "new-uuid-generated", hash: ""}
}

func (f *deployFakeQuerier) Exec(_ context.Context, sql string, _ ...any) error {
	f.execCalls = append(f.execCalls, sql)
	return f.execErr
}

func (f *deployFakeQuerier) ExecReturning(_ context.Context, _ string, _ ...any) dal.SingleRowScanner {
	return &errFakeRow{err: errNoRows}
}

// ── row fakes ─────────────────────────────────────────────────────────────────

type errFakeRow struct{ err error }

func (r *errFakeRow) Scan(_ ...any) error { return r.err }

type existsFakeRow struct{ id, hash string }

func (r *existsFakeRow) Scan(dest ...any) error {
	if len(dest) >= 1 {
		if sp, ok := dest[0].(*string); ok {
			*sp = r.id
		}
	}
	if len(dest) >= 2 {
		if sp, ok := dest[1].(*string); ok {
			*sp = r.hash
		}
	}
	return nil
}

// agentFakeRow fills the 35-column agent details scan with minimal real data.
type agentFakeRow struct{ ag agentRowData }

func (r *agentFakeRow) Scan(dest ...any) error {
	// Indices matching agentQ column order in app_deploy.go:
	// 0:id 1:kind 2:ns 3:name 4:version 5:cdDisplayName 6:cdDesc 7:implType
	// 8:configSchema 9:defaultConfig 10:capabilities 11:inputSchemaCd
	// 12:outputSchema 13:credSchema 14:status 15:hash 16:enabled 17:publishedAt
	// 18:slug 19:aDisplayName 20:aDesc 21:transport 22:endpointURL
	// 23:aInputSchema 24:timeoutSec 25:maxConc 26:maxRetry
	// 27:agentCard 28:agentCardURL 29:skills 30:streaming 31:push
	// 32:tags 33:icon 34:category
	type setter func(any)
	sets := []func(any){
		func(d any) { setStr(d, r.ag.id) },
		func(d any) { setStr(d, r.ag.kind) },
		func(d any) { setStr(d, r.ag.ns) },
		func(d any) { setStr(d, r.ag.name) },
		func(d any) { setInt(d, r.ag.version) },
		func(d any) { setStr(d, "") },        // cdDisplayName
		func(d any) { setStr(d, "") },        // cdDesc
		func(d any) { setStr(d, r.ag.implType) },
		func(d any) { setBytes(d, []byte("{}")) }, // configSchema
		func(d any) { setBytes(d, []byte("{}")) }, // defaultConfig
		func(d any) { setBytes(d, []byte("[]")) }, // capabilities
		func(d any) { setBytes(d, []byte("{}")) }, // inputSchemaCd
		func(d any) { setBytes(d, []byte("{}")) }, // outputSchema
		func(d any) { setBytes(d, []byte("[]")) }, // credSchema
		func(d any) { setStr(d, "published") },    // status
		func(d any) { setStr(d, r.ag.hash) },      // hash
		func(d any) { setBool(d, true) },           // enabled
		func(d any) {},                             // publishedAt (*time.Time) — leave nil
		func(d any) { setStr(d, r.ag.slug) },       // slug
		func(d any) { setStr(d, "") },              // aDisplayName
		func(d any) { setStr(d, "") },              // aDesc
		func(d any) { setStr(d, "a2a_http") },      // transport
		func(d any) {},                             // endpointURL (*string) — leave nil
		func(d any) { setBytes(d, []byte("{}")) }, // aInputSchema
		func(d any) { setInt(d, 30) },              // timeoutSec
		func(d any) { setInt(d, 5) },               // maxConc
		func(d any) { setInt(d, 3) },               // maxRetry
		func(d any) { setBytes(d, []byte("{}")) }, // agentCard
		func(d any) {},                             // agentCardURL (*string)
		func(d any) { setBytes(d, []byte("[]")) }, // skills
		func(d any) { setBool(d, true) },           // streaming
		func(d any) { setBool(d, false) },          // push
		func(d any) {},                             // tags ([]string)
		func(d any) {},                             // icon (*string)
		func(d any) {},                             // category (*string)
	}
	for i, s := range sets {
		if i >= len(dest) {
			break
		}
		s(dest[i])
	}
	return nil
}

func setStr(d any, v string) {
	if sp, ok := d.(*string); ok {
		*sp = v
	}
}
func setInt(d any, v int) {
	if ip, ok := d.(*int); ok {
		*ip = v
	}
}
func setBool(d any, v bool) {
	if bp, ok := d.(*bool); ok {
		*bp = v
	}
}
func setBytes(d any, v []byte) {
	if bp, ok := d.(*[]byte); ok {
		*bp = v
	}
}

// ── fakeRows ─────────────────────────────────────────────────────────────────

// agentQueryRows implements dal.RowScanner for the agentQ multi-row result.
type agentQueryRows struct {
	rows []agentRowData
	pos  int
}

func (r *agentQueryRows) Next() bool  { return r.pos < len(r.rows) }
func (r *agentQueryRows) Close() error { return nil }
func (r *agentQueryRows) Err() error  { return nil }
func (r *agentQueryRows) Scan(dest ...any) error {
	if r.pos >= len(r.rows) {
		return errNoRows
	}
	row := &agentFakeRow{ag: r.rows[r.pos]}
	r.pos++
	return row.Scan(dest...)
}

type stringRows struct {
	rows [][]any
	pos  int
}

func newStringRows(rows [][]any) *stringRows { return &stringRows{rows: rows} }
func (r *stringRows) Next() bool              { return r.pos < len(r.rows) }
func (r *stringRows) Close() error            { return nil }
func (r *stringRows) Err() error              { return nil }
func (r *stringRows) Scan(dest ...any) error {
	if r.pos >= len(r.rows) {
		return errNoRows
	}
	row := r.rows[r.pos]
	r.pos++
	for i, d := range dest {
		if i >= len(row) {
			break
		}
		setStr(d, row[i].(string))
	}
	return nil
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestCopyAgentsForDeploy_NoAgents_ReturnsEmpty(t *testing.T) {
	q := &deployFakeQuerier{}
	db := dal.NewDB(q)
	result, err := db.CopyAgentsForDeploy(context.Background(), "app-1", "tenant-target")
	require.NoError(t, err)
	assert.Empty(t, result.CopiedSlugs)
	assert.Empty(t, result.ReusedSlugs)
	assert.Empty(t, result.ConflictSlugs)
	assert.Empty(t, result.IDMap)
}

func TestCopyAgentsForDeploy_ExternalAgent_Reused_NoConflict(t *testing.T) {
	q := &deployFakeQuerier{
		agentIDs: []string{"agent-uuid-1"},
		agentRows: []agentRowData{
			{id: "agent-uuid-1", kind: "agent", ns: "ns", name: "my-agent",
				version: 1, implType: "a2a_http", hash: "hash-abc", slug: "my-agent"},
		},
		existsResult: map[string]existsData{
			"my-agent": {id: "target-uuid-1", hash: "hash-abc"},
		},
	}
	db := dal.NewDB(q)
	result, err := db.CopyAgentsForDeploy(context.Background(), "app-1", "tenant-target")
	require.NoError(t, err)
	assert.Equal(t, []string{"my-agent"}, result.ReusedSlugs)
	assert.Empty(t, result.CopiedSlugs)
	assert.Empty(t, result.ConflictSlugs)
	assert.Equal(t, "target-uuid-1", result.IDMap["agent-uuid-1"])
}

func TestCopyAgentsForDeploy_ExternalAgent_Reused_Conflict(t *testing.T) {
	q := &deployFakeQuerier{
		agentIDs: []string{"agent-uuid-1"},
		agentRows: []agentRowData{
			{id: "agent-uuid-1", kind: "agent", ns: "ns", name: "my-agent",
				version: 1, implType: "a2a_http", hash: "hash-abc", slug: "my-agent"},
		},
		existsResult: map[string]existsData{
			"my-agent": {id: "target-uuid-1", hash: "hash-DIFFERENT"},
		},
	}
	db := dal.NewDB(q)
	result, err := db.CopyAgentsForDeploy(context.Background(), "app-1", "tenant-target")
	require.Error(t, err)
	require.True(t, errors.Is(err, dal.ErrDeployConflict), "expected ErrDeployConflict, got: %v", err)
	assert.Equal(t, []string{"my-agent"}, result.ConflictSlugs)
	// Writes must not have occurred — no exec calls.
	assert.Empty(t, q.execCalls)
}

func TestCopyAgentsForDeploy_CanvasAgent_Copied_ExecutesSpecInsert(t *testing.T) {
	q := &deployFakeQuerier{
		agentIDs: []string{"canvas-uuid-1"},
		agentRows: []agentRowData{
			{id: "canvas-uuid-1", kind: "agent", ns: "ns", name: "canvas-agent",
				version: 1, implType: "canvas_a2a", hash: "hash-xyz", slug: "canvas-agent"},
		},
		existsResult: map[string]existsData{}, // not in target
	}
	db := dal.NewDB(q)
	result, err := db.CopyAgentsForDeploy(context.Background(), "app-1", "tenant-target")
	require.NoError(t, err)
	assert.Equal(t, []string{"canvas-agent"}, result.CopiedSlugs)
	assert.Empty(t, result.ReusedSlugs)
	// Verify that Exec was called for agent_definitions and agent_runtime_specs inserts.
	require.GreaterOrEqual(t, len(q.execCalls), 2, "expected at least insertAgentDef + insertSpec exec calls")
}
