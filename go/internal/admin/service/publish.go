package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/registry"
)

// ── RegistryResolver interface ────────────────────────────────────────────────

// RegistryResolver is the subset of registry.Resolver needed by DefinitionService.
// Defined here as a local interface to keep the service package import cycle-free
// and to enable easy mocking in tests.
type RegistryResolver interface {
	ResolveForPublish(ctx context.Context, tenantID string, ref registry.DefinitionRef, definitionID string) (*registry.ComponentDefinition, error)
	Resolve(ctx context.Context, tenantID string, ref registry.DefinitionRef, definitionID string) (*registry.ComponentDefinition, error)
}

// ── ValidationReport ─────────────────────────────────────────────────────────

// ValidationReport is the result of ValidateDefinition.
type ValidationReport struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// ValidationError is one entry in ValidationReport.Errors.
type ValidationError struct {
	InstanceID string `json:"instance_id,omitempty"`
	Field      string `json:"field,omitempty"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

// ── appDefinitionDoc — internal parse target ──────────────────────────────────

// appDefinitionDoc is the parsed in-memory form of the definition JSON.
type appDefinitionDoc struct {
	Components  []componentInstance  `json:"components"`
	EntryPoints []entryPointInstance `json:"entry_points"`
	Connections []connectionDef      `json:"connections"`
}

type componentInstance struct {
	InstanceID    string                 `json:"instance_id"`
	Name          string                 `json:"name"`
	DefinitionRef registry.DefinitionRef `json:"definition_ref"`
	DefinitionID  string                 `json:"definition_id,omitempty"`
	Config        map[string]any         `json:"config"`
}

type entryPointInstance struct {
	InstanceID string `json:"instance_id"`
	Slug       string `json:"slug"`
	Protocol   string `json:"protocol"`
	Root       string `json:"root"` // instance_id of the root orchestrator
}

type connectionDef struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"` // "entry"|"delegation"|"tool"
}

// ── ErrValidationFailed sentinel ──────────────────────────────────────────────

// errValidationFailed is a typed error carrying a ValidationReport.
// It wraps ErrValidation so callers can use errors.Is(err, service.ErrValidation).
type errValidationFailed struct {
	Report ValidationReport
}

func (e *errValidationFailed) Error() string { return "definition validation failed" }
func (e *errValidationFailed) Unwrap() error { return ErrValidation }

// ── DefinitionService extension ───────────────────────────────────────────────

// ValidateDefinition performs full publish-time validation of a definition:
//   - Structural validation (delegated to validateDefinition)
//   - Component registry resolution for every component
//   - Uniqueness of all instance_ids across components + entry_points
//   - Connection source/target references existing instance_ids
//   - Valid entry_point protocol values
//
// Returns a ValidationReport with Valid=true/false + per-item errors.
// Also returns a Go error for hard failures (e.g. definition not found).
func (s *DefinitionService) ValidateDefinition(ctx context.Context, tenantID, appID, defID string) (*ValidationReport, error) {
	def, err := s.GetDefinition(ctx, tenantID, appID, defID)
	if err != nil {
		return nil, err
	}
	return s.validateDoc(ctx, tenantID, def.Definition)
}

