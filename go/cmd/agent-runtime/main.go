// Package main is the them-agent-runtime — a generic stateless A2A agent server.
// It reads AgentSpec definitions from PostgreSQL (cached locally, TTL 60s) and
// serves any canvas-designed agent over the A2A JSON-RPC 2.0 and streaming wire
// protocol, backed by the official github.com/a2aproject/a2a-go/v2 SDK.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/temporal"
)

// agentParamEntry is the stored shape for a secret-type agent param.
type agentParamEntry struct {
	CT   string `json:"ct"`
	Hint string `json:"hint"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		logger.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	redisCache, err := cache.New(ctx, cfg.RedisAddr(), cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		logger.Error("redis connect failed", "err", err)
		os.Exit(1)
	}

	taskRedis := cache.NewAuthRedisClient(redisCache.Client())
	cryptoKey := crypto.DeriveKey(cfg.SecretKey)

	interpBase := agentgen.NewInterpreter(
		&http.Client{Timeout: 60 * time.Second},
		&multiLLMFactory{platformKey: cfg.AnthropicAPIKey},
		cfg.AnthropicAPIKey,
	)
	if cfg.MCPServiceURL != "" {
		interpBase.WithMCPCaller(agentgen.NewHTTPMCPCaller(cfg.MCPServiceURL, &http.Client{Timeout: 30 * time.Second}))
	}

	rt := &Runtime{
		pool:      database.Pool(),
		cryptoKey: cryptoKey,
		taskStore: agentgen.NewRedisTaskStore(taskRedis),
		specCache: &specCache{entries: make(map[string]*cachedSpec)},
		logger:    logger,
		interp:    interpBase,
	}

	// When Temporal is enabled, create a TemporalExecutor so canvas agents with
	// execution_backend=="temporal" can be routed to the DAG worker.
	if cfg.TemporalEnabled {
		temporalCli, err := temporal.Connect(cfg.TemporalHostPort, logger)
		if err != nil {
			logger.Error("temporal connect failed", "err", err)
			os.Exit(1)
		}
		rt.temporalExecutor = temporal.NewTemporalExecutor(temporalCli, 0, 0, logger)
		logger.Info("temporal executor configured", "host_port", cfg.TemporalHostPort)
	}

	port := "9300"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	r := chi.NewRouter()
	r.Get("/healthz", rt.healthz)
	// SDK card handler: NewStaticAgentCardHandler requires the spec to build the card.
	// We serve it via a thin wrapper that loads the spec then delegates to the SDK handler.
	r.Get("/agents/{slug}/.well-known/agent-card.json", rt.agentCard)
	// A2A JSON-RPC endpoint: auth + spec + binding resolution happens here in middleware,
	// then the SDK's NewJSONRPCHandler dispatches message/send, tasks/get, tasks/cancel,
	// message/stream, tasks/resubscribe and all other A2A methods.
	r.Post("/agents/{slug}", rt.handle)

	logger.Info("them-agent-runtime starting", "port", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
}

// Runtime is the stateless request handler. One per process; all state in Redis/Postgres.
type Runtime struct {
	pool             *pgxpool.Pool
	cryptoKey        []byte
	taskStore        *agentgen.RedisTaskStore
	specCache        *specCache
	logger           *slog.Logger
	interp           *agentgen.Interpreter
	// temporalExecutor is non-nil when TEMPORAL_ENABLED=true. Canvas agents with
	// execution_backend=="temporal" are routed here; all others use LocalExecutor.
	temporalExecutor agentgen.ExecutionBackend
}

func (rt *Runtime) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
}

// agentCard serves /.well-known/agent-card.json using the SDK's NewStaticAgentCardHandler.
func (rt *Runtime) agentCard(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	spec, err := rt.loadSpecBySlug(r.Context(), slug)
	if err != nil {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	card := buildSDKAgentCard(spec)
	a2asrv.NewStaticAgentCardHandler(card).ServeHTTP(w, r)
}

// handle is the A2A JSON-RPC endpoint. It resolves auth/spec/binding in our middleware,
// then creates a per-request AgentExecutor and delegates to the SDK's JSON-RPC handler.
// The SDK provides full method dispatch: message/send, tasks/get, tasks/cancel,
// message/stream, tasks/resubscribe, and push notification methods.
func (rt *Runtime) handle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	ic, err := rt.parseInvocationContext(r)
	if err != nil {
		rt.logger.Warn("agent-runtime: unauthorized request", "slug", slug, "err", err)
		writeJSONRPCError(w, nil, -32600, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Invariant 2: cross-check URL slug vs authoritative agent_id from invocation context.
	spec, err := rt.loadSpecByAgentID(r.Context(), ic.TenantID, ic.AgentID)
	if err != nil || spec.Slug != slug {
		writeJSONRPCError(w, nil, -32600, "forbidden", http.StatusForbidden)
		return
	}
	ic.Spec = spec

	binding, agentParamsJSON, err := rt.loadBinding(r.Context(), ic.TenantID, ic.ApplicationID, ic.AgentID, ic.BindingID)
	if err != nil {
		writeJSONRPCError(w, nil, -32600, "binding not found", http.StatusNotFound)
		return
	}

	// Invariant 1: assert binding definition_id matches spec when pinned.
	if binding.DefinitionID != nil && *binding.DefinitionID != spec.DefinitionID {
		writeJSONRPCError(w, nil, -32603, "binding stale — application must be re-published", http.StatusConflict)
		return
	}

	ic.ConfigOverrides = binding.ConfigOverrides
	ic.Policies = binding.Policies

	// Load per-app provider keys so the interpreter can prefer them over the platform env key.
	// Errors are non-fatal — the platform key fallback still works.
	ic.AppAPIKey = rt.loadAppAPIKey(r.Context(), ic.TenantID, ic.ApplicationID)

	// Resolve agent params from the binding (decrypt secrets, apply defaults).
	// ic.AgentParams is never nil — steps can safely read from it without nil checks.
	ic.AgentParams = rt.resolveAgentParams(agentParamsJSON, spec.RequiredParams)

	// Load app-level global params for HTTP app_param_ref injection.
	ic.AppGlobalParams = rt.loadAppGlobalParams(r.Context(), ic.TenantID, ic.ApplicationID)

	// Extract per-node LLM overrides from config_overrides["llm_nodes"].
	ic.NodeLLMOverrides = extractNodeLLMOverrides(binding.ConfigOverrides)

	// Build the SDK executor function. It is called by the SDK for each message/send
	// or message/stream request. The closure captures the fully-resolved InvocationContext.
	executor := a2asrv.AgentExecutorFunc(func(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return rt.executeSkill(ctx, ic, execCtx)
	})

	// NewHandler creates a RequestHandler backed by the SDK's local task manager
	// (in-memory by default). It handles GetTask, ListTasks, CancelTask,
	// SendMessage, SubscribeToTask, SendStreamingMessage, and push config methods.
	handler := a2asrv.NewHandler(executor,
		a2asrv.WithLogger(rt.logger),
	)

	// NewJSONRPCHandler wraps the RequestHandler in a single POST endpoint that
	// dispatches all A2A JSON-RPC 2.0 methods, replacing our hand-rolled dispatch.
	a2asrv.NewJSONRPCHandler(handler).ServeHTTP(w, r)
}

// executeSkill runs the agent's first skill pipeline and emits SDK-compliant A2A events.
// It follows the template from agentexec.go:
//
//	Submitted → Working → ArtifactEvent → Completed  (success path)
//	Submitted → Working → Failed                     (error path)
//
// InvocationID is stamped from execCtx.TaskID — the SDK assigns this once per logical
// task and reuses it across retries, giving Temporal a stable workflow ID for re-attach.
func (rt *Runtime) executeSkill(ctx context.Context, ic *agentgen.InvocationContext, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	ic.InvocationID = string(execCtx.TaskID)
	return func(yield func(a2a.Event, error) bool) {
		// Emit Submitted only for new tasks (no prior StoredTask).
		if execCtx.StoredTask == nil {
			submitted := a2a.NewSubmittedTask(execCtx, execCtx.Message)
			if !yield(submitted, nil) {
				return
			}
		}

		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		if len(ic.Spec.Skills) == 0 {
			errMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("agent has no skills"))
			if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) {
				return
			}
			return
		}

		// Skill selection: prefer skill matching the requested ID in message metadata,
		// fall back to first skill (single-skill agents have no skill ID in the message).
		skill := &ic.Spec.Skills[0]
		if execCtx.Message != nil {
			if requestedID, ok := execCtx.Message.Metadata["skill_id"].(string); ok && requestedID != "" {
				found := false
				for i := range ic.Spec.Skills {
					if ic.Spec.Skills[i].ID == requestedID {
						skill = &ic.Spec.Skills[i]
						found = true
						break
					}
				}
				if !found {
					errMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("skill not found: "+requestedID))
					yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) //nolint:errcheck
					return
				}
			}
		}

		// Enforce AllowedSkillIDs policy from the binding (nil = all skills allowed).
		if len(ic.Policies.AllowedSkillIDs) > 0 {
			allowed := false
			for _, id := range ic.Policies.AllowedSkillIDs {
				if id == skill.ID {
					allowed = true
					break
				}
			}
			if !allowed {
				errMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("skill not permitted by binding policy"))
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) //nolint:errcheck
				return
			}
		}

		// Extract text and structured data from the incoming message parts.
		// text part  → vars["input"] (the raw caller message)
		// data part  → each top-level key becomes a named pipeline var
		inputText := ""
		dataVars := map[string]any{}
		if execCtx.Message != nil {
			for _, part := range execCtx.Message.Parts {
				if t := part.Text(); t != "" && inputText == "" {
					inputText = t
				} else if d := part.Data(); d != nil {
					raw, err := json.Marshal(d)
					if err == nil {
						var obj map[string]any
						if json.Unmarshal(raw, &obj) == nil {
							for k, v := range obj {
								dataVars[k] = v
							}
						}
					}
				}
			}
		}

		initial := agentgen.PipelineVars{"input": inputText}
		for k, v := range dataVars {
			initial[k] = v
		}

		plan := agentgen.CompileExecutionPlan(skill)

		// Choose execution backend: temporal for canvas agents that have opted in,
		// local (goroutine fan-out) for all others.
		// Fail closed: if the agent requests Temporal but the executor is nil (i.e.
		// TEMPORAL_ENABLED=false on this pod), return a typed error rather than
		// silently falling back to Local execution.
		var backend agentgen.ExecutionBackend
		if ic.Spec.ExecutionBackend == "temporal" {
			if rt.temporalExecutor == nil {
				errMsg := a2a.NewMessage(a2a.MessageRoleAgent,
					a2a.NewTextPart("execution_backend=temporal but Temporal is not enabled on this runtime"))
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) //nolint:errcheck
				return
			}
			backend = rt.temporalExecutor
		} else {
			backend = agentgen.NewLocalExecutor(rt.interp)
		}
		execResult, err := backend.Execute(ctx, ic, plan, initial)
		if err != nil {
			rt.logger.Error("agent-runtime: execution failed",
				"tenant_id", ic.TenantID,
				"application_id", ic.ApplicationID,
				"agent_id", ic.AgentID,
				"invocation_id", ic.InvocationID,
				"err", err,
			)
			errMsg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("execution failed"))
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errMsg), nil) //nolint:errcheck
			return
		}

		// Emit the result as an artifact, then mark completed.
		artifactEvent := a2a.NewArtifactEvent(execCtx, a2a.NewTextPart(execResult.Text))
		if !yield(artifactEvent, nil) {
			return
		}

		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil) //nolint:errcheck
	}
}

// parseInvocationContext reads identity from X-Them-* headers.
// Phase 1 uses plain headers (internal Docker network only).
// Phase 3 upgrades to signed JWT (THE_M_INVOCATION_JWT_KEY).
// InvocationID is left empty here; executeSkill stamps it from execCtx.TaskID
// so retries of the same A2A task reuse the same Temporal workflow ID.
func (rt *Runtime) parseInvocationContext(r *http.Request) (*agentgen.InvocationContext, error) {
	tenantID := r.Header.Get("X-Them-Tenant-Id")
	appID := r.Header.Get("X-Them-Application-Id")
	agentID := r.Header.Get("X-Them-Agent-Id")
	bindingID := r.Header.Get("X-Them-Binding-Id")
	if tenantID == "" || appID == "" || agentID == "" {
		return nil, fmt.Errorf("missing required invocation context headers")
	}
	return &agentgen.InvocationContext{
		TenantID:      tenantID,
		ApplicationID: appID,
		AgentID:       agentID,
		BindingID:     bindingID,
	}, nil
}

// specCache is an in-process AgentSpec cache with 60s TTL per entry.
type specCache struct {
	mu      sync.Mutex
	entries map[string]*cachedSpec
}

type cachedSpec struct {
	spec      *agentgen.AgentSpec
	expiresAt time.Time
}

// specCacheKey returns a cache key that includes the tenantID so specs from
// different tenants with coincidental agent UUIDs cannot cross-contaminate.
func specCacheKey(tenantID, agentID string) string {
	return tenantID + ":" + agentID
}

func (c *specCache) get(key string) *agentgen.AgentSpec {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expiresAt) {
		return e.spec
	}
	return nil
}

func (c *specCache) set(key string, spec *agentgen.AgentSpec) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = &cachedSpec{spec: spec, expiresAt: time.Now().Add(60 * time.Second)}
}

func (rt *Runtime) loadSpecByAgentID(ctx context.Context, tenantID, agentID string) (*agentgen.AgentSpec, error) {
	key := specCacheKey(tenantID, agentID)
	if spec := rt.specCache.get(key); spec != nil {
		return spec, nil
	}
	row := rt.pool.QueryRow(ctx,
		`SELECT s.spec FROM them.agent_runtime_specs s
		 JOIN them.agents a ON a.id = s.agent_id
		 WHERE s.agent_id = $1::uuid AND a.tenant_id = $2::uuid`, agentID, tenantID)
	var specJSON []byte
	if err := row.Scan(&specJSON); err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}
	var spec agentgen.AgentSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec: %w", err)
	}
	rt.specCache.set(key, &spec)
	return &spec, nil
}

// loadAppAPIKey fetches and decrypts the provider_keys for the given application.
// Returns a map of provider→plaintext key (e.g. "anthropic"→"sk-ant-...").
// Returns an empty map on any error — callers fall back to the platform key.
// The decrypted keys are never logged. The tenant_id predicate prevents cross-tenant key reads.
func (rt *Runtime) loadAppAPIKey(ctx context.Context, tenantID, appID string) map[string]string {
	row := rt.pool.QueryRow(ctx,
		`SELECT COALESCE(provider_keys, '{}') FROM them.applications WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		appID, tenantID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return map[string]string{}
	}

	// Try new structured format {"anthropic": {"ct": "...", "hint": "XXXX"}}.
	type entry struct {
		CT   string `json:"ct"`
		Hint string `json:"hint"`
	}
	var structured map[string]entry
	if err := json.Unmarshal(raw, &structured); err == nil {
		out := make(map[string]string, len(structured))
		for provider, e := range structured {
			if e.CT == "" && e.Hint == "" {
				continue
			}
			plain, err := crypto.DecryptStored(rt.cryptoKey, e.CT)
			if err != nil {
				// Legacy plaintext row (written before encryption): use CT directly.
				// This handles the migration window until keys are re-encrypted.
				if len(e.CT) > 6 && e.CT[:6] == "plain:" {
					out[provider] = e.CT[6:]
					continue
				}
				// Decryption failed for an encrypted entry — likely a key rotation mismatch.
				// Log at warn so operators can detect and re-save affected keys.
				slog.Warn("agent-runtime: provider key decryption failed; falling back to platform key",
					"app_id", appID, "provider", provider, "err", err)
				continue
			}
			out[provider] = plain
		}
		if len(out) > 0 {
			return out
		}
	}

	// Legacy flat format {"anthropic": "sk-ant-..."} — plaintext, pre-encryption.
	var flat map[string]string
	if err := json.Unmarshal(raw, &flat); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(flat))
	for provider, v := range flat {
		if v != "" {
			out[provider] = v
		}
	}
	return out
}

