package middleware

import (
	"encoding/json"
	"fmt"
)

// SecurityConfig is the per-application middleware pipeline configuration.
// Stored as JSONB in them.applications.security_config.
type SecurityConfig struct {
	Enabled    bool                        `json:"enabled"`
	Processors map[string]json.RawMessage  `json:"processors,omitempty"`
}

// AVScanConfig is the config shape for the av_scan processor.
type AVScanConfig struct {
	Enabled          bool `json:"enabled"`
	MaxFileMB        int  `json:"max_file_mb"`
	BlockOnInfected  bool `json:"block_on_infected"`
}

// PIIRedactConfig is the config shape for the pii_redact processor.
// Mode replaces the old BlockOnDetect bool (docs/APPFLOW_TEXT_GUARDS_PLAN.md
// Phase 4, confirmed with the user 2026-09-27) — one field instead of two
// ways to say the same thing, same lesson as the duplicate "enabled"
// checkbox found in the File Guard Guards UI this session.
type PIIRedactConfig struct {
	Enabled   bool   `json:"enabled"`
	LLMAssist bool   `json:"llm_assist"`
	Mode      string `json:"mode"` // "block" | "redact" | "warn"
	// Direction: "output" (scan what the LLM/agent produced — default) or
	// "both" (also scan the incoming prompt/user message before it's sent).
	// "input"-only is not offered: if you pay the cost to scan input you
	// always want output scanned too.
	Direction string `json:"direction"` // "output" | "both"
}

// PromptInjectConfig is the config shape for the prompt_inject processor.
// Mode replaces the old BlockOnDetect bool — see PIIRedactConfig's comment.
// No "redact" mode here: an injection attempt isn't a value to mask, it's a
// decision to accept or reject the whole text.
type PromptInjectConfig struct {
	Enabled     bool   `json:"enabled"`
	Mode        string `json:"mode"`      // "block" | "warn"
	Sensitivity string `json:"sensitivity"` // "low" | "medium" | "high"
	Direction   string `json:"direction"`   // "output" | "both"
}

// SchemaValidateConfig is the config shape for the schema_validate processor.
type SchemaValidateConfig struct {
	Enabled bool `json:"enabled"`
	Strict  bool `json:"strict"`
}

// AuditCaptureConfig is the config shape for the audit_capture processor.
type AuditCaptureConfig struct {
	Enabled bool `json:"enabled"`
}

// DefaultSecurityConfig returns a SecurityConfig with safe defaults.
// Enabled is false — zero overhead until explicitly turned on.
func DefaultSecurityConfig() SecurityConfig {
	avRaw, _     := json.Marshal(AVScanConfig{Enabled: true, MaxFileMB: 5, BlockOnInfected: true})
	piiRaw, _    := json.Marshal(PIIRedactConfig{Enabled: false, Mode: "redact", Direction: "output"})
	injectRaw, _ := json.Marshal(PromptInjectConfig{Enabled: false, Mode: "block", Sensitivity: "medium", Direction: "output"})
	schemaRaw, _ := json.Marshal(SchemaValidateConfig{Enabled: false})
	auditRaw, _  := json.Marshal(AuditCaptureConfig{Enabled: false})

	return SecurityConfig{
		Enabled: false,
		Processors: map[string]json.RawMessage{
			"av_scan":         avRaw,
			"pii_redact":      piiRaw,
			"prompt_inject":   injectRaw,
			"schema_validate": schemaRaw,
			"audit_capture":   auditRaw,
		},
	}
}

// MergeDefaults returns a new SecurityConfig with defaults filled in for any
// missing processor keys in src.
func MergeDefaults(src SecurityConfig) SecurityConfig {
	def := DefaultSecurityConfig()
	if src.Processors == nil {
		src.Processors = def.Processors
		return src
	}
	for k, v := range def.Processors {
		if _, ok := src.Processors[k]; !ok {
			src.Processors[k] = v
		}
	}
	return src
}