// validateDoc runs registry resolution + structural checks on a raw definition.
// Called by both ValidateDefinition and PublishDefinition.
func (s *DefinitionService) validateDoc(ctx context.Context, tenantID string, raw json.RawMessage) (*ValidationReport, error) {
	// Phase-B structural validation first (secret keys, object shape, etc.)
	if err := validateDefinition(raw); err != nil {
		var fe *FieldError
		if errors.As(err, &fe) {
			return &ValidationReport{
				Valid: false,
				Errors: []ValidationError{
					{Code: "structural_error", Message: fe.Message},
				},
			}, nil
		}
		return nil, err
	}

	var doc appDefinitionDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return &ValidationReport{
			Valid: false,
			Errors: []ValidationError{
				{Code: "parse_error", Message: "definition is not valid JSON: " + err.Error()},
			},
		}, nil
	}

	var errs []ValidationError

	// Collect all instance_ids for uniqueness and connection validation.
	instanceIDs := make(map[string]struct{}, len(doc.Components)+len(doc.EntryPoints))

	// Validate components — structural + registry resolution.
	for _, comp := range doc.Components {
		if _, dup := instanceIDs[comp.InstanceID]; dup {
			errs = append(errs, ValidationError{
				InstanceID: comp.InstanceID,
				Code:       "duplicate_instance_id",
				Message:    fmt.Sprintf("duplicate instance_id: %q", comp.InstanceID),
			})
		} else {
			instanceIDs[comp.InstanceID] = struct{}{}
		}

		// Registry resolution (skip if no registry wired — test mode).
		if s.registry != nil {
			_, resolveErr := s.registry.ResolveForPublish(ctx, tenantID, comp.DefinitionRef, comp.DefinitionID)
			if resolveErr != nil {
				code := "component_not_found"
				switch {
				case errors.Is(resolveErr, registry.ErrDisabled):
					code = "component_disabled"
				case errors.Is(resolveErr, registry.ErrDeprecated):
					code = "component_deprecated"
				}
				errs = append(errs, ValidationError{
					InstanceID: comp.InstanceID,
					Field:      "definition_ref",
					Code:       code,
					Message:    fmt.Sprintf("component %q: %v", comp.InstanceID, resolveErr),
				})
			}
		}
	}

	// Validate entry_points.
	validProtocols := map[string]struct{}{
		"websocket": {},
		"sse":       {},
		"voice":     {},
		"webrtc":    {},
		"a2a":       {},
	}
	for _, ep := range doc.EntryPoints {
		if _, dup := instanceIDs[ep.InstanceID]; dup {
			errs = append(errs, ValidationError{
				InstanceID: ep.InstanceID,
				Code:       "duplicate_instance_id",
				Message:    fmt.Sprintf("duplicate instance_id: %q", ep.InstanceID),
			})
		} else {
			instanceIDs[ep.InstanceID] = struct{}{}
		}

		if _, ok := validProtocols[ep.Protocol]; !ok {
			errs = append(errs, ValidationError{
				InstanceID: ep.InstanceID,
				Field:      "protocol",
				Code:       "invalid_protocol",
				Message:    fmt.Sprintf("entry_point %q: invalid protocol %q", ep.InstanceID, ep.Protocol),
			})
		}
	}

	// Validate connections — sources and targets must reference known instance_ids.
	for _, conn := range doc.Connections {
		if conn.Source != "" {
			if _, ok := instanceIDs[conn.Source]; !ok {
				errs = append(errs, ValidationError{
					Field:   "connections",
					Code:    "dangling_connection",
					Message: fmt.Sprintf("connection source %q is not a known instance_id", conn.Source),
				})
			}
		}
		if conn.Target != "" {
			if _, ok := instanceIDs[conn.Target]; !ok {
				errs = append(errs, ValidationError{
					Field:   "connections",
					Code:    "dangling_connection",
					Message: fmt.Sprintf("connection target %q is not a known instance_id", conn.Target),
				})
			}
		}
	}

	return &ValidationReport{Valid: len(errs) == 0, Errors: errs}, nil
}

// ── PublishDefinition ─────────────────────────────────────────────────────────

