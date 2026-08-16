package registry_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/registry"
)

// ─── test constants ────────────────────────────────────────────────────────────

const (
	testTenantID  = "00000000-0000-0000-0000-000000000001"
	otherTenantID = "00000000-0000-0000-0000-000000000002"
)

// ─── fake row ─────────────────────────────────────────────────────────────────

// fakeRow implements registry.SingleRowScanner.
// It holds a pre-built ComponentDefinition (or an error) and populates
// all 21 Scan destinations in the order expected by scanDefinition.
type fakeRow struct {
	def *registry.ComponentDefinition
	err error
}

var errFakeNoRows = errors.New("no rows in result set")

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.def == nil {
		return errFakeNoRows
	}
	d := r.def

	// marshal JSONB fields
	configSchema, _ := json.Marshal(d.ConfigurationSchema)
	defaultConfig, _ := json.Marshal(d.DefaultConfig)
	capabilities, _ := json.Marshal(d.Capabilities)
	var inputSchema, outputSchema []byte
	if d.InputSchema != nil {
		inputSchema, _ = json.Marshal(d.InputSchema)
	}
	if d.OutputSchema != nil {
		outputSchema, _ = json.Marshal(d.OutputSchema)
	}
	credSchema, _ := json.Marshal(d.CredentialSchema)

	// Must match the order in scanDefinition (pgx.go):
	// id, kind, namespace, name, version, display_name, description, implementation_type,
	// configSchemaRaw, defaultConfigRaw, capabilitiesRaw,
	// inputSchemaRaw, outputSchemaRaw, credSchemaRaw,
	// scope, tenantID, status, content_hash, enabled, created_at, published_at
	vals := []any{
		d.ID,
		string(d.Kind),
		d.Namespace,
		d.Name,
		d.Version,
		d.DisplayName,
		d.Description,
		d.ImplementationType,
		configSchema,
		defaultConfig,
		capabilities,
		inputSchema,
		outputSchema,
		credSchema,
		string(d.Scope),
		d.TenantID,
		string(d.Status),
		d.ContentHash,
		d.Enabled,
		d.CreatedAt,
		d.PublishedAt,
	}

	if len(dest) != len(vals) {
		return errors.New("fakeRow: Scan dest length mismatch")
	}
	for i, v := range vals {
		if err := assignTo(dest[i], v); err != nil {
			return err
		}
	}
	return nil
}

// assignTo sets *dest to v using type switches.
func assignTo(dest any, v any) error {
	switch d := dest.(type) {
	case *string:
		if v == nil {
			*d = ""
			return nil
		}
		if s, ok := v.(string); ok {
			*d = s
			return nil
		}
	case *int:
		if n, ok := v.(int); ok {
			*d = n
			return nil
		}
	case *bool:
		if b, ok := v.(bool); ok {
			*d = b
			return nil
		}
	case *[]byte:
		if b, ok := v.([]byte); ok {
			*d = b
			return nil
		}
		if v == nil {
			*d = nil
			return nil
		}
	case *time.Time:
		if t, ok := v.(time.Time); ok {
			*d = t
			return nil
		}
	case **time.Time:
		if v == nil {
			*d = nil
			return nil
		}
		if t, ok := v.(*time.Time); ok {
			*d = t
			return nil
		}
	case *registry.ComponentKind:
		if s, ok := v.(string); ok {
			*d = registry.ComponentKind(s)
			return nil
		}
	case *registry.ComponentScope:
		if s, ok := v.(string); ok {
			*d = registry.ComponentScope(s)
			return nil
		}
	case *registry.ComponentStatus:
		if s, ok := v.(string); ok {
			*d = registry.ComponentStatus(s)
			return nil
		}
	}
	return errors.New("fakeRow: unsupported type assignment")
}

// ─── fake DB ──────────────────────────────────────────────────────────────────