// Validate returns an error if the config is invalid.
func Validate(cfg SecurityConfig) error {
	if !cfg.Enabled {
		return nil // disabled config is always valid
	}
	if avRaw, ok := cfg.Processors["av_scan"]; ok {
		var av AVScanConfig
		if err := json.Unmarshal(avRaw, &av); err != nil {
			return fmt.Errorf("av_scan config: %w", err)
		}
		if av.MaxFileMB <= 0 || av.MaxFileMB > 500 {
			return fmt.Errorf("av_scan.max_file_mb must be 1–500")
		}
	}
	if injectRaw, ok := cfg.Processors["prompt_inject"]; ok {
		var inj PromptInjectConfig
		if err := json.Unmarshal(injectRaw, &inj); err != nil {
			return fmt.Errorf("prompt_inject config: %w", err)
		}
		switch inj.Sensitivity {
		case "", "low", "medium", "high":
		default:
			return fmt.Errorf("prompt_inject.sensitivity must be low|medium|high")
		}
		if err := validateMode(inj.Mode, "block", "warn"); err != nil {
			return fmt.Errorf("prompt_inject.%w", err)
		}
		if err := validateDirection(inj.Direction); err != nil {
			return fmt.Errorf("prompt_inject.%w", err)
		}
	}
	if piiRaw, ok := cfg.Processors["pii_redact"]; ok {
		var pii PIIRedactConfig
		if err := json.Unmarshal(piiRaw, &pii); err != nil {
			return fmt.Errorf("pii_redact config: %w", err)
		}
		if err := validateMode(pii.Mode, "block", "redact", "warn"); err != nil {
			return fmt.Errorf("pii_redact.%w", err)
		}
		if err := validateDirection(pii.Direction); err != nil {
			return fmt.Errorf("pii_redact.%w", err)
		}
	}
	return nil
}

// validateMode checks mode against the set of values valid for one guard
// ("" is always allowed — an unset mode falls back to the processor's own
// default, same fail-open convention as an unset Sensitivity above).
func validateMode(mode string, allowed ...string) error {
	if mode == "" {
		return nil
	}
	for _, a := range allowed {
		if mode == a {
			return nil
		}
	}
	return fmt.Errorf("mode must be one of %v", allowed)
}

func validateDirection(direction string) error {
	switch direction {
	case "", "output", "both":
		return nil
	default:
		return fmt.Errorf("direction must be output|both")
	}
}

// EnabledProcessors returns the ordered list of processor names that are both
// registered in the registry and enabled in cfg, filtered to those applicable
// to the given part kind ("file", "text", "data").
// Order is the canonical pipeline order.
func EnabledProcessors(cfg SecurityConfig, reg *Registry, partKind string) []string {
	if !cfg.Enabled {
		return nil
	}
	// Canonical pipeline order
	order := []string{"av_scan", "pii_redact", "prompt_inject", "schema_validate", "audit_capture"}

	// Which part kinds each processor applies to
	applies := map[string]map[string]bool{
		"av_scan":         {"file": true},
		"pii_redact":      {"text": true},
		"prompt_inject":   {"text": true},
		"schema_validate": {"data": true},
		"audit_capture":   {"file": true, "text": true, "data": true},
	}

	var out []string
	for _, name := range order {
		if reg.Get(name) == nil {
			continue // processor not registered yet (later phase)
		}
		if !applies[name][partKind] {
			continue
		}
		raw, ok := cfg.Processors[name]
		if !ok {
			continue
		}
		// Check the "enabled" field common to all processor configs
		var base struct{ Enabled bool `json:"enabled"` }
		if err := json.Unmarshal(raw, &base); err != nil || !base.Enabled {
			continue
		}
		out = append(out, name)
	}
	return out
}

// ProcessorConfig returns the raw JSON config for one processor from a SecurityConfig.
// Returns nil if the processor is not present.
func ProcessorConfig(cfg SecurityConfig, name string) json.RawMessage {
	if cfg.Processors == nil {
		return nil
	}
	return cfg.Processors[name]
}
