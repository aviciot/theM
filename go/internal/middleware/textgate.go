package middleware

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// TextGate runs pii_redact/prompt_inject synchronously against text produced
// by an llm or agent node (docs/APPFLOW_TEXT_GUARDS_PLAN.md Phase 1).
//
// Deliberately NOT built on FileGate's async quarantine/enqueue path: a
// redaction guard must change the text BEFORE the workflow continues using
// it, not eventually once a background worker gets to it — by the time an
// async job result arrived, the unredacted text would already have been
// used downstream. TextGate resolves config and runs the Pipeline entirely
// in-process, in the calling activity, returning the (possibly redacted)
// text immediately.
//
// Also deliberately NOT reusing FileGate's resolveSecCfg/loadWiringCfgForDef:
// those parse middleware_defs.config as a nested SecurityConfig{Processors:
// map[string]json.RawMessage} shape (file-guard's own config happens to be
// stored that way). pii_redact/prompt_inject's middleware_defs.config is a
// flat processor config instead (e.g. {"enabled":false,"mode":"redact",...}
// — see db/113_pii_prompt_guard_defs.sql), matching PIIRedactConfig/
// PromptInjectConfig's own flat struct shape directly. Reusing the
// SecurityConfig-shaped resolver against this flat data would silently
// return an empty config (no "processors" key to find), not an error —
// worth the small duplication to get a correct result instead.
type TextGate struct {
	db  GateQuerier
	reg *Registry

	cacheMu sync.Mutex
	cache   map[string]cachedTextCfg // cacheKey → config+expiry
}

type cachedTextCfg struct {
	cfg    json.RawMessage
	found  bool
	expiry time.Time
}

// NewTextGate creates a TextGate. reg must have pii_redact/prompt_inject
// processors registered (or both) — a processor with no matching wiring
// enabled is simply never invoked, same fail-open posture as FileGate.
func NewTextGate(db GateQuerier, reg *Registry) *TextGate {
	return &TextGate{db: db, reg: reg, cache: make(map[string]cachedTextCfg)}
}

// TextGateInput carries the scoping fields needed to resolve a per-node
// text-guard wiring. Mirrors GateInput's tenant/app/node fields; no
// DownloadURL/FileName — not applicable to text.
type TextGateInput struct {
	ApplicationID string
	NodeID        string
	AgentSlug     string
	// Phase is "input" (about to send this text to the llm/agent — checked
	// BEFORE the call) or "output" (the llm/agent's response — checked
	// AFTER). A guard's own "direction" config field ("output", the
	// default, or "both") decides whether it runs for a given phase — see
	// Check's own doc comment. Required; Check treats "" the same as
	// "output" (fail toward the pre-existing behavior every wiring created
	// before this field existed already has).
	Phase string
}

// TextGateResult is returned by Check.
type TextGateResult struct {
	// Text is the final text after any redaction — equal to the input text
	// when nothing matched or no guard is enabled.
	Text string
	// Blocked is true when a guard's mode is "block" and something matched —
	// the caller must fail the run, not just log a warning.
	Blocked bool
	// Results carries one middleware.Result per guard that actually ran
	// (skipped guards, e.g. no wiring enabled, are not included) — for trace
	// visibility, same "don't silently scan with zero visibility" lesson
	// from this session's File Guard trace fix.
	Results []Result
	// ResultsByGuard is the same data as Results, keyed by defSlug
	// ("pii_redact", "prompt_inject") instead of positional order —
	// docs/APPFLOW_GUARD_OUTPUT_PORTS_PLAN.md Phase 1.5. Each guard's own
	// Result.Detail carries its real per-category match data (e.g.
	// pii_redact's Detail["categories"] = map[string]int{"email": 1}) —
	// this was already computed correctly by pii.Detector.Process the whole
	// time, just never threaded past this struct before Phase 1.5 (the
	// appflow-layer TextGateCheckOutput/appFlowTextGateAdapter only ever
	// extracted Categories, a flat human-readable string, and silently
	// dropped everything else). Added as a NEW field alongside Results
	// rather than replacing it — Results' positional-order guarantee (this
	// same Check method's own iteration order over
	// []string{"pii_redact","prompt_inject"}) is fragile to rely on from a
	// caller that wants a specific guard's own result, so ResultsByGuard is
	// the one new callers should use; Results stays for whatever narrow
	// internal use it already had.
	ResultsByGuard map[string]Result
	// Categories is a short human-readable summary of what ran and matched,
	// e.g. "pii_redact:flagged prompt_inject:clean" — for the workflow trace
	// line, same spirit as FileGateCheckOutput's ScanStatus.
	Categories string
}