// fakeQuerier implements registry.DBQuerier.
// It maps query SQL to a fakeRow: byID for resolveByIDSQL, byRef for resolveByRefSQL.
type fakeQuerier struct {
	byID  *fakeRow // returned when id-based query detected (arg count == 1)
	byRef *fakeRow // returned when ref-based query detected (arg count == 4)
}

func (f *fakeQuerier) QueryRow(_ context.Context, _ string, args ...any) registry.SingleRowScanner {
	// Distinguish by number of args:
	// resolveByIDSQL uses 1 arg ($1::uuid)
	// resolveByRefSQL uses 4 args ($1 kind, $2 namespace, $3 name, $4 version)
	if len(args) == 1 {
		if f.byID != nil {
			return f.byID
		}
		return &fakeRow{err: errFakeNoRows}
	}
	if f.byRef != nil {
		return f.byRef
	}
	return &fakeRow{err: errFakeNoRows}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func now() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }
func nowPtr() *time.Time {
	t := now()
	return &t
}

func builtin(name string) *registry.ComponentDefinition {
	return &registry.ComponentDefinition{
		ID:                  "aaaaaaaa-0000-0000-0000-000000000001",
		Kind:                registry.KindOrchestrator,
		Namespace:           "them.builtin",
		Name:                name,
		Version:             1,
		DisplayName:         "Test Builtin",
		Description:         "A builtin definition",
		ImplementationType:  "llm_loop",
		ConfigurationSchema: map[string]any{"type": "object"},
		DefaultConfig:       map[string]any{},
		Capabilities:        []string{"tool.host"},
		CredentialSchema:    []registry.CredentialSlot{},
		Scope:               registry.ScopeBuiltin,
		TenantID:            "",
		Status:              registry.StatusPublished,
		ContentHash:         "abc123",
		Enabled:             true,
		CreatedAt:           now(),
		PublishedAt:         nowPtr(),
	}
}

func tenantOwned(tenantID string, name string) *registry.ComponentDefinition {
	return &registry.ComponentDefinition{
		ID:                  "bbbbbbbb-0000-0000-0000-000000000002",
		Kind:                registry.KindAgent,
		Namespace:           "them.tenant." + tenantID,
		Name:                name,
		Version:             1,
		DisplayName:         "Test Tenant Agent",
		Description:         "",
		ImplementationType:  "a2a_async",
		ConfigurationSchema: map[string]any{},
		DefaultConfig:       map[string]any{},
		Capabilities:        []string{},
		CredentialSchema:    []registry.CredentialSlot{},
		Scope:               registry.ScopeTenant,
		TenantID:            tenantID,
		Status:              registry.StatusPublished,
		ContentHash:         "def456",
		Enabled:             true,
		CreatedAt:           now(),
		PublishedAt:         nowPtr(),
	}
}

// ─── tests ────────────────────────────────────────────────────────────────────

// TestResolver_TenantOwnedDefinitionResolvesForOwner verifies that a tenant-scoped
// definition is accessible to the tenant that owns it.
func TestResolver_TenantOwnedDefinitionResolvesForOwner(t *testing.T) {
	def := tenantOwned(testTenantID, "my-agent")
	q := &fakeQuerier{byRef: &fakeRow{def: def}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: def.Namespace, Name: "my-agent", Version: 1}
	got, err := r.Resolve(context.Background(), testTenantID, ref, "")
	require.NoError(t, err)
	assert.Equal(t, def.Name, got.Name)
	assert.Equal(t, testTenantID, got.TenantID)
}

// TestResolver_BuiltinResolvesForAnyTenant verifies that a builtin definition
// is accessible to any tenant regardless of scope.
func TestResolver_BuiltinResolvesForAnyTenant(t *testing.T) {
	def := builtin("llm-orchestrator")
	q := &fakeQuerier{byRef: &fakeRow{def: def}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindOrchestrator, Namespace: "them.builtin", Name: "llm-orchestrator", Version: 1}

	// Any tenant should be able to resolve a builtin.
	for _, tenantID := range []string{testTenantID, otherTenantID, "some-random-tenant"} {
		got, err := r.Resolve(context.Background(), tenantID, ref, "")
		require.NoError(t, err, "tenant %s should resolve builtin", tenantID)
		assert.Equal(t, registry.ScopeBuiltin, got.Scope)
	}
}