// loadAppGlobalParams fetches and decrypts app_params for the given application.
// Returns a name→plaintext map. Non-fatal: returns an empty map on any error.
// The decrypted values are never logged. The tenant_id predicate prevents cross-tenant reads.
func (rt *Runtime) loadAppGlobalParams(ctx context.Context, tenantID, appID string) map[string]string {
	row := rt.pool.QueryRow(ctx,
		`SELECT COALESCE(app_params, '{}') FROM them.applications WHERE id = $1::uuid AND tenant_id = $2::uuid`,
		appID, tenantID)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return map[string]string{}
	}
	return decodeAppGlobalParams(raw, rt.cryptoKey, appID)
}

// decodeAppGlobalParams parses and decrypts the raw app_params JSONB blob.
// Exported for testing. Returns an empty map (never nil) on any decode error.
func decodeAppGlobalParams(raw []byte, cryptoKey []byte, appID string) map[string]string {
	type secretEntry struct {
		CT   string `json:"ct"`
		Hint string `json:"hint"`
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return map[string]string{}
	}

	out := make(map[string]string, len(top))
	for name, valRaw := range top {
		var entry secretEntry
		if json.Unmarshal(valRaw, &entry) == nil && entry.CT != "" {
			// Test/dev mode: service stores "plain:<plaintext>" when no crypto key is configured.
			if len(entry.CT) > 6 && entry.CT[:6] == "plain:" {
				out[name] = entry.CT[6:]
				continue
			}
			plain, err := crypto.DecryptStored(cryptoKey, entry.CT)
			if err != nil {
				slog.Warn("agent-runtime: app global param decryption failed",
					"app_id", appID, "name", name)
				continue
			}
			out[name] = plain
			continue
		}
		var s string
		if json.Unmarshal(valRaw, &s) == nil && s != "" {
			out[name] = s
		}
	}
	return out
}

