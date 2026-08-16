// Package registry provides the design-time component definition resolver for the-M.
//
// A ComponentDefinition is a versioned, immutable record that describes how a component
// (agent, orchestrator, middleware, tool, or entry_point) is structured and configured.
// Definitions are stored in them.component_definitions and are referenced by
// ApplicationDefinition JSON during the publish/compile pipeline.
//
// Resolution order (per architecture spec Section 14):
//  1. UUID fast path — if a definitionID is provided and found in this environment.
//  2. Portable identity fallback — (kind, namespace, name, version) tuple.
package registry

import "time"

// ComponentKind is the discriminator for component definition types.
type ComponentKind string

const (
	KindAgent        ComponentKind = "agent"
	KindOrchestrator ComponentKind = "orchestrator"
	KindMiddleware   ComponentKind = "middleware"
	KindTool         ComponentKind = "tool"
	KindEntryPoint   ComponentKind = "entry_point"
)

// ComponentScope distinguishes platform-owned vs tenant-authored definitions.
type ComponentScope string

const (
	ScopeBuiltin ComponentScope = "builtin"
	ScopeTenant  ComponentScope = "tenant"
)

// ComponentStatus is the lifecycle status of a definition version.
type ComponentStatus string

const (
	StatusDraft      ComponentStatus = "draft"
	StatusPublished  ComponentStatus = "published"
	StatusDeprecated ComponentStatus = "deprecated"
)

// DefinitionRef is the portable identity of a component definition.
// UUIDs do not survive environment boundaries; this tuple is the stable key.
type DefinitionRef struct {
	Kind      ComponentKind `json:"kind"`
	Namespace string        `json:"namespace"`
	Name      string        `json:"name"`
	Version   int           `json:"version"`
}

// ComponentDefinition is the resolved, full definition from the registry.
type ComponentDefinition struct {
	ID                 string          `json:"id"`
	Kind               ComponentKind   `json:"kind"`
	Namespace          string          `json:"namespace"`
	Name               string          `json:"name"`
	Version            int             `json:"version"`
	DisplayName        string          `json:"display_name"`
	Description        string          `json:"description"`
	ImplementationType string          `json:"implementation_type"`
	ConfigurationSchema map[string]any `json:"configuration_schema"`
	DefaultConfig      map[string]any  `json:"default_config"`
	Capabilities       []string        `json:"capabilities"`
	InputSchema        map[string]any  `json:"input_schema,omitempty"`
	OutputSchema       map[string]any  `json:"output_schema,omitempty"`
	CredentialSchema   []CredentialSlot `json:"credential_schema"`
	Scope              ComponentScope  `json:"scope"`
	TenantID           string          `json:"tenant_id,omitempty"`
	Status             ComponentStatus `json:"status"`
	ContentHash        string          `json:"content_hash"`
	Enabled            bool            `json:"enabled"`
	CreatedAt          time.Time       `json:"created_at"`
	PublishedAt        *time.Time      `json:"published_at,omitempty"`
}

// CredentialSlot describes one secret slot in a component definition.
type CredentialSlot struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}