// TestResolver_NoCrossTenantResolution verifies that tenant A cannot resolve
// a definition owned by tenant B.
func TestResolver_NoCrossTenantResolution(t *testing.T) {
	// Definition belongs to testTenantID, but caller is otherTenantID.
	def := tenantOwned(testTenantID, "private-agent")
	q := &fakeQuerier{byRef: &fakeRow{def: def}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: def.Namespace, Name: "private-agent", Version: 1}
	_, err := r.Resolve(context.Background(), otherTenantID, ref, "")
	assert.ErrorIs(t, err, registry.ErrNotFound)
}

// TestResolver_ExactVersionResolution verifies that the version is forwarded
// to the DAL and the correct definition is returned.
func TestResolver_ExactVersionResolution(t *testing.T) {
	def := builtin("llm-orchestrator")
	def.Version = 3
	q := &fakeQuerier{byRef: &fakeRow{def: def}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindOrchestrator, Namespace: "them.builtin", Name: "llm-orchestrator", Version: 3}
	got, err := r.Resolve(context.Background(), testTenantID, ref, "")
	require.NoError(t, err)
	assert.Equal(t, 3, got.Version)
}

// TestResolver_MissingDefinitionReturnsErrNotFound verifies that a missing
// definition returns ErrNotFound and nothing else.
func TestResolver_MissingDefinitionReturnsErrNotFound(t *testing.T) {
	q := &fakeQuerier{} // no rows for either path
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: "them.builtin", Name: "nonexistent", Version: 1}
	_, err := r.Resolve(context.Background(), testTenantID, ref, "")
	assert.ErrorIs(t, err, registry.ErrNotFound)
}

// TestResolver_DisabledDefinitionReturnsErrDisabled verifies that a disabled
// definition (enabled=false) cannot be resolved.
func TestResolver_DisabledDefinitionReturnsErrDisabled(t *testing.T) {
	def := builtin("disabled-tool")
	def.Enabled = false
	q := &fakeQuerier{byRef: &fakeRow{def: def}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindOrchestrator, Namespace: "them.builtin", Name: "disabled-tool", Version: 1}
	_, err := r.Resolve(context.Background(), testTenantID, ref, "")
	assert.ErrorIs(t, err, registry.ErrDisabled)
}

// TestResolver_DeprecatedDefinition_ResolveSucceeds verifies that Resolve does NOT
// block deprecated definitions (palette queries are allowed to see them).
func TestResolver_DeprecatedDefinition_ResolveSucceeds(t *testing.T) {
	def := builtin("old-orch")
	def.Status = registry.StatusDeprecated
	q := &fakeQuerier{byRef: &fakeRow{def: def}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindOrchestrator, Namespace: "them.builtin", Name: "old-orch", Version: 1}
	got, err := r.Resolve(context.Background(), testTenantID, ref, "")
	require.NoError(t, err)
	assert.Equal(t, registry.StatusDeprecated, got.Status)
}

// TestResolver_DeprecatedDefinition_ResolveForPublishReturnsErrDeprecated verifies that
// ResolveForPublish blocks deprecated definitions at the publish/compile pipeline.
func TestResolver_DeprecatedDefinition_ResolveForPublishReturnsErrDeprecated(t *testing.T) {
	def := builtin("old-orch")
	def.Status = registry.StatusDeprecated
	q := &fakeQuerier{byRef: &fakeRow{def: def}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindOrchestrator, Namespace: "them.builtin", Name: "old-orch", Version: 1}
	_, err := r.ResolveForPublish(context.Background(), testTenantID, ref, "")
	assert.ErrorIs(t, err, registry.ErrDeprecated)
}