func (rt *Runtime) loadSpecBySlug(ctx context.Context, slug string) (*agentgen.AgentSpec, error) {
	row := rt.pool.QueryRow(ctx,
		`SELECT s.spec FROM them.agent_runtime_specs s
		 JOIN them.agents a ON a.id = s.agent_id
		 WHERE a.slug = $1 AND a.enabled = true`, slug)
	var specJSON []byte
	if err := row.Scan(&specJSON); err != nil {
		return nil, fmt.Errorf("load spec by slug: %w", err)
	}
	var spec agentgen.AgentSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("unmarshal spec: %w", err)
	}
	// Note: we cannot pre-populate the agent-ID cache here because loadSpecBySlug
	// has no tenantID, and cache keys are tenant-scoped (specCacheKey).
	return &spec, nil
}

// extractNodeLLMOverrides reads the llm_nodes sub-map from config_overrides and
// returns a map of node_id → NodeLLMOverride. Safe to call with a nil map.
func extractNodeLLMOverrides(overrides map[string]any) map[string]agentgen.NodeLLMOverride {
	out := make(map[string]agentgen.NodeLLMOverride)
	if overrides == nil {
		return out
	}
	raw, ok := overrides["llm_nodes"]
	if !ok {
		return out
	}
	nodes, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for nodeID, v := range nodes {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		provider, _ := m["provider"].(string)
		model, _ := m["model"].(string)
		if provider != "" || model != "" {
			out[nodeID] = agentgen.NodeLLMOverride{Provider: provider, Model: model}
		}
	}
	return out
}