// Check runs pii_redact then prompt_inject (in that order — matches
// EnabledProcessors' canonical pipeline order) against text, using each
// guard's own per-node wiring when one exists, falling back to "not
// enabled" (skipped) when no wiring is configured. Never returns an error
// for a processor-level failure — same fail-open convention FileGate/
// Pipeline.Run already use; only a DB error resolving config surfaces as an
// error here.
//
// in.Phase gates each guard by its own "direction" config field: a guard
// configured "output" (the default) only runs when Phase=="output"; a guard
// configured "both" runs on both "input" and "output" calls. This lets one
// wiring's config decide whether the caller needs to invoke Check a second
// time (before the llm/agent call, on the outgoing prompt) — the workflow
// caller doesn't need to know each guard's direction setting itself, only
// that it should always call Check twice (input phase, then output phase)
// and let TextGate decide per-guard whether either call actually does
// anything.
func (g *TextGate) Check(ctx context.Context, in TextGateInput, text string) (TextGateResult, error) {
	pipeline := NewPipeline(g.reg)
	part := Part{Kind: "text", Text: text}

	var allResults []Result
	resultsByGuard := make(map[string]Result)
	var categoryParts []string
	current := part
	for _, defSlug := range []string{"pii_redact", "prompt_inject"} {
		if g.reg.Get(defSlug) == nil {
			continue // processor not registered — nothing to run
		}
		cfgRaw, found, err := g.loadTextWiringCfg(ctx, defSlug, in.ApplicationID, in.NodeID, in.AgentSlug)
		if err != nil {
			return TextGateResult{Text: current.Text}, err
		}
		if !found {
			continue // no wiring for this guard on this node — skip, fail open
		}

		var cfgMeta struct {
			Enabled   bool   `json:"enabled"`
			Direction string `json:"direction"`
		}
		_ = json.Unmarshal(cfgRaw, &cfgMeta)
		if !cfgMeta.Enabled {
			continue
		}
		phase := in.Phase
		if phase == "" {
			phase = "output"
		}
		runsOnThisPhase := phase == "output" || cfgMeta.Direction == "both"
		if !runsOnThisPhase {
			continue
		}

		pr := pipeline.Run(ctx, current, []string{defSlug}, wrapSingleProcessorConfig(defSlug, cfgRaw), nil)
		allResults = append(allResults, pr.Results...)
		// pipeline.Run was called with exactly one processor name (defSlug),
		// so pr.Results has exactly one entry when the processor actually
		// ran (it can be empty if the processor was never found in the
		// registry, though that's already filtered out above).
		if len(pr.Results) > 0 {
			resultsByGuard[defSlug] = pr.Results[0]
		}
		current = pr.FinalPart
		categoryParts = append(categoryParts, defSlug+":"+pr.FinalStatus)

		if pr.FinalStatus == "flagged" {
			for _, r := range pr.Results {
				if r.Block {
					return TextGateResult{
						Text: current.Text, Blocked: true, Results: allResults,
						ResultsByGuard: resultsByGuard,
						Categories:     joinCategoryParts(categoryParts),
					}, nil
				}
			}
		}
	}

	return TextGateResult{
		Text: current.Text, Results: allResults, ResultsByGuard: resultsByGuard,
		Categories: joinCategoryParts(categoryParts),
	}, nil
}

func joinCategoryParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// wrapSingleProcessorConfig builds the minimal SecurityConfig Pipeline.Run
// needs to find one processor's config via ProcessorConfig — Pipeline.Run's
// signature takes a SecurityConfig for historical reasons (shared with
// av_scan's multi-processor pipeline), but TextGate only ever runs one
// processor per Run call, so this just wraps the already-resolved flat
// config under the one key that call needs.
func wrapSingleProcessorConfig(defSlug string, cfgRaw json.RawMessage) SecurityConfig {
	return SecurityConfig{
		Enabled:    true,
		Processors: map[string]json.RawMessage{defSlug: cfgRaw},
	}
}

// loadTextWiringCfg looks up a middleware_wirings row for defSlug
// ("pii_redact" or "prompt_inject"), scoped by nodeID when provided (same
// node_id-then-agent-slug precedence as FileGate's loadWiringCfgForDef),
// returning the FLAT config (middleware_defs.config merged with
// config_override — no SecurityConfig unwrapping, see TextGate's doc
// comment on why). Returns (nil, false, nil) when no wiring exists.
func (g *TextGate) loadTextWiringCfg(ctx context.Context, defSlug, appID, nodeID, agentSlug string) (json.RawMessage, bool, error) {
	cacheKey := defSlug + ":" + appID + ":" + nodeID + ":" + agentSlug
	g.cacheMu.Lock()
	if cached, ok := g.cache[cacheKey]; ok && time.Now().Before(cached.expiry) {
		g.cacheMu.Unlock()
		return cached.cfg, cached.found, nil
	}
	g.cacheMu.Unlock()

	// LEFT JOIN agents, not JOIN — an llm-node wiring has no agent_id at all
	// (docs/APPFLOW_TEXT_GUARDS_PLAN.md Phase 4/db/114); an inner join would
	// silently exclude it.
	const q = `
SELECT
    mw.enabled,
    COALESCE(md.config, '{}')          AS def_config,
    COALESCE(mw.config_override, '{}') AS override
FROM them.middleware_wirings mw
LEFT JOIN them.agents        a  ON a.id  = mw.agent_id
JOIN them.middleware_defs    md ON md.id = mw.def_id
WHERE mw.application_id = $1::uuid
  AND (a.slug = $2 OR $2 = '')
  AND (mw.node_id = $3 OR mw.node_id IS NULL OR mw.node_id = '')
  AND md.kind           = 'guard'
  AND md.slug           = $4
ORDER BY (mw.node_id = $3) DESC
LIMIT 1`

	rows, err := g.db.Query(ctx, q, appID, agentSlug, nodeID, defSlug)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close() //nolint:errcheck

	if !rows.Next() {
		g.cacheMu.Lock()
		g.cache[cacheKey] = cachedTextCfg{found: false, expiry: time.Now().Add(30 * time.Second)}
		g.cacheMu.Unlock()
		return nil, false, nil
	}

	var (
		wiringEnabled bool
		defRaw        []byte
		overrideRaw   []byte
	)
	if err := rows.Scan(&wiringEnabled, &defRaw, &overrideRaw); err != nil {
		return nil, false, err
	}

	// Merge: start from the def's own default config, apply config_override
	// on top key-by-key (flat JSON objects, not nested Processors maps).
	merged := map[string]any{}
	_ = json.Unmarshal(defRaw, &merged)
	if len(overrideRaw) > 2 { // skip empty '{}'
		var override map[string]any
		if err := json.Unmarshal(overrideRaw, &override); err == nil {
			for k, v := range override {
				merged[k] = v
			}
		}
	}
	merged["enabled"] = wiringEnabled
	cfgRaw, _ := json.Marshal(merged)

	g.cacheMu.Lock()
	g.cache[cacheKey] = cachedTextCfg{cfg: cfgRaw, found: true, expiry: time.Now().Add(30 * time.Second)}
	g.cacheMu.Unlock()

	return cfgRaw, true, nil
}