// TestResolver_UUIDFastPathHitsBeforeRef verifies that when a definitionID is provided
// and found via UUID lookup, the ref lookup is not used.
func TestResolver_UUIDFastPathHitsBeforeRef(t *testing.T) {
	idDef := builtin("id-path-orch")
	idDef.ID = "cccccccc-0000-0000-0000-000000000003"
	idDef.Name = "id-path-orch"

	refDef := builtin("ref-path-orch")
	refDef.ID = "dddddddd-0000-0000-0000-000000000004"
	refDef.Name = "ref-path-orch"

	// byID returns idDef; byRef returns refDef (should not be reached).
	q := &fakeQuerier{
		byID:  &fakeRow{def: idDef},
		byRef: &fakeRow{def: refDef},
	}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindOrchestrator, Namespace: "them.builtin", Name: "ref-path-orch", Version: 1}
	got, err := r.Resolve(context.Background(), testTenantID, ref, idDef.ID)
	require.NoError(t, err)
	// Should have come from the UUID fast path, not the ref path.
	assert.Equal(t, "id-path-orch", got.Name)
}

// TestResolver_UUIDMissFallsThroughToRef verifies that when the UUID lookup fails,
// the resolver falls through to the portable ref lookup.
func TestResolver_UUIDMissFallsThroughToRef(t *testing.T) {
	refDef := builtin("ref-fallback-orch")

	// byID returns error (UUID miss); byRef returns refDef.
	q := &fakeQuerier{
		byID:  &fakeRow{err: errFakeNoRows},
		byRef: &fakeRow{def: refDef},
	}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindOrchestrator, Namespace: "them.builtin", Name: "ref-fallback-orch", Version: 1}
	got, err := r.Resolve(context.Background(), testTenantID, ref, "some-nonexistent-uuid")
	require.NoError(t, err)
	assert.Equal(t, "ref-fallback-orch", got.Name)
}

// TestResolver_ResolveForPublish_PublishedDefinitionSucceeds verifies the happy path
// for the publish/compile pipeline with a published definition.
func TestResolver_ResolveForPublish_PublishedDefinitionSucceeds(t *testing.T) {
	def := tenantOwned(testTenantID, "publish-agent")
	def.Status = registry.StatusPublished
	q := &fakeQuerier{byRef: &fakeRow{def: def}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: def.Namespace, Name: "publish-agent", Version: 1}
	got, err := r.ResolveForPublish(context.Background(), testTenantID, ref, "")
	require.NoError(t, err)
	assert.Equal(t, registry.StatusPublished, got.Status)
}

// TestResolver_TwoTenantsIndependent verifies that two tenants with definitions of
// the same name are resolved independently and cannot cross-contaminate.
func TestResolver_TwoTenantsIndependent(t *testing.T) {
	defA := tenantOwned(testTenantID, "shared-name")
	defB := tenantOwned(otherTenantID, "shared-name")

	// Simulate: tenant A's definition returned when querying with tenantA's namespace.
	qA := &fakeQuerier{byRef: &fakeRow{def: defA}}
	rA := registry.NewResolver(qA)

	refA := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: defA.Namespace, Name: "shared-name", Version: 1}
	gotA, err := rA.Resolve(context.Background(), testTenantID, refA, "")
	require.NoError(t, err)
	assert.Equal(t, testTenantID, gotA.TenantID)

	// Simulate: tenant B's definition returned when querying with tenantB's namespace.
	qB := &fakeQuerier{byRef: &fakeRow{def: defB}}
	rB := registry.NewResolver(qB)

	refB := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: defB.Namespace, Name: "shared-name", Version: 1}
	gotB, err := rB.Resolve(context.Background(), otherTenantID, refB, "")
	require.NoError(t, err)
	assert.Equal(t, otherTenantID, gotB.TenantID)
}