func (rt *Runtime) loadBinding(ctx context.Context, tenantID, appID, agentID, bindingID string) (*agentgen.AppAgentBinding, []byte, error) {
	var (
		query string
		args  []any
	)
	// Both query paths JOIN applications to assert tenant ownership.
	// This prevents a caller from accessing a binding that belongs to another tenant
	// by supplying a valid binding/application UUID they do not own.
	if bindingID != "" {
		query = `SELECT b.id, b.application_id, b.agent_id, b.definition_id,
		          b.credential_bindings, b.config_overrides, b.policies,
		          COALESCE(b.agent_params, '{}')
		          FROM them.app_agent_bindings b
		          JOIN them.applications a ON a.id = b.application_id
		          WHERE b.id = $1::uuid AND a.tenant_id = $2::uuid`
		args = []any{bindingID, tenantID}
	} else {
		query = `SELECT b.id, b.application_id, b.agent_id, b.definition_id,
		          b.credential_bindings, b.config_overrides, b.policies,
		          COALESCE(b.agent_params, '{}')
		          FROM them.app_agent_bindings b
		          JOIN them.applications a ON a.id = b.application_id
		          WHERE b.application_id = $1::uuid AND b.agent_id = $2::uuid AND a.tenant_id = $3::uuid`
		args = []any{appID, agentID, tenantID}
	}

	row := rt.pool.QueryRow(ctx, query, args...)
	var (
		id, appIDDB, agentIDDB string
		defID                  *string
		credJSON               []byte // selected but unused — column retained for compat
		cfgJSON, polJSON       []byte
		agentParamsJSON        []byte
	)
	if err := row.Scan(&id, &appIDDB, &agentIDDB, &defID, &credJSON, &cfgJSON, &polJSON, &agentParamsJSON); err != nil {
		return nil, nil, fmt.Errorf("load binding: %w", err)
	}
	_ = credJSON

	var overrides map[string]any
	_ = json.Unmarshal(cfgJSON, &overrides)
	var policies agentgen.InvocationPolicies
	_ = json.Unmarshal(polJSON, &policies)

	return &agentgen.AppAgentBinding{
		ID:              id,
		ApplicationID:   appIDDB,
		AgentID:         agentIDDB,
		DefinitionID:    defID,
		ConfigOverrides: overrides,
		Policies:        policies,
	}, agentParamsJSON, nil
}

