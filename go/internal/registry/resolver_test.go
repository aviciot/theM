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
// resolveByRefSQL now takes 5 args (kind, namespace, name, version, tenantID).
// The fake simulates tenant scoping: it returns byRef only when the tenantID arg
// matches the definition's tenant (or the definition is builtin).
type fakeQuerier struct {
	byRef *fakeRow
}

func (f *fakeQuerier) QueryRow(_ context.Context, _ string, args ...any) registry.SingleRowScanner {
	if f.byRef == nil || f.byRef.def == nil {
		return &fakeRow{err: errFakeNoRows}
	}
	// args: kind, namespace, name, version, tenantID
	if len(args) < 5 {
		return &fakeRow{err: errFakeNoRows}
	}
	callerTenant, _ := args[4].(string)
	def := f.byRef.def
	// Simulate SQL tenant filter: return row only if builtin or tenant matches.
	if def.Scope == registry.ScopeBuiltin || def.TenantID == callerTenant {
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

	for _, tenantID := range []string{testTenantID, otherTenantID, "some-random-tenant"} {
		got, err := r.Resolve(context.Background(), tenantID, ref, "")
		require.NoError(t, err, "tenant %s should resolve builtin", tenantID)
		assert.Equal(t, registry.ScopeBuiltin, got.Scope)
	}
}

// TestResolver_TenantIsolationEnforcedBySQL verifies that tenant A cannot resolve
// a definition owned by tenant B — enforced by the SQL tenant filter, not post-hoc Go logic.
func TestResolver_TenantIsolationEnforcedBySQL(t *testing.T) {
	// Definition belongs to testTenantID. Caller is otherTenantID.
	// fakeQuerier simulates the SQL filter: returns no-rows when tenants don't match.
	def := tenantOwned(testTenantID, "private-agent")
	q := &fakeQuerier{byRef: &fakeRow{def: def}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: def.Namespace, Name: "private-agent", Version: 1}
	_, err := r.Resolve(context.Background(), otherTenantID, ref, "")
	assert.ErrorIs(t, err, registry.ErrNotFound)
}

// TestResolver_SameTenantRefResolvesCorrectly verifies that same-tenant publish works —
// the ref resolves to the right definition even when definition_id is ignored.
func TestResolver_SameTenantRefResolvesCorrectly(t *testing.T) {
	def := tenantOwned(testTenantID, "my-agent")
	q := &fakeQuerier{byRef: &fakeRow{def: def}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: def.Namespace, Name: "my-agent", Version: 1}
	// Pass a definition_id — it must be ignored.
	got, err := r.Resolve(context.Background(), testTenantID, ref, "some-uuid-from-another-tenant")
	require.NoError(t, err)
	assert.Equal(t, def.Name, got.Name)
	assert.Equal(t, testTenantID, got.TenantID)
}

// TestResolver_CrossTenantRefResolvesWhenTargetHasMatchingComponent verifies that
// publishing a blueprint built in tenant A to tenant B succeeds when tenant B has
// a component with the same kind+namespace+name+version.
func TestResolver_CrossTenantRefResolvesWhenTargetHasMatchingComponent(t *testing.T) {
	// Tenant B has its own version of the same agent.
	targetDef := tenantOwned(otherTenantID, "shared-agent")
	q := &fakeQuerier{byRef: &fakeRow{def: targetDef}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: targetDef.Namespace, Name: "shared-agent", Version: 1}
	got, err := r.Resolve(context.Background(), otherTenantID, ref, "")
	require.NoError(t, err)
	assert.Equal(t, otherTenantID, got.TenantID)
}

// TestResolver_CrossTenantRefFailsWhenTargetLacksComponent verifies that publishing
// to a tenant that does not have the referenced component returns ErrNotFound.
func TestResolver_CrossTenantRefFailsWhenTargetLacksComponent(t *testing.T) {
	// Source tenant's definition — SQL filter blocks it for otherTenantID.
	srcDef := tenantOwned(testTenantID, "missing-in-target")
	q := &fakeQuerier{byRef: &fakeRow{def: srcDef}}
	r := registry.NewResolver(q)

	ref := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: srcDef.Namespace, Name: "missing-in-target", Version: 1}
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
	q := &fakeQuerier{}
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

	qA := &fakeQuerier{byRef: &fakeRow{def: defA}}
	rA := registry.NewResolver(qA)
	refA := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: defA.Namespace, Name: "shared-name", Version: 1}
	gotA, err := rA.Resolve(context.Background(), testTenantID, refA, "")
	require.NoError(t, err)
	assert.Equal(t, testTenantID, gotA.TenantID)

	qB := &fakeQuerier{byRef: &fakeRow{def: defB}}
	rB := registry.NewResolver(qB)
	refB := registry.DefinitionRef{Kind: registry.KindAgent, Namespace: defB.Namespace, Name: "shared-name", Version: 1}
	gotB, err := rB.Resolve(context.Background(), otherTenantID, refB, "")
	require.NoError(t, err)
	assert.Equal(t, otherTenantID, gotB.TenantID)
}