// PublishDefinition validates, compiles projections, then atomically publishes a
// draft application definition.
//
// Steps:
//  1. Fetch the definition — ErrNotFound if missing or wrong tenant.
//  2. Reject if already published (ErrConflict).
//  3. Run full validation — ErrValidation if not valid.
//  4. Resolve all component definitions from the registry.
//  5. Compile and upsert app_orchestrators projection rows.
//  6. Compile and upsert entry_points projection rows.
//  7. Deactivate stale orchestrators + entry_points.
//  8. Mark definition published + update applications.active_definition_id.
//
// Returns the PublishResult on success.
func (s *DefinitionService) PublishDefinition(ctx context.Context, tenantID, appID, defID string) (*dal.PublishResult, error) {
	// 1. Fetch definition.
	def, err := s.GetDefinition(ctx, tenantID, appID, defID)
	if err != nil {
		return nil, err
	}
	if def.Status == "published" {
		return nil, ErrConflict
	}

	// 2. Full validation.
	report, err := s.validateDoc(ctx, tenantID, def.Definition)
	if err != nil {
		return nil, err
	}
	if !report.Valid {
		return nil, &errValidationFailed{Report: *report}
	}

	// 3. Parse definition.
	var doc appDefinitionDoc
	if err := json.Unmarshal(def.Definition, &doc); err != nil {
		return nil, validation("definition is not valid JSON")
	}

	// 4. Resolve component definitions.
	resolved := make(map[string]*registry.ComponentDefinition, len(doc.Components))
	for _, comp := range doc.Components {
		if s.registry == nil {
			// No registry wired (tests without registry) — skip resolution.
			continue
		}
		cd, resolveErr := s.registry.ResolveForPublish(ctx, tenantID, comp.DefinitionRef, comp.DefinitionID)
		if resolveErr != nil {
			return nil, fmt.Errorf("registry resolution failed for %q: %w", comp.InstanceID, resolveErr)
		}
		resolved[comp.InstanceID] = cd
	}

	// 5. Build instance_id → UUID maps from orchestrator upserts.
	orchUUIDs := make(map[string]string, len(doc.Components)) // instance_id → UUID

	// Determine which instance_ids are delegation targets (delegatable=true).
	delegationTargets := make(map[string]bool)
	for _, conn := range doc.Connections {
		if conn.Type == "delegation" {
			delegationTargets[conn.Target] = true
		}
	}

	// Build tool connection map: orchestrator instance_id → []agent instance_ids.
	toolTargets := make(map[string][]string) // orch instance_id → agent instance_ids
	for _, conn := range doc.Connections {
		if conn.Type == "tool" {
			toolTargets[conn.Source] = append(toolTargets[conn.Source], conn.Target)
		}
	}

	for _, comp := range doc.Components {
		// Only compile orchestrator kind components into app_orchestrators.
		if comp.DefinitionRef.Kind != registry.KindOrchestrator {
			continue
		}

		row := dal.AppOrchestratorRow{
			ApplicationID:        appID,
			Name:                 comp.InstanceID, // stable Temporal key (instance_id)
			InstanceID:           comp.InstanceID,
			Kind:                 "standard",
			Delegatable:          delegationTargets[comp.InstanceID],
			SourceDefinitionID:   defID,
			SourceDefinitionHash: def.DefinitionHash,
		}

		// Extract config values.
		if comp.Config != nil {
			row.MaxIterations = configInt(comp.Config, "max_iterations", 10)
			row.MaxParallelTools = configInt(comp.Config, "max_parallel_tools", 5)
			row.HistoryWindow = configInt(comp.Config, "history_window", 20)
			row.LLMProvider = configStringPtr(comp.Config, "llm_provider")
			row.LLMModel = configStringPtr(comp.Config, "llm_model")
			row.SystemPrompt = configStringPtr(comp.Config, "system_prompt")
			if v, ok := comp.Config["budget_tokens"]; ok {
				if n, ok2 := toInt(v); ok2 {
					row.BudgetTokens = &n
				}
			}
			if v, ok := comp.Config["mcp_servers"]; ok {
				if b, err := json.Marshal(v); err == nil {
					row.MCPServers = b
				}
			}
		} else {
			row.MaxIterations = 10
			row.MaxParallelTools = 5
			row.HistoryWindow = 20
		}

		// Component definition pin.
		if cd, ok := resolved[comp.InstanceID]; ok {
			row.ComponentDefinitionID = &cd.ID
			v := cd.Version
			row.ComponentVersion = &v
		}

		// AllowedAgentIDs: tool connections from this orchestrator → agent instances.
		var agentIDs []string
		for _, targetInstID := range toolTargets[comp.InstanceID] {
			// Look up the component definition ID for the agent instance.
			if cd, ok := resolved[targetInstID]; ok {
				agentIDs = append(agentIDs, cd.ID)
			}
			// If resolution is skipped (no registry), leave agentIDs empty.
		}
		row.AllowedAgentIDs = agentIDs

		orchID, upsertErr := s.dal.UpsertAppOrchestrator(ctx, row)
		if upsertErr != nil {
			return nil, fmt.Errorf("upsert orchestrator %q: %w", comp.InstanceID, upsertErr)
		}
		orchUUIDs[comp.InstanceID] = orchID
	}

	// 5b. Upsert app_agent_bindings for every canvas agent in this definition.
	// This ensures RuntimeView can discover required params for internal (canvas_a2a) agents.
	for _, comp := range doc.Components {
		if comp.DefinitionRef.Kind != registry.KindAgent {
			continue
		}
		cd, ok := resolved[comp.InstanceID]
		if !ok {
			continue
		}
		if bindErr := s.dal.UpsertAgentBinding(ctx, dal.AgentBindingRow{
			ApplicationID: appID,
			AgentID:       cd.ID,
		}); bindErr != nil {
			return nil, fmt.Errorf("upsert agent binding %q: %w", comp.InstanceID, bindErr)
		}
	}

	// 6. Compile entry_points.
	// Build a map of orchestrator instance_id → component config for ep_memory lookup.
	orchConfigs := make(map[string]map[string]any, len(doc.Components))
	for _, comp := range doc.Components {
		if comp.Config != nil {
			orchConfigs[comp.InstanceID] = comp.Config
		}
	}

	defaultAccessPolicy := []byte(`{"mode":"token"}`)
	for _, ep := range doc.EntryPoints {
		var orchID *string
		if ep.Root != "" {
			if uid, ok := orchUUIDs[ep.Root]; ok {
				orchID = &uid
			}
		}

		// Extract per-EP memory config from the orchestrator's canvas config.
		// Stored as config.ep_memory[ep.instance_id] on the orchestrator node.
		var (
			memoryEnabled        bool
			historyWindow        = 20
			summarizeEveryN      int
			rawFallbackN         = 3
			summarizerProvider   *string
			summarizerModel      *string
		)
		if ep.Root != "" {
			if orchCfg, ok := orchConfigs[ep.Root]; ok {
				if epMemoryRaw, ok := orchCfg["ep_memory"]; ok {
					if epMemoryMap, ok := epMemoryRaw.(map[string]any); ok {
						if epMem, ok := epMemoryMap[ep.InstanceID].(map[string]any); ok {
							if v, ok := epMem["memory_enabled"].(bool); ok {
								memoryEnabled = v
							}
							if v, ok := epMem["history_window"].(float64); ok && v > 0 {
								historyWindow = int(v)
							}
							if v, ok := epMem["summarize_every_n_calls"].(float64); ok {
								summarizeEveryN = int(v)
							}
							if v, ok := epMem["memory_raw_fallback_n"].(float64); ok && v > 0 {
								rawFallbackN = int(v)
							}
							if v, ok := epMem["summarizer_provider"].(string); ok && v != "" {
								summarizerProvider = &v
							}
							if v, ok := epMem["summarizer_model"].(string); ok && v != "" {
								summarizerModel = &v
							}
						}
					}
				}
			}
		}

		row := dal.EntryPointRow{
			ApplicationID:        appID,
			TenantID:             tenantID,
			Slug:                 ep.Slug,
			InstanceID:           ep.InstanceID,
			EntryPointType:       ep.Protocol,
			AppOrchestratorID:    orchID,
			AccessPolicy:         defaultAccessPolicy,
			SourceDefinitionID:   defID,
			SourceDefinitionHash: def.DefinitionHash,
			MemoryEnabled:        memoryEnabled,
			HistoryWindow:        historyWindow,
			SummarizeEveryNCalls: summarizeEveryN,
			MemoryRawFallbackN:   rawFallbackN,
			SummarizerProvider:   summarizerProvider,
			SummarizerModel:      summarizerModel,
		}

		if _, upsertErr := s.dal.UpsertEntryPoint(ctx, row); upsertErr != nil {
			return nil, fmt.Errorf("upsert entry_point %q: %w", ep.Slug, upsertErr)
		}
	}

	// 7. Deactivate stale projections.
	if err := s.dal.DeactivateStaleOrchestrators(ctx, tenantID, appID, defID); err != nil {
		return nil, fmt.Errorf("deactivate stale orchestrators: %w", err)
	}
	if err := s.dal.DeactivateStaleEntryPoints(ctx, tenantID, appID, defID); err != nil {
		return nil, fmt.Errorf("deactivate stale entry_points: %w", err)
	}

	// 8. Atomically mark published + update active_definition_id.
	result, err := s.dal.PublishDefinition(ctx, tenantID, appID, defID, def.DefinitionHash)
	if err != nil {
		if dal.IsNoRows(err) {
			// The status changed between our check and now (e.g. concurrent publish).
			return nil, ErrConflict
		}
		return nil, err
	}

	return &result, nil
}

// ── config extraction helpers ─────────────────────────────────────────────────

func configInt(m map[string]any, key string, defaultVal int) int {
	v, ok := m[key]
	if !ok {
		return defaultVal
	}
	if n, ok2 := toInt(v); ok2 {
		return n
	}
	return defaultVal
}

func configStringPtr(m map[string]any, key string) *string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i64, err := n.Int64()
		if err == nil {
			return int(i64), true
		}
	}
	return 0, false
}