// resolveAgentParams decrypts secret-type params and returns a plaintext map.
// Params absent from the stored JSON fall back to their declared default.
// Decryption failures are logged at Warn (key name only) and the param is omitted.
func (rt *Runtime) resolveAgentParams(raw []byte, decls []agentgen.AgentParamSpec) map[string]string {
	out := make(map[string]string, len(decls))
	if len(decls) == 0 {
		return out
	}

	var stored map[string]json.RawMessage
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &stored)
	}

	for _, decl := range decls {
		rawVal, exists := stored[decl.Key]
		if !exists {
			if decl.DefaultValue != "" {
				out[decl.Key] = decl.DefaultValue
			}
			continue
		}

		if decl.Type == "secret" {
			var entry agentParamEntry
			if json.Unmarshal(rawVal, &entry) == nil && entry.CT != "" {
				plain, err := crypto.DecryptStored(rt.cryptoKey, entry.CT)
				if err != nil {
					rt.logger.Warn("agent-runtime: agent param decryption failed",
						"key", decl.Key)
					continue
				}
				out[decl.Key] = plain
			}
		} else {
			var s string
			if json.Unmarshal(rawVal, &s) == nil {
				out[decl.Key] = s
			}
		}
	}
	return out
}

// buildSDKAgentCard constructs a proper a2a.AgentCard from the AgentSpec.
// It sets InputModes and OutputModes per-skill, populates SupportedInterfaces
// (the SDK v2.5 replacement for the deprecated URL field), and uses value-typed
// AgentCapabilities as required by the struct definition.
func buildSDKAgentCard(spec *agentgen.AgentSpec) *a2a.AgentCard {
	skills := make([]a2a.AgentSkill, len(spec.Skills))
	for i, sk := range spec.Skills {
		skills[i] = a2a.AgentSkill{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Tags:        sk.Tags,
			InputModes:  []string{"text/plain", "application/json"},
			OutputModes: []string{"text/plain"},
		}
	}

	agentURL := fmt.Sprintf("http://them-agent-runtime:9300/agents/%s", spec.Slug)

	return &a2a.AgentCard{
		Name:        spec.Card.Name,
		Description: spec.Card.Description,
		Version:     spec.Card.Version,
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(agentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text/plain", "application/json"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities: a2a.AgentCapabilities{
			Streaming:         spec.Card.Capabilities.Streaming,
			PushNotifications: spec.Card.Capabilities.PushNotifications,
		},
		Skills: skills,
	}
}

// writeJSONRPCError writes a JSON-RPC 2.0 error response before the SDK handler is reached
// (i.e., during our auth/spec/binding middleware phase).
func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string, httpStatus int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// multiLLMFactory routes to the correct provider implementation.
// Currently only "anthropic" is fully implemented; other providers return a clear error.
type multiLLMFactory struct {
	platformKey string
}

func (f *multiLLMFactory) NewProvider(provider, model string, maxTokens int, apiKey string) (agentgen.LLMProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured for provider %q — set a key in App Runtime", provider)
	}
	switch provider {
	case "anthropic", "":
		p := llm.NewAnthropicProvider(apiKey, model, maxTokens)
		return &anthropicProviderAdapter{p: p}, nil
	default:
		return nil, fmt.Errorf("provider %q is not yet supported in the agent runtime; only 'anthropic' is available", provider)
	}
}

// anthropicProviderAdapter adapts llm.AnthropicProvider to agentgen.LLMProvider.
// It calls Provider.Stream with a single user message and collects text deltas.
type anthropicProviderAdapter struct {
	p *llm.AnthropicProvider
}

func (a *anthropicProviderAdapter) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	msgs := []domain.Message{
		{
			Role:  domain.RoleUser,
			Parts: []domain.ContentPart{{Type: "text", Text: userPrompt}},
		},
	}
	opts := llm.Options{SystemPrompt: systemPrompt}
	ch, err := a.p.Stream(ctx, msgs, nil, opts)
	if err != nil {
		return "", fmt.Errorf("LLM stream start: %w", err)
	}
	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case "text_delta":
			sb.WriteString(ev.Delta)
		case "error":
			return "", fmt.Errorf("LLM stream error: %w", ev.Error)
		}
	}
	return sb.String(), nil
}

var _ agentgen.LLMProvider = (*anthropicProviderAdapter)(nil)
var _ agentgen.LLMFactory = (*multiLLMFactory)(nil)
